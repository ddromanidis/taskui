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

// events collects what one poll's worth of state would send, decoded far enough to assert
// on: a consumer reads these as lines of JSON and switches on `type`.
func events(t *testing.T, r *run.Run, d *deltas) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	d.flush(r, func(v any) {
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
	})

	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not one JSON object per line: %v in %q", err, line)
		}
		out = append(out, m)
	}
	return out
}

func newDeltas() *deltas {
	return &deltas{seen: map[string]int{}, said: map[string]taskState{}}
}

// Each poll sends only what is new: the second flush of an unchanged run says nothing at
// all, which is what makes the stream cheap enough to poll at twenty milliseconds.
func TestTheStreamSendsOnlyWhatIsNew(t *testing.T) {
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"test"}}))
	d := newDeltas()

	r.Feed("test", "one")
	first := events(t, r, d)
	if len(first) == 0 {
		t.Fatal("the first flush said nothing")
	}
	if again := events(t, r, d); len(again) != 0 {
		t.Errorf("nothing changed and it sent %d events: %v", len(again), again)
	}

	r.Feed("test", "two")
	next := events(t, r, d)
	if len(next) != 1 || next[0]["type"] != "line" || next[0]["text"] != "two" {
		t.Errorf("second flush = %v, want just the new line", next)
	}
}

// A task is announced before its output and its verdict after it, which is the order the two
// actually happen in — a consumer must never see `Ok` and then more lines from the same task.
func TestTheStreamBracketsOutputWithItsTask(t *testing.T) {
	r := run.Detached("test", run.GraphFrom(run.Edge{Parent: "test"}))
	d := newDeltas()

	r.Feed("test", "running 42 tests")
	events(t, r, d)

	r.Feed("test", "--- FAIL: TestOrderTotal")
	r.ApplyFailed("test")
	r.Finish(1)

	var seq []string
	for _, e := range events(t, r, d) {
		switch e["type"] {
		case "line":
			seq = append(seq, "line")
		case "task":
			seq = append(seq, "task:"+e["status"].(string))
		}
	}
	if len(seq) == 0 || seq[len(seq)-1] != "task:Failed" {
		t.Errorf("event order = %v, want the verdict last", seq)
	}
}

// The line index counts from the start of the run rather than from the start of the buffer.
// A long-lived task drops lines off the front, and an index that slid with them would
// renumber everything a consumer had already stored.
func TestTheStreamIndexesFromTheStartOfTheRun(t *testing.T) {
	r := run.Detached("tail", run.GraphFrom(run.Edge{Parent: "tail"}))
	d := newDeltas()

	for range run.MaxLines + run.DropBlock {
		r.Feed("tail", "line")
	}
	last := events(t, r, d)
	if len(last) == 0 {
		t.Fatal("no events")
	}
	final := last[len(last)-1]
	if final["type"] != "line" {
		t.Fatalf("last event = %v", final)
	}
	if got := int(final["index"].(float64)); got != run.MaxLines+run.DropBlock-1 {
		t.Errorf("last index = %d, want %d", got, run.MaxLines+run.DropBlock-1)
	}

	// And nothing is re-sent once the front of the buffer has gone.
	if again := events(t, r, d); len(again) != 0 {
		t.Errorf("re-sent %d events after a drop", len(again))
	}
}

// The graph goes out once, when it arrives — it is what a consumer draws the tree from, and
// resending it on every poll would be a kilobyte a tick on a large aggregate.
func TestTheStreamSendsTheGraphOnce(t *testing.T) {
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"test"}}))
	d := newDeltas()

	count := func(evts []map[string]any) int {
		n := 0
		for _, e := range evts {
			if e["type"] == "graph" {
				n++
			}
		}
		return n
	}
	if n := count(events(t, r, d)); n != 1 {
		t.Errorf("%d graph events on the first flush, want 1", n)
	}
	r.Feed("test", "something")
	if n := count(events(t, r, d)); n != 0 {
		t.Errorf("%d graph events on the second flush, want none", n)
	}
}

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
