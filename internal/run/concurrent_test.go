package run

import (
	"testing"
)

// go-task runs `deps:` concurrently and interleaves their lines under `--output prefixed`.
// The parser used to read "another task spoke" as "the last one finished", so the first
// line out of one half of a parallel build marked the other half ✓ — with its duration
// frozen at however long it had run — and nothing ever reopened it.
func TestParallelDepsAreNotClosedByEachOther(t *testing.T) {
	g := GraphFrom(
		Edge{Parent: "all", Children: []string{"lint", "test"}},
		Edge{Parent: "lint"},
		Edge{Parent: "test"},
	)
	g.Deps["all"] = []string{"lint", "test"}
	r := Detached("all", g)

	r.Feed("lint", "checking")
	r.Feed("test", "running")
	if got := r.Tasks["lint"].Status; got != Running {
		t.Errorf("lint = %v, want still Running while its sibling prints", got)
	}

	r.Feed("lint", "still checking")
	if got := r.Tasks["test"].Status; got != Running {
		t.Errorf("test = %v, want still Running", got)
	}
}

// The sequential case is what the closing rule is for, and it still holds: `cmds:` run in
// order, so a line from the second says the first is done.
func TestSequentialSiblingsStillCloseEachOther(t *testing.T) {
	r := Detached("all", GraphFrom(
		Edge{Parent: "all", Children: []string{"first", "second"}},
		Edge{Parent: "first"},
		Edge{Parent: "second"},
	))

	r.Feed("first", "working")
	r.Feed("second", "working")
	if got := r.Tasks["first"].Status; got != Ok {
		t.Errorf("first = %v, want Ok once the next command speaks", got)
	}
}

// A parent is not closed by its own child, concurrent or not.
func TestAParentStaysOpenWhileItsDepsRun(t *testing.T) {
	g := GraphFrom(
		Edge{Parent: "all", Children: []string{"lint", "test"}},
		Edge{Parent: "lint"},
		Edge{Parent: "test"},
	)
	g.Deps["all"] = []string{"lint", "test"}
	r := Detached("all", g)

	r.Feed("lint", "checking")
	if got := r.Tasks["all"].Status; got != Running {
		t.Errorf("all = %v, want Running while a dep works", got)
	}
}

// A read boundary can fall inside a line belonging to a task other than the one speaking,
// and the fragment carries no tag — so it is attributed by guess. When the newline arrives
// with a different tag the guess was wrong, and the fragment used to stay behind forever as
// a truncated copy in a task that never printed it.
func TestAMisattributedFragmentIsTakenBackOut(t *testing.T) {
	r := Detached("all", GraphFrom(
		Edge{Parent: "all", Children: []string{"build", "test"}},
		Edge{Parent: "build"},
		Edge{Parent: "test"},
	))
	r.Feed("build", "compiling")

	// A read boundary lands mid-line: no tag, so it is attributed to whoever spoke last.
	r.apply(Partial{Text: "Enter passphrase:"})
	// The newline arrives and the line turns out to have been the other task's.
	r.apply(LineEvent{Task: "test", Raw: "Enter passphrase: yes"})

	for _, line := range r.Tasks["build"].Lines {
		if line.Plain == "Enter passphrase:" {
			t.Error("the fragment stayed in the task it was guessed into")
		}
	}
	if _, waiting := r.PendingPrompt(); waiting {
		t.Error("a superseded fragment is still reported as a live prompt")
	}
}

// The ordinary case is untouched: a fragment that grows into a line of the same task is
// replaced in place rather than duplicated.
func TestAFragmentThatCompletesInPlaceIsReplaced(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{Parent: "all"}))
	r.apply(Partial{Text: "compil"})
	r.apply(LineEvent{Task: "all", Raw: "compiling core"})

	lines := r.Tasks["all"].Lines
	if len(lines) != 1 {
		t.Fatalf("%d lines, want the fragment replaced by the whole line: %+v", len(lines), lines)
	}
	if lines[0].Plain != "compiling core" {
		t.Errorf("line = %q", lines[0].Plain)
	}
}
