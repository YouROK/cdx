package daemon

import (
	"sync"

	"github.com/yourok/cdx/internal/ipc"
)

// session serves one connection: reads frames and hands out replies.
type session struct {
	conn *ipc.Conn

	mu      sync.Mutex
	pending map[string]chan *ipc.Frame

	// done closes when the connection breaks; the running turn watches it
	// to notice the client is gone and stop.
	done chan struct{}
	// closeOnce keeps done from being closed twice.
	closeOnce sync.Once
}

// newSession creates a connection session.
func newSession(c *ipc.Conn) *session {
	return &session{
		conn:    c,
		pending: map[string]chan *ipc.Frame{},
		done:    make(chan struct{}),
	}
}

// markDone reports the connection is closed.
func (s *session) markDone() {
	s.closeOnce.Do(func() { close(s.done) })
}

// await registers a wait for the reply with this id.
func (s *session) await(id string) chan *ipc.Frame {
	ch := make(chan *ipc.Frame, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	return ch
}

// forget drops a wait.
func (s *session) forget(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// deliver hands the reply to the waiting side.
func (s *session) deliver(f *ipc.Frame) bool {
	s.mu.Lock()
	ch, ok := s.pending[f.ID]
	if ok {
		delete(s.pending, f.ID)
	}
	s.mu.Unlock()

	if !ok {
		return false
	}
	select {
	case ch <- f:
	default:
	}
	return true
}
