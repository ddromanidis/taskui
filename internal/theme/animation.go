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
// A terminal cannot move a row without moving everything under it, so nothing here
// animates position. What it animates is the cursor's own two columns: given a sequence of
// half blocks, the lit edge climbs to the top of its cell, fills it, drops to the bottom
// and comes back — which is a mark travelling up and down beside whatever you have
// selected. The row does not move. The thing marking it does.
//
// Off by default, and off in three of the four themes that ship. A theme that animates
// costs a redraw every Interval for as long as taskui is open, which is cheap but is not
// nothing, and is not a decision to make on somebody else's behalf.
type Animation struct {
	// Frames are the glyphs the selection's edges cycle through, one column each.
	Frames []string
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

// Moves reports whether this theme has anything to animate.
func (a Animation) Moves() bool { return len(a.Frames) > 1 && a.Interval > 0 }

// Frame is the glyph for a phase, or fallback when the theme does not animate.
func (a Animation) Frame(phase int, fallback string) string {
	if !a.Moves() {
		return fallback
	}
	n := len(a.Frames)
	return a.Frames[((phase%n)+n)%n]
}

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

// ToYAML renders the animation as a block a theme file can be built from.
func (a Animation) ToYAML() string {
	var b strings.Builder
	b.WriteString("animation:\n")
	b.WriteString("  # The glyphs the cursor's two columns cycle through, one column each.\n")
	b.WriteString("  # Leave it out and nothing moves, which is what three of the four shipped themes do.\n")
	fmt.Fprintf(&b, "  selection-frames: %q\n", strings.Join(a.Frames, ""))
	b.WriteString("  # How long each frame lasts. 0 turns the animation off.\n")
	fmt.Fprintf(&b, "  interval-ms: %d\n", a.Interval.Milliseconds())
	return b.String()
}
