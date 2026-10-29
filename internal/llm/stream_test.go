package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStreamCollectsText checks text is assembled from stream chunks.
func TestStreamCollectsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"delta":{"content":"При"}}]}`,
			`{"choices":[{"delta":{"content":"вет"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"usage":{"prompt_tokens":10,"completion_tokens":3},"choices":[]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var got []string
	c := NewOpenAIChat(srv.URL, "k", "m")
	resp, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		OnDelta: func(d Delta) {
			if d.Text != "" {
				got = append(got, d.Text)
			}
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "Привет" {
		t.Fatalf("content = %q", resp.Content)
	}
	if strings.Join(got, "") != "Привет" {
		t.Fatalf("deltas = %v", got)
	}
	if resp.InputTokens != 10 || resp.OutputTokens != 3 {
		t.Fatalf("usage = %d/%d", resp.InputTokens, resp.OutputTokens)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish = %q", resp.FinishReason)
	}
}

// TestStreamAssemblesToolCalls checks a tool call is glued together.
//
// The name arrives in the first frame and the arguments in pieces, so
// without joining by index the JSON would be truncated.
func TestStreamAssemblesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunks := []string{
			`{"choices":[{"delta":{"content":"думаю"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":""}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go.mod\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	var tools []string
	c := NewOpenAIChat(srv.URL, "k", "m")
	resp, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		OnDelta: func(d Delta) {
			if d.ToolCall != "" {
				tools = append(tools, d.ToolCall)
			}
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "read_file" || tc.ID != "call_1" {
		t.Fatalf("unexpected tool call %+v", tc)
	}
	if tc.Arguments != `{"path":"go.mod"}` {
		t.Fatalf("arguments were not assembled: %q", tc.Arguments)
	}
	if len(tools) != 1 || tools[0] != "read_file" {
		t.Fatalf("tool announcement missing: %v", tools)
	}
}

// TestStreamWithoutOnDelta checks a plain request without streaming.
func TestStreamWithoutOnDelta(t *testing.T) {
	var sawStream bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawStream = strings.Contains(readBody(r), `"stream":true`)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIChat(srv.URL, "k", "m")
	if _, err := c.Chat(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if sawStream {
		t.Fatal("stream must not be requested without a delta handler")
	}
}

// readBody reads the request body.
func readBody(r *http.Request) string {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
