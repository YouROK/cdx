package llm

import (
	"context"
	"encoding/json"
	"testing"
)

// TestProtocolsRegistered checks that the declared protocols exist.
func TestProtocolsRegistered(t *testing.T) {
	for _, name := range []string{ProtocolOpenAIChat, ProtocolAnthropic} {
		if !Supported(name) {
			t.Fatalf("protocol %q must be registered", name)
		}
	}
}

// TestNewProviderUnknown checks the refusal of an unknown protocol.
func TestNewProviderUnknown(t *testing.T) {
	if _, err := NewProvider(Config{Protocol: "nope"}); err == nil {
		t.Fatal("an unknown protocol must be refused")
	}
}

// TestNewProviderDefaults checks that an empty protocol means openai-chat.
func TestNewProviderDefaults(t *testing.T) {
	p, err := NewProvider(Config{})
	if err != nil {
		t.Fatalf("empty protocol must fall back to openai-chat: %v", err)
	}
	if p.Name() != ProtocolOpenAIChat {
		t.Fatalf("expected openai-chat, got %q", p.Name())
	}
}

// TestNewProviderByName checks the constructor picked for each protocol.
func TestNewProviderByName(t *testing.T) {
	cases := map[string]string{
		ProtocolOpenAIChat: ProtocolOpenAIChat,
		ProtocolAnthropic:  ProtocolAnthropic,
	}
	for name, want := range cases {
		p, err := NewProvider(Config{Protocol: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Name() != want {
			t.Fatalf("%s: got %q", name, p.Name())
		}
	}
}

// TestProtocolsSorted checks that the list is ordered.
func TestProtocolsSorted(t *testing.T) {
	list := Protocols()
	if len(list) < 2 {
		t.Fatalf("at least two protocols are expected, got %v", list)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1] > list[i] {
			t.Fatalf("the list must be sorted: %v", list)
		}
	}
}

// TestAnthropicNoModelList checks that the protocol reports the missing list.
//
// The Messages API has no model list endpoint, so the interface must fall back
// to entering the model by hand.
func TestAnthropicNoModelList(t *testing.T) {
	p := NewAnthropic("", "", "")
	d, ok := any(p).(Discoverer)
	if !ok {
		t.Fatal("the anthropic client must implement Discoverer to report the gap")
	}
	if _, err := d.DiscoverModels(context.Background()); err == nil {
		t.Fatal("a missing model list must be reported as an error")
	}
}

// TestAnthropicBuildBody checks the request shape.
func TestAnthropicBuildBody(t *testing.T) {
	c := NewAnthropic("", "", "")

	body, err := c.buildBody("claude", Request{
		Messages: []Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleUser, Content: "hi"},
		},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("build body: %v", err)
	}

	var got anthropicRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// The system prompt is a field of its own, not a message.
	if got.System != "be brief" {
		t.Fatalf("system prompt must be a separate field, got %q", got.System)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("the system prompt must not become a message, got %d", len(got.Messages))
	}
	if got.MaxTokens != 100 {
		t.Fatalf("expected the limit to be passed, got %d", got.MaxTokens)
	}
}

// TestAnthropicDefaultMaxTokens checks the required limit.
//
// The API rejects a request without max_tokens, so a default is sent.
func TestAnthropicDefaultMaxTokens(t *testing.T) {
	c := NewAnthropic("", "", "")

	body, err := c.buildBody("claude", Request{})
	if err != nil {
		t.Fatalf("build body: %v", err)
	}

	var got anthropicRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.MaxTokens <= 0 {
		t.Fatalf("the API requires a limit, got %d", got.MaxTokens)
	}
}

// TestAnthropicToolResult checks that a tool result becomes a user block.
func TestAnthropicToolResult(t *testing.T) {
	c := NewAnthropic("", "", "")

	body, err := c.buildBody("claude", Request{
		Messages: []Message{
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "t1", Name: "read", Arguments: `{"path":"a"}`}}},
			{Role: RoleTool, ToolCallID: "t1", Content: "file content"},
		},
	})
	if err != nil {
		t.Fatalf("build body: %v", err)
	}

	var got anthropicRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("expected an assistant turn and a tool result, got %d", len(got.Messages))
	}
	if got.Messages[1].Role != RoleUser {
		t.Fatalf("a tool result must travel as a user turn, got %q", got.Messages[1].Role)
	}
}

// TestAnthropicToResponse checks the answer conversion.
func TestAnthropicToResponse(t *testing.T) {
	c := NewAnthropic("", "", "")

	out := anthropicResponse{
		Content: []anthropicBlock{
			{Type: "thinking", Text: "reasoning"},
			{Type: "text", Text: "answer"},
			{Type: "tool_use", ID: "t1", Name: "read", Input: []byte(`{"path":"a"}`)},
		},
	}
	out.Usage.Input = 10
	out.Usage.Output = 20

	res := c.toResponse(out)

	if res.Reasoning != "reasoning" {
		t.Fatalf("reasoning must be kept apart, got %q", res.Reasoning)
	}
	if res.Content != "answer" {
		t.Fatalf("expected the answer text, got %q", res.Content)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "read" {
		t.Fatalf("tool call lost: %+v", res.ToolCalls)
	}
	if res.InputTokens != 10 || res.OutputTokens != 20 {
		t.Fatalf("token usage lost: %d/%d", res.InputTokens, res.OutputTokens)
	}
}
