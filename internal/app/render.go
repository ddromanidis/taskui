package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/ddromanidis/taskui/internal/theme"
)

// span is a run of text with one style; line is a row built out of them. The pair stands
// in for ratatui's Span/Line, so the rendering code reads the way the original did while
// still producing the single string Bubble Tea's View wants.
type span struct {
	text  string
	style lipgloss.Style
}

type line []span

func plain(text string) span { return span{text: text} }

func styled(text string, st lipgloss.Style) span { return span{text: text, style: st} }

// fg is the common case: colour the text, unless the theme says "leave it alone".
func fg(c theme.Color) lipgloss.Style {
	if c.IsDefault() {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(c.Lip())
}

func bold() lipgloss.Style { return lipgloss.NewStyle().Bold(true) }

func fgBold(c theme.Color) lipgloss.Style { return fg(c).Bold(true) }

func onBg(fgc, bgc theme.Color) lipgloss.Style {
	st := lipgloss.NewStyle().Bold(true)
	if !fgc.IsDefault() {
		st = st.Foreground(fgc.Lip())
	}
	if !bgc.IsDefault() {
		st = st.Background(bgc.Lip())
	}
	return st
}

// selectionOf is the highlighted-row style.
//
// Reverse video by default rather than a fixed background colour: a dark slate looks
// deliberate on the theme it was picked against and is invisible on any other. Reversing
// whatever the terminal is already using cannot be invisible. Setting `selection` to a
// real colour opts back into a background.
func selectionOf(st lipgloss.Style, sel theme.Color) lipgloss.Style {
	if sel.IsDefault() {
		return st.Reverse(true)
	}
	return st.Background(sel.Lip()).Bold(true)
}

// The rail is the glyph in the cursor's own column: an accent edge down the left of the
// selected row.
//
// It exists because reverse video alone is a poor cursor. On a light terminal it turns the
// row into a black bar that reads heavier than anything else on screen, and on a dark one
// it competes with every status glyph. A one-column edge says "here" without shouting, and
// it survives a terminal whose reverse video is unhelpful — which is the same argument the
// selection colour already makes for itself, one column further left.
//
// The column on the other side is its shade.
//
// A lit edge on one side and a darker one on the other is how a flat surface is made to
// read as a raised one — it is not a cast shadow, it is the two faces of something with
// thickness. Themes that do not want it set the shade glyph to a space and the column goes
// back to being the right margin the layout already left there.
//
// renderRow draws a row framed by those two columns. Neither is selection-styled: they are
// the frame, not the thing framed.
func (l line) renderRow(width int, selected bool, t theme.Theme, phase int) string {
	if width <= 0 {
		return ""
	}
	left, leftStyle := " ", lipgloss.NewStyle()
	right, rightStyle := " ", lipgloss.NewStyle()
	if selected {
		// Both edges take the same frame, so the marker travels up and down as one rather
		// than tilting. The row itself never moves — a terminal cannot move one row without
		// moving everything under it, and a list that shifted while you read it would be a
		// worse trade than any amount of charm.
		left = t.Animation.Frame(phase, t.Glyphs.Rail)
		right = t.Animation.Frame(phase, t.Glyphs.SelectionShade)
		leftStyle, rightStyle = fgBold(t.Colors.SelectionLight), fg(t.Colors.SelectionShade)
	}
	return leftStyle.Render(left) +
		l.render(width-frameWidth, selected, t.Colors.Selection) +
		rightStyle.Render(right)
}

// render turns one line into a string of exactly width cells.
func (l line) render(width int, selected bool, sel theme.Color) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, s := range l {
		if used >= width {
			break
		}
		text := s.text
		runes := []rune(text)
		if used+len(runes) > width {
			runes = runes[:width-used]
			text = string(runes)
		}
		if text == "" {
			continue
		}
		st := s.style
		if selected {
			st = selectionOf(st, sel)
		}
		b.WriteString(st.Render(text))
		used += len(runes)
	}
	if used < width {
		pad := strings.Repeat(" ", width-used)
		if selected {
			b.WriteString(selectionOf(lipgloss.NewStyle(), sel).Render(pad))
		} else {
			b.WriteString(pad)
		}
	}
	return b.String()
}

// wrap breaks text to fit width, preferring word boundaries but hard-splitting anything
// that cannot fit — file paths and long type signatures routinely exceed a whole line, and
// leaving them to overflow is how the end of an error message goes missing.
func wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var out []string
	var line []rune

	for _, word := range splitInclusive(text, ' ') {
		w := []rune(word)
		if len(line)+len(w) > width && len(line) > 0 {
			out = append(out, string(line))
			line = line[:0]
		}
		if len(w) > width {
			// A single token longer than the line: hard-split it.
			for _, c := range w {
				if len(line) == width {
					out = append(out, string(line))
					line = line[:0]
				}
				line = append(line, c)
			}
		} else {
			line = append(line, w...)
		}
	}
	if len(line) > 0 || len(out) == 0 {
		out = append(out, string(line))
	}
	return out
}

// splitInclusive splits on sep, keeping the separator on the end of each piece — Rust's
// `split_inclusive`, which is what makes wrap keep its spaces.
func splitInclusive(s string, sep byte) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == sep {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// clip cuts a line down to one row, marking that it was cut.
//
// The counterpart to wrap, and the reason a peek window can promise a number of lines:
// five lines has to mean five lines, and one 300-character stack frame wrapped into nine
// rows would mean showing one of them. Counts characters rather than display columns,
// exactly as wrap does, so the two agree about where the edge is.
func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width-1]) + "…"
}

// columnBounds says where each column starts, given a first visible row.
//
// Rows have variable height once wrapped, so columns are filled by accumulating heights
// rather than by dividing the count.
func columnBounds(heights []int, offset, height, columns int) [][2]int {
	var bounds [][2]int
	at := offset
	for range columns {
		start := at
		used := 0
		for at < len(heights) && used+heights[at] <= height {
			used += heights[at]
			at++
		}
		// A single row taller than the column still has to go somewhere.
		if at == start && at < len(heights) {
			at++
		}
		bounds = append(bounds, [2]int{start, at})
		if at >= len(heights) {
			break
		}
	}
	return bounds
}

// offsetForCursor is the first row to show, given where the cursor is.
//
// The cursor is kept vertically centred in the left column rather than being allowed to
// walk to the bottom edge: you read around the thing you are on, and having context on
// both sides beats having it all above. The columns to the right are a look-ahead, so
// scrolling moves everything and your place stays where you are looking.
//
// Clamped at both ends, so a short list never scrolls and the last row can still reach the
// bottom instead of leaving a screenful of blank below it.
func offsetForCursor(heights []int, cursor, height, columns int) int {
	if len(heights) == 0 {
		return 0
	}
	cursor = min(cursor, len(heights)-1)

	// Walk back from the cursor until half a column is used up.
	half := height / 2
	used := 0
	centred := cursor
	for centred > 0 {
		h := heights[centred-1]
		if used+h > half {
			break
		}
		used += h
		centred--
	}

	// …but never past the point where everything left fits on screen.
	//
	// Filled backwards, one column at a time, rather than dividing by height*columns: a
	// column cannot split a row, so three-row items in a four-row column hold one each and
	// waste the rest. Assuming the arithmetic capacity put the offset a row too high and
	// dropped the cursor off the end entirely.
	at := len(heights)
	for range columns {
		before := at
		used := 0
		for at > 0 && used+heights[at-1] <= height {
			used += heights[at-1]
			at--
		}
		// A row taller than a whole column still occupies one.
		if at == before && at > 0 {
			at--
		}
		if at == 0 {
			break
		}
	}
	offset := min(centred, at)

	// Packing backwards is not always the mirror of packing forwards, so confirm against
	// the real layout rather than trusting the estimate.
	for offset < cursor && !within(columnBounds(heights, offset, height, columns), cursor) {
		offset++
	}
	return offset
}

func within(bounds [][2]int, cursor int) bool {
	for _, b := range bounds {
		if cursor >= b[0] && cursor < b[1] {
			return true
		}
	}
	return false
}

// duration renders a span at a glance. `0.00s` for a task that took four milliseconds
// reads as no information, and `134.20s` makes you do arithmetic to see it is over two
// minutes.
func duration(d time.Duration) string {
	secs := d.Seconds()
	switch {
	case secs < 1.0:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case secs < 60.0:
		return fmt.Sprintf("%.1fs", secs)
	default:
		total := int(d.Seconds())
		return fmt.Sprintf("%dm%02ds", total/60, total%60)
	}
}

// ago is whole minutes and hours, not a timestamp: what you want from a run list is how
// long ago, and the exact clock time almost never matters.
func ago(startedUnix int64) string {
	now := time.Now().Unix()
	if startedUnix > now {
		return "just now"
	}
	secs := now - startedUnix
	switch {
	case secs < 60:
		return "just now"
	case secs < 3600:
		return fmt.Sprintf("%dm ago", secs/60)
	case secs < 86_400:
		return fmt.Sprintf("%dh ago", secs/3600)
	default:
		return fmt.Sprintf("%dd ago", secs/86_400)
	}
}
