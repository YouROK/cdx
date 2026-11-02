// Package bus implements the central event pub/sub, RPC request router, and ordered hook pipeline.
package bus

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

// Common bus errors.
var (
	ErrNoHandler   = errors.New("no handler registered for topic")
	ErrSelfRequest = errors.New("recursive request to own handler")
)

// EventHandler processes a broadcast event.
type EventHandler func(ctx context.Context, payload any)

// Responder processes a 1-to-1 RPC request and returns a reply.
type Responder func(ctx context.Context, payload any) (any, error)

// HookFunc inspects or transforms a hook payload, or returns an error to abort the pipeline.
type HookFunc func(ctx context.Context, payload map[string]any) (map[string]any, error)

type subEntry struct {
	id    uint64
	owner string
	topic string
	fn    EventHandler
}

type rpcEntry struct {
	owner string
	fn    Responder
}

type hookEntry struct {
	id       uint64
	owner    string
	name     string
	priority int
	fn       HookFunc
}

type callerKey struct{}

// Bus coordinates events, RPC calls, and lifecycle hooks between the core and plugins.
type Bus struct {
	mu       sync.RWMutex
	nextID   atomic.Uint64
	subs     map[string][]subEntry
	handlers map[string]rpcEntry
	hooks    map[string][]hookEntry
}

// New creates a new Bus instance.
func New() *Bus {
	return &Bus{
		subs:     make(map[string][]subEntry),
		handlers: make(map[string]rpcEntry),
		hooks:    make(map[string][]hookEntry),
	}
}

// Subscribe registers an event listener for topic and returns an unsubscribe function.
func (b *Bus) Subscribe(owner, topic string, fn EventHandler) func() {
	id := b.nextID.Add(1)
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], subEntry{
		id:    id,
		owner: owner,
		topic: topic,
		fn:    fn,
	})
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[topic]
		for i := range list {
			if list[i].id == id {
				b.subs[topic] = append(list[:i], list[i+1:]...)
				break
			}
		}
	}
}

// Publish dispatches an event to all subscribers registered for topic.
func (b *Bus) Publish(ctx context.Context, topic string, payload any) int {
	b.mu.RLock()
	listeners := append([]subEntry(nil), b.subs[topic]...)
	b.mu.RUnlock()

	delivered := 0
	for _, sub := range listeners {
		if ctx.Err() != nil {
			break
		}
		sub.fn(ctx, payload)
		delivered++
	}
	return delivered
}

// Handle registers or overrides a 1-to-1 RPC handler for topic.
func (b *Bus) Handle(owner, topic string, fn Responder) {
	b.mu.Lock()
	b.handlers[topic] = rpcEntry{
		owner: owner,
		fn:    fn,
	}
	b.mu.Unlock()
}

// Request invokes the RPC handler registered for topic.
func (b *Bus) Request(ctx context.Context, caller, topic string, payload any) (any, error) {
	b.mu.RLock()
	entry, ok := b.handlers[topic]
	b.mu.RUnlock()

	if !ok {
		return nil, ErrNoHandler
	}
	if caller != "" && entry.owner != "" && caller == entry.owner {
		return nil, ErrSelfRequest
	}
	if prev, ok := ctx.Value(callerKey{}).(string); ok && prev != "" && prev == entry.owner {
		return nil, ErrSelfRequest
	}

	callCtx := context.WithValue(ctx, callerKey{}, caller)
	return entry.fn(callCtx, payload)
}

// OnHook registers an interceptor on a named hook pipeline, ordered by ascending priority.
func (b *Bus) OnHook(owner, hookName string, priority int, fn HookFunc) func() {
	id := b.nextID.Add(1)
	b.mu.Lock()
	list := append(b.hooks[hookName], hookEntry{
		id:       id,
		owner:    owner,
		name:     hookName,
		priority: priority,
		fn:       fn,
	})
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].priority < list[j].priority
	})
	b.hooks[hookName] = list
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		cur := b.hooks[hookName]
		for i := range cur {
			if cur[i].id == id {
				b.hooks[hookName] = append(cur[:i], cur[i+1:]...)
				break
			}
		}
	}
}

// RunHook executes all interceptors registered for hookName in priority order.
// Each interceptor may mutate and return the payload or return an error to abort the chain.
func (b *Bus) RunHook(ctx context.Context, hookName string, initial map[string]any) (map[string]any, error) {
	b.mu.RLock()
	chain := append([]hookEntry(nil), b.hooks[hookName]...)
	b.mu.RUnlock()

	current := make(map[string]any, len(initial))
	for k, v := range initial {
		current[k] = v
	}

	for _, h := range chain {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		next, err := h.fn(ctx, current)
		if err != nil {
			return current, err
		}
		if next != nil {
			current = next
		}
	}
	return current, nil
}

// UnregisterOwner removes all subscriptions, RPC handlers, and hooks belonging to owner.
func (b *Bus) UnregisterOwner(owner string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for topic, list := range b.subs {
		filtered := list[:0]
		for _, sub := range list {
			if sub.owner != owner {
				filtered = append(filtered, sub)
			}
		}
		if len(filtered) == 0 {
			delete(b.subs, topic)
		} else {
			b.subs[topic] = filtered
		}
	}

	for topic, h := range b.handlers {
		if h.owner == owner {
			delete(b.handlers, topic)
		}
	}

	for name, list := range b.hooks {
		filtered := list[:0]
		for _, h := range list {
			if h.owner != owner {
				filtered = append(filtered, h)
			}
		}
		if len(filtered) == 0 {
			delete(b.hooks, name)
		} else {
			b.hooks[name] = filtered
		}
	}
}
