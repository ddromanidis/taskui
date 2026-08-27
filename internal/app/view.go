package app

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/theme"
)

// gutter is the width of the line-number column in the run view.
const gutter = 5

// minColumn is the narrowest picker column worth splitting into: a task name plus enough
// description to be worth reading. Below this, one wide column beats two cramped ones.
const minColumn = 46

// minRunColumn is the same for output, which needs more width than a task name does, so it
// splits later than the picker.
const minRunColumn = 60

// View renders one frame. Rendering to a string rather than into a cell buffer is what
// makes `--screenshot` and the render tests the same code path as the live UI.
func (a *App) View() string {
	width, height := a.Width, a.Height
	if width <= 0 || height <= 0 {
		return ""
	}

	// The slot bar costs a line, so it only appears once there is something to switch
	// between. One run open is the common case and should look exactly as it always did.
	slotBar := a.Screen == ScreenRun && len(a.Slots()) > 1

	headerH, footerH, slotsH := 0, 0, 0
	left := height
	if left > 0 {
		headerH = 1
		left--
	}
	if left > 0 {
		footerH = 1
		left--
	}
	if slotBar && left > 0 {
		slotsH = 1
		left--
	}
	bodyH := left
	a.Viewport = bodyH

	out := make([]string, 0, height)
	blank := strings.Repeat(" ", width)

	var header, footer line
	var body []string

	switch a.Screen {
	case ScreenPicker:
		header = a.pickerHeader()
		body = a.drawTree(width, bodyH)
		footer = a.pickerFooter()
	case ScreenRun:
		header = a.runHeader()
		body = a.drawRun(width, bodyH)
		footer = a.runFooter()
	case ScreenHistory:
		header = a.historyHeader()
		body = a.drawHistory(width, bodyH)
		footer = a.historyFooter()
	case ScreenHelp:
		header = a.helpHeader()
		body = a.drawHelp(width, bodyH)
		footer = a.helpFooter()
	case ScreenDetail:
		header = a.detailHeader()
		body = a.drawDetail(width, bodyH)
		footer = a.detailFooter()
	}

	if headerH == 1 {
		out = append(out, header.render(width, false, a.Theme.Selection))
	}
	if slotsH == 1 {
		out = append(out, a.drawSlotBar().render(width, false, a.Theme.Selection))
	}
	for i := range bodyH {
		if i < len(body) {
			out = append(out, body[i])
		} else {
			out = append(out, blank)
		}
	}
	if footerH == 1 {
		out = append(out, footer.render(width, false, a.Theme.Selection))
	}

	return strings.Join(out, "\n")
}

// RenderHeadless renders one frame to plain text off-screen. It backs both the render
// tests and `--screenshot`, which is how you look at the real thing without a terminal.
func (a *App) RenderHeadless(w, h int) []string {
	a.Width, a.Height = w, h
	frame := a.View()
	rows := strings.Split(frame, "\n")
	out := make([]string, 0, h)
	for i := range h {
		text := ""
		if i < len(rows) {
			text = strings.TrimRight(ansi.Strip(rows[i]), " ")
		}
		out = append(out, text)
	}
	return out
}

// --- shared pieces ----------------------------------------------------------------

func (a *App) wordmark() []span {
	return []span{
		styled("taskui", fgBold(a.Theme.Accent)),
		styled(" · ", fg(a.Theme.Dim)),
	}
}

func statusStyle(status run.Status, t theme.Theme) theme.Color {
	switch status {
	case run.Pending:
		return t.StatusPending
	case run.Running:
		return t.StatusRunning
	case run.Ok:
		return t.StatusOk
	case run.Failed:
		return t.StatusFailed
	default:
		return t.StatusSkipped
	}
}

// scrollPane clamps an offset and cuts a paragraph down to the visible rows.
func scrollPane(lines []line, width, height int, offset *int) []string {
	overflow := max(0, len(lines)-height)
	*offset = min(*offset, overflow)
	out := make([]string, 0, height)
	for i := *offset; i < len(lines) && len(out) < height; i++ {
		out = append(out, lines[i].render(width, false, theme.Default))
	}
	return out
}

// --- detail -----------------------------------------------------------------------

func (a *App) detailHeader() line {
	t := a.Theme
	l := append(line{}, a.wordmark()...)
	l = append(l, styled(a.DetailOf, bold()))
	if o, ok := a.Outcomes[a.DetailOf]; ok {
		glyph, colour := "   ✗ ", t.StatusFailed
		if o.Ok {
			glyph, colour = "   ✓ ", t.StatusOk
		}
		l = append(l, styled(glyph, fgBold(colour)), styled(ago(o.WhenUnix), fg(t.Dim)))
	}
	return l
}

func (a *App) drawDetail(width, height int) []string {
	t := a.Theme
	room := max(0, width-4)
	d := a.Detail
	var lines []line

	section := func(title string) {
		if len(lines) > 0 {
			lines = append(lines, line{})
		}
		lines = append(lines, line{styled(title, fgBold(t.Accent))})
	}

	for _, para := range d.Summary {
		for _, chunk := range wrap(para, room) {
			lines = append(lines, line{styled("  "+chunk, fg(t.Text))})
		}
	}

	if len(d.Requires) > 0 {
		section("requires")
		for _, v := range d.Requires {
			lines = append(lines, line{
				plain("  "),
				styled(v+"=", fgBold(t.Mode)),
				styled("   must be supplied with `a`", fg(t.Dim)),
			})
		}
	}

	if len(d.Dependencies) > 0 {
		section("runs first")
		for _, dep := range d.Dependencies {
			lines = append(lines, line{styled("  "+dep, fg(t.Alias))})
		}
	}

	if len(d.Commands) > 0 {
		section("will run")
		for _, cmd := range d.Commands {
			// Another task, or a shell line — worth telling apart at a glance.
			style, text := fg(t.Text), "  "+cmd
			if name, ok := strings.CutPrefix(cmd, "Task: "); ok {
				style, text = fg(t.Alias), "  → "+name
			}
			for _, chunk := range wrap(text, room) {
				lines = append(lines, line{styled(chunk, style)})
			}
		}
	}

	if len(lines) == 0 {
		lines = append(lines, line{styled("  go-task reports nothing about this task", fg(t.Dim))})
	}

	return scrollPane(lines, width, height, &a.DetailOffset)
}

func (a *App) detailFooter() line {
	if l, ok := a.confirmBar(); ok {
		return l
	}
	return line{styled("j k scroll   ⏎ run   a args   esc back   q quit", fg(a.Theme.Dim))}
}

// --- help -------------------------------------------------------------------------

func (a *App) helpHeader() line {
	t := a.Theme
	l := append(line{}, a.wordmark()...)
	return append(l,
		styled("keys", bold()),
		styled("   the footer shows a subset; this is all of them", fg(t.Dim)),
	)
}

func (a *App) drawHelp(width, height int) []string {
	t := a.Theme
	// Widest key column across every section, so the descriptions line up as one table
	// rather than five.
	pad := keys.WidestKeys()

	var lines []line
	for _, section := range keys.Sections {
		if len(lines) > 0 {
			lines = append(lines, line{})
		}
		lines = append(lines, line{
			styled(section.Title, fgBold(t.Accent)),
			styled("  — "+section.Note, fg(t.Dim)),
		})
		for _, binding := range section.Bindings {
			lines = append(lines, line{
				plain("  "),
				styled(padRight(binding.Keys, pad), fgBold(t.Mode)),
				plain("  "),
				styled(binding.What, fg(t.Text)),
			})
		}
	}

	return scrollPane(lines, width, height, &a.HelpOffset)
}

func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func (a *App) helpFooter() line {
	// `q` works from here too, so the question it can raise has to be visible from here
	// too — a confirmation with nowhere to draw is a modal you cannot see.
	if l, ok := a.confirmBar(); ok {
		return l
	}
	return line{styled("j k scroll   ? esc close   q quit", fg(a.Theme.Dim))}
}

// --- history ----------------------------------------------------------------------

func (a *App) historyHeader() line {
	t := a.Theme
	failed := 0
	for _, m := range a.History {
		if m.Failed() {
			failed++
		}
	}
	scope := "this project"
	if a.HistoryAllProjects {
		scope = "all projects"
	} else if base := baseName(a.Root); base != "" {
		scope = base
	}

	l := append(line{}, a.wordmark()...)
	l = append(l,
		styled("history", bold()),
		styled(" · "+scope, fg(t.Stored)),
	)
	if a.HistoryQuery != "" {
		l = append(l, styled("   /"+a.HistoryQuery, fg(t.Search)))
	}
	return append(l, styled(fmt.Sprintf("   %d runs   %d failed", len(a.History), failed), fg(t.Dim)))
}

func (a *App) drawHistory(width, height int) []string {
	t := a.Theme
	if len(a.History) == 0 {
		return nil
	}
	a.HistoryCursor = clamp(a.HistoryCursor, 0, len(a.History)-1)
	a.HistoryOffset = scrollTo(a.HistoryOffset, a.HistoryCursor, len(a.History), height)

	out := make([]string, 0, height)
	for i := a.HistoryOffset; i < len(a.History) && len(out) < height; i++ {
		m := a.History[i]
		glyph, colour := "✓", t.StatusOk
		if m.Failed() {
			glyph, colour = "✗", t.StatusFailed
		}
		lines := 0
		for _, e := range m.Tasks {
			lines += e.Lines
		}
		commandStyle := fg(theme.Default)
		if m.Failed() {
			commandStyle = fg(t.StatusFailed)
		}
		l := line{
			styled(glyph+" ", fgBold(colour)),
			styled(padRight(ago(m.StartedUnix), 10), fg(t.Dim)),
			styled(padRight(m.Command(), 30), commandStyle),
			styled(fmt.Sprintf("%8s  %6d lines", duration(millis(m.DurationMs)), lines), fg(t.Dim)),
		}
		// Only present when a cross-run search is narrowing the list.
		if n, ok := a.HistoryHits[m.ID]; ok {
			suffix := "s"
			if n == 1 {
				suffix = ""
			}
			l = append(l, styled(fmt.Sprintf("   %d hit%s", n, suffix), fg(t.Search)))
		}
		out = append(out, l.render(width, i == a.HistoryCursor, t.Selection))
	}
	return out
}

func (a *App) historyFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.HistorySearching {
		return line{
			styled("search runs: ", fg(t.Search)),
			plain(a.HistoryQuery),
			styled("█", fg(t.Search)),
			styled(fmt.Sprintf("   %d runs matched", len(a.History)), fg(t.Dim)),
			styled("   ⏎ keep   esc clear", fg(t.Dim)),
		}
	}
	if a.Status != "" {
		return line{styled(a.Status, fg(t.Notice))}
	}
	return line{styled(keys.Footer(&keys.HistorySection), fg(t.Dim))}
}

// --- slot bar ---------------------------------------------------------------------

// drawSlotBar names every open run on one line, so a background one is visible without
// going to look for it — a stack that died an hour ago should not need to be discovered.
//
// Numbered from one to match the digit keys that jump to them.
func (a *App) drawSlotBar() line {
	t := a.Theme
	l := line{plain("  ")}
	for i, slot := range a.Slots() {
		if i > 0 {
			l = append(l, styled("   ", fg(t.Dim)))
		}
		nameStyle := fg(t.Dim)
		if slot.Focused {
			nameStyle = bold()
		}
		l = append(l,
			styled(fmt.Sprintf("%d ", i+1), fg(t.Dim)),
			styled(slot.Status.Glyph()+" ", fgBold(statusStyle(slot.Status, t))),
			styled(slot.Root, nameStyle),
			styled(" "+duration(slot.Elapsed), fg(t.Dim)),
		)
	}
	return l
}

// --- run view ---------------------------------------------------------------------

func (a *App) runHeader() line {
	t := a.Theme
	r := a.Run
	if r == nil {
		return line{}
	}

	elapsed := elapsedOf(r)
	glyph, colour := "▶", t.StatusRunning
	if r.Finished() {
		glyph, colour = "✗", t.StatusFailed
		if r.Exit == 0 {
			glyph, colour = "✓", t.StatusOk
		}
	}

	l := append(line{}, a.wordmark()...)
	l = append(l,
		styled(glyph, fgBold(colour)),
		plain(" "),
		styled(r.Command(), bold()),
		styled("   "+duration(elapsed), fg(t.Dim)),
	)

	if r.Interactive && !r.Finished() {
		l = append(l, styled("   interactive", fg(t.Interactive)))
	}
	if a.Watching != "" {
		l = append(l, styled("   watching "+a.Watching, fg(t.Interactive)))
	}
	switch {
	case r.Cancelled():
		// Cancelled and still going is not the same state as cancelled and gone, and it is
		// the one where you need to be told what is left to try: SIGTERM can be caught,
		// and something that catches it looks identical to something that is taking its
		// time. Naming the next press is the difference between waiting and being stuck.
		text := "   stopping — x again to kill it"
		switch {
		case r.Finished():
			text = "   cancelled"
		case r.Killed():
			text = "   killed — waiting on the OS"
		}
		l = append(l, styled(text, fg(t.StatusFailed)))
	case r.IsStored():
		l = append(l, styled("   from history", fg(t.Stored)))
	case len(r.Graph.Edges) == 0:
		l = append(l, styled("   resolving graph…", fg(t.Dim)))
	case a.Following && !r.Finished():
		l = append(l, styled("   following", fg(t.Notice)))
	}

	if r.HasExit && r.Exit != 0 {
		l = append(l, styled(fmt.Sprintf("   exit %d", r.Exit), fg(t.StatusFailed)))
	}

	if a.Search != nil {
		position := "no matches"
		if len(a.SearchHits) > 0 {
			position = fmt.Sprintf("%d/%d", a.SearchIdx+1, len(a.SearchHits))
		}
		l = append(l,
			styled("   /", fg(t.Dim)),
			styled(a.Search.Pattern, fg(t.Search)),
			styled("  "+position, fg(t.Dim)),
		)
		if a.FilterMatches {
			l = append(l, styled(fmt.Sprintf("  filtered ±%d", a.FilterContext), fg(t.Search)))
		}
	}

	return l
}

// runRowLines builds the rendered lines for one run row.
// splitting them would duplicate the indent, marker and highlight arithmetic.
//
//nolint:cyclop // a task row and a line row are two shapes with one gutter between them;
func (a *App) runRowLines(row RunRow, width int) []line {
	t := a.Theme
	r := a.Run
	if r == nil {
		return nil
	}

	if row.IsTask {
		tr, hasTask := r.Tasks[row.Name]
		status := run.Pending
		if hasTask {
			status = tr.Status
		}
		indent := strings.Repeat("  ", row.Depth)

		// Only offer a fold glyph where there is something to unfold. Filled means open,
		// hollow means ajar — the peek is a door left open a crack, and the glyph should
		// not claim you are looking at all of it.
		hasLines := hasTask && len(tr.Lines) > 0
		glyph := "  "
		if hasLines {
			switch row.Fold {
			case FoldHidden:
				glyph = "▸ "
			case FoldPeek:
				glyph = "▿ "
			case FoldFull:
				glyph = "▾ "
			}
		}

		nameStyle := fg(theme.Default)
		switch status {
		case run.Failed:
			nameStyle = fgBold(t.StatusFailed)
		case run.Skipped:
			nameStyle = fg(t.Dim)
		default:
			// Pending, running and finished-well all read as ordinary text; the glyph
			// beside the name is what carries the state.
		}

		l := line{
			plain(indent),
			styled(glyph, fg(t.Dim)),
			styled(status.Glyph()+" ", fgBold(statusStyle(status, t))),
			styled(row.Name, nameStyle),
		}

		var tail strings.Builder
		if hasTask {
			// Ticking while the task runs, final once it stops.
			if d, ok := tr.Elapsed(); ok {
				fmt.Fprintf(&tail, "  %7s", duration(d))
			}
			// Closed, say how much there is; ajar, say how much is out of sight. "45
			// lines" next to five of them on screen reads as a contradiction.
			switch row.Fold {
			case FoldHidden:
				if len(tr.Lines) > 0 {
					fmt.Fprintf(&tail, "  %d lines", len(tr.Lines))
				}
			case FoldPeek:
				if hidden := len(tr.Lines) - a.PeekLines; hidden > 0 {
					fmt.Fprintf(&tail, "  %d more", hidden)
				}
			default:
				// Fully open: the lines are all on screen, so counting them would be
				// telling you what you can already see.
			}
			// Said out loud, open or closed: a buffer that has silently forgotten its
			// first hour is a buffer you would otherwise search and trust.
			if tr.Dropped > 0 {
				fmt.Fprintf(&tail, "  %d earlier dropped", tr.Dropped)
			}
		}
		if tail.Len() > 0 {
			l = append(l, styled(tail.String(), fg(t.Dim)))
		}
		return []line{l}
	}

	text, isCommand := "", false
	if tr, ok := r.Tasks[row.Task]; ok && row.Index < len(tr.Lines) {
		text, isCommand = tr.Lines[row.Index].Plain, tr.Lines[row.Index].IsCommand
	}

	indent := strings.Repeat("  ", row.Depth)
	// The gutter distinguishes go-task's own command echo from the command's actual
	// output.
	marker := "│ "
	if isCommand {
		marker = "$ "
	}
	// Two cells for the marker, whatever it is made of. `│` is three bytes and one column,
	// and measuring it in bytes — as the Rust original did — wrapped output two columns
	// early and, worse, disagreed with runRowHeight, which always assumed two. A line
	// measured shorter than it renders is a line that overflows its column.
	room := max(8, width-(utf8.RuneCountInString(indent)+gutter+2))

	base := fg(theme.Default)
	if isCommand {
		base = fg(t.Alias)
	}

	// One captured line can become several visual rows; the number and the marker belong
	// only to the first. In a peek it stays one row, cut at the edge — which is the trade
	// the window makes: five lines you can count beats one line you can read all of.
	var chunks []string
	if row.Peek {
		chunks = []string{clip(text, room)}
	} else {
		chunks = wrap(text, room)
	}

	out := make([]line, 0, len(chunks))
	for n, chunk := range chunks {
		l := line{plain(indent)}
		if n == 0 {
			l = append(l,
				styled(fmt.Sprintf("%*d ", gutter-1, row.Index+1), fg(t.Dim)),
				styled(marker, fg(t.Dim)),
			)
		} else {
			// Continuation: blank gutter, aligned under the text above.
			l = append(l, plain(strings.Repeat(" ", gutter)), styled("  ", fg(t.Dim)))
		}

		// Highlight per chunk, so a match survives being wrapped.
		if a.Search != nil {
			if start, end, ok := a.Search.FirstMatch(chunk); ok && end <= len(chunk) {
				l = append(l,
					styled(chunk[:start], base),
					styled(chunk[start:end], onBg(t.MatchFg, t.MatchBg)),
					styled(chunk[end:], base),
				)
				out = append(out, l)
				continue
			}
		}
		out = append(out, append(l, styled(chunk, base)))
	}
	return out
}

// runRowHeight is how many terminal rows one run row occupies once wrapped.
func (a *App) runRowHeight(row RunRow, width int) int {
	// A peeking line is always exactly one row. That is the whole contract of the window:
	// its height is known before its content is, so it cannot grow under you as output
	// arrives.
	if row.IsTask || row.Peek {
		return 1
	}
	text := ""
	if a.Run != nil {
		if t, ok := a.Run.Tasks[row.Task]; ok && row.Index < len(t.Lines) {
			text = t.Lines[row.Index].Plain
		}
	}
	prefix := row.Depth*2 + gutter + 2
	return len(wrap(text, max(8, width-prefix)))
}

func (a *App) drawRun(width, height int) []string {
	if a.Run == nil {
		return nil
	}
	height = max(1, height)

	// One column at full width unless the output genuinely overflows, in which case use
	// whatever the width can carry. Heights depend on the column width, so they are
	// measured once to decide and again to lay out.
	single := 0
	for _, r := range a.RunRows {
		single += a.runRowHeight(r, width)
	}
	columns := 1
	if single > height {
		columns = clamp(width/minRunColumn, 1, 3)
	}
	widths := columnWidths(width, columns)
	colWidth := widths[0]
	if columns > 1 {
		colWidth = max(0, colWidth-2)
	}

	heights := make([]int, len(a.RunRows))
	for i, r := range a.RunRows {
		heights[i] = a.runRowHeight(r, colWidth)
	}

	a.RunCursor = min(a.RunCursor, max(0, len(a.RunRows)-1))
	a.RunOffset = offsetForCursor(heights, a.RunCursor, height, columns)
	bounds := columnBounds(heights, a.RunOffset, height, columns)

	build := func(from, to int) [][]line {
		out := make([][]line, 0, to-from)
		for i := from; i < to; i++ {
			out = append(out, a.runRowLines(a.RunRows[i], colWidth))
		}
		return out
	}
	return a.composeColumns(bounds, widths, colWidth, height, a.RunCursor, build)
}

// --- picker -----------------------------------------------------------------------

func (a *App) pickerHeader() line {
	t := a.Theme
	dir := baseName(a.Root)
	if dir == "" {
		dir = a.Root
	}

	shown := 0
	for _, r := range a.Rows {
		if a.Tree.Nodes[r.Node].Task != pivot.NoTask {
			shown++
		}
	}

	l := append(line{}, a.wordmark()...)
	l = append(l,
		plain(dir),
		styled("  group: ", fg(t.Dim)),
		styled(a.Mode.Label(), fgBold(t.Mode)),
	)

	if a.Query == "" {
		l = append(l, styled(fmt.Sprintf("   %d tasks", len(a.Tasks)), fg(t.Dim)))
		if a.InteractiveNext {
			l = append(l, styled("   interactive", fg(t.Interactive)))
		}
		if a.ForceNext {
			l = append(l, styled("   force", fg(t.Notice)))
		}
		// Leaving the run view does not stop anything; say so, or it is easy to forget —
		// and with several slots open the picker is the only screen that would not
		// otherwise mention the ones you are not looking at.
		switch n := a.InFlightCount(); n {
		case 0:
		case 1:
			name := ""
			for _, s := range a.Slots() {
				if s.Status == run.Running {
					name = s.Root
					break
				}
			}
			l = append(l, styled("   ▶ "+name+" running", fg(t.Notice)))
		default:
			l = append(l, styled(fmt.Sprintf("   ▶ %d running", n), fg(t.Notice)))
		}
	} else {
		l = append(l,
			styled("   filter: ", fg(t.Dim)),
			styled(a.Query, fg(t.Search)),
			styled(fmt.Sprintf("   %d/%d tasks", shown, len(a.Tasks)), fg(t.Dim)),
		)
	}

	return l
}

// treeItem builds one row of the task tree.
func (a *App) treeItem(row pivot.Row, width int) []line {
	t := a.Theme
	node := a.Tree.Nodes[row.Node]
	indent := strings.Repeat("  ", row.Depth)

	// A node can be both a group and a task (`backend:migrate`), so the fold glyph and the
	// runnable-ness are decided independently.
	glyph := "  "
	if node.IsGroup() {
		glyph = "▸ "
		if row.Open {
			glyph = "▾ "
		}
	}

	labelStyle := fg(theme.Default)
	if node.IsGroup() {
		labelStyle = bold()
	}

	l := line{
		plain(indent),
		styled(glyph, fg(t.Dim)),
		styled(node.Label, labelStyle),
	}

	used := row.Depth*2 + 2 + utf8.RuneCountInString(node.Label)

	if node.IsGroup() {
		c := fmt.Sprintf("  %d", node.Count)
		used += utf8.RuneCountInString(c)
		l = append(l, styled(c, fg(t.Dim)))
	}

	var extra []line

	if node.Task != pivot.NoTask {
		task := a.Tasks[node.Task]
		// Running now takes the column, because it is the more urgent of the two questions
		// and the only one the list could not previously answer: with runs parked in slots
		// you are not looking at, the footer could say "3 running" while every row in
		// front of you showed nothing but history. The elapsed time reads the same as the
		// slot bar's, so the two agree at a glance.
		//
		// Failing that, how it went last time. A blank column means never run, which is
		// information too — it is not the same as having passed.
		if elapsed, ok := a.RunningFor(task.Name); ok {
			l = append(l, styled("  ▶", fgBold(t.StatusRunning)))
			forHowLong := " " + duration(elapsed)
			used += 3 + utf8.RuneCountInString(forHowLong)
			l = append(l, styled(forHowLong, fg(t.Dim)))
		} else if o, ok := a.Outcomes[task.Name]; ok {
			glyph, colour := "  ✗", t.StatusFailed
			if o.Ok {
				glyph, colour = "  ✓", t.StatusOk
			}
			l = append(l, styled(glyph, fgBold(colour)))
			when := " " + ago(o.WhenUnix)
			used += utf8.RuneCountInString(glyph) + utf8.RuneCountInString(when)
			l = append(l, styled(when, fg(t.Dim)))
		}
		if task.Dangerous {
			l = append(l, styled("  ⚠", fgBold(t.Danger)))
			used += 3
		}
		if len(task.Aliases) > 0 {
			aliases := "  (" + strings.Join(task.Aliases, ", ") + ")"
			used += utf8.RuneCountInString(aliases)
			l = append(l, styled(aliases, fg(t.Alias)))
		}
		// Descriptions wrap into their own column rather than being cut off mid-word — a
		// truncated description is the half that does not tell you anything. Continuation
		// rows are indented to line up under the first.
		//
		// Aligned at column 34 when there is room, tightened when there is not — a
		// three-column layout gives each description about half its column, and starting
		// it at 34 would leave it nothing.
		descCol := clamp(width/2, used+2, max(34, used+2))
		if task.Desc != "" && descCol+12 < width {
			room := width - descCol - 1
			chunks := wrap(task.Desc, room)
			if len(chunks) > 0 {
				l = append(l,
					plain(strings.Repeat(" ", max(0, descCol-used))),
					styled(chunks[0], fg(t.Dim)),
				)
				for _, chunk := range chunks[1:] {
					extra = append(extra, line{
						plain(strings.Repeat(" ", descCol)),
						styled(chunk, fg(t.Dim)),
					})
				}
			}
		}
	}

	return append([]line{l}, extra...)
}

func (a *App) drawTree(width, height int) []string {
	height = max(1, height)

	single := 0
	for _, r := range a.Rows {
		single += len(a.treeItem(r, width))
	}
	columns := 1
	if single > height {
		columns = clamp(width/minColumn, 1, 3)
	}
	widths := columnWidths(width, columns)
	colWidth := widths[0]
	if columns > 1 {
		colWidth = max(0, colWidth-2)
	}

	heights := make([]int, len(a.Rows))
	for i, r := range a.Rows {
		heights[i] = len(a.treeItem(r, colWidth))
	}

	a.Cursor = min(a.Cursor, max(0, len(a.Rows)-1))
	a.Offset = offsetForCursor(heights, a.Cursor, height, columns)
	bounds := columnBounds(heights, a.Offset, height, columns)

	build := func(from, to int) [][]line {
		out := make([][]line, 0, to-from)
		for i := from; i < to; i++ {
			out = append(out, a.treeItem(a.Rows[i], colWidth))
		}
		return out
	}
	return a.composeColumns(bounds, widths, colWidth, height, a.Cursor, build)
}

// composeColumns lays items into columns and zips them into terminal rows.
//
// The cursor lives in whichever column contains it; the rest are a look-ahead, which is
// why the selection is only drawn once.
func (a *App) composeColumns(
	bounds [][2]int,
	widths []int,
	colWidth, height, cursor int,
	build func(from, to int) [][]line,
) []string {
	cols := make([][]string, len(widths))
	for c := range cols {
		cols[c] = make([]string, height)
		for i := range cols[c] {
			cols[c][i] = strings.Repeat(" ", widths[c])
		}
	}

	for c, b := range bounds {
		if c >= len(widths) || b[0] >= b[1] {
			break
		}
		items := build(b[0], b[1])
		at := 0
		for i, item := range items {
			selected := b[0]+i == cursor
			for _, l := range item {
				if at >= height {
					break
				}
				text := l.render(colWidth, selected, a.Theme.Selection)
				if pad := widths[c] - colWidth; pad > 0 {
					if selected {
						text += selectionOf(lipgloss.NewStyle(), a.Theme.Selection).Render(strings.Repeat(" ", pad))
					} else {
						text += strings.Repeat(" ", pad)
					}
				}
				cols[c][at] = text
				at++
			}
		}
	}

	out := make([]string, height)
	for i := range height {
		var b strings.Builder
		for c := range cols {
			b.WriteString(cols[c][i])
		}
		out[i] = b.String()
	}
	return out
}

// columnWidths splits width into n columns of as near equal size as whole cells allow.
//
// Each boundary is the *rounded* fraction of the width rather than the truncated one, so
// 200 across three columns comes out 67/66/67 and not 66/67/67. That is not cosmetic: the
// text width every item is built for comes from the first column, so which column carries
// the spare cell decides where every description wraps.
func columnWidths(width, columns int) []int {
	boundary := func(i int) int { return (width*i + columns/2) / columns }
	out := make([]int, columns)
	for i := range columns {
		out[i] = boundary(i+1) - boundary(i)
	}
	return out
}

// --- footers ----------------------------------------------------------------------

// confirmBar is the confirmation line. It takes precedence over every other footer:
// nothing else is happening until it is answered.
func (a *App) confirmBar() (line, bool) {
	pending := a.Confirm
	if pending == nil {
		return nil, false
	}
	t := a.Theme
	// Verb, subject, why, and what `y` will do. Spelling out the verb separately from the
	// subject is what keeps "run deploy:prod" from reading like "stop deploy:prod" at a
	// glance — these are the two questions it is least acceptable to confuse.
	var verb, subject, why, does string
	switch pending.Kind {
	case ConfirmRun:
		subject = "task " + pending.Name
		if len(pending.Args) > 0 {
			subject += " " + strings.Join(pending.Args, " ")
		}
		why = "  —  this one touches production.  "
		if pending.Reason == WouldStopRunning {
			why = "  —  this stops the run already going.  "
		}
		verb, does = " run ", " to run"
	case ConfirmQuit:
		verb, does = " quit ", " to quit"
		switch pending.Live {
		case 0:
			// Nothing to lose, so there is no warning to give — but the question is still
			// asked, so `q` never means "gone" without a second keystroke.
			why = "  "
		case 1:
			why = "  —  1 run is still going, and quitting stops it.  "
		default:
			why = fmt.Sprintf("  —  %d runs are still going, and quitting stops them.  ", pending.Live)
		}
	case ConfirmStopAll:
		verb, does = " stop ", " to stop"
		subject = fmt.Sprintf("all %d runs", pending.Live)
		if pending.Live == 1 {
			subject = "1 run"
		}
		why = "  —  including the ones you are not looking at.  "
	}

	return line{
		styled("  ⚠  ", onBg(t.ConfirmFg, t.ConfirmBg)),
		styled(verb, fg(t.StatusFailed)),
		styled(subject, fgBold(t.StatusFailed)),
		styled(why, fg(t.StatusFailed)),
		styled("y", fgBold(t.StatusFailed)),
		styled(does+", anything else cancels", fg(t.Dim)),
	}, true
}

// argsPrompt is shared by both screens that can open it.
func (a *App) argsPrompt() (line, bool) {
	if !a.EnteringArgs {
		return nil, false
	}
	t := a.Theme
	runes := []rune(a.ArgsInput)
	at := clamp(a.ArgsCursor, 0, len(runes))
	l := line{
		styled("task "+a.ArgsTarget+" ", fg(t.Accent)),
		plain(string(runes[:at])),
		styled("█", fg(t.Accent)),
		plain(string(runes[at:])),
	}
	// A hint, not a default: the descriptions trail off into prose often enough that
	// pre-filling would hand you a subtly wrong command.
	if hint, ok := a.ArgsHint(); ok {
		l = append(l, styled("   e.g. "+hint, fg(t.Dim)))
	} else {
		l = append(l, styled("   ⏎ run   esc cancel", fg(t.Dim)))
	}
	return l, true
}

func (a *App) pickerFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Jumping {
		l := line{
			styled("jump: ", fg(t.Accent)),
			plain(a.JumpQuery),
			styled("█", fg(t.Accent)),
		}
		if a.JumpQuery != "" {
			text := "   no match"
			if len(a.JumpMatches) > 0 {
				text = fmt.Sprintf("   %d/%d", a.JumpIdx+1, len(a.JumpMatches))
			}
			l = append(l, styled(text, fg(t.Dim)))
		}
		return append(l, styled("   ⇥ next   ⏎ stay   esc go back", fg(t.Dim)))
	}
	if l, ok := a.argsPrompt(); ok {
		return l
	}
	if a.Filtering {
		return line{
			styled("/", fg(t.Search)),
			plain(a.Query),
			styled("█", fg(t.Search)),
			styled("   ⏎ accept   esc clear", fg(t.Dim)),
		}
	}
	if a.Status != "" {
		return line{styled(a.Status, fg(t.Notice))}
	}

	hint := "p group by verb"
	if a.Mode.Toggled() == pivot.Domain {
		hint = "p group by domain"
	}
	return line{styled(hint+"   "+keys.Footer(&keys.Picker), fg(t.Dim))}
}

func (a *App) runFooter() line {
	t := a.Theme

	if a.SendingInput {
		l := line{styled("  input  ", onBg(t.WarningFg, t.Interactive))}
		if prompt, ok := promptOf(a.Run); ok {
			l = append(l, styled(" "+prompt, fg(t.Interactive)))
		} else {
			l = append(l, styled(" keys go to the task", fg(t.Interactive)))
		}
		// The receipt: what has actually gone down the pipe. Without it, "I typed y and
		// nothing happened" cannot be told apart from "y never left the building".
		if a.Run != nil && a.Run.Sent != "" {
			l = append(l,
				styled("   sent: ", fg(t.Dim)),
				styled(a.Run.Sent, fgBold(t.Interactive)),
			)
		}
		// Typing works either way, but in a buffered run you are doing it blind.
		if a.Run != nil && !a.Run.Interactive {
			l = append(l, styled("   buffered: ⏎ sends a newline, output may lag   ⇧I re-runs visibly", fg(t.Notice)))
		}
		return append(l, styled("   esc to stop typing", fg(t.Dim)))
	}

	// Under `prefixed` a blocked task emits nothing at all, so this is the only warning
	// available — otherwise it reads as an unusually slow build.
	if a.PossiblyStuck() {
		return line{
			styled("  …  ", onBg(t.WarningFg, t.WarningBg)),
			styled(" no output for a while", fg(t.Notice)),
			styled("   waiting for input?  i types at it   ⇧I re-runs so you can see it   x stops", fg(t.Dim)),
		}
	}

	// A task blocked on a question looks identical to a slow one; say which it is.
	if a.AwaitingInput() {
		prompt, _ := promptOf(a.Run)
		return line{
			styled("  ?  ", onBg(t.WarningFg, t.WarningBg)),
			styled(" "+prompt, fg(t.Notice)),
			styled("   i to answer   x to stop", fg(t.Dim)),
		}
	}

	if l, ok := a.confirmBar(); ok {
		return l
	}
	if l, ok := a.argsPrompt(); ok {
		return l
	}

	if a.Searching {
		// The prompt says which of the two jobs it is doing: jump to matches, or hide
		// everything that is not one.
		label := "/"
		if a.FilterMatches {
			label = "filter: "
		}
		l := line{
			styled(label, fg(t.Search)),
			plain(a.SearchInput),
			styled("█", fg(t.Search)),
		}
		switch {
		case a.SearchError != "":
			l = append(l, styled("   "+a.SearchError, fg(t.StatusFailed)))
		case a.Search != nil:
			tasks := map[string]bool{}
			for _, h := range a.SearchHits {
				tasks[h.Task] = true
			}
			n := len(a.SearchHits)
			l = append(l, styled(fmt.Sprintf("   %d %s in %d %s",
				n, plural(n, "match", "matches"),
				len(tasks), plural(len(tasks), "task", "tasks")), fg(t.Dim)))
		}
		return append(l, styled("   ⏎ keep   esc clear", fg(t.Dim)))
	}

	if a.Status != "" {
		return line{styled(a.Status, fg(t.Notice))}
	}
	return line{styled(keys.Footer(&keys.Run), fg(t.Dim))}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func promptOf(r *run.Run) (string, bool) {
	if r == nil {
		return "", false
	}
	return r.PendingPrompt()
}

// --- small helpers ----------------------------------------------------------------

func baseName(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// scrollTo keeps a cursor inside a simple, one-line-per-row list.
func scrollTo(offset, cursor, count, height int) int {
	if height <= 0 || count == 0 {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	return clamp(offset, 0, max(0, count-height))
}

func millis(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// elapsedOf is a run's clock: the final figure once it has stopped, a ticking one until
// then.
func elapsedOf(r *run.Run) time.Duration {
	if r.HasDuration {
		return r.Duration
	}
	return time.Since(r.Started)
}
