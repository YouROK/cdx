package cli

import (
	"os"
	"testing"
	"time"

	"github.com/yourok/cdx/internal/config"
)

// TestBellCharIsBEL checks the character itself.
func TestBellCharIsBEL(t *testing.T) {
	if bellChar != "\a" {
		t.Fatalf("bell must be the BEL character, got %q", bellChar)
	}
	if len(bellChar) != 1 || bellChar[0] != 0x07 {
		t.Fatalf("BEL must be 0x07, got %v", []byte(bellChar))
	}
}

// TestBellDisabledByOption checks the disabled option.
func TestBellDisabledByOption(t *testing.T) {
	cfg := config.Default()
	cfg.Output.Bell = false

	b := newBell(cfg, &Options{})
	if b.enabled {
		t.Fatal("bell must be off when the option is off")
	}
	b.ring()
}

// TestBellDisabledByJSON checks that machine output stays intact.
func TestBellDisabledByJSON(t *testing.T) {
	cfg := config.Default()
	cfg.Output.Bell = true

	for _, o := range []*Options{{JSON: true}, {Quiet: true}} {
		if newBell(cfg, o).enabled {
			t.Fatalf("bell must be off in machine output %+v", o)
		}
	}
}

// TestBellDisabledInPipe checks there is no bell outside a terminal.
func TestBellDisabledInPipe(t *testing.T) {
	if isTerminal(os.Stdout) {
		t.Skip("test requires a non-terminal stdout")
	}
	cfg := config.Default()
	cfg.Output.Bell = true

	if newBell(cfg, &Options{}).enabled {
		t.Fatal("bell must be off when stdout is not a terminal")
	}
}

// TestBellSuppressesFastAnswer checks suppression right after the start.
//
// An instant answer must not ring: the user is still at the terminal.
func TestBellSuppressesFastAnswer(t *testing.T) {
	b := &bellEmitter{enabled: true}
	// In tests startedAt is the process start, so the bell right after the
	// launch is suppressed.
	b.ring()
	if !b.last.IsZero() {
		t.Fatal("bell must not ring right after the start")
	}
}

// TestBellSuppressesRepeatedCalls checks suppression of repeats in a row.
func TestBellSuppressesRepeatedCalls(t *testing.T) {
	b := &bellEmitter{enabled: true}
	// The previous bell was just now, so no repeat is needed.
	b.last = time.Now()
	b.ring()
	if time.Since(b.last) > time.Second {
		t.Fatal("bell must not repeat within the quiet period")
	}
}

// TestBellRingsAfterQuiet checks ringing after the pause.
func TestBellRingsAfterQuiet(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("test requires a terminal")
	}
	b := &bellEmitter{enabled: true}
	b.last = time.Now().Add(-bellQuiet - time.Second)

	before := b.last
	b.ring()
	if !b.last.After(before) {
		t.Fatal("bell must ring after the quiet period")
	}
}

// TestBellQuietDuration checks the length of the pause.
func TestBellQuietDuration(t *testing.T) {
	if bellQuiet != 2*time.Second {
		t.Fatalf("quiet period must be two seconds, got %v", bellQuiet)
	}
}
