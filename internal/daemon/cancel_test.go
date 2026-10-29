package daemon

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestTurnsCancel checks cancelling a registered turn.
func TestTurnsCancel(t *testing.T) {
	tr := newTurns()
	s := newSession(nil)

	ctx, cancel := tr.register(s, "t1")
	defer cancel()

	if !tr.cancel(s, "t1") {
		t.Fatal("registered turn must be cancellable")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel must close the turn context")
	}
}

// TestTurnsCancelUnknown checks cancelling an unknown turn.
func TestTurnsCancelUnknown(t *testing.T) {
	tr := newTurns()
	if tr.cancel(newSession(nil), "nope") {
		t.Fatal("unknown turn must not be cancellable")
	}
}

// TestTurnsCancelAll checks cancelling every turn of a connection.
//
// A dropped connection must stop the work: otherwise the request runs
// on for nothing and blocks the queue behind it.
func TestTurnsCancelAll(t *testing.T) {
	tr := newTurns()
	s := newSession(nil)

	first, cancelFirst := tr.register(s, "t1")
	defer cancelFirst()
	second, cancelSecond := tr.register(s, "t2")
	defer cancelSecond()

	tr.cancelAll(s)

	for i, ctx := range []context.Context{first, second} {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatalf("turn %d must be cancelled", i+1)
		}
	}
}

// TestTurnsList checks the list of running turns.
func TestTurnsList(t *testing.T) {
	tr := newTurns()
	s := newSession(nil)

	_, cancel := tr.register(s, "t1")
	defer cancel()
	tr.setInfo(s, turnInfo{ID: "t1", Model: "m", Preset: "mini", Prompt: "вопрос", Started: 100})

	list := tr.list()
	if len(list) != 1 {
		t.Fatalf("expected one turn, got %+v", list)
	}
	if list[0].Model != "m" || list[0].Preset != "mini" {
		t.Fatalf("unexpected turn %+v", list[0])
	}

	tr.dropInfo(s, "t1")
	if len(tr.list()) != 0 {
		t.Fatal("finished turn must leave the list")
	}
}

// TestTurnsCancelByID checks cancelling by id from the interface.
func TestTurnsCancelByID(t *testing.T) {
	tr := newTurns()
	s := newSession(nil)

	ctx, cancel := tr.register(s, "t1")
	defer cancel()

	if !tr.cancelByID("t1") {
		t.Fatal("cancelByID must find the turn")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context must be cancelled")
	}
}

// TestTurnCancelStopsConnectionRead checks that a dropped connection
// releases the waiter, so the queue does not block.
func TestTurnCancelStopsConnectionRead(t *testing.T) {
	tr := newTurns()
	s := newSession(nil)

	done := make(chan struct{})
	go func() {
		ctx, cancel := tr.register(s, "t1")
		defer cancel()
		<-ctx.Done()
		close(done)
	}()

	// Wait for the registration, then drop the connection.
	time.Sleep(50 * time.Millisecond)
	s.markDone()
	tr.cancelAll(s)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("turn must stop when the connection is gone")
	}
}

// TestSessionMarkDoneIsIdempotent checks a repeated close call.
func TestSessionMarkDoneIsIdempotent(t *testing.T) {
	s := newSession(nil)
	s.markDone()
	s.markDone()

	select {
	case <-s.done:
	default:
		t.Fatal("done channel must be closed")
	}
}

// TestNetConnNilSafe checks a session without a connection does not panic.
func TestNetConnNilSafe(t *testing.T) {
	var raw net.Conn
	s := newSession(nil)
	if s.conn != nil {
		t.Fatal("expected an empty connection")
	}
	s.markDone()
	_ = raw
}
