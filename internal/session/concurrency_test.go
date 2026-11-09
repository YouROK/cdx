package session

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/plugin"
)

func registerMockProvider(reg *plugin.Registry, complete func(context.Context, plugin.CompleteRequest) (*plugin.CompleteResponse, error)) {
	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(ctx context.Context, req plugin.CompleteRequest, _ func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return complete(ctx, req)
		},
	})
}

func lastUserPrompt(req plugin.CompleteRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if role, _ := req.Messages[i]["role"].(string); role == "user" {
			if text, _ := req.Messages[i]["content"].(string); text != "" {
				return text
			}
		}
	}
	return ""
}

type turnOutcome struct {
	res *TurnResult
	err error
}

func runTurnAsync(orch *Orchestrator, req TurnRequest, onEvent func(TurnEvent)) <-chan turnOutcome {
	out := make(chan turnOutcome, 1)
	go func() {
		res, err := orch.RunTurn(context.Background(), req, onEvent)
		out <- turnOutcome{res: res, err: err}
	}()
	return out
}

func TestConcurrentTurnsInOneSessionWaitAndShareHistory(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	var calls atomic.Int32
	var messagesMu sync.Mutex
	var secondMessages []map[string]any
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case 2:
			messagesMu.Lock()
			secondMessages = req.Messages
			messagesMu.Unlock()
		}
		return &plugin.CompleteResponse{Content: "answer to " + lastUserPrompt(req)}, nil
	})

	workdir := "/home/user/proj"
	first := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "first"}, nil)
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn did not reach the provider")
	}

	var waiting atomic.Bool
	second := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "second"}, func(ev TurnEvent) {
		if ev.Type == "waiting" {
			waiting.Store(true)
		}
	})

	select {
	case out := <-second:
		t.Fatalf("the second turn finished while the first was running: %+v", out)
	case <-time.After(100 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the second turn reached the provider while the first was running: %d calls", got)
	}

	close(releaseFirst)

	firstOut := <-first
	secondOut := <-second
	if firstOut.err != nil || secondOut.err != nil {
		t.Fatalf("turns failed: first=%v second=%v", firstOut.err, secondOut.err)
	}
	if !waiting.Load() {
		t.Fatal("the second turn did not report waiting")
	}
	if firstOut.res.Session.ID != secondOut.res.Session.ID {
		t.Fatalf("turns used different sessions: %s and %s", firstOut.res.Session.ID, secondOut.res.Session.ID)
	}
	if secondOut.res.Session.MessageCount != 4 {
		t.Fatalf("expected 4 stored messages, got %d", secondOut.res.Session.MessageCount)
	}

	msgs, err := orch.GetMessages(firstOut.res.Session.ID)
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	want := []struct{ role, content string }{
		{"user", "first"},
		{"assistant", "answer to first"},
		{"user", "second"},
		{"assistant", "answer to second"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("expected %d messages, got %d: %+v", len(want), len(msgs), msgs)
	}
	for i, expected := range want {
		if msgs[i].Role != expected.role || msgs[i].Content != expected.content {
			t.Fatalf("message %d = %q/%q, want %q/%q", i, msgs[i].Role, msgs[i].Content, expected.role, expected.content)
		}
	}

	messagesMu.Lock()
	defer messagesMu.Unlock()
	if lastUserPrompt(plugin.CompleteRequest{Messages: secondMessages}) != "second" {
		t.Fatalf("the waiting turn did not receive the previous history: %+v", secondMessages)
	}
	if len(secondMessages) < 3 {
		t.Fatalf("the waiting turn did not see the first turn: %+v", secondMessages)
	}
}

func TestConcurrentTurnsStartInArrivalOrder(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	var (
		mu           sync.Mutex
		order        []string
		calls        atomic.Int32
		firstStarted = make(chan struct{})
		releaseFirst = make(chan struct{})
	)

	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		mu.Lock()
		order = append(order, lastUserPrompt(req))
		mu.Unlock()
		return &plugin.CompleteResponse{Content: "ok"}, nil
	})

	workdir := "/home/user/proj"
	first := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "first"}, nil)
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn did not reach the provider")
	}

	waitForWaiting := func(prompt string) (<-chan turnOutcome, chan struct{}) {
		waited := make(chan struct{})
		out := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: prompt}, func(ev TurnEvent) {
			if ev.Type == "waiting" {
				select {
				case <-waited:
				default:
					close(waited)
				}
			}
		})
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			t.Fatalf("the %q turn did not report waiting", prompt)
		}
		return out, waited
	}

	second, _ := waitForWaiting("second")
	third, _ := waitForWaiting("third")

	close(releaseFirst)
	if out := <-first; out.err != nil {
		t.Fatalf("the first turn failed: %v", out.err)
	}
	if out := <-second; out.err != nil {
		t.Fatalf("the second turn failed: %v", out.err)
	}
	if out := <-third; out.err != nil {
		t.Fatalf("the third turn failed: %v", out.err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"first", "second", "third"}
	if len(order) != len(want) {
		t.Fatalf("provider order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("provider order = %v, want %v", order, want)
		}
	}
}

func TestTurnsInDifferentSessionsRunInParallel(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	var inFlight atomic.Int32
	both := make(chan struct{})
	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		if inFlight.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
			return nil, errors.New("turns in different sessions did not run in parallel")
		}
		return &plugin.CompleteResponse{Content: "ok"}, nil
	})

	first := runTurnAsync(orch, TurnRequest{Workdir: "/home/user/one", Mode: ModeAuto, Prompt: "one"}, nil)
	second := runTurnAsync(orch, TurnRequest{Workdir: "/home/user/two", Mode: ModeAuto, Prompt: "two"}, nil)

	one := <-first
	two := <-second
	if one.err != nil || two.err != nil {
		t.Fatalf("turns failed: one=%v two=%v", one.err, two.err)
	}
	if one.res.Session.ID == two.res.Session.ID {
		t.Fatalf("different workdirs shared session %s", one.res.Session.ID)
	}
}

func TestCanceledWaiterReleasesTheQueue(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &plugin.CompleteResponse{Content: "ok"}, nil
	})

	workdir := "/home/user/proj"
	first := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "first"}, nil)
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn did not reach the provider")
	}

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan struct{})
	canceled := make(chan error, 1)
	go func() {
		_, err := orch.RunTurn(ctx, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "second"}, func(ev TurnEvent) {
			if ev.Type == "waiting" {
				select {
				case <-waited:
				default:
					close(waited)
				}
			}
		})
		canceled <- err
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the second turn did not report waiting")
	}

	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the canceled turn did not return")
	}

	close(releaseFirst)
	if out := <-first; out.err != nil {
		t.Fatalf("the first turn failed: %v", out.err)
	}

	third, err := orch.RunTurn(context.Background(), TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "third"}, nil)
	if err != nil {
		t.Fatalf("the third turn failed after a canceled waiter: %v", err)
	}
	if third.Session.MessageCount != 4 {
		t.Fatalf("expected 4 messages after the canceled turn, got %d", third.Session.MessageCount)
	}
}

func TestConcurrentFirstTurnsShareOneSession(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		return &plugin.CompleteResponse{Content: "ok"}, nil
	})

	workdir := "/home/user/proj"
	first := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "first"}, nil)
	second := runTurnAsync(orch, TurnRequest{Workdir: workdir, Mode: ModeAuto, Prompt: "second"}, nil)

	one := <-first
	two := <-second
	if one.err != nil || two.err != nil {
		t.Fatalf("turns failed: one=%v two=%v", one.err, two.err)
	}
	if one.res.Session.ID != two.res.Session.ID {
		t.Fatalf("first prompts created different sessions: %s and %s", one.res.Session.ID, two.res.Session.ID)
	}

	sessions, err := orch.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one session, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].MessageCount != 4 {
		t.Fatalf("expected 4 messages in the shared session, got %d", sessions[0].MessageCount)
	}
}

func TestStatelessTurnsAreNotQueued(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	var inFlight atomic.Int32
	both := make(chan struct{})
	registerMockProvider(reg, func(ctx context.Context, req plugin.CompleteRequest) (*plugin.CompleteResponse, error) {
		if inFlight.Add(1) == 2 {
			close(both)
		}
		select {
		case <-both:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
			return nil, errors.New("stateless turns were queued")
		}
		return &plugin.CompleteResponse{Content: "ok"}, nil
	})

	first := runTurnAsync(orch, TurnRequest{Workdir: "/home/user/proj", Mode: ModeNone, Prompt: "one"}, nil)
	second := runTurnAsync(orch, TurnRequest{Workdir: "/home/user/proj", Mode: ModeNone, Prompt: "two"}, nil)

	one := <-first
	two := <-second
	if one.err != nil || two.err != nil {
		t.Fatalf("stateless turns failed: one=%v two=%v", one.err, two.err)
	}
	sessions, err := orch.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("stateless turns stored sessions: %+v", sessions)
	}
}
