package bus

import (
	"context"
	"errors"
	"testing"
)

func TestPubSubAndUnregister(t *testing.T) {
	b := New()
	var received []string

	unsub := b.Subscribe("plugin:a", "session:started", func(_ context.Context, payload any) {
		received = append(received, "a:"+payload.(string))
	})
	b.Subscribe("plugin:b", "session:started", func(_ context.Context, payload any) {
		received = append(received, "b:"+payload.(string))
	})

	count := b.Publish(context.Background(), "session:started", "s1")
	if count != 2 || len(received) != 2 {
		t.Fatalf("expected 2 deliveries, got %d (%v)", count, received)
	}

	unsub()
	b.Publish(context.Background(), "session:started", "s2")
	if len(received) != 3 || received[2] != "b:s2" {
		t.Fatalf("unexpected deliveries after unsub: %v", received)
	}

	b.UnregisterOwner("plugin:b")
	if n := b.Publish(context.Background(), "session:started", "s3"); n != 0 {
		t.Fatalf("expected 0 deliveries after UnregisterOwner, got %d", n)
	}
}

func TestRPCRequestAndRecursionGuard(t *testing.T) {
	b := New()

	b.Handle("plugin:calc", "math:add", func(_ context.Context, payload any) (any, error) {
		nums := payload.([]int)
		return nums[0] + nums[1], nil
	})

	res, err := b.Request(context.Background(), "plugin:caller", "math:add", []int{4, 5})
	if err != nil || res.(int) != 9 {
		t.Fatalf("unexpected RPC result: res=%v err=%v", res, err)
	}

	// Calling own handler directly must fail with ErrSelfRequest.
	if _, err := b.Request(context.Background(), "plugin:calc", "math:add", []int{1, 2}); !errors.Is(err, ErrSelfRequest) {
		t.Fatalf("expected ErrSelfRequest, got %v", err)
	}

	// Missing topic must fail with ErrNoHandler.
	if _, err := b.Request(context.Background(), "plugin:caller", "unknown:topic", nil); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("expected ErrNoHandler, got %v", err)
	}
}

func TestHookPipelinePriorityAndAbort(t *testing.T) {
	b := New()

	// Priority 20 runs second.
	b.OnHook("plugin:second", "session:before_turn", 20, func(_ context.Context, p map[string]any) (map[string]any, error) {
		p["trace"] = p["trace"].(string) + "->second"
		return p, nil
	})

	// Priority 10 runs first.
	b.OnHook("plugin:first", "session:before_turn", 10, func(_ context.Context, p map[string]any) (map[string]any, error) {
		p["trace"] = "first"
		return p, nil
	})

	out, err := b.RunHook(context.Background(), "session:before_turn", map[string]any{})
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	if out["trace"] != "first->second" {
		t.Fatalf("unexpected hook execution order: %v", out["trace"])
	}

	// Add a blocking hook (e.g. permission denied) at priority 15.
	denyErr := errors.New("permission denied")
	b.OnHook("plugin:perm", "session:before_turn", 15, func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, denyErr
	})

	out2, err := b.RunHook(context.Background(), "session:before_turn", map[string]any{})
	if !errors.Is(err, denyErr) {
		t.Fatalf("expected denyErr, got %v", err)
	}
	if out2["trace"] != "first" {
		t.Fatalf("expected second hook to not run after abort, trace=%v", out2["trace"])
	}
}
