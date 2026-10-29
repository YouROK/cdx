package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/llm"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/registry"
)

// testOptions builds the minimal turn parameters.
func testOptions(provider llm.Provider) Options {
	pm, _ := perm.New(config.Default())
	return Options{
		Provider: provider,
		Model:    "m",
		Prompt:   "system",
		Registry: registry.NewRegistry(),
		Perms:    pm,
	}
}

// overflowProvider answers with an overflow error.
type overflowProvider struct{ detail string }

func (p *overflowProvider) Name() string { return "test" }

func (p *overflowProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, &llm.ContextOverflowError{Detail: p.detail}
}

// TestOverflowFailureHasHint checks a clear refusal on overflow.
func TestOverflowFailureHasHint(t *testing.T) {
	res := Run(context.Background(),
		testOptions(&overflowProvider{detail: "maximum context length is 8192 tokens"}), "запрос")

	if res.Error == nil {
		t.Fatal("expected a failure")
	}
	if res.Error.Code != "context_overflow" {
		t.Fatalf("unexpected code %q", res.Error.Code)
	}
	if !strings.Contains(res.Error.Hint, "cdx --compact") {
		t.Fatalf("hint must name the compact command: %q", res.Error.Hint)
	}
	if !strings.Contains(res.Error.Hint, "8192") {
		t.Fatalf("hint must keep the provider detail: %q", res.Error.Hint)
	}
	if !strings.Contains(res.Error.Hint, "context window") {
		t.Fatalf("hint must explain what to set: %q", res.Error.Hint)
	}
}

// TestOtherFailureStaysGeneric checks other errors are not passed off
// as an overflow.
func TestOtherFailureStaysGeneric(t *testing.T) {
	res := Run(context.Background(), testOptions(&failingProvider{}), "запрос")

	if res.Error == nil {
		t.Fatal("expected a failure")
	}
	if res.Error.Code != "llm_error" {
		t.Fatalf("unexpected code %q", res.Error.Code)
	}
}

// TestTimeoutHasOwnCode checks a stalled provider gets its own code.
func TestTimeoutHasOwnCode(t *testing.T) {
	res := Run(context.Background(), testOptions(&timeoutProvider{}), "запрос")

	if res.Error == nil || res.Error.Code != "llm_timeout" {
		t.Fatalf("expected a timeout code, got %+v", res.Error)
	}
	if res.Error.Hint == "" {
		t.Fatal("timeout must explain what to change")
	}
}

// TestCancelHasOwnCode checks an interrupted request gets its own code.
func TestCancelHasOwnCode(t *testing.T) {
	res := Run(context.Background(), testOptions(&cancelProvider{}), "запрос")

	if res.Error == nil || res.Error.Code != "canceled" {
		t.Fatalf("expected a cancel code, got %+v", res.Error)
	}
}

// failingProvider answers with a plain error.
type failingProvider struct{}

func (p *failingProvider) Name() string { return "test" }

func (p *failingProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("connection refused")
}

// timeoutProvider answers with a timeout.
type timeoutProvider struct{}

func (p *timeoutProvider) Name() string { return "test" }

func (p *timeoutProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, context.DeadlineExceeded
}

// cancelProvider answers with a cancellation.
type cancelProvider struct{}

func (p *cancelProvider) Name() string { return "test" }

func (p *cancelProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, context.Canceled
}
