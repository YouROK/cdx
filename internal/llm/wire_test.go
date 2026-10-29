package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmptyMaxTokensNotSent checks an unset limit is not sent.
func TestEmptyMaxTokensNotSent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := got["max_tokens"]; ok {
		t.Fatalf("empty max_tokens must not be sent, got %v", got)
	}
	// Zero is a valid temperature, so it is always sent.
	if v, ok := got["temperature"]; !ok || v != float64(0) {
		t.Fatalf("temperature must be sent even at zero, got %v", v)
	}
}

// TestSetMaxTokensSent checks a configured limit is sent.
func TestSetMaxTokensSent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	if _, err := c.Chat(context.Background(), Request{
		Messages:  []Message{{Role: RoleUser, Content: "hi"}},
		MaxTokens: 512,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got["max_tokens"] != float64(512) {
		t.Fatalf("expected max_tokens 512, got %v", got["max_tokens"])
	}
}

// TestSkipTemperature checks the temperature parameter is omitted.
func TestSkipTemperature(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	if _, err := c.Chat(context.Background(), Request{
		Messages:        []Message{{Role: RoleUser, Content: "hi"}},
		Temperature:     0.7,
		SkipTemperature: true,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := got["temperature"]; ok {
		t.Fatalf("temperature must be omitted when skipped, got %v", got["temperature"])
	}
}
