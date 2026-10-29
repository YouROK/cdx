package cli

import (
	"strings"
	"testing"
)

// TestSpawnUsesCurrentScheme checks the daemon launch arguments.
//
// The hidden command must match the current scheme: a bare "daemon serve"
// parses as a model request, and such a request starts a daemon again,
// yielding an endless chain of daemons.
func TestSpawnUsesCurrentScheme(t *testing.T) {
	args := []string{"--daemon", "serve"}

	o, err := Parse(args)
	if err != nil {
		t.Fatalf("Parse(%v): %v", args, err)
	}
	if o.Command != "daemon" {
		t.Fatalf("spawn command must be daemon, got %q", o.Command)
	}
	if len(o.Args) != 1 || o.Args[0] != "serve" {
		t.Fatalf("unexpected spawn args %v", o.Args)
	}
	// No request may appear, else the process would go to the model.
	if o.Prompt != "" {
		t.Fatalf("spawn must not become a request, got prompt %q", o.Prompt)
	}
}

// TestOldSpawnFormIsRequest checks the old form does not start a daemon.
//
// Such a line is now a request, which is exactly how the daemon avalanche started.
func TestOldSpawnFormIsRequest(t *testing.T) {
	o, err := Parse([]string{"daemon", "serve"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Command != "run" {
		t.Fatalf("old form must parse as a request, got %q", o.Command)
	}
	if !strings.Contains(o.Prompt, "daemon") {
		t.Fatalf("unexpected prompt %q", o.Prompt)
	}
}
