package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/i18n"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/sessions"
	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/web"
)

// startWeb brings up the web interface and API.
func (d *Daemon) startWeb() {
	srv0 := web.New(d.cfg, d.webDeps())
	handler := srv0.Handler()

	addr := fmt.Sprintf("%s:%d", d.cfg.Web.Addr, d.cfg.Web.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// The reason is kept so the client shows it instead of a vague
		// "interface is off".
		logx.Error("[Web] Failed to listen on %s: %v", addr, err)
		d.mu.Lock()
		d.webError = fmt.Sprintf("cannot listen on %s: %v", addr, err)
		d.mu.Unlock()
		return
	}

	// Port 0 means any free one: the client needs the actual number.
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		d.cfg.Web.Port = tcp.Port
	}
	// The URL carries the token, otherwise the interface cannot call the API.
	d.mu.Lock()
	d.webURL = srv0.URL()
	d.mu.Unlock()

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	d.mu.Lock()
	d.webSrv = srv
	d.mu.Unlock()

	logx.Info("[Web] Listening on %s", ln.Addr())

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logx.Error("[Web] Server stopped: %v", err)
	}
}

// WebURL returns the interface URL including the token.
func (d *Daemon) WebURL() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.webURL
}

// webFailure returns why the web interface failed to start.
func (d *Daemon) webFailure() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.webError
}

// webDeps assembles the web server dependencies.
func (d *Daemon) webDeps() web.Deps {
	return web.Deps{
		Status: func() web.StatusView {
			return web.StatusView{
				State:   d.State(),
				Version: versionString(),
				Proto:   protoVersion(),
				PID:     pidOf(),
				Uptime:  d.Uptime(),
			}
		},
		Config: func() any { return configView(d.cfg) },
		SaveCfg: func(body map[string]any) (bool, error) {
			return d.saveConfig(body)
		},
		Providers: func() ([]web.ProviderView, error) {
			providers, err := d.store.ListProviders()
			if err != nil {
				return nil, err
			}
			out := make([]web.ProviderView, 0, len(providers))
			for _, p := range providers {
				models, err := d.store.ListModels(p.Name)
				if err != nil {
					return nil, err
				}
				views := make([]web.ModelView, 0, len(models))
				for _, m := range models {
					views = append(views, web.NewModelView(m))
				}
				out = append(out, web.NewProviderView(p, d.keys.Has(p.Name), views))
			}
			return out, nil
		},
		Sessions: func() ([]web.SessionView, error) {
			list, err := d.store.List(store.BucketSessions)
			if err != nil {
				return nil, err
			}
			out := make([]web.SessionView, 0, len(list))
			for _, id := range list {
				s, found, err := d.sess.Get(id)
				if err != nil || !found || s.Kind == sessions.KindSub {
					continue
				}
				out = append(out, toWebSession(s))
			}
			return out, nil
		},
		Session: func(id string) (web.SessionView, []web.MessageView, error) {
			s, found, err := d.sess.Get(id)
			if err != nil {
				return web.SessionView{}, nil, err
			}
			if !found {
				return web.SessionView{}, nil, fmt.Errorf("session %q not found", id)
			}
			msgs, err := d.sess.Messages(id)
			if err != nil {
				return web.SessionView{}, nil, err
			}
			return toWebSession(s), toWebMessages(msgs), nil
		},
		Logs: func(lines int) ([]string, error) {
			dir, err := d.cfg.LogPath()
			if err != nil {
				return nil, err
			}
			return web.TailLogs(dir, lines)
		},
		Perms: func() web.PermissionsView {
			return web.PermissionsView{
				Read:           d.cfg.Perms.Read,
				Edit:           d.cfg.Perms.Edit,
				Dangerous:      d.cfg.Perms.Dangerous,
				NonInteractive: d.cfg.Perms.NonInteractive,
			}
		},
		Language: i18n.Language,

		SaveProvider:   d.saveProvider,
		DeleteProvider: d.deleteProvider,
		SetProviderKey: d.keys.Set,
		TestProvider:   d.testProvider,
		DiscoverModels: d.discoverProviderModels,

		SaveModel:       d.saveModel,
		DeleteModel:     d.deleteModel,
		SetDefaultModel: d.setDefaultModel,

		Running:       d.listRunning,
		CancelRunning: d.cancelRunning,

		Presets: func() []web.PresetView {
			list := d.listPresets()
			// The active preset is flagged here: the interface needs one
			// marker instead of diffing the config on the client.
			for i := range list {
				list[i].Active = list[i].ID == d.cfg.AgentPreset.Active
			}
			return list
		},
		SavePreset:      d.savePreset,
		DeletePreset:    d.deletePreset,
		SetActivePreset: d.setActivePreset,

		Plugins:      d.listPlugins,
		SetPlugin:    d.setPlugin,
		DeletePlugin: d.deletePlugin,
		InstallPlugin: func(url string) (web.PluginView, error) {
			m, err := d.installPlugin(url)
			if err != nil {
				return web.PluginView{}, err
			}
			view := pluginManifestView(m)
			view.Loaded = true
			view.Enabled = true
			return view, nil
		},

		MCPServers: d.listMCPServers,
		SaveMCP:    d.saveMCP,
		DeleteMCP:  d.deleteMCP,
		RestartMCP: func(name string) error {
			if d.mcps == nil {
				return fmt.Errorf("mcp is disabled")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return d.mcps.Restart(ctx, name)
		},

		SavePerms:   d.savePerms,
		DangerRules: d.listRules,
		SaveRules:   d.saveRules,
	}
}

// configView prepares the editable part of the config.
func configView(cfg *config.Config) map[string]any {
	return map[string]any{
		"agent": map[string]any{
			"model":           cfg.Agent.Model,
			"max_iterations":  cfg.Agent.MaxIterations,
			"turn_timeout_ms": cfg.Agent.TurnTimeoutMS,
		},
		"context": map[string]any{
			"strategy":        cfg.Context.Strategy,
			"recent_messages": cfg.Context.RecentMessages,
		},
		"permissions": map[string]any{
			"read":            cfg.Perms.Read,
			"edit":            cfg.Perms.Edit,
			"dangerous":       cfg.Perms.Dangerous,
			"non_interactive": cfg.Perms.NonInteractive,
		},
		"output": map[string]any{
			"color":    cfg.Output.Color,
			"language": cfg.Output.Language,
			"bell":     cfg.Output.Bell,
		},
	}
}

// saveConfig applies interface edits and writes the file.
func (d *Daemon) saveConfig(body map[string]any) (bool, error) {
	next := *d.cfg

	if agentCfg, ok := body["agent"].(map[string]any); ok {
		if v, ok := asString(agentCfg["model"]); ok {
			next.Agent.Model = v
		}
		if v, ok := asInt(agentCfg["max_iterations"]); ok {
			next.Agent.MaxIterations = v
		}
		if v, ok := asInt(agentCfg["turn_timeout_ms"]); ok {
			next.Agent.TurnTimeoutMS = v
		}
	}

	if ctxCfg, ok := body["context"].(map[string]any); ok {
		if v, ok := asString(ctxCfg["strategy"]); ok {
			next.Context.Strategy = v
		}
		if v, ok := asInt(ctxCfg["recent_messages"]); ok {
			next.Context.RecentMessages = v
		}
	}

	if permCfg, ok := body["permissions"].(map[string]any); ok {
		if v, ok := asString(permCfg["read"]); ok {
			next.Perms.Read = v
		}
		if v, ok := asString(permCfg["edit"]); ok {
			next.Perms.Edit = v
		}
		if v, ok := asString(permCfg["dangerous"]); ok {
			next.Perms.Dangerous = v
		}
		if v, ok := asString(permCfg["non_interactive"]); ok {
			next.Perms.NonInteractive = v
		}
	}

	if outCfg, ok := body["output"].(map[string]any); ok {
		if v, ok := asString(outCfg["color"]); ok {
			next.Output.Color = v
		}
		if v, ok := asString(outCfg["language"]); ok {
			next.Output.Language = v
		}
		if v, ok := outCfg["bell"].(bool); ok {
			next.Output.Bell = v
		}
	}

	if err := next.Validate(); err != nil {
		return false, err
	}

	path := d.cfgPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return false, err
		}
		path = p
	}
	if err := next.Save(path); err != nil {
		return false, err
	}

	// Applied settings take effect at once, except those needing a restart.
	needRestart := next.Agent.Model != d.cfg.Agent.Model && next.Agent.Model != ""
	*d.cfg = next

	// Permissions are rebuilt from the new values and need no restart.
	if pm, err := perm.New(d.cfg); err == nil {
		d.perms = pm
	}

	// The interface language changes at once: it is display only.
	if next.Output.Language != "auto" {
		i18n.SetLanguage(next.Output.Language)
	}
	return needRestart, nil
}

// toWebSession converts a session for the interface.
func toWebSession(s *sessions.Session) web.SessionView {
	return web.SessionView{
		ID:        s.ID,
		Root:      s.Root,
		Title:     s.Title,
		Model:     s.Model,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Messages:  s.LastSeq,
		Active:    false,
	}
}

// toWebMessages converts history for the interface.
func toWebMessages(msgs []sessions.Message) []web.MessageView {
	out := make([]web.MessageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, web.MessageView{
			Seq:        m.Seq,
			Kind:       m.Kind,
			Content:    m.Content,
			ToolName:   m.ToolName,
			Model:      m.Model,
			TokensIn:   m.TokensIn,
			TokensOut:  m.TokensOut,
			CoversFrom: m.CoversFrom,
			CoversTo:   m.CoversTo,
			TS:         m.TS,
		})
	}
	return out
}

// asString reads a string from decoded JSON.
func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// asInt reads an integer from decoded JSON.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}
