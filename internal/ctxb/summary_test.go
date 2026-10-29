package ctxb

import (
	"context"
	"testing"

	"github.com/yourok/cdx/internal/llm"
)

// TestEstimateTokensLatin checks the Latin estimate.
func TestEstimateTokensLatin(t *testing.T) {
	// 400 Latin characters give about 100 tokens.
	text := repeat('a', 400)
	got := EstimateTokens(text)
	if got < 90 || got > 120 {
		t.Fatalf("expected about 100 tokens, got %d", got)
	}
}

// TestEstimateTokensCyrillic checks the Cyrillic estimate.
func TestEstimateTokensCyrillic(t *testing.T) {
	// Cyrillic is denser: 200 characters give about 100 tokens.
	text := repeat('я', 200)
	got := EstimateTokens(text)
	if got < 90 || got > 120 {
		t.Fatalf("expected about 100 tokens, got %d", got)
	}
}

// TestEstimateTokensEmpty checks empty text.
func TestEstimateTokensEmpty(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// TestEstimateMessages checks the combined estimate.
func TestEstimateMessages(t *testing.T) {
	one := EstimateTokens("hello")
	two := EstimateTokens("привет")
	if got := EstimateMessages([]string{"hello", "привет"}); got != one+two {
		t.Fatalf("expected sum %d, got %d", one+two, got)
	}
	if got := EstimateMessages(nil); got != 0 {
		t.Fatalf("expected 0 for empty list, got %d", got)
	}
}

// TestSplitEmpty checks an empty history.
func TestSplitEmpty(t *testing.T) {
	plan := Split(nil, Options{MaxTokens: 1000})
	if len(plan.Tail) != 0 || len(plan.Head) != 0 || plan.Overflow {
		t.Fatalf("unexpected plan %+v", plan)
	}
}

// TestSplitFitsEverything checks that a short history is not cut.
func TestSplitFitsEverything(t *testing.T) {
	messages := makeMessages(5, 10)

	plan := Split(messages, Options{MaxTokens: 100000, RecentMessages: 20})
	if len(plan.Tail) != 5 {
		t.Fatalf("expected all messages in tail, got %d", len(plan.Tail))
	}
	if len(plan.Head) != 0 {
		t.Fatalf("expected empty head, got %d", len(plan.Head))
	}
}

// TestSplitKeepsRecent checks the tail limit.
func TestSplitKeepsRecent(t *testing.T) {
	messages := makeMessages(30, 10)

	plan := Split(messages, Options{
		MaxTokens:      100000,
		RecentMessages: 10,
		// The threshold is above the old message count: no head.
		SummarizeThreshold: 100,
	})
	if len(plan.Tail) != 10 {
		t.Fatalf("expected 10 tail messages, got %d", len(plan.Tail))
	}
	if len(plan.Head) != 0 {
		t.Fatalf("head must be dropped below threshold, got %d", len(plan.Head))
	}
}

// TestSplitProducesHead checks that the part to summarize is selected.
func TestSplitProducesHead(t *testing.T) {
	messages := makeMessages(30, 10)

	plan := Split(messages, Options{
		MaxTokens:          100000,
		RecentMessages:     10,
		SummarizeThreshold: 5,
	})
	if len(plan.Head) != 20 {
		t.Fatalf("expected 20 head messages, got %d", len(plan.Head))
	}
	if len(plan.Tail) != 10 {
		t.Fatalf("expected 10 tail messages, got %d", len(plan.Tail))
	}
}

// TestSplitShrinksTail checks shrinking the tail to the budget.
func TestSplitShrinksTail(t *testing.T) {
	// 1000 characters per message: even 20 do not fit the budget.
	messages := makeMessages(20, 1000)

	plan := Split(messages, Options{
		MaxTokens:          1200,
		RecentMessages:     20,
		RecentMessagesMin:  2,
		SummarizeThreshold: 1000,
	})
	if len(plan.Tail) >= 20 {
		t.Fatalf("tail must shrink, got %d", len(plan.Tail))
	}
	if len(plan.Tail) < 2 {
		t.Fatalf("tail must not go below minimum, got %d", len(plan.Tail))
	}
}

// TestSplitRespectsMinimum checks the lower bound of the tail.
func TestSplitRespectsMinimum(t *testing.T) {
	messages := makeMessages(50, 500)

	plan := Split(messages, Options{
		MaxTokens:          100,
		RecentMessages:     40,
		RecentMessagesMin:  3,
		SummarizeThreshold: 1000,
	})
	if len(plan.Tail) < 3 {
		t.Fatalf("tail must keep at least minimum messages, got %d", len(plan.Tail))
	}
}

// TestSplitOverflow checks the overflow flag.
func TestSplitOverflow(t *testing.T) {
	messages := makeMessages(20, 2000)

	plan := Split(messages, Options{
		MaxTokens:         500,
		RecentMessages:    20,
		RecentMessagesMin: 2,
	})
	if !plan.Overflow {
		t.Fatalf("expected overflow, got %+v", plan)
	}
}

// TestSplitFixedExceedsBudget checks overflow caused by the service blocks.
func TestSplitFixedExceedsBudget(t *testing.T) {
	plan := Split(makeMessages(5, 10), Options{MaxTokens: 100, Fixed: 200})
	if !plan.Overflow {
		t.Fatal("expected overflow when fixed blocks exceed the budget")
	}
}

// TestSplitRecentStrategySkipsHead checks the recent strategy.
func TestSplitRecentStrategySkipsHead(t *testing.T) {
	messages := makeMessages(30, 10)

	plan := Split(messages, Options{
		MaxTokens:          100000,
		RecentMessages:     5,
		SummarizeThreshold: 5,
		Strategy:           "recent",
	})
	if len(plan.Head) != 0 {
		t.Fatalf("recent strategy must not produce a head, got %d", len(plan.Head))
	}
	if len(plan.Tail) != 5 {
		t.Fatalf("expected 5 tail messages, got %d", len(plan.Tail))
	}
}

// makeMessages creates messages of the given size.
func makeMessages(count, size int) []llm.Message {
	text := repeat('a', size)
	out := make([]llm.Message, 0, count)
	for i := 0; i < count; i++ {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		out = append(out, llm.Message{Role: role, Content: text})
	}
	return out
}

// repeat returns a string of n repeated characters.
func repeat(r rune, n int) string {
	buf := make([]rune, n)
	for i := range buf {
		buf[i] = r
	}
	return string(buf)
}

// fakeProvider records requests and returns the given text.
type fakeProvider struct {
	response string
	err      error
	requests []llm.Request
}

func (f *fakeProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Response{Content: f.response}, nil
}

func (f *fakeProvider) Name() string { return "fake" }

// TestSummaryBuildsPrompt checks the content of the summarization request.
func TestSummaryBuildsPrompt(t *testing.T) {
	p := &fakeProvider{response: "краткое содержание"}
	s := &Summarizer{Provider: p, MaxTokens: 500}

	got, err := s.Summary(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "первый вопрос"},
		{Role: llm.RoleAssistant, Content: "первый ответ"},
	})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != "краткое содержание" {
		t.Fatalf("unexpected summary %q", got)
	}

	if len(p.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(p.requests))
	}
	prompt := p.requests[0].Messages[0].Content
	if !contains(prompt, "первый вопрос") || !contains(prompt, "первый ответ") {
		t.Fatalf("prompt must contain the conversation: %s", prompt)
	}
	if !contains(prompt, "Summarize") {
		t.Fatalf("prompt must contain the instruction: %s", prompt)
	}
	if p.requests[0].MaxTokens != 500 {
		t.Fatalf("expected max tokens 500, got %d", p.requests[0].MaxTokens)
	}
}

// TestSummaryDefaultMaxTokens checks the default value.
func TestSummaryDefaultMaxTokens(t *testing.T) {
	p := &fakeProvider{response: "x"}
	s := &Summarizer{Provider: p}

	if _, err := s.Summary(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "a"}}); err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if p.requests[0].MaxTokens != 2000 {
		t.Fatalf("expected default 2000, got %d", p.requests[0].MaxTokens)
	}
}

// TestSummaryEmptyInput checks an empty history.
func TestSummaryEmptyInput(t *testing.T) {
	s := &Summarizer{Provider: &fakeProvider{response: "x"}}

	got, err := s.Summary(context.Background(), nil)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty summary, got %q", got)
	}
}

// TestSummaryNoProvider checks a missing provider.
func TestSummaryNoProvider(t *testing.T) {
	s := &Summarizer{}
	if _, err := s.Summary(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "a"}}); err == nil {
		t.Fatal("expected error without provider")
	}
}

// TestSummaryProviderError checks a provider error.
func TestSummaryProviderError(t *testing.T) {
	s := &Summarizer{Provider: &fakeProvider{err: errTest}}
	if _, err := s.Summary(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "a"}}); err == nil {
		t.Fatal("expected provider error to be returned")
	}
}

// TestSummaryEmptyResponse checks an empty model reply.
func TestSummaryEmptyResponse(t *testing.T) {
	s := &Summarizer{Provider: &fakeProvider{response: "   "}}
	if _, err := s.Summary(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "a"}}); err == nil {
		t.Fatal("expected error for empty summary")
	}
}

// TestSummarySkipsEmptyContent checks that empty messages are skipped.
func TestSummarySkipsEmptyContent(t *testing.T) {
	p := &fakeProvider{response: "ok"}
	s := &Summarizer{Provider: p}

	_, err := s.Summary(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "   "},
		{Role: llm.RoleAssistant, Content: "текст"},
	})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if contains(p.requests[0].Messages[0].Content, "user:    ") {
		t.Fatal("empty messages must be skipped")
	}
}

// errTest is a test error.
var errTest = &testError{}

type testError struct{}

func (e *testError) Error() string { return "boom" }

// contains reports whether the substring is present.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
