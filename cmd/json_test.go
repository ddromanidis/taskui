package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
)

// The listing is what a picker somewhere else needs: the colon path to run, the description
// to show, and how the archive says it went.
func TestListJSONCarriesWhatAPickerNeeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Feed("build", "compiling")
	r.Finish(0)
	if _, err := store.Save(store.StateDir(), dir, r); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := printTaskList(&buf, dir, []task.Task{
		{Name: "build", Desc: "Compile the workspace"},
		{Name: "deploy", Desc: "Ship it", Dangerous: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	var page listing
	if err := json.Unmarshal(buf.Bytes(), &page); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if page.Project != dir || len(page.Tasks) != 2 {
		t.Fatalf("listing = %+v", page)
	}
	if page.Tasks[0].Last == nil || !page.Tasks[0].Last.Ok {
		t.Errorf("build ran and passed; the listing says %+v", page.Tasks[0].Last)
	}
	if !page.Tasks[1].Dangerous || page.Tasks[1].Last != nil {
		t.Errorf("deploy = %+v, want it flagged and never run", page.Tasks[1])
	}
}

// `--json` is a form the other flags are printed in, not a command. On its own it would
// otherwise launch the TUI at somebody who is piping the output into a program.
func TestJSONOnItsOwnIsRefused(t *testing.T) {
	t.Cleanup(func() { opts.asJSON = false; opts.quickfix = false })

	_, err := execute(t, "--json", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "machine-readable form of") {
		t.Errorf("error = %v", err)
	}

	_, err = execute(t, "--json", "--quickfix", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "Pick one") {
		t.Errorf("error = %v", err)
	}
}

// End to end through the flag, on a real Taskfile: the archive's verdicts included.
func TestListJSONThroughTheFlag(t *testing.T) {
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not installed; --list needs it to discover anything")
	}
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Cleanup(func() { opts.asJSON = false; opts.list = false })

	taskfile := "version: \"3\"\ntasks:\n  build:\n    desc: Compile it\n    cmds: ['echo hi']\n"
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "--list", "--json", dir)
	if err != nil {
		t.Fatal(err)
	}
	var page listing
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].Name != "build" || page.Tasks[0].Desc != "Compile it" {
		t.Errorf("listing = %+v", page.Tasks)
	}
	if page.Tasks[0].Taskfile == "" || page.Tasks[0].Line == 0 {
		t.Errorf("no location on %+v — the JSON listing is what an editor jumps with", page.Tasks[0])
	}
}
