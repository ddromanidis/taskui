package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddromanidis/taskui/internal/run"
)

func finishedRun(root string) *run.Run {
	r := run.Detached(root, run.GraphFrom(
		run.Edge{Parent: root, Children: []string{"child"}},
		run.Edge{Parent: "child"},
	))
	r.Feed("child", "hello from the child")
	r.Feed("child", "error: boom")
	r.Finish(1)
	return r
}

func TestASavedRunRoundTripsThroughTheManifest(t *testing.T) {
	base := t.TempDir()
	dir, err := Save(base, "/proj", finishedRun("all"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}

	listed := List(base)
	if len(listed) != 1 {
		t.Fatalf("listed %d runs", len(listed))
	}
	if listed[0].Root != "all" || listed[0].Exit != 1 || !listed[0].Failed() {
		t.Errorf("manifest = %+v", listed[0])
	}
	if !reflect.DeepEqual(listed[0].Edges["all"], []string{"child"}) {
		t.Errorf("edges = %v", listed[0].Edges)
	}

	var child *TaskEntry
	for i := range listed[0].Tasks {
		if listed[0].Tasks[i].Name == "child" {
			child = &listed[0].Tasks[i]
		}
	}
	if child == nil || child.Lines != 2 {
		t.Errorf("child entry = %+v", child)
	}
}

// The archive has to be readable by anything, not just taskui — that is the whole argument
// for plain files.
func TestOutputLandsAsPlainGreppableText(t *testing.T) {
	base := t.TempDir()
	dir, _ := Save(base, "/proj", finishedRun("all"))
	text, err := os.ReadFile(filepath.Join(dir, "child.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "hello from the child\nerror: boom\n" {
		t.Errorf("got %q", text)
	}
}

// Colour is kept beside the searchable text, not instead of it.
func TestEscapeSequencesAreKeptInASidecar(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a"}))
	r.Feed("a", "\x1b[31merror\x1b[0m: boom")
	r.Finish(1)
	dir, _ := Save(base, "/proj", r)

	txt, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(txt) != "error: boom\n" {
		t.Errorf("txt = %q", txt)
	}
	ansi, _ := os.ReadFile(filepath.Join(dir, "a.ansi"))
	if !strings.Contains(string(ansi), "\x1b") {
		t.Errorf("ansi sidecar lost its escapes: %q", ansi)
	}
}

// Colons are not filenames.
func TestNamespacedTaskNamesBecomeSafeFilenames(t *testing.T) {
	if got := safeName("backend:migrate:down"); got != "backend.migrate.down" {
		t.Errorf("got %q", got)
	}
	if got := safeName("app:build"); got != "app.build" {
		t.Errorf("got %q", got)
	}
}

func TestPruningKeepsTheNewestRuns(t *testing.T) {
	base := t.TempDir()
	for i := range 5 {
		// Distinct task names, so the five runs get distinct ids even when they land in
		// the same second.
		if _, err := Save(base, "/proj", finishedRun("task"+string(rune('0'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(List(base)); got != 5 {
		t.Fatalf("listed %d", got)
	}
	if _, err := Prune(base, 2); err != nil {
		t.Fatal(err)
	}
	if got := len(List(base)); got != 2 {
		t.Errorf("after pruning: %d", got)
	}
}

// The picker's ✓/✗ column: newest result per task, drawn from the per-task entries so one
// `task all` teaches it about everything that run touched.
func TestLastOutcomesTakeTheNewestResultPerTask(t *testing.T) {
	base := t.TempDir()
	old := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"child"}}))
	old.Feed("child", "boom")
	old.ApplyFailed("child")
	old.Finish(1)
	if _, err := Save(base, "/proj", old); err != nil {
		t.Fatal(err)
	}

	outcomes := LastOutcomes(base, "/proj")
	if outcomes["child"].Ok {
		t.Error("child failed last time")
	}
	if outcomes["ci"].Ok {
		t.Error("and so did its parent")
	}
}

// Another project's runs are not this project's business.
func TestOutcomesAreScopedToTheProject(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"child"}}))
	r.Feed("child", "fine")
	r.Finish(0)
	if _, err := Save(base, "/elsewhere", r); err != nil {
		t.Fatal(err)
	}

	if len(LastOutcomes(base, "/proj")) != 0 {
		t.Error("another project's runs leaked in")
	}
	if len(LastOutcomes(base, "/elsewhere")) == 0 {
		t.Error("its own project's runs went missing")
	}
}

// A task that was never reached has no outcome — that is not the same as passing.
func TestSkippedTasksHaveNoOutcome(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"ran", "never"}}))
	r.Feed("ran", "hello")
	r.Finish(0)
	if _, err := Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}

	outcomes := LastOutcomes(base, "/proj")
	if _, ok := outcomes["ran"]; !ok {
		t.Error("the task that ran has no outcome")
	}
	if _, ok := outcomes["never"]; ok {
		t.Error("skipped is not passed")
	}
}

// `--force` is part of what was run, so it belongs in the record.
func TestForceIsRecordedInTheManifest(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("check", run.GraphFrom(run.Edge{Parent: "check"}))
	r.Feed("check", "checking")
	r.Finish(0)
	if _, err := Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}
	// Detached runs are never forced; the field simply has to round-trip.
	if List(base)[0].Force {
		t.Error("force should be false")
	}
	if got := List(base)[0].Command(); got != "task check" {
		t.Errorf("command = %q", got)
	}
}

func TestTheRunDirectoryIsOwnerOnly(t *testing.T) {
	base := t.TempDir()
	dir, _ := Save(base, "/proj", finishedRun("all"))

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("captured output is not owner-only: %o", got)
	}
	info, err = os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("manifest mode = %o", got)
	}
}

// The whole point of the archive: a stored run comes back as the same structure a live one
// has, so it folds and searches identically.
func TestAStoredRunReloadsAsARun(t *testing.T) {
	base := t.TempDir()
	original := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"build", "test"}}))
	original.Feed("build", "compiling core")
	original.Feed("test", "--- FAIL: TestOrderTotal")
	original.ApplyFailed("test")
	original.Finish(1)
	if _, err := Save(base, "/proj", original); err != nil {
		t.Fatal(err)
	}

	manifest := List(base)[0]
	reloaded, err := Load(base, manifest)
	if err != nil {
		t.Fatal(err)
	}

	if !reloaded.IsStored() {
		t.Error("should be marked as stored")
	}
	if !reloaded.Finished() || reloaded.Exit != 1 {
		t.Errorf("exit = %d finished = %v", reloaded.Exit, reloaded.Finished())
	}
	if !reflect.DeepEqual(reloaded.Graph.Children("ci"), []string{"build", "test"}) {
		t.Errorf("children = %v", reloaded.Graph.Children("ci"))
	}
	if reloaded.Tasks["test"].Status != run.Failed {
		t.Errorf("test status = %v", reloaded.Tasks["test"].Status)
	}
	if got := reloaded.Tasks["build"].Lines[0].Plain; got != "compiling core" {
		t.Errorf("build line = %q", got)
	}
}

// Reopening an archived run should look like it did live, colour included.
func TestColourSurvivesTheRoundTrip(t *testing.T) {
	base := t.TempDir()
	original := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a"}))
	original.Feed("a", "\x1b[31merror\x1b[0m: boom")
	original.Finish(1)
	if _, err := Save(base, "/proj", original); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := Load(base, List(base)[0])
	line := reloaded.Tasks["a"].Lines[0]
	if line.Plain != "error: boom" {
		t.Errorf("plain = %q, should still be searchable", line.Plain)
	}
	if !strings.Contains(line.Raw, "\x1b") {
		t.Errorf("raw = %q, should still be coloured", line.Raw)
	}
}

// IsCommand is derived rather than stored, so a marker never pollutes the greppable text.
func TestCommandEchoesAreRecognisedAgainOnReload(t *testing.T) {
	base := t.TempDir()
	original := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a"}))
	original.Feed("a", "task: [a] cargo build")
	original.Feed("a", "Compiling taskui")
	original.Finish(0)
	if _, err := Save(base, "/proj", original); err != nil {
		t.Fatal(err)
	}

	reloaded, _ := Load(base, List(base)[0])
	if !reloaded.Tasks["a"].Lines[0].IsCommand {
		t.Error("the command echo was not recognised")
	}
	if reloaded.Tasks["a"].Lines[1].IsCommand {
		t.Error("ordinary output was mistaken for a command echo")
	}
}

// --- timeline -----------------------------------------------------------------------

// agedRun is a finished run that looks like it started `ago` seconds back. Save derives the
// start from the duration, and the run id is `<started>-<root>` — so without distinct ages
// three runs of the same task in one test second would be one run three times.
//
// It always feeds at least one line: a task that printed nothing is, deliberately, not
// distinguishable from one that go-task never reached, and both are left Pending. Under
// `--output prefixed` a real task that runs always echoes its command, so a silent one is
// the artificial case, not the ordinary one.
func agedRun(root, task string, ok bool, ago int, lines ...string) *run.Run {
	r := run.Detached(root, run.GraphFrom(
		run.Edge{Parent: root, Children: []string{task}},
		run.Edge{Parent: task},
	))
	if len(lines) == 0 {
		lines = []string{"task: [" + task + "] echo hello"}
	}
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
	return r
}

func TestATimelineIsOneTasksHistoryNewestFirst(t *testing.T) {
	base := t.TempDir()
	for _, r := range []*run.Run{
		agedRun("all", "test", true, 300, "ok"),
		agedRun("all", "test", false, 200, "boom"),
		agedRun("test", "test", true, 100, "ok again"),
	} {
		if _, err := Save(base, "/proj", r); err != nil {
			t.Fatal(err)
		}
	}

	points := Timeline(base, "/proj", "test")
	if len(points) != 3 {
		t.Fatalf("got %d points, want 3", len(points))
	}
	for i := 1; i < len(points); i++ {
		if points[i].WhenUnix > points[i-1].WhenUnix {
			t.Errorf("point %d is newer than the one before it", i)
		}
	}
	// Newest first: the standalone `task test`, then the failure, then the first pass.
	if points[0].Root != "test" || !points[0].Ok() {
		t.Errorf("newest is %+v", points[0])
	}
	if points[1].Ok() {
		t.Error("the middle run failed")
	}
}

// The root is what explains a surprising duration: the same task reached from `task all`
// and on its own is the same task under different circumstances.
func TestATimelinePointRemembersTheRunItWasPartOf(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "/proj", agedRun("all", "lint", true, 60)); err != nil {
		t.Fatal(err)
	}
	points := Timeline(base, "/proj", "lint")
	if len(points) != 1 || points[0].Root != "all" {
		t.Fatalf("got %+v", points)
	}
}

func TestATimelineIsScopedToItsProject(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "/elsewhere", agedRun("all", "test", true, 60)); err != nil {
		t.Fatal(err)
	}
	if got := Timeline(base, "/proj", "test"); len(got) != 0 {
		t.Errorf("another project's runs leaked in: %+v", got)
	}
	if got := Timeline(base, "", "test"); len(got) != 1 {
		t.Errorf("an empty project should mean every project, got %d", len(got))
	}
}

// A task go-task decided was up to date did not run. A row saying so makes the trend harder
// to read, not easier.
func TestATimelineSkipsTheTasksThatNeverRan(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("all", run.GraphFrom(
		run.Edge{Parent: "all", Children: []string{"ran", "never"}},
	))
	r.Feed("ran", "hello")
	r.Finish(0)
	if _, err := Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}
	if got := Timeline(base, "/proj", "never"); len(got) != 0 {
		t.Errorf("a task that never ran has no timeline, got %+v", got)
	}
}

func TestLastGreenSkipsTheFailuresAndItself(t *testing.T) {
	base := t.TempDir()
	var ids []string
	for _, c := range []struct {
		ok  bool
		ago int
	}{{true, 300}, {true, 200}, {false, 100}} {
		dir, err := Save(base, "/proj", agedRun("all", "test", c.ok, c.ago))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, filepath.Base(dir))
	}
	newest := ids[2]

	green, ok := LastGreen(base, "/proj", "test", "")
	if !ok {
		t.Fatal("no green run found")
	}
	if !green.Ok() {
		t.Error("last green is not green")
	}
	// Two passes, 300s and 200s ago, then a failure. The newer pass is the one to compare
	// against — the older one is green too, and picking it would answer a question nobody
	// asked.
	if green.RunID != ids[1] {
		t.Errorf("got %s, want the newer pass %s", green.RunID, ids[1])
	}

	// Previous, unlike LastGreen, does not care how it went — and skipping itself is what
	// keeps a stored run from diffing against its own output.
	prev, ok := Previous(base, "/proj", "test", newest)
	if !ok {
		t.Fatal("no previous run")
	}
	if prev.RunID == newest {
		t.Error("Previous returned the run it was told to skip")
	}
}

func TestLastGreenOfATaskThatNeverPassed(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "/proj", agedRun("all", "test", false, 60)); err != nil {
		t.Fatal(err)
	}
	if _, ok := LastGreen(base, "/proj", "test", ""); ok {
		t.Error("found a green run that does not exist")
	}
	if _, ok := Previous(base, "/proj", "test", ""); !ok {
		t.Error("but there is a previous run, and it should be offered")
	}
}

// The diff reads through this, so it has to come back exactly as it went in.
func TestOutputReadsBackWhatTheTaskPrinted(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "/proj", agedRun("all", "test", true, 60, "first", "second", "third")); err != nil {
		t.Fatal(err)
	}
	points := Timeline(base, "/proj", "test")
	if len(points) != 1 {
		t.Fatalf("got %d points", len(points))
	}
	got := Output(base, points[0])
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Two runs of one task inside a second used to be one run: the id is `<second>-<task>`, and
// the second save landed on the first's directory. That is precisely the pair a timeline
// exists to show, so losing it there is the worst place for it to happen.
func TestTwoRunsOfOneTaskInOneSecondBothSurvive(t *testing.T) {
	base := t.TempDir()
	first, err := Save(base, "/proj", agedRun("suite", "suite", true, 5, "green"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Save(base, "/proj", agedRun("suite", "suite", false, 5, "red"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("both runs landed in %s", first)
	}
	if got := len(List(base)); got != 2 {
		t.Errorf("archive holds %d runs, want 2", got)
	}
	points := Timeline(base, "/proj", "suite")
	if len(points) != 2 {
		t.Fatalf("timeline has %d points, want 2", len(points))
	}
	// Newest first, and the newest is the failure.
	if points[0].Ok() || !points[1].Ok() {
		t.Errorf("out of order: %v then %v", points[0].Status, points[1].Status)
	}
}

// --- flakes -------------------------------------------------------------------------

// atCommit is agedRun plus the git revision the run happened at, written straight into the
// manifest — Save reads the real repository, and a test must not depend on one.
// atCommit archives a run of `test` at a given revision. The commit is written straight
// into the manifest rather than derived: Save reads the real repository, and a test must not
// depend on which one it happens to be sitting in.
func atCommit(t *testing.T, base, project, commit string, ok bool, ago int) {
	t.Helper()
	dir, err := Save(base, project, agedRun("test", "test", ok, ago))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatal(err)
	}
	m.Commit = commit
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Both outcomes at one commit is not suggestive of flakiness, it is flakiness: the code did
// not change and the answer did.
func TestBothOutcomesAtOneCommitIsAFlake(t *testing.T) {
	base := t.TempDir()
	atCommit(t, base, "/proj", "abc1234deadbeef", true, 300)
	atCommit(t, base, "/proj", "abc1234deadbeef", false, 200)

	flakes := Flaky(base, "/proj")
	if len(flakes) != 1 {
		t.Fatalf("got %d flakes, want 1: %+v", len(flakes), flakes)
	}
	if flakes[0].Task != "test" || flakes[0].Passed != 1 || flakes[0].Failed != 1 {
		t.Errorf("got %+v", flakes[0])
	}
	if flakes[0].Short() != "abc1234" {
		t.Errorf("short = %q", flakes[0].Short())
	}
}

// The obvious explanation for a task that failed and then passed is that somebody fixed it.
// Only the commit tells those two apart, which is why it is recorded.
func TestFailingThenPassingAcrossACommitIsNotAFlake(t *testing.T) {
	base := t.TempDir()
	atCommit(t, base, "/proj", "broken0000", false, 300)
	atCommit(t, base, "/proj", "fixed11111", true, 200)

	if got := Flaky(base, "/proj"); len(got) != 0 {
		t.Errorf("called a fix a flake: %+v", got)
	}
}

// Two runs of uncommitted work are not two runs of the same code.
func TestADirtyTreeIsNeverFlaky(t *testing.T) {
	base := t.TempDir()
	atCommit(t, base, "/proj", "abc1234-dirty", true, 300)
	atCommit(t, base, "/proj", "abc1234-dirty", false, 200)

	if got := Flaky(base, "/proj"); len(got) != 0 {
		t.Errorf("a dirty tree cannot establish a flake: %+v", got)
	}
}

// A project that is not a checkout still gets its runs kept; it just cannot answer this.
func TestRunsWithNoCommitAreIgnored(t *testing.T) {
	base := t.TempDir()
	atCommit(t, base, "/proj", "", true, 300)
	atCommit(t, base, "/proj", "", false, 200)

	if got := Flaky(base, "/proj"); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestFlakesAreScopedToTheProject(t *testing.T) {
	base := t.TempDir()
	atCommit(t, base, "/elsewhere", "abc1234", true, 300)
	atCommit(t, base, "/elsewhere", "abc1234", false, 200)

	if got := Flaky(base, "/proj"); len(got) != 0 {
		t.Errorf("another project's flake leaked in: %+v", got)
	}
	if got := Flaky(base, "/elsewhere"); len(got) != 1 {
		t.Errorf("its own project's flake went missing")
	}
}

func TestASteadyTaskIsNotAFlake(t *testing.T) {
	base := t.TempDir()
	for i := range 5 {
		atCommit(t, base, "/proj", "abc1234", true, 300-i*10)
	}
	if got := Flaky(base, "/proj"); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
