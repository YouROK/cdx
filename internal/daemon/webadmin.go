package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/web"
)

// saveProvider creates or updates a provider from the interface.
func (d *Daemon) saveProvider(edit web.ProviderEdit) error {
	if edit.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !store.ValidName(edit.Name) {
		return fmt.Errorf("name %q: use letters, digits, dot, dash or underscore", edit.Name)
	}
	if edit.Protocol == "" {
		edit.Protocol = "openai-chat"
	}
	if !contains(store.Protocols, edit.Protocol) {
		return fmt.Errorf("protocol %q is not supported", edit.Protocol)
	}

	original := edit.Original
	if original == "" {
		original = edit.Name
	}

	// A rename moves the models too, otherwise they lose their provider.
	if original != edit.Name {
		models, err := d.store.ListModels(original)
		if err != nil {
			return err
		}
		for i := range models {
			if err := d.store.DeleteModel(original, models[i].Name); err != nil {
				return err
			}
			models[i].Provider = edit.Name
			if err := d.store.PutModel(&models[i]); err != nil {
				return err
			}
		}
		if err := d.store.DeleteProvider(original); err != nil {
			return err
		}
		// The key belongs to the provider, so it moves along.
		if key, ok := d.keys.Get(original); ok {
			if err := d.keys.Set(edit.Name, key); err != nil {
				return err
			}
			_ = d.keys.Delete(original)
		}
	}

	if err := d.store.PutProvider(&store.Provider{
		Name:     edit.Name,
		Protocol: edit.Protocol,
		BaseURL:  edit.BaseURL,
	}); err != nil {
		return err
	}

	// The key is stored apart from the provider and survives edits.
	if edit.APIKey != "" {
		return d.keys.Set(edit.Name, edit.APIKey)
	}
	return nil
}

// deleteProvider removes a provider with its models and key.
func (d *Daemon) deleteProvider(name string) error {
	if err := d.store.DeleteProvider(name); err != nil {
		return err
	}
	return d.keys.Delete(name)
}

// saveModel creates or updates a model from the interface.
func (d *Daemon) saveModel(edit web.ModelEdit) error {
	if edit.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !store.ValidName(edit.Name) {
		return fmt.Errorf("name %q: use letters, digits, dot, dash or underscore", edit.Name)
	}
	if edit.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if _, found, err := d.store.GetProvider(edit.Provider); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("provider %q is not configured", edit.Provider)
	}
	if edit.Remote == "" {
		edit.Remote = edit.Name
	}

	// A model name shows up in the CLI and history, so it is unique across providers.
	if taken, err := d.modelNameExists(edit.Name, edit.Provider, edit.Original); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("model %q already exists", edit.Name)
	}

	m := &store.Model{
		Provider:      edit.Provider,
		Name:          edit.Name,
		Remote:        edit.Remote,
		Default:       edit.Default,
		ContextWindow: edit.ContextWindow,
		MaxTokens:     edit.MaxTokens,
		Temperature:   edit.Temperature,
		Reasoning: store.Reasoning{
			Levels:  edit.Reasoning,
			Default: edit.ReasoningDef,
		},
		Compat: store.Compat{
			SupportsReasoningEffort: edit.AskReasoning,
			MaxTokensField:          edit.MaxTokenField,
			SupportsTemperature:     edit.NoTemperature,
		},
	}

	// On rename the previous record is deleted.
	if edit.Original != "" && edit.Original != edit.Name {
		if err := d.store.DeleteModel(edit.Provider, edit.Original); err != nil {
			return err
		}
	}

	return d.store.PutModel(m)
}

// deleteModel removes a model.
func (d *Daemon) deleteModel(provider, name string) error {
	return d.store.DeleteModel(provider, name)
}

// testProvider checks a provider connection with a short request.
func (d *Daemon) testProvider(name string) (string, error) {
	provider, found, err := d.store.GetProvider(name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("provider %q is not configured", name)
	}

	models, err := d.store.ListModels(name)
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return "", fmt.Errorf("provider has no models to test")
	}

	// The first model is probed: every model shares the provider connection.
	client := llm.NewOpenAIChat(provider.BaseURL, d.providerKey(name), models[0].Remote)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	started := time.Now()
	resp, err := client.Chat(ctx, llm.Request{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "Reply with the single word: ok"}},
		MaxTokens: 16,
	})
	if err != nil {
		return "", err
	}

	elapsed := time.Since(started).Milliseconds()
	answer := strings.TrimSpace(resp.Content)
	if answer == "" {
		return fmt.Sprintf("empty answer, %d ms, tokens %d/%d", elapsed, resp.InputTokens, resp.OutputTokens), nil
	}
	if len(answer) > 120 {
		answer = answer[:120] + "..."
	}
	return fmt.Sprintf("%s, %d ms, tokens %d/%d", answer, elapsed, resp.InputTokens, resp.OutputTokens), nil
}

// listPlugins collects plugins, both loaded and on disk.
//
// A disabled plugin stays visible so it can be enabled again from the
// interface.
func (d *Daemon) listPlugins() ([]web.PluginView, error) {
	loaded := map[string]*plugin.Manifest{}
	if d.plugins != nil {
		for _, m := range d.plugins.List() {
			loaded[m.Name] = m
		}
	}

	onDisk := map[string]bool{}
	if entries, err := os.ReadDir(d.pluginsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				onDisk[e.Name()] = true
			}
		}
	}

	names := make([]string, 0, len(loaded)+len(onDisk))
	seen := map[string]bool{}
	for name := range loaded {
		names = append(names, name)
		seen[name] = true
	}
	for name := range onDisk {
		if !seen[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	out := make([]web.PluginView, 0, len(names))
	for _, name := range names {
		// A loaded plugin and one on disk are described the same way:
		// only the loaded and enabled flags differ.
		if m, ok := loaded[name]; ok {
			view := pluginManifestView(m)
			view.Loaded = true
			view.Enabled = true
			out = append(out, view)
			continue
		}

		view := web.PluginView{Name: name, Enabled: false}
		if m, err := plugin.Load(filepath.Join(d.pluginsDir, name)); err == nil {
			view = pluginManifestView(m)
		}
		out = append(out, view)
	}
	return out, nil
}

// pluginManifestView maps a plugin manifest to the interface view.
func pluginManifestView(m *plugin.Manifest) web.PluginView {
	tools := m.SortedToolNames()
	details := make([]web.ToolView, 0, len(m.Tools))
	for i := range m.Tools {
		spec := m.Tools[i]
		details = append(details, web.ToolView{
			Name:        spec.Name,
			Description: spec.Description,
			Permission:  spec.Permission,
		})
	}

	return web.PluginView{
		Name:         m.Name,
		Version:      m.Version,
		Description:  m.Description,
		Author:       m.Author,
		Homepage:     m.Homepage,
		Permission:   m.Permission,
		MinCDX:       m.MinCDX,
		Capabilities: m.Capabilities,
		Tools:        tools,
		ToolDetails:  details,
	}
}

// setPlugin enables or disables a plugin.
func (d *Daemon) setPlugin(name string, enabled bool) error {
	if d.plugins == nil {
		return fmt.Errorf("plugins are disabled")
	}

	if enabled {
		dir := filepath.Join(d.pluginsDir, name)
		return d.plugins.Load(context.Background(), dir)
	}
	return d.plugins.Unload(name)
}

// installPlugin downloads a plugin by URL and loads it.
//
// Both a direct manifest address and a directory work: in either case
// the entry file named by the manifest must sit next to it.
func (d *Daemon) installPlugin(rawURL string) (*plugin.Manifest, error) {
	manifest, files, err := plugin.Fetch(rawURL)
	if err != nil {
		return nil, err
	}

	// Validate before writing: a broken plugin must not touch the disk.
	if err := manifest.ValidateFiles(files); err != nil {
		return nil, err
	}

	if err := plugin.Install(d.pluginsDir, manifest.Name, files); err != nil {
		return nil, err
	}

	if d.plugins != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.plugins.Load(ctx, filepath.Join(d.pluginsDir, manifest.Name)); err != nil {
			return manifest, fmt.Errorf("installed, but failed to load: %w", err)
		}
	}
	return manifest, nil
}

// deletePlugin unloads a plugin and removes its directory.
func (d *Daemon) deletePlugin(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid plugin name %q", name)
	}

	if d.plugins != nil {
		// The plugin may not be loaded, so an unload error is ignored.
		_ = d.plugins.Unload(name)
	}

	dir := filepath.Join(d.pluginsDir, name)
	if !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(d.pluginsDir)) {
		return fmt.Errorf("plugin path escapes the plugins directory")
	}
	return os.RemoveAll(dir)
}

// listMCP collects servers for the interface.
func (d *Daemon) listMCPServers() ([]web.MCPView, error) {
	status := map[string]mcpStatus{}
	if d.mcps != nil {
		for _, s := range d.mcps.Status() {
			status[s.Name] = mcpStatus{connected: s.Connected, tools: s.Tools, failed: s.Failed}
		}
	}

	out := make([]web.MCPView, 0, len(d.cfg.MCP))
	for _, srv := range d.cfg.MCP {
		view := web.MCPView{
			Name:        srv.Name,
			Transport:   srv.Transport,
			Command:     srv.Command,
			Args:        srv.Args,
			URL:         srv.URL,
			Enabled:     srv.Enabled,
			TimeoutMS:   srv.TimeoutMS,
			Permission:  srv.Permission,
			Env:         srv.Env,
			Dir:         srv.Dir,
			AutoRestart: srv.AutoRestart,
		}
		if s, ok := status[srv.Name]; ok {
			view.Connected = s.connected
			view.Tools = s.tools
			view.Failed = s.failed
		}
		out = append(out, view)
	}
	return out, nil
}

// mcpStatus is a server state for the summary.
type mcpStatus struct {
	connected bool
	tools     int
	failed    bool
}

// saveMCP creates or updates a server in the config.
func (d *Daemon) saveMCP(edit web.MCPEdit) error {
	server := config.MCPServer{
		Name:        edit.Name,
		Transport:   edit.Transport,
		Command:     edit.Command,
		Args:        edit.Args,
		URL:         edit.URL,
		Enabled:     edit.Enabled,
		TimeoutMS:   edit.TimeoutMS,
		Permission:  edit.Permission,
		Env:         edit.Env,
		Dir:         edit.Dir,
		AutoRestart: edit.AutoRestart,
	}
	if server.Transport == "" {
		server.Transport = "stdio"
	}

	original := edit.Original
	if original == "" {
		original = edit.Name
	}

	next := *d.cfg
	next.MCP = make([]config.MCPServer, 0, len(d.cfg.MCP)+1)
	found := false
	for _, srv := range d.cfg.MCP {
		if srv.Name == original {
			next.MCP = append(next.MCP, server)
			found = true
			continue
		}
		next.MCP = append(next.MCP, srv)
	}
	if !found {
		next.MCP = append(next.MCP, server)
	}

	if err := next.Validate(); err != nil {
		return err
	}
	if err := d.persistConfig(&next); err != nil {
		return err
	}

	// Connecting runs in the background: the server starts and talks over
	// the network, and the save request must not wait. Otherwise the page
	// hangs during startup and the browser times out on a slow start.
	if d.mcps != nil {
		d.mcps.Disconnect(original)
		if server.Enabled {
			go d.connectMCPAsync(server)
		}
	}
	return nil
}

// connectMCPAsync starts a server in the background and logs a failure.
func (d *Daemon) connectMCPAsync(server config.MCPServer) {
	// Extra time for the first launch: the package may be downloading.
	timeout := time.Duration(server.TimeoutMS) * time.Millisecond
	if timeout < 2*time.Minute {
		timeout = 2 * time.Minute
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := d.mcps.Connect(ctx, server); err != nil {
		logx.Warn("[MCP:%s] Failed to connect: %v", server.Name, err)
		return
	}
	logx.Info("[MCP:%s] Connected", server.Name)
}

// deleteMCP removes a server from the config.
func (d *Daemon) deleteMCP(name string) error {
	if d.mcps != nil {
		d.mcps.Disconnect(name)
	}

	next := *d.cfg
	next.MCP = make([]config.MCPServer, 0, len(d.cfg.MCP))
	for _, srv := range d.cfg.MCP {
		if srv.Name == name {
			continue
		}
		next.MCP = append(next.MCP, srv)
	}
	if len(next.MCP) == len(d.cfg.MCP) {
		return fmt.Errorf("server %q is not configured", name)
	}
	return d.persistConfig(&next)
}

// savePerms stores permission levels.
func (d *Daemon) savePerms(view web.PermissionsView) error {
	next := *d.cfg
	next.Perms.Read = view.Read
	next.Perms.Edit = view.Edit
	next.Perms.Dangerous = view.Dangerous
	next.Perms.NonInteractive = view.NonInteractive

	if err := next.Validate(); err != nil {
		return err
	}
	if err := d.persistConfig(&next); err != nil {
		return err
	}
	d.reloadPerms()
	return nil
}

// listRules returns the dangerous rules.
func (d *Daemon) listRules() ([]web.DangerRuleView, error) {
	out := make([]web.DangerRuleView, 0, len(d.cfg.Danger))
	for _, r := range d.cfg.Danger {
		out = append(out, web.DangerRuleView{Name: r.Name, Pattern: r.Pattern, Severity: r.Severity})
	}
	return out, nil
}

// saveRules replaces the dangerous rule list.
func (d *Daemon) saveRules(rules []web.DangerRuleView) error {
	next := *d.cfg
	next.Danger = make([]config.DangerRule, 0, len(rules))
	for _, r := range rules {
		next.Danger = append(next.Danger, config.DangerRule{
			Name: r.Name, Pattern: r.Pattern, Severity: r.Severity,
		})
	}

	if err := next.Validate(); err != nil {
		return err
	}
	if err := d.persistConfig(&next); err != nil {
		return err
	}
	d.reloadPerms()
	return nil
}

// persistConfig saves the config and applies it in memory.
func (d *Daemon) persistConfig(next *config.Config) error {
	path := d.cfgPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	if err := next.Save(path); err != nil {
		return err
	}

	d.mu.Lock()
	d.cfg = next
	d.mu.Unlock()
	return nil
}

// reloadPerms rebuilds permission rules after a config change.
func (d *Daemon) reloadPerms() {
	pm, err := permFromConfig(d.cfg)
	if err != nil {
		return
	}
	d.mu.Lock()
	d.perms = pm
	d.mu.Unlock()
}

// permFromConfig builds the permission manager from the config.
func permFromConfig(cfg *config.Config) (*perm.Manager, error) {
	return perm.New(cfg)
}

// contains reports whether a value is in the list.
func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// discoverProviderModels asks a provider for its model list.
//
// Each entry is tagged as already configured, so the interface marks
// those models and refuses to add them twice.
func (d *Daemon) discoverProviderModels(name string) ([]web.DiscoveredModelView, error) {
	provider, found, err := d.store.GetProvider(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("provider %q is not configured", name)
	}

	client := llm.NewOpenAIChat(provider.BaseURL, d.providerKey(name), "")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	remote, err := client.DiscoverModels(ctx)
	if err != nil {
		return nil, err
	}

	configured, err := d.store.ListModels(name)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(configured))
	local := make(map[string]string, len(configured))
	for _, m := range configured {
		known[m.Remote] = true
		local[m.Remote] = m.Name
	}

	out := make([]web.DiscoveredModelView, 0, len(remote))
	for _, m := range remote {
		out = append(out, web.DiscoveredModelView{
			ID:            m.ID,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
			Configured:    known[m.ID],
			LocalName:     local[m.ID],
		})
	}
	return out, nil
}

// setDefaultModel marks a model as the default one.
//
// It is a separate method: the list button changes only this flag and
// must not rewrite the other fields, or it would clobber edits made in
// another tab.
func (d *Daemon) setDefaultModel(provider, name string) error {
	m, found, err := d.store.GetModel(provider, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("model %q of provider %q is not configured", name, provider)
	}
	if m.Default {
		return nil
	}
	m.Default = true
	return d.store.PutModel(m)
}
