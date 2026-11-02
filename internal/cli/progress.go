package cli

import (
	"fmt"
	"os"
	"sync"
	"time"
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

			timer.Reset(p.anim.delayAt(p.frame))

			width := terminalWidth()
			if !unicodeCapable() {
				frame = fallbackFrame(p.frame)
			}
			text = clipText(text, width-frameWidth(frame))

			if p.color {
				fmt.Fprintf(os.Stderr, "\r\033[K\033[2m%s %s\033[0m", frame, text)
				continue
			}
			fmt.Fprintf(os.Stderr, "\r\033[K%s %s", frame, text)
		}
	}
}

func (p *progressLine) SetText(text string) {
	if text == "" {
		return
	}
	p.mu.Lock()
	p.text = text
	p.mu.Unlock()
}

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
