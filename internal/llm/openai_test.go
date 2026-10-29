package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturedRequest is the request body the test server received.
type capturedRequest map[string]any

// newServer starts a server and returns it with the request sink.
func newServer(t *testing.T, handler func(w http.ResponseWriter, body capturedRequest, attempt int)) (*httptest.Server, *int) {
	t.Helper()

	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		var body capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		handler(w, body, attempt)
	}))
	t.Cleanup(srv.Close)
	return srv, &attempt
}

// okResponse sends a valid model answer.
func okResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 7},
	})
}

// TestChatSendsReasoningAndTokens checks the request parameters are sent.
func TestChatSendsReasoningAndTokens(t *testing.T) {
	var got capturedRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, body capturedRequest, _ int) {
		got = body
		okResponse(w, "привет")
	})

	c := NewOpenAIChat(srv.URL, "key", "remote-model")
	resp, err := c.Chat(context.Background(), Request{
		Messages:        []Message{{Role: RoleUser, Content: "x"}},
		MaxTokens:       1024,
		Temperature:     0.4,
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got["model"] != "remote-model" {
		t.Fatalf("expected remote model id, got %v", got["model"])
	}
	if got["max_tokens"] != float64(1024) {
		t.Fatalf("expected max_tokens 1024, got %v", got["max_tokens"])
	}
	if got["temperature"] != 0.4 {
		t.Fatalf("expected temperature 0.4, got %v", got["temperature"])
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort high, got %v", got["reasoning_effort"])
	}
	if resp.Content != "привет" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
	if resp.InputTokens != 12 || resp.OutputTokens != 7 {
		t.Fatalf("unexpected usage %+v", resp)
	}
}

// TestChatHonoursFieldOverrides checks the field name override and temperature omission.
func TestChatHonoursFieldOverrides(t *testing.T) {
	var got capturedRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, body capturedRequest, _ int) {
		got = body
		okResponse(w, "ok")
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	_, err := c.Chat(context.Background(), Request{
		Messages:        []Message{{Role: RoleUser, Content: "x"}},
		MaxTokens:       500,
		Temperature:     0.9,
		MaxTokensField:  "max_completion_tokens",
		SkipTemperature: true,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if _, ok := got["max_tokens"]; ok {
		t.Fatal("max_tokens must not be sent when field is overridden")
	}
	if got["max_completion_tokens"] != float64(500) {
		t.Fatalf("expected max_completion_tokens, got %v", got)
	}
	if _, ok := got["temperature"]; ok {
		t.Fatal("temperature must be omitted when disabled")
	}
}

// TestChatOmitsEmptyOptions checks no extra fields are sent.
func TestChatOmitsEmptyOptions(t *testing.T) {
	var got capturedRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, body capturedRequest, _ int) {
		got = body
		okResponse(w, "ok")
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "x"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Temperature is a required request field: zero is a value too, so
	// omission is controlled by the SkipTemperature flag only.
	for _, field := range []string{"max_tokens", "reasoning_effort", "tools"} {
		if _, ok := got[field]; ok {
			t.Fatalf("%s must be omitted when unset", field)
		}
	}
	if v, ok := got["temperature"]; !ok || v != float64(0) {
		t.Fatalf("temperature must be sent even at zero, got %v", got["temperature"])
	}
}

// TestChatSendsTools checks tool descriptions are sent.
func TestChatSendsTools(t *testing.T) {
	var got capturedRequest
	srv, _ := newServer(t, func(w http.ResponseWriter, body capturedRequest, _ int) {
		got = body
		okResponse(w, "ok")
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	_, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "x"}},
		Tools:    []ToolSpec{{"type": "function"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, got %v", got["tools"])
	}
}

// TestChatParsesToolCalls checks tool calls are parsed.
func TestChatParsesToolCalls(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ capturedRequest, _ int) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name": "read_file", "arguments": `{"path":"a.go"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" || resp.ToolCalls[0].ID != "call-1" {
		t.Fatalf("unexpected tool call %+v", resp.ToolCalls[0])
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("unexpected finish reason %q", resp.FinishReason)
	}
}

// TestChatRetriesOnRateLimit checks a retry on 429.
func TestChatRetriesOnRateLimit(t *testing.T) {
	srv, attempt := newServer(t, func(w http.ResponseWriter, _ capturedRequest, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"busy"}}`))
			return
		}
		okResponse(w, "после повтора")
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if *attempt < 2 {
		t.Fatalf("expected a retry, attempts=%d", *attempt)
	}
	if resp.Content != "после повтора" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
}

// TestChatNoRetryOnAuth checks there is no retry on 401.
func TestChatNoRetryOnAuth(t *testing.T) {
	srv, attempt := newServer(t, func(w http.ResponseWriter, _ capturedRequest, _ int) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	})

	c := NewOpenAIChat(srv.URL, "bad", "m")
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if *attempt != 1 {
		t.Fatalf("401 must not be retried, attempts=%d", *attempt)
	}
}

// TestChatErrorMentionsStatus checks the error text.
func TestChatErrorMentionsStatus(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ capturedRequest, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "400") || !contains(got, "bad model") {
		t.Fatalf("error must mention status and message, got %q", got)
	}
}

// TestChatEmptyChoices checks the error on an empty answer.
func TestChatEmptyChoices(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ capturedRequest, _ int) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	})

	c := NewOpenAIChat(srv.URL, "", "m")
	if _, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("expected error for empty choices")
	}
}

// TestChatCancelledByContext checks request cancellation.
func TestChatCancelledByContext(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, _ capturedRequest, _ int) {
		okResponse(w, "ok")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewOpenAIChat(srv.URL, "", "m")
	if _, err := c.Chat(ctx, Request{Messages: []Message{{Role: RoleUser, Content: "x"}}}); err == nil {
		t.Fatal("expected context error")
	}
}

// TestTimeoutWrapper checks the timeout wrapper.
func TestTimeoutWrapper(t *testing.T) {
	p := &staticProvider{}
	wrapped := &Timeout{Inner: p, Timeout: 0}

	resp, err := wrapped.Chat(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "static" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
	if wrapped.Name() != "static" {
		t.Fatalf("unexpected name %q", wrapped.Name())
	}
}

// staticProvider returns a fixed answer.
type staticProvider struct{}

func (s *staticProvider) Chat(context.Context, Request) (*Response, error) {
	return &Response{Content: "static"}, nil
}

func (s *staticProvider) Name() string { return "static" }

// contains reports whether the string holds the substring.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestDiscoverModelsParsesList checks the provider model list is parsed.
func TestDiscoverModelsParsesList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("unexpected authorization %q", got)
		}
		_, _ = w.Write([]byte(`{
			"data": [
				{"id": "plain"},
				{"id": "with-context", "context_length": 8000, "max_tokens": 500},
				{"id": "with-top-provider", "top_provider": {"context_length": 16000, "max_completion_tokens": 900}}
			]
		}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "secret", "")
	got, err := c.DiscoverModels(context.Background())
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected three models, got %+v", got)
	}
	// The list is sorted by identifier.
	if got[0].ID != "plain" || got[1].ID != "with-context" || got[2].ID != "with-top-provider" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if got[0].ContextWindow != 0 || got[0].MaxTokens != 0 {
		t.Fatalf("unknown limits must stay zero: %+v", got[0])
	}
	if got[1].ContextWindow != 8000 || got[1].MaxTokens != 500 {
		t.Fatalf("unexpected limits: %+v", got[1])
	}
	if got[2].ContextWindow != 16000 || got[2].MaxTokens != 900 {
		t.Fatalf("limits from top_provider were lost: %+v", got[2])
	}
}

// TestDiscoverModelsReportsStatus checks the provider error is reported.
func TestDiscoverModelsReportsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "bad key"}}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "wrong", "")
	if _, err := c.DiscoverModels(context.Background()); err == nil {
		t.Fatal("expected an error for a rejected key")
	}
}
