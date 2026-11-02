package plugin

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/yourok/cdx/internal/schema"
)

// ToolSpec describes a tool exposed to the LLM.
type ToolSpec struct {
	PluginID    string         `json:"plugin_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Permission  string         `json:"permission,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
	parsed      *schema.Schema
	handler     func(ctx context.Context, args map[string]any) (any, error)
}

// ModelInfo describes a model exposed by a provider plugin.
type ModelInfo struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Provider      string         `json:"provider"`
	PluginID      string         `json:"plugin_id"`
	Default       bool           `json:"default,omitempty"`
	ContextWindow int            `json:"context_window,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

// StreamEvent represents a streaming chunk emitted by a provider plugin during completion.
type StreamEvent struct {
	Type     string         `json:"type"` // "text", "reasoning", "tool_call", "usage"
	Delta    string         `json:"delta,omitempty"`
	ToolCall map[string]any `json:"tool_call,omitempty"`
	Usage    map[string]any `json:"usage,omitempty"`
}

// CompleteRequest contains the conversation payload passed to a provider plugin.
type CompleteRequest struct {
	Model        string           `json:"model"`
	SystemPrompt string           `json:"system_prompt,omitempty"`
	Messages     []map[string]any `json:"messages"`
	Tools        []map[string]any `json:"tools,omitempty"`
	Options      map[string]any   `json:"options,omitempty"`
}

// CompleteResponse is the final turn response returned by a provider plugin.
type CompleteResponse struct {
	Content   string           `json:"content"`
	Reasoning string           `json:"reasoning,omitempty"`
	Model     string           `json:"model,omitempty"`
	Provider  string           `json:"provider,omitempty"`
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
	Usage     map[string]any   `json:"usage,omitempty"`
}

// ProviderEntry holds callbacks registered by a provider plugin.
type ProviderEntry struct {
	PluginID   string
	ListModels func(ctx context.Context) ([]ModelInfo, error)
	Complete   func(ctx context.Context, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error)
}

// Registry tracks tools and LLM providers registered by loaded plugins.
type Registry struct {
	mu        sync.RWMutex
	tools     map[string]ToolSpec
	providers map[string]ProviderEntry
}

// NewRegistry creates an empty tool and provider registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]ToolSpec),
		providers: make(map[string]ProviderEntry),
	}
}

// RegisterTool adds or updates a tool definition.
func (r *Registry) RegisterTool(spec ToolSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	if spec.InputSchema == nil {
		spec.InputSchema = map[string]any{"type": "object"}
	}
	parsed, err := schema.Parse(spec.InputSchema)
	if err != nil {
		return fmt.Errorf("tool %q schema: %w", spec.Name, err)
	}
	spec.parsed = parsed

	r.mu.Lock()
	r.tools[spec.Name] = spec
	r.mu.Unlock()
	return nil
}

// ListTools returns all registered tools in alphabetical order.
func (r *Registry) ListTools() []ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// GetTool looks up a tool by name.
func (r *Registry) GetTool(name string) (ToolSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// CallTool validates input arguments against the tool's JSON Schema and executes its handler.
func (r *Registry) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	spec, ok := r.GetTool(name)
	if !ok {
		return nil, fmt.Errorf("tool %q is not registered", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	validated, err := spec.parsed.Validate(args)
	if err != nil {
		return nil, fmt.Errorf("tool %q invalid arguments: %w", name, err)
	}
	return spec.handler(ctx, validated)
}

// RegisterProvider registers a plugin as an LLM provider.
func (r *Registry) RegisterProvider(entry ProviderEntry) {
	if entry.PluginID == "" {
		return
	}
	r.mu.Lock()
	r.providers[entry.PluginID] = entry
	r.mu.Unlock()
}

// ListModels queries all registered provider plugins and returns their available models.
func (r *Registry) ListModels(ctx context.Context) ([]ModelInfo, error) {
	r.mu.RLock()
	entries := make([]ProviderEntry, 0, len(r.providers))
	for _, p := range r.providers {
		entries = append(entries, p)
	}
	r.mu.RUnlock()

	var all []ModelInfo
	for _, p := range entries {
		if p.ListModels == nil {
			continue
		}
		models, err := p.ListModels(ctx)
		if err != nil {
			continue
		}
		for _, m := range models {
			m.PluginID = p.PluginID
			if m.ID == "" {
				m.ID = m.Name
			}
			all = append(all, m)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].ID < all[j].ID
	})
	return all, nil
}

// Complete routes a completion request to the provider plugin that owns req.Model (or the default model).
func (r *Registry) Complete(ctx context.Context, req CompleteRequest, onStream func(StreamEvent)) (*CompleteResponse, error) {
	models, err := r.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("no models are configured in any provider plugin")
	}

	var chosen *ModelInfo
	if req.Model != "" {
		for i := range models {
			if models[i].ID == req.Model || models[i].Name == req.Model {
				chosen = &models[i]
				break
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("model %q is not found", req.Model)
		}
	} else {
		for i := range models {
			if models[i].Default {
				chosen = &models[i]
				break
			}
		}
		if chosen == nil {
			chosen = &models[0]
		}
		req.Model = chosen.ID
	}

	r.mu.RLock()
	prov, ok := r.providers[chosen.PluginID]
	r.mu.RUnlock()
	if !ok || prov.Complete == nil {
		return nil, fmt.Errorf("provider plugin %q is not available", chosen.PluginID)
	}

	resp, err := prov.Complete(ctx, req, onStream)
	if err != nil {
		return nil, err
	}
	if resp.Model == "" {
		resp.Model = chosen.ID
	}
	if resp.Provider == "" {
		resp.Provider = chosen.Provider
	}
	return resp, nil
}

// UnregisterPlugin removes all tools and providers belonging to pluginID.
func (r *Registry) UnregisterPlugin(pluginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for name, t := range r.tools {
		if t.PluginID == pluginID {
			delete(r.tools, name)
		}
	}
	delete(r.providers, pluginID)
}
