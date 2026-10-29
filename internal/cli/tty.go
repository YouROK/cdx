package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminal reports whether the stream is attached to a terminal.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
