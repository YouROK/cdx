package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/pluginapi"
)

// initBus builds the bus and wires the extensions.
//
// Nothing is registered on the bus: the core ships no tools.
func (d *Daemon) initBus() {
	d.bus = bus.New()

	d.plugins = plugin.NewManager(plugin.Options{
		Dir:      d.pluginsDir,
		Bus:      d.bus,
		Registry: d.reg,
		CallBridge: func(ctx context.Context, from, topic string, payload any) (any, error) {
			return d.bus.Request(ctx, from, topic, payload)
		},
		NewAPI: func(dir string) *pluginapi.API {
			return d.newPluginAPI(dir)
		},
	})

	d.mcps = mcp.NewManager(d.cfg, d.reg)
}

// startExtensions loads plugins and connects MCP servers.
func (d *Daemon) startExtensions(ctx context.Context) {
	if d.cfg.Plugins.Enabled && d.plugins != nil {
		enabled := map[string]bool{}
		for _, item := range d.cfg.Plugins.Items {
			enabled[item.Name] = item.Enabled
		}
		if err := d.plugins.LoadAll(ctx, enabled); err != nil {
			logx.Warn("[Daemon] Failed to load plugins: %v", err)
		}
	}

	if d.mcps != nil {
		d.mcps.ConnectAll(ctx)
	}
}

// stopExtensions unloads plugins and disconnects MCP.
func (d *Daemon) stopExtensions() {
	if d.plugins != nil {
		d.plugins.UnloadAll()
	}
	if d.mcps != nil {
		d.mcps.DisconnectAll()
	}
}

// handlePlugin runs plugin control methods.
func (d *Daemon) handlePlugin(method string, payload any) (any, error) {
	switch method {
	case "plugins.list":
		list := []map[string]any{}
		if d.plugins != nil {
			for _, m := range d.plugins.List() {
				list = append(list, map[string]any{
					"name":         m.Name,
					"version":      m.Version,
					"description":  m.Description,
					"author":       m.Author,
					"capabilities": m.Capabilities,
					"tools":        m.SortedToolNames(),
					"loaded":       true,
				})
			}
		}
		return map[string]any{"plugins": list}, nil

	case "plugins.unload":
		name, _ := payload.(string)
		if d.plugins == nil {
			return nil, fmt.Errorf("plugins are disabled")
		}
		return map[string]any{"unloaded": name}, d.plugins.Unload(name)
	}
	return nil, fmt.Errorf("unknown plugin method %q", method)
}

// handleMCP runs MCP server control methods.
func (d *Daemon) handleMCP(method string, payload any) (any, error) {
	switch method {
	case "mcp.list":
		if d.mcps == nil {
			return map[string]any{"servers": []mcp.ServerStatus{}}, nil
		}
		return map[string]any{"servers": d.mcps.Status()}, nil

	case "mcp.restart":
		name, _ := payload.(string)
		if d.mcps == nil {
			return nil, fmt.Errorf("mcp is disabled")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return map[string]any{"restarted": name}, d.mcps.Restart(ctx, name)
	}
	return nil, fmt.Errorf("unknown mcp method %q", method)
}

// newPluginAPI builds the core capabilities for a plugin.
//
// Permissions are shared: a plugin never gets more than the user allows.
// Paths are rooted at the turn working directory, so a plugin works on
// the user's project rather than its own install directory.
func (d *Daemon) newPluginAPI(dir string) *pluginapi.API {
	api := pluginapi.New(pluginapi.Options{
		Limits: pluginapi.Limits{
			MaxReadBytes:   d.cfg.Files.MaxInlineSizeKB << 10,
			MaxOutputBytes: d.cfg.Agent.ToolResultMaxBytes,
			TimeoutMS:      d.cfg.Agent.TurnTimeoutMS,
		},
		Perms: d.perms,
		Root:  dir,
		// The root is read at call time: a plugin loads at daemon start
		// but works in the directory of the current turn.
		RootFn: func() string { return d.turnRoot("") },
	})
	api.SetAsker(d.pluginAsker())
	return api
}

// pluginAsker sends permission asks to the active turn.
//
// Plugin operations run inside an agent turn, so the ask goes to the
// connection that started that turn. Without one, the headless rule
// applies.
func (d *Daemon) pluginAsker() pluginapi.Asker {
	return func(level, tool, summary string) (string, error) {
		d.mu.Lock()
		active := d.activeSession
		d.mu.Unlock()

		if active != nil {
			ask := d.makeAsk(active)
			return ask(context.Background(), level, tool, summary, perm.Options(false))
		}

		// Nobody to ask: the headless setting decides.
		if d.perms != nil && d.perms.NonInteractive() == "allow" {
			return perm.ChoiceOnce, nil
		}
		return perm.ChoiceDeny, nil
	}
}

// turnRoot returns the path root for a plugin.
//
// It is the working directory of the running turn, since the model calls
// the plugin from the user's project. Without a turn the plugin directory
// stays, so paths never become unbounded.
func (d *Daemon) turnRoot(fallback string) string {
	d.mu.Lock()
	cwd := d.turnCWD
	d.mu.Unlock()

	if cwd != "" {
		return cwd
	}
	return fallback
}
