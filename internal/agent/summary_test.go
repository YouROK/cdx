package agent

import (
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/registry"
)

// TestSummaryFormat checks the action description format.
func TestSummaryFormat(t *testing.T) {
	tool := &registry.Tool{Spec: registry.Spec{LLMName: "p_local_tools_run_command"}}

	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"command": "git log --oneline"}, "run: git log --oneline"},
		{map[string]any{"url": "https://example.com/x"}, "fetch https://example.com/x"},
		{map[string]any{"path": "main.go"}, "p_local_tools_run_command main.go"},
		{map[string]any{}, "p_local_tools_run_command"},
	}

	for _, c := range cases {
		if got := summaryOf(tool, c.args); got != c.want {
			t.Fatalf("summaryOf(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// TestSummaryOneLine checks a multiline command is collapsed.
func TestSummaryOneLine(t *testing.T) {
	tool := &registry.Tool{Spec: registry.Spec{LLMName: "run"}}

	// The internal name must not leak into the command description.
	got := summaryOf(tool, map[string]any{"command": "cd /tmp &&\n  git log \\\n  -5"})
	if strings.Contains(got, "\n") || strings.Contains(got, "\\") {
		t.Fatalf("command must be one line, got %q", got)
	}
	if !strings.HasPrefix(got, "run: ") {
		t.Fatalf("unexpected prefix %q", got)
	}
}

// TestSummaryTruncates checks a long command is clipped.
func TestSummaryTruncates(t *testing.T) {
	tool := &registry.Tool{Spec: registry.Spec{LLMName: "run"}}

	got := summaryOf(tool, map[string]any{"command": strings.Repeat("x", 500)})
	if len([]rune(got)) > 220 {
		t.Fatalf("long command must be truncated, got %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("truncated command must end with an ellipsis")
	}
}
