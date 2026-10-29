package cli

import "testing"

func TestNewScheme(t *testing.T) {
	cases := []struct {
		args    []string
		command string
		prompt  string
		model   string
		noSess  bool
	}{
		{[]string{"--model", "glm", "объясни", "код"}, "run", "объясни код", "glm", false},
		{[]string{"--json", "--quiet", "что", "в", "go.mod"}, "run", "что в go.mod", "", false},
		{[]string{"--json", "--model", "glm"}, "help", "", "glm", false},
		{[]string{"--"}, "help", "", "", false},
		{[]string{"--", "--json", "это", "флаг"}, "run", "--json это флаг", "", false},
		{[]string{"--no-session", "--", "-v", "вопрос"}, "run", "-v вопрос", "", true},
		{[]string{"--daemon", "status"}, "daemon", "", "", false},
		{[]string{"--session", "use", "abc"}, "session", "", "", false},
		{[]string{"--config"}, "config", "", "", false},
		{[]string{"--doctor"}, "doctor", "", "", false},
		{[]string{"--compact"}, "compact", "", "", false},
		{[]string{"-q", "--model=glm", "привет"}, "run", "привет", "glm", false},
	}

	for _, c := range cases {
		o, err := Parse(c.args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", c.args, err)
		}
		if o.Command != c.command {
			t.Fatalf("Parse(%v) command = %q, want %q", c.args, o.Command, c.command)
		}
		if o.Prompt != c.prompt {
			t.Fatalf("Parse(%v) prompt = %q, want %q", c.args, o.Prompt, c.prompt)
		}
		if o.Model != c.model {
			t.Fatalf("Parse(%v) model = %q, want %q", c.args, o.Model, c.model)
		}
		if o.NoSess != c.noSess {
			t.Fatalf("Parse(%v) noSession = %v", c.args, o.NoSess)
		}
	}
}

func TestUnknownFlagRejected(t *testing.T) {
	if _, err := Parse([]string{"--nope", "запрос"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

func TestSessionUseArgs(t *testing.T) {
	o, err := Parse([]string{"--session", "use", "abc123"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "session" {
		t.Fatalf("command = %q", o.Command)
	}
	if len(o.Args) != 2 || o.Args[0] != "use" || o.Args[1] != "abc123" {
		t.Fatalf("args = %v", o.Args)
	}
}
