package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/registry"
)

// fakeProvider replays a script: the first step asks for a tool, the second returns text.
type fakeProvider struct {
	calls int
	tool  string
	args  string
}

// Chat returns the preloaded sequence of answers.
func (f *fakeProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	f.calls++
	if f.calls == 1 {
		return &llm.Response{
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: f.tool, Arguments: f.args}},
		}, nil
	}
	return &llm.Response{Content: "готово", InputTokens: 10, OutputTokens: 5}, nil
}

// Name returns the provider name.
func (f *fakeProvider) Name() string { return "fake" }

// newOptions builds the turn parameters.
func newOptions(p llm.Provider) Options {
	cfg := config.Default()
	cfg.Perms.Edit = "allow"
	m, _ := perm.New(cfg)

	return Options{
		Provider: p,
		Model:    "fake",
		Registry: testRegistry(),
		Perms:    m,
		Prompt:   "system",
		CWD:      ".",
		MaxIters: 5,
	}
}

// testRegistry builds a registry with a tool that echoes its arguments.
//
// The core no longer ships tools, so the test declares its own.
func testRegistry() *registry.Registry {
	reg := registry.NewRegistry()
	_ = reg.Add(&registry.Tool{
		Spec: registry.Spec{
			ID:          "plugin.test.echo",
			LLMName:     "read_file",
			Description: "Echo arguments",
			Level:       registry.LevelRead,
			Schema:      map[string]any{"type": "object"},
		},
		Run: func(_ context.Context, args map[string]any) (registry.Result, error) {
			path, _ := args["path"].(string)
			if path == "no-such-file.txt" || path == "" {
				return registry.Result{Content: "error: не найдено"}, nil
			}
			return registry.Result{Content: "содержимое " + path, OK: true}, nil
		},
	})
	return reg
}

// TestToolLoop runs the full cycle with a tool call.
func TestToolLoop(t *testing.T) {
	p := &fakeProvider{tool: "read_file", args: `{"path":"agent.go","limit":2}`}

	var events []string
	opts := newOptions(p)
	opts.Emit = func(e Event) { events = append(events, e.Topic) }

	res := Run(context.Background(), opts, "прочитай файл")

	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}
	if res.Result != "готово" {
		t.Fatalf("unexpected result %q", res.Result)
	}
	if len(res.ToolsUsed) != 1 || res.ToolsUsed[0] != "read_file" {
		t.Fatalf("expected read_file usage, got %v", res.ToolsUsed)
	}
	if res.Tokens.Input != 10 || res.Tokens.Output != 5 {
		t.Fatalf("unexpected tokens %+v", res.Tokens)
	}
	if len(events) == 0 {
		t.Fatal("expected progress events")
	}
}

// TestUnknownTool checks an unknown tool does not break the turn.
func TestUnknownTool(t *testing.T) {
	p := &fakeProvider{tool: "no_such_tool", args: `{}`}
	opts := newOptions(p)

	res := Run(context.Background(), opts, "вызови неизвестный")
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}
	if res.Result != "готово" {
		t.Fatalf("expected fallback answer, got %q", res.Result)
	}
}

// TestPermissionDenied checks a denial without asking the user.
func TestPermissionDenied(t *testing.T) {
	p := &fakeProvider{tool: "write_file", args: `{"path":"out.txt","content":"x"}`}

	cfg := config.Default()
	cfg.Perms.Edit = "deny"
	m, _ := perm.New(cfg)

	opts := Options{
		Provider: p, Model: "fake", Registry: testRegistry(), Perms: m,
		Prompt: "system", CWD: ".", MaxIters: 5,
	}

	res := Run(context.Background(), opts, "запиши файл")
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}
}

// TestMaxIterations checks the step limit.
func TestMaxIterations(t *testing.T) {
	p := &endlessProvider{}
	opts := newOptions(p)
	opts.MaxIters = 2

	res := Run(context.Background(), opts, "цикл")
	if res.Error == nil || res.Error.Code != "max_iterations" {
		t.Fatalf("expected max_iterations error, got %+v", res.Error)
	}
}

// endlessProvider always asks for a tool.
type endlessProvider struct{}

func (e *endlessProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{ToolCalls: []llm.ToolCall{{
		ID: "c", Name: "read_file", Arguments: `{"path":"agent.go"}`,
	}}}, nil
}

func (e *endlessProvider) Name() string { return "endless" }

// TestBuildPrompt substitutes the environment.
func TestBuildPrompt(t *testing.T) {
	cfg := config.Default()
	out := BuildPrompt(StandardPrompt, cfg, "/work", "/work/root")

	if strings.Contains(out, "{{cwd}}") {
		t.Fatal("cwd placeholder must be replaced")
	}
	if !strings.Contains(out, "/work") {
		t.Fatal("cwd value must appear in prompt")
	}
}

// TestFailureJSON checks error serialization.
func TestFailureJSON(t *testing.T) {
	f := &Failure{Code: "x", Message: "y"}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"code":"x"`) {
		t.Fatalf("unexpected json %s", data)
	}
	_ = fmt.Sprint(f)
}

// historyProvider records the messages it receives.
type historyProvider struct {
	messages []llm.Message
}

func (h *historyProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	h.messages = req.Messages
	return &llm.Response{Content: "ok"}, nil
}

func (h *historyProvider) Name() string { return "history" }

// TestHistoryGoesBeforePrompt checks the order: system, history, request.
func TestHistoryGoesBeforePrompt(t *testing.T) {
	p := &historyProvider{}
	opts := newOptions(p)
	opts.History = []llm.Message{
		{Role: llm.RoleUser, Content: "прошлый вопрос"},
		{Role: llm.RoleAssistant, Content: "прошлый ответ"},
	}

	if res := Run(context.Background(), opts, "новый вопрос"); res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}

	if len(p.messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(p.messages))
	}
	if p.messages[0].Role != llm.RoleSystem {
		t.Fatalf("first message must be system, got %q", p.messages[0].Role)
	}
	if p.messages[1].Content != "прошлый вопрос" {
		t.Fatalf("history must follow system, got %+v", p.messages[1])
	}
	if p.messages[3].Content != "новый вопрос" {
		t.Fatalf("prompt must be last, got %+v", p.messages[3])
	}
}

// TestToolCallsRecorded checks that completed calls are recorded.
func TestToolCallsRecorded(t *testing.T) {
	p := &fakeProvider{tool: "read_file", args: `{"path":"agent.go","limit":2}`}
	opts := newOptions(p)

	res := Run(context.Background(), opts, "прочитай")
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected one recorded call, got %d", len(res.ToolCalls))
	}

	call := res.ToolCalls[0]
	if call.Name != "read_file" {
		t.Fatalf("unexpected tool name %q", call.Name)
	}
	if call.ID != "c1" {
		t.Fatalf("unexpected call id %q", call.ID)
	}
	if !call.OK {
		t.Fatalf("successful call must be marked ok: %+v", call)
	}
	if call.Output == "" {
		t.Fatal("output must be recorded")
	}
}

// TestFailedToolCallMarked checks a failed call is marked.
func TestFailedToolCallMarked(t *testing.T) {
	p := &fakeProvider{tool: "read_file", args: `{"path":"no-such-file.txt"}`}
	opts := newOptions(p)

	res := Run(context.Background(), opts, "прочитай")
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected one recorded call, got %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].OK {
		t.Fatalf("failing call must be marked not ok: %+v", res.ToolCalls[0])
	}
}

// TestProviderGetsModelSettings checks the model settings are passed.
type settingsProvider struct {
	got llm.Request
}

func (s *settingsProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.got = req
	return &llm.Response{Content: "ok"}, nil
}

func (s *settingsProvider) Name() string { return "settings" }

// TestRequestCarriesSettings checks the settings reach the provider.
func TestRequestCarriesSettings(t *testing.T) {
	p := &settingsProvider{}
	opts := newOptions(p)
	opts.Temperature = 0.3
	opts.ReasoningEffort = "high"
	opts.MaxTokensField = "max_completion_tokens"
	opts.SkipTemperature = true

	if res := Run(context.Background(), opts, "x"); res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error.Message)
	}

	if p.got.ReasoningEffort != "high" {
		t.Fatalf("reasoning effort must be passed, got %q", p.got.ReasoningEffort)
	}
	if p.got.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("max tokens field must be passed, got %q", p.got.MaxTokensField)
	}
	if !p.got.SkipTemperature {
		t.Fatal("skip temperature flag must be passed")
	}
	if p.got.Temperature != 0.3 {
		t.Fatalf("temperature must be passed, got %v", p.got.Temperature)
	}
}
