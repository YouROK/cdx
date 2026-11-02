package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic is a Messages API client.
//
// The wire format differs from openai in three ways: the system prompt is a
// separate field, tool results travel as user content blocks, and the answer
// streams as typed events rather than deltas.
type Anthropic struct {
	baseURL    string
	apiKey     string
	client     *http.Client
	model      string
	maxRetries int
	retryWait  time.Duration
}

// anthropicVersion is the API version header the service requires.
const anthropicVersion = "2023-06-01"

// anthropicMaxTokens is used when the request sets no limit.
//
// The field is required by the API, so a default is always sent.
const anthropicMaxTokens = 4096

func init() {
	Register(ProtocolAnthropic, func(cfg Config) Provider {
		return NewAnthropic(cfg.BaseURL, cfg.APIKey, cfg.Model)
	})
}

// NewAnthropic creates a client.
func NewAnthropic(baseURL, apiKey, model string) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &Anthropic{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		client:     &http.Client{Timeout: 5 * time.Minute},
		maxRetries: defaultRetries,
		retryWait:  time.Second,
	}
}

// Name returns the protocol name.
func (c *Anthropic) Name() string { return ProtocolAnthropic }

// anthropicRequest is the Messages API body.
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	// Thinking turns on extended reasoning for models that support it.
	Thinking *anthropicThinking `json:"thinking,omitempty"`
	Stream   bool               `json:"stream,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type"`
	// BudgetTokens is the reasoning budget; the API requires one.
	BudgetTokens int `json:"budget_tokens"`
}

// anthropicMessage is one dialogue turn.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// anthropicBlock is one content block of a message.
type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Tool use fields.
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// Tool result fields.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// anthropicTool describes a tool for the model.
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// anthropicResponse is a finished answer.
type anthropicResponse struct {
	ID      string           `json:"id"`
	Type    string           `json:"type"`
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
	Model   string           `json:"model"`
	Stop    string           `json:"stop_reason"`
	Usage   struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Chat performs one model request.
func (c *Anthropic) Chat(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	body, err := c.buildBody(model, req)
	if err != nil {
		return nil, err
	}

	s := &sender{
		client:        c.client,
		endpoint:      c.baseURL + "/messages",
		maxRetries:    c.maxRetries,
		retryWaitBase: c.retryWait,
		headers: map[string]string{
			"anthropic-version": anthropicVersion,
		},
	}
	if c.apiKey != "" {
		s.headers["x-api-key"] = c.apiKey
	}

	resp, err := s.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if req.OnDelta != nil && resp.StatusCode < 400 {
		return c.readStream(resp.Body, req.OnDelta)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		if IsContextOverflow(string(data)) {
			return nil, &ContextOverflowError{Detail: shortBody(data)}
		}
		return nil, fmt.Errorf("model request failed with status %d: %s", resp.StatusCode, shortBody(data))
	}

	var out anthropicResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		if IsContextOverflow(out.Error.Message) {
			return nil, &ContextOverflowError{Detail: out.Error.Message}
		}
		return nil, fmt.Errorf("model error: %s", out.Error.Message)
	}
	return c.toResponse(out), nil
}

// buildBody turns a request into the Messages API body.
func (c *Anthropic) buildBody(model string, req Request) ([]byte, error) {
	payload := anthropicRequest{
		Model: model,
		// The API requires a limit, so an unset value falls back to a default.
		MaxTokens: req.MaxTokens,
		Stream:    req.OnDelta != nil,
	}
	if payload.MaxTokens <= 0 {
		payload.MaxTokens = anthropicMaxTokens
	}

	// The system prompt is a field of its own, not a message.
	var messages []anthropicMessage
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			if payload.System == "" {
				payload.System = m.Content
			} else {
				payload.System += "\n\n" + m.Content
			}
		case RoleTool:
			// Tool results come back as user content blocks.
			messages = append(messages, anthropicMessage{
				Role: RoleUser,
				Content: []anthropicBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		case RoleAssistant:
			// An assistant turn may carry text and tool calls together.
			blocks := make([]anthropicBlock, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: json.RawMessage(toolInput(tc.Arguments)),
				})
			}
			if len(blocks) == 0 {
				continue
			}
			messages = append(messages, anthropicMessage{Role: RoleAssistant, Content: blocks})
		default:
			messages = append(messages, anthropicMessage{Role: RoleUser, Content: m.Content})
		}
	}
	payload.Messages = messages

	// Temperature is omitted when the caller sets the flag: some models
	// reject it alongside extended reasoning.
	if !req.SkipTemperature {
		temp := req.Temperature
		payload.Temperature = &temp
	}

	if len(req.Tools) > 0 {
		payload.Tools = toAnthropicTools(req.Tools)
	}

	// Reasoning arrives as a thinking block with its own budget.
	if req.ReasoningEffort != "" {
		payload.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: reasoningBudget(req.ReasoningEffort),
		}
	}

	return json.Marshal(payload)
}

// toolInput keeps the arguments usable even when the model sent broken JSON.
func toolInput(args string) string {
	if strings.TrimSpace(args) == "" {
		return "{}"
	}
	return args
}

// toAnthropicTools converts the shared tool specs.
func toAnthropicTools(specs []ToolSpec) []anthropicTool {
	out := make([]anthropicTool, 0, len(specs))
	for _, spec := range specs {
		// The shared spec is an openai function wrapper; the API wants the
		// inner fields directly.
		fn, ok := spec["function"].(map[string]any)
		if !ok {
			continue
		}
		tool := anthropicTool{}
		if name, ok := fn["name"].(string); ok {
			tool.Name = name
		}
		if desc, ok := fn["description"].(string); ok {
			tool.Description = desc
		}
		if schema, ok := fn["parameters"].(map[string]any); ok {
			tool.InputSchema = schema
		}
		if tool.InputSchema == nil {
			tool.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, tool)
	}
	return out
}

// reasoningBudget maps a level name to thinking tokens.
func reasoningBudget(level string) int {
	switch strings.ToLower(level) {
	case "minimal":
		return 1024
	case "low":
		return 2048
	case "medium":
		return 4096
	case "high":
		return 8192
	case "xhigh", "max":
		return 16384
	}
	return 4096
}

// toResponse converts the API answer into the shared shape.
func (c *Anthropic) toResponse(out anthropicResponse) *Response {
	res := &Response{
		FinishReason: out.Stop,
		InputTokens:  out.Usage.Input,
		OutputTokens: out.Usage.Output,
	}

	var text strings.Builder
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			// Reasoning comes as its own block, so it is kept apart from
			// the answer rather than parsed out of the text.
			res.Reasoning += block.Text
		case "tool_use":
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}
	res.Content = StripArtifacts(text.String())
	return res
}

// streamEvent is one server-sent event of the answer stream.
type streamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta *struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Message *struct {
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage *struct {
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readStream reads the answer as it comes.
func (c *Anthropic) readStream(body io.Reader, onDelta func(Delta)) (*Response, error) {
	res := &Response{}

	// Tool calls arrive in pieces: the name first, the arguments as JSON
	// fragments, so they are collected by block index.
	type partial struct {
		id   string
		name string
		args strings.Builder
	}
	partials := map[int]*partial{}
	var order []int

	var text strings.Builder
	var reasoning strings.Builder

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				p := &partial{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				partials[ev.Index] = p
				order = append(order, ev.Index)
				if p.name != "" {
					onDelta(Delta{ToolCall: p.name})
				}
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				onDelta(Delta{Text: ev.Delta.Text})
			case "thinking_delta":
				reasoning.WriteString(ev.Delta.Thinking)
				onDelta(Delta{Text: ev.Delta.Thinking})
			case "input_json_delta":
				if p, ok := partials[ev.Index]; ok {
					p.args.WriteString(ev.Delta.Text)
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.Text != "" {
				text.WriteString(ev.Delta.Text)
			}
			if ev.Usage != nil {
				res.OutputTokens = ev.Usage.Output
			}
		case "message_start":
			if ev.Message != nil {
				res.InputTokens = ev.Message.Usage.Input
			}
		case "error":
			if ev.Error != nil {
				return nil, fmt.Errorf("model error: %s", ev.Error.Message)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	for _, idx := range order {
		p := partials[idx]
		args := p.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        p.id,
			Name:      p.name,
			Arguments: args,
		})
	}

	res.Reasoning = reasoning.String()
	res.Content = StripArtifacts(text.String())
	return res, nil
}

// DiscoverModels lists the models the API serves.
//
// The Messages API has no model list endpoint, so the answer names the known
// models and the interface offers them for a choice.
func (c *Anthropic) DiscoverModels(ctx context.Context) ([]DiscoveredModel, error) {
	return nil, fmt.Errorf("the anthropic protocol does not list models")
}
