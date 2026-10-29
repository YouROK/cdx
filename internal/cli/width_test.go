package cli

import (
	"strings"
	"testing"
)

// TestClipText checks clipping to the width.
func TestClipText(t *testing.T) {
	short := "git status"
	if got := clipText(short, 80); got != short {
		t.Fatalf("short text must stay intact, got %q", got)
	}

	long := strings.Repeat("x", 200)
	got := clipText(long, 40)
	if len(got) > 40 {
		t.Fatalf("text must fit the width, got %d runes", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("clipped text must end with an ellipsis, got %q", got)
	}
}

// TestClipTextCountsRunes checks that Cyrillic counts by characters.
//
// Byte clipping would cut a multibyte character in half and yield garbage.
func TestClipTextCountsRunes(t *testing.T) {
	text := strings.Repeat("привет", 40)
	got := clipText(text, 30)
	if len([]rune(got)) > 30 {
		t.Fatalf("text must fit the width, got %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("clipped text must end with an ellipsis")
	}
	// Not a single broken character: the string must be valid UTF-8.
	if strings.ContainsRune(got, 0xFFFD) {
		t.Fatalf("text must not contain broken runes: %q", got)
	}
}

// TestClipTextTinyWidth checks a very narrow window.
func TestClipTextTinyWidth(t *testing.T) {
	got := clipText("git log --oneline", 5)
	if got == "" {
		t.Fatal("some text must remain even in a narrow window")
	}
}

// TestTerminalWidthDefault checks the fallback width.
func TestTerminalWidthDefault(t *testing.T) {
	if defaultWidth < 40 {
		t.Fatalf("default width must be usable, got %d", defaultWidth)
	}
}
