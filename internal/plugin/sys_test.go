package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestFetchRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("busy"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	api := &SysAPI{HTTPClient: srv.Client(), MaxRetries: 4, RetryWaitBase: time.Millisecond}
	res, err := api.Fetch(context.Background(), srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if res["body"] != "ok" {
		t.Fatalf("unexpected body: %v", res["body"])
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestFetchRetriesExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("busy"))
	}))
	defer srv.Close()

	api := &SysAPI{HTTPClient: srv.Client(), MaxRetries: 3, RetryWaitBase: time.Millisecond}
	if _, err := api.Fetch(context.Background(), srv.URL, nil, nil); err == nil {
		t.Fatal("expected an error after retries")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestFetchDoesNotRetryBadRequest(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()

	api := &SysAPI{HTTPClient: srv.Client(), MaxRetries: 4, RetryWaitBase: time.Millisecond}
	res, err := api.Fetch(context.Background(), srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if res["ok"] != false || res["body"] != "bad" {
		t.Fatalf("unexpected response: %v", res)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestFetchRetriesBeforeStreaming(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte("data: one\n\ndata: two\n\n"))
	}))
	defer srv.Close()

	var lines []string
	api := &SysAPI{HTTPClient: srv.Client(), MaxRetries: 3, RetryWaitBase: time.Millisecond}
	if _, err := api.Fetch(context.Background(), srv.URL, nil, func(line string) error {
		lines = append(lines, line)
		return nil
	}); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	var nonEmpty []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if strings.Join(nonEmpty, "|") != "data: one|data: two" {
		t.Fatalf("unexpected streamed lines: %#v", lines)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestExecKillsChildProcessesOnTimeout(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child.pid")
	api := NewSysAPI()

	command := "sleep 30 & echo $! > " + marker + "; wait"
	res, err := api.Exec(context.Background(), command, "", 500)
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if res["timed_out"] != true {
		t.Fatalf("expected timed_out=true, got %v", res)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child process %d survived command timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRandomHex(t *testing.T) {
	api := NewSysAPI()

	first, err := api.RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex failed: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("expected 32 hex characters, got %d", len(first))
	}
	second, err := api.RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex failed: %v", err)
	}
	if first == second {
		t.Fatalf("RandomHex returned the same value twice")
	}

	for _, n := range []int{0, -1, 65} {
		if _, err := api.RandomHex(n); err == nil {
			t.Fatalf("expected an error for %d bytes", n)
		}
	}
}
