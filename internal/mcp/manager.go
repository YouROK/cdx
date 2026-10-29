package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/registry"
)

// Manager keeps the connected MCP servers.
type Manager struct {
	cfg *config.Config
	reg *registry.Registry

	mu      sync.RWMutex
	clients map[string]*Client
	tools   map[string][]Tool
	crashes map[string]int
	failed  map[string]bool
}

// NewManager creates a manager.
func NewManager(cfg *config.Config, reg *registry.Registry) *Manager {
	return &Manager{
		cfg:     cfg,
		reg:     reg,
		clients: map[string]*Client{},
		tools:   map[string][]Tool{},
		crashes: map[string]int{},
		failed:  map[string]bool{},
	}
}

// ConnectAll connects every enabled server.
//
// A failing server does not stop the others.
func (m *Manager) ConnectAll(ctx context.Context) {
	for i := range m.cfg.MCP {
		srv := m.cfg.MCP[i]
		if !srv.Enabled {
			continue
		}
		if err := m.Connect(ctx, srv); err != nil {
			logx.Warn("[MCP:%s] Failed to connect: %v", srv.Name, err)
		}
	}
}

// Connect connects one server and registers its tools.
func (m *Manager) Connect(ctx context.Context, srv config.MCPServer) error {
	if srv.Transport != TransportStdio {
		return fmt.Errorf("transport %q is not supported yet", srv.Transport)
	}

	timeout := time.Duration(srv.TimeoutMS) * time.Millisecond
	client := NewClient(Options{
		Name:    srv.Name,
		Command: srv.Command,
		Args:    srv.Args,
		Env:     srv.Env,
		Dir:     srv.Dir,
		Timeout: timeout,
	})

	if err := client.Start(ctx); err != nil {
		return err
	}

	toolList, err := client.ListTools(ctx)
	if err != nil {
		client.Stop()
		return err
	}

	m.mu.Lock()
	m.clients[srv.Name] = client
	m.tools[srv.Name] = toolList
	m.mu.Unlock()

	if err := m.registerTools(srv, client, toolList); err != nil {
		client.Stop()
		return err
	}

	logx.Info("[MCP:%s] Connected with %d tools", srv.Name, len(toolList))
	return nil
}

// registerTools adds the server tools to the agent registry.
func (m *Manager) registerTools(srv config.MCPServer, client *Client, list []Tool) error {
	if m.reg == nil {
		return nil
	}

	level := srv.Permission
	if level == "" {
		level = registry.LevelEdit
	}

	for _, t := range list {
		tool := &registry.Tool{
			Spec: registry.Spec{
				ID:          "mcp." + srv.Name + "." + t.Name,
				LLMName:     mcpShortName(srv.Name, t.Name),
				Description: t.Description,
				Level:       level,
				Schema:      t.InputSchema,
			},
			Run: m.toolRunner(srv, client, t.Name),
		}
		if err := m.reg.Add(tool); err != nil {
			return fmt.Errorf("register tool %s: %w", t.Name, err)
		}
	}
	return nil
}

// toolRunner builds the handler that calls an MCP tool.
func (m *Manager) toolRunner(srv config.MCPServer, client *Client, toolName string) registry.Handler {
	return func(ctx context.Context, args map[string]any) (registry.Result, error) {
		clean := make(map[string]any, len(args))
		for k, v := range args {
			if strings.HasPrefix(k, "__") {
				continue
			}
			clean[k] = v
		}

		text, err := client.CallTool(ctx, toolName, clean)
		if err != nil {
			return registry.Result{Content: fmt.Sprintf("error: %v", err)}, nil
		}
		return registry.Result{Content: text, OK: true}, nil
	}
}

// mcpShortName builds the tool name the model sees.
func mcpShortName(server, tool string) string {
	safe := strings.NewReplacer(".", "_", "-", "_").Replace(server)
	name := "mcp_" + safe + "_" + tool
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// Restart reconnects a server.
func (m *Manager) Restart(ctx context.Context, name string) error {
	m.Disconnect(name)

	for i := range m.cfg.MCP {
		if m.cfg.MCP[i].Name != name {
			continue
		}
		if err := m.Connect(ctx, m.cfg.MCP[i]); err != nil {
			m.mu.Lock()
			m.failed[name] = true
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		delete(m.failed, name)
		m.crashes[name] = 0
		m.mu.Unlock()
		return nil
	}
	return fmt.Errorf("server %q is not configured", name)
}

// Disconnect drops a server and removes its tools.
func (m *Manager) Disconnect(name string) {
	m.mu.Lock()
	client, ok := m.clients[name]
	list := m.tools[name]
	delete(m.clients, name)
	delete(m.tools, name)
	m.mu.Unlock()

	if !ok {
		return
	}
	client.Stop()

	// The server tools go away together with the connection.
	if m.reg != nil {
		m.reg.RemovePrefix("mcp." + name + ".")
	}
	_ = list
}

// DisconnectAll drops every server.
func (m *Manager) DisconnectAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	m.mu.Unlock()

	for _, name := range names {
		m.Disconnect(name)
	}
}

// Status reports the state of each server.
func (m *Manager) Status() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ServerStatus, 0, len(m.cfg.MCP))
	for _, srv := range m.cfg.MCP {
		status := ServerStatus{Name: srv.Name, Transport: srv.Transport, Enabled: srv.Enabled}
		if _, ok := m.clients[srv.Name]; ok {
			status.Connected = true
		}
		status.Tools = len(m.tools[srv.Name])
		status.Crashes = m.crashes[srv.Name]
		status.Failed = m.failed[srv.Name]
		out = append(out, status)
	}
	return out
}

// ServerStatus describes the state of a server.
type ServerStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	Tools     int    `json:"tools"`
	Crashes   int    `json:"crashes"`
	Failed    bool   `json:"failed"`
}

// Names returns the connected server names.
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, 0, len(m.clients))
	for name := range m.clients {
		out = append(out, name)
	}
	return out
}
