package ipc

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestIPCBidirectionalStreamAndPrompt(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	srv := NewServer(sock, func(_ context.Context, method string, payload map[string]any, stream *ServerStream) (any, error) {
		if err := stream.Emit(map[string]any{"delta": "thinking..."}); err != nil {
			return nil, err
		}
		ans, err := stream.Prompt(map[string]any{
			"question": "Allow command?",
			"tool":     payload["tool"],
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"method":  method,
			"allowed": ans["allow"],
		}, nil
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start server failed: %v", err)
	}
	defer srv.Close()

	var events []any
	res, err := Call(context.Background(), sock, "run", map[string]any{"tool": "shell"}, ClientCallbacks{
		OnEvent: func(data any) {
			events = append(events, data)
		},
		OnPrompt: func(q map[string]any) (map[string]any, error) {
			if q["tool"] != "shell" {
				t.Fatalf("unexpected prompt tool: %v", q)
			}
			return map[string]any{"allow": true}, nil
		},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 streamed event, got %d", len(events))
	}
	resMap, _ := res.(map[string]any)
	if resMap["allowed"] != true {
		t.Fatalf("expected allowed=true in final response, got %v", res)
	}
}

func TestPromptReturnsWhenClientDisconnects(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	promptErr := make(chan error, 1)
	srv := NewServer(sock, func(_ context.Context, _ string, _ map[string]any, stream *ServerStream) (any, error) {
		_, err := stream.Prompt(map[string]any{"question": "Allow?"})
		promptErr <- err
		return nil, err
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start server failed: %v", err)
	}
	defer srv.Close()

	raw, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	conn := wrapConn(raw)
	if err := conn.Send(Frame{Type: FrameRequest, Method: "run"}); err != nil {
		t.Fatalf("send request: %v", err)
	}
	frame, err := conn.Receive()
	if err != nil {
		t.Fatalf("receive prompt: %v", err)
	}
	if frame.Type != FramePrompt {
		t.Fatalf("expected prompt frame, got %q", frame.Type)
	}
	_ = raw.Close()

	select {
	case err := <-promptErr:
		if err == nil {
			t.Fatal("expected prompt error after client disconnect")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not return after client disconnect")
	}
}

func TestServerCancelsHandlerWhenClientDisconnects(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "test.sock")

	started := make(chan struct{})
	canceled := make(chan struct{})
	srv := NewServer(sock, func(ctx context.Context, _ string, _ map[string]any, _ *ServerStream) (any, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start server failed: %v", err)
	}
	defer srv.Close()

	raw, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	conn := wrapConn(raw)
	if err := conn.Send(Frame{Type: FrameRequest, Method: "run"}); err != nil {
		t.Fatalf("send request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	_ = raw.Close()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was not canceled after client disconnect")
	}
}
