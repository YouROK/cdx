package cli

// Terminal width for clipping lines.

import (
	"os"

	"golang.org/x/sys/unix"
)

// defaultWidth is the width used when the window size is unknown.
const defaultWidth = 100

// terminalWidth returns the window width in columns.
//
// The size is read on every call, since the window can be resized while
// the line has to follow.
func terminalWidth() int {
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 {
			return int(ws.Col)
		}
	}
	return defaultWidth
}

// clipText clips text to the width, ending it with an ellipsis.
//
// Characters are counted, not bytes, since Cyrillic takes one cell each.
// The text must stay on one line because the progress line is redrawn in
// place: a wrap splits it over several lines that never get erased.
func clipText(text string, width int) string {
	if width <= 0 {
		width = defaultWidth
	}
	// One cell is left for the spinner and one for the space after it.
	limit := width - 3
	if limit < 8 {
		limit = 8
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}
