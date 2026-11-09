package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/paths"
)

func TestAPICommandPrintsPluginReference(t *testing.T) {
	layout := paths.ResolveWithHome(t.TempDir(), func(string) string { return "" })
	var stdout, stderr bytes.Buffer
	app := &App{Layout: layout, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
	if err := app.Run([]string{"api"}); err != nil {
		t.Fatalf("api command failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Plugin API reference") {
		t.Fatalf("api output has no reference: %s", stdout.String())
	}
}

func TestThinkingTailKeepsTheLatestText(t *testing.T) {
	long := strings.Repeat("a", 120)
	got := thinkingTail("first\nsecond " + long)
	if strings.Contains(got, "\n") {
		t.Fatalf("newlines must be replaced: %q", got)
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("long reasoning must be clipped from the left: %q", got)
	}
	if !strings.HasSuffix(got, long[len(long)-90:]) {
		t.Fatalf("long reasoning must keep the tail: %q", got)
	}
	if got := thinkingTail("<think>hello"); got != "hello" {
		t.Fatalf("unexpected reasoning tail: %q", got)
	}
}

func TestToolProgressLabel(t *testing.T) {
	label := toolProgressLabel(map[string]any{
		"tool_name": "shell",
		"tool_args": map[string]any{"command": "go test ./..."},
	})
	if label != "shell go test ./..." {
		t.Fatalf("unexpected tool label: %q", label)
	}

	label = toolProgressLabel(map[string]any{
		"tool_name": "read_file",
		"tool_args": map[string]any{"path": "/tmp/plugin.js"},
	})
	if label != "read_file /tmp/plugin.js" {
		t.Fatalf("unexpected read_file label: %q", label)
	}
}

func TestTurnSummaryFormat(t *testing.T) {
	s := newTurnSummary()
	s.recordStart(map[string]any{"tool_name": "write_file", "tool_args": map[string]any{"path": "/tmp/a.js"}})
	s.recordStart(map[string]any{"tool_name": "edit_file", "tool_args": map[string]any{"path": "/tmp/b.js"}})

	out := s.format(map[string]any{
		"steps":          float64(16),
		"content":        "",
		"stopped_reason": "max_steps",
		"session":        map[string]any{"tokens_in": float64(7906208), "tokens_out": float64(100050)},
	}, 4*time.Minute+12*time.Second)

	for _, want := range []string{"Done in 4m12s", "16 steps", "7.9M in / 100k out tokens", "2 files (a.js, b.js)", "tool-step limit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Tool errors", "Shell commands", "Read paths"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("summary must not contain %q:\n%s", unwanted, out)
		}
	}
}

func TestSingleLineFlattensProgress(t *testing.T) {
	got := singleLine("shell cd /tmp && cat > t.js <<'EOF'\nconst fs=require('fs')\n\tvm\x1b[31m")
	if strings.ContainsAny(got, "\n\t") || strings.Contains(got, "\x1b") {
		t.Fatalf("progress text must be one printable line: %q", got)
	}
	if got != "shell cd /tmp && cat > t.js <<'EOF' const fs=require('fs') vm" {
		t.Fatalf("unexpected flat label: %q", got)
	}
	if singleLine("  ") != "" {
		t.Fatalf("blank labels must stay empty")
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := formatDuration(12 * time.Second); got != "12s" {
		t.Fatalf("unexpected duration: %q", got)
	}
	if got := formatDuration(2*time.Minute + 14*time.Second); got != "2m14s" {
		t.Fatalf("unexpected duration: %q", got)
	}
	if got := formatDuration(3 * time.Minute); got != "3m" {
		t.Fatalf("unexpected duration: %q", got)
	}
	if got := formatTokens(987); got != "987" {
		t.Fatalf("unexpected tokens: %q", got)
	}
	if got := formatTokens(1200); got != "1.2k" {
		t.Fatalf("unexpected tokens: %q", got)
	}
	if got := formatTokens(2000); got != "2k" {
		t.Fatalf("unexpected tokens: %q", got)
	}
	if got := formatTokens(7906208); got != "7.9M" {
		t.Fatalf("unexpected tokens: %q", got)
	}
}

func TestPromptSourceCombinesFileAndText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("  first line\nsecond line\n"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}

	got, err := promptSource(strings.NewReader(""), path, []string{"extra", "text"})
	if err != nil {
		t.Fatalf("prompt source failed: %v", err)
	}
	if want := "first line\nsecond line extra text"; got != want {
		t.Fatalf("unexpected prompt: %q", got)
	}

	got, err = promptSource(strings.NewReader("from stdin\n"), "-", nil)
	if err != nil {
		t.Fatalf("stdin prompt source failed: %v", err)
	}
	if got != "from stdin" {
		t.Fatalf("unexpected stdin prompt: %q", got)
	}

	if _, err := promptSource(strings.NewReader(""), "-", nil); err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected empty prompt error, got: %v", err)
	}

	if _, err := promptSource(strings.NewReader(""), filepath.Join(t.TempDir(), "missing.md"), nil); err == nil || !strings.Contains(err.Error(), "failed to read prompt file") {
		t.Fatalf("expected missing file error, got: %v", err)
	}
}

func TestSettingsURL(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"127.0.0.1:8787", "http://127.0.0.1:8787"},
		{"0.0.0.0:8787", "http://127.0.0.1:8787"},
		{":8787", "http://127.0.0.1:8787"},
		{"[::1]:8787", "http://[::1]:8787"},
		{"", ""},
		{"not-an-address", ""},
	}
	for _, c := range cases {
		if got := settingsURL(c.addr); got != c.want {
			t.Fatalf("settingsURL(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestDefaultHTTPAddr(t *testing.T) {
	t.Setenv("CDX_HTTP_ADDR", "")
	t.Setenv("CDX_ALLOW_REMOTE_HOST", "")
	if got := defaultHTTPAddr(); got != "127.0.0.1:8787" {
		t.Fatalf("default address = %q, want 127.0.0.1:8787", got)
	}

	t.Setenv("CDX_ALLOW_REMOTE_HOST", "1")
	if got := defaultHTTPAddr(); got != "0.0.0.0:8787" {
		t.Fatalf("remote address = %q, want 0.0.0.0:8787", got)
	}

	t.Setenv("CDX_ALLOW_REMOTE_HOST", "0")
	if got := defaultHTTPAddr(); got != "127.0.0.1:8787" {
		t.Fatalf("disabled remote address = %q, want 127.0.0.1:8787", got)
	}

	t.Setenv("CDX_ALLOW_REMOTE_HOST", "1")
	t.Setenv("CDX_HTTP_ADDR", "127.0.0.1:9000")
	if got := defaultHTTPAddr(); got != "127.0.0.1:9000" {
		t.Fatalf("explicit address = %q, want 127.0.0.1:9000", got)
	}
}

func TestConfigCommandOpensDashboard(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{filepath.Join(tmp, "plugins")}

	d, err := daemon.New(context.Background(), daemon.Options{Layout: layout, HTTPAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	var stdout, stderr bytes.Buffer
	var opened string
	app := &App{
		Layout:      layout,
		Stdin:       strings.NewReader(""),
		Stdout:      &stdout,
		Stderr:      &stderr,
		OpenBrowser: func(url string) error { opened = url; return nil },
	}

	if err := app.Run([]string{"--config"}); err != nil {
		t.Fatalf("--config failed: %v", err)
	}
	want := "http://" + d.HTTPAddr()
	if opened != want {
		t.Fatalf("opened %q, want %q", opened, want)
	}
	if strings.TrimSpace(stdout.String()) != want {
		t.Fatalf("printed %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	app.OpenBrowser = func(string) error { return errors.New("no browser") }
	if err := app.Run([]string{"--config"}); err != nil {
		t.Fatalf("--config must succeed without a browser: %v", err)
	}
	if !strings.Contains(stdout.String(), want) || !strings.Contains(stderr.String(), "failed to open browser") {
		t.Fatalf("expected the URL and a warning, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestDaemonCommandDetachesStdio(t *testing.T) {
	cmd := newDaemonCommand("/bin/true")
	if cmd.Stdin != nil || cmd.Stdout != nil || cmd.Stderr != nil {
		t.Fatalf("daemon stdio must be nil to use /dev/null, got stdin=%v stdout=%v stderr=%v", cmd.Stdin, cmd.Stdout, cmd.Stderr)
	}
}

func TestCLIAndDaemonEndToEndWithPlugins(t *testing.T) {
	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{filepath.Join(tmp, "plugins")}

	// Create a mock provider plugin and a mock permission plugin that prompts the user.
	provDir := filepath.Join(layout.PluginDirs[0], "test-provider")
	if err := os.MkdirAll(provDir, 0o755); err != nil {
		t.Fatalf("mkdir provDir: %v", err)
	}
	_ = os.WriteFile(filepath.Join(provDir, "manifest.json"), []byte(`{
		"id": "test-provider",
		"name": "Test Provider",
		"version": "1.0.0",
		"description": "Mock LLM provider",
		"entry": "main.js"
	}`), 0o644)
	_ = os.WriteFile(filepath.Join(provDir, "main.js"), []byte(`
		function setup(cdx) {
			cdx.tools.register({
				name: "sensitive_op",
				description: "Sensitive operation",
				permission: "dangerous",
				input_schema: { type: "object", properties: { target: { type: "string" } } }
			}, function(args) {
				return "executed on " + args.target;
			});

			cdx.hooks.on("tool:before_call", 10, function(payload) {
				if (payload.permission === "dangerous") {
					var ans = cdx.bus.request("client:prompt_user", {
						question: "Run sensitive_op on " + payload.arguments.target + "?",
						tool: payload.tool
					});
					if (!ans || !ans.allow) {
						throw new Error("denied by user");
					}
				}
				return payload;
			});

			var step = 0;
			cdx.providers.register({
				listModels: function() {
					return [{ id: "test-model", name: "Test Model", default: true }];
				},
				complete: function(req, emit) {
					step++;
					if (step === 1) {
						return {
							tool_calls: [{
								id: "tc1",
								name: "sensitive_op",
								arguments: { target: "prod" }
							}]
						};
					}
					var last = req.messages[req.messages.length - 1];
					var reply = "Done: " + last.content;
					emit({ type: "text", delta: reply });
					return { content: reply };
				}
			});
		}
	`), 0o644)

	d, err := daemon.New(context.Background(), daemon.Options{
		Layout:   layout,
		HTTPAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	var stdout, stderr bytes.Buffer
	app := &App{
		Layout: layout,
		Stdin:  strings.NewReader("1\n"), // Choose [1] once in the permission menu.
		Stdout: &stdout,
		Stderr: &stderr,
	}

	// 1. Check plugins list.
	if err := app.Run([]string{"plugins", "list"}); err != nil {
		t.Fatalf("plugins list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "test-provider") {
		t.Fatalf("expected test-provider in output: %s", stdout.String())
	}

	// 2. Run a prompt that triggers a tool call -> interactive prompt -> tool execution -> rendered reply.
	stdout.Reset()
	stderr.Reset()
	if err := app.Run([]string{"Please run sensitive op"}); err != nil {
		t.Fatalf("prompt run failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "Permission required") || !strings.Contains(stderr.String(), "[1] once") {
		t.Fatalf("expected numbered permission prompt on stderr, got: %s", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "Done: executed on prod" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}

	// 3. --prompt-file reads the prompt from a file and appends positional text.
	promptPath := filepath.Join(tmp, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("prompt from a file"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	app.Stdin = strings.NewReader("")
	if err := app.Run([]string{"--prompt-file", promptPath}); err != nil {
		t.Fatalf("prompt file run failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Done: prompt from a file" {
		t.Fatalf("unexpected prompt file output: %q", stdout.String())
	}

	stdout.Reset()
	if err := app.Run([]string{"--prompt-file=" + promptPath, "and text"}); err != nil {
		t.Fatalf("inline prompt file run failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "Done: prompt from a file and text" {
		t.Fatalf("unexpected inline prompt file output: %q", stdout.String())
	}

	// 4. Session commands do not require a prompt.
	stdout.Reset()
	stderr.Reset()
	app.Stdin = strings.NewReader("")
	if err := app.Run([]string{"session", "new"}); err != nil {
		t.Fatalf("session new failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created persistent session") {
		t.Fatalf("unexpected session new output: %s", stdout.String())
	}

	stdout.Reset()
	if err := app.Run([]string{"session", "list"}); err != nil {
		t.Fatalf("session list failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "* ses_") || !strings.Contains(stdout.String(), "[persistent]") {
		t.Fatalf("expected active persistent session in list: %s", stdout.String())
	}

	stdout.Reset()
	if err := app.Run([]string{"session", "mode", "temp"}); err != nil {
		t.Fatalf("session mode failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "is now temp") {
		t.Fatalf("unexpected session mode output: %s", stdout.String())
	}

	if err := app.Run([]string{"--session", "new", "test"}); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("expected --session to be rejected, got: %v", err)
	}

	stdout.Reset()
	if err := app.Run([]string{"session", "delete"}); err != nil {
		t.Fatalf("session delete failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Deleted session") {
		t.Fatalf("unexpected session delete output: %s", stdout.String())
	}

	// 5. --json prints a machine-readable result.
	stdout.Reset()
	app.Stdin = strings.NewReader("")
	if err := app.Run([]string{"--json", "json output"}); err != nil {
		t.Fatalf("json run failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json output: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["content"]; !ok {
		t.Fatalf("json output has no content field: %v", payload)
	}
}
