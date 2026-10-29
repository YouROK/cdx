package llm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestIsContextOverflow checks overflow wordings are recognized.
func TestIsContextOverflow(t *testing.T) {
	overflow := []string{
		`{"error":{"code":"context_length_exceeded"}}`,
		"This model's maximum context length is 8192 tokens",
		"maximum context window exceeded",
		"the prompt is too long for this model",
		"input is too large",
		"too many tokens in request",
		"Please reduce the length of the messages",
		"request exceeds the model context window",
		"token limit exceeded",
		"prompt too long",
	}
	for _, text := range overflow {
		if !IsContextOverflow(text) {
			t.Fatalf("must recognise overflow in %q", text)
		}
	}

	other := []string{
		"",
		"invalid api key",
		"rate limit exceeded, retry later",
		"insufficient quota",
		"connection refused",
		"model not found",
		"internal server error",
	}
	for _, text := range other {
		if IsContextOverflow(text) {
			t.Fatalf("must not treat %q as overflow", text)
		}
	}
}

// TestContextOverflowErrorIs checks comparison through errors.Is.
func TestContextOverflowErrorIs(t *testing.T) {
	base := &ContextOverflowError{Detail: "too long"}
	if !errors.Is(base, ErrContextOverflow) {
		t.Fatal("direct error must match the sentinel")
	}
	if !errors.Is(fmt.Errorf("chat failed: %w", base), ErrContextOverflow) {
		t.Fatal("wrapped error must match the sentinel")
	}
	if errors.Is(fmt.Errorf("other failure"), ErrContextOverflow) {
		t.Fatal("unrelated error must not match")
	}
}

// TestContextOverflowMessage checks the error text carries the provider answer.
func TestContextOverflowMessage(t *testing.T) {
	withDetail := &ContextOverflowError{Detail: "maximum context length is 8192"}
	if !strings.Contains(withDetail.Error(), "8192") {
		t.Fatalf("provider detail must be kept: %q", withDetail.Error())
	}

	bare := &ContextOverflowError{}
	if !strings.Contains(bare.Error(), "context window") {
		t.Fatalf("unexpected text %q", bare.Error())
	}
}
