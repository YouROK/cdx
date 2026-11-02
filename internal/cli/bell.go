package cli

import (
	"fmt"
	"io"
	"os"
	"time"
)

const (
	bellChar  = "\a"
	bellQuiet = 2 * time.Second
)

var startedAt = time.Now()

type bellEmitter struct {
	enabled bool
	out     io.Writer
	last    time.Time
}

func newBell(enabled bool, out io.Writer) *bellEmitter {
	f, ok := out.(*os.File)
	isTTY := ok && isTerminal(f)
	return &bellEmitter{
		enabled: enabled && isTTY,
		out:     out,
	}
}

func (b *bellEmitter) ring() {
	if b == nil || !b.enabled {
		return
	}
	now := time.Now()
	if b.last.IsZero() {
		if time.Since(startedAt) < bellQuiet {
			return
		}
	} else if now.Sub(b.last) < bellQuiet {
		return
	}
	fmt.Fprint(b.out, bellChar)
	b.last = now
}
