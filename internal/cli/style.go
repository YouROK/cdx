package cli

// Terminal line styling.

import (
	"os"

	"golang.org/x/sys/unix"
)

// ANSI sequences for the permission prompt line.
const (
	styleBold   = "\033[1m"
	styleDim    = "\033[2m"
	styleYellow = "\033[33m"
	styleRed    = "\033[31m"
	styleReset  = "\033[0m"
)

// colorOff records that --no-color disabled styling.
var colorOff bool

// disableColor disables styling for the whole run.
func disableColor() { colorOff = true }

// styleEnabled reports whether the output may be colored.
//
// Control sequences never go to redirected output, where they would turn
// into garbage.
func styleEnabled() bool {
	if colorOff {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	_, err := unix.IoctlGetTermios(int(os.Stderr.Fd()), unix.TCGETS)
	return err == nil
}

// styleAction highlights the action in bold.
//
// Bold rather than color, because color already marks the permission level
// and two color accents on one line compete with each other.
func styleAction(text string) string {
	if !styleEnabled() {
		return text
	}
	return styleBold + text + styleReset
}

// styleLevel marks the permission level in a muted color.
//
// Dangerous is red and editing is yellow, so the risk shows without the
// line reading as an error.
func styleLevel(level string) string {
	if !styleEnabled() {
		return "(" + level + ")"
	}

	color := styleDim
	switch level {
	case "dangerous":
		color = styleRed
	case "edit":
		color = styleYellow
	}
	return color + "(" + level + ")" + styleReset
}
