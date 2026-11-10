package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/yourok/cdx/internal/logx"
)

// PluginInfo describes the state, location, and registered tools of a discovered plugin.
type PluginInfo struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Description   string     `json:"description"`
	Author        string     `json:"author,omitempty"`
	Homepage      string     `json:"homepage,omitempty"`
	MinCDXVersion string     `json:"min_cdx_version,omitempty"`
	SourceURL     string     `json:"source_url,omitempty"`
	Entry         string     `json:"entry"`
	Order         int        `json:"order,omitempty"`
	Dir           string     `json:"dir"`
	Scope         string     `json:"scope"`     // "user" or "system"
	Removable     bool       `json:"removable"` // true for user-installed plugins
	Enabled       bool       `json:"enabled"`
	Status        string     `json:"status"` // "running", "disabled", "error"
	LastError     string     `json:"last_error,omitempty"`
	Tools         []ToolSpec `json:"tools,omitempty"`
}

// Plugin lifecycle events published on the bus.
const (
	EventPluginEnabled   = "plugin:enabled"
	EventPluginDisabled  = "plugin:disabled"
	EventPluginRemoved   = "plugin:removed"
	EventPluginInstalled = "plugin:installed"
	EventPluginsReloaded = "plugins:reloaded"
)

// Order bands separate vital, built-in, and external plugins.
const (
	orderExternalBase    = 1000
	orderExternalDefault = 5000
)

// effectiveOrder moves user-installed plugins into the external order band.
func effectiveOrder(scope string, order int) int {
	if scope != "user" {
		return order
	}
	if order <= 0 {
		return orderExternalDefault
	}
	if order < orderExternalBase {
		return order + orderExternalBase
	}
	return order
}

// Manager discovers, installs, enables/disables, tracks, and stops plugins across plugin directories.
type Manager struct {
	mu      sync.RWMutex
	deps    RuntimeDependencies
	log     *logx.Scope
	dirs    []string
	known   map[string]PluginInfo
	plugins map[string]*Runtime
}

// NewManager creates a plugin Manager with the given core dependencies.
func NewManager(deps RuntimeDependencies) *Manager {
	if deps.Registry == nil {
		deps.Registry = NewRegistry()
	}
	if deps.Sys == nil {
		deps.Sys = NewSysAPI()
	}
	logger := deps.Logger
	if logger == nil {
		logger = logx.New()
	}
	return &Manager{
		deps:    deps,
		log:     logger.Tagged("Plugin"),
		known:   make(map[string]PluginInfo),
		plugins: make(map[string]*Runtime),
	}
}

// Registry returns the shared tool and provider registry.
func (m *Manager) Registry() *Registry {
	return m.deps.Registry
}

// publish emits a lifecycle event when the bus is available.
func (m *Manager) publish(ctx context.Context, topic string, payload any) {
	if m.deps.Bus == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.deps.Bus.Publish(ctx, topic, payload)
}

// pluginEventPayload renders plugin info as a plain map for bus subscribers.
func pluginEventPayload(info PluginInfo) map[string]any {
	return map[string]any{
		"id":          info.ID,
		"name":        info.Name,
		"version":     info.Version,
		"description": info.Description,
		"source_url":  info.SourceURL,
		"scope":       info.Scope,
		"order":       info.Order,
		"dir":         info.Dir,
		"enabled":     info.Enabled,
		"status":      info.Status,
	}
}

// orderFor returns the effective order for a plugin directory. The shift only
// applies when the user and system directories are separate.
func (m *Manager) orderFor(dir string, order int) int {
	m.mu.RLock()
	separate := len(m.dirs) > 1
	m.mu.RUnlock()
	if !separate {
		return order
	}
	return effectiveOrder(m.scopeForDir(dir), order)
}

// UserPluginDir returns the primary user plugin installation directory.
func (m *Manager) UserPluginDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.dirs) > 0 {
		return m.dirs[0]
	}
	return ""
}

// LoadDirs scans directories in order and loads every enabled plugin.
// Earlier directories take precedence when two directories contain the same plugin ID.
func (m *Manager) LoadDirs(ctx context.Context, dirs []string) error {
	m.mu.Lock()
	m.dirs = append([]string(nil), dirs...)
	m.deps.Plugins = m
	m.mu.Unlock()

	type candidate struct {
		dir      string
		scope    string
		manifest *Manifest
	}

	disabledMap := m.loadDisabledMap()
	seen := map[string]bool{}
	var list []candidate

	for idx, root := range dirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			m.log.Warn("Failed to read plugin directory %s: %v", root, err)
			continue
		}

		scope := "user"
		if idx > 0 {
			scope = "system"
		}

		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			plugDir := filepath.Join(root, e.Name())
			manifest, err := LoadManifest(plugDir)
			if err != nil {
				m.log.Warn("Skipped invalid plugin in %s: %v", plugDir, err)
				brokenID := filepath.Base(plugDir)
				if !seen[brokenID] {
					seen[brokenID] = true
					m.recordBroken(plugDir, scope, brokenID, err)
				}
				continue
			}
			if seen[manifest.ID] {
				continue
			}
			seen[manifest.ID] = true
			manifest.Order = m.orderFor(plugDir, manifest.Order)
			list = append(list, candidate{dir: plugDir, scope: scope, manifest: manifest})
		}
	}

	sort.SliceStable(list, func(i, j int) bool {
		if list[i].manifest.Order != list[j].manifest.Order {
			return list[i].manifest.Order < list[j].manifest.Order
		}
		return list[i].manifest.ID < list[j].manifest.ID
	})

	for _, c := range list {
		isDisabled := disabledMap[c.manifest.ID]
		m.recordKnown(c.dir, c.scope, c.manifest, !isDisabled, "")
		if isDisabled {
			m.log.Info("Skipped disabled plugin %s", c.manifest.ID)
			continue
		}
		if _, err := m.LoadPlugin(ctx, c.dir); err != nil {
			m.log.Error("Failed to start plugin %s: %v", c.manifest.ID, err)
			m.recordKnown(c.dir, c.scope, c.manifest, false, err.Error())
		}
	}
	return nil
}

// LoadPlugin loads (or reloads) a single plugin from dir.
func (m *Manager) LoadPlugin(ctx context.Context, dir string) (*Runtime, error) {
	manifest, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	manifest.Order = m.orderFor(dir, manifest.Order)

	m.mu.Lock()
	m.deps.Plugins = m
	if existing, ok := m.plugins[manifest.ID]; ok {
		existing.Close()
		delete(m.plugins, manifest.ID)
	}
	m.mu.Unlock()

	rt, err := StartRuntime(ctx, dir, manifest, m.deps)
	if err != nil {
		scope := m.scopeForDir(dir)
		m.recordKnown(dir, scope, manifest, false, err.Error())
		return nil, err
	}

	scope := m.scopeForDir(dir)
	m.recordKnown(dir, scope, manifest, true, "")

	m.mu.Lock()
	m.plugins[manifest.ID] = rt
	m.mu.Unlock()

	m.log.Info("Loaded plugin %s v%s", manifest.ID, manifest.Version)
	return rt, nil
}

// SetEnabled enables or disables a plugin by ID and persists the choice in BoltDB.
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) (*PluginInfo, error) {
	m.mu.RLock()
	info, exists := m.known[id]
	m.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("plugin %q is not found", id)
	}

	if err := m.setDisabledState(id, !enabled); err != nil {
		return nil, err
	}

	if enabled {
		if _, err := m.LoadPlugin(ctx, info.Dir); err != nil {
			return nil, err
		}
		updated, _ := m.GetPluginInfo(id)
		m.publish(ctx, EventPluginEnabled, pluginEventPayload(updated))
		return &updated, nil
	}

	wasRunning := m.UnloadPlugin(id)
	info.Enabled = false
	info.Status = "disabled"
	m.mu.Lock()
	if k, ok := m.known[id]; ok {
		k.Enabled = false
		m.known[id] = k
	}
	m.mu.Unlock()
	if wasRunning {
		m.publish(ctx, EventPluginDisabled, pluginEventPayload(info))
	}

	updated, _ := m.GetPluginInfo(id)
	return &updated, nil
}

// RemovePlugin stops and deletes a user-installed plugin from disk.
func (m *Manager) RemovePlugin(ctx context.Context, id string) error {
	m.mu.RLock()
	info, exists := m.known[id]
	m.mu.RUnlock()

	if !exists {
		// Fallback: check if a broken folder with this name exists in the user plugin directory.
		userDir := m.UserPluginDir()
		if userDir != "" {
			candidate := filepath.Join(userDir, filepath.Base(id))
			if st, err := os.Stat(candidate); err == nil && st.IsDir() {
				if err := os.RemoveAll(candidate); err != nil {
					return fmt.Errorf("remove broken plugin directory: %w", err)
				}
				m.publish(ctx, EventPluginRemoved, map[string]any{"id": id, "dir": candidate, "scope": "user"})
				m.log.Info("Removed broken plugin folder %s", candidate)
				return nil
			}
		}
		return fmt.Errorf("plugin %q is not found", id)
	}
	if !info.Removable {
		return fmt.Errorf("system plugin %q cannot be deleted (disable it instead)", id)
	}

	if m.UnloadPlugin(id) {
		info.Enabled = false
		info.Status = "disabled"
		m.publish(ctx, EventPluginDisabled, pluginEventPayload(info))
	}
	_ = m.setDisabledState(id, false)

	if err := os.RemoveAll(info.Dir); err != nil {
		return fmt.Errorf("remove plugin files: %w", err)
	}

	m.mu.Lock()
	delete(m.known, id)
	m.mu.Unlock()

	m.publish(ctx, EventPluginRemoved, map[string]any{"id": info.ID, "dir": info.Dir, "scope": info.Scope})
	m.log.Info("Removed plugin %s from %s", id, info.Dir)
	return nil
}

// Reload restarts every plugin from the configured directories.
func (m *Manager) Reload(ctx context.Context) error {
	m.mu.RLock()
	dirs := append([]string(nil), m.dirs...)
	m.mu.RUnlock()

	m.CloseAll()
	if err := m.LoadDirs(ctx, dirs); err != nil {
		return err
	}

	m.mu.RLock()
	count := len(m.plugins)
	m.mu.RUnlock()
	m.publish(ctx, EventPluginsReloaded, map[string]any{"count": count})
	return nil
}

// UnloadPlugin stops a running plugin VM without removing it from the known list.
func (m *Manager) UnloadPlugin(id string) bool {
	m.mu.Lock()
	rt, ok := m.plugins[id]
	if ok {
		delete(m.plugins, id)
	}
	m.mu.Unlock()

	if ok {
		rt.Close()
		m.log.Info("Unloaded plugin %s", id)
	}
	return ok
}

// SettingsSnapshot returns the registered settings schema and current values for a plugin.
func (m *Manager) SettingsSnapshot(id string) (map[string]any, map[string]any, map[string]bool, bool) {
	m.mu.RLock()
	rt := m.plugins[id]
	m.mu.RUnlock()
	if rt == nil {
		return nil, nil, nil, false
	}
	return rt.SettingsSnapshot()
}

// SaveSettings validates and persists declarative settings for a plugin.
func (m *Manager) SaveSettings(id string, values map[string]any) (map[string]any, error) {
	m.mu.RLock()
	rt := m.plugins[id]
	m.mu.RUnlock()
	if rt == nil {
		return nil, fmt.Errorf("plugin %q is not running", id)
	}
	return rt.SaveSettings(values)
}

// GetPluginInfo returns detailed status for a single plugin.
func (m *Manager) GetPluginInfo(id string) (PluginInfo, bool) {
	all := m.ListDetailed()
	for _, p := range all {
		if p.ID == id {
			return p, true
		}
	}
	return PluginInfo{}, false
}

// ListDetailed returns full information (including enabled state, scope, removable flag, and tools) for all discovered plugins.
func (m *Manager) ListDetailed() []PluginInfo {
	m.mu.RLock()
	out := make([]PluginInfo, 0, len(m.known))
	for _, info := range m.known {
		_, running := m.plugins[info.ID]
		info.Enabled = running
		if running {
			info.Status = "running"
		} else if info.LastError != "" {
			info.Status = "error"
		} else {
			info.Status = "disabled"
		}
		out = append(out, info)
	}
	m.mu.RUnlock()

	var allTools []ToolSpec
	if m.deps.Registry != nil {
		allTools = m.deps.Registry.ListTools()
	}

	for i := range out {
		var pTools []ToolSpec
		for _, t := range allTools {
			if t.PluginID == out[i].ID {
				pTools = append(pTools, t)
			}
		}
		out[i].Tools = pTools
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// List returns manifests of all currently active plugins sorted by ID.
func (m *Manager) List() []Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Manifest, 0, len(m.plugins))
	for _, rt := range m.plugins {
		out = append(out, *rt.Manifest())
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// CloseAll stops all running plugins.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := make([]*Runtime, 0, len(m.plugins))
	for _, rt := range m.plugins {
		all = append(all, rt)
	}
	m.plugins = make(map[string]*Runtime)
	m.mu.Unlock()

	for _, rt := range all {
		rt.Close()
	}
}

func (m *Manager) scopeForDir(dir string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.dirs) > 0 && strings.HasPrefix(filepath.Clean(dir), filepath.Clean(m.dirs[0])) {
		return "user"
	}
	if len(m.dirs) > 1 {
		return "system"
	}
	return "user"
}

func (m *Manager) recordKnown(dir, scope string, manifest *Manifest, enabled bool, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := "disabled"
	if enabled {
		status = "running"
	} else if lastErr != "" {
		status = "error"
	}
	m.known[manifest.ID] = PluginInfo{
		ID:            manifest.ID,
		Name:          manifest.Name,
		Version:       manifest.Version,
		Description:   manifest.Description,
		Author:        manifest.Author,
		Homepage:      manifest.Homepage,
		MinCDXVersion: manifest.MinCDXVersion,
		SourceURL:     manifest.SourceURL,
		Entry:         manifest.Entry,
		Order:         manifest.Order,
		Dir:           dir,
		Scope:         scope,
		Removable:     scope == "user",
		Enabled:       enabled,
		Status:        status,
		LastError:     lastErr,
	}
}

func (m *Manager) recordBroken(dir, scope, id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.known[id] = PluginInfo{
		ID:          id,
		Name:        id,
		Version:     "0.0.0",
		Description: "Invalid plugin directory",
		Dir:         dir,
		Scope:       scope,
		Removable:   scope == "user",
		Enabled:     false,
		Status:      "error",
		LastError:   err.Error(),
	}
}

func (m *Manager) loadDisabledMap() map[string]bool {
	out := map[string]bool{}
	if m.deps.Store == nil {
		return out
	}
	_, _ = m.deps.Store.GetState("disabled_plugins", &out)
	return out
}

func (m *Manager) setDisabledState(id string, disabled bool) error {
	if m.deps.Store == nil {
		return nil
	}
	cur := m.loadDisabledMap()
	if disabled {
		cur[id] = true
	} else {
		delete(cur, id)
	}
	return m.deps.Store.PutState("disabled_plugins", cur)
}
