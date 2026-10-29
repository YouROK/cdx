package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/ipc"
)

// progressLine draws a single progress line and updates it in place.
type progressLine struct {
	mu     sync.Mutex
	color  bool
	anim   animation
	text   string
	frame  int
	stop   chan struct{}
	done   chan struct{}
	active bool
}

// newProgressLine starts drawing.
func newProgressLine(color bool) *progressLine {
	p := &progressLine{
		color:  color,
		anim:   activeAnimation,
		text:   "starting...",
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		active: true,
	}
	go p.loop()
	return p
}

// loop redraws the line using the animation's pauses.
//
// Every frame carries its own pause, which is what gives the motion its
// inertia. A timer rather than a ticker, because the interval changes from
// frame to frame.
func (p *progressLine) loop() {
	defer close(p.done)

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-timer.C:
			p.mu.Lock()
			if !p.active {
				p.mu.Unlock()
				return
			}
			frame := p.anim.textAt(p.frame)
			p.frame++
			text := p.text
			p.mu.Unlock()

			// The pause is taken for the next frame: the current one sets it.
			timer.Reset(p.anim.delayAt(p.frame))

			// Clip to the window width: a long command otherwise wraps
			// over several lines while only the last one is erased.
			width := terminalWidth()
			if !unicodeCapable() {
				// A plain bar replaces braille in a simplified terminal.
				frame = fallbackFrame(p.frame)
			}
			// The frame width is taken out of the total, so the line does
			// not wrap because of the spinner itself.
			text = clipText(text, width-frameWidth(frame))

			// Dimmed, so the progress line does not compete with the
			// answer that follows it.
			if p.color {
				fmt.Fprintf(os.Stderr, "\r\033[K\033[2m%s %s\033[0m", frame, text)
				continue
			}
			fmt.Fprintf(os.Stderr, "\r\033[K%s %s", frame, text)
		}
	}
}

// Update refreshes the text from a daemon event.
func (p *progressLine) Update(f *ipc.Frame) {
	if f.Data == nil {
		return
	}

	raw, err := ipc.MarshalPayload(f.Data)
	if err != nil {
		return
	}
	var ev progressEvent
	if err := ipc.Unmarshal(raw, &ev); err != nil {
		return
	}

	var text string
	switch f.Topic {
	case "progress.queued":
		text = "waiting for turn..."
	case "progress.llm":
		text = fmt.Sprintf("asking model (step %d)", ev.Step)
	case "progress.tool":
		if ev.Summary != "" {
			text = ev.Summary
		} else {
			text = ev.Tool
		}
	case "progress.tool_done":
		text = ev.Tool + " done"
	case "progress.warn":
		text = ev.Message
	case "progress.thinking":
		text = ev.Text
	}
	if text == "" {
		return
	}

	p.mu.Lock()
	p.text = text
	p.mu.Unlock()
}

// Stop erases the line and halts drawing.
func (p *progressLine) Stop() {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return
	}
	p.active = false
	p.mu.Unlock()

	close(p.stop)
	<-p.done
	fmt.Fprint(os.Stderr, "\r\033[K")
}
