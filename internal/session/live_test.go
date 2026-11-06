package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yourok/cdx/internal/plugin"
)

func TestLiveTurnTracksStreamingTurn(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(_ context.Context, _ plugin.CompleteRequest, onStream func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			onStream(plugin.StreamEvent{Type: "reasoning", Delta: "thinking"})
			onStream(plugin.StreamEvent{Type: "text", Delta: "hello "})
			once.Do(func() { close(started) })
			<-release
			onStream(plugin.StreamEvent{Type: "text", Delta: "world"})
			return &plugin.CompleteResponse{Content: "hello world"}, nil
		},
	})

	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		_, runErr = orch.RunTurn(context.Background(), TurnRequest{
			Workdir: "/work/live",
			Mode:    ModeAuto,
			Prompt:  "say hi",
		}, nil)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the provider was not called")
	}

	sessions, err := orch.ListSessions()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected one session, got %+v (err=%v)", sessions, err)
	}
	id := sessions[0].ID

	turn, ok := orch.GetLiveTurn(id)
	if !ok {
		t.Fatal("expected a live turn while the provider is blocked")
	}
	if !turn.Running || turn.Prompt != "say hi" || turn.HistoryCount != 0 {
		t.Fatalf("unexpected live turn: %+v", turn)
	}
	if len(turn.Steps) != 1 {
		t.Fatalf("expected one live step, got %d", len(turn.Steps))
	}
	if turn.Steps[0].Text != "hello " || turn.Steps[0].Reasoning != "thinking" {
		t.Fatalf("unexpected streamed step: %+v", turn.Steps[0])
	}
	if ids := orch.LiveSessionIDs(); len(ids) != 1 || ids[0] != id {
		t.Fatalf("unexpected live session ids: %v", ids)
	}

	close(release)
	<-done
	if runErr != nil {
		t.Fatalf("RunTurn failed: %v", runErr)
	}

	turn, ok = orch.GetLiveTurn(id)
	if !ok {
		t.Fatal("the finished turn should stay readable")
	}
	if turn.Running || turn.FinishedAt == 0 {
		t.Fatalf("expected a finished live turn: %+v", turn)
	}
	if turn.Steps[0].Text != "hello world" {
		t.Fatalf("expected the full streamed text, got %q", turn.Steps[0].Text)
	}
	if ids := orch.LiveSessionIDs(); len(ids) != 0 {
		t.Fatalf("finished turns must not be reported as live: %v", ids)
	}
}

func TestLiveTurnTracksToolCalls(t *testing.T) {
	orch, _, _, _ := setupTestOrchestrator(t)
	sess := &Session{ID: "ses_live", Kind: KindTemp}

	turn := orch.beginLive(sess, "run ls", "m1", 4)
	if turn == nil {
		t.Fatal("expected a live turn")
	}
	orch.applyLive(turn, TurnEvent{
		Type:     "tool_start",
		Step:     1,
		ToolName: "shell",
		ToolArgs: map[string]any{"cmd": "ls"},
	})
	orch.applyLive(turn, TurnEvent{
		Type:     "tool_end",
		Step:     1,
		ToolName: "shell",
		ToolOut:  "a.txt\nb.txt",
	})

	got, ok := orch.GetLiveTurn(sess.ID)
	if !ok {
		t.Fatal("expected a live turn")
	}
	if got.HistoryCount != 4 || len(got.Steps) != 1 || len(got.Steps[0].Tools) != 1 {
		t.Fatalf("unexpected live state: %+v", got)
	}
	tool := got.Steps[0].Tools[0]
	if tool.Running || tool.Output != "a.txt\nb.txt" || tool.Args["cmd"] != "ls" {
		t.Fatalf("unexpected live tool: %+v", tool)
	}

	// A snapshot must not expose the mutable live slices.
	got.Steps[0].Tools[0].Output = "changed"
	again, _ := orch.GetLiveTurn(sess.ID)
	if again.Steps[0].Tools[0].Output != "a.txt\nb.txt" {
		t.Fatalf("a snapshot shares tool state with the live turn: %+v", again.Steps[0].Tools[0])
	}
}

func TestLiveTurnSkipsStatelessTurns(t *testing.T) {
	orch, _, reg, _ := setupTestOrchestrator(t)

	reg.RegisterProvider(plugin.ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]plugin.ModelInfo, error) {
			return []plugin.ModelInfo{{ID: "m1", Name: "m1", Default: true}}, nil
		},
		Complete: func(context.Context, plugin.CompleteRequest, func(plugin.StreamEvent)) (*plugin.CompleteResponse, error) {
			return &plugin.CompleteResponse{Content: "ok"}, nil
		},
	})

	if _, err := orch.RunTurn(context.Background(), TurnRequest{
		Workdir: "/work/stateless",
		Mode:    ModeNone,
		Prompt:  "no history",
	}, nil); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if _, ok := orch.GetLiveTurn("ephemeral"); ok {
		t.Fatal("stateless turns must not be tracked")
	}
	if ids := orch.LiveSessionIDs(); len(ids) != 0 {
		t.Fatalf("stateless turns must not be reported as live: %v", ids)
	}
}

func TestLiveToolOutputTrimsLongResults(t *testing.T) {
	long := strings.Repeat("x", liveToolOutputLimit+50)
	out := liveToolOutput(long)
	if !strings.HasSuffix(out, "... (truncated)") {
		t.Fatalf("expected a truncation marker, got %q", out[len(out)-30:])
	}
	if len(out) > liveToolOutputLimit+32 {
		t.Fatalf("trimmed output is too long: %d bytes", len(out))
	}

	// A cut must not split a multi-byte rune.
	wide := strings.Repeat("я", liveToolOutputLimit)
	out = liveToolOutput(wide)
	body := strings.TrimSuffix(out, "\n... (truncated)")
	if !utf8.ValidString(body) {
		t.Fatal("trimmed output is not valid UTF-8")
	}
}
