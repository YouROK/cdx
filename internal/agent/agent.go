// Package agent runs a single agent turn: a model request and tool calls.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/registry"
)

// Event is a turn event for the client.
type Event struct {
	Topic string         `json:"topic"`
	Data  map[string]any `json:"data"`
}

// ToolCallRecord is a completed tool call.
type ToolCallRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Output    string `json:"output"`
	OK        bool   `json:"ok"`
}

// Result is the turn outcome.
type Result struct {
	Result    string   `json:"result"`
	Error     *Failure `json:"error"`
	Model     string   `json:"model"`
	DurationM int64    `json:"duration_ms"`
	Tokens    Usage    `json:"tokens"`
	ToolsUsed []string `json:"tools_used"`
	// ToolCalls holds the calls in execution order.
	ToolCalls []ToolCallRecord `json:"-"`
}

// Usage is token spending.
type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// Failure is an error with a code.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (f *Failure) Error() string { return f.Message }

// overflowHint builds the advice for a context overflow.
//
// The provider names its own limit, so its message goes below the advice:
// without it the user cannot tell which limit was hit.
func overflowHint(detail string) string {
	const advice = "set the model context window so cdx compacts automatically, or run: cdx --compact"
	if detail == "" {
		return advice
	}
	return advice + "\nprovider said: " + detail
}

// EmitFunc receives turn events.
type EmitFunc func(Event)

// AskFunc asks the user for confirmation.
type AskFunc func(ctx context.Context, level, tool, summary string, options []string) (string, error)

// Options are the turn parameters.
type Options struct {
	Provider llm.Provider
	// Model is the model configuration name, used only in the report.
	Model       string
	MaxTokens   int
	Temperature float64
	// ReasoningEffort is the API value translated from the model level.
	ReasoningEffort string
	// MaxTokensField and SkipTemperature handle provider compatibility.
	MaxTokensField  string
	SkipTemperature bool
	Registry        *registry.Registry
	Perms           *perm.Manager
	Prompt          string
	CWD             string
	MaxIters        int
	ToolMaxByte     int
	Emit            EmitFunc
	Ask             AskFunc
	HasSession      bool
	// History holds messages from past turns, added after the system one.
	History []llm.Message
	// ShowThinking streams reasoning into progress events.
	ShowThinking bool
	// LLMTimeout limits one model request; zero means no limit.
	LLMTimeout time.Duration
}

// Run executes a turn and returns the outcome.
func Run(ctx context.Context, opts Options, prompt string) *Result {
	started := time.Now()
	res := &Result{Model: opts.Model, ToolsUsed: []string{}}

	emit := func(topic string, data map[string]any) {
		if opts.Emit != nil {
			opts.Emit(Event{Topic: topic, Data: data})
		}
	}

	messages := make([]llm.Message, 0, len(opts.History)+2)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: opts.Prompt})
	messages = append(messages, opts.History...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: prompt})

	maxIters := opts.MaxIters
	if maxIters <= 0 {
		maxIters = 50
	}

	step := 0
	for {
		if step >= maxIters {
			res.Error = &Failure{
				Code:    "max_iterations",
				Message: fmt.Sprintf("reached limit of %d tool steps", maxIters),
			}
			break
		}
		step++

		emit("progress.llm", map[string]any{"phase": "llm", "step": step, "model": opts.Model})

		// The model name is not sent: the provider knows its own id, and
		// opts.Model is only the configuration name for the report.
		// Reasoning is forwarded as it streams so the progress line can show
		// what the model is thinking about.
		var onDelta func(llm.Delta)
		if opts.ShowThinking && opts.Emit != nil {
			var mu sync.Mutex
			var tail strings.Builder
			onDelta = func(d llm.Delta) {
				mu.Lock()
				defer mu.Unlock()
				if d.ToolCall != "" {
					emit("progress.thinking", map[string]any{
						"text": "calling " + d.ToolCall,
					})
					return
				}
				if d.Text == "" {
					return
				}
				tail.WriteString(d.Text)
				text := thinkingTail(tail.String())
				if text != "" {
					emit("progress.thinking", map[string]any{"text": text})
				}
			}
		}

		// The request is time limited so a stalled provider cannot hold
		// the turn forever.
		reqCtx := ctx
		var cancelReq context.CancelFunc
		if opts.LLMTimeout > 0 {
			reqCtx, cancelReq = context.WithTimeout(ctx, opts.LLMTimeout)
		}
		resp, err := opts.Provider.Chat(reqCtx, llm.Request{
			Messages:        messages,
			Tools:           opts.Registry.LLMSpecs(),
			MaxTokens:       opts.MaxTokens,
			Temperature:     opts.Temperature,
			ReasoningEffort: opts.ReasoningEffort,
			MaxTokensField:  opts.MaxTokensField,
			SkipTemperature: opts.SkipTemperature,
			OnDelta:         onDelta,
		})
		if cancelReq != nil {
			cancelReq()
		}
		if err != nil {
			// Context overflow is not a transport failure: the user has to
			// compact the history and needs to know how.
			if errors.Is(err, llm.ErrContextOverflow) {
				detail := strings.TrimSpace(strings.TrimPrefix(err.Error(),
					"request exceeds the model context window:"))
				res.Error = &Failure{
					Code:    "context_overflow",
					Message: "request exceeds the model context window",
					Hint:    overflowHint(detail),
				}
				break
			}
			// Cancellation differs from failure: the turn was interrupted.
			if errors.Is(err, context.Canceled) {
				res.Error = &Failure{
					Code:    "canceled",
					Message: "request was interrupted",
				}
				break
			}
			if errors.Is(err, context.DeadlineExceeded) {
				res.Error = &Failure{
					Code:    "llm_timeout",
					Message: "the model did not answer in time",
					Hint:    "raise agent.llm_timeout_ms in the settings, or try another model",
				}
				break
			}
			res.Error = &Failure{Code: "llm_error", Message: err.Error()}
			break
		}

		res.Tokens.Input += resp.InputTokens
		res.Tokens.Output += resp.OutputTokens

		if len(resp.ToolCalls) == 0 {
			res.Result = resp.Content
			break
		}

		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, call := range resp.ToolCalls {
			output := runTool(ctx, opts, call, emit)
			res.ToolCalls = append(res.ToolCalls, ToolCallRecord{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: call.Arguments,
				Output:    output,
				OK:        !strings.HasPrefix(output, "error:"),
			})
			if !contains(res.ToolsUsed, call.Name) {
				res.ToolsUsed = append(res.ToolsUsed, call.Name)
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    output,
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}
	}

	res.DurationM = time.Since(started).Milliseconds()
	return res
}

// runTool parses arguments, checks permissions and runs the tool.
func runTool(ctx context.Context, opts Options, call llm.ToolCall, emit func(string, map[string]any)) string {
	tool, ok := opts.Registry.GetByLLMName(call.Name)
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Name)
	}

	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return fmt.Sprintf("error: arguments are not valid JSON: %v", err)
		}
	}
	args["__cwd"] = opts.CWD

	decision := opts.Perms.Check(tool.ID, tool.Level, call.Arguments)
	if decision.NeedAsk {
		if opts.Ask == nil {
			return fmt.Sprintf("error: permission denied for %s", tool.ID)
		}
		choice, err := opts.Ask(ctx, decision.Level, tool.ID, summaryOf(tool, args), perm.Options(opts.HasSession))
		if err != nil {
			return fmt.Sprintf("error: permission request failed: %v", err)
		}
		if choice == perm.ChoiceDeny {
			return fmt.Sprintf("error: permission denied by user for %s", tool.ID)
		}
		opts.Perms.Remember(decision.Level, tool.ID, choice)
	} else if !decision.Allowed {
		return fmt.Sprintf("error: permission denied for %s", tool.ID)
	}

	// The call is allowed, so nested plugin operations do not ask again.
	opts.Perms.GrantTurn()
	defer opts.Perms.EndTurn()

	emit("progress.tool", map[string]any{
		"phase":   "tool",
		"tool":    tool.LLMName,
		"summary": summaryOf(tool, args),
	})

	out, err := tool.Run(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	content := out.Content
	if opts.ToolMaxByte > 0 && len(content) > opts.ToolMaxByte {
		content = content[:opts.ToolMaxByte] + "\n[truncated]"
	}
	return content
}

// summaryOf describes the action in words, without the tool name.
//
// The user sees what will happen: "run: git log" reads better than
// "p_local_tools_run_command git log".
func summaryOf(t *registry.Tool, args map[string]any) string {
	if c, ok := args["command"].(string); ok && c != "" {
		return "run: " + oneLine(c)
	}
	if u, ok := args["url"].(string); ok && u != "" {
		return "fetch " + oneLine(u)
	}
	if p, ok := args["path"].(string); ok && p != "" {
		return t.LLMName + " " + oneLine(p)
	}
	if q, ok := args["pattern"].(string); ok && q != "" {
		return t.LLMName + " " + oneLine(q)
	}
	return t.LLMName
}

// oneLine collapses multiline text into a single line.
//
// Commands and URLs can be long and wrapped, but the prompt line has to
// stay single, otherwise the prompt breaks apart.
func oneLine(text string) string {
	// A trailing backslash continues the command; in a single line it is
	// not needed and reads as noise.
	text = strings.ReplaceAll(text, "\\\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, " \\ ", " ")
	const limit = 200
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return text
}

// contains reports whether the list holds the value.
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// thinkingTail keeps the tail of the reasoning.
//
// There is only one progress line, so the tail is shown: it creates the
// sense of live text. Newlines become spaces.
func thinkingTail(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	// Reasoning tags only get in the way on the progress line.
	text = strings.TrimPrefix(text, "<think>")
	text = strings.TrimSpace(text)

	const limit = 90
	if runes := []rune(text); len(runes) > limit {
		return "..." + string(runes[len(runes)-limit:])
	}
	return text
}
