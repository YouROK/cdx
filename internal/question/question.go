// Package question defines the client question envelope shared by the core, plugins, and clients.
package question

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Question sources and well-known kinds. Kind is a rendering hint, not core logic.
const (
	SourceCore = "core"

	KindPermission = "permission"
	KindStepLimit  = "step_limit"
	KindModel      = "model"
	KindLoop       = "loop"
)

const (
	maxTitle   = 200
	maxBody    = 8000
	maxDetails = 20
	maxOptions = 20
)

// Option is one selectable answer of a question.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Detail is a labelled value shown with the question, such as a command or a path.
type Detail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Question is the envelope a client renders and answers.
type Question struct {
	Source   string   `json:"source,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Title    string   `json:"title"`
	Body     string   `json:"body,omitempty"`
	Details  []Detail `json:"details,omitempty"`
	Options  []Option `json:"options,omitempty"`
	Default  string   `json:"default,omitempty"`
	Text     bool     `json:"text,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
	// Tag identifies the asker for its own hooks; the core does not interpret it.
	Tag string `json:"tag,omitempty"`
}

// Answer is the client reply to a question.
type Answer struct {
	Choice    string `json:"choice,omitempty"`
	Text      string `json:"text,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Validate checks the envelope before it reaches a client.
func (q *Question) Validate() error {
	if q == nil {
		return fmt.Errorf("question is required")
	}
	if strings.TrimSpace(q.Title) == "" {
		return fmt.Errorf("question title is required")
	}
	if len(q.Title) > maxTitle {
		return fmt.Errorf("question title is longer than %d characters", maxTitle)
	}
	if len(q.Body) > maxBody {
		return fmt.Errorf("question body is longer than %d characters", maxBody)
	}
	if len(q.Details) > maxDetails {
		return fmt.Errorf("question has more than %d details", maxDetails)
	}
	if q.Multiple {
		return fmt.Errorf("multiple selection is not supported yet")
	}
	if len(q.Options) == 0 && !q.Text {
		return fmt.Errorf("question needs options or free text")
	}
	if len(q.Options) > maxOptions {
		return fmt.Errorf("question has more than %d options", maxOptions)
	}
	if len(q.Tag) > 64 {
		return fmt.Errorf("question tag is longer than 64 characters")
	}

	seen := make(map[string]bool, len(q.Options))
	for i, opt := range q.Options {
		id := strings.TrimSpace(opt.ID)
		if id == "" {
			return fmt.Errorf("question option %d has no id", i+1)
		}
		if strings.TrimSpace(opt.Label) == "" {
			return fmt.Errorf("question option %q has no label", id)
		}
		if seen[id] {
			return fmt.Errorf("question option id %q is duplicated", id)
		}
		seen[id] = true
	}
	if q.Default != "" && !seen[q.Default] {
		return fmt.Errorf("question default %q is not an option", q.Default)
	}
	return nil
}

// FromMap decodes a question envelope received from a plugin or hook payload.
func FromMap(raw map[string]any) (Question, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return Question{}, err
	}
	var q Question
	if err := json.Unmarshal(data, &q); err != nil {
		return Question{}, fmt.Errorf("parse question: %w", err)
	}
	return q, nil
}

// Asker routes a question to a client and waits for the answer.
type Asker interface {
	Ask(ctx context.Context, q Question) (Answer, error)
}

type askerKey struct{}

// WithAsker attaches a question asker to a turn context.
func WithAsker(ctx context.Context, asker Asker) context.Context {
	if asker == nil {
		return ctx
	}
	return context.WithValue(ctx, askerKey{}, asker)
}

// AskerFromContext returns the asker attached to ctx, or nil.
func AskerFromContext(ctx context.Context) Asker {
	if ctx == nil {
		return nil
	}
	asker, _ := ctx.Value(askerKey{}).(Asker)
	return asker
}

// Ask validates the question and routes it through the asker attached to ctx.
// Without a client the answer is cancelled instead of blocking the turn.
func Ask(ctx context.Context, q Question) (Answer, error) {
	if err := q.Validate(); err != nil {
		return Answer{}, err
	}
	asker := AskerFromContext(ctx)
	if asker == nil {
		return Answer{Cancelled: true, Reason: "no interactive client"}, nil
	}
	return asker.Ask(ctx, q)
}

// AnswerFromMap converts a client reply frame into an Answer.
func AnswerFromMap(raw map[string]any) Answer {
	var answer Answer
	if raw == nil {
		return Answer{Cancelled: true, Reason: "empty answer"}
	}
	if choice, ok := raw["choice"].(string); ok {
		answer.Choice = choice
	}
	if text, ok := raw["text"].(string); ok {
		answer.Text = text
	}
	if cancelled, ok := raw["cancelled"].(bool); ok {
		answer.Cancelled = cancelled
	}
	if reason, ok := raw["reason"].(string); ok {
		answer.Reason = reason
	}
	if !answer.Cancelled && answer.Choice == "" && answer.Text == "" {
		if allow, ok := raw["allow"].(bool); ok && !allow {
			answer.Cancelled = true
			answer.Reason = "denied"
		}
	}
	return answer
}
