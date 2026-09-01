package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
)

// appAt puts the cursor on a task in a real but empty directory: pressing enter starts a
// run that fails immediately, which is all these tests need — they assert the transition,
// not the output.
func appAt(t *testing.T, name string) *App {
	t.Helper()
	a := New(pivot.Fixture([]string{"backend:migrate", "backend:migrate:down", "backend:lint"}), t.TempDir())
	a.SetStateDir(t.TempDir())
	parkOn(t, a, name)
	return a
}

func press(a *App, k Key) { a.HandleKey(k) }

// groupRows is where the picker's group headers sit in its list.
func groupRows(a *App) []int {
	var out []int
	for i, row := range a.PickerRows {
		if !row.IsRun() && a.Tree.Nodes[a.Rows[row.Tree].Node].IsGroup() {
			out = append(out, i)
		}
	}
	return out
}

// rootLabels is the top-level rows of the tree, in the order they are read.
func rootLabels(a *App) []string {
	var out []string
	for _, r := range a.Tree.Roots {
		out = append(out, a.Tree.Nodes[r].Label)
	}
	return out
}

func rootOf(a *App) string {
	if a.Run == nil {
		return ""
	}
	return a.Run.Root
}

// `backend:migrate` is a group and a task. Space must fold it without running, so the two
// actions never compete for one key.
func TestSpaceFoldsAGroupThatIsAlsoATask(t *testing.T) {
	a := appAt(t, "backend:migrate")
	if n := a.SelectedNode(); n == nil || !n.IsGroup() {
		t.Fatal("expected a group under the cursor")
	}
	before := len(a.Rows)

	press(a, Char(' '))

	if len(a.Rows) >= before {
		t.Errorf("the subtree should have collapsed: %d -> %d", before, len(a.Rows))
	}
	if a.Status != "" {
		t.Errorf("space should not run anything: %q", a.Status)
	}
}

// …and enter must run it from its own header, without expanding. That is the whole reason
// the subtree does not relist the task inside itself.
func TestEnterRunsAGroupThatIsAlsoATaskWithoutFolding(t *testing.T) {
	a := appAt(t, "backend:migrate")
	before := len(a.Rows)

	press(a, Enter())
	defer a.KillAll()

	if len(a.Rows) != before {
		t.Errorf("enter should leave the picker's folds alone: %d -> %d", before, len(a.Rows))
	}
	if a.Screen != ScreenPicker {
		t.Errorf("running should not take the screen: %v", a.Screen)
	}
	if rootOf(a) != "backend:migrate" {
		t.Errorf("the group header should have run itself: %q", rootOf(a))
	}
}

// A pure group has nothing to run; say so rather than silently folding.
func TestEnterOnAPureGroupExplainsItself(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Cursor = 0 // the `backend` namespace row
	if a.SelectedTask() >= 0 {
		t.Fatal("row 0 should be a pure group")
	}
	before := len(a.Rows)

	press(a, Enter())

	if len(a.Rows) != before {
		t.Error("nothing should have folded")
	}
	if !strings.Contains(a.Status, "not one") {
		t.Errorf("status = %q", a.Status)
	}
}

// Enter runs the leaf and leaves you in the list, with the run unfolded under the row it
// came from. Taking the screen was the old behaviour and the reason starting a second task
// meant going back for it.
func TestEnterRunsAPlainLeaf(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Enter())
	defer a.KillAll()
	if a.Screen != ScreenPicker || rootOf(a) != "backend:lint" {
		t.Errorf("screen = %v root = %q", a.Screen, rootOf(a))
	}
	if !strings.Contains(a.Status, "v") {
		t.Errorf("the status should say where the whole screen is: %q", a.Status)
	}
}

func appWithLiveRun(t *testing.T, name string) *App {
	t.Helper()
	a := appAt(t, "backend:lint")
	a.Run = run.Detached(name, run.GraphFrom(run.Edge{Parent: name}))
	a.Screen = ScreenPicker
	return a
}

// Selecting the task that is already running means "show me it", never "start another":
// a second run would take down the first, and on a half-finished deploy that is the worst
// thing this tool could do. From the picker the run is already on screen under the row, so
// showing it means staying and saying so; from the run view it still means going to it.
func TestRunningTheTaskAlreadyRunningDoesNotRestartIt(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	before := a.Run.Started

	a.RequestRun("backend:lint", nil)

	if a.Screen != ScreenPicker {
		t.Errorf("the picker should have kept the screen: %v", a.Screen)
	}
	if !strings.Contains(a.Status, "already running") {
		t.Errorf("status = %q", a.Status)
	}
	if a.Confirm != nil {
		t.Error("and should not have asked about anything")
	}
	if !a.Run.Started.Equal(before) {
		t.Error("the same run, not a fresh one")
	}

	a.Screen = ScreenRun
	a.RequestRun("backend:lint", nil)
	if a.Screen != ScreenRun {
		t.Error("from the run view it should still be the run view")
	}
	if !a.Run.Started.Equal(before) {
		t.Error("still the same run")
	}
}

// Starting something else while a run is live parks the first rather than killing it. This
// is what makes a long-lived slot — a compose stack, a dev server — usable: the tool has to
// stay usable while it is up, not only before and after.
func TestStartingAnotherTaskMidRunParksTheFirst(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")

	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()

	if a.Confirm != nil {
		t.Error("nothing to ask about any more")
	}
	if rootOf(a) != "backend:migrate" {
		t.Errorf("the new run should be the one on screen: %q", rootOf(a))
	}
	var parked []string
	for _, p := range a.Parked {
		parked = append(parked, p.Run.Root)
	}
	if !reflect.DeepEqual(parked, []string{"backend:lint"}) {
		t.Errorf("the first should still be open, not killed: %v", parked)
	}
	var bar []string
	for _, s := range a.Slots() {
		bar = append(bar, s.Root)
	}
	if !reflect.DeepEqual(bar, []string{"backend:lint", "backend:migrate"}) {
		t.Errorf("the bar should list them in the order they were started: %v", bar)
	}
}

// `v` goes to what is running, not to whatever slot you left last.
func TestVGoesToTheRunningSlotNotTheLastOne(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	// Start a second run, which parks the lint, then let the new one finish.
	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()
	a.Run.Finish(0)
	if rootOf(a) != "backend:migrate" {
		t.Fatalf("the finished one should be on screen: %q", rootOf(a))
	}

	a.Screen = ScreenPicker
	if !a.ResumeRun() {
		t.Fatal("resume refused")
	}

	if a.Screen != ScreenRun {
		t.Errorf("screen = %v", a.Screen)
	}
	if rootOf(a) != "backend:lint" {
		t.Errorf("it should have gone to the one still going: %q", rootOf(a))
	}
}

// Already watching something live: stay there. Two running slots must not make `v` hop
// away from the one you are looking at.
func TestVLeavesALiveSlotAlone(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()
	a.Screen = ScreenPicker

	if !a.ResumeRun() {
		t.Fatal("resume refused")
	}
	if rootOf(a) != "backend:migrate" {
		t.Errorf("both are live, so the focused one keeps the screen: %q", rootOf(a))
	}
}

// With nothing running, `v` still shows the last run — "what just happened" beats refusing
// to go anywhere.
func TestVFallsBackToTheLastRunWhenNothingIsLive(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.Run.Finish(1)
	a.Screen = ScreenPicker

	if !a.ResumeRun() {
		t.Fatal("resume refused")
	}
	if a.Screen != ScreenRun || rootOf(a) != "backend:lint" {
		t.Errorf("screen = %v root = %q", a.Screen, rootOf(a))
	}
}

// Re-running a task that is still going is a restart, and a restart kills what is in that
// slot — the one case left where taskui stops something for you, so it asks.
func TestRestartingALiveSlotAsksFirst(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RunCursor = 0
	a.RebuildRunRows()

	a.RerunSelected()

	if a.Confirm == nil || a.Confirm.Kind != ConfirmRun || a.Confirm.Reason != WouldStopRunning {
		t.Errorf("should be waiting on a yes, and saying why: %+v", a.Confirm)
	}
}

// Switching slots must not lose where you were reading in the one you left.
func TestAParkedSlotKeepsItsViewState(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RunExpand("backend:lint")
	a.RebuildRunRows()
	a.Following = false

	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()
	if a.RunIsExpanded("backend:lint") {
		t.Error("the new slot should start with its own fold state")
	}

	a.CycleSlot(1)

	if rootOf(a) != "backend:lint" {
		t.Fatalf("root = %q", rootOf(a))
	}
	if !a.RunIsExpanded("backend:lint") {
		t.Error("coming back should restore what was unfolded")
	}
	if a.Following {
		t.Error("and whether you were following")
	}
}

// Quitting takes every slot with it, not just the one on screen.
func TestQuittingStopsBackgroundRunsToo(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RequestRun("backend:migrate", nil)
	if !a.AnyInFlight() {
		t.Fatal("nothing is in flight")
	}

	if a.quit() {
		t.Error("should not leave until the question is answered")
	}
	if !a.ConfirmYes() {
		t.Error("yes means quit")
	}
	a.shutdown()

	for _, p := range a.Parked {
		if !p.Run.Cancelled() {
			t.Errorf("%s was orphaned rather than cancelled", p.Run.Root)
		}
	}
	if a.Run == nil || !a.Run.Cancelled() {
		t.Error("the focused run was not cancelled")
	}
}

// `q` with runs in flight reaches slots you are not looking at, so it asks — and anything
// other than `y` leaves both the runs and the tool where they were.
func TestQuittingAsksBeforeStoppingAnything(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()

	if a.quit() {
		t.Error("should not have left")
	}
	if a.Confirm == nil || a.Confirm.Kind != ConfirmQuit || a.Confirm.Live != 2 {
		t.Fatalf("confirm = %+v", a.Confirm)
	}

	a.ConfirmNo()

	if a.Confirm != nil {
		t.Error("the question should be gone")
	}
	if a.InFlightCount() != 2 {
		t.Errorf("nothing should have been signalled: %d in flight", a.InFlightCount())
	}
	if a.Run.Cancelled() {
		t.Error("the focused run was signalled anyway")
	}
}

// It asks with nothing running too. There is nothing to lose, so the prompt carries no
// warning — but `q` must not mean "gone" one keystroke after the last task finished, which
// is exactly when you are still reading the output.
func TestQuittingAsksEvenWithNothingRunning(t *testing.T) {
	a := appAt(t, "backend:lint")

	if a.quit() {
		t.Error("should not have left on the first press")
	}
	if a.Confirm == nil || a.Confirm.Kind != ConfirmQuit || a.Confirm.Live != 0 {
		t.Fatalf("confirm = %+v", a.Confirm)
	}

	a.ConfirmNo()
	if a.Confirm != nil {
		t.Error("`n` should put it away")
	}

	a.quit()
	if !a.ConfirmYes() {
		t.Error("`y` is what leaves")
	}
}

// `esc` means back out of this, and in the picker there is nothing left to back out of —
// which is not the same as "leave".
func TestEscDoesNotQuitAndSaysSoOnTheSecondPress(t *testing.T) {
	a := appAt(t, "backend:lint")

	press(a, Esc())
	if a.Confirm != nil {
		t.Error("nothing should have been asked")
	}
	if a.Status != "" {
		t.Errorf("the first press should be silent: %q", a.Status)
	}

	press(a, Esc())
	if !strings.Contains(a.Status, "press q to quit") {
		t.Errorf("the second press should explain: %q", a.Status)
	}

	// Still there, and still not quitting.
	press(a, Esc())
	if a.Confirm != nil {
		t.Error("still should not have asked")
	}
}

// The streak is a run of presses, not a tally. Anything in between resets it.
func TestAnythingElseBreaksTheEscStreak(t *testing.T) {
	a := appAt(t, "backend:lint")

	press(a, Esc())
	press(a, Char('j'))
	if a.EscStreak != 0 {
		t.Error("`j` should have reset it")
	}

	press(a, Esc())
	if a.Status != "" {
		t.Errorf("so this should be a first press again: %q", a.Status)
	}
}

// `esc` still does its real job: it clears the filter rather than counting towards a quit
// hint, and only starts counting once there is nothing left to dismiss.
func TestEscClearsAFilterBeforeItCounts(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Filtering = true
	a.PushQuery('m')
	if a.Query == "" {
		t.Fatal("no filter to clear")
	}

	press(a, Esc())
	if a.Query != "" {
		t.Error("the filter should have gone first")
	}
	if a.EscStreak != 0 {
		t.Error("and it should not have counted as an escape")
	}
}

// Stopping everything is reachable without leaving — and it too asks, because most of what
// it reaches is not on screen.
func TestStopAllAsksThenStopsEverySlot(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()

	a.RequestStopAll()
	if a.Confirm == nil || a.Confirm.Kind != ConfirmStopAll || a.Confirm.Live != 2 {
		t.Fatalf("confirm = %+v", a.Confirm)
	}

	if a.ConfirmYes() {
		t.Error("stopping is not quitting")
	}

	if !a.Run.Cancelled() {
		t.Error("the focused run was not cancelled")
	}
	for _, p := range a.Parked {
		if !p.Run.Cancelled() {
			t.Errorf("%s was not cancelled", p.Run.Root)
		}
	}
	if a.Screen != ScreenPicker {
		t.Errorf("and we should still be here: %v", a.Screen)
	}
}

// The picker can stop a run it is not showing, addressed by task name — otherwise killing
// a background stack means loading its buffer first just to press a key.
func TestThePickerStopsARunItIsNotShowing(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")
	a.RequestRun("backend:migrate", nil)
	defer a.KillAll()
	a.Screen = ScreenPicker

	// `backend:lint` is the parked one now.
	a.CancelTask("backend:lint")

	found := false
	for _, p := range a.Parked {
		if p.Run.Root == "backend:lint" {
			found = p.Run.Cancelled()
		}
	}
	if !found {
		t.Error("the parked run should have taken the signal")
	}
	if a.Run.Cancelled() {
		t.Error("and the one on screen should have been left alone")
	}
	if a.Screen != ScreenPicker {
		t.Error("without switching to it")
	}
}

// A second `x` escalates. SIGTERM is catchable and plenty of things catch it, so a key
// that does nothing the second time you press it is not good enough.
func TestStoppingTwiceEscalatesToAKill(t *testing.T) {
	a := appWithLiveRun(t, "backend:lint")

	a.CancelRun()
	if !a.Run.Cancelled() {
		t.Error("not cancelled")
	}
	if a.Run.Killed() {
		t.Error("the first press should be polite")
	}

	a.CancelRun()
	if !a.Run.Killed() {
		t.Error("the second one should not be")
	}
}

// Naming a task that has no slot says so rather than silently doing nothing.
func TestStoppingATaskThatIsNotRunningSaysSo(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.CancelTask("backend:migrate")
	if a.Status != "`backend:migrate` is not running" {
		t.Errorf("status = %q", a.Status)
	}
}

// `o` folds one thing, `⇧O` folds the lot — and both are toggles, so the same key gets you
// back.
func TestOFoldsOneAndShiftOFoldsEverything(t *testing.T) {
	// appAt leaves the tree fully open.
	a := appAt(t, "backend:lint")
	open := len(a.Rows)

	press(a, Char('O'))
	closed := len(a.Rows)
	if closed >= open {
		t.Errorf("everything should have folded: %d vs %d", closed, open)
	}

	press(a, Char('O'))
	if len(a.Rows) != open {
		t.Errorf("and unfolded again: %d vs %d", len(a.Rows), open)
	}

	// `o` on a group takes only that group with it.
	a.Cursor = 0
	if n := a.SelectedNode(); n == nil || !n.IsGroup() {
		t.Fatal("row 0 should be a group")
	}
	press(a, Char('o'))
	after := len(a.Rows)
	if after >= open {
		t.Errorf("`o` should have folded the group under the cursor: %d", after)
	}

	press(a, Char('o'))
	if len(a.Rows) <= after {
		t.Error("and `o` again should have unfolded it")
	}
}

// The same key in the run view, over a task's captured output — walking the same three
// states `o` does, but for every task at once.
func TestShiftOMovesEveryTaskThroughTheFoldStates(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Screen = ScreenRun
	a.OpenRunForTest(run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"})))
	for i := range 9 {
		a.Run.Feed("backend:lint", fmt.Sprintf("error %d", i))
	}
	a.RebuildRunRows()

	// Resting: the task and the last five of its nine lines.
	if len(a.RunRows) != 6 {
		t.Fatalf("rows = %d", len(a.RunRows))
	}

	press(a, Char('O'))
	if len(a.RunRows) != 10 {
		t.Errorf("everything, wrapped: %d", len(a.RunRows))
	}

	press(a, Char('O'))
	if len(a.RunRows) != 1 {
		t.Errorf("and down to the shape of the run: %d", len(a.RunRows))
	}

	press(a, Char('O'))
	if len(a.RunRows) != 6 {
		t.Errorf("round to the peek again: %d", len(a.RunRows))
	}
}

// `gg` and `G` in the run view, which is where the rows actually pile up.
func TestGgAndGReachTheRunViewToo(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Screen = ScreenRun
	a.OpenRunForTest(run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"})))
	for i := range 40 {
		a.Run.Feed("backend:lint", fmt.Sprintf("line %d", i))
	}
	// Open it fully, so there is a long way to travel.
	press(a, Char('O'))
	last := len(a.RunRows) - 1
	if last <= 5 {
		t.Fatalf("%d rows is not enough to test a jump", last)
	}

	press(a, Char('G'))
	if a.RunCursor != last {
		t.Errorf("`G` should go to the last row: %d", a.RunCursor)
	}

	press(a, Char('g'))
	if !a.PendingG || a.RunCursor != last {
		t.Error("armed, and nothing should have moved")
	}

	press(a, Char('g'))
	if a.RunCursor != 0 {
		t.Errorf("`gg` should go to the first: %d", a.RunCursor)
	}
	if a.PendingG {
		t.Error("and disarm")
	}
}

// Typing at a task owns every key, `g` included — otherwise answering a prompt with `go`
// would jump the view instead of reaching the child.
func TestARunPromptStillSwallowsG(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Screen = ScreenRun
	a.OpenRunForTest(run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"})))
	a.Searching = true
	press(a, Char('g'))
	if a.PendingG {
		t.Error("the search line should have taken it")
	}
	if a.SearchInput != "g" {
		t.Errorf("search input = %q", a.SearchInput)
	}
}

// `gg` and `G`, as in vim. A lone `g` only arms the pair.
func TestGgAndGJumpToTheEnds(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	last := len(a.Rows) - 1

	press(a, Char('G'))
	if a.Cursor != last {
		t.Errorf("cursor = %d, want %d", a.Cursor, last)
	}

	press(a, Char('g'))
	if !a.PendingG || a.Cursor != last {
		t.Error("armed, and nothing should have moved")
	}

	press(a, Char('g'))
	if a.Cursor != 0 || a.PendingG {
		t.Errorf("cursor = %d pending = %v", a.Cursor, a.PendingG)
	}
}

// A forgotten `g` must not swallow the next keystroke.
func TestALoneGIsDisarmedByAnythingElse(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)

	before := a.Cursor
	press(a, Char('g'))
	if !a.PendingG {
		t.Fatal("not armed")
	}
	press(a, Char('j'))
	if a.PendingG {
		t.Error("should have disarmed")
	}
	if a.Cursor != before+1 {
		t.Error("and `j` should still have moved")
	}
}

// `{` and `}`, as in vim: the next group header, whatever lies between here and it.
func TestBracesJumpBetweenGroups(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	groups := groupRows(a)
	if len(groups) < 2 {
		t.Fatalf("the fixture needs two groups to jump between: %v", groups)
	}

	a.Cursor = groups[0]
	press(a, Char('}'))
	if a.Cursor != groups[1] {
		t.Errorf("`}` should have reached the next group at %d: %d", groups[1], a.Cursor)
	}
	press(a, Char('{'))
	if a.Cursor != groups[0] {
		t.Errorf("`{` should have come back to %d: %d", groups[0], a.Cursor)
	}
}

// Past the last group there is no next one, and a movement key that does nothing at all
// reads as broken — so it runs out at the end of the list, the way vim's does at the end
// of the file.
func TestBracesRunOutAtTheEnds(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	groups := groupRows(a)
	last := len(a.PickerRows) - 1

	a.Cursor = groups[len(groups)-1]
	press(a, Char('}'))
	if a.Cursor != last {
		t.Errorf("cursor = %d, want the last row %d", a.Cursor, last)
	}
	press(a, Char('{'))
	press(a, Char('{'))
	press(a, Char('{'))
	if a.Cursor != 0 {
		t.Errorf("cursor = %d, want the first row", a.Cursor)
	}
}

// The block a run prints is exactly what you press `}` to get past, so its rows are not
// somewhere the jump can land.
func TestBracesStepOverAnUnfoldedRun(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	a.Run = run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"}))
	a.Run.Feed("backend:lint", "compiling")
	a.RebuildRunRows()
	a.RebuildPickerRows()
	if len(blockRows(a, "backend:lint")) == 0 {
		t.Fatal("no run rows to step over")
	}
	groups := groupRows(a)

	a.Cursor = groups[0]
	press(a, Char('}'))
	if a.Cursor != groups[1] {
		t.Errorf("cursor = %d, want the next group at %d", a.Cursor, groups[1])
	}
}

// `t` on the `?` screen searches the keymap itself: 140 bindings over eight screens is a
// page and a half of scrolling to answer "which key copies a line".
func TestTFindsABindingInTheHelp(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	press(a, Char('t'))
	if !a.HelpFinding {
		t.Fatal("the find prompt should be open")
	}
	if a.Screen != ScreenHelp {
		t.Errorf("and the help should still be up: %v", a.Screen)
	}

	for _, c := range "copy" {
		press(a, Char(c))
	}
	if a.HelpQuery != "copy" {
		t.Errorf("query = %q", a.HelpQuery)
	}
	page := strings.Join(a.RenderHeadless(96, 30), "\n")
	if !strings.Contains(page, "copy the line under the cursor") {
		t.Errorf("the binding it was looking for is not there:\n%s", page)
	}
	// And it is a search, not a scroll: what did not match is gone.
	if strings.Contains(page, "quit — always asks first") {
		t.Errorf("the rest of the keymap should have been filtered out:\n%s", page)
	}
}

// Every line the find leaves has to contain what you typed. It did not: the section title
// was part of the haystack, so `ke` — three letters of `Picker` — kept all 27 of that
// section's bindings, and not one of them says `ke`.
func TestEveryBindingTheFindLeavesSaysWhatYouTyped(t *testing.T) {
	for _, query := range []string{"ke", "run", "fold", "esc", "⇧"} {
		for _, section := range keys.Sections {
			for _, b := range section.Bindings {
				if !helpMatch(b, query) {
					continue
				}
				shown := strings.ToLower(b.Keys + " " + b.What)
				if !strings.Contains(shown, strings.ToLower(query)) {
					t.Errorf("%q kept `%s  %s`, which does not say it", query, b.Keys, b.What)
				}
			}
		}
	}
}

// While the prompt is open every key is text — including the ones that are bindings out
// there. Typing `quit` to look up how to quit must not quit.
func TestTheHelpFindPromptSwallowsItsOwnBindings(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	press(a, Char('t'))

	for _, c := range "quit" {
		if press(a, Char(c)); a.Screen != ScreenHelp {
			t.Fatalf("`%c` escaped the prompt: screen = %v", c, a.Screen)
		}
	}
	if a.HelpQuery != "quit" {
		t.Errorf("query = %q", a.HelpQuery)
	}
	if a.Confirm != nil {
		t.Error("and it must not have asked to quit")
	}
}

// `⏎` keeps what the query narrowed the table to and gives the scroll keys back, as the
// picker's filter does. `esc` then drops the query rather than the screen: backing out of a
// search must not throw away the keymap you were reading.
func TestEnterKeepsTheFindAndEscDropsItFirst(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	press(a, Char('t'))
	press(a, Char('y'))

	press(a, Enter())
	if a.HelpFinding || a.HelpQuery != "y" {
		t.Errorf("finding = %v query = %q", a.HelpFinding, a.HelpQuery)
	}
	press(a, Char('j'))
	if a.HelpOffset != 1 {
		t.Errorf("the scroll keys should be back: offset = %d", a.HelpOffset)
	}

	press(a, Esc())
	if a.Screen != ScreenHelp {
		t.Fatalf("the first `esc` should only clear the query: %v", a.Screen)
	}
	if a.HelpQuery != "" {
		t.Errorf("query = %q", a.HelpQuery)
	}
	press(a, Esc())
	if a.Screen != ScreenPicker {
		t.Errorf("the second should close the screen: %v", a.Screen)
	}
}

// A query does not outlive the screen: `?` is somewhere you arrive to look one thing up.
func TestClosingTheHelpForgetsTheQuery(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	press(a, Char('t'))
	press(a, Char('y'))
	press(a, Enter())

	press(a, Char('?'))
	press(a, Char('?'))

	if a.HelpQuery != "" || a.HelpFinding {
		t.Errorf("query = %q finding = %v", a.HelpQuery, a.HelpFinding)
	}
}

// `p` took over the pivot so `gg` could have `g`.
func TestPTogglesThePivot(t *testing.T) {
	a := appAt(t, "backend:lint")
	before := a.ModeLabel()
	press(a, Char('p'))
	if a.ModeLabel() == before {
		t.Error("the pivot did not toggle")
	}
}

// `⇧S` is the other half of `p`: the pivot decides what the tree is, the order decides what
// sits above what inside it. It cycles every ordering and wraps back to the default.
func TestShiftSCyclesEveryOrdering(t *testing.T) {
	a := appAt(t, "backend:lint")
	if a.Order.By != pivot.ByNatural {
		t.Fatalf("a fresh app should start on the default order: %q", a.Order.By)
	}

	var seen []pivot.By
	for range len(pivot.Orders()) {
		press(a, Char('S'))
		seen = append(seen, a.Order.By)
	}
	if !reflect.DeepEqual(seen, append(pivot.Orders()[1:], pivot.ByNatural)) {
		t.Errorf("the cycle went %v", seen)
	}
}

// Re-sorting is a way of looking for something, so the thing you were looking at has to
// survive it — the same rule `p` follows.
func TestTheSelectionSurvivesAReSort(t *testing.T) {
	a := appAt(t, "backend:migrate:down")
	before := a.SelectedTask()
	if before == pivot.NoTask {
		t.Fatal("nothing selected to keep")
	}

	press(a, Char('S'))

	if a.SelectedTask() != before {
		t.Errorf("the cursor left the task: %d -> %d", before, a.SelectedTask())
	}
}

// And it has to actually re-sort: `size` puts the biggest group first, whatever the pivot
// would have done on its own.
func TestOrderingChangesWhatSitsAboveWhat(t *testing.T) {
	a := appWith(t, []string{"zeta:one", "zeta:two", "zeta:three", "alpha:one"})
	a.Order.By = pivot.ByName
	a.Rebuild(pivot.NoTask)
	if got := rootLabels(a); !equalStrings(got, []string{"alpha", "zeta"}) {
		t.Fatalf("by name the roots are %v", got)
	}

	a.Order.By = pivot.BySize
	a.Rebuild(pivot.NoTask)
	if got := rootLabels(a); !equalStrings(got, []string{"zeta", "alpha"}) {
		t.Errorf("by size the roots are %v, want the three-task group first", got)
	}
}

// Leaving a run must not be a one-way door.
func TestVGoesBackToTheLiveRun(t *testing.T) {
	a := appWithLiveRun(t, "deploy:local:env")
	press(a, Char('v'))
	if a.Screen != ScreenRun || rootOf(a) != "deploy:local:env" {
		t.Errorf("screen = %v root = %q", a.Screen, rootOf(a))
	}
}

// Rebinding has to change what the key actually does, not just what `?` claims.
func TestAReboundKeyDispatchesToTheNewKey(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Keymap.Rebind(keys.Pivot, keys.Plain('z'))
	before := a.ModeLabel()

	press(a, Char('p'))
	if a.ModeLabel() != before {
		t.Error("the old key should no longer pivot")
	}

	press(a, Char('z'))
	if a.ModeLabel() == before {
		t.Error("the new one should")
	}
}

// An action means the same thing wherever it is offered.
func TestRebindingAppliesOnEveryScreenThatOffersIt(t *testing.T) {
	a := appAt(t, "backend:lint")
	// Derived rather than hardcoded: rebinding onto a key some action already uses shadows
	// it, which is a different behaviour with its own test in the keys package — and a
	// hardcoded "free" key stops being free as the keymap grows.
	key := rune(0)
	for c := '!'; c <= '~'; c++ {
		if a.Keymap.Picker(keys.Plain(c)) == keys.None && a.Keymap.Run(keys.Plain(c)) == keys.None {
			key = c
			break
		}
	}
	if key == 0 {
		t.Fatal("no free key to rebind onto")
	}
	a.Keymap.Rebind(keys.Help, keys.Plain(key))
	press(a, Char(key))
	if a.Screen != ScreenHelp {
		t.Errorf("screen = %v", a.Screen)
	}
}

// The regression this pins: `i` used to refuse to type at a normal run and re-ran it
// interactively instead, which on a half-finished deploy is the worst possible move. stdin
// reaches the child either way, so `i` must open input mode regardless.
func TestITypesAtANormalRunRatherThanRestartingIt(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Run = run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"}))
	a.Screen = ScreenRun
	if a.Run.Interactive {
		t.Fatal("expected a normal, buffered run")
	}

	press(a, Char('i'))

	if !a.SendingInput {
		t.Error("input mode should have opened")
	}
	if a.InteractiveNext {
		t.Error("nothing should have been re-run")
	}
	if rootOf(a) != "backend:lint" {
		t.Error("the same run should still be there")
	}
}

// …while ⇧I is the deliberate restart, for when seeing the prompt matters more.
func TestShiftIReRunsInteractively(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Run = run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"}))
	a.Screen = ScreenRun

	press(a, Char('I'))
	defer a.KillAll()

	if !a.InteractiveNext {
		t.Error("should be armed for interactive")
	}
	if a.SendingInput {
		t.Error("and not typing at the old one")
	}
}

// Leaving the run view goes back to the picker without discarding the run — it is still
// going, and still there when you return.
func TestEscReturnsToThePickerAndKeepsTheRun(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Enter())
	defer a.KillAll()
	press(a, Esc())
	if a.Screen != ScreenPicker {
		t.Errorf("screen = %v", a.Screen)
	}
	if a.Run == nil {
		t.Error("the run should survive")
	}
}

// The whole point, against real processes: two long-lived tasks running at once, both
// reachable, and both reaped on the way out.
func TestTwoLongRunningTasksShareTheTool(t *testing.T) {
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not on PATH")
	}
	dir := t.TempDir()
	body := "version: \"3\"\ntasks:\n  " +
		"up:\n    cmds: ['echo stack up', 'sleep 30']\n  " +
		"logs:\n    cmds: ['echo tailing', 'sleep 30']\n"
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(pivot.Fixture([]string{"up", "logs"}), dir)
	a.SetStateDir(t.TempDir())
	a.RequestRun("up", nil)
	a.RequestRun("logs", nil)
	defer a.KillAll()

	// Wait for both to have actually said something.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.PollRun()
		talking := 0
		for _, s := range a.Slots() {
			if s.Status == run.Running {
				talking++
			}
		}
		quiet := false
		for _, p := range a.Parked {
			if len(p.Run.Tasks) == 0 {
				quiet = true
			}
		}
		if talking == 2 && !quiet {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if a.InFlightCount() != 2 {
		t.Fatalf("both should still be going: %d", a.InFlightCount())
	}
	if len(a.Slots()) != 2 {
		t.Fatalf("both should have a slot: %d", len(a.Slots()))
	}

	// Switching reaches the one that is not on screen, and its output is there.
	a.CycleSlot(1)
	if rootOf(a) != "up" {
		t.Errorf("root = %q", rootOf(a))
	}
	a.PollRun()

	if a.quit() {
		t.Error("should ask, because both are still going")
	}
	if !a.ConfirmYes() {
		t.Error("yes means quit")
	}
	a.shutdown()

	stoppedAt := time.Now().Add(15 * time.Second)
	for time.Now().Before(stoppedAt) && a.AnyInFlight() {
		a.PollRun()
		time.Sleep(50 * time.Millisecond)
	}
	if a.AnyInFlight() {
		t.Error("both process groups should have been reaped, not orphaned")
	}
}

// --- key translation ----------------------------------------------------------------
//
// fromTea is the whole surface Bubble Tea touches, so it is where a version bump goes
// wrong. These pin the shapes the terminal actually sends, in both the legacy encoding and
// the disambiguated one — the same keystroke arrives differently depending on which the
// terminal negotiated, and both have to end up at the same binding.

func TestKeysArriveTheSameFromEitherEncoding(t *testing.T) {
	for _, tc := range []struct {
		what string
		msg  tea.KeyPressMsg
		want Key
	}{
		// Legacy: a plain character is its own byte.
		{"a", tea.KeyPressMsg{Code: 'a', Text: "a"}, Char('a')},
		// A capital arrives capitalised, because the terminal applies the shift. Carrying the
		// modifier as well would make `⇧G` a different binding from `G`.
		{"G", tea.KeyPressMsg{Code: 'G', Text: "G"}, Char('G')},
		// The same key under key disambiguation, where the shift is reported separately: the
		// text is still `G`, so the shift is spent and must not survive into the chord.
		{"G, disambiguated", tea.KeyPressMsg{Code: 'g', Text: "G", Mod: tea.ModShift}, Char('G')},
		// Caps lock is a state the terminal has already applied, not a modifier to bind to.
		{"caps-locked G", tea.KeyPressMsg{Code: 'g', Text: "G", Mod: tea.ModCapsLock}, Char('G')},
		{"space", tea.KeyPressMsg{Code: ' ', Text: " "}, Char(' ')},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, Enter()},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, Esc()},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, Tab()},
		// v2 has no shift+⇥ code of its own; it is ⇥ with shift held.
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, Key{kind: keyBackTab}},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, Key{kind: keyBackspace}},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, Key{kind: keyUp}},
		// ⌃c is the letter plus the modifier — the control code is not a key of its own.
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, Key{kind: keyChar, ch: 'c', mods: keys.ModCtrl}},
		// Nothing the dispatch table has a use for.
		{"f1", tea.KeyPressMsg{Code: tea.KeyF1}, Key{kind: keyOther}},
	} {
		if got := fromTea(tc.msg); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.what, got, tc.want)
		}
	}
}

// The point of the upgrade: ⇧␣ is a keystroke of its own, distinguishable from ␣.
func TestShiftSpaceIsNotSpace(t *testing.T) {
	plain := fromTea(tea.KeyPressMsg{Code: ' ', Text: " "})
	shifted := fromTea(tea.KeyPressMsg{Code: ' ', Text: " ", Mod: tea.ModShift})
	if plain == shifted {
		t.Fatal("⇧␣ and ␣ still arrive as the same key")
	}
	// And the literal `case` for space must not swallow it on the way to the keymap, or a
	// binding onto ⇧␣ would never be consulted.
	if shifted.isChar(' ') {
		t.Error("⇧␣ matched the bare-space case")
	}
	if !plain.isChar(' ') {
		t.Error("␣ stopped matching the bare-space case")
	}
}

// A modified key is not text: ⌃z used to reach a running task as a `z`.
func TestModifiedKeysAreNotTypedAtTheChild(t *testing.T) {
	if fromTea(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}).typed() {
		t.Error("⌃z counted as typing")
	}
	if !fromTea(tea.KeyPressMsg{Code: 'Z', Text: "Z"}).typed() {
		t.Error("a capital is typing")
	}
}

// A modifier can now carry a binding, which is what the upgrade was for.
func TestAnActionCanBeBoundToAModifiedKey(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Keymap.Rebind(keys.Help, keys.Chord{Key: ' ', Mods: keys.ModShift})

	press(a, fromTea(tea.KeyPressMsg{Code: ' ', Text: " "}))
	if a.Screen == ScreenHelp {
		t.Fatal("plain space should still fold, not open help")
	}
	press(a, fromTea(tea.KeyPressMsg{Code: ' ', Text: " ", Mod: tea.ModShift}))
	if a.Screen != ScreenHelp {
		t.Errorf("screen = %v — ⇧␣ did not reach its binding", a.Screen)
	}
}

// --- the wheel ----------------------------------------------------------------------

// Scrolling is defined as arrowing, so it works on every screen without any screen
// knowing about it — and the picker is where you can see it move.
func TestTheWheelMovesTheCursor(t *testing.T) {
	a := sample(t)
	a.SetFoldAll(true)
	a.Cursor = 0

	a.handleWheel(tea.MouseWheelDown)
	if a.Cursor != wheelStep {
		t.Errorf("a notch moved the cursor to %d, want %d", a.Cursor, wheelStep)
	}

	a.handleWheel(tea.MouseWheelUp)
	if a.Cursor != 0 {
		t.Errorf("back up the same distance should be 0, got %d", a.Cursor)
	}

	// Clamped like every other movement, rather than running off the top.
	a.handleWheel(tea.MouseWheelUp)
	if a.Cursor != 0 {
		t.Errorf("cursor = %d past the top", a.Cursor)
	}

	// And through Update, which is the path the terminal actually takes.
	a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if a.Cursor != wheelStep {
		t.Errorf("a wheel message through Update left the cursor at %d", a.Cursor)
	}
}

// A confirmation reads every key that is not `y` as "no". A wheel is not an answer, and a
// question that vanished because the mouse moved would be a question you never answered.
func TestTheWheelDoesNotAnswerAConfirmation(t *testing.T) {
	a := sample(t)
	a.Confirm = &Confirm{Kind: ConfirmRun, Name: "deploy", Reason: TouchesProduction}

	a.handleWheel(tea.MouseWheelDown)

	if a.Confirm == nil {
		t.Error("scrolling dismissed the confirmation")
	}
}

// Sideways wheels exist and nothing here scrolls sideways; the cursor must not move for one.
func TestASidewaysWheelDoesNothing(t *testing.T) {
	a := sample(t)
	a.SetFoldAll(true)
	a.Cursor = 2

	a.handleWheel(tea.MouseWheelLeft)
	a.handleWheel(tea.MouseWheelRight)

	if a.Cursor != 2 {
		t.Errorf("cursor = %d; a horizontal wheel should not move it", a.Cursor)
	}
}

// The frame is what asks the terminal for mouse events. Without that ask the wheel never
// reaches this program at all — the terminal keeps it and scrolls its own scrollback, which
// inside Neovim means scrolling the buffer taskui is being drawn into.
func TestTheFrameAsksForTheMouseUnlessTurnedOff(t *testing.T) {
	a := sample(t)
	a.Width, a.Height = 80, 24

	if got := a.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell motion", got)
	}

	a.Mouse = false
	if got := a.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("`mouse: off` still asked for %v", got)
	}
}
