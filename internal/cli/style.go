package cli

import "os"

const (
	styleBold   = "\033[1m"
	styleDim    = "\033[2m"
	styleYellow = "\033[33m"
	styleRed    = "\033[31m"
	styleReset  = "\033[0m"
)

var colorOff bool

func disableColor() { colorOff = true }

func styleEnabled() bool {
	if colorOff {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stderr)
}

func styleAction(text string) string {
	if !styleEnabled() {
		return text
	}
	return styleBold + text + styleReset
}

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
