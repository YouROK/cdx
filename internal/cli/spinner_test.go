package cli

import (
	"testing"
	"time"
)

// TestAnimationsPresent checks that the animations are built.
func TestAnimationsPresent(t *testing.T) {
	if len(animations) < 2 {
		t.Fatalf("at least two animations are expected, got %d", len(animations))
	}
	for _, a := range animations {
		if a.name == "" {
			t.Fatal("animation must have a name")
		}
		if len(a.frames) == 0 {
			t.Fatalf("%s: animation must have frames", a.name)
		}
		if a.base <= 0 {
			t.Fatalf("%s: base delay must be positive", a.name)
		}
	}
}

// TestFramesSameWidth checks that frames share one width.
//
// Unequal widths would make the line jitter on every frame change.
func TestFramesSameWidth(t *testing.T) {
	for _, a := range animations {
		want := len([]rune(a.textAt(0)))
		for i := 0; i < len(a.frames); i++ {
			if got := len([]rune(a.textAt(i))); got != want {
				t.Fatalf("%s: frame %d has width %d, want %d", a.name, i, got, want)
			}
		}
	}
}

// TestFramesNotEmpty checks that a frame is not empty.
func TestFramesNotEmpty(t *testing.T) {
	for _, a := range animations {
		for i := 0; i < len(a.frames); i++ {
			if a.textAt(i) == "" {
				t.Fatalf("%s: frame %d is empty", a.name, i)
			}
		}
	}
}

// TestCellsInsideField checks that dots stay inside the grid.
//
// A dot outside the grid would be lost on render, leaving the frame
// emptier than intended.
func TestCellsInsideField(t *testing.T) {
	for _, a := range animations {
		for i, f := range a.frames {
			for _, p := range f.cells {
				if p.col < 0 || p.col >= fieldCols || p.row < 0 || p.row >= fieldRows {
					t.Fatalf("%s: frame %d has a point outside the field: %+v", a.name, i, p)
				}
			}
		}
	}
}

// TestNoDuplicateCells checks that a frame has no repeats.
func TestNoDuplicateCells(t *testing.T) {
	for _, a := range animations {
		for i, f := range a.frames {
			seen := map[point]bool{}
			for _, p := range f.cells {
				if seen[p] {
					t.Fatalf("%s: frame %d repeats point %+v", a.name, i, p)
				}
				seen[p] = true
			}
		}
	}
}

// TestNoRepeatedFrames checks that consecutive frames differ.
//
// A repeat in a row reads as a stalled animation.
func TestNoRepeatedFrames(t *testing.T) {
	for _, a := range animations {
		for i := 1; i < len(a.frames); i++ {
			if a.textAt(i) == a.textAt(i-1) {
				t.Fatalf("%s: frame %d repeats the previous one: %q", a.name, i, a.textAt(i))
			}
		}
	}
}

// TestSnakeHasInertia checks that the snake pauses at the top.
//
// Without slowing down the motion looks mechanical.
func TestSnakeHasInertia(t *testing.T) {
	var snakeAnim animation
	for _, a := range animations {
		if a.name == "snake" {
			snakeAnim = a
		}
	}
	if snakeAnim.name == "" {
		t.Fatal("snake animation is missing")
	}

	longest := time.Duration(0)
	for _, f := range snakeAnim.frames {
		if f.hold > longest {
			longest = f.hold
		}
	}
	if longest <= snakeAnim.base {
		t.Fatalf("snake must slow down somewhere, longest hold %v, base %v",
			longest, snakeAnim.base)
	}
}

// TestSpiralCoversRing checks that the whole ring is covered.
func TestSpiralCoversRing(t *testing.T) {
	visited := map[point]bool{}
	for _, p := range ring {
		visited[p] = true
	}
	if len(visited) != 8 {
		t.Fatalf("the ring must have eight distinct points, got %d", len(visited))
	}

	// Every ring point appears in at least one frame.
	for _, p := range ring {
		found := false
		for _, a := range animations {
			if a.name != "spiral" {
				continue
			}
			for _, f := range a.frames {
				for _, c := range f.cells {
					if c == p {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("ring point %+v never appears in the spiral", p)
		}
	}
}

// TestRenderEmptyField checks an empty frame.
func TestRenderEmptyField(t *testing.T) {
	got := frame{}.render()
	if got != "⠀⠀" {
		t.Fatalf("empty frame must be blank braille, got %q", got)
	}
}

// TestRenderSinglePoint checks one dot in each corner.
func TestRenderSinglePoint(t *testing.T) {
	cases := []struct {
		p    point
		want string
	}{
		// Left cell: its top left dot.
		{point{0, 0}, "⠁⠀"},
		// Right cell: its top left dot.
		{point{2, 0}, "⠀⠁"},
		// Bottom dot of the left cell.
		{point{0, 3}, "⡀⠀"},
		// Bottom dot of the right cell.
		{point{3, 3}, "⠀⢀"},
	}
	for _, c := range cases {
		if got := (frame{cells: []point{c.p}}).render(); got != c.want {
			t.Fatalf("point %+v rendered as %q, want %q", c.p, got, c.want)
		}
	}
}

// TestPickAnimation checks selecting an animation by name.
func TestPickAnimation(t *testing.T) {
	if got := pickAnimation("snake"); got.name != "snake" {
		t.Fatalf("snake must be selected, got %q", got.name)
	}
	if got := pickAnimation("spiral"); got.name != "spiral" {
		t.Fatalf("spiral must be selected, got %q", got.name)
	}
	// An empty name and "random" both pick a random animation.
	for _, name := range []string{"", "random"} {
		got := pickAnimation(name)
		if got.name != "spiral" && got.name != "snake" {
			t.Fatalf("%q gave an unknown animation %q", name, got.name)
		}
	}
}

// TestFallbackFrame checks the simplified spinner.
func TestFallbackFrame(t *testing.T) {
	for step := 0; step < len(fallbackFrames)*2; step++ {
		want := fallbackFrames[step%len(fallbackFrames)]
		if got := fallbackFrame(step); got != want {
			t.Fatalf("fallbackFrame(%d) = %q, want %q", step, got, want)
		}
	}
	fallbackFrame(-1)
}

// TestFrameWidth checks the frame width including the space.
func TestFrameWidth(t *testing.T) {
	if got := frameWidth("⠁⠀"); got != 3 {
		t.Fatalf("expected width 3, got %d", got)
	}
	if got := frameWidth("|"); got != 2 {
		t.Fatalf("expected width 2, got %d", got)
	}
}

// TestDelayAt checks the pause selection by step.
func TestDelayAt(t *testing.T) {
	a := animation{
		frames: []frame{{}, {hold: 300 * time.Millisecond}},
		base:   80 * time.Millisecond,
	}
	if got := a.delayAt(0); got != 80*time.Millisecond {
		t.Fatalf("frame without hold must use the base delay, got %v", got)
	}
	if got := a.delayAt(1); got != 300*time.Millisecond {
		t.Fatalf("frame with hold must use it, got %v", got)
	}
	// A step beyond the set wraps around.
	if got := a.delayAt(3); got != 300*time.Millisecond {
		t.Fatalf("delay must wrap around, got %v", got)
	}
}

// referenceSnake holds the snake frames of the reference animation.
//
// The list lives in the test: if the order or the layout of the frames
// changes, the animation no longer matches the tuned motion.
var referenceSnake = []string{
	"⡀⠀", "⡄⠀", "⡆⠀", "⡇⠀", "⠏⠀", "⠋⠁", "⠉⠉", "⠈⠙", "⠀⠛", "⠐⠚",
	"⠒⠒", "⠖⠂", "⠶⠀", "⠦⠄", "⠤⠤", "⠠⢤", "⠀⣤", "⢀⣠", "⣀⣀", "⣀⡀",
	"⣀⠀", "⡀⠀",
}

// TestSnakeMatchesReference checks the snake frames against the reference.
func TestSnakeMatchesReference(t *testing.T) {
	var snakeAnim animation
	for _, a := range animations {
		if a.name == "snake" {
			snakeAnim = a
		}
	}
	if snakeAnim.name == "" {
		t.Fatal("snake animation is missing")
	}

	if len(snakeAnim.frames) != len(referenceSnake) {
		t.Fatalf("expected %d frames, got %d", len(referenceSnake), len(snakeAnim.frames))
	}
	for i := range referenceSnake {
		if got := snakeAnim.textAt(i); got != referenceSnake[i] {
			t.Fatalf("frame %d: got %q, want %q", i+1, got, referenceSnake[i])
		}
	}
}

// referenceSpiralDelays holds the spiral pauses of the reference animation.
//
// Their sum is the duration of the circle: squeezing the pauses makes the
// motion jerky and too fast.
var referenceSpiralDelays = []int64{132, 99, 99, 66, 33, 33, 66, 33, 33, 66, 33, 33, 66, 66, 33, 33, 66, 99}

// TestSpiralMatchesReferenceTiming checks the spiral pauses against the reference.
func TestSpiralMatchesReferenceTiming(t *testing.T) {
	var spiralAnim animation
	for _, a := range animations {
		if a.name == "spiral" {
			spiralAnim = a
		}
	}
	if spiralAnim.name == "" {
		t.Fatal("spiral animation is missing")
	}

	if len(spiralAnim.frames) != len(referenceSpiralDelays)+1 {
		t.Fatalf("expected %d frames, got %d", len(referenceSpiralDelays)+1, len(spiralAnim.frames))
	}

	var total int64
	for i, want := range referenceSpiralDelays {
		got := spiralAnim.delayAt(i).Milliseconds()
		if got != want {
			t.Fatalf("frame %d: delay %d ms, want %d ms", i+1, got, want)
		}
		total += got
	}

	// The circle must take about a second, since much faster reads as jerky.
	if total < 900 || total > 1300 {
		t.Fatalf("the circle must take about a second, got %d ms", total)
	}
}
