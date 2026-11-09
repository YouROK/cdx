package question

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type staticAsker struct {
	questions []Question
	answer    Answer
	err       error
}

func (a *staticAsker) Ask(_ context.Context, q Question) (Answer, error) {
	a.questions = append(a.questions, q)
	return a.answer, a.err
}

func TestValidateAcceptsPermissionQuestion(t *testing.T) {
	q := Question{
		Kind:    KindPermission,
		Title:   "Run the command?",
		Body:    "rm -rf build",
		Details: []Detail{{Label: "Folder", Value: "/home/user/proj"}},
		Options: []Option{{ID: "once", Label: "Allow once"}, {ID: "deny", Label: "Deny"}},
		Default: "once",
	}
	if err := q.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestValidateRejectsInvalidQuestions(t *testing.T) {
	cases := []struct {
		name string
		q    Question
		want string
	}{
		{"empty title", Question{Options: []Option{{ID: "a", Label: "A"}}}, "title"},
		{"no answer mode", Question{Title: "question"}, "options or free text"},
		{"duplicate option", Question{Title: "question", Options: []Option{{ID: "a", Label: "A"}, {ID: "a", Label: "B"}}}, "duplicated"},
		{"empty option id", Question{Title: "question", Options: []Option{{Label: "A"}}}, "no id"},
		{"empty option label", Question{Title: "question", Options: []Option{{ID: "a"}}}, "no label"},
		{"unknown default", Question{Title: "question", Options: []Option{{ID: "a", Label: "A"}}, Default: "b"}, "default"},
		{"multiple", Question{Title: "question", Options: []Option{{ID: "a", Label: "A"}}, Multiple: true}, "multiple"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestAskWithoutAskerCancels(t *testing.T) {
	answer, err := Ask(context.Background(), Question{Title: "question", Text: true})
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if !answer.Cancelled || answer.Reason == "" {
		t.Fatalf("expected a cancelled answer, got %+v", answer)
	}
}

func TestAskRoutesToAsker(t *testing.T) {
	asker := &staticAsker{answer: Answer{Choice: "once"}}
	ctx := WithAsker(context.Background(), asker)

	answer, err := Ask(ctx, Question{
		Title:   "Run?",
		Options: []Option{{ID: "once", Label: "Allow once"}, {ID: "deny", Label: "Deny"}},
	})
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if answer.Choice != "once" {
		t.Fatalf("answer = %+v, want once", answer)
	}
	if len(asker.questions) != 1 || asker.questions[0].Title != "Run?" {
		t.Fatalf("asker did not receive the question: %+v", asker.questions)
	}
}

func TestAskReturnsAskerError(t *testing.T) {
	want := errors.New("client gone")
	asker := &staticAsker{err: want}
	ctx := WithAsker(context.Background(), asker)

	if _, err := Ask(ctx, Question{Title: "question", Text: true}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestAnswerFromMap(t *testing.T) {
	if got := AnswerFromMap(map[string]any{"choice": "stop"}); got.Choice != "stop" || got.Cancelled {
		t.Fatalf("unexpected answer: %+v", got)
	}
	if got := AnswerFromMap(map[string]any{"text": "hello"}); got.Text != "hello" {
		t.Fatalf("unexpected answer: %+v", got)
	}
	if got := AnswerFromMap(map[string]any{"cancelled": true, "reason": "closed"}); !got.Cancelled || got.Reason != "closed" {
		t.Fatalf("unexpected answer: %+v", got)
	}
	if got := AnswerFromMap(map[string]any{"allow": false}); !got.Cancelled {
		t.Fatalf("denied legacy answer must be cancelled: %+v", got)
	}
}
