package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/theme"
)

func appWith(t *testing.T, names []string) *App {
	t.Helper()
	a := New(pivot.Fixture(names), "/tmp/repo")
	// Never touch the user's real archive from a test.
	a.SetStateDir(t.TempDir())
	return a
}

func sample(t *testing.T) *App {
	t.Helper()
	return appWith(t, []string{
		"all",
		"build",
		"fmt",
		"lint",
		"app:build",
		"app:fmt",
		"app:lint",
		"backend:build",
		"backend:fmt",
		"backend:lint",
		"backend:migrate",
		"backend:migrate:down",
		"infra:lint",
		"site:build",
	})
}

func parkOn(t *testing.T, a *App, name string) int {
	t.Helper()
	a.SetFoldAll(true)
	ti := -1
	for i, x := range a.Tasks {
		if x.Name == name {
			ti = i
		}
	}
	if ti < 0 {
		t.Fatalf("no task %q", name)
	}
	for i, r := range a.Rows {
		if a.Tree.Nodes[r.Node].Task == ti {
			// Through the picker list rather than straight at the tree index: with a run
			// unfolded above it, a task's row and its tree row are no longer the same
			// number.
			a.Cursor = a.pickerIndexOfTree(i)
			return ti
		}
	}
	t.Fatalf("task %q has no row", name)
	return -1
}

func visibleTaskNames(a *App) []string {
	var out []string
	for _, r := range a.Rows {
		if ti := a.Tree.Nodes[r.Node].Task; ti != pivot.NoTask {
			out = append(out, a.Tasks[ti].Name)
		}
	}
	return out
}

// The property that makes the pivot read as a pivot rather than a navigation reset: the
// same task stays under the cursor, at its new address, with folds opened to reveal it.
func TestSelectionSurvivesThePivot(t *testing.T) {
	a := sample(t)
	ti := parkOn(t, a, "backend:lint")
	if a.ModeLabel() != pivot.DomainName {
		t.Fatalf("mode = %v", a.ModeLabel())
	}

	a.ToggleMode()
	if a.ModeLabel() != pivot.VerbName {
		t.Errorf("mode = %v", a.ModeLabel())
	}
	if a.SelectedTask() != ti {
		t.Error("the same task should stay under the cursor after pivoting to verb")
	}

	a.ToggleMode()
	if a.SelectedTask() != ti {
		t.Error("and again on the way back")
	}
}

// Fold state is kept per mode, so bouncing between pivots does not collapse what you
// opened on the other side.
func TestFoldStateIsRememberedPerMode(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	domainRows := len(a.Rows)

	// By name rather than by toggling twice: `p` cycles through every grouping there is, so
	// two presses stopped meaning "there and back" the moment a third pivot existed.
	if !a.SetPivot(pivot.VerbName) {
		t.Fatal("no verb pivot")
	}
	a.SetFoldAll(false)
	if !a.SetPivot(pivot.DomainName) {
		t.Fatal("no domain pivot")
	}

	if len(a.Rows) != domainRows {
		t.Errorf("domain folds were disturbed: %d, want %d", len(a.Rows), domainRows)
	}
}

// `p` visits every grouping and comes back, however many there are.
func TestThePivotKeyCyclesThroughEveryGrouping(t *testing.T) {
	a := sample(t)
	start := a.ModeLabel()
	seen := map[string]bool{start: true}
	for range len(a.Pivots) - 1 {
		a.ToggleMode()
		if seen[a.ModeLabel()] {
			t.Fatalf("%q came round twice before the cycle was done", a.ModeLabel())
		}
		seen[a.ModeLabel()] = true
	}
	a.ToggleMode()
	if a.ModeLabel() != start {
		t.Errorf("the cycle ended on %q, not back at %q", a.ModeLabel(), start)
	}
	if len(seen) != len(a.Pivots) {
		t.Errorf("visited %d of %d groupings", len(seen), len(a.Pivots))
	}
}

// A hit hidden behind a fold is a hit you did not find, so filtering opens everything —
// without disturbing the fold state you get back when you clear it.
func TestFilteringRevealsMatchesAndRestoresFoldsAfter(t *testing.T) {
	a := sample(t)
	collapsed := len(a.Rows)

	for _, c := range "lint" {
		a.PushQuery(c)
	}
	names := visibleTaskNames(a)
	found := false
	for _, n := range names {
		if n == "backend:lint" {
			found = true
		}
		if !strings.Contains(n, "lint") {
			t.Errorf("only matches expected: %v", names)
		}
	}
	if !found {
		t.Errorf("matches should be on screen, not behind a fold: %v", names)
	}

	a.ClearQuery()
	if len(a.Rows) != collapsed {
		t.Errorf("folds should be as they were: %d, want %d", len(a.Rows), collapsed)
	}
}

// The whole difference from the filter: the list stays as it was, and only the cursor
// moves. Folds hiding the match are opened.
func TestJumpingMovesTheCursorWithoutNarrowingTheList(t *testing.T) {
	a := sample(t)
	rowsBefore := len(a.Rows)

	a.BeginJump()
	for _, c := range "backend:lint" {
		a.PushJump(c)
	}

	if ti := a.SelectedTask(); ti < 0 || a.Tasks[ti].Name != "backend:lint" {
		t.Errorf("selected = %d", ti)
	}
	if len(a.Rows) < rowsBefore {
		t.Errorf("the list was filtered down: %d -> %d", rowsBefore, len(a.Rows))
	}
	if a.Query != "" {
		t.Error("the filter should never have been touched")
	}
}

// `esc` really cancels: the cursor goes back where it started.
func TestCancellingAJumpRestoresTheCursor(t *testing.T) {
	a := sample(t)
	origin := parkOn(t, a, "app:fmt")

	a.BeginJump()
	for _, c := range "backend:lint" {
		a.PushJump(c)
	}
	if a.SelectedTask() == origin {
		t.Fatal("the jump did not move")
	}

	a.CancelJump()
	if a.SelectedTask() != origin {
		t.Error("the cursor should be back where it started")
	}
}

// Stepping wraps, as the output search does.
func TestJumpStepsThroughEveryMatch(t *testing.T) {
	a := sample(t)
	a.BeginJump()
	for _, c := range "lint" {
		a.PushJump(c)
	}
	n := len(a.JumpMatches)
	if n <= 1 {
		t.Fatalf("several tasks should match lint: %d", n)
	}

	first := a.SelectedTask()
	a.JumpStep(1)
	if a.SelectedTask() == first {
		t.Error("stepping did not move")
	}
	for i := 1; i < n; i++ {
		a.JumpStep(1)
	}
	if a.SelectedTask() != first {
		t.Error("stepping should have wrapped back round")
	}
}

// Accepting keeps the cursor where the jump left it.
func TestAcceptingAJumpKeepsThePosition(t *testing.T) {
	a := sample(t)
	a.BeginJump()
	for _, c := range "infra:lint" {
		a.PushJump(c)
	}
	landed := a.SelectedTask()
	a.AcceptJump()
	if a.Jumping {
		t.Error("still jumping")
	}
	if a.SelectedTask() != landed {
		t.Error("the cursor moved after accepting")
	}
}

// Fuzzy, over the full colon path — `blint` should find `backend:lint`.
func TestFilterMatchesFuzzilyAcrossThePath(t *testing.T) {
	a := sample(t)
	for _, c := range "blint" {
		a.PushQuery(c)
	}
	found := false
	for _, n := range visibleTaskNames(a) {
		if n == "backend:lint" {
			found = true
		}
	}
	if !found {
		t.Errorf("visible = %v", visibleTaskNames(a))
	}
}

// Smart case, as nucleo and fzf do it: an uppercase letter in the query makes the whole
// thing exact.
func TestTheFilterIsSmartCased(t *testing.T) {
	a := appWith(t, []string{"backend:lint", "Backend:Lint"})
	for _, c := range "BL" {
		a.PushQuery(c)
	}
	names := visibleTaskNames(a)
	if !reflect.DeepEqual(names, []string{"Backend:Lint"}) {
		t.Errorf("uppercase should match exactly: %v", names)
	}

	a.ClearQuery()
	for _, c := range "bl" {
		a.PushQuery(c)
	}
	if len(visibleTaskNames(a)) != 2 {
		t.Errorf("lowercase should match loosely: %v", visibleTaskNames(a))
	}
}

// A group row that is also a task must stay runnable — selecting `backend:migrate` should
// offer the task, not just the fold.
func TestGroupThatIsAlsoATaskIsStillSelectable(t *testing.T) {
	a := sample(t)
	ti := parkOn(t, a, "backend:migrate")
	node := a.SelectedNode()
	if node == nil || !node.IsGroup() {
		t.Fatal("backend:migrate should parent its subtasks")
	}
	if a.SelectedTask() != ti {
		t.Error("and should still be runnable itself")
	}
}

// --- run view ---------------------------------------------------------------------

func appWithRun(t *testing.T, root string, edges ...run.Edge) *App {
	t.Helper()
	a := appWith(t, []string{root})
	a.Run = run.Detached(root, run.GraphFrom(edges...))
	a.Screen = ScreenRun
	a.RebuildRunRows()
	return a
}

func taskRows(a *App) []string {
	var out []string
	for _, r := range a.RunRows {
		if r.IsTask {
			out = append(out, strings.Repeat("  ", r.Depth)+r.Name)
		}
	}
	return out
}

func rendered(a *App) []string {
	out := make([]string, 0, len(a.RunRows))
	for _, r := range a.RunRows {
		if r.IsTask {
			out = append(out, "task "+r.Name)
		} else {
			out = append(out, "line "+a.Run.Tasks[r.Task].Lines[r.Index].Plain)
		}
	}
	return out
}

func TestTheRunTreeFollowsInvocationOrder(t *testing.T) {
	a := appWithRun(t, "all",
		run.Edge{Parent: "all", Children: []string{"lint", "test"}},
		run.Edge{Parent: "lint", Children: []string{"app:lint"}},
	)
	want := []string{"all", "  lint", "    app:lint", "  test"}
	if got := taskRows(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A task reached by two paths — invoked from both `check` and `build` — is one node in the
// graph and must be one row, not two.
func TestADiamondIsShownOnce(t *testing.T) {
	a := appWithRun(t, "all",
		run.Edge{Parent: "all", Children: []string{"check", "build"}},
		run.Edge{Parent: "check", Children: []string{"app:css"}},
		run.Edge{Parent: "build", Children: []string{"app:css"}},
	)
	rows := taskRows(a)
	n := 0
	for _, r := range rows {
		if strings.TrimSpace(r) == "app:css" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("app:css appears %d times: %v", n, rows)
	}
	want := []string{"all", "  check", "    app:css", "  build"}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %v, want %v", rows, want)
	}
}

// Output lines are rows of the same list, so one fold tree holds both — and `o` walks
// three states over them rather than two.
//
// A task rests at a peek: the last few lines, always. Only FoldHidden is empty, and you
// have to ask for it.
func TestFoldingCyclesHiddenPeekAndFull(t *testing.T) {
	a := appWithRun(t, "a", run.Edge{Parent: "a"})
	for i := range 8 {
		a.Run.Feed("a", "line "+string(rune('0'+i)))
	}
	a.RebuildRunRows()
	a.RunCursor = 0

	// The resting state. Five of eight, and the last five — the end of a command's output
	// is the part that says how it went.
	if a.FoldOf("a") != FoldPeek {
		t.Fatalf("fold = %v", a.FoldOf("a"))
	}
	want := []string{"task a", "line line 3", "line line 4", "line line 5", "line line 6", "line line 7"}
	if got := rendered(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for _, r := range a.RunRows[1:] {
		if !r.Peek {
			t.Error("peek rows should each be one row tall")
		}
	}

	a.RunToggleFold()
	if a.FoldOf("a") != FoldFull {
		t.Errorf("fold = %v", a.FoldOf("a"))
	}
	if len(a.RunRows) != 9 {
		t.Errorf("task plus all eight lines: %d", len(a.RunRows))
	}
	if a.RunRows[1].Peek || a.RunRows[1].Index != 0 || a.RunRows[1].Task != "a" {
		t.Errorf("first line row = %+v", a.RunRows[1])
	}

	a.RunToggleFold()
	if a.FoldOf("a") != FoldHidden || len(a.RunRows) != 1 {
		t.Errorf("fold = %v, rows = %d", a.FoldOf("a"), len(a.RunRows))
	}

	a.RunToggleFold()
	if a.FoldOf("a") != FoldPeek {
		t.Error("should have come round again")
	}
}

// The window tails: what it shows is whatever arrived last, not whatever arrived first. A
// dev server's peek that froze on its startup banner would be a window onto nothing.
func TestAPeekFollowsTheEndOfTheOutput(t *testing.T) {
	a := appWithRun(t, "a", run.Edge{Parent: "a"})
	for i := range 6 {
		a.Run.Feed("a", "line "+string(rune('0'+i)))
	}
	a.RebuildRunRows()
	rows := rendered(a)
	if rows[len(rows)-1] != "line line 5" {
		t.Fatalf("last = %q", rows[len(rows)-1])
	}

	a.Run.Feed("a", "the newest thing")
	a.RebuildRunRows()

	rows = rendered(a)
	if rows[len(rows)-1] != "line the newest thing" {
		t.Errorf("last = %q", rows[len(rows)-1])
	}
	if len(rows) != 6 {
		t.Errorf("still the task and five lines: %d", len(rows))
	}
	for _, r := range rows {
		if r == "line line 1" {
			t.Error("the top should have rolled off")
		}
	}
}

// Following moves the cursor and leaves every fold alone.
func TestFollowingMovesTheCursorWithoutOpeningAnything(t *testing.T) {
	a := appWithRun(t, "ci", run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	for i := range 8 {
		a.Run.Feed("build", "build "+string(rune('0'+i)))
	}
	a.Follow()
	if a.FoldOf("build") != FoldPeek {
		t.Error("what is running should still peek")
	}
	if name, _ := a.RunSelectedTask(); name != "build" {
		t.Errorf("cursor is on %q", name)
	}

	a.Run.Tasks["build"].Status = run.Ok
	for i := range 8 {
		a.Run.Feed("test", "test "+string(rune('0'+i)))
	}
	a.Follow()

	if name, _ := a.RunSelectedTask(); name != "test" {
		t.Errorf("cursor is on %q", name)
	}
	// Both windows are still open at once — the thing that was missing.
	if a.FoldOf("build") != FoldPeek || a.FoldOf("test") != FoldPeek {
		t.Error("both tasks should still peek")
	}
}

// Every task in a live run shows its own window at the same time, so a glance tells you
// what each step is saying without opening any of them.
func TestEveryTaskInARunKeepsItsOwnWindow(t *testing.T) {
	a := appWithRun(t, "ci", run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	for i := range 20 {
		a.Run.Feed("build", "build "+string(rune('a'+i)))
		a.Run.Feed("test", "test "+string(rune('a'+i)))
	}
	a.Follow()
	a.RebuildRunRows()

	rows := rendered(a)
	// Three task rows and five lines under each of the two that printed.
	if len(rows) != 3+5+5 {
		t.Errorf("rows = %d: %v", len(rows), rows)
	}
}

// A fold you set yourself is yours, and the next task starting does not overrule it.
func TestFollowingDoesNotTakeBackAFoldYouSet(t *testing.T) {
	a := appWithRun(t, "ci", run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	for i := range 8 {
		a.Run.Feed("build", "build "+string(rune('0'+i)))
	}
	a.Follow()

	// Off the resting peek by hand, twice, to land somewhere following would not.
	a.cursorToTask("build")
	a.RunToggleFold()
	a.RunToggleFold()
	if a.FoldOf("build") != FoldHidden {
		t.Fatalf("fold = %v", a.FoldOf("build"))
	}

	a.Following = true
	a.Run.Tasks["build"].Status = run.Ok
	a.Run.Feed("test", "test 0")
	a.Follow()

	if a.FoldOf("build") != FoldHidden {
		t.Error("the fold should be exactly as set")
	}
}

// A task with less output than the window shows all of it, and says nothing about what is
// not there.
func TestAShortTaskPeeksAtEverythingItHas(t *testing.T) {
	a := appWithRun(t, "a", run.Edge{Parent: "a"})
	a.Run.Feed("a", "first")
	a.Run.Feed("a", "second")
	a.RebuildRunRows()

	want := []string{"task a", "line first", "line second"}
	if got := rendered(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Reading one task while an earlier one is still printing must not drag the line under the
// cursor away.
func TestOutputArrivingAboveTheCursorDoesNotMoveIt(t *testing.T) {
	a := appWithRun(t, "ci", run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	for i := range 3 {
		a.Run.Feed("build", "build "+string(rune('0'+i)))
	}
	for i := range 3 {
		a.Run.Feed("test", "test "+string(rune('0'+i)))
	}
	a.RebuildRunRows()

	// Park on a specific line of the later task, the way reading does.
	at := -1
	for i, r := range rendered(a) {
		if r == "line test 1" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the line is not on screen")
	}
	a.RunCursor = at
	a.Following = false

	// The earlier task prints four more lines, pushing everything below it down.
	for i := 3; i < 7; i++ {
		a.Run.Feed("build", "build "+string(rune('0'+i)))
	}
	a.RebuildRunRows()

	if a.RunCursor == at {
		t.Error("the row genuinely moved, so the cursor should have too")
	}
	if got := rendered(a)[a.RunCursor]; got != "line test 1" {
		t.Errorf("cursor is on %q", got)
	}
}

// A cursor inside a peek window, on a line the window has since scrolled past.
func TestACursorInAPeekHoldsItsPlaceOnceItsLineRollsOff(t *testing.T) {
	a := appWithRun(t, "a", run.Edge{Parent: "a"})
	for i := range 6 {
		a.Run.Feed("a", "line "+string(rune('0'+i)))
	}
	a.RebuildRunRows()

	// Third row under the task: the window is showing lines 1..=5.
	a.RunCursor = 3
	a.Following = false
	if got := rendered(a)[3]; got != "line line 3" {
		t.Fatalf("row 3 = %q", got)
	}

	// One more line: the window slides, but line 3 is still in it, so the cursor follows
	// the text down rather than staying put.
	a.Run.Feed("a", "line 6")
	a.RebuildRunRows()
	if got := rendered(a)[a.RunCursor]; got != "line line 3" {
		t.Errorf("cursor is on %q", got)
	}
	if a.RunCursor != 2 {
		t.Errorf("cursor = %d, want 2", a.RunCursor)
	}

	// Four more, and line 3 is off the window entirely.
	for i := 7; i < 11; i++ {
		a.Run.Feed("a", "line "+string(rune('0'+i)))
	}
	a.RebuildRunRows()

	if a.RunCursor != 2 {
		t.Errorf("cursor = %d, should have held its place in the window", a.RunCursor)
	}
	for _, r := range rendered(a) {
		if r == "line line 3" {
			t.Error("the line it was on should be gone")
		}
	}
}

// `⇧R` re-runs with `--force` regardless of how the original run was invoked, and plain
// `r` still inherits.
func TestForceRerunForcesAndPlainRerunDoesNot(t *testing.T) {
	a := appWithRun(t, "build", run.Edge{Parent: "build"})
	if a.Run.Force {
		t.Fatal("the run was not forced to begin with")
	}

	a.cursorToTask("build")
	a.RerunSelected()
	if a.ForceNext {
		t.Error("`r` should re-run it the way it was run")
	}

	a.ForceRerunSelected()
	if !a.ForceNext {
		t.Error("`⇧R` should turn the checks off")
	}
}

// The override only ever adds. Re-running a forced run with plain `r` keeps it forced.
func TestPlainRerunKeepsAForcedRunForced(t *testing.T) {
	a := appWithRun(t, "build", run.Edge{Parent: "build"})
	a.Run.Force = true

	a.cursorToTask("build")
	a.RerunSelected()
	if !a.ForceNext {
		t.Error("force should have been inherited")
	}
}

// Moving the cursor means you have gone looking for something; the view must stop yanking
// you back to whatever is running.
func TestManualMovementStopsFollowing(t *testing.T) {
	a := appWithRun(t, "all", run.Edge{Parent: "all", Children: []string{"lint"}})
	if !a.Following {
		t.Fatal("should start out following")
	}
	a.RunMoveCursor(1)
	if a.Following {
		t.Error("should have stopped following")
	}
}

// The cursor can sit on an output line; `r` should still know which task to re-run.
func TestALineRowReportsItsOwningTask(t *testing.T) {
	a := appWithRun(t, "a", run.Edge{Parent: "a"})
	a.Run.Feed("a", "hello")
	a.RebuildRunRows()
	a.RunCursor = 0
	a.RunToggleFold()
	a.RunCursor = 1 // the output line
	if a.RunRows[1].IsTask {
		t.Fatal("row 1 should be a line")
	}
	if name, _ := a.RunSelectedTask(); name != "a" {
		t.Errorf("owning task = %q", name)
	}
}

// --- output search ------------------------------------------------------------------

func searchableApp(t *testing.T) *App {
	t.Helper()
	a := appWithRun(t, "ci", run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	a.Run.Feed("build", "compiling core")
	a.Run.Feed("build", "compiling api")
	a.Run.Feed("test", "running 42 tests")
	a.Run.Feed("test", "--- FAIL: TestOrderTotal")
	a.Run.Feed("test", "3 migrations pending")
	a.RebuildRunRows()
	return a
}

// Filter mode collapses the run to matching lines, and drops the tasks that have none —
// including the root, which would otherwise be a permanent empty header.
func TestFilterModeShowsOnlyMatchingLinesUnderTheirTasks(t *testing.T) {
	a := searchableApp(t)
	a.FilterContext = 0
	a.SearchInput = "pending"
	a.ApplySearch()
	a.ToggleFilterMatches()

	want := []string{"task test", "line 3 migrations pending"}
	if got := rendered(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A `FAIL:` line on its own hides the assertion underneath it, which is the half that says
// what actually broke — so a hit drags its neighbours in with it.
func TestFilterModeKeepsContextAroundEachHit(t *testing.T) {
	a := searchableApp(t)
	a.FilterContext = 1
	a.SearchInput = "FAIL"
	a.ApplySearch()
	a.ToggleFilterMatches()

	want := []string{
		"task test",
		"line running 42 tests",
		"line --- FAIL: TestOrderTotal",
		"line 3 migrations pending",
	}
	if got := rendered(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Context is adjustable, and widening it does not lose the hit.
func TestContextCanBeWidenedAndNarrowed(t *testing.T) {
	a := searchableApp(t)
	a.SearchInput = "FAIL"
	a.ApplySearch()
	a.ToggleFilterMatches()

	a.SetFilterContext(-10)
	if a.FilterContext != 0 {
		t.Errorf("context = %d", a.FilterContext)
	}
	want := []string{"task test", "line --- FAIL: TestOrderTotal"}
	if got := rendered(a); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	a.SetFilterContext(1)
	if got := len(rendered(a)); got != 4 {
		t.Errorf("the hit plus one line either side: %d", got)
	}
}

// A hit behind a closed fold is a hit you did not find — and a peek can hide one too,
// since it only shows the tail.
func TestJumpingToAHitOpensTheFoldHidingIt(t *testing.T) {
	a := searchableApp(t)
	a.RunSetFold("test", FoldHidden)
	for _, r := range a.RunRows {
		if !r.IsTask && r.Task == "test" {
			t.Fatal("the task holding the hit should be showing nothing")
		}
	}

	a.SearchInput = "FAIL"
	a.ApplySearch()

	if len(a.SearchHits) != 1 {
		t.Fatalf("hits = %d", len(a.SearchHits))
	}
	row := a.RunRows[a.RunCursor]
	if row.IsTask || row.Task != "test" {
		t.Errorf("cursor is on %+v", row)
	}
}

// `n` past the last hit comes back to the first rather than sticking.
func TestSteppingThroughHitsWraps(t *testing.T) {
	a := searchableApp(t)
	a.SearchInput = "compiling"
	a.ApplySearch()
	if len(a.SearchHits) != 2 || a.SearchIdx != 0 {
		t.Fatalf("hits = %d idx = %d", len(a.SearchHits), a.SearchIdx)
	}

	a.SearchStep(1)
	if a.SearchIdx != 1 {
		t.Errorf("idx = %d", a.SearchIdx)
	}
	a.SearchStep(1)
	if a.SearchIdx != 0 {
		t.Error("should have wrapped forward")
	}
	a.SearchStep(-1)
	if a.SearchIdx != 1 {
		t.Error("and backward")
	}
}

// Half-typed regexes are the normal state during incremental search.
func TestAnIncompletePatternReportsRatherThanFailing(t *testing.T) {
	a := searchableApp(t)
	a.SearchInput = "(unclosed"
	a.ApplySearch()
	if a.SearchError == "" || a.Search != nil {
		t.Errorf("error = %q search = %v", a.SearchError, a.Search)
	}

	a.SearchInput = "(unclosed)"
	a.ApplySearch()
	if a.SearchError != "" {
		t.Errorf("error = %q", a.SearchError)
	}
}

// Searching means you have gone looking; the view must stop chasing the run.
func TestSearchingStopsFollowing(t *testing.T) {
	a := searchableApp(t)
	a.Following = true
	a.SearchInput = "FAIL"
	a.ApplySearch()
	if a.Following {
		t.Error("should have stopped following")
	}
}

// --- ordering, end to end ---------------------------------------------------------------

// rowLabels is the picker's tree rows, top to bottom.
func rowLabels(a *App) []string {
	out := make([]string, 0, len(a.Rows))
	for _, r := range a.Rows {
		out = append(out, a.Tree.Nodes[r.Node].Label)
	}
	return out
}

// A config that names an order has to reach the tree the first frame draws, not the one
// after the first keypress.
func TestTheConfiguredOrderIsInTheFirstTreeDrawn(t *testing.T) {
	config := theme.DefaultConfig()
	config.Order.Pins = []string{"site", "infra"}

	a := New(pivot.Fixture([]string{"app:build", "backend:build", "infra:lint", "site:build"}),
		"/tmp/repo")
	a.SetStateDir(t.TempDir())
	a.WithConfig(config)
	a.SetFoldAll(false)

	want := []string{"site", "infra", "app", "backend"}
	if got := rowLabels(a); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v; want %v", got, want)
	}
}

// `recent` and `failed` are sorted on the archive, which changes every time a run finishes —
// so the lookup has to be the live one rather than a closure captured at startup.
func TestRecentOrderFollowsTheArchiveAsItChanges(t *testing.T) {
	a := appWith(t, []string{"aaa", "zzz"})
	a.Order.By = pivot.ByRecent
	a.Rebuild(-1)
	a.SetFoldAll(true)

	if got := rowLabels(a); got[0] != "aaa" {
		t.Fatalf("with an empty archive this should be alphabetical, got %v", got)
	}

	a.Outcomes["zzz"] = store.Outcome{Ok: true, WhenUnix: 1000}
	a.Rebuild(-1)
	if got := rowLabels(a); got[0] != "zzz" {
		t.Errorf("the run that just happened should lead, got %v", got)
	}
}
