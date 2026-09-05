package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/task"
)

func tasksNamed(names ...string) []task.Task {
	out := make([]task.Task, 0, len(names))
	for _, name := range names {
		out = append(out, task.Task{Name: name})
	}
	return out
}

// A reload has to feel like nothing happened: the row you were on is still under the cursor,
// by name, the way a pivot keeps its place.
func TestAReloadKeepsTheCursorOnTheSameTask(t *testing.T) {
	a := appWith(t, []string{"backend:lint", "backend:build", "fmt"})
	a.SetFoldAll(true)
	parkOn(t, a, "backend:build")

	a.ReplaceTasks(tasksNamed("backend:lint", "backend:build", "backend:migrate", "fmt"))

	if ti := a.SelectedTask(); ti < 0 || a.Tasks[ti].Name != "backend:build" {
		t.Errorf("cursor landed on %v, want backend:build", a.selectedLabel())
	}
	if !strings.Contains(a.Status, "1 new") {
		t.Errorf("status = %q, want it to say what changed", a.Status)
	}
}

// A task that is gone is gone: a mark on it is a run waiting to fail, and a watch on it is a
// re-run that can never happen.
func TestAReloadDropsMarksAndWatchesForTasksThatWent(t *testing.T) {
	a := appWith(t, []string{"backend:lint", "backend:build"})
	a.SetFoldAll(true)
	parkOn(t, a, "backend:lint")
	press(a, Char('m'))
	parkOn(t, a, "backend:build")
	press(a, Char('m'))
	press(a, Char('W'))
	if len(a.Marked()) != 2 || len(a.Watching) != 2 {
		t.Fatalf("setup: marked %v watching %v", a.Marked(), a.Watching)
	}

	a.ReplaceTasks(tasksNamed("backend:lint"))

	if got := a.Marked(); len(got) != 1 || got[0] != "backend:lint" {
		t.Errorf("marks = %v, want only the task that still exists", got)
	}
	if len(a.Watching) != 1 || a.Watching[0] != "backend:lint" {
		t.Errorf("watching = %v", a.Watching)
	}
	if !strings.Contains(a.Status, "1 gone") {
		t.Errorf("status = %q", a.Status)
	}
}

// A Taskfile does not parse for as long as it takes to finish typing one. Blanking the list
// on every save mid-edit would be worse than being briefly out of date.
func TestABrokenTaskfileKeepsTheLastGoodList(t *testing.T) {
	a := appWith(t, []string{"backend:lint", "backend:build"})
	before := len(a.Tasks)

	ch := make(chan reloaded, 1)
	ch <- reloaded{err: os.ErrInvalid}
	a.reloadCh = ch
	a.collectReload()

	if len(a.Tasks) != before {
		t.Errorf("the list changed to %d tasks on a failed read", len(a.Tasks))
	}
	if !strings.Contains(a.Status, "does not parse") {
		t.Errorf("status = %q", a.Status)
	}
}

// A save that lands while the last re-read is still running is remembered, not dropped —
// the whole point is that the list ends up agreeing with the file.
func TestASaveDuringAReloadIsNotLost(t *testing.T) {
	a := appWith(t, []string{"fmt"})
	held := make(chan reloaded, 1)
	a.reloadCh = held
	a.reloadPending = true

	held <- reloaded{tasks: tasksNamed("fmt", "lint")}
	a.collectReload()

	if a.reloadCh == nil {
		t.Error("the save that arrived mid-read was dropped")
	}
	// Drain the re-read this started so it cannot outlive the test.
	<-a.reloadCh
}

// The root Taskfile is watched before go-task has been asked anything, and the listing adds
// the files the includes live in.
func TestTheWatchCoversTheRootTaskfileAndEveryIncludedOne(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Taskfile.yml")
	if err := os.WriteFile(root, []byte("version: '3'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(pivot.Fixture([]string{"site:new"}), dir)
	a.SetStateDir(t.TempDir())

	if got := a.taskfilePaths(); len(got) != 1 || got[0] != root {
		t.Errorf("paths = %v, want just the root Taskfile", got)
	}

	included := filepath.Join(dir, "site", "Taskfile.yml")
	a.Details = map[string]task.Detail{"site:new": {Where: task.Where{File: included, Line: 3}}}
	got := a.taskfilePaths()
	if len(got) != 2 || got[0] != root || got[1] != included {
		t.Errorf("paths = %v, want the root and the included file", got)
	}
}

// The whole loop, against a real Taskfile and a real go-task: edit the file taskui read,
// and the list catches up without a restart. This is the one that would have failed before
// any of the parts above existed.
func TestEditingTheTaskfileUpdatesTheListInPlace(t *testing.T) {
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "Taskfile.yml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("version: '3'\ntasks:\n  build:\n    desc: Build it\n    cmds: [true]\n")

	tasks, err := task.Discover(dir)
	if err != nil {
		t.Skipf("go-task could not read the fixture: %v", err)
	}
	a := New(tasks, dir)
	a.SetStateDir(t.TempDir())
	a.WatchTaskfile()
	defer func() {
		if a.taskfileWatch != nil {
			a.taskfileWatch.Close()
		}
	}()
	if a.taskfileWatch == nil {
		t.Fatal("nothing is watching the Taskfile")
	}
	a.taskfileWatch.Settle = 120 * time.Millisecond

	write("version: '3'\ntasks:\n  build:\n    desc: Build it\n    cmds: [true]\n" +
		"  lint:\n    desc: Lint it\n    cmds: [true]\n")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(a.Tasks) < 2 {
		a.PollTaskfile()
		a.collectReload()
		time.Sleep(20 * time.Millisecond)
	}

	names := make([]string, 0, len(a.Tasks))
	for _, x := range a.Tasks {
		names = append(names, x.Name)
	}
	if !slices.Contains(names, "lint") {
		t.Fatalf("the new task never arrived: %v", names)
	}
	if !strings.Contains(a.Status, "Taskfile changed") {
		t.Errorf("status = %q", a.Status)
	}
}
