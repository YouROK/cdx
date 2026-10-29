package cli

// The audible bell the program uses to call the user.
//
// A user starts a request and walks away, so the bell is needed every time
// the program stops and waits for them: an answer, an error, a permission
// prompt. A bell right after the start is pointless though, since the user
// is still looking at the terminal. The same goes for two occasions in a
// row: the model answered and at once asked for permission, so there is no
// reason to ring twice.

import (
	"fmt"
	"os"
	"time"

	"github.com/yourok/cdx/internal/config"
)

// bellChar is the bell character (BEL).
const bellChar = "\a"

// bellQuiet is the pause during which no further bell is given.
//
// It is counted from both the program start and the previous bell, which
// suppresses both instant answers and a run of occasions.
const bellQuiet = 2 * time.Second

// bellEmitter rings with debouncing.
type bellEmitter struct {
	// enabled turns the bell off entirely: option, JSON, quiet mode, pipe.
	enabled bool
	// last is the time of the previous bell; zero means none yet.
	last time.Time
}

// newBell builds the bell from the settings and the output mode.
//
// It is off when the option is disabled, when machine-readable or quiet
// output is on, or when the output is not a terminal, where the character
// would end up in the data.
func newBell(cfg *config.Config, o *Options) *bellEmitter {
	enabled := cfg != nil && cfg.Output.Bell
	if o != nil && (o.JSON || o.Quiet) {
		enabled = false
	}
	return &bellEmitter{enabled: enabled && isTerminal(os.Stdout)}
}

// ring calls the user.
//
// It rings only when the program has run longer than the pause and as long
// has passed since the previous bell. Otherwise the user is still there.
func (b *bellEmitter) ring() {
	if b == nil || !b.enabled {
		return
	}

	now := time.Now()
	if b.last.IsZero() {
		// First occasion: compared against the start time.
		if time.Since(startedAt) < bellQuiet {
			return
		}
	} else if now.Sub(b.last) < bellQuiet {
		return
	}

	fmt.Fprint(os.Stdout, bellChar)
	b.last = now
}
