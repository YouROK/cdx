package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

const defaultWidth = 100

func terminalWidth() int {
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err == nil && ws.Col > 0 {
			return int(ws.Col)
		}
	}
	return defaultWidth
}

func clipText(text string, width int) string {
	if width <= 0 {
		width = defaultWidth
	}
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
