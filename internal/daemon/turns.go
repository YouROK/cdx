package daemon

import (
	"context"
	"sort"
	"sync"
)

// turns keeps cancellable turns per connection.
//
// A turn runs in its own goroutine, so cancellation arrives in another
// frame; without this registry there would be no way to stop a request.
type turns struct {
	mu     sync.Mutex
	byConn map[*session]map[string]context.CancelFunc
	// info keeps turn details for the web interface.
	info map[*session]map[string]turnInfo
}

// newTurns creates a turn registry.
func newTurns() *turns {
	return &turns{
		byConn: map[*session]map[string]context.CancelFunc{},
		info:   map[*session]map[string]turnInfo{},
	}
}

// register records a turn and returns its context and cancel func.
func (t *turns) register(s *session, id string) (context.Context, context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.byConn[s] == nil {
		t.byConn[s] = map[string]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.byConn[s][id] = cancel

	// The cancel func also drops the entry when the turn ends.
	return ctx, func() {
		t.forget(s, id)
		cancel()
	}
}

// forget removes a turn from the registry.
func (t *turns) forget(s *session, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if m, ok := t.byConn[s]; ok {
		delete(m, id)
		if len(m) == 0 {
			delete(t.byConn, s)
		}
	}
}

// cancel stops one turn by id.
func (t *turns) cancel(s *session, id string) bool {
	t.mu.Lock()
	cancel, ok := t.byConn[s][id]
	t.mu.Unlock()

	if !ok {
		return false
	}
	cancel()
	return true
}

// cancelAll stops every turn of a connection.
func (t *turns) cancelAll(s *session) {
	t.mu.Lock()
	list := make([]context.CancelFunc, 0, len(t.byConn[s]))
	for _, cancel := range t.byConn[s] {
		list = append(list, cancel)
	}
	t.mu.Unlock()

	for _, cancel := range list {
		cancel()
	}
}

// turnInfo describes a running turn.
type turnInfo struct {
	ID     string
	Model  string
	Preset string
	Prompt string
	// Started is the start time in Unix seconds.
	Started int64
	// CWD is the directory the request came from.
	CWD string
}

// setInfo records turn details for the web interface.
func (t *turns) setInfo(s *session, info turnInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.info == nil {
		t.info = map[*session]map[string]turnInfo{}
	}
	if t.info[s] == nil {
		t.info[s] = map[string]turnInfo{}
	}
	t.info[s][info.ID] = info
}

// dropInfo removes turn details.
func (t *turns) dropInfo(s *session, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if m, ok := t.info[s]; ok {
		delete(m, id)
		if len(m) == 0 {
			delete(t.info, s)
		}
	}
}

// list returns the running turns.
func (t *turns) list() []turnInfo {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]turnInfo, 0)
	for _, m := range t.info {
		for _, info := range m {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started < out[j].Started })
	return out
}

// cancelByID stops a turn by request id, across connections.
func (t *turns) cancelByID(id string) bool {
	t.mu.Lock()
	var cancel context.CancelFunc
	for _, m := range t.byConn {
		if c, ok := m[id]; ok {
			cancel = c
			break
		}
	}
	t.mu.Unlock()

	if cancel == nil {
		return false
	}
	cancel()
	return true
}
