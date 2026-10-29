package agent

import (
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
)

// TestPromptBlockOrder checks the order of the context blocks.
func TestPromptBlockOrder(t *testing.T) {
	cfg := config.Default()
	files := []ContextFile{
		{Dir: "/root", Name: "agent.md", Content: "правила проекта"},
		{Dir: "/root/sub", Name: "skills.md", Content: "навыки"},
	}

	parts := BuildParts(StandardPrompt, cfg, "/root/sub", "/root", files)
	out := parts.Render()

	stableAt := strings.Index(out, "You are cdx")
	filesAt := strings.Index(out, "## agent.md (/root)")
	envAt := strings.Index(out, "## Environment")
	if stableAt < 0 || filesAt < 0 || envAt < 0 {
		t.Fatalf("all blocks must be present:\n%s", out)
	}
	if !(stableAt < filesAt && filesAt < envAt) {
		t.Fatalf("order must be prompt, files, environment: stable=%d files=%d env=%d",
			stableAt, filesAt, envAt)
	}
}

// TestStablePartHasNoVolatileData checks the stable part does not depend on cwd.
func TestStablePartHasNoVolatileData(t *testing.T) {
	cfg := config.Default()

	first := BuildParts(StandardPrompt, cfg, "/work/a", "", nil).Stable
	second := BuildParts(StandardPrompt, cfg, "/work/b", "", nil).Stable

	if first != second {
		t.Fatal("stable part must not depend on cwd, otherwise prompt cache breaks")
	}
	if strings.Contains(first, "{{cwd}}") || strings.Contains(first, "{{date}}") {
		t.Fatal("stable part must not contain volatile placeholders")
	}
}

// TestEnvironmentCarriesVolatileData checks the contents of the environment block.
func TestEnvironmentCarriesVolatileData(t *testing.T) {
	cfg := config.Default()
	cfg.Perms.Edit = "allow"

	env := BuildParts(StandardPrompt, cfg, "/work/proj", "/work", nil).Environment
	for _, want := range []string{"- cwd: /work/proj", "- session root: /work", "- date: ", "edit=allow"} {
		if !strings.Contains(env, want) {
			t.Fatalf("environment must contain %q:\n%s", want, env)
		}
	}
}

// TestEnvironmentOmitsEmptySessionRoot checks an empty session root is omitted.
func TestEnvironmentOmitsEmptySessionRoot(t *testing.T) {
	env := BuildParts(StandardPrompt, config.Default(), "/w", "", nil).Environment
	if strings.Contains(env, "session root") {
		t.Fatalf("session root must be omitted when empty:\n%s", env)
	}
}

// TestWithToolsListsNames checks that tools are listed.
func TestWithToolsListsNames(t *testing.T) {
	specs := []map[string]any{
		{"type": "function", "function": map[string]any{
			"name": "read_file", "description": "Read a file",
		}},
		{"type": "function", "function": map[string]any{
			"name": "shell", "description": "Run a command",
		}},
	}

	parts := BuildParts(StandardPrompt, config.Default(), "/w", "", nil).
		WithTools(specs, config.Default(), "/w", "")

	if !strings.Contains(parts.Environment, "## Tools") {
		t.Fatalf("tools section is missing:\n%s", parts.Environment)
	}
	for _, want := range []string{"- read_file: Read a file", "- shell: Run a command"} {
		if !strings.Contains(parts.Environment, want) {
			t.Fatalf("tools list must contain %q:\n%s", want, parts.Environment)
		}
	}
}

// TestWithToolsSkipsMalformed checks resilience to malformed descriptions.
func TestWithToolsSkipsMalformed(t *testing.T) {
	specs := []map[string]any{
		{"type": "function"},
		{"type": "function", "function": map[string]any{"description": "без имени"}},
	}

	parts := BuildParts(StandardPrompt, config.Default(), "/w", "", nil).
		WithTools(specs, config.Default(), "/w", "")

	if strings.Contains(parts.Environment, "## Tools") {
		t.Fatalf("malformed specs must not produce a tools section:\n%s", parts.Environment)
	}
}

// TestHashPromptStable checks the hash stays stable.
func TestHashPromptStable(t *testing.T) {
	files := []ContextFile{{Dir: "/r", Name: "agent.md", Content: "x"}}

	a := HashPrompt("prompt", files)
	b := HashPrompt("prompt", files)
	if a != b {
		t.Fatalf("hash must be deterministic: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("hash must be 16 characters, got %d", len(a))
	}
}

// TestHashPromptChanges checks the hash reacts to changes.
func TestHashPromptChanges(t *testing.T) {
	base := HashPrompt("prompt", nil)

	if HashPrompt("other", nil) == base {
		t.Fatal("hash must change with the prompt")
	}
	if HashPrompt("prompt", []ContextFile{{Dir: "/r", Name: "agent.md", Content: "y"}}) == base {
		t.Fatal("hash must change with context files")
	}
}

// TestPromptTemplateFallback checks prompt selection.
func TestPromptTemplateFallback(t *testing.T) {
	if PromptTemplate("") != StandardPrompt {
		t.Fatal("empty custom prompt must fall back to the default")
	}
	if PromptTemplate("   ") != StandardPrompt {
		t.Fatal("blank custom prompt must fall back to the default")
	}
	if PromptTemplate("custom") != "custom" {
		t.Fatal("custom prompt must be used as is")
	}
}

// TestStandardPromptKeepsMaxIterations checks the step limit substitution.
func TestStandardPromptKeepsMaxIterations(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.MaxIterations = 7

	out := BuildParts(StandardPrompt, cfg, "/w", "", nil).Stable
	if strings.Contains(out, "{{max_iterations}}") {
		t.Fatal("max_iterations placeholder must be replaced")
	}
	if !strings.Contains(out, "7 tool steps") {
		t.Fatalf("limit must be substituted:\n%s", out)
	}
}

// TestSessionSectionPresent checks the section about past turns.
//
// Without it the model in a session answers "I have no memory", even
// though the history is mixed into the context.
func TestSessionSectionPresent(t *testing.T) {
	if !strings.Contains(StandardPrompt, "## Session context") {
		t.Fatal("prompt must describe session context")
	}
	// The meaning is checked, not the line break: the prompt text is edited.
	if !strings.Contains(StandardPrompt, "do not claim that you have no memory") {
		t.Fatal("prompt must forbid denying memory of earlier invocations")
	}

	out := BuildParts(StandardPrompt, config.Default(), "/w", "/w", nil).Render()
	if !strings.Contains(out, "replayed for context") {
		t.Fatal("rendered prompt must keep the session section")
	}
}
