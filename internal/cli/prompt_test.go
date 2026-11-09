package cli

import (
	"bytes"
	"strings"
	"testing"
)

func shellQuestion(command string) map[string]any {
	return map[string]any{
		"kind":       "permission",
		"tool":       "shell",
		"permission": "dangerous",
		"title":      "Run a shell command",
		"details": []any{
			map[string]any{"label": "Command", "value": command},
		},
		"options": []any{"once", "session", "daemon", "always", "deny"},
	}
}

func TestPermissionPromptPrintsTheFullCommand(t *testing.T) {
	disableColor()
	command := "cd /home/yourok/project/cdx_plugins/web-search && ls -la && find . -type f | head -50"
	p := buildPermissionPrompt(shellQuestion(command), 60)
	output := strings.Join(p.lines, "\n")

	if strings.Contains(output, "...") {
		t.Fatalf("the command must not be clipped:\n%s", output)
	}
	if !strings.Contains(output, "Run a shell command") {
		t.Fatalf("the title is missing:\n%s", output)
	}
	if !strings.Contains(output, "dangerous") {
		t.Fatalf("the permission level is missing:\n%s", output)
	}
	if !strings.Contains(output, "once (default)") {
		t.Fatalf("the default option is not marked:\n%s", output)
	}
	if p.prompt != "Choose [1]: " {
		t.Fatalf("unexpected prompt %q", p.prompt)
	}
	// Every word of the command stays in the output, in order.
	flat := strings.Join(strings.Fields(output), " ")
	if !strings.Contains(flat, command) {
		t.Fatalf("the full command is missing:\n%s", output)
	}
	for _, line := range p.lines {
		if len([]rune(line)) > 60 {
			t.Fatalf("line is wider than the terminal: %q", line)
		}
	}
}

func TestPermissionPromptWrapsLongWords(t *testing.T) {
	disableColor()
	command := "/very/long/path/without/any/spaces/that/must/be/broken/across/lines/for/the/terminal"
	p := buildPermissionPrompt(shellQuestion(command), 50)
	for _, line := range p.lines {
		if len([]rune(line)) > 50 {
			t.Fatalf("line is wider than the terminal: %q", line)
		}
	}
	joined := strings.ReplaceAll(strings.Join(p.lines, ""), " ", "")
	if !strings.Contains(joined, command) {
		t.Fatalf("the command was damaged by wrapping:\n%s", strings.Join(p.lines, "\n"))
	}
}

func TestPermissionPromptOutsideFolder(t *testing.T) {
	disableColor()
	question := map[string]any{
		"kind":       "outside_workspace",
		"tool":       "shell",
		"permission": "dangerous",
		"title":      "Run a shell command outside the working folder",
		"details": []any{
			map[string]any{"label": "Command", "value": "cd /tmp && rm -rf build"},
			map[string]any{"label": "Working folder", "value": "/home/yourok/project"},
			map[string]any{"label": "Outside", "value": "/tmp"},
		},
		"options": []any{"once", "deny"},
	}
	p := buildPermissionPrompt(question, 80)
	output := strings.Join(p.lines, "\n")

	if !strings.Contains(output, "Working folder: /home/yourok/project") {
		t.Fatalf("the working folder is missing:\n%s", output)
	}
	if !strings.Contains(output, "Outside: /tmp") {
		t.Fatalf("the outside path is missing:\n%s", output)
	}
	if strings.Contains(output, "once   [n]") {
		t.Fatalf("the option legend must not be printed:\n%s", output)
	}
	if p.prompt != "Allow once? [Y/n]: " {
		t.Fatalf("unexpected prompt %q", p.prompt)
	}
	if got := parsePromptAnswer("", p); got != "once" {
		t.Fatalf("enter must choose once, got %q", got)
	}
	if got := parsePromptAnswer("n\n", p); got != "deny" {
		t.Fatalf("n must choose deny, got %q", got)
	}
	if got := parsePromptAnswer("y", p); got != "once" {
		t.Fatalf("y must choose once, got %q", got)
	}
}

func TestParsePromptAnswer(t *testing.T) {
	disableColor()
	p := buildPermissionPrompt(shellQuestion("ls"), 80)
	cases := map[string]string{
		"":          "once",
		"\n":        "once",
		"1":         "once",
		"2":         "session",
		"3":         "daemon",
		"4":         "always",
		"5":         "deny",
		"always":    "always",
		"SESSION":   "session",
		"y":         "once",
		"n":         "deny",
		" nonsense": "deny",
		"9":         "deny",
	}
	for answer, want := range cases {
		if got := parsePromptAnswer(answer, p); got != want {
			t.Fatalf("answer %q: got %q, want %q", answer, got, want)
		}
	}
}

func TestAskPermissionPromptReadsAnswer(t *testing.T) {
	disableColor()
	command := "cd /home/yourok/project && ls -la"

	var stderr bytes.Buffer
	app := &App{Stdin: strings.NewReader("\n"), Stdout: &bytes.Buffer{}, Stderr: &stderr}

	if got := app.askPermissionPrompt(shellQuestion(command)); got != "once" {
		t.Fatalf("enter must choose once, got %q", got)
	}
	if !strings.Contains(stderr.String(), command) {
		t.Fatalf("the question must show the full command:\n%s", stderr.String())
	}

	stderr.Reset()
	app.Stdin = strings.NewReader("3\n")
	if got := app.askPermissionPrompt(shellQuestion(command)); got != "daemon" {
		t.Fatalf("answer 3 must choose daemon, got %q", got)
	}

	stderr.Reset()
	app.Stdin = strings.NewReader("")
	if got := app.askPermissionPrompt(shellQuestion(command)); got != "deny" {
		t.Fatalf("a closed input must deny, got %q", got)
	}
}

func stepLimitQuestion() map[string]any {
	return map[string]any{
		"source": "core",
		"kind":   "step_limit",
		"title":  "The model made 3 requests without a final answer.",
		"body":   "Continue with 2 more steps?",
		"options": []any{
			map[string]any{"id": "continue", "label": "Continue"},
			map[string]any{"id": "stop", "label": "Stop"},
		},
		"default": "continue",
	}
}

func TestQuestionPromptRendersStepLimit(t *testing.T) {
	p := buildQuestionPrompt(stepLimitQuestion(), 80)
	output := strings.Join(p.lines, "\n")
	if !strings.Contains(output, "Continue with 2 more steps?") {
		t.Fatalf("body is missing:\n%s", output)
	}
	if !strings.Contains(output, "[2] Stop") {
		t.Fatalf("options are missing:\n%s", output)
	}
	if p.prompt != "Choose [1]: " {
		t.Fatalf("prompt = %q, want the default option", p.prompt)
	}
}

func TestQuestionAnswerParsing(t *testing.T) {
	p := buildQuestionPrompt(stepLimitQuestion(), 80)
	cases := map[string]string{
		"":         "continue",
		"1":        "continue",
		"2":        "stop",
		"n":        "stop",
		"Continue": "continue",
	}
	for input, want := range cases {
		answer := parseQuestionAnswer(input, p)
		if answer["choice"] != want {
			t.Fatalf("parseQuestionAnswer(%q) = %v, want %q", input, answer, want)
		}
	}
}

func TestQuestionPromptAcceptsFreeText(t *testing.T) {
	p := buildQuestionPrompt(map[string]any{
		"source": "plugin:ask",
		"kind":   "model",
		"title":  "What should the project be called?",
		"text":   true,
	}, 80)
	if p.prompt != "Answer: " {
		t.Fatalf("prompt = %q, want a free-text prompt", p.prompt)
	}
	answer := parseQuestionAnswer("  my-project  ", p)
	if answer["text"] != "my-project" {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}

func TestQuestionPromptRejectsUnknownOption(t *testing.T) {
	p := buildQuestionPrompt(stepLimitQuestion(), 80)
	answer := parseQuestionAnswer("maybe", p)
	if answer["cancelled"] != true {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}
