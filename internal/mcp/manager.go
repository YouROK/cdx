package mcp

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// Manager owns the MCP connections opened by every plugin.
type Manager struct {
	mu      sync.Mutex
	clients map[string]*Client
}

// NewManager creates an empty connection manager.
func NewManager() *Manager {
	return &Manager{clients: map[string]*Client{}}
}

// connectionKey namespaces a server by its owning plugin.
func connectionKey(owner, id string) string {
	return owner + "\x00" + id
}

// Connect returns the live connection for a server or dials a new one.
func (m *Manager) Connect(ctx context.Context, owner string, cfg Config) (*Client, error) {
	key := connectionKey(owner, cfg.ID)

	m.mu.Lock()
	if client, ok := m.clients[key]; ok && !client.IsClosed() {
		m.mu.Unlock()
		return client, nil
	}
	m.mu.Unlock()

	client, err := Dial(ctx, cfg)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.clients[key]; ok && !existing.IsClosed() {
		m.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	m.clients[key] = client
	m.mu.Unlock()
	return client, nil
}

// Get returns a live connection by plugin and server id.
func (m *Manager) Get(owner, id string) (*Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	client, ok := m.clients[connectionKey(owner, id)]
	if !ok || client.IsClosed() {
		return nil, false
	}
	return client, true
}

// Close stops one connection and reports whether it existed.
func (m *Manager) Close(owner, id string) bool {
	key := connectionKey(owner, id)
	m.mu.Lock()
	client, ok := m.clients[key]
	delete(m.clients, key)
	m.mu.Unlock()
	if !ok {
		return false
	}
	_ = client.Close()
	return true
}

// CloseOwner stops every connection of one plugin.
func (m *Manager) CloseOwner(owner string) int {
	prefix := owner + "\x00"
	m.mu.Lock()
	var closing []*Client
	for key, client := range m.clients {
		if strings.HasPrefix(key, prefix) {
			closing = append(closing, client)
			delete(m.clients, key)
		}
	}
	m.mu.Unlock()
	for _, client := range closing {
		_ = client.Close()
	}
	return len(closing)
}

// CloseAll stops every connection.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	closing := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		closing = append(closing, client)
	}
	m.clients = map[string]*Client{}
	m.mu.Unlock()
	for _, client := range closing {
		_ = client.Close()
	}
}

// IDs returns the server ids connected for a plugin.
func (m *Manager) IDs(owner string) []string {
	prefix := owner + "\x00"
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for key, client := range m.clients {
		if strings.HasPrefix(key, prefix) && !client.IsClosed() {
			ids = append(ids, strings.TrimPrefix(key, prefix))
		}
	}
	sort.Strings(ids)
	return ids
}
