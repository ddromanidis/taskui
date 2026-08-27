package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
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
	for _, want := range []string{"taskui", "atlas", "domain·verb"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q is missing %q", lines[0], want)
		}
	}

	active, other := a.pivotStyle(pivot.Domain), a.pivotStyle(pivot.Verb)
	if active.GetForeground() == other.GetForeground() {
		t.Error("the two halves should not look alike")
	}

	a.ToggleMode()
	if a.pivotStyle(pivot.Verb).GetForeground() != active.GetForeground() {
		t.Error("pivoting should move the accent to the other half")
	}
	if !strings.Contains(a.RenderHeadless(70, 12)[0], "domain·verb") {
		t.Error("and both halves stay named")
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
	for _, mode := range []pivot.Mode{pivot.Domain, pivot.Verb} {
		a := manyTasks(t, 80)
		a.Mode = mode
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
	prefix := func(l, label string) string { return l[:strings.Index(l, label)] }

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
			if i := strings.Index(l, label); i >= 0 {
				return utf8.RuneCountInString(l[:i])
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
		if i := strings.Index(l, tick); i >= 0 {
			columns = append(columns, utf8.RuneCountInString(l[:i]))
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
