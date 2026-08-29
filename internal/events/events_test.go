package events_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/events"
	"github.com/ddromanidis/taskui/internal/run"
)

// nopCloser lets a buffer stand in for the socket or file a sink usually owns.
type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

// events collects what one poll's worth of state would send, decoded far enough to assert
// on: a consumer reads these as lines of JSON and switches on `type`.
func sent(t *testing.T, r *run.Run, d *events.Deltas) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	sink := events.New(nopCloser{&buf})
	sink.Lines = true
	d.Flush(sink, r)

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

func newDeltas() *events.Deltas {
	return events.NewDeltas()
}

// Each poll sends only what is new: the second flush of an unchanged run says nothing at
// all, which is what makes the stream cheap enough to poll at twenty milliseconds.
func TestTheStreamSendsOnlyWhatIsNew(t *testing.T) {
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"test"}}))
	d := newDeltas()

	r.Feed("test", "one")
	first := sent(t, r, d)
	if len(first) == 0 {
		t.Fatal("the first flush said nothing")
	}
	if again := sent(t, r, d); len(again) != 0 {
		t.Errorf("nothing changed and it sent %d events: %v", len(again), again)
	}

	r.Feed("test", "two")
	next := sent(t, r, d)
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
	sent(t, r, d)

	r.Feed("test", "--- FAIL: TestOrderTotal")
	r.ApplyFailed("test")
	r.Finish(1)

	var seq []string
	for _, e := range sent(t, r, d) {
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
	last := sent(t, r, d)
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
	if again := sent(t, r, d); len(again) != 0 {
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
	if n := count(sent(t, r, d)); n != 1 {
		t.Errorf("%d graph events on the first flush, want 1", n)
	}
	r.Feed("test", "something")
	if n := count(sent(t, r, d)); n != 0 {
		t.Errorf("%d graph events on the second flush, want none", n)
	}
}
