package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/theme"
)

// minRunColumn is the same for output, which needs more width than a task name does, so it
// splits later than the picker.
const minRunColumn = 60

// View is what Bubble Tea asks for: the frame, plus the terminal state the frame wants.
//
// The alternate screen is declared here rather than passed to NewProgram because that is
// where v2 moved it — a frame now carries the modes it needs, so there is one answer to
// "are we on the alternate screen" instead of a startup flag and a pair of commands.
func (a *App) View() tea.View {
	v := tea.NewView(a.frame())
	v.AltScreen = true
	// Asking for mouse events is what makes the wheel reach this program at all. Without it
	// the terminal keeps the wheel and scrolls its own scrollback — which in a Neovim
	// terminal buffer means the wheel scrolls the buffer taskui is drawing into, past
	// frames it has already thrown away.
	//
	// Cell motion rather than all motion: it is the least that carries the wheel, and the
	// difference is a report on every pixel of movement that nothing here would read.
	if a.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// frame renders one frame. Rendering to a string rather than into a cell buffer is what
// makes `--screenshot` and the render tests the same code path as the live UI.
func (a *App) frame() string {
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
	// The hairlines are what separate the three bands, but they are the first thing to go
	// when there is no room: a rule costs a row of content, and on an eight-row terminal
	// content is the only thing worth spending rows on.
	rules := 0
	if left >= minRuledBody+2 {
		rules = 2
		left -= 2
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
	case ScreenTimeline:
		header = a.timelineHeader()
		body = a.drawTimeline(width, bodyH)
		footer = a.timelineFooter()
	case ScreenDiff:
		header = a.diffHeader()
		body = a.drawDiff(width, bodyH)
		footer = a.diffFooter()
	case ScreenProfile:
		header = a.profileHeader()
		body = a.drawProfile(width, bodyH)
		footer = a.profileFooter()
	}

	if headerH == 1 {
		out = append(out, header.render(width, false, a.Theme.Colors.Selection))
	}
	if slotsH == 1 {
		out = append(out, a.drawSlotBar().render(width, false, a.Theme.Colors.Selection))
	}
	// The rule closes the header band, so the slot bar sits inside it rather than adrift
	// between the rule and the body.
	if rules > 0 {
		out = append(out, a.hairline(width))
	}
	for i := range bodyH {
		if i < len(body) {
			out = append(out, body[i])
		} else {
			out = append(out, blank)
		}
	}
	if rules > 0 {
		out = append(out, a.hairline(width))
	}
	if footerH == 1 {
		out = append(out, footer.render(width, false, a.Theme.Colors.Selection))
	}

	return strings.Join(out, "\n")
}

// minRuledBody is how much body has to be left over before the hairlines earn their rows.
const minRuledBody = 4

// hairline is the rule between the bands. It starts one column in, under the cursor rail,
// so the rail's column stays the only thing that ever appears there.
func (a *App) hairline(width int) string {
	return line{
		plain(" "),
		styled(strings.Repeat(a.Theme.Glyphs.Rule, max(0, width-1)), fg(a.Theme.Colors.Rule)),
	}.render(width, false, a.Theme.Colors.Selection)
}

// RenderFrame renders one frame at a given size, escape sequences and all.
//
// The colours are the point when you are writing a theme: the loop is edit the file, run
// this, look at it. Everything else — the tests, the diffs — wants the text underneath,
// which is what RenderHeadless returns.
func (a *App) RenderFrame(w, h int) string {
	a.Width, a.Height = w, h
	return a.frame()
}

// RenderHeadless renders one frame to plain text off-screen. It backs both the render
// tests and `--screenshot`, which is how you look at the real thing without a terminal.
func (a *App) RenderHeadless(w, h int) []string {
	frame := a.RenderFrame(w, h)
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

// header lays a header out: the wordmark and a subject on the left, state right-anchored
// against the last column.
//
// Right-anchoring is what makes the state block a column rather than a tail. It always ends
// in the same place, so the eye knows where to look for "how many tasks" or "did it fail"
// without reading the left-hand side first.
func (a *App) header(subject string, state []span) line {
	mark := a.wordmark()
	l := line{plain(" "), styled(mark, fgBold(a.Theme.Colors.Accent))}
	// `taskui · taskui` — the wordmark, then a directory that happens to share its name —
	// reads as a bug, and spends the most valuable row on the screen saying it twice.
	//
	// Compared against the wordmark as drawn rather than as written, so a theme that put
	// `{project}` in it is caught by the same rule that catches one whose name happens to
	// match.
	if subject != "" && !strings.EqualFold(subject, plainWordmark(mark)) {
		l = append(l,
			styled(" "+a.Theme.Glyphs.Separator+" ", fg(a.Theme.Colors.Faint)),
			styled(subject, bold()))
	}
	used := 0
	for _, s := range l {
		used += utf8.RuneCountInString(s.text)
	}
	stateWidth := 0
	for _, s := range state {
		stateWidth += utf8.RuneCountInString(s.text)
	}
	l = append(l, plain(strings.Repeat(" ", max(1, a.Width-1-used-stateWidth))))
	return append(l, state...)
}

func statusChip(status run.Status, t theme.Theme) span {
	switch status {
	case run.Ok:
		return styled(" PASSED ", onBg(t.Colors.WarningFg, t.Colors.StatusOk))
	case run.Failed:
		return styled(" FAILED ", onBg(t.Colors.WarningFg, t.Colors.StatusFailed))
	default:
		return styled(" RUNNING ", onBg(t.Colors.WarningFg, t.Colors.StatusRunning))
	}
}

// statusGlyph is the theme's mark for a status. `run.Status.Glyph()` stays as the default
// for the headless `--run` output, which is piped and diffed rather than looked at.
func statusGlyph(status run.Status, t theme.Theme) string {
	switch status {
	case run.Pending:
		return t.Glyphs.StatusPending
	case run.Running:
		return t.Glyphs.StatusRunning
	case run.Ok:
		return t.Glyphs.StatusOk
	case run.Failed:
		return t.Glyphs.StatusFailed
	default:
		return t.Glyphs.StatusSkipped
	}
}

// projectPlaceholder is what a wordmark writes to mean "whatever this project is called",
// and framePlaceholder is where the wordmark's animation goes.
const (
	projectPlaceholder = "{project}"
	framePlaceholder   = "{frame}"
)

// wordmark is the theme's wordmark with its placeholders filled in.
//
// `{project}` is what makes the header able to name the thing you are looking at rather
// than the tool you are looking at it with, without a theme having to give up its own
// decoration to do it: `"✧･ﾟ {project} ･ﾟ✧"` keeps the sparkles and changes the middle.
func (a *App) wordmark() string {
	mark := a.Theme.Glyphs.Wordmark
	if strings.Contains(mark, projectPlaceholder) {
		name := baseName(a.Root)
		if name == "" {
			name = a.Root
		}
		mark = strings.ReplaceAll(mark, projectPlaceholder, name)
	}
	return strings.ReplaceAll(mark, framePlaceholder, a.Theme.Animation.WordmarkFrame(a.Phase))
}

// plainWordmark is the wordmark with any decoration stripped, so a themed one still
// recognises the project it is named after.
//
// Trims from both ends anything that is not a letter or a digit, rather than a list of the
// decoration characters seen so far — that list rotted the moment two themes were added
// whose flourishes were not on it, and the header started printing the name twice. Modifier
// letters go too: `ﾟ` is one, and it is punctuation everywhere this is used.
//
// A cosmetic comparison, so the odd script that builds words out of modifier letters losing
// a character here costs nothing but a repeated word in a header.
func plainWordmark(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		if unicode.IsDigit(r) {
			return false
		}
		return !unicode.IsLetter(r) || unicode.Is(unicode.Lm, r)
	})
}

func statusStyle(status run.Status, t theme.Theme) theme.Color {
	switch status {
	case run.Pending:
		return t.Colors.StatusPending
	case run.Running:
		return t.Colors.StatusRunning
	case run.Ok:
		return t.Colors.StatusOk
	case run.Failed:
		return t.Colors.StatusFailed
	default:
		return t.Colors.StatusSkipped
	}
}

// scrollPane clamps an offset and cuts a paragraph down to the visible rows.
func (a *App) scrollPane(lines []line, width, height int, offset *int) []string {
	overflow := max(0, len(lines)-height)
	*offset = min(*offset, overflow)
	out := make([]string, 0, height)
	for i := *offset; i < len(lines) && len(out) < height; i++ {
		// renderRow, not render: the blank rail column keeps these panes flush with the
		// rows on every other screen.
		out = append(out, lines[i].renderRow(width, false, a.Theme, a.Phase, 0, 1))
	}
	return out
}

// --- detail -----------------------------------------------------------------------

func (a *App) detailHeader() line {
	t := a.Theme
	var state []span
	if o, ok := a.Outcomes[a.DetailOf]; ok {
		glyph, colour := t.Glyphs.StatusFailed+" ", t.Colors.StatusFailed
		if o.Ok {
			glyph, colour = t.Glyphs.StatusOk+" ", t.Colors.StatusOk
		}
		state = []span{styled(glyph, fgBold(colour)), styled(ago(o.WhenUnix), fg(t.Colors.Dim))}
	}
	return a.header(a.DetailOf, state)
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
		lines = append(lines, line{styled(title, fgBold(t.Colors.Accent))})
	}

	for _, para := range d.Summary {
		for _, chunk := range wrap(para, room) {
			lines = append(lines, line{styled("  "+chunk, fg(t.Colors.Text))})
		}
	}

	if len(d.Requires) > 0 {
		section("requires")
		for _, v := range d.Requires {
			lines = append(lines, line{
				plain("  "),
				styled(v+"=", fgBold(t.Colors.Mode)),
				styled("   must be supplied with `a`", fg(t.Colors.Dim)),
			})
		}
	}

	if len(d.Dependencies) > 0 {
		section("runs first")
		for _, dep := range d.Dependencies {
			lines = append(lines, line{styled("  "+dep, fg(t.Colors.Alias))})
		}
	}

	if len(d.Commands) > 0 {
		heading := "will run"
		if a.UpToDate(a.DetailOf) {
			// go-task's own answer to "would running this do anything". Saying it here is
			// the difference between predicting that `⏎` does nothing and discovering it.
			heading = "would run — but go-task says it is up to date"
		}
		section(heading)
		if len(d.Requires) > 0 {
			// `task --summary` expands the template before printing it, so a variable you
			// have not supplied is substituted with nothing: `case "" in` where the real run
			// would have `case "v0.3.0" in`. The panel is not hiding anything — `requires`
			// is right above — but a command preview that quietly differs from the command
			// is worse than one that says it does.
			lines = append(lines, line{styled(
				"  shown with "+strings.Join(d.Requires, ", ")+" unset — `a` supplies them",
				fg(t.Colors.Notice),
			)})
		}
		for _, cmd := range d.Commands {
			// Another task, or a shell line — worth telling apart at a glance.
			style, text := fg(t.Colors.Text), "  "+cmd
			if name, ok := strings.CutPrefix(cmd, "Task: "); ok {
				style, text = fg(t.Colors.Alias), "  → "+name
			}
			for _, chunk := range wrap(text, room) {
				lines = append(lines, line{styled(chunk, style)})
			}
		}
	}

	if len(lines) == 0 {
		lines = append(lines, line{styled("  go-task reports nothing about this task", fg(t.Colors.Dim))})
	}

	return a.scrollPane(lines, width, height, &a.DetailOffset)
}

func (a *App) detailFooter() line {
	if l, ok := a.confirmBar(); ok {
		return l
	}
	// Through the table like every other screen. This footer used to be a literal, which is
	// exactly the drift the table exists to prevent: `e` was added to the section and this
	// line went on listing four keys, and it could not report a status either.
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(a.Theme.Colors.Notice))}
	}
	return a.hintBar(&keys.DetailSection)
}

// --- help -------------------------------------------------------------------------

func (a *App) helpHeader() line {
	t := a.Theme
	return a.header("keys", []span{
		styled("the footer shows a subset; this is all of them", fg(t.Colors.Dim)),
	})
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
			styled(section.Title, fgBold(t.Colors.Accent)),
			styled("  — "+section.Note, fg(t.Colors.Dim)),
		})
		for _, binding := range section.Bindings {
			lines = append(lines, line{
				plain("  "),
				styled(padRight(binding.Keys, pad), fgBold(t.Colors.Mode)),
				plain("  "),
				styled(binding.What, fg(t.Colors.Text)),
			})
		}
	}

	return a.scrollPane(lines, width, height, &a.HelpOffset)
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
	// `t` only works its way back to a list, so it is only offered where there is one.
	hints := "j k scroll   ? esc close   q quit"
	if a.helpReturn == ScreenPicker {
		hints = "j k scroll   t jump   ? esc close   q quit"
	}
	return line{plain(" "), styled(hints, fg(a.Theme.Colors.Dim))}
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

	state := []span{styled(scope, fg(t.Colors.Stored))}
	if a.HistoryQuery != "" {
		state = append(state, styled("   /"+a.HistoryQuery, fg(t.Colors.Search)))
	}
	state = append(state, styled(fmt.Sprintf("   %d runs   %d failed", len(a.History), failed), fg(t.Colors.Dim)))
	return a.header("history", state)
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
		glyph, colour := t.Glyphs.StatusOk, t.Colors.StatusOk
		if m.Failed() {
			glyph, colour = t.Glyphs.StatusFailed, t.Colors.StatusFailed
		}
		lines := 0
		for _, e := range m.Tasks {
			lines += e.Lines
		}
		commandStyle := fg(theme.Default)
		if m.Failed() {
			commandStyle = fg(t.Colors.StatusFailed)
		}
		l := line{
			styled(glyph+" ", fgBold(colour)),
			styled(padRight(ago(m.StartedUnix), 10), fg(t.Colors.Dim)),
			styled(padRight(m.Command(), 30), commandStyle),
			styled(fmt.Sprintf("%8s  %6d lines", duration(millis(m.DurationMs)), lines), fg(t.Colors.Dim)),
		}
		// Only present when a cross-run search is narrowing the list.
		if n, ok := a.HistoryHits[m.ID]; ok {
			suffix := "s"
			if n == 1 {
				suffix = ""
			}
			l = append(l, styled(fmt.Sprintf("   %d hit%s", n, suffix), fg(t.Colors.Search)))
		}
		out = append(out, l.renderRow(width, i == a.HistoryCursor, t, a.Phase, 0, 1))
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
			plain(" "),
			styled("search runs: ", fg(t.Colors.Search)),
			plain(a.HistoryQuery),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
			styled(fmt.Sprintf("   %d runs matched", len(a.History)), fg(t.Colors.Dim)),
			styled("   ⏎ keep   esc clear", fg(t.Colors.Dim)),
		}
	}
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}
	return a.hintBar(&keys.HistorySection)
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
			l = append(l, styled("   ", fg(t.Colors.Dim)))
		}
		nameStyle := fg(t.Colors.Dim)
		if slot.Focused {
			nameStyle = bold()
		}
		l = append(l,
			styled(fmt.Sprintf("%d ", i+1), fg(t.Colors.Dim)),
			styled(statusGlyph(slot.Status, t)+" ", fgBold(statusStyle(slot.Status, t))),
			styled(slot.Root, nameStyle),
			styled(" "+duration(slot.Elapsed), fg(t.Colors.Dim)),
		)
		// A slot quitting will not take down should say so where you choose between slots.
		if a.IsDetached(slot.Seq) {
			l = append(l, styled(" detached", fg(t.Colors.Stored)))
		}
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

	status := run.Running
	if r.Finished() {
		status = run.Failed
		if r.Exit == 0 {
			status = run.Ok
		}
	}

	var l line
	if r.Interactive && !r.Finished() {
		l = append(l, styled("   interactive", fg(t.Colors.Interactive)))
	}
	if a.Watching != "" {
		l = append(l, styled("   watching "+a.Watching, fg(t.Colors.Interactive)))
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
		l = append(l, styled(text, fg(t.Colors.StatusFailed)))
	case r.IsStored():
		l = append(l, styled("   from history", fg(t.Colors.Stored)))
	case len(r.Graph.Edges) == 0:
		l = append(l, styled("   resolving graph…", fg(t.Colors.Dim)))
	case a.Following && !r.Finished():
		l = append(l, styled("   following", fg(t.Colors.Notice)))
	}

	if a.Search != nil {
		position := "no matches"
		if len(a.SearchHits) > 0 {
			position = fmt.Sprintf("%d/%d", a.SearchIdx+1, len(a.SearchHits))
		}
		l = append(l,
			styled("   /", fg(t.Colors.Dim)),
			styled(a.Search.Pattern, fg(t.Colors.Search)),
			styled("  "+position, fg(t.Colors.Dim)),
		)
		if a.FilterMatches {
			l = append(l, styled(fmt.Sprintf("  filtered ±%d", a.FilterContext), fg(t.Colors.Search)))
		}
	}

	state := l
	if len(state) > 0 {
		state = append(state, plain("   "))
	}
	state = append(state, statusChip(status, t), styled("   "+duration(elapsedOf(r)), fg(t.Colors.Dim)))
	if r.HasExit && r.Exit != 0 {
		state = append(state, styled(fmt.Sprintf("   exit %d", r.Exit), fg(t.Colors.StatusFailed)))
	}
	return a.header(r.Command(), state)
}

// runRowLines builds the rendered lines for one run row.
//
// splitting them would duplicate the indent, marker and highlight arithmetic.
//
//nolint:cyclop // a task row and a line row are two shapes with one gutter between them;
func (a *App) runRowLines(r *run.Run, row RunRow, gutter string, width int) []line {
	t := a.Theme
	if r == nil {
		return nil
	}
	width = max(0, width-utf8.RuneCountInString(gutter))

	if row.IsTask {
		tr, hasTask := r.Tasks[row.Name]
		status := run.Pending
		if hasTask {
			status = tr.Status
		}
		// Only offer a fold glyph where there is something to unfold. Filled means open,
		// hollow means ajar — the peek is a door left open a crack, and the glyph should
		// not claim you are looking at all of it.
		hasLines := hasTask && len(tr.Lines) > 0
		glyph := "  "
		if hasLines {
			switch row.Fold {
			case FoldHidden:
				glyph = t.Glyphs.FoldClosed + " "
			case FoldPeek:
				glyph = t.Glyphs.FoldPeek + " "
			case FoldFull:
				glyph = t.Glyphs.FoldOpen + " "
			}
		}

		nameStyle := fg(theme.Default)
		switch status {
		case run.Failed:
			nameStyle = fgBold(t.Colors.StatusFailed)
		case run.Skipped:
			nameStyle = fg(t.Colors.Dim)
		default:
			// Pending, running and finished-well all read as ordinary text; the glyph
			// beside the name is what carries the state.
		}

		l := line{
			styled(gutter, fg(t.Colors.Faint)),
			styled(glyph, fg(t.Colors.Faint)),
			styled(statusGlyph(status, t)+" ", fgBold(statusStyle(status, t))),
			styled(row.Name, nameStyle),
		}

		// The reason a task did not run belongs next to the task, not in the parent's output
		// where go-task printed it. `⇧R` is the answer to most of them, and the footer
		// already offers it.
		if hasTask && tr.Note != "" {
			l = append(l, styled("  "+tr.Note, fg(t.Colors.StatusSkipped)))
		}

		var tail strings.Builder
		if hasTask {
			// Closed, say how much there is; ajar, say how much is out of sight. "45
			// lines" next to five of them on screen reads as a contradiction.
			switch row.Fold {
			case FoldHidden:
				if len(tr.Lines) > 0 {
					fmt.Fprintf(&tail, "%d lines", len(tr.Lines))
				}
			case FoldPeek:
				if hidden := len(tr.Lines) - a.PeekLines; hidden > 0 {
					fmt.Fprintf(&tail, "%d more", hidden)
				}
			default:
				// Fully open: the lines are all on screen, so counting them would be
				// telling you what you can already see.
			}
			// Said out loud, open or closed: a buffer that has silently forgotten its
			// first hour is a buffer you would otherwise search and trust.
			if tr.Dropped > 0 {
				if tail.Len() > 0 {
					tail.WriteString("  ")
				}
				fmt.Fprintf(&tail, "%d earlier dropped", tr.Dropped)
			}
			// Ticking while the task runs, final once it stops. Sub-10ms figures are
			// dropped for the same reason `duration` refuses to print `0.00s`: a number
			// that is always zero is a column of noise, and here it is a column the eye
			// runs down looking for the slow step.
			if d, ok := tr.Elapsed(); ok && d >= tooFastToMatter {
				if tail.Len() > 0 {
					tail.WriteString("    ")
				}
				tail.WriteString(duration(d))
			}
		}
		// Right-anchored, so the whole column ends in the same place on every row.
		if tail.Len() > 0 {
			used := 0
			for _, sp := range l {
				used += utf8.RuneCountInString(sp.text)
			}
			gap := max(2, width+utf8.RuneCountInString(gutter)-used-utf8.RuneCountInString(tail.String()))
			l = append(l, plain(strings.Repeat(" ", gap)), styled(tail.String(), fg(t.Colors.Dim)))
		}
		return []line{l}
	}

	text, isCommand := "", false
	if tr, ok := r.Tasks[row.Task]; ok && row.Index < len(tr.Lines) {
		// Through CommandText, which takes go-task's `[test] ` off the echo: the task's
		// header is a few rows above, and restating it on every command it runs spends
		// fifteen columns of a build log saying what the indentation already says.
		text = run.CommandText(tr.Lines[row.Index])
		isCommand = tr.Lines[row.Index].IsCommand
	}

	// A command echo is a step, and what follows it is that step's output — so the two are
	// drawn as one thing. The echo carries its own verdict (▶ while it runs, ✓ once the
	// next one started, ✗ on the one that took the task down) and its output hangs off it
	// on a rail that closes at the last line. "Which step is this", "how did it go" and
	// "what did it print" are the three questions a build log gets read with, and the
	// first two used to be answerable only by finding where the output stopped.
	//
	// This reverses an earlier decision: output used to get no marker at all, on the
	// grounds that a `│` on every line is a column of chrome you stop seeing but keep
	// paying for. That was right while a command echo was only a label. It stopped being
	// right when the echo became a step with a status, because then which lines belong to
	// which step is a question worth being able to answer at a glance.
	rail, endsGroup := a.commandRail(r, row)
	marker := []span{styled(rail, fg(t.Colors.Faint)), plain("  ")}
	switch {
	case isCommand:
		st := run.Pending
		if tr, ok := r.Tasks[row.Task]; ok {
			st = tr.CommandStatus(row.Index)
		}
		marker = []span{
			styled(statusGlyph(st, t)+" ", fgBold(statusStyle(st, t))),
			styled(t.Glyphs.Command+" ", fg(t.Colors.Faint)),
		}
	case isFailure(text):
		marker = []span{
			styled(rail, fg(t.Colors.Faint)),
			styled(t.Glyphs.Warning+" ", fg(t.Colors.Faint)),
		}
	}
	// The marker column is measured in cells, whatever it is made of. `❯` is three bytes
	// and one column, and measuring it in bytes — as the Rust original did — wrapped
	// output two columns early and, worse, disagreed with runRowHeight, which always
	// assumed two. A line measured shorter than it renders is a line that overflows its
	// column.
	pad := gutterFor(r)
	room := max(8, width-(pad+1+markerCells))

	base := fg(theme.Default)
	switch {
	case isCommand:
		base = fg(t.Colors.Alias)
	case isFailure(text):
		base = fg(t.Colors.StatusFailed)
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
		l := line{styled(gutter, fg(t.Colors.Faint))}
		if n == 0 {
			l = append(l, styled(fmt.Sprintf("%*d ", pad, row.Index+1), fg(t.Colors.Faint)))
			l = append(l, marker...)
		} else {
			// Continuation: no line number, and the rail carries on down the wrapped rows
			// — except past the line that closed the group, where it has already ended.
			cont := "  "
			if rail != blankRail && !endsGroup {
				cont = t.Glyphs.GuideVertical + " "
			}
			l = append(l,
				plain(strings.Repeat(" ", pad+1)),
				styled(cont, fg(t.Colors.Faint)),
				plain("  "),
			)
		}

		// Highlight per chunk, so a match survives being wrapped. A search hit wins over a
		// file location: you went looking for the one and merely happened upon the other.
		if a.Search != nil {
			if start, end, ok := a.Search.FirstMatch(chunk); ok && end <= len(chunk) {
				l = append(l,
					styled(chunk[:start], base),
					styled(chunk[start:end], onBg(t.Colors.MatchFg, t.Colors.MatchBg)),
					styled(chunk[end:], base),
				)
				out = append(out, l)
				continue
			}
		}
		out = append(out, append(l, a.textWithLocations(chunk, base)...))
	}
	return out
}

// guides builds the tree guide for every row of a run: the column that says which task an
// output line came from, and which tasks are still to come below it.
//
// The geometry is what the plain two-spaces-per-depth indent already produced — the same
// columns, with a glyph in them — so nothing moves sideways by adopting it. The picker hands
// the result a prefix of its own, because there a run hangs under a row of the task tree and
// has to connect back to it.
func guides(rows []RunRow, g theme.Glyphs, prefix string) []string {
	// last[i] marks the task rows with no sibling below them: their branch closes, and
	// nothing under them carries a rail.
	last := make([]bool, len(rows))
	for i, row := range rows {
		if !row.IsTask {
			continue
		}
		last[i] = true
		for j := i + 1; j < len(rows); j++ {
			if rows[j].Depth < row.Depth {
				break
			}
			if rows[j].IsTask && rows[j].Depth == row.Depth {
				last[i] = false
				break
			}
		}
	}

	// carries[d] says whether the task drawn at depth d has something below it, and so
	// whether its rail runs on past the rows in between.
	carries := map[int]bool{}
	out := make([]string, len(rows))
	var b strings.Builder
	for i, row := range rows {
		if row.IsTask {
			carries[row.Depth] = !last[i]
		}
		b.Reset()
		b.WriteString(prefix)
		for d := 1; d < row.Depth; d++ {
			if carries[d] {
				b.WriteString(g.GuideVertical + " ")
			} else {
				b.WriteString("  ")
			}
		}
		switch {
		case row.Depth == 0:
			// The root, which has nothing above it to hang from.
		case row.IsTask && last[i]:
			b.WriteString(g.GuideLast + " ")
		case row.IsTask:
			b.WriteString(g.GuideBranch + " ")
		default:
			// An output line: its own depth column is blank, and the rail it hangs from is
			// already in the columns above.
			b.WriteString("  ")
		}
		out[i] = b.String()
	}
	return out
}

// markerCells is the width of a row's marker column: a status glyph and a `❯` on a command
// echo, the rail and a failure mark on everything else. One number rather than one per
// caller — the text width, the continuation indent and runRowHeight disagreeing is exactly
// how a line comes to be measured shorter than it renders.
const markerCells = 4

// blankRail is the rail column of a line no command claimed.
const blankRail = "  "

// commandRail is the guide tying an output line to the command above it: `│` while more of
// that command's output follows, `╰` on its last line, and nothing at all in a task whose
// output no command claimed — go-task's own messages, or a run restored from an archive
// written before the echo was kept.
//
// Read from the whole buffer rather than from what is on screen, so a peek window on the end
// of a command's output still says the lines belong to something above it.
func (a *App) commandRail(r *run.Run, row RunRow) (string, bool) {
	tr, ok := r.Tasks[row.Task]
	if !ok {
		return blankRail, false
	}
	switch under, last := tr.UnderCommand(row.Index); {
	case !under:
		return blankRail, false
	case last:
		return a.Theme.Glyphs.GuideLast + " ", true
	default:
		return a.Theme.Glyphs.GuideVertical + " ", false
	}
}

// tooFastToMatter is the point below which a duration is noise. `duration` already refuses
// to print `0.00s` for the same reason.
const tooFastToMatter = 10 * time.Millisecond

// gutterFor is how wide the line-number column has to be.
//
// Sized to the run rather than fixed at five: a run whose longest task printed nine lines
// numbers in one column, not five, and spends the other four on output. It is the run's
// widest task rather than each task's own width, because a gutter that changed between
// tasks would leave their output ragged against each other — and the column is there to be
// scanned down.
func gutterFor(r *run.Run) int {
	longest := 0
	if r != nil {
		for _, tr := range r.Tasks {
			longest = max(longest, len(tr.Lines))
		}
	}
	width := 1
	for longest >= 10 {
		longest /= 10
		width++
	}
	return width
}

// isFailure spots go-task's own report of what broke. The line is left exactly as it
// arrived — it is real output — but it does not have to look like ordinary output.
func isFailure(text string) bool {
	return strings.HasPrefix(text, "task: ") && strings.Contains(text, "Failed to run task")
}

// runRowHeight is how many terminal rows one run row occupies once wrapped.
func runRowHeight(r *run.Run, row RunRow, gutter string, width int) int {
	// A peeking line is always exactly one row. That is the whole contract of the window:
	// its height is known before its content is, so it cannot grow under you as output
	// arrives.
	if row.IsTask || row.Peek {
		return 1
	}
	text := ""
	if r != nil {
		if t, ok := r.Tasks[row.Task]; ok && row.Index < len(t.Lines) {
			text = t.Lines[row.Index].Plain
		}
	}
	prefix := utf8.RuneCountInString(gutter) + gutterFor(r) + 1 + markerCells
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
	gutters := guides(a.RunRows, a.Theme.Glyphs, "")
	single := 0
	for i, r := range a.RunRows {
		single += runRowHeight(a.Run, r, gutters[i], a.bodyWidth(width))
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
		heights[i] = runRowHeight(a.Run, r, gutters[i], a.bodyWidth(colWidth))
	}

	a.RunCursor = min(a.RunCursor, max(0, len(a.RunRows)-1))
	a.RunOffset = offsetForCursor(heights, a.RunCursor, height, columns)
	bounds := columnBounds(heights, a.RunOffset, height, columns)

	build := func(from, to int) [][]line {
		out := make([][]line, 0, to-from)
		for i := from; i < to; i++ {
			out = append(out, a.runRowLines(a.Run, a.RunRows[i], gutters[i], a.bodyWidth(colWidth)))
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

	state := a.pivotNames()
	// The order rides beside the grouping, and only when it is not the default: `⇧S` is a
	// key you press and forget, and a list sorted by what failed looks exactly like a list
	// that is broken unless something says why. The default says nothing, because every
	// pivot already names the order it is read in.
	if a.Order.By != pivot.ByNatural {
		state = append(state, styled("   by "+a.OrderLabel(), fg(t.Colors.Mode)))
	}

	if a.Query == "" {
		if a.InteractiveNext {
			state = append(state, styled("   interactive", fg(t.Colors.Interactive)))
		}
		if a.ForceNext {
			state = append(state, styled("   force", fg(t.Colors.Notice)))
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
			state = append(state, styled("   ▶ "+name+" running", fg(t.Colors.Notice)))
		default:
			state = append(state, styled(fmt.Sprintf("   ▶ %d running", n), fg(t.Colors.Notice)))
		}
		state = append(state, styled(fmt.Sprintf("   %d tasks", len(a.Tasks)), fg(t.Colors.Dim)))
	} else {
		state = append(state,
			styled("   /", fg(t.Colors.Dim)),
			styled(a.Query, fg(t.Colors.Search)),
			styled(fmt.Sprintf("   %d/%d tasks", shown, len(a.Tasks)), fg(t.Colors.Dim)),
		)
	}

	return a.header(dir, state)
}

// pivotNames lists the groupings with the active one accented.
//
// It replaces the header's `group: domain` and the footer's `p group by verb` at once: a
// control that shows its other positions is more use than either half on its own. With two
// pivots that was the whole list; with a config that can add more it stops fitting, so past
// a few it shows the one you are in and how many others there are — the header's right-hand
// block is right-anchored and a longer list would push the task count off the edge.
func (a *App) pivotNames() []span {
	t := a.Theme
	names := make([]string, 0, len(a.Pivots))
	for _, p := range a.Pivots {
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return nil
	}

	width := len(names) - 1
	for _, n := range names {
		width += utf8.RuneCountInString(n)
	}
	if width > pivotNamesBudget {
		return []span{
			styled(a.ModeLabel(), fgBold(t.Colors.Mode)),
			styled(fmt.Sprintf(" +%d", len(names)-1), fg(t.Colors.Faint)),
		}
	}

	var out []span
	for i, n := range names {
		if i > 0 {
			out = append(out, styled(t.Glyphs.Dot, fg(t.Colors.Faint)))
		}
		style := fg(t.Colors.Faint)
		if i == a.Pivot {
			style = fgBold(t.Colors.Mode)
		}
		out = append(out, styled(n, style))
	}
	return out
}

// pivotNamesBudget is how much of the header the grouping list may take before it is
// summarised. The block it sits in is right-anchored against the task count.
const pivotNamesBudget = 28

// nameColumn is where a task's description starts, on every row, always.
//
// A fixed column is the whole point. It used to be computed from whatever the row had
// already spent — so a task carrying an outcome badge pushed its description right, and no
// two rows lined up. There is nothing to scan down when the column moves.
const nameColumn = 17

// countWidth is the column a group's task count occupies, and which every other row leaves
// empty so the signals to its left stay in line.
const countWidth = 4

// frameWidth is the two columns the cursor's frame occupies — the lit edge on the left and
// its shade on the right — which every row builder has to leave for it.
//
// Both are reserved whatever the theme does with them, so a row is the same width in every
// look. Geometry that changed with the colours would be a theme that could break a layout.
const frameWidth = 2

// bodyWidth is how much of a row is content: everything the cursor's frame does not take,
// less whatever room the theme's jiggle needs to lean into.
//
// One function rather than the subtraction spelled out at each site, because the number has
// to be the same on both sides of the render: this is what decides where text wraps, and
// renderRow does the same arithmetic to decide where to draw it. The two disagreeing by one
// column is a row that wraps a word it then has space for.
func (a *App) bodyWidth(width int) int {
	return width - frameWidth - a.Theme.Animation.MaxLean()
}

// lastOfParent reports whether the row at i is the final child of whatever contains it, so
// the guide can be a corner rather than a tee. Without it every branch looks like it has a
// sibling below, including the ones that do not.
func (a *App) lastOfParent(i int) bool {
	return i+1 >= len(a.Rows) || a.Rows[i+1].Depth < a.Rows[i].Depth
}

// treeItem builds one row of the task tree: guide, label, description, signals.
// slotBadge is how a row reports the run in its slot: `▶` and a ticking clock while it is
// going, then the verdict and what it took.
//
// It outranks the archive's "how it went last time", which is what the column says when no
// slot holds this task. The run you started a minute ago is the more current answer to the
// same question — and until it is saved, the archive has never heard of it.
func (a *App) slotBadge(name string) ([]span, bool) {
	r := a.slotRun(name)
	if r == nil {
		return nil, false
	}
	t := a.Theme
	if !r.Finished() {
		return []span{
			styled(t.Glyphs.StatusRunning+" ", fgBold(t.Colors.StatusRunning)),
			styled(duration(time.Since(r.Started)), fg(t.Colors.Dim)),
		}, true
	}
	status := run.Failed
	if r.Exit == 0 {
		status = run.Ok
	}
	took := time.Since(r.Started)
	if r.HasDuration {
		took = r.Duration
	}
	return []span{
		styled(statusGlyph(status, t)+" ", fgBold(statusStyle(status, t))),
		styled(duration(took), fg(t.Colors.Dim)),
	}, true
}

// inlineFoldBadge is the fold glyph for a run unfolded under a task's row: how you know
// there is something under it at all, and which of the three states it is in. The
// vocabulary is the run view's own, so `▿` means a peek in both places.
func (a *App) inlineFoldBadge(name string) (span, bool) {
	fold, ok := a.InlineFold(name)
	if !ok {
		return span{}, false
	}
	g := a.Theme.Glyphs
	mark := g.FoldClosed
	switch fold {
	case FoldHidden:
		// Folded away; the closed glyph is what says there is something to open.
	case FoldPeek:
		mark = g.FoldPeek
	case FoldFull:
		mark = g.FoldOpen
	}
	return styled(mark+" ", fg(a.Theme.Colors.Faint)), true
}

func (a *App) treeItem(row pivot.Row, last bool, width int) []line {
	t := a.Theme
	node := a.Tree.Nodes[row.Node]

	// Tree guides, so depth is something you see rather than something you count.
	//
	// Two columns, always, whatever is in them — that is what keeps every label in the same
	// place. A group used to spend both on its fold marker and a space, at every depth, which
	// meant a nested group and a top-level one rendered identically: in a Taskfile with
	// `backend:migrate:*`, the row for `migrate` was indistinguishable from the row for
	// `deploy` beside it, and the tree stopped being a tree exactly where it got deep enough
	// to need to be one. A group below the top now spends the first column on its branch and
	// the second on its fold marker, so it sits with its siblings and still says it opens.
	g := t.Glyphs
	indent := strings.Repeat(g.GuideVertical+" ", max(0, row.Depth-1))
	branch := g.GuideBranch
	if last {
		branch = g.GuideLast
	}
	fold := g.FoldClosed
	if row.Open {
		fold = g.FoldOpen
	}

	glyph := "  "
	switch {
	case node.IsGroup() && row.Depth > 0:
		glyph = branch + fold
	case node.IsGroup():
		glyph = fold + " "
	case row.Depth > 0:
		glyph = branch + " "
	}

	labelStyle := fg(theme.Default)
	if node.IsGroup() {
		labelStyle = bold()
	}

	// A marked task takes its guide column for the mark. That column is one wide and always
	// there, so the mark costs no width and lands where the eye already runs down the list —
	// which is the whole reason to put it there rather than in the signal column, where it
	// would compete with how the task went last time.
	glyphStyle := fg(t.Colors.Faint)
	if node.Task != pivot.NoTask && a.IsMarked(a.Tasks[node.Task].Name) {
		glyph = g.Marked + " "
		glyphStyle = fgBold(t.Colors.Marked)
	}

	l := line{
		styled(indent, fg(t.Colors.Faint)),
		styled(glyph, glyphStyle),
		styled(node.Label, labelStyle),
	}
	used := max(1, row.Depth)*2 + utf8.RuneCountInString(node.Label)

	// Everything that is not content — the count, an alias, how it went — right-anchors
	// into a signal column against the edge, so all of it ends where the eye expects it.
	var signals line
	if node.IsGroup() {
		signals = append(signals, styled(fmt.Sprintf("%*d", countWidth, node.Count), fg(t.Colors.Dim)))
	}

	var extra []line

	if node.Task != pivot.NoTask {
		task := a.Tasks[node.Task]
		var badges line
		if len(task.Aliases) > 0 {
			badges = append(badges, styled(strings.Join(task.Aliases, ", "), fg(t.Colors.Alias)), plain("  "))
		}
		if task.Dangerous {
			badges = append(badges, styled(g.Danger+" ", fgBold(t.Colors.Danger)))
		}
		// Running now takes the column, because it is the more urgent of the two questions
		// and the only one the list could not previously answer: with runs parked in slots
		// you are not looking at, the footer could say "3 running" while every row in
		// front of you showed nothing but history.
		//
		// Failing that, how it went last time. A blank column means never run, which is
		// information too — it is not the same as having passed.
		if badge, ok := a.inlineFoldBadge(task.Name); ok {
			badges = append(badges, badge)
		}
		if slot, ok := a.slotBadge(task.Name); ok {
			badges = append(badges, slot...)
		} else if o, ok := a.Outcomes[task.Name]; ok {
			glyph, colour := g.StatusFailed+" ", t.Colors.StatusFailed
			if o.Ok {
				glyph, colour = g.StatusOk+" ", t.Colors.StatusOk
			}
			badges = append(badges, styled(glyph, fgBold(colour)), styled(ago(o.WhenUnix), fg(t.Colors.Dim)))
		}
		signals = append(badges, signals...)
		// Reserve the count's columns on a row that has no count, so that the ✓/✗ ends in the
		// same place whether or not this row is also a group. Without it the outcome sits four
		// columns further right on every leaf than on every namespace, and the column the eye
		// runs down looking for what is broken zigzags — which is most of what that column was
		// for. Only where there is something to align: a row showing no outcome has nothing to
		// put in the space and would rather spend it on its description.
		if len(badges) > 0 && !node.IsGroup() {
			signals = append(signals, plain(strings.Repeat(" ", countWidth)))
		}

		// Descriptions wrap into their own column rather than being cut off mid-word — a
		// truncated description is the half that does not tell you anything. Continuation
		// rows hang under the first, and carry the guide down with them.
		signalWidth := 0
		for _, sp := range signals {
			signalWidth += utf8.RuneCountInString(sp.text)
		}
		room := width - nameColumn - signalWidth - 2
		if task.Desc != "" && room >= 12 {
			chunks := wrap(task.Desc, room)
			// A label wider than the column it was given pushes its description sideways and
			// squeezes the signals off the end — which is how `✓ 9h ago` came out as `✓ 9h`.
			// The domain pivot never hits this, because its labels are single segments; the
			// verb and custom pivots show whole colon paths and hit it constantly. Where the
			// name does not fit, it keeps the row to itself and the description starts on the
			// next one, in the column it belongs to.
			first := 0
			if used <= nameColumn {
				l = append(l,
					plain(strings.Repeat(" ", max(1, nameColumn-used))),
					styled(chunks[0], fg(t.Colors.Dim)),
				)
				used = nameColumn + utf8.RuneCountInString(chunks[0])
				first = 1
			}
			// A wrapped description used to leave the guide column blank, which broke the
			// run of branches in half: the eye follows the vertical down the list, and a
			// task with a two-line description put a gap in it that read as the end of the
			// group. The guide continues instead — except on the last child, where there
			// is nothing below to connect to and a vertical would promise a sibling that
			// does not exist.
			cont := g.GuideVertical
			if last {
				cont = " "
			}
			prefix := indent + cont + " "
			for _, chunk := range chunks[first:] {
				extra = append(extra, line{
					styled(prefix, fg(t.Colors.Faint)),
					plain(strings.Repeat(" ", max(0, nameColumn-utf8.RuneCountInString(prefix)))),
					styled(chunk, fg(t.Colors.Dim)),
				})
			}
		}
	}

	if len(signals) > 0 {
		signalWidth := 0
		for _, sp := range signals {
			signalWidth += utf8.RuneCountInString(sp.text)
		}
		l = append(l, plain(strings.Repeat(" ", max(2, width-used-signalWidth))))
		l = append(l, signals...)
	}

	return append([]line{l}, extra...)
}

// drawTree lays the picker out in one column, whatever the width.
//
// A list can be columnised. A tree cannot: the columns fill sequentially, so a group header
// ends up in one column while its own children continue in the next, and the indentation —
// the only thing saying which task belongs to what — stops meaning anything the moment it
// wraps. The width goes to the description instead, which is where it does work.
func (a *App) drawTree(width, height int) []string {
	height = max(1, height)

	const columns = 1
	widths := columnWidths(width, columns)
	colWidth := widths[0]

	item := func(i int) []line {
		row := a.PickerRows[i]
		if !row.IsRun() {
			return a.treeItem(a.Rows[row.Tree], a.lastOfParent(row.Tree), a.bodyWidth(colWidth))
		}
		r := a.slotRun(row.Root)
		if r == nil {
			return []line{{plain("")}}
		}
		return a.runRowLines(r, row.Run, row.Rail, a.bodyWidth(colWidth))
	}

	heights := make([]int, len(a.PickerRows))
	for i, row := range a.PickerRows {
		if row.IsRun() {
			// Measured rather than rendered: a picker with a run unfolded under it is
			// mostly output rows, and every one of them is measured twice per frame.
			heights[i] = a.pickerRunHeight(row, a.bodyWidth(colWidth))
			continue
		}
		heights[i] = len(item(i))
	}

	a.Cursor = min(a.Cursor, max(0, len(a.PickerRows)-1))
	a.Offset = offsetForCursor(heights, a.Cursor, height, columns)
	bounds := columnBounds(heights, a.Offset, height, columns)

	build := func(from, to int) [][]line {
		out := make([][]line, 0, to-from)
		for i := from; i < to; i++ {
			out = append(out, item(i))
		}
		return out
	}
	return a.composeColumns(bounds, widths, colWidth, height, a.Cursor, build)
}

// pickerRunHeight is how many terminal rows one inline run row occupies, indentation
// included.
func (a *App) pickerRunHeight(row PickerRow, width int) int {
	r := a.slotRun(row.Root)
	if r == nil {
		return 1
	}
	return runRowHeight(r, row.Run, row.Rail, width)
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
			for li, l := range item {
				if at >= height {
					break
				}
				// len(item), not 1: this is the one place a row can be more than a line
				// tall, and the rail has to know so it does not animate itself into pieces.
				text := l.renderRow(colWidth, selected, a.Theme, a.Phase, li, len(item))
				if pad := widths[c] - colWidth; pad > 0 {
					if selected {
						text += selectionOf(
							lipgloss.NewStyle(),
							a.Theme.Colors.Selection,
						).Render(strings.Repeat(" ", pad))
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
		// The detached ones are the point of having detached them, and a prompt that only
		// counted what it was about to kill would be leaving out the good news.
		if n := pending.Detached; n > 0 {
			why = strings.TrimSuffix(why, "  ") +
				fmt.Sprintf(
					"  %s will keep running.  ",
					plural(n, "1 detached run", fmt.Sprintf("%d detached runs", n)),
				)
		}
	case ConfirmRunMarked:
		verb, does = " run ", " to run"
		subject = fmt.Sprintf("%d marked tasks", pending.Live)
		if pending.Live == 1 {
			subject = "1 marked task"
		}
		// Named, because the whole reason this is one question rather than several is that
		// the dangerous ones are in a batch with tasks that are not.
		why = "  —  " + pending.Name + " touches production.  "
	case ConfirmStopAll:
		verb, does = " stop ", " to stop"
		subject = fmt.Sprintf("all %d runs", pending.Live)
		if pending.Live == 1 {
			subject = "1 run"
		}
		why = "  —  including the ones you are not looking at.  "
	}

	return line{
		styled("  "+t.Glyphs.Danger+"  ", onBg(t.Colors.ConfirmFg, t.Colors.ConfirmBg)),
		styled(verb, fg(t.Colors.StatusFailed)),
		styled(subject, fgBold(t.Colors.StatusFailed)),
		styled(why, fg(t.Colors.StatusFailed)),
		styled("y", fgBold(t.Colors.StatusFailed)),
		styled(does+", anything else cancels", fg(t.Colors.Dim)),
	}, true
}

// hintGap is the space between one hint and the next, and before the pointer to the rest.
// Wide enough that two hints never read as one.
const hintGap = 3

// hintBar builds a footer of key hints that fits, and pins `? keys` to the right edge.
//
// The keys are accented and the labels are not, so the line reads as a row of controls
// rather than a paragraph of grey. It stops at a binding boundary: a hint you cannot finish
// reading — `t jump   s deta` — is worse than one that was never offered, and `?` already
// documents every last one of them.
func (a *App) hintBar(section *keys.Section) line {
	t := a.Theme
	const tail = "? keys"
	hints := keys.FooterHints(section)
	fits := keys.FooterFits(hints, a.Width-1, len(tail)+hintGap)

	l := line{plain(" ")}
	used := 1
	for i, b := range hints[:fits] {
		if i > 0 {
			l = append(l, plain("   "))
			used += 3
		}
		l = append(l, styled(b.Keys, fg(t.Colors.Accent)), plain(" "), styled(b.Footer, fg(t.Colors.Dim)))
		used += utf8.RuneCountInString(b.Keys) + 1 + utf8.RuneCountInString(b.Footer)
	}
	l = append(l, plain(strings.Repeat(" ", max(hintGap, a.Width-used-len(tail)-1))))
	return append(l, styled("?", fg(t.Colors.Accent)), styled(" keys", fg(t.Colors.Dim)))
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
		plain(" "),
		styled("task "+a.ArgsTarget+" ", fg(t.Colors.Accent)),
		plain(string(runes[:at])),
		styled(t.Glyphs.Cursor, fg(t.Colors.Accent)),
		plain(string(runes[at:])),
	}
	// A hint, not a default: the descriptions trail off into prose often enough that
	// pre-filling would hand you a subtly wrong command.
	if hint, ok := a.ArgsHint(); ok {
		l = append(l, styled("   e.g. "+hint, fg(t.Colors.Dim)))
	} else {
		l = append(l, styled("   ⏎ run   esc cancel", fg(t.Colors.Dim)))
	}
	return l, true
}

// markBar replaces the hints while a set is waiting: `⏎` means something different with
// marks set, and a footer that went on saying "run" would be describing the other keymap.
func (a *App) markBar() (line, bool) {
	n := len(a.marked)
	if n == 0 {
		return nil, false
	}
	t := a.Theme
	return line{
		plain(" "),
		styled(t.Glyphs.Marked+" ", fgBold(t.Colors.Marked)),
		styled(fmt.Sprintf("%d marked", n), fgBold(t.Colors.Marked)),
		styled("   ⏎ run them   m unmark   ⇧M clear", fg(t.Colors.Dim)),
	}, true
}

func (a *App) pickerFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Jumping {
		l := line{
			plain(" "),
			styled("jump: ", fg(t.Colors.Accent)),
			plain(a.JumpQuery),
			styled(t.Glyphs.Cursor, fg(t.Colors.Accent)),
		}
		if a.JumpQuery != "" {
			text := "   no match"
			if len(a.JumpMatches) > 0 {
				text = fmt.Sprintf("   %d/%d", a.JumpIdx+1, len(a.JumpMatches))
			}
			l = append(l, styled(text, fg(t.Colors.Dim)))
		}
		return append(l, styled("   ⇥ next   ⏎ stay   esc go back", fg(t.Colors.Dim)))
	}
	if l, ok := a.argsPrompt(); ok {
		return l
	}
	// After the prompts, before the status: a prompt owns the footer while it is open, and
	// a status message is transient where a set of marks is a state you are holding.
	if l, ok := a.markBar(); ok {
		return l
	}
	if a.Filtering {
		return line{
			plain(" "),
			styled("/", fg(t.Colors.Search)),
			plain(a.Query),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
			styled("   ⏎ accept   esc clear", fg(t.Colors.Dim)),
		}
	}
	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}

	// No pivot hint here any more: the header's `domain·verb` names both sides and which
	// one you are in, which is more than this line ever said.
	return a.hintBar(&keys.Picker)
}

func (a *App) runFooter() line {
	t := a.Theme

	if a.SendingInput {
		l := line{styled("  input  ", onBg(t.Colors.WarningFg, t.Colors.Interactive))}
		if prompt, ok := promptOf(a.Run); ok {
			l = append(l, styled(" "+prompt, fg(t.Colors.Interactive)))
		} else {
			l = append(l, styled(" keys go to the task", fg(t.Colors.Interactive)))
		}
		// The receipt: what has actually gone down the pipe. Without it, "I typed y and
		// nothing happened" cannot be told apart from "y never left the building".
		if a.Run != nil && a.Run.Sent != "" {
			l = append(l,
				styled("   sent: ", fg(t.Colors.Dim)),
				styled(a.Run.Sent, fgBold(t.Colors.Interactive)),
			)
		}
		// Typing works either way, but in a buffered run you are doing it blind.
		if a.Run != nil && !a.Run.Interactive {
			l = append(
				l,
				styled("   buffered: ⏎ sends a newline, output may lag   ⇧I re-runs visibly", fg(t.Colors.Notice)),
			)
		}
		return append(l, styled("   esc to stop typing", fg(t.Colors.Dim)))
	}

	// Under `prefixed` a blocked task emits nothing at all, so this is the only warning
	// available — otherwise it reads as an unusually slow build.
	if a.PossiblyStuck() {
		return line{
			styled("  …  ", onBg(t.Colors.WarningFg, t.Colors.WarningBg)),
			styled(" no output for a while", fg(t.Colors.Notice)),
			styled("   waiting for input?  i types at it   ⇧I re-runs so you can see it   x stops", fg(t.Colors.Dim)),
		}
	}

	// A task blocked on a question looks identical to a slow one; say which it is.
	if a.AwaitingInput() {
		prompt, _ := promptOf(a.Run)
		return line{
			styled("  ?  ", onBg(t.Colors.WarningFg, t.Colors.WarningBg)),
			styled(" "+prompt, fg(t.Colors.Notice)),
			styled("   i to answer   x to stop", fg(t.Colors.Dim)),
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
			plain(" "),
			styled(label, fg(t.Colors.Search)),
			plain(a.SearchInput),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
		}
		switch {
		case a.SearchError != "":
			l = append(l, styled("   "+a.SearchError, fg(t.Colors.StatusFailed)))
		case a.Search != nil:
			tasks := map[string]bool{}
			for _, h := range a.SearchHits {
				tasks[h.Task] = true
			}
			n := len(a.SearchHits)
			l = append(l, styled(fmt.Sprintf("   %d %s in %d %s",
				n, plural(n, "match", "matches"),
				len(tasks), plural(len(tasks), "task", "tasks")), fg(t.Colors.Dim)))
		}
		return append(l, styled("   ⏎ keep   esc clear", fg(t.Colors.Dim)))
	}

	if a.Status != "" {
		return line{plain(" "), styled(a.Status, fg(t.Colors.Notice))}
	}
	return a.hintBar(&keys.Run)
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

// relativeTo shortens a path for display when it is inside the project, and leaves it alone
// when it is not. An absolute path repeated on every status line is mostly prefix.
func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

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
