package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/pluginapi"
	"github.com/yourok/cdx/internal/registry"
)

// Manager loads plugins and keeps their runtimes.
type Manager struct {
	dir string
	bus *bus.Bus
	reg *registry.Registry

	mu       sync.RWMutex
	runtimes map[string]*Runtime
	order    []string

	// bridge makes bus calls on behalf of a plugin.
	bridge func(ctx context.Context, from, topic string, payload any) (any, error)
	newAPI func(dir string) *pluginapi.API
}

// Options configures the manager.
type Options struct {
	Dir        string
	Bus        *bus.Bus
	Registry   *registry.Registry
	CallBridge func(ctx context.Context, from, topic string, payload any) (any, error)
	// NewAPI builds the core capabilities for one plugin.
	//
	// The returned API anchors paths to a root, so a plugin cannot reach
	// files outside the directory it was given.
	NewAPI func(dir string) *pluginapi.API
}

// NewManager creates a manager.
func NewManager(opts Options) *Manager {
	return &Manager{
		dir:      opts.Dir,
		bus:      opts.Bus,
		newAPI:   opts.NewAPI,
		reg:      opts.Registry,
		runtimes: map[string]*Runtime{},
		bridge:   opts.CallBridge,
	}
}

// LoadAll loads every plugin in the directory.
//
// A failing plugin only logs: the rest must still load.
func (m *Manager) LoadAll(ctx context.Context, enabled map[string]bool) error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugins dir: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if enabled != nil {
			if on, known := enabled[name]; known && !on {
				continue
			}
		}
		if err := m.Load(ctx, filepath.Join(m.dir, name)); err != nil {
			logx.Warn("[Plugin:%s] Failed to load: %v", name, err)
		}
	}
	return nil
}

// Load loads a single plugin from its directory.
func (m *Manager) Load(ctx context.Context, dir string) error {
	manifest, err := Load(dir)
	if err != nil {
		return err
	}

	m.mu.RLock()
	_, exists := m.runtimes[manifest.Name]
	m.mu.RUnlock()
	if exists {
		return fmt.Errorf("plugin %q is already loaded", manifest.Name)
	}

	var api *pluginapi.API
	if m.newAPI != nil {
		api = m.newAPI(dir)
	}

	rt, err := Start(ctx, dir, manifest, RuntimeOptions{
		Bus: m.bus,
		API: api,
		CallBridge: func(ctx context.Context, topic string, payload any) (any, error) {
			if m.bridge == nil {
				return nil, fmt.Errorf("bus bridge is not configured")
			}
			return m.bridge(ctx, "plugin:"+manifest.Name, topic, payload)
		},
		PublishBridge: func(topic string, payload any) error {
			m.bus.Publish(context.Background(), topic, payload)
			return nil
		},
		Log: func(level, message string) {
			switch level {
			case "error":
				logx.Error("[Plugin:%s] %s", manifest.Name, message)
			case "warn":
				logx.Warn("[Plugin:%s] %s", manifest.Name, message)
			default:
				logx.Info("[Plugin:%s] %s", manifest.Name, message)
			}
		},
	})
	if err != nil {
		return err
	}

	if err := m.registerTools(rt); err != nil {
		rt.Close(0)
		return err
	}

	m.mu.Lock()
	m.runtimes[manifest.Name] = rt
	m.order = append(m.order, manifest.Name)
	m.mu.Unlock()

	logx.Info("[Plugin:%s] Loaded version %s with %d tools",
		manifest.Name, manifest.Version, len(manifest.Tools))
	return nil
}

// registerTools adds the plugin tools to the agent registry.
func (m *Manager) registerTools(rt *Runtime) error {
	if m.reg == nil {
		return nil
	}

	manifest := rt.manifest
	for i := range manifest.Tools {
		spec := manifest.Tools[i]

		llmName := shortName(manifest.Name, spec.Name)
		tool := &registry.Tool{
			Spec: registry.Spec{
				ID:          manifest.ToolID(spec.Name),
				LLMName:     llmName,
				Description: spec.Description,
				Level:       manifest.LevelFor(&spec),
				Schema:      spec.InputSchema,
			},
			Run: m.toolRunner(manifest, spec.Name),
		}
		if err := m.reg.Add(tool); err != nil {
			return fmt.Errorf("register tool %s: %w", spec.Name, err)
		}
	}
	return nil
}

// toolRunner builds the handler that calls a tool over the bus.
func (m *Manager) toolRunner(manifest *Manifest, toolName string) registry.Handler {
	topic := manifest.ToolID(toolName)

	return func(ctx context.Context, args map[string]any) (registry.Result, error) {
		// Engine fields are dropped so they never reach the plugin.
		clean := make(map[string]any, len(args))
		for k, v := range args {
			if strings.HasPrefix(k, "__") {
				continue
			}
			clean[k] = v
		}

		value, err := m.bus.Request(ctx, "", topic, clean)
		if err != nil {
			return registry.Result{Content: fmt.Sprintf("error: %v", err)}, nil
		}

		content := asText(value)
		return registry.Result{Content: content, OK: true}, nil
	}
}

// asText turns a plugin reply into text.
func asText(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case error:
		return "error: " + value.Error()
	}

	if obj, ok := v.(map[string]any); ok {
		if text, ok := obj["content"].(string); ok {
			return text
		}
	}
	return fmt.Sprint(v)
}

// shortName builds the tool name the model sees.
//
// Model API names allow only latin letters, digits and underscores, so dots
// and dashes of the plugin name are replaced.
func shortName(pluginName, toolName string) string {
	safe := strings.NewReplacer(".", "_", "-", "_").Replace(pluginName)
	name := "p_" + safe + "_" + toolName
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// Unload stops a plugin and removes its tools.
func (m *Manager) Unload(name string) error {
	m.mu.Lock()
	rt, ok := m.runtimes[name]
	if ok {
		delete(m.runtimes, name)
		for i, item := range m.order {
			if item == name {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("plugin %q is not loaded", name)
	}

	// Tools are removed here, otherwise the model would be offered a
	// tool whose handler is already gone.
	if m.reg != nil {
		m.reg.RemovePrefix("plugin." + name + ".")
	}

	timeout := time.Duration(rt.manifest.ShutdownTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	rt.Close(timeout)
	return nil
}

// UnloadAll stops every plugin.
func (m *Manager) UnloadAll() {
	m.mu.Lock()
	names := append([]string(nil), m.order...)
	m.mu.Unlock()

	for _, name := range names {
		_ = m.Unload(name)
	}
}

// List returns the loaded plugins.
func (m *Manager) List() []*Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Manifest, 0, len(m.order))
	for _, name := range m.order {
		if rt, ok := m.runtimes[name]; ok {
			out = append(out, rt.manifest)
		}
	}
	return out
}

// Names returns the names of loaded plugins.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := append([]string(nil), m.order...)
	sort.Strings(out)
	return out
}

// Get returns the runtime of a loaded plugin.
func (m *Manager) Get(name string) (*Runtime, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rt, ok := m.runtimes[name]
	return rt, ok
}
