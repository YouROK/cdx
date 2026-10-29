package cli

import (
	"testing"
)

// TestParsePrompt checks request parsing.
func TestParsePrompt(t *testing.T) {
	o, err := Parse([]string{"объясни", "этот", "код"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "run" {
		t.Fatalf("expected run command, got %q", o.Command)
	}
	if o.Prompt != "объясни этот код" {
		t.Fatalf("unexpected prompt %q", o.Prompt)
	}
}

// TestParseFlags checks parsing of request flags.
func TestParseFlags(t *testing.T) {
	o, err := Parse([]string{"-q", "--json", "--no-color", "--model", "m", "--allow", "edit", "--lang", "en", "запрос"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.Quiet || !o.JSON || !o.NoColor {
		t.Fatalf("boolean flags not set: %+v", o)
	}
	if o.Model != "m" || o.Lang != "en" {
		t.Fatalf("value flags not set: %+v", o)
	}
	if len(o.Allow) != 1 || o.Allow[0] != "edit" {
		t.Fatalf("unexpected allow list %v", o.Allow)
	}
	if o.Prompt != "запрос" {
		t.Fatalf("unexpected prompt %q", o.Prompt)
	}
}

// TestParseEqualsForm checks the --flag=value form.
func TestParseEqualsForm(t *testing.T) {
	o, err := Parse([]string{"--model=test", "--lang=ru", "--allow=read", "x"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Model != "test" || o.Lang != "ru" {
		t.Fatalf("unexpected options %+v", o)
	}
	if len(o.Allow) != 1 || o.Allow[0] != "read" {
		t.Fatalf("unexpected allow %v", o.Allow)
	}
}

// TestParseMissingValue checks the error without a flag value.
func TestParseMissingValue(t *testing.T) {
	for _, flag := range []string{"--model", "--allow", "--lang"} {
		if _, err := Parse([]string{flag}); err == nil {
			t.Fatalf("expected error for %s without value", flag)
		}
	}
}

// TestParseNoArgs checks the help with no arguments.
func TestParseNoArgs(t *testing.T) {
	o, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "help" {
		t.Fatalf("expected help, got %q", o.Command)
	}
}

// TestParseHelpFlags checks -h and -v.
func TestParseHelpFlags(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		o, err := Parse([]string{flag})
		if err != nil {
			t.Fatalf("Parse(%q): %v", flag, err)
		}
		if o.Command != "help" {
			t.Fatalf("expected help for %q, got %q", flag, o.Command)
		}
	}

	for _, flag := range []string{"-v", "--version"} {
		o, err := Parse([]string{flag})
		if err != nil {
			t.Fatalf("Parse(%q): %v", flag, err)
		}
		if o.Command != "version" {
			t.Fatalf("expected version for %q, got %q", flag, o.Command)
		}
	}
}

// TestParseSubcommands checks commands given by a flag.
func TestParseSubcommands(t *testing.T) {
	cases := map[string]string{
		"--daemon":  "daemon",
		"--session": "session",
		"--plugin":  "plugin",
		"--mcp":     "mcp",
		"--config":  "config",
		"--doctor":  "doctor",
	}
	for input, want := range cases {
		o, err := Parse([]string{input})
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if o.Command != want {
			t.Fatalf("Parse(%q) = %q, want %q", input, o.Command, want)
		}
	}

	// Single commands live in the same flags.
	for input, want := range map[string]string{"--version": "version", "--help": "help"} {
		o, err := Parse([]string{input})
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if o.Command != want {
			t.Fatalf("Parse(%q) = %q, want %q", input, o.Command, want)
		}
	}
}

// TestParseSubcommandFlags checks that subcommand flags are not swallowed.
func TestParseSubcommandFlags(t *testing.T) {
	o, err := Parse([]string{"--plugin", "add", "https://example.com/p", "--force"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "plugin" {
		t.Fatalf("expected plugin command, got %q", o.Command)
	}
	want := []string{"add", "https://example.com/p", "--force"}
	if len(o.Args) != len(want) {
		t.Fatalf("expected %d args, got %v", len(want), o.Args)
	}
	for i := range want {
		if o.Args[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q", i, o.Args[i], want[i])
		}
	}
}

// TestParseDaemonForeground checks --foreground reaches the subcommand.
func TestParseDaemonForeground(t *testing.T) {
	o, err := Parse([]string{"--daemon", "start", "--foreground"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "daemon" {
		t.Fatalf("expected daemon command, got %q", o.Command)
	}
	if len(o.Args) != 2 || o.Args[1] != "--foreground" {
		t.Fatalf("unexpected args %v", o.Args)
	}
}

// TestSessionMode checks the session mode selection.
func TestSessionMode(t *testing.T) {
	cases := []struct {
		opts *Options
		want string
	}{
		{&Options{}, "auto"},
		{&Options{NoSess: true}, "none"},
	}
	for _, tc := range cases {
		if got := tc.opts.sessionMode(); got != tc.want {
			t.Fatalf("sessionMode() = %q, want %q", got, tc.want)
		}
	}
}

// TestCodeForError checks the exit codes.
func TestCodeForError(t *testing.T) {
	cases := map[string]int{
		"permission_denied": ExitPerms,
		"config":            ExitConfig,
		"daemon":            ExitDaemon,
		"connection":        ExitDaemon,
		"llm_error":         ExitGeneral,
		"":                  ExitGeneral,
	}
	for code, want := range cases {
		if got := codeForError(code); got != want {
			t.Fatalf("codeForError(%q) = %d, want %d", code, got, want)
		}
	}
}

// TestAgeOf checks the short age label.
func TestAgeOf(t *testing.T) {
	if got := ageOf(0); got != "-" {
		t.Fatalf("expected dash for zero time, got %q", got)
	}
}

// TestCallError checks the error text selection.
func TestCallError(t *testing.T) {
	if got := callError(nil, errTest); got != errTest.Error() {
		t.Fatalf("unexpected text %q", got)
	}
	if got := callError(nil, nil); got != "call failed" {
		t.Fatalf("unexpected fallback %q", got)
	}
}

// errTest is a test error.
var errTest = &testError{}

type testError struct{}

func (e *testError) Error() string { return "boom" }
