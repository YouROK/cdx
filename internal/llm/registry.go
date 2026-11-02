// Package llm defines model providers and their protocols.
package llm

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Config describes one provider connection.
type Config struct {
	// Protocol picks the wire format: openai-chat, anthropic and so on.
	Protocol string
	// BaseURL is the API address.
	BaseURL string
	// APIKey is the provider access key.
	APIKey string
	// Model is the remote model identifier.
	Model string
}

// Discoverer lists the models a provider serves.
//
// A protocol that cannot list models skips this interface, and the interface
// then falls back to entering the model by hand.
type Discoverer interface {
	DiscoverModels(ctx context.Context) ([]DiscoveredModel, error)
}

// Constructor builds a provider from its settings.
type Constructor func(Config) Provider

var (
	registryMu sync.RWMutex
	// registry maps a protocol name to its constructor.
	registry = map[string]Constructor{}
)

// Register adds a protocol.
//
// Registration happens at init time, before any request, so no locking is
// needed around the call sites.
func Register(name string, c Constructor) {
	if name == "" || c == nil {
		return
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = c
}

// Protocols returns the registered protocol names in order.
func Protocols() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Supported reports whether a protocol is registered.
func Supported(name string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()

	_, ok := registry[name]
	return ok
}

// NewProvider builds a provider for the configured protocol.
//
// An empty protocol means openai-chat: it is the most common wire format and
// the safe default for older records.
func NewProvider(cfg Config) (Provider, error) {
	name := cfg.Protocol
	if name == "" {
		name = ProtocolOpenAIChat
	}

	registryMu.RLock()
	ctor, ok := registry[name]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("protocol %q is not supported", name)
	}
	cfg.Protocol = name
	return ctor(cfg), nil
}

// Protocol names.
const (
	ProtocolOpenAIChat      = "openai-chat"
	ProtocolOpenAIResponses = "openai-responses"
	ProtocolAnthropic       = "anthropic"
)
