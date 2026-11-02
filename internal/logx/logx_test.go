package logx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		"":        LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
	}
	for input, want := range cases {
		got, err := ParseLevel(input)
		if err != nil {
			t.Fatalf("ParseLevel(%q) unexpected error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}

	if _, err := ParseLevel("invalid"); err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestScopedLoggingAndFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New()
	if err := l.Init(Options{Level: LevelInfo, Stderr: &buf}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	scope := l.Tagged("Plugin:openai")
	scope.Debug("Hidden debug message")
	scope.Info("Loaded model %s", "gpt-4o")

	out := buf.String()
	if strings.Contains(out, "Hidden debug message") {
		t.Fatalf("debug message should have been filtered out: %s", out)
	}
	if !strings.Contains(out, "info  [Plugin:openai] Loaded model gpt-4o") {
		t.Fatalf("unexpected log output: %s", out)
	}
}

func TestFileRotation(t *testing.T) {
	dir := t.TempDir()
	l := New()
	err := l.Init(Options{
		Dir:      dir,
		Level:    LevelInfo,
		MaxSize:  120,
		MaxFiles: 2,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer l.Close()

	scope := l.Tagged("Daemon")
	for i := 0; i < 10; i++ {
		scope.Info("Log entry number %d with extra padding bytes", i)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	// 1 active cdx.log + at most 2 rotated archives = 3 files max.
	if len(entries) < 2 || len(entries) > 3 {
		var names []string
		for _, e := range entries {
			names = append(names, filepath.Base(e.Name()))
		}
		t.Fatalf("expected 2 or 3 log files after rotation, got %d: %v", len(entries), names)
	}
}
