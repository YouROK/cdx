// Package llm defines model providers and the openai-chat client.
package llm

import (
	"context"
	"time"
)

// Message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is a dialogue message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a model request to run a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec describes a tool for the model.
type ToolSpec = map[string]any

// Request is a model request.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
	Model    string
	// MaxTokens limits the answer; zero means the provider default.
	MaxTokens int
	// Temperature sets the sampling temperature; SkipTemperature omits it.
	Temperature float64
	// ReasoningEffort is the API value already translated from the model
	// level. An empty string means the parameter is not sent.
	ReasoningEffort string
	// MaxTokensField sets the limit field name, max_tokens by default.
	MaxTokensField string
	// SkipTemperature omits temperature entirely.
	SkipTemperature bool
	// OnDelta receives answer chunks as they are generated.
	//
	// nil means a plain non-streaming request. When set, the answer streams
	// and the chunks arrive here as they come.
	OnDelta func(Delta)
}

// Delta is one chunk of the model answer.
type Delta struct {
	// Text is the next fragment of reasoning or answer.
	Text string
	// ToolCall is the tool name once its call starts.
	ToolCall string
}

// Response is the model answer.
type Response struct {
	// Reasoning holds the model reasoning when the provider sends it.
	Reasoning string
	// Content is the answer with reasoning removed.
	Content      string
	ToolCalls    []ToolCall
	InputTokens  int
	OutputTokens int
	FinishReason string
}

// Usage is token spending.
type Usage struct {
	Input  int
	Output int
}

// Provider is the model provider interface.
type Provider interface {
	// Chat performs one model request.
	Chat(ctx context.Context, req Request) (*Response, error)
	// Name returns the provider name.
	Name() string
}

// Timeout wraps a provider with a deadline.
type Timeout struct {
	Inner   Provider
	Timeout time.Duration
}

// Chat calls the provider under the deadline.
func (t *Timeout) Chat(ctx context.Context, req Request) (*Response, error) {
	if t.Timeout <= 0 {
		return t.Inner.Chat(ctx, req)
	}
	sub, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()

	type outcome struct {
		resp *Response
		err  error
	}
	ch := make(chan outcome, 1)
	go func() {
		resp, err := t.Inner.Chat(sub, req)
		ch <- outcome{resp: resp, err: err}
	}()

	select {
	case out := <-ch:
		return out.resp, out.err
	case <-sub.Done():
		return nil, sub.Err()
	}
}

// Name returns the inner provider name.
func (t *Timeout) Name() string { return t.Inner.Name() }
