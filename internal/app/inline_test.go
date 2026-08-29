package app

import (
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
)

// pickerWithRun puts a run in a slot and leaves the cursor on the task it was started
// from — the state pressing `⏎` in the picker now leaves you in.
func pickerWithRun(t *testing.T, root string, names []string, edges ...run.Edge) *App {
	t.Helper()
	a := appWith(t, names)
	a.SetFoldAll(true)
	a.Run = run.Detached(root, run.GraphFrom(edges...))
	a.Screen = ScreenPicker
	a.RebuildRunRows()
	a.RebuildPickerRows()
	parkOn(t, a, root)
	return a
}

// blockRows are the run rows the picker is showing under a task.
func blockRows(a *App, root string) []PickerRow {
	var out []PickerRow
	for _, row := range a.PickerRows {
		if row.IsRun() && row.Root == root {
			out = append(out, row)
		}
	}
	return out
}

func inlineTaskNames(a *App, root string) []string {
	var out []string
	for _, row := range blockRows(a, root) {
		if row.Run.IsTask {
			out = append(out, row.Run.Name)
		}
	}
	return out
}

// The run appears under the row it was started from, whole: the tasks it pulled in and the
// output each of them printed. Its own root row is not repeated — the picker row it hangs
// under is that row.
func TestARunUnfoldsUnderTheTaskItCameFrom(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"},
		run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	a.Run.Feed("build", "compiling core")
	a.Run.Feed("test", "--- FAIL: TestOrderTotal")
	a.RebuildPickerRows()

	if got := inlineTaskNames(a, "ci"); !equalStrings(got, []string{"build", "test"}) {
		t.Errorf("inline tasks = %v, want the two ci pulled in", got)
	}
	for _, row := range blockRows(a, "ci") {
		if row.Run.IsTask && row.Run.Name == "ci" {
			t.Error("the root's own row is the picker row; it must not be drawn twice")
		}
	}

	// The tree is still the tree: every task keeps its row, in order.
	var tree []int
	for _, row := range a.PickerRows {
		if !row.IsRun() {
			tree = append(tree, row.Tree)
		}
	}
	for i, at := range tree {
		if at != i {
			t.Fatalf("tree rows came out as %v, want them in order", tree)
		}
	}
	if len(tree) != len(a.Rows) {
		t.Errorf("%d tree rows in the picker, want %d", len(tree), len(a.Rows))
	}
}

// The fold key on the task walks the whole run through hidden, peek and full — the three
// states the run view uses, on the granularity the picker is for.
func TestTheBlockCyclesHiddenPeekFull(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"},
		run.Edge{Parent: "ci", Children: []string{"build"}})
	for i := range 12 {
		a.Run.Feed("build", "line "+string(rune('a'+i)))
	}
	a.RebuildPickerRows()

	if got := a.BlockFold("ci"); got != FoldPeek {
		t.Fatalf("a fresh run starts at %v, want a peek", got)
	}
	peeked := len(blockRows(a, "ci"))

	if !a.CycleOutputFold() {
		t.Fatal("the fold key found nothing to fold")
	}
	if got := a.BlockFold("ci"); got != FoldFull {
		t.Fatalf("after one press: %v, want full", got)
	}
	full := len(blockRows(a, "ci"))
	if full <= peeked {
		t.Errorf("full showed %d rows and the peek showed %d", full, peeked)
	}

	a.CycleOutputFold()
	if got := a.BlockFold("ci"); got != FoldHidden {
		t.Fatalf("after two: %v, want hidden", got)
	}
	if n := len(blockRows(a, "ci")); n != 0 {
		t.Errorf("hidden left %d rows on screen", n)
	}

	a.CycleOutputFold()
	if got := a.BlockFold("ci"); got != FoldPeek {
		t.Errorf("after three: %v, want a peek again", got)
	}
}

// Inside the block the same key moves one task, so a run whose interesting half is one
// failing dep does not have to be opened whole to read it.
func TestFoldingInsideTheBlockMovesOneTask(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"},
		run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	for i := range 9 {
		a.Run.Feed("build", "build "+string(rune('a'+i)))
		a.Run.Feed("test", "test "+string(rune('a'+i)))
	}
	a.RebuildPickerRows()

	at := -1
	for i, row := range a.PickerRows {
		if row.IsRun() && row.Run.IsTask && row.Run.Name == "build" {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("build has no row in the block")
	}
	a.Cursor = at

	if !a.CursorInRun() {
		t.Fatal("the cursor is on a row of the run")
	}
	a.CycleOutputFold()

	folds := a.slotFolds("ci")
	if folds["build"] != FoldFull {
		t.Errorf("build = %v, want it opened", folds["build"])
	}
	if folds["test"] != FoldPeek {
		t.Errorf("test = %v, want it left alone", folds["test"])
	}
}

// The fold belongs to the run, not to the screen it was set from: open a task in the
// picker, press `v`, and it is open there too. Two states would be two states to
// contradict each other.
func TestAFoldSetInThePickerIsTheRunsOwn(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"ci"})
	for i := range 9 {
		a.Run.Feed("ci", "line "+string(rune('a'+i)))
	}
	a.RebuildPickerRows()

	a.CycleOutputFold() // peek -> full, on the whole block
	if !a.ResumeRun() {
		t.Fatal("v should have gone to the run")
	}
	if a.FoldOf("ci") != FoldFull {
		t.Errorf("the run view sees %v, want the full it was given", a.FoldOf("ci"))
	}
	if n := len(a.RunRows); n != 10 {
		t.Errorf("the run view has %d rows, want the task and its nine lines", n)
	}
}

// Reading is not disturbed by output arriving above: the cursor tracks the row it is on,
// not the number that row happened to have.
func TestTheCursorKeepsItsRowAsOutputArrives(t *testing.T) {
	a := pickerWithRun(t, "build", []string{"build", "ci", "test"})
	a.RebuildPickerRows()
	parkOn(t, a, "test") // below the run, so every new line pushes it down
	before := a.Cursor

	for i := range 4 {
		a.Run.Feed("build", "line "+string(rune('a'+i)))
	}
	a.RebuildPickerRows()

	if a.Cursor == before {
		t.Fatal("the rows should have moved; the test is not testing anything")
	}
	if name := a.Tasks[a.SelectedTask()].Name; name != "test" {
		t.Errorf("the cursor slid onto %q", name)
	}
}

// Every key that acts on "the task under the cursor" keeps working from inside a run: the
// block belongs to the task it hangs under, and that is the task `⏎`, `x` and `m` mean —
// the same rule the run view's `r` follows.
func TestTheTaskUnderTheCursorSurvivesTheBlock(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"})
	a.Run.Feed("ci", "compiling")
	a.RebuildPickerRows()

	at := -1
	for i, row := range a.PickerRows {
		if row.IsRun() && !row.Run.IsTask {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("no output row to stand on")
	}
	a.Cursor = at

	ti := a.SelectedTask()
	if ti < 0 || a.Tasks[ti].Name != "ci" {
		t.Errorf("selected task = %d, want ci", ti)
	}
	if n := a.SelectedNode(); n == nil || n.Label == "" {
		t.Error("and the node it came from is still reachable")
	}
}

// Slots are what make several runs possible at once, and the picker is the only screen
// that can show all of them: one block per task, each with its own output.
func TestEveryOpenSlotShowsUnderItsOwnTask(t *testing.T) {
	a := pickerWithRun(t, "build", []string{"build", "ci", "test"})
	a.Run.Feed("build", "compiling core")
	a.parkFocused()
	a.Run = run.Detached("test", run.GraphFrom(run.Edge{Parent: "test"}))
	a.Run.Feed("test", "--- FAIL: TestOrderTotal")
	a.RebuildPickerRows()

	for _, root := range []string{"build", "test"} {
		if len(blockRows(a, root)) == 0 {
			t.Errorf("%s is open in a slot and shows nothing", root)
		}
	}
}

// A run in a slot puts a fold glyph on its task's row: without one, the only way to find
// out whether there is anything under a row is to press the key and see.
func TestThePickerRowSaysThereIsARunUnderIt(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"})
	a.Run.Feed("ci", "compiling")
	a.RebuildPickerRows()

	if _, ok := a.InlineFold("ci"); !ok {
		t.Error("ci has a run and does not say so")
	}
	if _, ok := a.InlineFold("build"); ok {
		t.Error("build has no run and claims one")
	}

	frame := strings.Join(a.RenderHeadless(90, 20), "\n")
	if !strings.Contains(frame, a.Theme.Glyphs.FoldPeek) {
		t.Errorf("no peek glyph on the row:\n%s", frame)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The block hangs off its task's row on a guide, and its tasks hang off that — the tree's
// own vocabulary, so a run in the middle of a list reads as belonging to the row above it
// rather than floating between two.
func TestTheBlockHangsOffItsRow(t *testing.T) {
	a := pickerWithRun(t, "ci", []string{"build", "ci", "test"},
		run.Edge{Parent: "ci", Children: []string{"build", "test"}})
	a.Run.Feed("build", "compiling")
	a.Run.Feed("test", "testing")
	a.RebuildPickerRows()

	g := a.Theme.Glyphs
	var branches []string
	for _, row := range blockRows(a, "ci") {
		if row.Run.IsTask {
			branches = append(branches, row.Rail)
		}
	}
	if len(branches) != 2 {
		t.Fatalf("%d task rows in the block", len(branches))
	}
	if !strings.HasSuffix(branches[0], g.GuideBranch+" ") {
		t.Errorf("the first task should branch: %q", branches[0])
	}
	if !strings.HasSuffix(branches[1], g.GuideLast+" ") {
		t.Errorf("the last task should close the branch: %q", branches[1])
	}

	// `ci` is not the last row of its group here, so the rail continues past the block —
	// the same rule a wrapped description follows.
	if !strings.HasPrefix(branches[0], g.GuideVertical) {
		t.Errorf("the block should hang from its row: %q", branches[0])
	}
}
