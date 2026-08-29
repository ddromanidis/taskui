package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/theme"
)

func viewSample(t *testing.T) *App {
	t.Helper()
	tasks := pivot.Fixture([]string{
		"all",
		"lint",
		"app:lint",
		"backend:lint",
		"backend:migrate",
		"backend:migrate:prod",
	})
	tasks[5].Dangerous = true
	tasks[1].Desc = "Lint all source code"
	a := New(tasks, "/tmp/atlas")
	a.SetStateDir(t.TempDir())
	return a
}

func manyTasks(t *testing.T, n int) *App {
	t.Helper()
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf("task%03d", i))
	}
	a := New(pivot.Fixture(names), "/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.SetFoldAll(true)
	return a
}

func find(lines []string, want string) (string, bool) {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return l, true
		}
	}
	return "", false
}

// The header names both halves of the pivot and accents the one you are in, which is what
// let the footer stop saying `p group by verb`.
func TestHeaderNamesBothPivotsAndAccentsTheActiveOne(t *testing.T) {
	a := viewSample(t)
	lines := a.RenderHeadless(70, 12)
	for _, want := range []string{"taskui", "atlas", "domain·verb·file"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q is missing %q", lines[0], want)
		}
	}

	// The active one is accented and the others are not; the styles are built from the
	// list, so comparing them is comparing what the header actually renders.
	styleOf := func(name string) lipgloss.Style {
		t.Helper()
		for i, sp := range a.pivotNames() {
			if sp.text == name {
				return a.pivotNames()[i].style
			}
		}
		t.Fatalf("no span for %q", name)
		return lipgloss.NewStyle()
	}
	active, other := styleOf("domain"), styleOf("verb")
	if active.GetForeground() == other.GetForeground() {
		t.Error("the active grouping should not look like the rest")
	}

	a.ToggleMode()
	if styleOf("verb").GetForeground() != active.GetForeground() {
		t.Error("pivoting should move the accent")
	}
	if !strings.Contains(a.RenderHeadless(70, 12)[0], "domain·verb·file") {
		t.Error("and every grouping stays named")
	}
}

// `taskui · taskui` — the wordmark, then a directory that happens to share its name — is a
// row spent saying the same thing twice.
func TestTheWordmarkIsNotRepeatedAsTheProject(t *testing.T) {
	a := appWith(t, []string{"all"})
	a.Root = "/src/taskui"
	if got := a.RenderHeadless(70, 12)[0]; strings.Count(got, "taskui") != 1 {
		t.Errorf("header = %q", got)
	}

	a.Root = "/src/atlas"
	if got := a.RenderHeadless(70, 12)[0]; !strings.Contains(got, "taskui ▸ atlas") {
		t.Errorf("header = %q", got)
	}
}

// Collapsed groups show a closed glyph and a subtree count; opening flips it.
func TestGroupsRenderFoldGlyphAndCount(t *testing.T) {
	a := viewSample(t)
	lines := a.RenderHeadless(70, 12)
	if !strings.Contains(lines[2], "▸ (root)") {
		t.Errorf("row = %q", lines[2])
	}
	if !strings.Contains(lines[2], "2") {
		t.Errorf("root holds all + lint: %q", lines[2])
	}

	a.SetFoldAll(true)
	lines = a.RenderHeadless(70, 12)
	if !strings.Contains(lines[2], "▾ (root)") {
		t.Errorf("row = %q", lines[2])
	}
	if !strings.Contains(lines[3], "all") {
		t.Errorf("row = %q", lines[3])
	}
}

// With one run open the run view looks exactly as it always did; the bar only earns its
// line once there is somewhere to switch to.
func TestTheSlotBarAppearsOnlyOnceASecondRunIsOpen(t *testing.T) {
	a := viewSample(t)
	a.Screen = ScreenRun
	a.OpenRunForTest(run.Detached("up", run.GraphFrom(run.Edge{Parent: "up"})))

	lines := a.RenderHeadless(70, 12)
	if strings.Contains(lines[1], "1 ") {
		t.Errorf("one run, no bar: %q", lines[1])
	}

	a.OpenRunForTest(run.Detached("test", run.GraphFrom(run.Edge{Parent: "test"})))
	lines = a.RenderHeadless(70, 12)
	if !strings.Contains(lines[1], "up") || !strings.Contains(lines[1], "test") {
		t.Fatalf("both should be listed: %q", lines[1])
	}
	if strings.Index(lines[1], "up") > strings.Index(lines[1], "test") {
		t.Errorf("in the order they were started: %q", lines[1])
	}
}

func TestTasksShowDescriptionsAndDangerMarker(t *testing.T) {
	a := viewSample(t)
	a.SetFoldAll(true)
	lines := a.RenderHeadless(70, 20)

	lint, ok := find(lines, "Lint all")
	if !ok || !strings.Contains(lint, "Lint all source code") {
		t.Errorf("description row = %q", lint)
	}
	prod, ok := find(lines, "prod")
	if !ok || !strings.Contains(prod, "⚠") {
		t.Errorf("production tasks should be marked: %q", prod)
	}
}

// The picker marks what is running right now, in a slot you may not be looking at.
func TestThePickerMarksATaskThatIsRunningNow(t *testing.T) {
	a := viewSample(t)
	a.SetFoldAll(true)
	a.Run = run.Detached("backend:migrate", run.GraphFrom(run.Edge{Parent: "backend:migrate"}))

	lines := a.RenderHeadless(70, 20)
	// Not the footer, which says "▶ <name> running" and would pass this on its own.
	row := ""
	for _, l := range lines {
		if strings.Contains(l, "migrate") && !strings.Contains(l, "prod") && !strings.Contains(l, "running") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("the running task has no row: %#v", lines)
	}
	if !strings.Contains(row, "▶") {
		t.Errorf("it should be marked as running: %q", row)
	}

	// And only that one: its sibling is not running, and must not claim to be.
	lint, _ := find(lines, "lint")
	if strings.Contains(lint, "▶") {
		t.Errorf("row = %q", lint)
	}
}

// Once it finishes, the row goes back to reporting how it went.
func TestAFinishedTaskStopsClaimingToBeRunning(t *testing.T) {
	a := viewSample(t)
	a.SetFoldAll(true)
	r := run.Detached("backend:migrate", run.GraphFrom(run.Edge{Parent: "backend:migrate"}))
	r.Finish(0)
	a.Run = r

	for _, l := range a.RenderHeadless(70, 20) {
		if strings.Contains(l, "▶") {
			t.Errorf("nothing is running: %q", l)
		}
	}
}

// Columns appear only when the content does not fit, and never more than the width can
// carry.
func TestRendersAtOneTwoAndThreeColumns(t *testing.T) {
	a := manyTasks(t, 80)
	for _, size := range [][2]int{{60, 30}, {100, 12}, {150, 12}} {
		lines := a.RenderHeadless(size[0], size[1])
		if _, ok := find(lines, "task0"); !ok {
			t.Errorf("%dx%d rendered nothing", size[0], size[1])
		}
	}
}

// The window follows the cursor down a long list and comes back again.
func TestTheWindowFollowsTheCursor(t *testing.T) {
	a := manyTasks(t, 60)
	w, h := 150, 12

	first := a.RenderHeadless(w, h)
	if !strings.Contains(first[2], "(root)") {
		t.Errorf("should start at the top: %q", first[2])
	}

	a.Cursor = 40
	paged := a.RenderHeadless(w, h)
	if strings.Contains(paged[2], "(root)") {
		t.Errorf("the window should have moved to follow the cursor: %q", paged[2])
	}
	label := a.Tree.Nodes[a.Rows[a.Cursor].Node].Label
	if _, ok := find(paged, label); !ok {
		t.Errorf("the cursor's row %q should be on screen", label)
	}

	// …and back.
	a.Cursor = 0
	if home := a.RenderHeadless(w, h); !strings.Contains(home[2], "(root)") {
		t.Errorf("row = %q", home[2])
	}
}

// A tree cannot be columnised: its columns fill sequentially, so a group header ends in one
// column while its own children continue in the next.
func TestTheTreeIsNeverSplitIntoColumns(t *testing.T) {
	a := manyTasks(t, 200)
	for _, w := range []int{100, 150, 300} {
		lines := a.RenderHeadless(w, 30)
		for _, l := range lines[2 : len(lines)-2] {
			if n := strings.Count(l, "task"); n > 1 {
				t.Errorf("%d wide: two tasks on one row: %q", w, l)
			}
		}
	}
}

// Typing a filter reshapes the list under the layout on every keystroke.
func TestFilteringRendersOnEveryKeystroke(t *testing.T) {
	a := manyTasks(t, 80)
	w, h := 150, 12

	for _, c := range "task01" {
		a.PushQuery(c)
		lines := a.RenderHeadless(w, h)
		if !strings.Contains(lines[0], "/"+a.Query) {
			t.Errorf("header %q does not show the query %q", lines[0], a.Query)
		}
	}
	if len(a.Rows) >= 20 {
		t.Errorf("should have narrowed: %d rows", len(a.Rows))
	}
}

// Columns fill by accumulated height, because a wrapped output line is several rows.
func TestRunColumnsFillByHeightNotByCount(t *testing.T) {
	// Second row is a wrapped line worth three terminal rows.
	heights := []int{1, 3, 1, 1, 1, 1, 1, 1}
	bounds := columnBounds(heights, 0, 5, 2)
	if bounds[0] != [2]int{0, 3} {
		t.Errorf("1 + 3 fills five rows; the next would overflow: %v", bounds[0])
	}
	if bounds[1] != [2]int{3, 8} {
		t.Errorf("the rest continues in the second column: %v", bounds[1])
	}
}

// A row taller than the whole column still has to be shown somewhere.
func TestAnOversizedRowStillGetsAColumn(t *testing.T) {
	if got := columnBounds([]int{9}, 0, 4, 2)[0]; got != [2]int{0, 1} {
		t.Errorf("got %v", got)
	}
}

// The cursor is kept centred rather than pinned to the bottom edge, so you can read around
// the row you are on.
func TestTheCursorIsKeptCentred(t *testing.T) {
	heights := make([]int, 100)
	for i := range heights {
		heights[i] = 1
	}
	// Deep in the list: roughly half a column of context above.
	if got := offsetForCursor(heights, 50, 10, 1); got != 45 {
		t.Errorf("five rows above, four below: %d", got)
	}
	// Near the top there is nothing to scroll to, so it stays put.
	if got := offsetForCursor(heights, 2, 10, 1); got != 0 {
		t.Errorf("got %d", got)
	}
	// Near the bottom it stops rather than leaving blank space below the last row.
	if got := offsetForCursor(heights, 99, 10, 1); got != 90 {
		t.Errorf("got %d", got)
	}
}

// A list that fits never scrolls at all.
func TestAShortListDoesNotScroll(t *testing.T) {
	heights := []int{1, 1, 1, 1, 1, 1}
	for cursor := range 6 {
		if got := offsetForCursor(heights, cursor, 10, 1); got != 0 {
			t.Errorf("cursor %d: offset %d", cursor, got)
		}
	}
}

// With columns, "everything left fits" means across all of them.
func TestTheEndClampAccountsForEveryColumn(t *testing.T) {
	heights := make([]int, 40)
	for i := range heights {
		heights[i] = 1
	}
	// 3 columns of 10 hold the last 30 rows, so it stops at 10.
	if got := offsetForCursor(heights, 39, 10, 3); got != 10 {
		t.Errorf("got %d", got)
	}
}

// Render every screen and every prompt across a spread of terminal shapes.
//
// Sizes here are deliberately awkward: one row of body, a terminal narrower than a task
// name, and widths either side of every column threshold.
func TestEveryScreenRendersAtEveryAwkwardSize(t *testing.T) {
	sizes := [][2]int{
		{20, 3}, // barely a terminal
		{40, 4},
		{60, 3},   // one body row
		{80, 24},  // ordinary
		{90, 5},   // wide and very short
		{100, 12}, // two columns
		{150, 12}, // three columns
		{200, 8},
		{300, 60}, // absurdly wide
	}

	a := manyTasks(t, 60)
	a.SetFoldAll(true)
	a.DetailOf = "task000"
	a.Detail = graph.Detail{
		Summary:      []string{"A description"},
		Requires:     []string{"NAME"},
		Dependencies: []string{"task001"},
		Commands:     []string{"Task: task001", "echo hello"},
	}
	a.Run = run.Detached("task000", run.GraphFrom(run.Edge{Parent: "task000", Children: []string{"task001"}}))
	a.Run.Feed("task001", "a line of output long enough that it has to wrap somewhere sensible")
	a.Run.Feed("task001", "error: boom")
	a.RebuildRunRows()
	// A second slot, so the run view is exercised with the slot bar taking a line off the
	// body — the sizes below include terminals with barely any body to take from.
	a.OpenRunForTest(run.Detached("task002", run.GraphFrom(run.Edge{Parent: "task002"})))

	// The timeline and the diff are lists like the others, and have to survive the same
	// shapes — a diff row is three columns before it gets to any text, which on a
	// twenty-column terminal is most of it.
	a.TimelineOf = "task001"
	a.Timeline = []store.Point{
		{RunID: "a", Root: "task000", WhenUnix: 1, Status: "Ok", DurationMs: 1200, Lines: 30},
		{RunID: "b", Root: "task001", WhenUnix: 2, Status: "Failed", DurationMs: 90, Lines: 4},
	}
	a.showDiff(
		"task001",
		[]string{"shared", "gone", "also shared"},
		[]string{"shared", "arrived at internal/app/view.go:212:5", "also shared"},
		"when it last passed", a.Timeline[0],
	)
	// showDiff leaves the app on the diff screen; the loop below sets it per iteration.
	a.Screen = ScreenPicker

	// The profile is a list like the others, and a mark held in the picker changes a glyph
	// and takes over the footer — both have to survive the same shapes.
	a.ProfileRows = a.Profile()
	a.marked = map[string]bool{"task001": true, "task002": true}

	screens := []Screen{
		ScreenPicker, ScreenRun, ScreenHistory, ScreenHelp, ScreenDetail,
		ScreenTimeline, ScreenDiff, ScreenProfile,
	}
	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, screen := range screens {
			a.Screen = screen
			lines := a.RenderHeadless(w, h)
			if len(lines) != h {
				t.Fatalf("screen %v at %dx%d rendered %d lines", screen, w, h, len(lines))
			}

			// …and again with each prompt open, since prompts take over the footer.
			a.EnteringArgs = true
			a.ArgsTarget = "task000"
			a.RenderHeadless(w, h)
			a.EnteringArgs = false

			a.Searching = true
			a.RenderHeadless(w, h)
			a.Searching = false

			a.Jumping = true
			a.RenderHeadless(w, h)
			a.Jumping = false

			a.SendingInput = true
			a.RenderHeadless(w, h)
			a.SendingInput = false

			// Every shape of the confirmation bar, since each builds its own line.
			for _, pending := range []*Confirm{
				{Kind: ConfirmRun, Name: "deploy:backend", Args: []string{"--force"}, Reason: TouchesProduction},
				{Kind: ConfirmQuit, Live: 3},
				{Kind: ConfirmStopAll, Live: 1},
			} {
				a.Confirm = pending
				a.RenderHeadless(w, h)
			}
			a.Confirm = nil
		}
	}
}

// Whatever the cursor is on has to be visible. Scrolling far enough was hiding it.
func TestTheCursorRowIsAlwaysOnScreen(t *testing.T) {
	for _, mode := range []string{pivot.DomainName, pivot.VerbName} {
		a := manyTasks(t, 80)
		a.SetPivot(mode)
		a.SetFoldAll(true)

		for _, size := range [][2]int{{80, 10}, {100, 12}, {150, 12}, {150, 24}} {
			w, h := size[0], size[1]
			for cursor := range len(a.Rows) {
				a.Cursor = cursor
				label := a.Tree.Nodes[a.Rows[cursor].Node].Label
				if _, ok := find(a.RenderHeadless(w, h), label); !ok {
					t.Fatalf("%v %dx%d: cursor %d is on %q, which is not on screen", mode, w, h, cursor, label)
				}
			}
		}
	}
}

// The selection is only drawn by the column that contains the cursor, so the cursor has to
// fall inside one of the column ranges.
func TestTheCursorFallsInsideAColumn(t *testing.T) {
	uniform := func(n, h int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = h
		}
		return out
	}
	mixed := uniform(120, 1)
	for i := range mixed {
		if i%7 == 0 {
			mixed[i] = 4
		}
	}

	for _, heights := range [][]int{uniform(200, 1), uniform(60, 3), mixed} {
		for _, columns := range []int{1, 2, 3} {
			for _, height := range []int{4, 10, 22} {
				for cursor := range heights {
					offset := offsetForCursor(heights, cursor, height, columns)
					bounds := columnBounds(heights, offset, height, columns)
					if !within(bounds, cursor) {
						t.Fatalf("cursor %d outside %v (offset %d, %d cols of %d)",
							cursor, bounds, offset, columns, height)
					}
				}
			}
		}
	}
}

// Cursors out of step with the rows must not take the renderer down either.
func TestAnOutOfRangeCursorDoesNotPanic(t *testing.T) {
	a := manyTasks(t, 10)
	a.SetFoldAll(true)
	a.Cursor = 9_999
	a.RenderHeadless(80, 10)

	a.Screen = ScreenHistory
	a.HistoryCursor = 9_999
	a.RenderHeadless(80, 10)
}

func TestDurationsReadAtAGlance(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{4 * time.Millisecond, "4ms"},
		{940 * time.Millisecond, "940ms"},
		{1500 * time.Millisecond, "1.5s"},
		{59 * time.Second, "59.0s"},
		{134 * time.Second, "2m14s"},
		{3600 * time.Second, "60m00s"},
	} {
		if got := duration(c.d); got != c.want {
			t.Errorf("duration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// Descriptions wrap into their column instead of being cut off mid-word. They used to be
// suppressed in a narrow column because a truncated description is noise — wrapping
// removes the reason for that.
func TestDescriptionsWrapRatherThanTruncate(t *testing.T) {
	tasks := pivot.Fixture([]string{"alpha"})
	tasks[0].Desc = "A description long enough that it cannot possibly fit on one line"
	a := New(tasks, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.SetFoldAll(true)

	narrow := strings.Join(a.RenderHeadless(56, 10), "\n")
	for _, want := range []string{"alpha", "A description", "one line"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("missing %q in:\n%s", want, narrow)
		}
	}
}

// A wrapped description makes its row taller, and the layout has to know that.
func TestAWrappedDescriptionMakesItsRowTaller(t *testing.T) {
	build := func(desc string) *App {
		tasks := []task.Task{{Name: "alpha", Desc: desc}}
		a := New(tasks, "/tmp/repo")
		a.SetStateDir(t.TempDir())
		a.SetFoldAll(true)
		return a
	}
	height := func(a *App) int {
		for _, r := range a.Rows {
			if a.Tree.Nodes[r.Node].Task != pivot.NoTask {
				return len(a.treeItem(r, false, 56))
			}
		}
		t.Fatal("no task row")
		return 0
	}

	short := height(build("Short"))
	long := height(build("A description long enough that it cannot possibly fit on one line"))
	if short != 1 {
		t.Errorf("short row = %d", short)
	}
	if long <= 1 {
		t.Errorf("long row should be taller: %d", long)
	}
}

// The footer stops at a binding boundary and always keeps room for the pointer to the rest.
// It used to be built at full length and clipped by the renderer, which ended it mid-word.
func TestTheFooterNeverEndsMidBinding(t *testing.T) {
	a := manyTasks(t, 20)
	for _, w := range []int{30, 40, 56, 70, 92, 150, 300} {
		lines := a.RenderHeadless(w, 14)
		footer := strings.TrimSpace(lines[len(lines)-1])
		if !strings.HasSuffix(footer, "? keys") {
			t.Errorf("%d wide: footer %q loses the pointer to the full keymap", w, footer)
		}
		whole := map[string]bool{"? keys": true}
		for _, hint := range keys.FooterHints(&keys.Picker) {
			whole[hint.Keys+" "+hint.Footer] = true
		}
		// Every piece the footer offers is a complete hint, never a prefix of one.
		for piece := range strings.SplitSeq(footer, "   ") {
			if piece = strings.TrimSpace(piece); piece != "" && !whole[piece] {
				t.Errorf("%d wide: %q is not a whole binding, in %q", w, piece, footer)
			}
		}
	}
}

// Every description starts in the same column, whatever badges the row is carrying. It used
// to start wherever the row before it happened to end.
func TestDescriptionsAllStartInTheSameColumn(t *testing.T) {
	tasks := pivot.Fixture([]string{"alpha", "beta", "gamma"})
	for i := range tasks {
		tasks[i].Desc = "A description"
	}
	tasks[1].Aliases = []string{"b"}
	tasks[2].Dangerous = true

	a := New(tasks, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.SetFoldAll(true)
	a.Outcomes = map[string]store.Outcome{"gamma": {Ok: false, WhenUnix: 1}}

	at := -1
	for _, l := range a.RenderHeadless(80, 12) {
		i := strings.Index(l, "A description")
		if i < 0 {
			continue
		}
		if at >= 0 && i != at {
			t.Errorf("description column moved from %d to %d: %q", at, i, l)
		}
		at = i
	}
	if at < 0 {
		t.Fatal("no descriptions rendered")
	}
}

// The cursor rail marks the selected row, and only that row.
func TestTheRailMarksTheCursorRow(t *testing.T) {
	a := manyTasks(t, 20)
	a.Cursor = 3
	lines := a.RenderHeadless(80, 16)
	railed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, a.Theme.Glyphs.Rail) {
			railed++
		}
	}
	if railed != 1 {
		t.Errorf("%d rows carry the rail, want exactly 1", railed)
	}
	if !strings.HasPrefix(lines[2+3], a.Theme.Glyphs.Rail) {
		t.Errorf("the rail is not on the cursor's row: %q", lines[2+3])
	}
}

// A wrapped description used to leave the guide column blank, which put a gap in the run of
// branches that read as the end of the group.
func TestAWrappedDescriptionCarriesTheGuideDown(t *testing.T) {
	long := "A description long enough that it has to wrap onto a second line"
	tasks := pivot.Fixture([]string{"group:first", "group:second"})
	for i := range tasks {
		tasks[i].Desc = long
	}
	a := New(tasks, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.SetFoldAll(true)

	lines := a.RenderHeadless(56, 12)
	guide := a.Theme.Glyphs.GuideVertical

	var continuations []string
	for _, l := range lines {
		if strings.Contains(l, "wrap onto") || strings.Contains(l, "second line") {
			continuations = append(continuations, l)
		}
	}
	if len(continuations) != 2 {
		t.Fatalf("expected one continuation per task, got %d: %#v", len(continuations), lines)
	}

	// `first` has a sibling below it, so its guide continues.
	if !strings.HasPrefix(strings.TrimPrefix(continuations[0], " "), guide) {
		t.Errorf("the guide should carry down: %q", continuations[0])
	}
	// `second` is the last child — a vertical there would promise a sibling that does not
	// exist.
	if strings.Contains(continuations[1], guide) {
		t.Errorf("the last child should not carry a guide: %q", continuations[1])
	}
}

// The continuation hangs under the description, not under the guide.
func TestAWrappedDescriptionStaysInItsColumn(t *testing.T) {
	tasks := pivot.Fixture([]string{"group:one"})
	tasks[0].Desc = "A description long enough that it has to wrap onto a second line"
	a := New(tasks, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.SetFoldAll(true)

	// Columns, not byte offsets: a guide glyph is three bytes and one column, and the row
	// with the description on it has one in front while its continuation does not.
	column := func(l string, at int) int { return utf8.RuneCountInString(l[:at]) }
	textStarts := func(l string) int {
		at := strings.IndexFunc(l, func(r rune) bool {
			return r != ' ' && r != []rune(a.Theme.Glyphs.GuideVertical)[0]
		})
		if at < 0 {
			return -1
		}
		return column(l, at)
	}

	first, second := -1, -1
	for _, l := range a.RenderHeadless(56, 10) {
		if i := strings.Index(l, "A description"); i >= 0 {
			first = column(l, i)
			continue
		}
		if first >= 0 && second < 0 && strings.Contains(l, "line") {
			second = textStarts(l)
		}
	}
	if first < 0 || second < 0 {
		t.Fatal("the description did not wrap")
	}
	if first != second {
		t.Errorf("continuation starts at column %d, the first line at %d", second, first)
	}
}

// A tree that stops distinguishing depth is not a tree. `backend:migrate` and `deploy` are
// at different levels and used to render byte-identically: both spent their two columns on
// a fold marker and a space, so the only thing that said one was nested was the row above
// it — which scrolls away.
func TestANestedGroupDoesNotLookLikeATopLevelOne(t *testing.T) {
	a := appWith(t, []string{
		"backend:migrate:up", "backend:migrate:down", "backend:check", "deploy:prod",
	})
	a.SetFoldAll(true)
	lines := a.RenderHeadless(100, 20)

	find := func(label string) string {
		t.Helper()
		for _, l := range lines {
			if strings.Contains(l, label) {
				return l
			}
		}
		t.Fatalf("no row for %q in:\n%s", label, strings.Join(lines, "\n"))
		return ""
	}

	// `migrate` is inside `backend`; `deploy` is beside it.
	nested := find("migrate")
	top := find("deploy")
	prefix := func(l, label string) string {
		before, _, _ := strings.Cut(l, label)
		return before
	}

	if prefix(nested, "migrate") == prefix(top, "deploy") {
		t.Errorf("a nested group and a top-level one share a prefix %q", prefix(top, "deploy"))
	}
	// It still says it opens, and it still hangs off a branch.
	if !strings.Contains(nested, a.Theme.Glyphs.GuideBranch) && !strings.Contains(nested, a.Theme.Glyphs.GuideLast) {
		t.Errorf("a nested group has no branch: %q", nested)
	}
	if !strings.Contains(nested, a.Theme.Glyphs.FoldClosed) && !strings.Contains(nested, a.Theme.Glyphs.FoldOpen) {
		t.Errorf("a nested group has no fold marker: %q", nested)
	}
}

// Depth still costs the same two columns per level, whatever is in them — every label in a
// group lines up or the tree reads as ragged.
func TestSiblingsShareALabelColumnWhateverTheyAre(t *testing.T) {
	a := appWith(t, []string{"backend:check", "backend:migrate:up", "backend:lint"})
	a.SetFoldAll(true)
	lines := a.RenderHeadless(100, 20)

	at := func(label string) int {
		t.Helper()
		for _, l := range lines {
			if before, _, ok := strings.Cut(l, label); ok {
				return utf8.RuneCountInString(before)
			}
		}
		t.Fatalf("no row for %q", label)
		return -1
	}
	// `check` is a leaf, `migrate` a group; both are children of `backend`.
	if leaf, group := at("check"), at("migrate"); leaf != group {
		t.Errorf("sibling labels start at %d and %d", leaf, group)
	}
}

// The ✓/✗ column is there to be run down looking for what is broken. It used to sit four
// columns further right on a leaf than on a namespace, because the namespace's task count
// took the edge and pushed everything else left — so the column zigzagged.
func TestTheOutcomeColumnDoesNotMoveForAGroupsCount(t *testing.T) {
	// `backend:migrate` is both a runnable task and a namespace — the shape `fmt` and `lint`
	// have in a real Taskfile, and the only shape where the two columns can collide.
	a := appWith(t, []string{
		"backend:check", "backend:migrate", "backend:migrate:up", "backend:migrate:down",
	})
	a.SetFoldAll(true)
	a.Outcomes = map[string]store.Outcome{
		"backend:check":   {Ok: true, WhenUnix: time.Now().Add(-time.Hour).Unix()},
		"backend:migrate": {Ok: true, WhenUnix: time.Now().Add(-time.Hour).Unix()},
	}
	lines := a.RenderHeadless(100, 20)

	tick := a.Theme.Glyphs.StatusOk
	var columns []int
	for _, l := range lines {
		if before, _, ok := strings.Cut(l, tick); ok {
			columns = append(columns, utf8.RuneCountInString(before))
		}
	}
	if len(columns) < 2 {
		t.Fatalf("expected two outcomes, found %d in:\n%s", len(columns), strings.Join(lines, "\n"))
	}
	for _, c := range columns[1:] {
		if c != columns[0] {
			t.Errorf("outcome glyphs at columns %v — they should share one", columns)
			break
		}
	}
}

// A label wider than the column it was given used to push its description sideways and
// squeeze the signals off the end — `✓ 9h ago` came out as `✓ 9h`. The domain pivot never
// hits it; the verb and custom pivots show whole colon paths and hit it constantly.
func TestALongLabelKeepsItsRowAndItsSignals(t *testing.T) {
	a := appWith(t, []string{"backend:migrate:control:check", "short"})
	a.Tasks[0].Desc = "Control: history integrity and drift proof"
	a.Tasks[1].Desc = "A short one"
	a.Outcomes = map[string]store.Outcome{
		"backend:migrate:control:check": {Ok: true, WhenUnix: time.Now().Add(-9 * time.Hour).Unix()},
	}
	a.SetPivot(pivot.VerbName)
	a.SetFoldAll(true)
	lines := a.RenderHeadless(96, 20)
	page := strings.Join(lines, "\n")

	// The signal survives in full rather than being clipped by the overhanging name.
	if !strings.Contains(page, "9h ago") {
		t.Errorf("the outcome was squeezed off the end:\n%s", page)
	}
	// The long name's own row carries no description; the description is on the next one.
	for i, l := range lines {
		if !strings.Contains(l, "backend:migrate:control:check") {
			continue
		}
		if strings.Contains(l, "Control: history") {
			t.Errorf("the description is still crammed onto the label's row: %q", l)
		}
		if i+1 < len(lines) && !strings.Contains(lines[i+1], "Control: history") {
			t.Errorf("the description did not move to the next row: %q", lines[i+1])
		}
		break
	}
}

// Wherever it lands, a description starts in the same column. That is the whole reason the
// column exists.
func TestEveryDescriptionStartsInOneColumn(t *testing.T) {
	a := appWith(t, []string{"backend:migrate:control:check", "api:lint", "short"})
	for i := range a.Tasks {
		a.Tasks[i].Desc = "A description long enough to be worth reading"
	}
	a.SetPivot(pivot.VerbName)
	a.SetFoldAll(true)

	columns := map[int]bool{}
	for _, l := range a.RenderHeadless(96, 20) {
		if before, _, ok := strings.Cut(l, "A description"); ok {
			columns[utf8.RuneCountInString(before)] = true
		}
	}
	if len(columns) != 1 {
		t.Errorf("descriptions start in %d different columns: %v", len(columns), columns)
	}
}

// --- the jiggle -------------------------------------------------------------------
//
// A theme is allowed to lean the selected row sideways. What it is not allowed to do is
// change how much room the row has, because the column it would take is the one holding
// the count at the right edge — so the room is reserved on every row, and the lean moves
// content around inside it.

func jiggling(t *testing.T, lean []int) *App {
	t.Helper()
	a := viewSample(t)
	a.Theme.Animation = theme.Animation{Jiggle: lean, Interval: theme.DefaultInterval}
	return a
}

func TestALeaningRowIsStillExactlyAsWideAsEveryOtherRow(t *testing.T) {
	a := jiggling(t, []int{0, 1})
	const width = 70
	for _, phase := range []int{0, 1} {
		a.Phase = phase
		frame := a.RenderFrame(width, 12)
		for i, row := range strings.Split(frame, "\n") {
			if n := lipgloss.Width(row); n != width {
				t.Errorf("phase %d row %d is %d columns, want %d", phase, i, n, width)
			}
		}
	}
}

// The point of reserving the room: the same text is on the row whether it is leaning or
// not, one column further along.
func TestTheLeanMovesTheRowRatherThanTrimmingIt(t *testing.T) {
	a := jiggling(t, []int{0, 1})

	a.Phase = 0
	still := a.RenderHeadless(70, 12)[0+2]
	a.Phase = 1
	leaning := a.RenderHeadless(70, 12)[0+2]

	if still == leaning {
		t.Fatal("the row did not move")
	}
	// Past the rail, which is the frame rather than the thing framed and does not move.
	content := func(row string) string { return strings.TrimSpace(string([]rune(row)[1:])) }
	if content(leaning) != content(still) {
		t.Errorf("the lean changed the row's content:\n  %q\n  %q", still, leaning)
	}
	// Specifically including the count at the right edge, which is what a lean that took the
	// column out of the row's own width would have eaten.
	if !strings.HasSuffix(content(leaning), "2") {
		t.Errorf("the count did not survive the lean: %q", leaning)
	}
}

// Every other row holds the room open too, so the list does not reflow as the cursor goes
// past — the unselected rows are identical whatever the phase.
func TestOnlyTheSelectedRowLeans(t *testing.T) {
	a := jiggling(t, []int{0, 1})
	a.Phase = 0
	before := a.RenderHeadless(70, 12)
	a.Phase = 1
	after := a.RenderHeadless(70, 12)

	moved := 0
	for i := range before {
		if before[i] != after[i] {
			moved++
		}
	}
	if moved != 1 {
		t.Errorf("%d rows moved, want just the selected one", moved)
	}
}

// A theme that does not jiggle keeps every column it had, or every existing theme quietly
// lost one.
func TestAStillThemeReservesNothing(t *testing.T) {
	still := viewSample(t)
	if got := still.bodyWidth(70); got != 70-frameWidth {
		t.Errorf("bodyWidth = %d, want %d", got, 70-frameWidth)
	}
	if got := jiggling(t, []int{0, 2}).bodyWidth(70); got != 70-frameWidth-2 {
		t.Errorf("a jiggling theme should reserve its lean, got %d", got)
	}
}

// --- the blink ----------------------------------------------------------------------

func blinking(t *testing.T, blink []bool) *App {
	t.Helper()
	a := viewSample(t)
	a.Theme.Animation = theme.Animation{Blink: blink, Interval: theme.DefaultInterval}
	a.Theme.Colors.Selection = theme.Color{Kind: theme.KindRGB, R: 0x2e, G: 0x31, B: 0x92}
	return a
}

// The bar goes out; the row does not. Same text, same width, same rail — only the
// highlight stops being drawn.
func TestTheBlinkTakesTheBarAndNotTheRow(t *testing.T) {
	a := blinking(t, []bool{true, false})

	a.Phase = 0
	lit := a.RenderHeadless(70, 12)
	a.Phase = 1
	dark := a.RenderHeadless(70, 12)

	if !reflect.DeepEqual(lit, dark) {
		t.Errorf("the blink changed what the rows say:\n  %q\n  %q", lit[2], dark[2])
	}
	if a.bodyWidth(70) != 70-frameWidth {
		t.Error("the blink should reserve nothing")
	}

	// The selection background is there on the lit frame and gone on the dark one, and the
	// rail is on both — losing that is losing your place, not blinking.
	//
	// Colour on, because with the profile stripped there is no background to look for and
	// the assertion would pass without ever testing anything.
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })

	a.Phase = 0
	litFrame := a.RenderFrame(70, 12)
	a.Phase = 1
	darkFrame := a.RenderFrame(70, 12)

	litRow := strings.Split(litFrame, "\n")[2]
	darkRow := strings.Split(darkFrame, "\n")[2]
	// Any RGB background, not a particular one: lipgloss rounds a hex through a float on the
	// way out, so `#2e3192` renders as 46;48;146 and pinning the triple pins the rounding.
	const anyBackground = "48;2;"
	if !strings.Contains(litRow, anyBackground) {
		t.Errorf("the lit row should carry the selection background:\n  %q", litRow)
	}
	if strings.Contains(darkRow, anyBackground) {
		t.Errorf("the dark row should not:\n  %q", darkRow)
	}
	rail := a.Theme.Glyphs.Rail
	if !strings.Contains(litRow, rail) || !strings.Contains(darkRow, rail) {
		t.Errorf("the rail keeps drawing through the dark frames:\n  %q\n  %q", litRow, darkRow)
	}
}

// Blinking and leaning are independent, so a theme can have both and the row keeps leaning
// while its bar is out.
func TestABlinkingRowStillLeans(t *testing.T) {
	a := viewSample(t)
	a.Theme.Animation = theme.Animation{
		Jiggle: []int{0, 1}, Blink: []bool{true, false}, Interval: theme.DefaultInterval,
	}
	a.Phase = 0
	home := a.RenderHeadless(70, 12)[2]
	a.Phase = 1
	away := a.RenderHeadless(70, 12)[2]

	if home == away {
		t.Fatal("the row stopped leaning once it blinked")
	}
	content := func(row string) string { return strings.TrimSpace(string([]rune(row)[1:])) }
	if content(home) != content(away) {
		t.Errorf("the lean lost content while dark:\n  %q\n  %q", home, away)
	}
}

// Naming a second colour turns the blink from a flash into a pulse: the bar stays on the
// row the whole time and changes colour instead of going away.
func TestABlinkColourPulsesRatherThanDropsTheBar(t *testing.T) {
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })

	a := blinking(t, []bool{true, false})
	a.Theme.Colors.SelectionBlink = theme.Color{Kind: theme.KindRGB, R: 0x43, G: 0x48, B: 0xd6}

	row := func(phase int) string {
		a.Phase = phase
		return strings.Split(a.RenderFrame(70, 12), "\n")[2]
	}
	lit, pulsed := row(0), row(1)

	if !strings.Contains(lit, "48;2;") || !strings.Contains(pulsed, "48;2;") {
		t.Fatalf("the bar should be drawn on both frames:\n  %q\n  %q", lit, pulsed)
	}
	if lit == pulsed {
		t.Error("the bar should have changed colour")
	}

	// …and with no second colour named, the same sequence drops the bar instead. Both
	// behaviours matter: the flash is what a theme gets for free.
	a.Theme.Colors.SelectionBlink = theme.Color{}
	if dark := row(1); strings.Contains(dark, "48;2;") {
		t.Errorf("without a blink colour the bar should go out:\n  %q", dark)
	}
}

// A row that wrapped is two or three cells tall, and the edge frames describe a mark moving
// inside one cell. Drawing the same half block on every line turned the rail into a stack of
// squares with gaps between them; the mark is spread across the row instead, so the lit part
// is one contiguous run that sloshes from one end to the other.
func TestATallRowsRailIsOneBarThatStillMoves(t *testing.T) {
	a := viewSample(t)
	a.Theme.Glyphs.Rail = "|"
	a.Theme.Animation = theme.Animation{
		Frames: []string{"▀", "▄"}, Interval: theme.DefaultInterval,
	}
	row := line{plain("wrapped")}

	railAt := func(phase, at, lines int) rune {
		return []rune(row.renderRow(20, true, a.Theme, phase, at, lines))[0]
	}

	// Two lines, and the half block marks the boundary of the lit part while the cell behind
	// it fills solid. `▀` lights from the top, so the solid cell is above it and the half
	// block is the bottom edge; `▄` is the mirror. Either way the lit run is contiguous.
	for _, want := range []struct {
		phase       int
		top, bottom rune
	}{
		{0, '█', '▀'}, // lit from the top, down through one and a half cells
		{1, '▄', '█'}, // and from the bottom on the next beat
	} {
		if got := railAt(want.phase, 0, 2); got != want.top {
			t.Errorf("phase %d line 0: %q, want %q", want.phase, string(got), string(want.top))
		}
		if got := railAt(want.phase, 1, 2); got != want.bottom {
			t.Errorf("phase %d line 1: %q, want %q", want.phase, string(got), string(want.bottom))
		}
	}

	// The two frames must differ, or it is a bar that does not bounce — which is the thing
	// this replaced.
	if railAt(0, 0, 2) == railAt(1, 0, 2) && railAt(0, 1, 2) == railAt(1, 1, 2) {
		t.Error("a tall row's rail stopped moving")
	}

	// A one-line row is untouched, which is nearly all of them.
	for _, phase := range []int{0, 1} {
		want := []rune(a.Theme.Animation.Frame(phase, "|"))[0]
		if got := railAt(phase, 0, 1); got != want {
			t.Errorf("phase %d: single-line row drew %q, want %q", phase, string(got), string(want))
		}
	}

	// A glyph nothing knows the shape of cannot be spread, so it is drawn as it comes rather
	// than guessed at.
	a.Theme.Animation.Frames = []string{"|", "+"}
	for at := range 2 {
		if got := railAt(1, at, 2); got != '+' {
			t.Errorf("line %d: %q, want the theme's own glyph unspread", at, string(got))
		}
	}

	// The lean is a property of the row rather than of a cell, so a tall row still leans.
	a.Theme.Animation.Jiggle = []int{0, 1}
	if row.renderRow(20, true, a.Theme, 0, 0, 2) == row.renderRow(20, true, a.Theme, 1, 0, 2) {
		t.Error("a tall row should still lean")
	}
}

// --- the wordmark -------------------------------------------------------------------

// `{project}` is what lets the header name what you are looking at rather than the tool you
// are looking at it with, without the theme giving up its decoration to do it.
func TestTheWordmarkCanNameTheProject(t *testing.T) {
	a := viewSample(t) // rooted at /tmp/atlas
	a.Theme.Glyphs.Wordmark = "✧ {project} ✧"

	head := a.RenderHeadless(70, 12)[0]
	if !strings.Contains(head, "✧ atlas ✧") {
		t.Errorf("header = %q", head)
	}
	// …and having named it, it does not name it again.
	if strings.Count(head, "atlas") != 1 {
		t.Errorf("the project is in the header twice: %q", head)
	}
}

// `{frame}` is where the wordmark's own sequence goes.
func TestTheWordmarkCanTwinkle(t *testing.T) {
	a := viewSample(t)
	a.Theme.Glyphs.Wordmark = "{frame} TASKUI {frame}"
	a.Theme.Animation = theme.Animation{
		WordmarkFrames: []string{"✧", "✦"}, Interval: theme.DefaultInterval,
	}

	a.Phase = 0
	first := a.RenderHeadless(70, 12)[0]
	a.Phase = 1
	second := a.RenderHeadless(70, 12)[0]

	if !strings.Contains(first, "✧ TASKUI ✧") || !strings.Contains(second, "✦ TASKUI ✦") {
		t.Errorf("the wordmark did not cycle:\n  %q\n  %q", first, second)
	}
	// A theme that leaves `{frame}` in but switches the animation off closes the gap rather
	// than printing the placeholder at somebody.
	a.Theme.Animation.Interval = 0
	if got := a.RenderHeadless(70, 12)[0]; strings.Contains(got, "{frame}") {
		t.Errorf("the placeholder leaked into the header: %q", got)
	}
}

// The header used to print the name twice for any theme whose decoration was not on a
// hardcoded list — which is what happened the moment two themes were added.
func TestEveryShippedWordmarkStripsToItsName(t *testing.T) {
	for mark, want := range map[string]string{
		"taskui":         "taskui",
		"░▒▓ TASKUI ▓▒░": "TASKUI",
		"▄▀▄ TASKUI ▄▀▄": "TASKUI",
		"✧･ﾟ TASKUI ･ﾟ✧": "TASKUI",
		"[ TASKUI ]":     "TASKUI",
		"»» my-repo ««":  "my-repo",
		"★ project 2 ★":  "project 2",
	} {
		if got := plainWordmark(mark); got != want {
			t.Errorf("plainWordmark(%q) = %q, want %q", mark, got, want)
		}
	}
}

// A command echo is drawn with the verdict of the command it announces, so a build log
// says which step is running and which one took the task down without being read end to
// end. Output lines keep their bare gutter — a marker on every row is chrome.
func TestCommandRowsCarryTheirStatus(t *testing.T) {
	a := appWithRun(t, "test")
	// Through LineEvent rather than Feed: the command flag is what the capture's parser
	// sets when it recognises go-task's own echo, and this test is about what is done
	// with it.
	a.Run.Apply(run.LineEvent{Task: "test", Raw: "go build ./...", IsCommand: true})
	a.Run.Feed("test", "ok")
	a.Run.Apply(run.LineEvent{Task: "test", Raw: "go test ./...", IsCommand: true})
	a.Run.Feed("test", "--- FAIL: TestOrderTotal")
	a.RunExpand("test")
	a.Run.ApplyFailed("test")
	a.Run.Finish(1)
	a.RebuildRunRows()

	frame := strings.Join(a.RenderHeadless(80, 14), "\n")
	g := a.Theme.Glyphs
	for _, want := range []string{
		g.StatusOk + " " + g.Command + " go build ./...",
		g.StatusFailed + " " + g.Command + " go test ./...",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("want %q in:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, g.StatusOk+" "+g.Command+" ok") {
		t.Errorf("plain output should carry no command marker:\n%s", frame)
	}
}

// Output hangs off the command that printed it on a rail that closes at the last line, so
// which lines belong to which step is a thing you can see rather than infer.
func TestOutputHangsOffItsCommand(t *testing.T) {
	a := appWithRun(t, "test")
	a.Run.Apply(run.LineEvent{Task: "test", Raw: "go test ./...", IsCommand: true})
	a.Run.Feed("test", "=== RUN TestOrderTotal")
	a.Run.Feed("test", "--- FAIL: TestOrderTotal")
	a.Run.Apply(run.LineEvent{Task: "test", Raw: "go vet ./...", IsCommand: true})
	a.Run.Feed("test", "clean")
	a.RunExpand("test")
	a.RebuildRunRows()

	frame := strings.Join(a.RenderHeadless(80, 14), "\n")
	g := a.Theme.Glyphs
	for _, want := range []string{
		g.GuideVertical + "   === RUN TestOrderTotal", // more of this command's output below
		g.GuideLast + "   --- FAIL: TestOrderTotal",   // the last of it
		g.GuideLast + "   clean",                      // and the next command's only line
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("want %q in:\n%s", want, frame)
		}
	}
}
