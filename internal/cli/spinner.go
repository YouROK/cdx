package cli

// Spinner animations for the progress line.
//
// The spinner is drawn in a four by four grid of dots. Two braille cells
// cover such a grid: each cell is a two by four grid, and together they
// form one figure without a gap in the middle.
//
// A frame is described by dot positions in the grid plus the pause after
// it. The pause provides inertia: motion speeds up, slows down and stops
// at a turn.

import (
	"math"
	"math/rand"
	"os"
	"strings"
	"time"
)

// Grid dimensions in dots.
const (
	fieldCols = 4
	fieldRows = 4
)

// pointBits maps a grid position to a braille cell bit.
//
// The first two columns land in the left cell, the rest in the right one.
// Within a cell the dots run top to bottom: 1-2-3 and 7 on the left,
// 4-5-6 and 8 on the right.
var pointBits = [fieldCols][fieldRows]rune{
	{0x01, 0x02, 0x04, 0x40},
	{0x08, 0x10, 0x20, 0x80},
}

// frame is a single animation frame.
type frame struct {
	cells []point
	// hold is the pause after the frame; zero means the usual duration.
	// A long pause reads as a stop, like inertia at the edge of motion.
	hold time.Duration
}

// point is a dot position in the grid.
type point struct {
	col int
	row int
}

// render builds a frame from braille characters.
func (f frame) render() string {
	var bits [2]rune
	for _, p := range f.cells {
		if p.col < 0 || p.col >= fieldCols || p.row < 0 || p.row >= fieldRows {
			continue
		}
		bits[p.col/2] |= pointBits[p.col%2][p.row]
	}
	return string([]rune{0x2800 + bits[0], 0x2800 + bits[1]})
}

// delay returns the pause after the frame.
func (f frame) delay(base time.Duration) time.Duration {
	if f.hold > 0 {
		return f.hold
	}
	return base
}

// animation is a named set of frames.
type animation struct {
	name   string
	frames []frame
	base   time.Duration
}

// delayAt returns the pause before the frame with the given number.
func (a animation) delayAt(step int) time.Duration {
	if len(a.frames) == 0 {
		return a.base
	}
	if step < 0 {
		step = 0
	}
	return a.frames[step%len(a.frames)].delay(a.base)
}

// textAt returns the frame with the given number.
func (a animation) textAt(step int) string {
	if len(a.frames) == 0 {
		return ""
	}
	if step < 0 {
		step = 0
	}
	return a.frames[step%len(a.frames)].render()
}

// Ring positions in clockwise order, starting on the left so that a dot
// enters the frame from the side the text is read from.
var ring = []point{
	{0, 1}, {1, 0}, {2, 0}, {3, 1},
	{3, 2}, {2, 3}, {1, 3}, {0, 2},
}

// ease gives soft acceleration and braking.
//
// Without it a dot moves uniformly and jumps between positions instead of
// gliding.
func ease(p float64) float64 {
	return 0.5 - 0.5*math.Cos(math.Pi*p)
}

// spiral returns the circular animation.
//
// Three dots set off in turn: the next one starts when the previous has
// covered a quarter of the circle, which reads as a stream rather than a
// single running dot. Frames are sampled at a fixed interval but only the
// changed ones are shown, and a frame's pause is the time until the next
// change. Showing every sample with a fixed pause would make the motion
// twice as fast as intended.
func spiral() animation {
	const (
		dots = 3
		// dur is the time of one full circle for a single dot.
		dur = 800 * time.Millisecond
		// lag is the delay of the next dot.
		lag = dur / 4
		// step is the sampling interval, finer than the output.
		step = 33 * time.Millisecond
	)

	total := dur + time.Duration(dots-1)*lag

	// Moments when the frame changes, with the frame itself.
	type moment struct {
		at time.Duration
		f  frame
	}
	var moments []moment
	last := ""
	for t := time.Duration(0); t <= total+step; t += step {
		var cells []point
		for i := 0; i < dots; i++ {
			p := float64(t-time.Duration(i)*lag) / float64(dur)
			p = math.Max(0, math.Min(1, p))
			idx := int(math.Round(ease(p)*float64(len(ring)))) % len(ring)
			cells = append(cells, ring[idx])
		}

		f := frame{cells: dedupe(cells)}
		if text := f.render(); text != last {
			last = text
			moments = append(moments, moment{at: t, f: f})
		}
	}

	// A frame's pause is the time until the next change, which is what
	// gives the motion its inertia.
	frames := make([]frame, 0, len(moments))
	for i := range moments {
		f := moments[i].f
		if i+1 < len(moments) {
			f.hold = moments[i+1].at - moments[i].at
		}
		frames = append(frames, f)
	}

	return animation{name: "spiral", frames: frames, base: step}
}

// snakePath is the route of the snake across the grid.
//
// A climb along the left edge, then row by row from top to bottom: right,
// left, right and left. The result is a zigzag without gaps.
var snakePath = []point{
	{0, 3}, {0, 2}, {0, 1}, {0, 0},
	{1, 0}, {2, 0}, {3, 0},
	{3, 1}, {2, 1}, {1, 1}, {0, 1},
	{0, 2}, {1, 2}, {2, 2}, {3, 2},
	{3, 3}, {2, 3}, {1, 3}, {0, 3},
}

// snake returns the snake animation.
//
// The head follows the route and the tail trails behind it. The climb is
// slower: the square gains height with effort and stops at the top.
func snake() animation {
	const (
		// length is how many dots the tail holds.
		length = 4
		base   = 60 * time.Millisecond
	)

	// Climb pauses: acceleration, deceleration and a stop at the top.
	up := []time.Duration{
		70 * time.Millisecond,
		100 * time.Millisecond,
		150 * time.Millisecond,
		420 * time.Millisecond,
	}

	var frames []frame
	for head := 0; head < len(snakePath)+length-1; head++ {
		var cells []point
		for k := 0; k < length; k++ {
			if i := head - k; i >= 0 && i < len(snakePath) {
				cells = append(cells, snakePath[i])
			}
		}

		f := frame{cells: dedupe(cells)}
		if head < len(up) {
			f.hold = up[head]
		} else {
			// The descent speeds up and fades out towards the end.
			d := 260 * math.Pow(0.8, float64(head-len(up)))
			if d < 35 {
				d = 35
			}
			f.hold = time.Duration(d) * time.Millisecond
		}
		frames = append(frames, f)
	}

	return animation{name: "snake", frames: frames, base: base}
}

// animations lists every available animation in display order.
var animations = []animation{
	spiral(),
	snake(),
}

// activeAnimation is the animation in use.
//
// Picking a fresh one on every request keeps the output alive. The
// CDX_SPINNER variable pins a single one: "spiral", "snake" or "random".
var activeAnimation = pickAnimation(os.Getenv("CDX_SPINNER"))

// pickAnimation selects an animation by name.
//
// "random" and an empty name both give a different one per request, since
// the same picture wears thin in daily use.
func pickAnimation(name string) animation {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "spiral":
		return animations[0]
	case "snake":
		return animations[1]
	case "none", "off":
		return animations[0]
	default:
		return animations[rand.Intn(len(animations))]
	}
}

// dedupe drops repeated positions.
//
// Ring positions can coincide when two dots run next to each other: one
// dot per frame, not two stacked on top of each other.
func dedupe(cells []point) []point {
	if len(cells) < 2 {
		return cells
	}
	seen := make(map[point]bool, len(cells))
	out := make([]point, 0, len(cells))
	for _, p := range cells {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// fallbackFrames are the frames for terminals without unicode.
var fallbackFrames = []string{"|", "/", "-", "\\"}

// fallbackFrame returns a frame of the simplified spinner.
func fallbackFrame(step int) string {
	if step < 0 {
		step = 0
	}
	return fallbackFrames[step%len(fallbackFrames)]
}

// frameWidth returns the width of a frame in character cells, including
// the space between the frame and the text.
func frameWidth(frame string) int {
	return len([]rune(frame)) + 1
}

// unicodeCapable reports whether braille characters can be printed.
//
// The check covers the terminal and the locale: without UTF-8 the dots
// would show up as garbage.
func unicodeCapable() bool {
	if os.Getenv("TERM") == "linux" || os.Getenv("TERM") == "dumb" {
		return false
	}
	lang := os.Getenv("LC_ALL") + os.Getenv("LC_CTYPE") + os.Getenv("LANG")
	if lang == "" {
		return true
	}
	lower := strings.ToLower(lang)
	return strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8")
}
