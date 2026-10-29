//go:build live

// Package live holds integration checks against real models.
//
// Run: CDX_LIVE_API_KEY=... go test -tags live ./test/live/
// Without a key the tests are skipped.
package live

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/ctxb"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/registry"
)

// baseURL is the default provider address.
const baseURL = "https://inference.dahl.global/v1"

// Provider models.
const (
	modelMiniMax  = "MiniMaxAI/MiniMax-M2.7"
	modelDeepSeek = "deepseek-ai/DeepSeek-V4-Flash-0731"
	modelGLM      = "zai-org/GLM-5.3-Flash"
)

// config describes a model under test.
type modelConfig struct {
	Name    string
	Remote  string
	MaxOut  int
	Reason  map[string]string
	Default string
}

// liveModels returns the models to run against.
func liveModels() []modelConfig {
	return []modelConfig{
		{
			Name: "minimax", Remote: modelMiniMax, MaxOut: 8192,
			Reason:  map[string]string{"low": "low", "medium": "medium", "high": "high"},
			Default: "medium",
		},
		{Name: "deepseek", Remote: modelDeepSeek, MaxOut: 4096},
		{Name: "glm", Remote: modelGLM, MaxOut: 8192},
	}
}

// apiKey returns the key or skips the test.
func apiKey(t *testing.T) string {
	t.Helper()

	key := os.Getenv("CDX_LIVE_API_KEY")
	if key == "" {
		t.Skip("CDX_LIVE_API_KEY is not set")
	}
	return key
}

// provider creates the model client.
func provider(t *testing.T, mc modelConfig) (llm.Provider, agent.Options) {
	t.Helper()

	key := apiKey(t)
	p := llm.NewOpenAIChat(baseURL, key, mc.Remote)

	cfg := configDefaults()
	reg := registry.NewRegistry()

	pm, err := perm.New(cfg)
	if err != nil {
		t.Fatalf("perm: %v", err)
	}

	reasoning := ""
	if mc.Default != "" {
		if wire, ok := mc.Reason[mc.Default]; ok {
			reasoning = wire
		}
	}

	opts := agent.Options{
		Provider:        p,
		Model:           mc.Name,
		MaxTokens:       mc.MaxOut,
		ReasoningEffort: reasoning,
		Registry:        reg,
		Perms:           pm,
		Prompt:          "You are a test assistant. Answer briefly in the language of the request.",
		CWD:             t.TempDir(),
		MaxIters:        8,
		ToolMaxByte:     8192,
	}
	return p, opts
}

// TestModelsAnswer checks that every model answers a simple question.
func TestModelsAnswer(t *testing.T) {
	for _, mc := range liveModels() {
		mc := mc
		t.Run(mc.Name, func(t *testing.T) {
			_, opts := provider(t, mc)

			res := agent.Run(t.Context(), opts, "Reply with the single word: pong")
			if res.Error != nil {
				t.Fatalf("model error: %s", res.Error.Message)
			}
			if strings.TrimSpace(res.Result) == "" {
				t.Fatal("model returned an empty answer")
			}
			if res.Tokens.Input == 0 {
				t.Fatal("usage must report input tokens")
			}
		})
	}
}

// TestModelsCallTool checks a tool call on every model.
func TestModelsCallTool(t *testing.T) {
	for _, mc := range liveModels() {
		mc := mc
		t.Run(mc.Name, func(t *testing.T) {
			_, opts := provider(t, mc)

			// Allow reads: the file lives in a temporary directory.
			path := opts.CWD + "/probe.txt"
			if err := os.WriteFile(path, []byte("маркер-проверки\n"), 0o644); err != nil {
				t.Fatalf("write probe: %v", err)
			}

			res := agent.Run(t.Context(), opts,
				"Read the file probe.txt and reply with the exact text it contains.")
			if res.Error != nil {
				t.Fatalf("model error: %s", res.Error.Message)
			}
			if len(res.ToolsUsed) == 0 {
				t.Fatalf("model did not use any tool, answer: %q", res.Result)
			}
			if !strings.Contains(res.Result, "маркер") {
				t.Fatalf("answer must contain file content, got %q", res.Result)
			}
		})
	}
}

// TestModelsRejectUnknownTool checks that an unknown tool does not break the turn.
func TestModelsRejectUnknownTool(t *testing.T) {
	for _, mc := range liveModels() {
		mc := mc
		t.Run(mc.Name, func(t *testing.T) {
			_, opts := provider(t, mc)
			opts.Registry = registry.NewRegistry()

			res := agent.Run(t.Context(), opts, "Say the word: ok")
			if res.Error != nil {
				t.Fatalf("model error: %s", res.Error.Message)
			}
			if strings.TrimSpace(res.Result) == "" {
				t.Fatal("model must answer even without tools")
			}
		})
	}
}

// TestModelsRespectDeniedPermission checks a permission denial.
func TestModelsRespectDeniedPermission(t *testing.T) {
	mc := liveModels()[1] // deepseek: быстрее и стабильнее
	_, opts := provider(t, mc)

	cfg := configDefaults()
	cfg.Perms.Edit = "deny"
	pm, err := perm.New(cfg)
	if err != nil {
		t.Fatalf("perm: %v", err)
	}
	opts.Perms = pm

	res := agent.Run(t.Context(), opts,
		"Create a file named forbidden.txt with the text test.")
	if res.Error != nil {
		t.Fatalf("model error: %s", res.Error.Message)
	}
	if _, err := os.Stat(opts.CWD + "/forbidden.txt"); err == nil {
		t.Fatal("file must not be created when permission is denied")
	}
}

// TestContextSummarization checks compression on a live model.
func TestContextSummarization(t *testing.T) {
	mc := liveModels()[1]
	p, _ := provider(t, mc)

	summarizer := &ctxb.Summarizer{Provider: p, MaxTokens: 300}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "We decided to use Go and BoltDB for storage."},
		{Role: llm.RoleAssistant, Content: "Understood. BoltDB it is."},
		{Role: llm.RoleUser, Content: "Also the CLI must stay a single binary."},
		{Role: llm.RoleAssistant, Content: "Agreed, one binary."},
	}

	summary, err := summarizer.Summary(t.Context(), messages)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary == "" {
		t.Fatal("summary must not be empty")
	}

	lower := strings.ToLower(summary)
	if !strings.Contains(lower, "bolt") && !strings.Contains(lower, "go") {
		t.Fatalf("summary must keep key facts, got %q", summary)
	}
}

// TestTokenEstimateSanity checks that the estimate is not wildly off.
func TestTokenEstimateSanity(t *testing.T) {
	mc := liveModels()[1]
	p, _ := provider(t, mc)

	text := strings.Repeat("Это проверка оценки токенов. ", 50)
	resp, err := p.Chat(t.Context(), llm.Request{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: text + " Reply with one word."}},
		MaxTokens: 20,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	estimated := ctxb.EstimateTokens(text)
	actual := resp.InputTokens
	if actual == 0 {
		t.Skip("provider did not report usage")
	}

	// The estimate may differ, but not by orders of magnitude.
	ratio := float64(estimated) / float64(actual)
	if ratio < 0.4 || ratio > 3 {
		t.Fatalf("estimate %d is too far from actual %d (ratio %.2f)", estimated, actual, ratio)
	}
}

// TestUsageMatchesContent checks that the token usage is filled in.
func TestUsageMatchesContent(t *testing.T) {
	for _, mc := range liveModels() {
		mc := mc
		t.Run(mc.Name, func(t *testing.T) {
			p, _ := provider(t, mc)

			resp, err := p.Chat(t.Context(), llm.Request{
				Messages:  []llm.Message{{Role: llm.RoleUser, Content: "Say: hello"}},
				MaxTokens: 50,
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if resp.InputTokens == 0 || resp.OutputTokens == 0 {
				t.Fatalf("usage must be reported, got in=%d out=%d",
					resp.InputTokens, resp.OutputTokens)
			}
		})
	}
}

// TestToolCallShape checks the shape of a tool call from the model.
func TestToolCallShape(t *testing.T) {
	mc := liveModels()[1]
	_, opts := provider(t, mc)

	// A file with unique content: the answer cannot guess it unread.
	marker := "уникальный-маркер-7f3a"
	if err := os.WriteFile(opts.CWD+"/probe.txt", []byte(marker+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var calls []agent.ToolCallRecord
	opts.Emit = func(e agent.Event) {}

	res := agent.Run(t.Context(), opts, "Read probe.txt and print its content exactly.")
	if res.Error != nil {
		t.Fatalf("error: %s", res.Error.Message)
	}
	calls = res.ToolCalls

	if len(calls) == 0 {
		t.Fatalf("expected a tool call, answer: %q", res.Result)
	}

	call := calls[0]
	if call.ID == "" {
		t.Fatal("tool call must carry an id")
	}
	if call.Name == "" {
		t.Fatal("tool call must carry a name")
	}
	if !json.Valid([]byte(call.Arguments)) {
		t.Fatalf("arguments must be valid JSON, got %q", call.Arguments)
	}
}
