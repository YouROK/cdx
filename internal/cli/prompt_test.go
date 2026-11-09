package cli

import (
	"bytes"
	"strings"
	"testing"
)

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
		"N":        "stop",
		"Y":        "continue",
		"YES":      "continue",
		"Continue": "continue",
	}
	for input, want := range cases {
		answer, ok := parseQuestionAnswer(input, p)
		if !ok || answer["choice"] != want {
			t.Fatalf("parseQuestionAnswer(%q) = %v/%v, want %q", input, answer, ok, want)
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
	answer, ok := parseQuestionAnswer("  my-project  ", p)
	if !ok || answer["text"] != "my-project" {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}

func TestQuestionPromptRejectsUnknownAnswer(t *testing.T) {
	p := buildQuestionPrompt(stepLimitQuestion(), 80)
	if _, ok := parseQuestionAnswer("maybe", p); ok {
		t.Fatal("an unknown answer must ask again")
	}
}

func permissionOptionsQuestion() map[string]any {
	return map[string]any{
		"source": "plugin",
		"kind":   "permission",
		"title":  "Run the command?",
		"options": []any{
			map[string]any{"id": "once", "label": "Allow once"},
			map[string]any{"id": "deny", "label": "Deny"},
		},
		"default": "once",
	}
}

func TestQuestionPromptPermissionIsCaseInsensitive(t *testing.T) {
	p := buildQuestionPrompt(permissionOptionsQuestion(), 80)
	if answer, ok := parseQuestionAnswer("Y", p); !ok || answer["choice"] != "once" {
		t.Fatalf("Y must allow once, got %+v", answer)
	}
	if answer, ok := parseQuestionAnswer("n", p); !ok || answer["choice"] != "deny" {
		t.Fatalf("n must deny, got %+v", answer)
	}
	if _, ok := parseQuestionAnswer("н", p); ok {
		t.Fatal("a Cyrillic letter is not an answer")
	}
}

func TestReadQuestionAnswerRetriesUntilValid(t *testing.T) {
	var stderr bytes.Buffer
	app := &App{Stdin: strings.NewReader("н\nN\n"), Stderr: &stderr}
	p := buildQuestionPrompt(permissionOptionsQuestion(), 80)

	answer := app.readQuestionAnswer(p)
	if answer["choice"] != "deny" {
		t.Fatalf("unexpected answer: %+v", answer)
	}
	if !strings.Contains(stderr.String(), "Unknown answer") {
		t.Fatalf("the retry hint is missing:\n%s", stderr.String())
	}
}

func TestReadQuestionAnswerUsesDefaultAfterRetry(t *testing.T) {
	var stderr bytes.Buffer
	app := &App{Stdin: strings.NewReader("maybe\n\n"), Stderr: &stderr}
	p := buildQuestionPrompt(stepLimitQuestion(), 80)

	answer := app.readQuestionAnswer(p)
	if answer["choice"] != "continue" {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}

func TestQuestionPromptUsesFirstOptionAsDefault(t *testing.T) {
	raw := map[string]any{
		"source": "plugin:ask",
		"kind":   "model",
		"title":  "Which one?",
		"options": []any{
			map[string]any{"id": "o1", "label": "Option A"},
			map[string]any{"id": "o2", "label": "Option B"},
		},
	}
	p := buildQuestionPrompt(raw, 80)
	if p.prompt != "Choose [1]: " {
		t.Fatalf("prompt = %q, want the first option as default", p.prompt)
	}
	answer, ok := parseQuestionAnswer("", p)
	if !ok || answer["choice"] != "o1" {
		t.Fatalf("Enter must pick the first option, got %+v", answer)
	}
}

func TestQuestionPromptTextOnlyCancelsOnEmpty(t *testing.T) {
	p := buildQuestionPrompt(map[string]any{
		"source": "plugin:ask",
		"kind":   "model",
		"title":  "Name it",
		"text":   true,
	}, 80)
	answer, ok := parseQuestionAnswer("", p)
	if !ok || answer["cancelled"] != true {
		t.Fatalf("empty text answer must cancel, got %+v", answer)
	}
}
