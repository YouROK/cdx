package cli

import (
	"time"
)

// nowMillis returns the current time in milliseconds.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// sleepMillis waits for the given number of milliseconds.
func sleepMillis(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
