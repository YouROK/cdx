package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenAIChat is an openai-chat protocol client.
type OpenAIChat struct {
	baseURL    string
	apiKey     string
	client     *http.Client
	model      string
	maxRetries int
	// retryWaitBase is the initial wait before a retry.
	retryWaitBase time.Duration
}

// NewOpenAIChat creates a client.
func NewOpenAIChat(baseURL, apiKey, model string) *OpenAIChat {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIChat{
		baseURL:       strings.TrimRight(baseURL, "/"),
		apiKey:        apiKey,
		model:         model,
		client:        &http.Client{Timeout: 5 * time.Minute},
		maxRetries:    defaultRetries,
		retryWaitBase: time.Second,
	}
}

// Name returns the protocol name.
func (c *OpenAIChat) Name() string { return "openai-chat" }

// chatRequest is built as a map because providers name the limit and
// reasoning fields differently.
type chatRequest map[string]any

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat performs one model request.
func (c *OpenAIChat) Chat(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}

	payload := chatRequest{"model": model}

	messages := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		cm := chatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			var ctc chatToolCall
			ctc.ID = tc.ID
			ctc.Type = "function"
			ctc.Function.Name = tc.Name
			ctc.Function.Arguments = tc.Arguments
			cm.ToolCalls = append(cm.ToolCalls, ctc)
		}
		messages = append(messages, cm)
	}
	payload["messages"] = messages

	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	if req.MaxTokens > 0 {
		field := req.MaxTokensField
		if field == "" {
			field = "max_tokens"
		}
		payload[field] = req.MaxTokens
	}
	// Zero is a valid temperature, so omission is controlled by the flag only.
	if !req.SkipTemperature {
		payload["temperature"] = req.Temperature
	}
	if req.ReasoningEffort != "" {
		payload["reasoning_effort"] = req.ReasoningEffort
	}

	// Streaming is requested only when the caller wants chunks; otherwise
	// the answer arrives whole.
	stream := req.OnDelta != nil
	if stream {
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	resp, err := c.do(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if stream && resp.StatusCode < 400 {
		return readStream(resp.Body, req.OnDelta)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Overflow is detected separately: the caller adds advice for it
		// instead of a generic error text.
		if IsContextOverflow(string(data)) {
			return nil, &ContextOverflowError{Detail: shortBody(data)}
		}
		return nil, fmt.Errorf("model request failed with status %d: %s", resp.StatusCode, shortBody(data))
	}

	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Error != nil {
		if IsContextOverflow(out.Error.Message) {
			return nil, &ContextOverflowError{Detail: out.Error.Message}
		}
		return nil, fmt.Errorf("model error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("model returned no choices")
	}

	choice := out.Choices[0]
	// Reasoning arrives inside content, so the finished answer is split off.
	reasoning, answer := SplitReasoning(choice.Message.Content)
	answer = StripArtifacts(answer)
	res := &Response{
		Reasoning:    reasoning,
		Content:      answer,
		FinishReason: choice.FinishReason,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}
	for _, tc := range choice.Message.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return res, nil
}

// defaultRetries is how many times a transient error is retried.
//
// Free gateways often answer 429 when busy: a single attempt almost always
// hits the limit, while several in a row wait out the load.
const defaultRetries = 10

// do sends the request and retries it on transient errors.
func (c *OpenAIChat) do(ctx context.Context, body []byte) (*http.Response, error) {
	var lastErr error
	wait := c.retryWaitBase
	if wait <= 0 {
		wait = time.Second
	}

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			// Growth is capped: otherwise by the tenth attempt the wait would
			// reach eight minutes and the turn timeout would hit first.
			wait *= 2
			if wait > maxRetryWait {
				wait = maxRetryWait
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, retryHint(resp))
			// The server may state its own retry delay: it wins over the
			// computed one, but is still capped.
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if sec, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && sec > 0 {
					delay := time.Duration(sec) * time.Second
					if delay > maxRetryWait {
						delay = maxRetryWait
					}
					wait = delay
				}
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("model request failed after %d attempts: %w", c.maxRetries, lastErr)
}

// retryHint extracts the error message for a retry.
func retryHint(resp *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		msg := payload.Error.Message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return msg
	}
	return shortBody(body)
}

// shortBody clips the response body for an error message.
func shortBody(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// DiscoveredModel is a model the provider lists.
//
// The provider does not always report the context and output limits: a zero
// means "not reported", and the caller substitutes its own default.
type DiscoveredModel struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"context_window,omitempty"`
	MaxTokens     int    `json:"max_tokens,omitempty"`
}

// modelsResponse is the model list answer in openai format.
type modelsResponse struct {
	Data []struct {
		ID            string `json:"id"`
		ContextLength int    `json:"context_length"`
		ContextWindow int    `json:"context_window"`
		MaxTokens     int    `json:"max_tokens"`
		TopProvider   struct {
			ContextLength int `json:"context_length"`
			MaxCompletion int `json:"max_completion_tokens"`
		} `json:"top_provider"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// DiscoverModels asks the provider for the available models.
//
// It uses GET /models, served by openai and compatible gateways. Context and
// output limits are taken from the answer when the provider reports them.
func (c *OpenAIChat) DiscoverModels(ctx context.Context) ([]DiscoveredModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("model list failed with status %d: %s", resp.StatusCode, shortBody(data))
	}

	var payload modelsResponse
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	if payload.Error != nil && payload.Error.Message != "" {
		return nil, fmt.Errorf("model list failed: %s", payload.Error.Message)
	}

	out := make([]DiscoveredModel, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		// Providers name the context field differently, so the first
		// non-empty one wins.
		ctxWindow := firstPositive(m.ContextLength, m.ContextWindow, m.TopProvider.ContextLength)
		maxTokens := firstPositive(m.MaxTokens, m.TopProvider.MaxCompletion)
		out = append(out, DiscoveredModel{
			ID:            m.ID,
			ContextWindow: ctxWindow,
			MaxTokens:     maxTokens,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// firstPositive returns the first positive value.
func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// streamChunk is one stream frame.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// readStream reads a streaming answer and assembles a plain Response.
//
// Tool calls arrive in fragments: the name in an early frame, the arguments
// in pieces after it, so they are joined by index.
func readStream(body io.Reader, onDelta func(Delta)) (*Response, error) {
	sc := bufio.NewScanner(body)
	// Tool arguments can be large, so the builder must not be capped.
	sc.Buffer(make([]byte, 64*1024), 4<<20)

	res := &Response{}
	var content strings.Builder
	// Arguments are keyed by index: several calls can arrive at once.
	args := map[int]*strings.Builder{}
	names := map[int]string{}
	ids := map[int]string{}
	order := []int{}
	announced := map[int]bool{}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			if IsContextOverflow(chunk.Error.Message) {
				return nil, &ContextOverflowError{Detail: chunk.Error.Message}
			}
			return nil, fmt.Errorf("model error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			res.InputTokens = chunk.Usage.PromptTokens
			res.OutputTokens = chunk.Usage.CompletionTokens
		}

		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				onDelta(Delta{Text: ch.Delta.Content})

				// A looping answer is cut here: the provider never closes
				// such a stream itself.
				if LooksLooping(content.String()) {
					return nil, errModelLooping
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				if _, seen := args[tc.Index]; !seen {
					args[tc.Index] = &strings.Builder{}
					order = append(order, tc.Index)
				}
				if tc.Function.Name != "" {
					// The name arrives once, before the arguments.
					names[tc.Index] = tc.Function.Name
					ids[tc.Index] = tc.ID
					if !announced[tc.Index] {
						announced[tc.Index] = true
						onDelta(Delta{ToolCall: tc.Function.Name})
					}
				}
				args[tc.Index].WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != "" {
				res.FinishReason = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	res.Reasoning, res.Content = SplitReasoning(content.String())
	res.Content = StripArtifacts(res.Content)
	for _, idx := range order {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        ids[idx],
			Name:      names[idx],
			Arguments: args[idx].String(),
		})
	}
	if res.Content == "" && len(res.ToolCalls) == 0 {
		return nil, fmt.Errorf("model returned no content")
	}
	return res, nil
}

// maxRetryWait caps the wait between retries.
const maxRetryWait = 10 * time.Second
