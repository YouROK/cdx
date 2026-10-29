// Package registry holds the tools available to the agent.
//
// The core itself has no tools: they come from plugins and MCP servers.
package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Tool permission levels.
const (
	LevelRead      = "read"
	LevelEdit      = "edit"
	LevelDangerous = "dangerous"
)

// Result is the outcome of a tool call.
type Result struct {
	Content string
	OK      bool
}

// Spec describes a tool for the registry.
type Spec struct {
	// ID is the internal identifier, for example system.fs.readFile.
	ID string
	// LLMName is the short name used by the model API.
	LLMName     string
	Description string
	Level       string
	Schema      map[string]any
}

// Handler runs a tool.
type Handler func(ctx context.Context, args map[string]any) (Result, error)

// Tool is a tool with its handler.
type Tool struct {
	Spec
	Run Handler
}

// Registry holds the tools.
type Registry struct {
	mu        sync.RWMutex
	byID      map[string]*Tool
	byLLMName map[string]*Tool
	order     []string
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:      map[string]*Tool{},
		byLLMName: map[string]*Tool{},
	}
}

// Add registers a tool.
func (r *Registry) Add(t *Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.byID[t.ID]; ok {
		return fmt.Errorf("tool %s already registered", t.ID)
	}
	if _, ok := r.byLLMName[t.LLMName]; ok {
		return fmt.Errorf("tool name %s already registered", t.LLMName)
	}

	r.byID[t.ID] = t
	r.byLLMName[t.LLMName] = t
	r.order = append(r.order, t.ID)
	return nil
}

// Remove unregisters a tool.
//
// It is needed when a plugin unloads or an MCP server disconnects,
// otherwise the agent keeps offering the model a tool that is gone.
func (r *Registry) Remove(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.byID[id]
	if !ok {
		return false
	}
	delete(r.byID, id)
	delete(r.byLLMName, t.LLMName)

	for i, item := range r.order {
		if item == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return true
}

// RemovePrefix removes every tool whose identifier starts with the prefix.
func (r *Registry) RemovePrefix(prefix string) int {
	r.mu.RLock()
	ids := make([]string, 0, len(r.order))
	for _, id := range r.order {
		if strings.HasPrefix(id, prefix) {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()

	removed := 0
	for _, id := range ids {
		if r.Remove(id) {
			removed++
		}
	}
	return removed
}

// Get returns a tool by its internal identifier.
func (r *Registry) Get(id string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byID[id]
	return t, ok
}

// GetByLLMName returns a tool by its short name.
func (r *Registry) GetByLLMName(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byLLMName[name]
	return t, ok
}

// List returns the tools in registration order.
func (r *Registry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Tool, 0, len(r.order))
	for _, id := range r.order {
		if t, ok := r.byID[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// LLMSpecs returns the descriptions for the model API.
func (r *Registry) LLMSpecs() []map[string]any {
	list := r.List()
	out := make([]map[string]any, 0, len(list))
	for _, t := range list {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.LLMName,
				"description": t.Description,
				"parameters":  t.Schema,
			},
		})
	}
	return out
}

// Names returns the short tool names.
func (r *Registry) Names() []string {
	list := r.List()
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.LLMName)
	}
	sort.Strings(out)
	return out
}
