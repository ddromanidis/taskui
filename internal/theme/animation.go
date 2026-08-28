package theme

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Animation is the only moving part a theme is allowed.
//
// A terminal cannot move a row without moving everything under it, so nothing here animates
// vertical position. Three things it does animate, all of them on the cursor's own row:
//
//   - Frames cycle the two columns that frame the row through a sequence of half blocks, so
//     the lit edge climbs to the top of its cell, fills it, drops to the bottom and comes
//     back. Sub-row vertical motion, confined to a column narrow enough to spare it.
//   - Jiggle leans the row's own text a column or two right and back. Sideways is safe in a
//     way that up and down is not — the row keeps its line, so nothing under it moves.
//   - Blink drops the row's highlight for a frame or two. The bar goes out; the row, the
//     rail and the text all stay exactly where they were.
//
// All three are sequences read off one clock, one entry per frame, so the length of what a
// theme writes is the speed of what it gets. Lengths that share no factor never come back
// round together, which is the difference between something that moves and something that
// ticks.
//
// Off by default, and off in most of the themes that ship. A theme that animates costs a
// redraw every Interval for as long as taskui is open, which is cheap but is not nothing,
// and is not a decision to make on somebody else's behalf.
type Animation struct {
	// Frames are the glyphs the selection's edges cycle through, one column each.
	Frames []string
	// Jiggle is how far the selected row's text sits to the right, in columns, one entry
	// per frame.
	//
	// Its own sequence rather than a flag, and read off the same clock as Frames, so a theme
	// sets the speed of the jiggle by how long it writes it: `"01"` wobbles every frame,
	// `"0000111100001111"` takes sixteen to get through one lean and back. That is how you
	// ask for something slow without a second timer racing the first.
	Jiggle []int
	// Blink is whether the selected row's highlight is showing, one entry per frame.
	//
	// Ours rather than the terminal's. SGR 5 exists and every terminal blinks it at whatever
	// rate it likes, which is the one thing a theme here is not allowed to be vague about —
	// the whole animation block is built on the idea that the sequence's length is the speed,
	// and a blink nobody can time is not a setting.
	Blink []bool
	// Interval is how long each frame lasts.
	Interval time.Duration
}

// minInterval and maxInterval bound what a theme can ask for. Faster than the floor is a
// strobe rather than a bounce; slower than the ceiling stops reading as motion at all.
const (
	minInterval = 40 * time.Millisecond
	maxInterval = 2 * time.Second
)

// DefaultInterval is used when a theme gives frames but no timing.
//
// Slow enough to read as a bob rather than a flicker: four frames at this rate is a bounce
// a little over a second long, which is something you notice without it asking for
// attention while you are trying to read the row it is marking.
const DefaultInterval = 280 * time.Millisecond

// maxLean bounds how far the jiggle can push a row.
//
// Every column it leans is a column of that row's own text pushed past the right edge, so
// the amplitude is capped where a wobble stops being a wobble and starts being a margin.
const maxLean = 2

// Moves reports whether this theme has anything to animate.
func (a Animation) Moves() bool {
	return a.Interval > 0 && (len(a.Frames) > 1 || len(a.Jiggle) > 1 || len(a.Blink) > 1)
}

// Lit is whether the selected row's highlight is drawn on this frame. A theme that does not
// blink is lit on every one of them.
func (a Animation) Lit(phase int) bool {
	if len(a.Blink) < 2 || a.Interval == 0 {
		return true
	}
	return a.Blink[wrapPhase(phase, len(a.Blink))]
}

// Frame is the glyph for a phase, or fallback when the theme does not animate its edges.
func (a Animation) Frame(phase int, fallback string) string {
	if len(a.Frames) < 2 || a.Interval == 0 {
		return fallback
	}
	return a.Frames[wrapPhase(phase, len(a.Frames))]
}

// Lean is how far the selected row's text sits to the right on this frame, in columns.
func (a Animation) Lean(phase int) int {
	if len(a.Jiggle) < 2 || a.Interval == 0 {
		return 0
	}
	return a.Jiggle[wrapPhase(phase, len(a.Jiggle))]
}

// MaxLean is the furthest this theme ever leans, which is the room the layout has to keep
// free for it.
//
// Reserved for every row rather than taken from the one that happens to be moving: a row
// that got wider as it leaned would have to take the column from somewhere, and the only
// place to take it from is its own right edge — where the counts and the timestamps are. A
// theme that wobbles pays for it once, in layout, instead of every time the cursor goes past.
func (a Animation) MaxLean() int {
	if len(a.Jiggle) < 2 || a.Interval == 0 {
		return 0
	}
	most := 0
	for _, n := range a.Jiggle {
		most = max(most, n)
	}
	return most
}

// wrapPhase folds a phase into a sequence, negatives included — `--phase` takes whatever
// number you hand it.
func wrapPhase(phase, n int) int { return ((phase % n) + n) % n }

// applyAnimation reads an `animation:` block.
//
// Frames are given as one string rather than a list — `"▀█▄█"` — because that is what the
// sequence looks like, and a four-item YAML list of single characters reads as data entry
// rather than as a shape.
func applyAnimation(a *Animation, block map[string]string) []string {
	if len(block) == 0 {
		return nil
	}
	var bad []string
	used := map[string]bool{}

	if text, ok := block["selection-frames"]; ok {
		used["selection-frames"] = true
		frames, problem := parseFrames(text)
		if problem != "" {
			bad = append(bad, problem)
		} else {
			a.Frames = frames
			if a.Interval == 0 {
				a.Interval = DefaultInterval
			}
		}
	}

	if text, ok := block["selection-jiggle"]; ok {
		used["selection-jiggle"] = true
		lean, problem := parseJiggle(text)
		if problem != "" {
			bad = append(bad, problem)
		} else {
			a.Jiggle = lean
			if a.Interval == 0 {
				a.Interval = DefaultInterval
			}
		}
	}

	if text, ok := block["selection-blink"]; ok {
		used["selection-blink"] = true
		blink, problem := parseBlink(text)
		if problem != "" {
			bad = append(bad, problem)
		} else {
			a.Blink = blink
			if a.Interval == 0 {
				a.Interval = DefaultInterval
			}
		}
	}

	if text, ok := block["interval-ms"]; ok {
		used["interval-ms"] = true
		ms, err := strconv.Atoi(strings.TrimSpace(text))
		switch {
		case err != nil:
			bad = append(bad, fmt.Sprintf("animation: `interval-ms` is not a number: %q", text))
		case ms == 0:
			a.Interval = 0
		case time.Duration(ms)*time.Millisecond < minInterval:
			bad = append(
				bad,
				fmt.Sprintf("animation: `interval-ms` below %d is a strobe, not a bounce", minInterval.Milliseconds()),
			)
		case time.Duration(ms)*time.Millisecond > maxInterval:
			bad = append(
				bad,
				fmt.Sprintf("animation: `interval-ms` above %d stops reading as motion", maxInterval.Milliseconds()),
			)
		default:
			a.Interval = time.Duration(ms) * time.Millisecond
		}
	}

	var unknown []string
	for k := range block {
		if !used[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		bad = append(bad, fmt.Sprintf("animation: `%s` is not an animation setting", k))
	}
	return bad
}

func parseFrames(text string) ([]string, string) {
	if strings.TrimSpace(text) == "" {
		return nil, ""
	}
	var frames []string
	for _, r := range text {
		g := string(r)
		if lipgloss.Width(g) != 1 {
			return nil, fmt.Sprintf(
				"animation: every frame is one column, and %q is %d", g, lipgloss.Width(g))
		}
		frames = append(frames, g)
	}
	if len(frames) < 2 {
		return nil, "animation: `selection-frames` needs at least two frames to move between"
	}
	return frames, ""
}

// parseJiggle reads the lean as one digit per frame — `"0011"`.
//
// Digits rather than a list of numbers for the same reason the frames are one string: it is
// a shape, and you want to see the shape. Written out it is also the only honest way to say
// how long the wobble takes, because the length is the timing.
func parseJiggle(text string) ([]int, string) {
	if strings.TrimSpace(text) == "" {
		return nil, ""
	}
	lean := make([]int, 0, len(text))
	for _, r := range text {
		if r < '0' || r > '0'+maxLean {
			return nil, fmt.Sprintf(
				"animation: `selection-jiggle` is one digit 0-%d per frame, and %q is not", maxLean, string(r))
		}
		lean = append(lean, int(r-'0'))
	}
	if len(lean) < 2 {
		return nil, "animation: `selection-jiggle` needs at least two frames to move between"
	}
	return lean, ""
}

// ToYAML renders the animation as a block a theme file can be built from.
func (a Animation) ToYAML() string {
	var b strings.Builder
	b.WriteString("animation:\n")
	b.WriteString("  # The glyphs the cursor's two columns cycle through, one column each.\n")
	b.WriteString("  # Leave it out and nothing moves, which is what most of the shipped themes do.\n")
	fmt.Fprintf(&b, "  selection-frames: %q\n", strings.Join(a.Frames, ""))
	fmt.Fprintf(&b, "  # How far the selected row's text leans right, one digit 0-%d per frame.\n", maxLean)
	b.WriteString("  # The length is the speed: the longer the sequence, the slower the wobble.\n")
	fmt.Fprintf(&b, "  selection-jiggle: %q\n", jiggleText(a.Jiggle))
	b.WriteString("  # Whether the selected row's highlight is showing: 1 lit, 0 dark, one per frame.\n")
	b.WriteString("  # The rail keeps drawing through the dark frames — a cursor you cannot see is not\n")
	b.WriteString("  # a blink, it is a loss.\n")
	fmt.Fprintf(&b, "  selection-blink: %q\n", blinkText(a.Blink))
	b.WriteString("  # How long each frame lasts. 0 turns the animation off.\n")
	fmt.Fprintf(&b, "  interval-ms: %d\n", a.Interval.Milliseconds())
	return b.String()
}

// parseBlink reads the highlight's on/off as one digit per frame — `"11111100"`.
//
// Written the same way as the lean underneath it so the two can be read as a pair, and so
// the same rule applies: how long you write it is how slow it goes. Six frames lit and two
// dark at 260ms is a bar that goes out for half a second every two seconds.
func parseBlink(text string) ([]bool, string) {
	if strings.TrimSpace(text) == "" {
		return nil, ""
	}
	blink := make([]bool, 0, len(text))
	for _, r := range text {
		if r != '0' && r != '1' {
			return nil, fmt.Sprintf(
				"animation: `selection-blink` is 1 for lit and 0 for dark, and %q is neither", string(r))
		}
		blink = append(blink, r == '1')
	}
	if len(blink) < 2 {
		return nil, "animation: `selection-blink` needs at least two frames to blink between"
	}
	return blink, ""
}

// jiggleText writes the lean back out as digits.
//
// Clamped rather than trusted: Jiggle is an ordinary field on an ordinary struct, and the
// one promise a dump has to keep is that what it writes parses back.
func jiggleText(lean []int) string {
	// One digit per allowed lean, 0 through maxLean.
	const digits = "012"
	var b strings.Builder
	for _, n := range lean {
		b.WriteByte(digits[min(max(n, 0), maxLean)])
	}
	return b.String()
}

func blinkText(blink []bool) string {
	var b strings.Builder
	for _, lit := range blink {
		if lit {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}
