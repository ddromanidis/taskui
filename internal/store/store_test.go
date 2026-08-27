package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
