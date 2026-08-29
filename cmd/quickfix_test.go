package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

// failedRun is a `ci` that called `test`, which failed with a Go test's own output: a
// command echo, an assertion carrying a `file:line:col`, and the summary line.
func failedRun(t *testing.T, dir string) *run.Run {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "order_test.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"test"}}))
	r.Apply(run.LineEvent{Task: "test", Raw: "go test ./order_test.go", IsCommand: true})
	r.Feed("test", "=== RUN   TestOrderTotal")
	r.Feed("test", "    order_test.go:88:12: want 1200, got 1180")
	r.Feed("test", "--- FAIL: TestOrderTotal (0.01s)")
	r.ApplyFailed("test")
	r.Finish(1)
	return r
}

func quickfixOf(t *testing.T, r *run.Run, dir, only string) []string {
	t.Helper()
	var buf bytes.Buffer
	writeQuickfix(&buf, r, dir, only)
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// The entry is what an errorformat of `%f:%l:%c: %m` wants: an absolute path, so the editor
// never has to guess what the reference was relative to, and the message with the reference
// cut out of it, because the path is already the first two columns.
func TestQuickfixWritesAbsoluteEntries(t *testing.T) {
	dir := t.TempDir()
	got := quickfixOf(t, failedRun(t, dir), dir, "")

	want := filepath.Join(dir, "order_test.go") + ":88:12: want 1200, got 1180"
	if len(got) != 1 || got[0] != want {
		t.Errorf("quickfix =\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

// go-task echoes the command before it runs it, and a command routinely names a file. Those
// are not reports of anything, and an entry per command is a list that walks you through
// files nothing has complained about.
func TestQuickfixIgnoresTheCommandEcho(t *testing.T) {
	dir := t.TempDir()
	for _, line := range quickfixOf(t, failedRun(t, dir), dir, "") {
		if strings.Contains(line, "go test ./order_test.go") {
			t.Errorf("the command echo became an entry: %q", line)
		}
	}
}

// A tool that does not say which column still gets an entry: column one is where an editor
// puts a caller with no column anyway.
func TestQuickfixDefaultsTheColumn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Feed("build", "main.go:12: undefined: fooBar")
	r.ApplyFailed("build")
	r.Finish(1)

	got := quickfixOf(t, r, dir, "")
	want := filepath.Join(dir, "main.go") + ":12:1: undefined: fooBar"
	if len(got) != 1 || got[0] != want {
		t.Errorf("quickfix =\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

// Only the tasks that failed, unless one was named — a passing task's warnings are not what
// you asked for when you asked where it broke.
func TestQuickfixKeepsTheFailedTasksAndWhatWasAskedFor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lint.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := failedRun(t, dir)
	r.Feed("lint", "lint.go:3:1: exported func Foo should have comment")

	if got := quickfixOf(t, r, dir, ""); strings.Contains(strings.Join(got, "\n"), "lint.go") {
		t.Errorf("lint passed; its warnings do not belong in the list:\n%s", strings.Join(got, "\n"))
	}
	got := quickfixOf(t, r, dir, "lint")
	if len(got) != 1 || !strings.Contains(got[0], "lint.go:3:1: exported func Foo") {
		t.Errorf("asked for lint and got:\n%s", strings.Join(got, "\n"))
	}
}

// A run can end non-zero with no task marked failed — the shell died before anything
// claimed the output. Answering "nothing" there would be useless exactly when the list is
// wanted, so the whole run is offered instead.
func TestQuickfixFallsBackWhenNothingIsMarkedFailed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Feed("build", "main.go:4:2: syntax error")
	r.Finish(2)

	if got := quickfixOf(t, r, dir, ""); len(got) != 1 || !strings.Contains(got[0], "main.go:4:2:") {
		t.Errorf("quickfix =\n%s", strings.Join(got, "\n"))
	}
}

// A run that passed has nothing to report, and says so by saying nothing at all.
func TestQuickfixOnAPassingRunIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Feed("build", "main.go:4:2: a note nobody asked about")
	r.Finish(0)

	var buf bytes.Buffer
	if n := writeQuickfix(&buf, r, dir, ""); n != 0 || buf.Len() != 0 {
		t.Errorf("%d entries from a passing run: %q", n, buf.String())
	}
}

// End to end, through the flag: the most recent stored run of this project.
func TestQuickfixFlagReadsTheLastStoredRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Cleanup(func() { opts.quickfix = false; opts.searchTask = "" })

	if _, err := store.Save(store.StateDir(), dir, failedRun(t, dir)); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "--quickfix", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "order_test.go") + ":88:12: want 1200, got 1180"
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// With nothing archived there is nothing to answer with, and an empty list would read as
// "no errors" rather than "no runs".
func TestQuickfixWithNoStoredRunsSaysSo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Cleanup(func() { opts.quickfix = false; opts.searchTask = "" })

	if _, err := execute(t, "--quickfix", dir); err == nil ||
		!strings.Contains(err.Error(), "no stored runs") {
		t.Errorf("error = %v", err)
	}
}
