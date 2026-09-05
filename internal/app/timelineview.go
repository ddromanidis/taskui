package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/theme"
)

// --- timeline ---------------------------------------------------------------------

func (a *App) timelineHeader() line {
	t := a.Theme
	failed := 0
	for _, p := range a.Timeline {
		if !p.Ok() {
			failed++
		}
	}
	var state []span
	if n := len(a.TimelineFlakes); n > 0 {
		// Counted by commit, not by flake: with arguments in the key one commit can produce
		// several, and "3 commits" over a single revision would be a plain lie.
		commits := map[string]bool{}
		for _, f := range a.TimelineFlakes {
			commits[f.Commit] = true
		}
		where := a.TimelineFlakes[0].Short()
		if len(commits) > 1 {
			where = fmt.Sprintf("%d commits", len(commits))
		}
		state = append(state, styled("flaky at "+where+"   ", fg(t.Colors.Notice)))
	}
	state = append(state, styled(a.trend(), fg(t.Colors.Dim)))
	state = append(state, styled(
		fmt.Sprintf("   %d %s   %d failed", len(a.Timeline), plural(len(a.Timeline), "run", "runs"), failed),
		fg(t.Colors.Dim),
	))
	return a.header(a.TimelineOf, state)
}

// trend is the run of outcomes as one string, newest on the right.
//
// Reading left to right is reading forwards in time, which is the opposite order to the
// list underneath — but it is the order every other sparkline in the world is drawn in, and
// the shape of `✓✓✓✗✗` is the whole point: where it turned is more use than how many
// failures there were.
func (a *App) trend() string {
	const most = 20
	points := a.Timeline
	if len(points) > most {
		points = points[:most]
	}
	var b strings.Builder
	for _, p := range slices.Backward(points) {
		if p.Ok() {
			b.WriteString(a.Theme.Glyphs.StatusOk)
		} else {
			b.WriteString(a.Theme.Glyphs.StatusFailed)
		}
	}
	return b.String()
}

// timelineBar is how wide the duration bar may be, or zero where the width cannot spare it.
//
// The bar is the first thing to go: it is a nicety on top of a number that is already
// there, and a truncated command is a real loss.
func timelineBar(width int) int {
	switch {
	case width < 76:
		return 0
	case width < 96:
		return 8
	default:
		return 14
	}
}

func (a *App) drawTimeline(width, height int) []string {
	t := a.Theme
	if len(a.Timeline) == 0 {
		return nil
	}
	a.TimelineCursor = clamp(a.TimelineCursor, 0, len(a.Timeline)-1)
	a.TimelineOffset = scrollTo(a.TimelineOffset, a.TimelineCursor, len(a.Timeline), height)

	// Scaled to the slowest run in the list rather than to a fixed millisecond count: the
	// question is "which of these was slow", and that is a comparison within the list.
	var slowest int64
	for _, p := range a.Timeline {
		slowest = max(slowest, p.DurationMs)
	}
	barWidth := timelineBar(width)
	// The commit is the last thing to earn its column and the first to lose it.
	showCommit := width >= 104
	// Keyed by the whole question rather than by the commit: with arguments in the key, one
	// commit can hold a flaky `ENV=staging` and a perfectly steady `ENV=prod`, and marking
	// both would be the header telling on the wrong row.
	flaky := map[string]bool{}
	for _, f := range a.TimelineFlakes {
		flaky[f.Question()] = true
	}

	out := make([]string, 0, height)
	for i := a.TimelineOffset; i < len(a.Timeline) && len(out) < height; i++ {
		p := a.Timeline[i]
		glyph, colour := t.Glyphs.StatusOk, t.Colors.StatusOk
		if !p.Ok() {
			glyph, colour = t.Glyphs.StatusFailed, t.Colors.StatusFailed
		}

		l := line{
			styled(glyph+" ", fgBold(colour)),
			styled(padRight(ago(p.WhenUnix), 10), fg(t.Colors.Dim)),
			styled(fmt.Sprintf("%8s  ", duration(millis(p.DurationMs))), fg(t.Colors.Dim)),
		}
		if barWidth > 0 {
			l = append(l, styled(padRight(bar(p.DurationMs, slowest, barWidth, t.Glyphs.Bar), barWidth+2), fg(colour)))
		}
		l = append(l, styled(fmt.Sprintf("%6d lines  ", p.Lines), fg(t.Colors.Dim)))
		if showCommit && p.Commit != "" {
			// Dimmer than the command beside it: it is there to be compared with the rows
			// above and below, not read.
			style := fg(t.Colors.Faint)
			if flaky[p.Question()] {
				style = fg(t.Colors.Notice)
			}
			l = append(l, styled(padRight(shortCommit(p.Commit), 10), style))
		}
		l = append(l, styled(p.Command(), fg(theme.Default)))
		out = append(out, l.renderRow(width, i == a.TimelineCursor, t, a.Phase, 0, 1))
	}
	return out
}

// bar draws one duration against the slowest in the list.
//
// A run that took any measurable time gets at least one cell. A bar that rounds to nothing
// says "instant" where the number beside it says 40ms, and the two disagreeing is worse
// than the bar being slightly generous.
func bar(value, most int64, width int, glyph string) string {
	if most <= 0 || value <= 0 || width <= 0 {
		return ""
	}
	cells := int((value*int64(width) + most - 1) / most)
	return strings.Repeat(glyph, clamp(cells, 1, width))
}

// shortCommit abbreviates a revision, keeping the `-dirty` marker — a run of uncommitted
// work is not a run of the commit it sits on, and the row should not claim otherwise.
func shortCommit(commit string) string {
	base, dirty := strings.CutSuffix(commit, "-dirty")
	if len(base) > 7 {
		base = base[:7]
	}
	if dirty {
		return base + "*"
	}
	return base
}

func (a *App) timelineFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}
	return a.hintBar(&keys.TimelineSection)
}

// --- diff -------------------------------------------------------------------------

func (a *App) diffHeader() line {
	t := a.Theme
	state := []span{
		styled("vs "+a.DiffAgainstWhat, fg(t.Colors.Stored)),
		styled("   "+ago(a.DiffAgainst.WhenUnix), fg(t.Colors.Dim)),
	}
	if a.DiffStat.Added > 0 {
		state = append(state, styled(fmt.Sprintf("   +%d", a.DiffStat.Added), fg(t.Colors.DiffAdded)))
	}
	if a.DiffStat.Removed > 0 {
		state = append(state, styled(fmt.Sprintf("   -%d", a.DiffStat.Removed), fg(t.Colors.DiffRemoved)))
	}
	if a.DiffStat.Added == 0 && a.DiffStat.Removed == 0 {
		state = append(state, styled("   no change", fg(t.Colors.Dim)))
	}
	return a.header(a.DiffOf, state)
}

// diffGutter is the width of the two line-number columns, sized to the largest number in
// the diff so a short one does not pay for a long one's digits.
func (a *App) diffGutter() int {
	widest := 0
	for _, r := range a.DiffRows {
		widest = max(widest, r.Old, r.New)
	}
	width := 1
	for widest >= 10 {
		widest /= 10
		width++
	}
	return width
}

func (a *App) drawDiff(width, height int) []string {
	t := a.Theme
	if len(a.DiffRows) == 0 {
		return nil
	}
	a.DiffCursor = clamp(a.DiffCursor, 0, len(a.DiffRows)-1)
	a.DiffOffset = scrollTo(a.DiffOffset, a.DiffCursor, len(a.DiffRows), height)

	pad := a.diffGutter()
	// Two numbers, a space between them, the marker, and a space: the fixed left edge every
	// row shares.
	prefix := pad*2 + 3
	room := max(8, a.bodyWidth(width)-prefix-1)

	out := make([]string, 0, height)
	for i := a.DiffOffset; i < len(a.DiffRows) && len(out) < height; i++ {
		row := a.DiffRows[i]
		selected := i == a.DiffCursor

		if row.Gap {
			l := line{
				plain(strings.Repeat(" ", pad*2+1)),
				styled(t.Glyphs.DiffGap+" ", fg(t.Colors.Faint)),
			}
			out = append(out, l.renderRow(width, selected, t, a.Phase, 0, 1))
			continue
		}

		marker, style := " ", fg(theme.Default)
		switch row.Op {
		case diff.Ins:
			marker, style = t.Glyphs.DiffAdded, fg(t.Colors.DiffAdded)
		case diff.Del:
			marker, style = t.Glyphs.DiffRemoved, fg(t.Colors.DiffRemoved)
		case diff.Same:
			// Unchanged context stays the colour of ordinary output: it is there to place
			// the change, and colouring it would make it compete with one.
			style = fg(t.Colors.Dim)
		}

		l := line{
			styled(numberOrBlank(row.Old, pad)+" ", fg(t.Colors.Faint)),
			styled(numberOrBlank(row.New, pad), fg(t.Colors.Faint)),
			styled(marker+" ", style),
		}
		l = append(l, a.textWithLocations(clip(row.Text, room), style)...)
		out = append(out, l.renderRow(width, selected, t, a.Phase, 0, 1))
	}
	return out
}

// numberOrBlank right-aligns a line number, or leaves the column empty for a line that does
// not exist on that side. A `0` there would be a line number, and there is no line zero.
func numberOrBlank(n, width int) string {
	if n == 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, n)
}

// textWithLocations splits a line so that any `file:line` in it is drawn as a link.
//
// Underlined as well as recoloured: on a failure line that is already red, a second red is
// not a signal. The underline is what says "this one is reachable" — and `e` is the key
// that reaches it.
func (a *App) textWithLocations(text string, base lipgloss.Style) []span {
	found := locationsIn(text)
	if len(found) == 0 {
		return []span{styled(text, base)}
	}
	out := make([]span, 0, len(found)*2+1)
	at := 0
	for _, l := range found {
		if l.Start > len(text) || l.End > len(text) || l.Start < at {
			continue
		}
		out = append(out,
			styled(text[at:l.Start], base),
			underlined(text[l.Start:l.End], a.linkStyle(base)),
		)
		at = l.End
	}
	return append(out, styled(text[at:], base))
}

// linkStyle recolours a location. The underline that goes with it is emitted by the
// renderer — see the note on span.underline for why it is not set here.
func (a *App) linkStyle(base lipgloss.Style) lipgloss.Style {
	if c := a.Theme.Colors.Location; !c.IsDefault() {
		return base.Foreground(c.Lip())
	}
	return base
}

func (a *App) diffFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}
	return a.hintBar(&keys.DiffSection)
}

// --- profile ------------------------------------------------------------------------

func (a *App) profileHeader() line {
	t := a.Theme
	total := a.ProfileTotal()
	state := []span{styled(duration(total)+" total", fg(t.Colors.Dim))}
	if len(a.ProfileRows) > 0 {
		state = append(state, styled(
			fmt.Sprintf("   %d %s", len(a.ProfileRows), plural(len(a.ProfileRows), "task", "tasks")),
			fg(t.Colors.Dim),
		))
	}
	subject := "profile"
	if a.Run != nil {
		subject = a.Run.Command()
	}
	return a.header(subject, state)
}

func (a *App) drawProfile(width, height int) []string {
	t := a.Theme
	if len(a.ProfileRows) == 0 {
		return nil
	}
	a.ProfileCursor = clamp(a.ProfileCursor, 0, len(a.ProfileRows)-1)
	a.ProfileOffset = scrollTo(a.ProfileOffset, a.ProfileCursor, len(a.ProfileRows), height)

	// Against the slowest row rather than the run's total: on a run where one step takes
	// nine tenths of the time, bars drawn against the total leave every other row with no
	// bar at all, and the comparison between those rows is the one still worth making.
	slowest := a.ProfileRows[0].Self
	total := a.ProfileTotal()
	barWidth := timelineBar(width)

	out := make([]string, 0, height)
	for i := a.ProfileOffset; i < len(a.ProfileRows) && len(out) < height; i++ {
		c := a.ProfileRows[i]

		share := ""
		if total > 0 {
			share = fmt.Sprintf("%4.0f%%", 100*float64(c.Self)/float64(total))
		}

		l := line{
			styled(statusGlyph(c.Status, t)+" ", fgBold(statusStyle(c.Status, t))),
			styled(fmt.Sprintf("%8s ", duration(c.Self)), fg(t.Colors.Text)),
			styled(share+"  ", fg(t.Colors.Dim)),
		}
		if barWidth > 0 {
			l = append(l, styled(
				padRight(bar(int64(c.Self), int64(slowest), barWidth, t.Glyphs.Bar), barWidth+2),
				fg(statusStyle(c.Status, t)),
			))
		}
		l = append(l, styled(c.Name, fg(theme.Default)))

		// An aggregate's own time is nearly all its children's; saying so stops the row
		// reading as though `all` were somehow slow by itself.
		if c.Children > 0 {
			l = append(l, styled(fmt.Sprintf("   %s inc.", duration(c.Duration)), fg(t.Colors.Faint)))
		}
		out = append(out, l.renderRow(width, i == a.ProfileCursor, t, a.Phase, 0, 1))
	}
	return out
}

func (a *App) profileFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}
	return a.hintBar(&keys.ProfileSection)
}
