package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

// archived saves a finished run of one task into the app's own state directory, aged so
// that several of them in one test second still get distinct ids.
func archived(t *testing.T, a *App, task string, ok bool, ago int, lines ...string) {
	t.Helper()
	r := run.Detached(task, run.GraphFrom(run.Edge{Parent: task}))
	for _, l := range lines {
		r.Feed(task, l)
	}
	exit := 0
	if !ok {
		r.ApplyFailed(task)
		exit = 1
	}
	r.Finish(exit)
	r.Duration = time.Duration(ago) * time.Second
	r.HasDuration = true
	if _, err := store.Save(a.StateDir(), a.Root, r); err != nil {
		t.Fatal(err)
	}
}

// --- the timeline -------------------------------------------------------------------

func TestTheTimelineChartsTheTaskUnderTheCursor(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "clean")
	archived(t, a, "backend:lint", false, 200, "boom")
	archived(t, a, "app:lint", true, 100, "elsewhere")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))

	if a.Screen != ScreenTimeline {
		t.Fatalf("screen = %v", a.Screen)
	}
	if a.TimelineOf != "backend:lint" {
		t.Errorf("charting %q", a.TimelineOf)
	}
	if len(a.Timeline) != 2 {
		t.Fatalf("got %d points, want the two runs of this task", len(a.Timeline))
	}
	// Newest first, and the newest is the failure.
	if a.Timeline[0].Ok() {
		t.Error("the newest run failed")
	}
}

// `h` is every run in the project; `⇧H` is this one task. They are different questions and
// pressing one should not answer the other.
func TestTheTimelineIsNotTheHistoryList(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 200, "one")
	archived(t, a, "app:lint", true, 100, "two")

	parkOn(t, a, "backend:lint")
	press(a, Char('h'))
	if a.Screen != ScreenHistory {
		t.Fatalf("h should open the history list, got %v", a.Screen)
	}
	if len(a.History) != 2 {
		t.Errorf("the history list should hold both runs, got %d", len(a.History))
	}

	press(a, Esc())
	press(a, Char('H'))
	if a.Screen != ScreenTimeline {
		t.Fatalf("H should open the timeline, got %v", a.Screen)
	}
	if len(a.Timeline) != 1 {
		t.Errorf("the timeline should hold only this task's run, got %d", len(a.Timeline))
	}
}

func TestATaskThatHasNeverRunSaysSo(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	if a.Screen != ScreenTimeline {
		t.Fatalf("screen = %v", a.Screen)
	}
	if !strings.Contains(a.Status, "has not run") {
		t.Errorf("status = %q", a.Status)
	}
}

func TestEscapeLeavesTheTimelineWhereItWasOpened(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 100, "hi")
	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Esc())
	if a.Screen != ScreenPicker {
		t.Errorf("screen = %v, want the picker it was opened from", a.Screen)
	}
}

// The timeline is a list, and `gg`/`G` reach every list.
func TestTheTimelineMovesLikeEveryOtherList(t *testing.T) {
	a := sample(t)
	for i := range 5 {
		archived(t, a, "backend:lint", true, 100+i*10, "line")
	}
	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	if len(a.Timeline) != 5 {
		t.Fatalf("got %d points", len(a.Timeline))
	}

	press(a, Char('G'))
	if a.TimelineCursor != 4 {
		t.Errorf("G left the cursor at %d", a.TimelineCursor)
	}
	press(a, Char('g'))
	press(a, Char('g'))
	if a.TimelineCursor != 0 {
		t.Errorf("gg left the cursor at %d", a.TimelineCursor)
	}
	press(a, Char('j'))
	if a.TimelineCursor != 1 {
		t.Errorf("j left the cursor at %d", a.TimelineCursor)
	}
}

func TestOpeningARunFromTheTimeline(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", false, 100, "boom")
	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())

	if a.Screen != ScreenRun {
		t.Fatalf("screen = %v", a.Screen)
	}
	if a.Run == nil || !a.Run.IsStored() {
		t.Fatal("should have opened the stored run")
	}
	if a.Run.Root != "backend:lint" {
		t.Errorf("opened %q", a.Run.Root)
	}
}

// --- the diff -----------------------------------------------------------------------

func TestDiffingAgainstTheLastGreenRun(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "checking", "all good")
	archived(t, a, "backend:lint", false, 200, "checking", "boom: broken")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter()) // the newest, which is the failure
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v", a.Screen)
	}
	if a.DiffOf != "backend:lint" {
		t.Errorf("diffing %q", a.DiffOf)
	}
	if a.DiffAgainstWhat != "when it last passed" {
		t.Errorf("compared against %q", a.DiffAgainstWhat)
	}
	if a.DiffStat.Added != 1 || a.DiffStat.Removed != 1 {
		t.Errorf("stat = %+v, want one line each way", a.DiffStat)
	}

	var added, removed []string
	for _, r := range a.DiffRows {
		switch r.Op {
		case diff.Ins:
			added = append(added, r.Text)
		case diff.Del:
			removed = append(removed, r.Text)
		case diff.Same:
		}
	}
	if len(added) != 1 || added[0] != "boom: broken" {
		t.Errorf("added = %q", added)
	}
	if len(removed) != 1 || removed[0] != "all good" {
		t.Errorf("removed = %q", removed)
	}
}

// A run being compared must not find itself in the archive: it is in there, it is the
// newest, and a diff of a thing against itself is a diff of nothing.
func TestAStoredRunDoesNotDiffAgainstItself(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "old output")
	archived(t, a, "backend:lint", true, 200, "new output")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if a.DiffStat.Added != 1 || a.DiffStat.Removed != 1 {
		t.Errorf("stat = %+v — it found itself", a.DiffStat)
	}
}

// When nothing green exists, the previous run is the honest second choice — and the header
// has to say which comparison it made, because there are two.
func TestATaskThatNeverPassedFallsBackToThePreviousRun(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", false, 300, "boom one")
	archived(t, a, "backend:lint", false, 200, "boom two")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if !strings.Contains(a.DiffAgainstWhat, "failed too") {
		t.Errorf("compared against %q — it should say the baseline failed too", a.DiffAgainstWhat)
	}
}

func TestNothingToCompareAgainstSaysSoRatherThanOpeningAnEmptyDiff(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", false, 100, "boom")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))

	if a.Screen == ScreenDiff {
		t.Error("opened a diff with only one run in the archive")
	}
	if !strings.Contains(a.Status, "no earlier run") {
		t.Errorf("status = %q", a.Status)
	}
}

// The whole value of the view is that it is short: a diff of two 60-line logs that differ
// in one place must not be 60 rows.
func TestTheDiffElidesWhatBothRunsShared(t *testing.T) {
	a := sample(t)
	var older, newer []string
	for i := range 60 {
		older = append(older, "line "+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	newer = append([]string(nil), older...)
	newer[30] = "CHANGED"

	archived(t, a, "backend:lint", true, 300, older...)
	archived(t, a, "backend:lint", false, 200, newer...)

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if len(a.DiffRows) > 12 {
		t.Errorf("kept %d rows of a 60-line log with one change", len(a.DiffRows))
	}
	// …and `]` widens it again.
	before := len(a.DiffRows)
	press(a, Char(']'))
	if len(a.DiffRows) <= before {
		t.Errorf("] did not widen the context: %d then %d", before, len(a.DiffRows))
	}
	press(a, Char('['))
	press(a, Char('['))
	if len(a.DiffRows) >= before {
		t.Errorf("[ did not narrow it: %d then %d", before, len(a.DiffRows))
	}
}

func TestTwoIdenticalRunsSayNothingChanged(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "same", "output")
	archived(t, a, "backend:lint", true, 200, "same", "output")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))

	if !strings.Contains(a.Status, "identical") {
		t.Errorf("status = %q", a.Status)
	}
	if got := a.DiffSummary(); got != "no change" {
		t.Errorf("summary = %q", got)
	}
}

func TestEscapeLeavesTheDiffForTheRunItCameFrom(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "one")
	archived(t, a, "backend:lint", false, 200, "two")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Enter())
	press(a, Char('D'))
	press(a, Esc())
	if a.Screen != ScreenRun {
		t.Errorf("screen = %v, want the run it was opened from", a.Screen)
	}
}

// --- opening an editor --------------------------------------------------------------

// editable is an app looking at a stored failing run, in a project with a real file to open.
func editable(t *testing.T, line string) *App {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "app", "view.go"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(pivot.Fixture([]string{"lint"}), root)
	a.SetStateDir(t.TempDir())
	r := run.Detached("lint", run.GraphFrom(run.Edge{Parent: "lint"}))
	r.Feed("lint", "starting")
	r.Feed("lint", line)
	r.ApplyFailed("lint")
	r.Finish(1)
	a.OpenRunForTest(r)
	// OpenRunForTest loads a run without switching to it; `e` is a run-view key.
	a.Screen = ScreenRun
	return a
}

func TestPressingEOnALocationAsksForAnEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := editable(t, "internal/app/view.go:212:5: undefined: foo")

	a.RunExpand("lint")
	a.RebuildRunRows()
	// Land on the line that carries the location.
	for i, row := range a.RunRows {
		if !row.IsTask && row.Index == 1 {
			a.RunCursor = i
		}
	}
	press(a, Char('e'))

	editor, ok := a.TakeEdit()
	if !ok {
		t.Fatalf("no editor was asked for — status %q", a.Status)
	}
	if editor.Name != "vim" {
		t.Errorf("editor = %s", editor.Name)
	}
	if len(editor.Args) != 2 || editor.Args[0] != "+212" {
		t.Errorf("args = %v", editor.Args)
	}
	if !strings.HasSuffix(editor.Args[1], filepath.Join("internal", "app", "view.go")) {
		t.Errorf("path = %s", editor.Args[1])
	}
	if !strings.Contains(a.Status, "opening") {
		t.Errorf("status = %q", a.Status)
	}
}

// The intent is taken exactly once. Leaving it set would reopen the editor on the next
// keystroke, which is the sort of bug that only shows up in a real terminal.
func TestTheEditorIsOnlyLaunchedOnce(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := editable(t, "internal/app/view.go:9:1: oops")
	a.RunExpand("lint")
	a.RebuildRunRows()
	for i, row := range a.RunRows {
		if !row.IsTask && row.Index == 1 {
			a.RunCursor = i
		}
	}
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); !ok {
		t.Fatal("nothing to take")
	}
	if _, ok := a.TakeEdit(); ok {
		t.Error("the editor was still pending on a second collection")
	}
}

// A `--- FAIL` line names no file, and pressing `e` on it should not be a dead keystroke.
func TestEOnALineWithNoLocationFallsBackToTheTask(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := editable(t, "internal/app/view.go:212:5: undefined: foo")
	a.RunExpand("lint")
	a.RebuildRunRows()
	// Line 0 is "starting", which carries nothing.
	for i, row := range a.RunRows {
		if !row.IsTask && row.Index == 0 {
			a.RunCursor = i
		}
	}
	press(a, Char('e'))

	editor, ok := a.TakeEdit()
	if !ok {
		t.Fatalf("no editor — status %q", a.Status)
	}
	if editor.Args[0] != "+212" {
		t.Errorf("args = %v, want the task's first location", editor.Args)
	}
	if !strings.Contains(a.Status, "from line") {
		t.Errorf("status should say where it got the location: %q", a.Status)
	}
}

func TestEOnATaskThatNamedNoFileSaysSo(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := editable(t, "everything is fine")
	a.RunExpand("lint")
	a.RebuildRunRows()
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); ok {
		t.Error("asked for an editor with no location anywhere")
	}
	if !strings.Contains(a.Status, "did not print one") {
		t.Errorf("status = %q", a.Status)
	}
}

// A location that does not resolve must report that, not launch an editor on a path that
// is not there — which for most editors means creating the file.
func TestAMissingFileDoesNotOpenAnything(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := editable(t, "nowhere/at/all.go:4:1: oops")
	a.RunExpand("lint")
	a.RebuildRunRows()
	for i, row := range a.RunRows {
		if !row.IsTask && row.Index == 1 {
			a.RunCursor = i
		}
	}
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); ok {
		t.Error("would have opened a file that does not exist")
	}
	if !strings.Contains(a.Status, "no such file") {
		t.Errorf("status = %q", a.Status)
	}
}

func TestNoEditorConfiguredSaysWhatToSet(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	a := editable(t, "internal/app/view.go:212:5: undefined: foo")
	a.RunExpand("lint")
	a.RebuildRunRows()
	for i, row := range a.RunRows {
		if !row.IsTask && row.Index == 1 {
			a.RunCursor = i
		}
	}
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); ok {
		t.Error("launched something with no editor configured")
	}
	if !strings.Contains(a.Status, "$EDITOR") {
		t.Errorf("status = %q", a.Status)
	}
}

// The trend across a timeline header is `✓✓✓✗✗`, and the question that shape puts in your
// head is what happened at the turn. Diffing against the row immediately below answers
// "nothing changed" on the newest of a run of failures — true, useless, and exactly where
// the question was most worth asking.
func TestDiffingFromTheTimelineLooksBackToTheTurn(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 500, "clean")
	archived(t, a, "backend:lint", false, 400, "boom")
	archived(t, a, "backend:lint", false, 300, "boom")
	archived(t, a, "backend:lint", false, 200, "boom")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	if len(a.Timeline) != 4 {
		t.Fatalf("got %d points", len(a.Timeline))
	}
	// The cursor starts on the newest, which is the third failure in a row.
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if a.DiffAgainstWhat != "when it last passed" {
		t.Errorf("compared against %q, want the last pass", a.DiffAgainstWhat)
	}
	if a.DiffStat.Added != 1 || a.DiffStat.Removed != 1 {
		t.Errorf("stat = %+v — it compared against another identical failure", a.DiffStat)
	}
}

// It works the other way too: on a run that passed, the turn is the failure before it.
func TestFromAPassTheTurnIsTheLastFailure(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", false, 400, "boom")
	archived(t, a, "backend:lint", true, 300, "clean")
	archived(t, a, "backend:lint", true, 200, "clean")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Char('D'))

	if a.DiffAgainstWhat != "the last failure" {
		t.Errorf("compared against %q", a.DiffAgainstWhat)
	}
	if a.DiffStat.Added != 1 || a.DiffStat.Removed != 1 {
		t.Errorf("stat = %+v", a.DiffStat)
	}
}

// With no turn anywhere behind it there is nothing to look back to, and the row below is
// the honest answer rather than a reason to refuse.
func TestWithNoTurnItFallsBackToTheAdjacentRun(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "one")
	archived(t, a, "backend:lint", true, 200, "two")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Char('D'))

	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if a.DiffAgainstWhat != "the run before" {
		t.Errorf("compared against %q", a.DiffAgainstWhat)
	}
}

// The oldest row has nothing behind it at all.
func TestTheEarliestRunHasNothingToCompareAgainst(t *testing.T) {
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "one")
	archived(t, a, "backend:lint", false, 200, "two")

	parkOn(t, a, "backend:lint")
	press(a, Char('H'))
	press(a, Char('G'))
	press(a, Char('D'))

	if a.Screen == ScreenDiff {
		t.Error("opened a diff for the earliest stored run")
	}
	if !strings.Contains(a.Status, "earliest") {
		t.Errorf("status = %q", a.Status)
	}
}

// A diff opened from a timeline has no live run behind it — `a.Run` is whatever was open
// before, or nothing. The fallback has to read the diff it is looking at.
func TestEInADiffFallsBackToTheDiffItself(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "beta_test.go"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(pivot.Fixture([]string{"suite"}), root)
	a.SetStateDir(t.TempDir())
	archived(t, a, "suite", true, 300, "running", "PASS")
	archived(t, a, "suite", false, 200, "running", "    beta_test.go:42: want 3, got 4", "FAIL")

	a.OpenTimeline("suite")
	press(a, Char('D'))
	if a.Screen != ScreenDiff {
		t.Fatalf("screen = %v — status %q", a.Screen, a.Status)
	}
	if a.Run != nil {
		t.Fatal("this test is pointless if a run is open")
	}

	// Anywhere in the diff, not only on the line that carries the location.
	a.DiffCursor = 0
	press(a, Char('e'))

	editor, ok := a.TakeEdit()
	if !ok {
		t.Fatalf("no editor — status %q", a.Status)
	}
	if editor.Args[0] != "+42" {
		t.Errorf("args = %v", editor.Args)
	}
	if !strings.Contains(a.Status, "of the diff") {
		t.Errorf("status should say it came from the diff: %q", a.Status)
	}
}

// In a diff, the line that just appeared is the one you are there about — a location on a
// line both runs printed is the one that was already fine.
func TestTheDiffFallbackPrefersTheLinesThatArrived(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")

	root := t.TempDir()
	for _, name := range []string{"old.go", "new.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := New(pivot.Fixture([]string{"suite"}), root)
	a.SetStateDir(t.TempDir())
	// `old.go:1` is on a line both runs printed and comes first; `new.go:9` only arrived.
	archived(t, a, "suite", true, 300, "note: old.go:1: fine", "PASS")
	archived(t, a, "suite", false, 200, "note: old.go:1: fine", "boom at new.go:9", "FAIL")

	a.OpenTimeline("suite")
	press(a, Char('D'))
	// Onto a row with no location of its own, or `e` would find that one and never reach
	// the fallback this test is about.
	for i, row := range a.DiffRows {
		if !row.Gap && !strings.Contains(row.Text, ".go:") {
			a.DiffCursor = i
			break
		}
	}
	press(a, Char('e'))

	editor, ok := a.TakeEdit()
	if !ok {
		t.Fatalf("no editor — status %q", a.Status)
	}
	if !strings.HasSuffix(editor.Args[1], "new.go") {
		t.Errorf("opened %s, want the file the new line named", editor.Args[1])
	}
}

func TestADiffWithNoLocationsSaysSo(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := sample(t)
	archived(t, a, "backend:lint", true, 300, "all fine")
	archived(t, a, "backend:lint", false, 200, "not fine")

	a.OpenTimeline("backend:lint")
	press(a, Char('D'))
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); ok {
		t.Error("opened something")
	}
	if !strings.Contains(a.Status, "anywhere in this diff") {
		t.Errorf("status = %q", a.Status)
	}
}
