// Package bus implements the internal bus: publishing, subscribing and requests.
package bus

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Topic prefixes.
const (
	PrefixSystem = "system"
	PrefixCore   = "core"
	PrefixPlugin = "plugin"
	PrefixMCP    = "mcp"
	PrefixEvent  = "event"
)

// Subscription buffer overflow policies.
const (
	PolicyDropOldest = "drop_oldest"
	PolicyBlock      = "block"
)

// Bus errors.
var (
	ErrNoHandler   = errors.New("no handler for topic")
	ErrAmbiguous   = errors.New("topic has more than one handler")
	ErrTimeout     = errors.New("request timed out")
	ErrForbidden   = errors.New("topic is not allowed")
	ErrSelfRequest = errors.New("recursive request to own tool")
)

// Handler processes an event.
type Handler func(ctx context.Context, payload any)

// Responder processes a request and returns a reply.
type Responder func(ctx context.Context, payload any) (any, error)

// Delivery is the delivery context returned by Publish.
type Delivery struct {
	ID string
	// Cancel drops the events not yet delivered.
	Cancel func()
	bus    *Bus
	// delivered counts the delivered copies.
	delivered atomic.Int64
}

// Delivered returns the number of delivered copies.
func (d *Delivery) Delivered() int64 {
	if d == nil {
		return 0
	}
	return d.delivered.Load()
}

type subscription struct {
	id     uint64
	owner  string
	topic  string
	policy string
	events chan event
	done   chan struct{}
}

type event struct {
	payload any
	dropped int64
}

// Bus is the central bus.
type Bus struct {
	mu     sync.RWMutex
	nextID atomic.Uint64
	subs   map[string][]*subscription
	// handlers maps an exact topic to its request handler.
	handlers map[string]registered
	// owners tracks handler owners to detect recursion.
	callMu sync.Mutex
}

type registered struct {
	owner string
	fn    Responder
}

// New creates a bus.
func New() *Bus {
	return &Bus{
		subs:     map[string][]*subscription{},
		handlers: map[string]registered{},
	}
}

// Subscribe subscribes to a topic.
//
// bufSize sets the buffer size: publishing does not wait for the handler,
// so a slow subscriber loses events by policy instead of stalling the bus.
func (b *Bus) Subscribe(owner, topic string, bufSize int, policy string, h Handler) (func(), error) {
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	if bufSize <= 0 {
		bufSize = 64
	}
	if policy != PolicyBlock {
		policy = PolicyDropOldest
	}

	sub := &subscription{
		id:     b.nextID.Add(1),
		owner:  owner,
		topic:  topic,
		policy: policy,
		events: make(chan event, bufSize),
		done:   make(chan struct{}),
	}

	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], sub)
	b.mu.Unlock()

	// Each subscription gets its own dispatcher, so events of one topic
	// arrive in publishing order.
	go b.dispatch(sub, h)

	return func() { b.unsubscribe(sub) }, nil
}

// dispatch delivers events to a subscriber in publishing order.
func (b *Bus) dispatch(sub *subscription, h Handler) {
	for {
		select {
		case <-sub.done:
			return
		case ev := <-sub.events:
			func() {
				defer func() {
					// A handler panic must not bring the bus down.
					_ = recover()
				}()
				h(context.Background(), ev.payload)
			}()
		}
	}
}

// unsubscribe removes a subscription.
func (b *Bus) unsubscribe(sub *subscription) {
	b.mu.Lock()
	list := b.subs[sub.topic]
	for i, s := range list {
		if s.id == sub.id {
			b.subs[sub.topic] = append(list[:i], list[i+1:]...)
			break
		}
	}
	b.mu.Unlock()

	close(sub.done)
}

// Publish sends an event to the subscribers.
//
// The call does not wait for handlers: the event goes to each subscription
// buffer.
func (b *Bus) Publish(ctx context.Context, topic string, payload any) *Delivery {
	subs := b.match(topic)

	d := &Delivery{ID: fmt.Sprintf("d%d", b.nextID.Add(1)), bus: b}
	ctx, cancel := context.WithCancel(ctx)
	d.Cancel = cancel

	for _, sub := range subs {
		b.deliver(ctx, sub, event{payload: payload}, d)
	}
	return d
}

// deliver puts the event into the subscription buffer by its policy.
func (b *Bus) deliver(ctx context.Context, sub *subscription, ev event, d *Delivery) {
	if sub.policy == PolicyBlock {
		select {
		case sub.events <- ev:
			d.delivered.Add(1)
		case <-ctx.Done():
		case <-sub.done:
		}
		return
	}

	select {
	case sub.events <- ev:
		d.delivered.Add(1)
	default:
		// Buffer is full: the oldest event is dropped.
		select {
		case <-sub.events:
		default:
		}
		select {
		case sub.events <- ev:
			d.delivered.Add(1)
		default:
		}
	}
}

// match finds the subscriptions matching a topic.
//
// Exact match and a trailing wildcard are supported: mcp.github.*
func (b *Bus) match(topic string) []*subscription {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var out []*subscription
	for pattern, list := range b.subs {
		if !MatchTopic(pattern, topic) {
			continue
		}
		out = append(out, list...)
	}
	// Order matters for predictability: exact matches come first.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].topic == topic && out[j].topic != topic
	})
	return out
}

// MatchTopic checks that a topic matches a pattern.
func MatchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(topic, prefix) && len(topic) > len(prefix)
	}
	return false
}

// Register adds a request handler for a topic.
func (b *Bus) Register(owner, topic string, fn Responder) error {
	if topic == "" {
		return fmt.Errorf("topic is required")
	}
	if fn == nil {
		return fmt.Errorf("handler is required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.handlers[topic]; ok {
		return fmt.Errorf("handler for %s is already registered", topic)
	}
	b.handlers[topic] = registered{owner: owner, fn: fn}
	return nil
}

// Unregister removes the handler.
func (b *Bus) Unregister(topic string) {
	b.mu.Lock()
	delete(b.handlers, topic)
	b.mu.Unlock()
}

// Clear removes all subscriptions and handlers of an owner.
func (b *Bus) Clear(owner string) {
	b.mu.Lock()
	for topic, list := range b.subs {
		var keep []*subscription
		for _, sub := range list {
			if sub.owner == owner {
				close(sub.done)
				continue
			}
			keep = append(keep, sub)
		}
		if len(keep) == 0 {
			delete(b.subs, topic)
			continue
		}
		b.subs[topic] = keep
	}

	for topic, h := range b.handlers {
		if h.owner == owner {
			delete(b.handlers, topic)
		}
	}
	b.mu.Unlock()
}

// Request performs a request and waits for the reply.
//
// from is the caller owner: it rejects a call to its own topic, otherwise
// the plugin VM would queue behind itself.
func (b *Bus) Request(ctx context.Context, from, topic string, payload any) (any, error) {
	b.mu.RLock()
	h, ok := b.handlers[topic]
	b.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoHandler, topic)
	}
	if from != "" && h.owner == from {
		return nil, fmt.Errorf("%w: %s", ErrSelfRequest, topic)
	}

	type outcome struct {
		value any
		err   error
	}
	ch := make(chan outcome, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome{err: fmt.Errorf("handler panicked: %v", r)}
			}
		}()
		value, err := h.fn(ctx, payload)
		ch <- outcome{value: value, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
	case got := <-ch:
		return got.value, got.err
	}
}

// Topics lists the registered request topics.
func (b *Bus) Topics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([]string, 0, len(b.handlers))
	for topic := range b.handlers {
		out = append(out, topic)
	}
	sort.Strings(out)
	return out
}

// WaitUntil waits for the condition or for the time to run out.
func WaitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}
