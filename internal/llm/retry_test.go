package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetriesOn429 checks a busy gateway is survived by retries.
func TestRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first three answers are busy, the fourth passes.
		if atomic.AddInt32(&calls, 1) <= 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"at capacity"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	c.retryWaitBase = time.Millisecond
	resp, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat must survive a busy gateway: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("expected 4 attempts, got %d", got)
	}
}

// TestRetriesExhausted checks the message after all attempts.
//
// A free gateway can stay busy: what matters is that attempts are many
// while the error stays readable.
func TestRetriesExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"at capacity"}}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	c.retryWaitBase = time.Millisecond
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != defaultRetries {
		t.Fatalf("expected %d attempts, got %d", defaultRetries, got)
	}
}

// TestRetriesRespectContext checks cancellation stops the retries.
func TestRetriesRespectContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"at capacity"}}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewOpenAIChat(srv.URL, "k", "m")
	c.retryWaitBase = time.Millisecond
	if _, err := c.Chat(ctx, Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("cancelled context must stop the request")
	}
}
