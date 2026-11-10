package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yourok/cdx/internal/question"
)

// fakeAsker records questions and returns prepared answers.
type fakeAsker struct {
	answers   []question.Answer
	questions []question.Question
}

// Ask returns the next prepared answer.
func (f *fakeAsker) Ask(ctx context.Context, q question.Question) (question.Answer, error) {
	f.questions = append(f.questions, q)
	if len(f.answers) < len(f.questions) {
		return question.Answer{Cancelled: true, Reason: "no answer"}, nil
	}
	return f.answers[len(f.questions)-1], nil
}

// TestElicitMCP covers the elicitation to question mapping.
func TestElicitMCP(t *testing.T) {
	// Properties are asked in sorted order: count, mode, name, ok.
	asker := &fakeAsker{answers: []question.Answer{
		{Text: "2"},
		{Choice: "o2"},
		{Text: "octocat"},
		{Choice: "yes"},
	}}
	ctx := question.WithAsker(context.Background(), asker)

	raw, err := json.Marshal(map[string]any{
		"mode":    "form",
		"message": "Sign in to GitHub",
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":  map[string]any{"type": "string", "description": "User name"},
				"count": map[string]any{"type": "integer"},
				"mode":  map[string]any{"type": "string", "enum": []any{"alpha", "beta"}},
				"ok":    map[string]any{"type": "boolean"},
			},
		},
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	result, err := elicitMCP(ctx, raw)
	if err != nil {
		t.Fatalf("elicitation: %v", err)
	}
	if result["action"] != "accept" {
		t.Fatalf("unexpected action: %v", result["action"])
	}
	content, _ := result["content"].(map[string]any)
	if content["name"] != "octocat" {
		t.Fatalf("unexpected name: %v", content["name"])
	}
	if content["count"] != int64(2) {
		t.Fatalf("unexpected count: %v (%T)", content["count"], content["count"])
	}
	if content["mode"] != "beta" {
		t.Fatalf("the enum label was not restored: %v", content["mode"])
	}
	if content["ok"] != true {
		t.Fatalf("unexpected boolean: %v", content["ok"])
	}
	if len(asker.questions) != 4 {
		t.Fatalf("unexpected question count: %d", len(asker.questions))
	}
	if asker.questions[2].Body != "User name" {
		t.Fatalf("the property description is missing: %+v", asker.questions[2])
	}
	if asker.questions[1].Options[1].Label != "beta" {
		t.Fatalf("unexpected enum options: %+v", asker.questions[1].Options)
	}
}

// TestElicitMCPCancel covers a cancelled answer.
func TestElicitMCPCancel(t *testing.T) {
	asker := &fakeAsker{answers: []question.Answer{{Cancelled: true}}}
	ctx := question.WithAsker(context.Background(), asker)

	raw, _ := json.Marshal(map[string]any{
		"mode":            "form",
		"message":         "Name",
		"requestedSchema": map[string]any{"properties": map[string]any{"name": map[string]any{"type": "string"}}},
	})
	result, err := elicitMCP(ctx, raw)
	if err != nil {
		t.Fatalf("elicitation: %v", err)
	}
	if result["action"] != "cancel" {
		t.Fatalf("unexpected action: %v", result["action"])
	}
}

// TestElicitMCPURLMode covers the unsupported url mode.
func TestElicitMCPURLMode(t *testing.T) {
	ctx := question.WithAsker(context.Background(), &fakeAsker{})
	raw, _ := json.Marshal(map[string]any{"mode": "url", "message": "Open the page", "url": "https://example.com"})
	result, err := elicitMCP(ctx, raw)
	if err != nil {
		t.Fatalf("elicitation: %v", err)
	}
	if result["action"] != "decline" {
		t.Fatalf("unexpected action: %v", result["action"])
	}
}

// TestMCPRoots covers the advertised root of the turn.
func TestMCPRoots(t *testing.T) {
	roots := mcpRoots("/home/user/project")
	if len(roots) != 1 {
		t.Fatalf("unexpected roots: %+v", roots)
	}
	if roots[0]["uri"] != "file:///home/user/project" || roots[0]["name"] != "project" {
		t.Fatalf("unexpected root: %+v", roots[0])
	}
	if roots := mcpRoots(""); len(roots) != 0 {
		t.Fatalf("an empty working directory produced a root: %+v", roots)
	}
}
