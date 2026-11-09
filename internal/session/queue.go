package session

import (
	"context"
	"sync"
)

// turnQueue serializes turns that share a session and hands the session to waiters in arrival order.
type turnQueue struct {
	mu      sync.Mutex
	locked  map[string]bool
	waiters map[string][]chan struct{}
}

// newTurnQueue creates an empty per-session turn queue.
func newTurnQueue() *turnQueue {
	return &turnQueue{
		locked:  make(map[string]bool),
		waiters: make(map[string][]chan struct{}),
	}
}

// acquire reserves the session for the caller, waiting until the running turn releases it.
// An empty session key skips the queue. onWait runs once when the caller has to wait.
func (q *turnQueue) acquire(ctx context.Context, sessionID string, onWait func()) (func(), error) {
	if sessionID == "" {
		return nil, nil
	}

	q.mu.Lock()
	if !q.locked[sessionID] {
		q.locked[sessionID] = true
		q.mu.Unlock()
		return func() { q.release(sessionID) }, nil
	}
	waiter := make(chan struct{})
	q.waiters[sessionID] = append(q.waiters[sessionID], waiter)
	q.mu.Unlock()

	if onWait != nil {
		onWait()
	}

	select {
	case <-waiter:
		if err := ctx.Err(); err != nil {
			q.release(sessionID)
			return nil, err
		}
		return func() { q.release(sessionID) }, nil
	case <-ctx.Done():
		q.cancelWait(sessionID, waiter)
		return nil, ctx.Err()
	}
}

// cancelWait removes a waiter that gave up and passes the session on when it was already granted.
func (q *turnQueue) cancelWait(sessionID string, waiter chan struct{}) {
	q.mu.Lock()
	list := q.waiters[sessionID]
	for i, item := range list {
		if item == waiter {
			q.waiters[sessionID] = append(list[:i], list[i+1:]...)
			q.mu.Unlock()
			return
		}
	}
	q.mu.Unlock()

	// The waiter was already granted the session, so hand it to the next one.
	q.release(sessionID)
}

// release hands the session to the next waiter or frees it when nobody waits.
func (q *turnQueue) release(sessionID string) {
	q.mu.Lock()
	if list := q.waiters[sessionID]; len(list) > 0 {
		next := list[0]
		q.waiters[sessionID] = list[1:]
		q.mu.Unlock()
		close(next)
		return
	}
	delete(q.locked, sessionID)
	delete(q.waiters, sessionID)
	q.mu.Unlock()
}

// turnQueueKey returns the session key used for serialization; stateless runs are not queued.
func turnQueueKey(s *Session) string {
	if s == nil || s.Kind == KindNone || s.ID == "" || s.ID == "ephemeral" {
		return ""
	}
	return s.ID
}
