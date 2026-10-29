package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/agent"
	"github.com/yourok/cdx/internal/ctxb"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/sessions"
)

// TestToLLMSkipsToolCalls checks tool calls stay out of the context.
func TestToLLMSkipsToolCalls(t *testing.T) {
	out, ok := toLLM(sessions.Message{Kind: sessions.KindToolCall, ToolName: "read_file"}, true)
	if ok || len(out) != 0 {
		t.Fatalf("tool_call must be skipped, got %+v", out)
	}
}

// TestToLLMToolResult checks tool results are passed on.
func TestToLLMToolResult(t *testing.T) {
	msg := sessions.Message{
		Kind: sessions.KindToolResult, ToolName: "read_file",
		ToolCallID: "c1", Content: "содержимое",
	}

	out, ok := toLLM(msg, true)
	if !ok || len(out) != 1 {
		t.Fatalf("expected one message, got %+v ok=%v", out, ok)
	}
	if out[0].Role != llm.RoleTool || out[0].ToolCallID != "c1" {
		t.Fatalf("unexpected message %+v", out[0])
	}

	// With keep_tool_results=false results are dropped.
	if _, ok := toLLM(msg, false); ok {
		t.Fatal("tool result must be dropped when keep_tool_results is false")
	}
}

// TestToLLMSkipsErrors checks errors do not reach the context.
func TestToLLMSkipsErrors(t *testing.T) {
	if _, ok := toLLM(sessions.Message{Kind: sessions.KindError, Content: "сбой"}, true); ok {
		t.Fatal("error must not go into context")
	}
}

// TestLastSummary checks the latest summary is picked.
func TestLastSummary(t *testing.T) {
	all := []sessions.Message{
		{Seq: 1, Kind: sessions.KindUser},
		{Seq: 2, Kind: sessions.KindSummary, Content: "старая", CoversTo: 1},
		{Seq: 3, Kind: sessions.KindAssistant},
		{Seq: 4, Kind: sessions.KindSummary, Content: "новая", CoversTo: 3},
	}

	got := lastSummary(all)
	if got == nil || got.Content != "новая" {
		t.Fatalf("expected latest summary, got %+v", got)
	}
	if got := lastSummary([]sessions.Message{{Seq: 1, Kind: sessions.KindUser}}); got != nil {
		t.Fatalf("expected nil when no summary, got %+v", got)
	}
}

// TestRecordTurnStoresMessages checks a turn is written to history.
func TestRecordTurnStoresMessages(t *testing.T) {
	d := newTestDaemon(t)
	s, err := d.sess.Create(t.TempDir(), "тест")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res := &agent.Result{
		Result:    "ответ",
		Tokens:    agent.Usage{Input: 10, Output: 5},
		ToolsUsed: []string{"read_file"},
		ToolCalls: []agent.ToolCallRecord{
			{ID: "c1", Name: "read_file", Arguments: `{"path":"a"}`, Output: "данные", OK: true},
		},
	}

	d.recordTurn(s, "вопрос", res, "mock", "hash1")

	msgs, err := d.sess.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	// user, assistant, tool_call, tool_result.
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Kind != sessions.KindUser || msgs[0].Content != "вопрос" {
		t.Fatalf("first message must be the question, got %+v", msgs[0])
	}
	if msgs[1].Kind != sessions.KindAssistant || msgs[1].Content != "ответ" {
		t.Fatalf("second message must be the answer, got %+v", msgs[1])
	}
	if msgs[2].Kind != sessions.KindToolCall || msgs[2].ToolName != "read_file" {
		t.Fatalf("third message must be the tool call, got %+v", msgs[2])
	}
	if msgs[3].Kind != sessions.KindToolResult || !msgs[3].OK {
		t.Fatalf("fourth message must be the tool result, got %+v", msgs[3])
	}
}

// TestRecordTurnSkipsFailure checks a failure is not recorded.
//
// Otherwise the model would see its own error as an answer and start
// commenting on it in the next call.
func TestRecordTurnSkipsFailure(t *testing.T) {
	d := newTestDaemon(t)
	s, _ := d.sess.Create(t.TempDir(), "тест")

	res := &agent.Result{Error: &agent.Failure{Code: "llm_error", Message: "нет связи"}}
	d.recordTurn(s, "вопрос", res, "mock", "hash1")

	msgs, err := d.sess.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("failure must not be recorded, got %+v", msgs)
	}
}

// TestRecordTurnKeepsPartialAnswer checks an answer with an error is kept.
//
// A partial answer is still real output, so it stays in the history.
func TestRecordTurnKeepsPartialAnswer(t *testing.T) {
	d := newTestDaemon(t)
	s, _ := d.sess.Create(t.TempDir(), "тест")

	res := &agent.Result{
		Result: "частичный ответ",
		Error:  &agent.Failure{Code: "max_iterations", Message: "лимит шагов"},
	}
	d.recordTurn(s, "вопрос", res, "mock", "hash1")

	msgs, err := d.sess.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Content != "частичный ответ" {
		t.Fatalf("partial answer must be kept, got %q", msgs[1].Content)
	}
}

// TestLoadHistoryUsesRecent checks history is limited to the recent tail.
//
// The model context window enables compaction; without it all history is used.
func TestLoadHistoryUsesRecent(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Context.RecentMessages = 2
	s, _ := d.sess.Create(t.TempDir(), "тест")

	for _, text := range []string{"раз", "два", "три"} {
		if _, err := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: text}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	history := d.loadHistory(s, 0, 100000)
	if len(history) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(history))
	}
	if history[0].Content != "два" || history[1].Content != "три" {
		t.Fatalf("unexpected history %+v", history)
	}
}

// TestLoadHistoryWithoutWindowKeepsAll checks running without a context window.
//
// While the window is unknown the history is not shortened: the program
// runs until the provider reports an overflow.
func TestLoadHistoryWithoutWindowKeepsAll(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Context.RecentMessages = 1
	s, _ := d.sess.Create(t.TempDir(), "тест")

	for _, text := range []string{"раз", "два", "три"} {
		if _, err := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: text}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	history := d.loadHistory(s, 0, 0)
	if len(history) != 3 {
		t.Fatalf("expected the whole history without a window, got %d", len(history))
	}
}

// TestLoadHistoryNilSession checks there is no history without a session.
func TestLoadHistoryNilSession(t *testing.T) {
	d := newTestDaemon(t)
	if got := d.loadHistory(nil, 0, 0); got != nil {
		t.Fatalf("expected nil history, got %+v", got)
	}
}

// TestLoadHistorySummaryReplacesOld checks the summary substitutes old messages.
func TestLoadHistorySummaryReplacesOld(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Context.RecentMessages = 1
	s, _ := d.sess.Create(t.TempDir(), "тест")

	// Three old messages and a summary covering them.
	for _, text := range []string{"старое1", "старое2", "старое3"} {
		if _, err := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: text}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := d.sess.Append(s.ID, sessions.Message{
		Kind: sessions.KindSummary, Content: "кратко", CoversFrom: 1, CoversTo: 3,
	}); err != nil {
		t.Fatalf("Append summary: %v", err)
	}
	if _, err := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: "свежее"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	history := d.loadHistory(s, 0, 0)
	if len(history) != 2 {
		t.Fatalf("expected summary and one message, got %d: %+v", len(history), history)
	}
	if history[0].Role != llm.RoleSystem {
		t.Fatalf("first entry must be the summary, got %+v", history[0])
	}
	if history[1].Content != "свежее" {
		t.Fatalf("last entry must be the fresh message, got %+v", history[1])
	}
}

// summaryProvider returns a ready-made summary.
type summaryProvider struct {
	calls int
}

func (s *summaryProvider) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	s.calls++
	return &llm.Response{Content: "сводка: обсуждали файлы"}, nil
}

func (s *summaryProvider) Name() string { return "summary" }

// TestSummarizeStoresSummary checks a summary is stored in the session.
func TestSummarizeStoresSummary(t *testing.T) {
	d := newTestDaemon(t)
	provider := &summaryProvider{}
	d.summarizer = &ctxb.Summarizer{Provider: provider, MaxTokens: 100}

	s, _ := d.sess.Create(t.TempDir(), "тест")
	head := []llm.Message{
		{Role: llm.RoleUser, Content: "старое"},
		{Role: llm.RoleAssistant, Content: "ответ"},
	}
	source := []sessions.Message{
		{Seq: 1, Kind: sessions.KindUser},
		{Seq: 2, Kind: sessions.KindAssistant},
	}

	text, ok := d.summarize(s, head, source)
	if !ok {
		t.Fatal("expected summary to be created")
	}
	if text != "сводка: обсуждали файлы" {
		t.Fatalf("unexpected summary %q", text)
	}

	msgs, err := d.sess.Messages(s.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Kind != sessions.KindSummary {
		t.Fatalf("summary must be stored, got %+v", msgs)
	}
	if msgs[0].CoversFrom != 1 || msgs[0].CoversTo != 2 {
		t.Fatalf("coverage range must be stored, got %d-%d", msgs[0].CoversFrom, msgs[0].CoversTo)
	}
}

// TestSummarizeWithoutProvider checks running without a summarizer.
func TestSummarizeWithoutProvider(t *testing.T) {
	d := newTestDaemon(t)
	s, _ := d.sess.Create(t.TempDir(), "тест")

	if _, ok := d.summarize(s, []llm.Message{{Role: llm.RoleUser, Content: "x"}}, nil); ok {
		t.Fatal("expected no summary without provider")
	}
}

// TestSummarizeEmptyHead checks an empty head.
func TestSummarizeEmptyHead(t *testing.T) {
	d := newTestDaemon(t)
	d.summarizer = &ctxb.Summarizer{Provider: &summaryProvider{}, MaxTokens: 100}
	s, _ := d.sess.Create(t.TempDir(), "тест")

	if _, ok := d.summarize(s, nil, nil); ok {
		t.Fatal("expected no summary for empty head")
	}
}

// TestLoadHistoryCoversSummary checks covered messages are not repeated.
func TestLoadHistoryCoversSummary(t *testing.T) {
	d := newTestDaemon(t)
	d.cfg.Context.RecentMessages = 10
	d.cfg.Context.SummarizeThreshold = 100
	s, _ := d.sess.Create(t.TempDir(), "тест")

	// Four messages and a summary covering the first two.
	first, _ := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: "старое1"})
	second, _ := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindAssistant, Content: "старое2"})
	if _, err := d.sess.Append(s.ID, sessions.Message{
		Kind: sessions.KindSummary, Content: "сжато",
		CoversFrom: first.Seq, CoversTo: second.Seq,
	}); err != nil {
		t.Fatalf("Append summary: %v", err)
	}
	if _, err := d.sess.Append(s.ID, sessions.Message{Kind: sessions.KindUser, Content: "новое1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	history := d.loadHistory(s, 0, 0)
	if len(history) != 2 {
		t.Fatalf("expected summary and one message, got %d: %+v", len(history), history)
	}
	if history[0].Role != llm.RoleSystem || !strings.Contains(history[0].Content, "сжато") {
		t.Fatalf("first entry must be the stored summary, got %+v", history[0])
	}
	if history[1].Content != "новое1" {
		t.Fatalf("covered messages must not repeat, got %+v", history[1])
	}
}

// TestToolsTokens checks the tool size estimate.
func TestToolsTokens(t *testing.T) {
	d := newTestDaemon(t)
	if got := d.toolsTokens(); got <= 0 {
		t.Fatalf("expected positive token estimate, got %d", got)
	}
}
