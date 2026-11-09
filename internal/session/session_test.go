package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/question"
	"github.com/yourok/cdx/internal/store"
)

// stepAnswers is a fake asker that returns scripted answers for client questions.
type stepAnswers struct {
	questions []question.Question
	answers   []question.Answer
}

func (a *stepAnswers) Ask(_ context.Context, q question.Question) (question.Answer, error) {
	a.questions = append(a.questions, q)
	if len(a.answers) == 0 {
		return question.Answer{Cancelled: true}, nil
	}
	answer := a.answers[0]
	a.answers = a.answers[1:]
	return answer, nil
}

func setupTestOrchestrator(t *testing.T) (*Orchestrator, *bus.Bus, *plugin.Registry, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(store.Options{
		DBPath:        filepath.Join(dir, "cdx.db"),
		MasterKeyPath: filepath.Join(dir, "master.key"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	eb := bus.New()
	reg := plugin.NewRegistry()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	orch := NewOrchestrator(st, eb, reg, logx.New())
	orch.nowFn = func() time.Time { return now }
	return orch, eb, reg, &now
}

func TestSessionAutoTempExpirationAndMode(t *testing.T) {
	orch, _, reg, now := setupTestOrchestrator(t)

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{
				Content: "reply to count=" + string(rune('0'+len(req.Messages))),
			}, nil
		},
	})

	workdir := "/home/user/proj"

	// 1. Default run creates a temporary session.
	res1, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    ModeAuto,
		Prompt:  "First question",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn 1 failed: %v", err)
	}
	if res1.Session.Kind != KindTemp || res1.Session.MessageCount != 2 {
		t.Fatalf("expected temp session with 2 messages, got %+v", res1.Session)
	}
	firstID := res1.Session.ID

	// 2. Second run 1 hour later reuses the same temp session.
	*now = now.Add(1 * time.Hour)
	res2, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    ModeAuto,
		Prompt:  "Follow-up question",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn 2 failed: %v", err)
	}
	if res2.Session.ID != firstID || res2.Session.MessageCount != 4 {
		t.Fatalf("expected same temp session with 4 messages, got %+v", res2.Session)
	}

	// 3. Stateless run does not read or modify the active session.
	resNo, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    "no",
		Prompt:  "Stateless question",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn stateless failed: %v", err)
	}
	if resNo.Session.Kind != KindNone {
		t.Fatalf("expected stateless session kind=none, got %+v", resNo.Session)
	}
	msgsAfterNo, _ := orch.GetMessages(firstID)
	if len(msgsAfterNo) != 4 {
		t.Fatalf("stateless turn should not touch temp session history, got %d messages", len(msgsAfterNo))
	}

	// 4. Advance clock by 25 hours (> 24h default TTL): temp session expires and a fresh one is created.
	*now = now.Add(25 * time.Hour)
	res3, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    ModeAuto,
		Prompt:  "Question next day",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn 3 failed: %v", err)
	}
	if res3.Session.ID == firstID || res3.Session.MessageCount != 2 {
		t.Fatalf("expected new temp session after 25h expiration, got %+v", res3.Session)
	}
	secondID := res3.Session.ID

	// 5. Switch the active session to persistent.
	resMode, err := orch.SetActiveSessionKind(workdir, KindPersistent)
	if err != nil {
		t.Fatalf("SetActiveSessionKind failed: %v", err)
	}
	if resMode.ID != secondID || resMode.Kind != KindPersistent {
		t.Fatalf("expected persistent session %s, got %+v", secondID, resMode)
	}

	// 6. Advance clock by 72 hours: persistent session must NOT expire.
	*now = now.Add(72 * time.Hour)
	res4, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    ModeAuto,
		Prompt:  "Still here after 3 days",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn 4 failed: %v", err)
	}
	if res4.Session.ID != secondID || res4.Session.MessageCount != 4 {
		t.Fatalf("expected persistent session to survive 72h, got %+v", res4.Session)
	}

	// 7. Switching back to temp lets the session expire normally.
	if _, err := orch.SetActiveSessionKind(workdir, KindTemp); err != nil {
		t.Fatalf("SetActiveSessionKind(temp) failed: %v", err)
	}
	*now = now.Add(25 * time.Hour)
	res5, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: workdir,
		Mode:    ModeAuto,
		Prompt:  "New temporary session",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn 5 failed: %v", err)
	}
	if res5.Session.ID == secondID || res5.Session.Kind != KindTemp {
		t.Fatalf("expected a fresh temp session, got %+v", res5.Session)
	}
}

func TestSessionParentInheritanceAndUse(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{Content: "ok"}, nil
		},
	})

	parent := "/home/user/proj"
	child := filepath.Join(parent, "src", "deep")

	// A session in the parent is inherited by a nested directory.
	resParent, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: parent, Mode: ModeAuto, Prompt: "parent"}, nil)
	if err != nil {
		t.Fatalf("parent run failed: %v", err)
	}
	resChild, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: child, Mode: ModeAuto, Prompt: "child"}, nil)
	if err != nil {
		t.Fatalf("child run failed: %v", err)
	}
	if resChild.Session.ID != resParent.Session.ID {
		t.Fatalf("expected child to inherit parent session %s, got %s", resParent.Session.ID, resChild.Session.ID)
	}
	if resChild.Session.MessageCount != 4 {
		t.Fatalf("expected inherited session with 4 messages, got %d", resChild.Session.MessageCount)
	}

	// An explicit session in the child shadows the parent session.
	childSess, err := orch.NewSession(child)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if childSess.Kind != KindPersistent || childSess.Workdir != child {
		t.Fatalf("unexpected child session: %+v", childSess)
	}
	resChild2, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: child, Mode: ModeAuto, Prompt: "child own"}, nil)
	if err != nil {
		t.Fatalf("child own run failed: %v", err)
	}
	if resChild2.Session.ID != childSess.ID {
		t.Fatalf("expected child session %s, got %s", childSess.ID, resChild2.Session.ID)
	}

	// Deleting the child session falls back to the inherited parent session.
	if err := orch.DeleteSession(childSess.ID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}
	resChild3, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: child, Mode: ModeAuto, Prompt: "back to parent"}, nil)
	if err != nil {
		t.Fatalf("child fallback run failed: %v", err)
	}
	if resChild3.Session.ID != resParent.Session.ID {
		t.Fatalf("expected fallback to parent session %s, got %s", resParent.Session.ID, resChild3.Session.ID)
	}

	// A new session in the parent becomes active, and use switches back to the old one.
	secondParent, err := orch.NewSession(parent)
	if err != nil {
		t.Fatalf("second NewSession failed: %v", err)
	}
	resParent2, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: parent, Mode: ModeAuto, Prompt: "new parent"}, nil)
	if err != nil {
		t.Fatalf("new parent run failed: %v", err)
	}
	if resParent2.Session.ID != secondParent.ID {
		t.Fatalf("expected new parent session %s, got %s", secondParent.ID, resParent2.Session.ID)
	}
	if _, err := orch.SetActiveSession(resParent.Session.ID); err != nil {
		t.Fatalf("SetActiveSession failed: %v", err)
	}
	resParent3, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: parent, Mode: ModeAuto, Prompt: "old parent"}, nil)
	if err != nil {
		t.Fatalf("old parent run failed: %v", err)
	}
	if resParent3.Session.ID != resParent.Session.ID {
		t.Fatalf("expected use to restore session %s, got %s", resParent.Session.ID, resParent3.Session.ID)
	}
}

func TestSanitizeWireMessagesDropsOrphanToolResults(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "tool", "tool_call_id": "call_1", "content": "orphan"},
		{"role": "assistant", "content": "", "tool_calls": []map[string]any{
			{"id": "call_2", "name": "read_file", "arguments": map[string]any{"path": "x"}},
		}},
		{"role": "tool", "tool_call_id": "call_2", "content": "ok"},
		{"role": "tool", "tool_call_id": "call_3", "content": "wrong id"},
		{"role": "assistant", "content": "done"},
		{"role": "tool", "tool_call_id": "call_2", "content": "orphan after assistant"},
	}

	got := sanitizeWireMessages(msgs)
	if len(got) != 4 {
		t.Fatalf("unexpected message count: %d, %+v", len(got), got)
	}
	if got[0]["role"] != "user" || got[1]["role"] != "assistant" || got[2]["role"] != "tool" || got[3]["role"] != "assistant" {
		t.Fatalf("unexpected message order: %+v", got)
	}
	if got[2]["tool_call_id"] != "call_2" {
		t.Fatalf("wrong tool result kept: %+v", got[2])
	}
}

func TestTurnStoppedReasonEmptyAnswer(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{Reasoning: "thinking only"}, nil
		},
	})

	res, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "question",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if res.Content != "" || res.StoppedReason != "empty_answer" {
		t.Fatalf("expected empty_answer, got content=%q reason=%q", res.Content, res.StoppedReason)
	}
	if res.Session.StoppedReason != "empty_answer" {
		t.Fatalf("expected session to record empty_answer, got %+v", res.Session)
	}
}

func TestTurnStoppedReasonMaxSteps(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{
				ToolCalls: []map[string]any{
					{"id": "call_1", "name": "noop", "arguments": map[string]any{}},
				},
			}, nil
		},
	})

	res, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir:  "/work",
		Mode:     ModeAuto,
		Prompt:   "loop",
		MaxSteps: 2,
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if res.Steps != 2 || res.StoppedReason != "max_steps" {
		t.Fatalf("expected max_steps stop after 2 steps, got steps=%d reason=%q", res.Steps, res.StoppedReason)
	}
	if res.Session.StoppedReason != "max_steps" {
		t.Fatalf("expected session to record max_steps, got %+v", res.Session)
	}
}

func TestAgentStepSettings(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	if got := orch.MaxSteps(); got != DefaultMaxSteps {
		t.Fatalf("default max steps = %d, want %d", got, DefaultMaxSteps)
	}
	if got := orch.StepLimitMode(); got != StepLimitAsk {
		t.Fatalf("default step limit mode = %q, want %q", got, StepLimitAsk)
	}
	if err := orch.SetMaxSteps(80); err != nil {
		t.Fatalf("SetMaxSteps: %v", err)
	}
	if err := orch.SetStepLimitMode(StepLimitStop); err != nil {
		t.Fatalf("SetStepLimitMode: %v", err)
	}
	if got := orch.MaxSteps(); got != 80 {
		t.Fatalf("max steps = %d, want 80", got)
	}
	if got := orch.StepLimitMode(); got != StepLimitStop {
		t.Fatalf("step limit mode = %q, want %q", got, StepLimitStop)
	}
	if err := orch.SetMaxSteps(0); err == nil {
		t.Fatal("zero max steps must be rejected")
	}
	if err := orch.SetMaxSteps(-1); err == nil {
		t.Fatal("negative max steps must be rejected")
	}
	if err := orch.SetMaxSteps(201); err == nil {
		t.Fatal("max steps above 200 must be rejected")
	}
	if err := orch.SetStepLimitMode("unknown"); err == nil {
		t.Fatal("unknown step limit mode must be rejected")
	}
}

func TestStepLimitModeReadsLegacySettings(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	if err := orch.store.PutState("max_steps", 0); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if got := orch.StepLimitMode(); got != StepLimitNone {
		t.Fatalf("a zero budget must mean no limit, got %q", got)
	}

	if err := orch.store.PutState("max_steps", 20); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if err := orch.store.PutState("ask_on_max_steps", false); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if got := orch.StepLimitMode(); got != StepLimitStop {
		t.Fatalf("ask_on_max_steps=false must mean stop, got %q", got)
	}

	if err := orch.store.PutState("ask_on_max_steps", true); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if got := orch.StepLimitMode(); got != StepLimitAsk {
		t.Fatalf("ask_on_max_steps=true must mean ask, got %q", got)
	}

	if err := orch.SetStepLimitMode(StepLimitNone); err != nil {
		t.Fatalf("SetStepLimitMode: %v", err)
	}
	if got := orch.StepLimitMode(); got != StepLimitNone {
		t.Fatalf("stored mode must win over legacy settings, got %q", got)
	}
}

func TestRunTurnWithoutStepLimit(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)
	if err := orch.SetMaxSteps(3); err != nil {
		t.Fatalf("SetMaxSteps: %v", err)
	}
	if err := orch.SetStepLimitMode(StepLimitNone); err != nil {
		t.Fatalf("SetStepLimitMode: %v", err)
	}

	calls := 0
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			calls++
			if calls < 6 {
				return &plugin.CompleteResponse{
					ToolCalls: []map[string]any{
						{"id": fmt.Sprintf("call_%d", calls), "name": "noop", "arguments": map[string]any{}},
					},
				}, nil
			}
			return &plugin.CompleteResponse{Content: "done"}, nil
		},
	})

	res, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "run until done",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if res.Steps != 6 {
		t.Fatalf("expected 6 steps without a limit, got %d", res.Steps)
	}
	if res.StoppedReason != "" {
		t.Fatalf("unlimited turn must not stop on max_steps, got %q", res.StoppedReason)
	}
	if res.Content != "done" {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

// TestTurnLiftsStepLimitMidTurn covers switching the mode to no limit while a turn runs:
// the running turn becomes unlimited instead of stopping at the budget.
func TestTurnLiftsStepLimitMidTurn(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)
	if err := orch.SetMaxSteps(2); err != nil {
		t.Fatalf("SetMaxSteps: %v", err)
	}

	calls := 0
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			calls++
			if calls == 2 {
				if err := orch.SetStepLimitMode(StepLimitNone); err != nil {
					t.Errorf("SetStepLimitMode: %v", err)
				}
			}
			if calls < 5 {
				return &plugin.CompleteResponse{
					ToolCalls: []map[string]any{
						{"id": fmt.Sprintf("call_%d", calls), "name": "noop", "arguments": map[string]any{}},
					},
				}, nil
			}
			return &plugin.CompleteResponse{Content: "done"}, nil
		},
	})

	prompts := &stepAnswers{}
	ctx := question.WithAsker(context.Background(), prompts)

	res, err := orch.RunTurn(ctx, TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "run until done",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(prompts.questions) != 0 {
		t.Fatalf("prompts = %d, want 0: a zero limit must not ask to extend", len(prompts.questions))
	}
	if res.Steps != 5 || res.Content != "done" || res.StoppedReason != "" {
		t.Fatalf("expected 5 steps and a final answer, got steps=%d content=%q reason=%q", res.Steps, res.Content, res.StoppedReason)
	}
}

func TestRunTurnUsesConfiguredMaxSteps(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)
	if err := orch.SetMaxSteps(7); err != nil {
		t.Fatalf("SetMaxSteps: %v", err)
	}
	if err := orch.SetStepLimitMode(StepLimitStop); err != nil {
		t.Fatalf("SetStepLimitMode: %v", err)
	}

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{
				ToolCalls: []map[string]any{
					{"id": "call_1", "name": "noop", "arguments": map[string]any{}},
				},
			}, nil
		},
	})

	res, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "loop",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if res.Steps != 7 || res.StoppedReason != "max_steps" {
		t.Fatalf("expected configured 7 steps, got steps=%d reason=%q", res.Steps, res.StoppedReason)
	}
}

func TestTurnAsksToExtendStepBudget(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	if err := orch.SetMaxSteps(2); err != nil {
		t.Fatalf("SetMaxSteps: %v", err)
	}

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{
				ToolCalls: []map[string]any{
					{"id": "call_1", "name": "noop", "arguments": map[string]any{}},
				},
			}, nil
		},
	})

	asker := &stepAnswers{answers: []question.Answer{{Choice: "continue"}, {Choice: "stop"}}}
	ctx := question.WithAsker(context.Background(), asker)

	res, err := orch.RunTurn(ctx, TurnRequest{
		Workdir:  "/work",
		Mode:     ModeAuto,
		Prompt:   "loop",
		MaxSteps: 2,
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(asker.questions) != 2 {
		t.Fatalf("prompts = %d, want 2", len(asker.questions))
	}
	first := asker.questions[0]
	if first.Kind != question.KindStepLimit || first.Source != question.SourceCore {
		t.Fatalf("unexpected question envelope: %+v", first)
	}
	if !strings.Contains(first.Body, "2") {
		t.Fatalf("question body does not mention the budget: %q", first.Body)
	}
	if first.Default != "continue" {
		t.Fatalf("question default = %q, want continue", first.Default)
	}
	if res.Steps != 4 || res.StoppedReason != "max_steps" {
		t.Fatalf("expected 4 steps after one extension, got steps=%d reason=%q", res.Steps, res.StoppedReason)
	}
}

func TestTurnLoopWithToolCallsAndPermissionHooks(t *testing.T) {
	orch, eb, reg, _ := setupTestOrchestrator(t)

	err := reg.RegisterTool(plugin.ToolSpec{
		PluginID:    "tools-local",
		Name:        "run_cmd",
		Description: "Runs a shell command",
		Permission:  "dangerous",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cmd": map[string]any{"type": "string"},
			},
			"required": []any{"cmd"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	// Register a hook that blocks "rm -rf" commands.
	eb.OnHook("plugin:permissions", "tool:before_call", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		args, _ := p["arguments"].(map[string]any)
		if args["cmd"] == "rm -rf /" {
			return nil, errors.New("blocked by permissions plugin")
		}
		return p, nil
	})

	callStep := 0
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			callStep++
			if callStep == 1 {
				return &plugin.CompleteResponse{
					ToolCalls: []map[string]any{
						{
							"id":        "call_1",
							"name":      "run_cmd",
							"arguments": map[string]any{"cmd": "rm -rf /"},
						},
					},
				}, nil
			}
			// Second step: verify the model received the permission error in tool message.
			lastMsg := req.Messages[len(req.Messages)-1]
			return &plugin.CompleteResponse{
				Content: "Tool result was: " + lastMsg["content"].(string),
			}, nil
		},
	})

	res, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "Try dangerous command",
	}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if res.Steps != 2 {
		t.Fatalf("expected 2 steps, got %d", res.Steps)
	}
	if res.Content != "Tool result was: error: blocked by permissions plugin" {
		t.Fatalf("unexpected final content: %q", res.Content)
	}
}

// TestInterruptedTurnKeepsHistory covers a turn cancelled in the middle: the prompt,
// the model answer, and the tool result must stay in the session.
func TestInterruptedTurnKeepsHistory(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			cancel()
			return &plugin.CompleteResponse{
				Content: "checking",
				ToolCalls: []map[string]any{
					{"id": "call_1", "name": "noop", "arguments": map[string]any{}},
				},
			}, nil
		},
	})

	sess, err := orch.ResolveForWorkdir("/work", ModeAuto)
	if err != nil {
		t.Fatalf("ResolveForWorkdir: %v", err)
	}

	if _, err := orch.RunTurn(ctx, TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "check the network",
	}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	msgs, err := orch.GetMessages(sess.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected prompt, answer, and tool result, got %d messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "check the network" {
		t.Fatalf("prompt was not kept: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("model answer was not kept: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("tool result was not kept: %+v", msgs[2])
	}

	stored, found, err := orch.GetSession(sess.ID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if stored.StoppedReason != "interrupted" {
		t.Fatalf("stopped reason = %q, want interrupted", stored.StoppedReason)
	}
	if stored.MessageCount != 3 {
		t.Fatalf("message count = %d, want 3", stored.MessageCount)
	}
}

// TestTurnDropsUnansweredToolCalls covers a session saved by a killed turn: calls without
// results must not reach the provider or the next saved history.
func TestTurnDropsUnansweredToolCalls(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	sess, err := orch.ResolveForWorkdir("/work", ModeAuto)
	if err != nil {
		t.Fatalf("ResolveForWorkdir: %v", err)
	}
	if err := orch.SaveMessages(sess, []Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []map[string]any{
			{"id": "call_9", "name": "noop"},
		}},
	}); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	var sent []map[string]any
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			sent = req.Messages
			return &plugin.CompleteResponse{Content: "ok"}, nil
		},
	})

	if _, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work",
		Mode:    ModeAuto,
		Prompt:  "second",
	}, nil); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	for _, m := range sent {
		if _, ok := m["tool_calls"]; ok {
			t.Fatalf("unanswered tool calls reached the provider: %+v", m)
		}
		if m["role"] == "tool" {
			t.Fatalf("orphan tool result reached the provider: %+v", m)
		}
	}

	msgs, err := orch.GetMessages(sess.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected the repaired history plus the new prompt and answer, got %d: %+v", len(msgs), msgs)
	}
}

func TestAskToolPermissionUsesDecisionHook(t *testing.T) {
	orch, eb, _, _ := setupTestOrchestrator(t)

	var decided map[string]any
	eb.OnHook("permissions", "tool:decided", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		decided = p
		p["allowed"] = p["choice"] != "deny"
		return p, nil
	})

	asker := &stepAnswers{answers: []question.Answer{{Choice: "once"}}}
	ctx := question.WithAsker(context.Background(), asker)
	raw := map[string]any{
		"kind":  "permission",
		"title": "Run the tool?",
		"options": []any{
			map[string]any{"id": "once", "label": "Allow once"},
			map[string]any{"id": "deny", "label": "Deny"},
		},
		"default": "once",
	}

	allowed, reason, err := orch.askToolPermission(ctx, plugin.ToolSpec{Name: "noop", Permission: "edit"}, "ses_1", "/work", "noop", map[string]any{"path": "x"}, raw)
	if err != nil {
		t.Fatalf("askToolPermission failed: %v", err)
	}
	if !allowed || reason != "" {
		t.Fatalf("expected an allowed call, got allowed=%v reason=%q", allowed, reason)
	}
	if len(asker.questions) != 1 || asker.questions[0].Source != "plugin" {
		t.Fatalf("unexpected question: %+v", asker.questions)
	}
	if decided == nil || decided["choice"] != "once" || decided["session_id"] != "ses_1" {
		t.Fatalf("unexpected decision payload: %+v", decided)
	}
}

func TestAskToolPermissionDeniedWithoutDecisionHook(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	asker := &stepAnswers{answers: []question.Answer{{Choice: "once"}}}
	ctx := question.WithAsker(context.Background(), asker)
	raw := map[string]any{
		"title":   "Run the tool?",
		"options": []any{map[string]any{"id": "once", "label": "Allow once"}},
	}

	allowed, reason, err := orch.askToolPermission(ctx, plugin.ToolSpec{Name: "noop"}, "ses_1", "/work", "noop", nil, raw)
	if err != nil {
		t.Fatalf("askToolPermission failed: %v", err)
	}
	if allowed {
		t.Fatal("a question without a tool:decided hook must deny the call")
	}
	if reason == "" {
		t.Fatal("a denied call must explain why")
	}
}

func TestToolCallDeniedByQuestionKeepsToolError(t *testing.T) {
	orch, eb, reg, _ := setupTestOrchestrator(t)

	if err := reg.RegisterTool(plugin.ToolSpec{
		PluginID:    "test",
		Name:        "noop",
		Description: "Does nothing",
		Permission:  "dangerous",
		InputSchema: map[string]any{"type": "object"},
	}); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	eb.OnHook("permissions", "tool:before_call", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		p["ask"] = map[string]any{
			"kind":    "permission",
			"title":   "Run noop?",
			"options": []any{map[string]any{"id": "deny", "label": "Deny"}},
			"default": "deny",
		}
		return p, nil
	})
	eb.OnHook("permissions", "tool:decided", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		p["allowed"] = p["choice"] != "deny"
		if p["allowed"] != true {
			p["error"] = "permission denied by user for tool: noop"
		}
		return p, nil
	})

	step := 0
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			step++
			if step == 1 {
				return &plugin.CompleteResponse{
					ToolCalls: []map[string]any{{"id": "call_1", "name": "noop", "arguments": map[string]any{}}},
				}, nil
			}
			last := req.Messages[len(req.Messages)-1]
			content, _ := last["content"].(string)
			return &plugin.CompleteResponse{Content: "tool said: " + content}, nil
		},
	})

	asker := &stepAnswers{answers: []question.Answer{{Choice: "deny"}}}
	ctx := question.WithAsker(context.Background(), asker)
	res, err := orch.RunTurn(ctx, TurnRequest{Workdir: "/work", Mode: ModeAuto, Prompt: "try it"}, nil)
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if len(asker.questions) != 1 {
		t.Fatalf("expected one question, got %d", len(asker.questions))
	}
	if res.Content != "tool said: error: permission denied by user for tool: noop" {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

func TestRunChildCreatesDetachedChildSession(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	var gotModel string
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(_ context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			gotModel = req.Model
			return &plugin.CompleteResponse{Content: "child answer"}, nil
		},
	})

	parent, err := orch.ResolveForWorkdir("/work", ModeAuto)
	if err != nil {
		t.Fatalf("ResolveForWorkdir: %v", err)
	}
	ctx := WithTurn(context.Background(), TurnInfo{
		SessionID: parent.ID,
		RootID:    parent.ID,
		Depth:     0,
		Workdir:   parent.Workdir,
		Preset:    "mini",
		Model:     "m1",
	})

	res, err := orch.RunChild(ctx, TurnRequest{Prompt: "child task"})
	if err != nil {
		t.Fatalf("RunChild failed: %v", err)
	}
	if res.Content != "child answer" {
		t.Fatalf("unexpected content: %q", res.Content)
	}
	child := res.Session
	if child.ParentID != parent.ID || child.RootID != parent.ID || child.Depth != 1 {
		t.Fatalf("unexpected child session: %+v", child)
	}
	if child.Workdir != parent.Workdir {
		t.Fatalf("child workdir = %q, want %q", child.Workdir, parent.Workdir)
	}
	if gotModel != "m1" {
		t.Fatalf("child model = %q, want m1", gotModel)
	}

	active, err := orch.ActiveForWorkdir("/work")
	if err != nil {
		t.Fatalf("ActiveForWorkdir: %v", err)
	}
	if active == nil || active.ID != parent.ID {
		t.Fatalf("the child must not become the active session: %+v", active)
	}
	parentMsgs, err := orch.GetMessages(parent.ID)
	if err != nil {
		t.Fatalf("parent messages: %v", err)
	}
	if len(parentMsgs) != 0 {
		t.Fatalf("the child must not write to the parent history: %+v", parentMsgs)
	}
	childMsgs, err := orch.GetMessages(child.ID)
	if err != nil {
		t.Fatalf("child messages: %v", err)
	}
	if len(childMsgs) != 2 || childMsgs[0].Role != "user" || childMsgs[0].Content != "child task" {
		t.Fatalf("unexpected child history: %+v", childMsgs)
	}
	sessions, err := orch.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected parent and child sessions, got %d: %+v", len(sessions), sessions)
	}
}

func TestRunChildRejectsDepthLimit(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	parent, err := orch.ResolveForWorkdir("/work", ModeAuto)
	if err != nil {
		t.Fatalf("ResolveForWorkdir: %v", err)
	}
	ctx := WithTurn(context.Background(), TurnInfo{SessionID: parent.ID, RootID: parent.ID, Depth: 1, Workdir: parent.Workdir})

	if _, err := orch.RunChild(ctx, TurnRequest{Prompt: "too deep"}); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("expected a depth error, got %v", err)
	}
}

func TestRunChildRequiresTurnContext(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	if _, err := orch.RunChild(context.Background(), TurnRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected an error outside a turn")
	}
}

func TestRunChildRejectsStatelessTurn(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)

	ctx := WithTurn(context.Background(), TurnInfo{SessionID: "ephemeral", Workdir: "/work"})
	if _, err := orch.RunChild(ctx, TurnRequest{Prompt: "x"}); err == nil {
		t.Fatal("expected an error in a stateless turn")
	}
}
