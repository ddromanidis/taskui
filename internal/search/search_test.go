package search

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

func runWith(lines [][2]string) *run.Run {
	r := run.Detached("all", run.GraphFrom(run.Edge{Parent: "all", Children: []string{"a", "b"}}))
	for _, l := range lines {
		r.Feed(l[0], l[1])
	}
	return r
}

func mustQuery(t *testing.T, pattern string) *Query {
	t.Helper()
	q, err := NewQuery(pattern)
	if err != nil {
		t.Fatalf("NewQuery(%q): %v", pattern, err)
	}
	return q
}

func TestFindsMatchesAcrossTasks(t *testing.T) {
	r := runWith([][2]string{
		{"a", "compiling"},
		{"a", "error: boom"},
		{"b", "warning: unused"},
		{"b", "error: also boom"},
	})
	hits := InRun(r, mustQuery(t, "error"))
	want := []LiveHit{{Task: "a", Index: 1}, {Task: "b", Index: 1}}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("hits = %v, want %v", hits, want)
	}
}

// Hits come back in execution order, so stepping through them walks the run the way it
// happened rather than alphabetically.
func TestHitsFollowExecutionOrderNotTaskNameOrder(t *testing.T) {
	r := run.Detached("all", run.GraphFrom(run.Edge{Parent: "all", Children: []string{"z", "a"}}))
	r.Feed("z", "error: first")
	r.Feed("a", "error: second")
	hits := InRun(r, mustQuery(t, "error"))
	if hits[0].Task != "z" {
		t.Errorf("z ran first, so its hit should come first: %v", hits)
	}
	if hits[1].Task != "a" {
		t.Errorf("hits = %v", hits)
	}
}

// Lowercase searches loosely, any uppercase searches exactly — ripgrep's rule.
func TestSmartCase(t *testing.T) {
	r := runWith([][2]string{
		{"a", "--- FAIL: TestOrderTotal"},
		{"a", "failed to connect"},
	})
	if got := len(InRun(r, mustQuery(t, "fail"))); got != 2 {
		t.Errorf("lowercase matched %d, want 2", got)
	}
	if got := len(InRun(r, mustQuery(t, "FAIL"))); got != 1 {
		t.Errorf("uppercase matched %d, want 1", got)
	}
}

// An escape is a class, not a capital: `\w` must not make an all-lowercase pattern
// case-sensitive.
func TestEscapesDoNotCountAsUppercase(t *testing.T) {
	r := runWith([][2]string{{"a", "--- FAIL: TestOrderTotal"}})
	if got := len(InRun(r, mustQuery(t, `\w+ail`))); got != 1 {
		t.Errorf("matched %d, want 1", got)
	}
}

func TestRegexNotJustLiterals(t *testing.T) {
	r := runWith([][2]string{
		{"a", "3 migrations pending"},
		{"a", "0 migrations pending"},
	})
	hits := InRun(r, mustQuery(t, `[1-9]\d* migrations pending`))
	if len(hits) != 1 || hits[0].Index != 0 {
		t.Errorf("hits = %v", hits)
	}
}

// Searching the raw bytes would miss this: the escape sequence splits the word.
func TestMatchesThroughColourCodes(t *testing.T) {
	r := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a"}))
	r.Feed("a", "\x1b[31merr\x1b[0mor: boom")
	if got := len(InRun(r, mustQuery(t, "error"))); got != 1 {
		t.Errorf("the match spans a colour change: %d hits", got)
	}
}

func TestFirstMatchGivesAHighlightRange(t *testing.T) {
	q := mustQuery(t, "boom")
	start, end, ok := q.FirstMatch("error: boom")
	if !ok || start != 7 || end != 11 {
		t.Errorf("got (%d, %d, %v)", start, end, ok)
	}
	if _, _, ok := q.FirstMatch("nothing here"); ok {
		t.Error("matched nothing")
	}
}

// The same query, run over the archive, must find the same thing it found live.
func TestTheSameQueryWorksOnStoredRuns(t *testing.T) {
	base := t.TempDir()
	r := runWith([][2]string{{"a", "error: boom"}, {"b", "all clear"}})
	r.Finish(1)
	if _, err := store.Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}

	results, dropped := InStore(base, mustQuery(t, "error"), 100)
	if dropped != 0 {
		t.Errorf("dropped = %d", dropped)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	if results[0].Manifest.Root != "all" {
		t.Errorf("root = %q", results[0].Manifest.Root)
	}
	if len(results[0].Hits) != 1 {
		t.Fatalf("hits = %v", results[0].Hits)
	}
	hit := results[0].Hits[0]
	if hit.Task != "a" || hit.Text != "error: boom" || hit.LineNo != 1 {
		t.Errorf("hit = %+v", hit)
	}
}

// A run with no hits is absent rather than present-and-empty.
func TestRunsWithoutHitsAreOmitted(t *testing.T) {
	base := t.TempDir()
	r := runWith([][2]string{{"a", "all clear"}})
	r.Finish(0)
	if _, err := store.Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}

	results, _ := InStore(base, mustQuery(t, "error"), 100)
	if len(results) != 0 {
		t.Errorf("results = %v", results)
	}
}

// Truncation has to be reported, or a capped result reads as a complete one.
func TestPerRunTruncationIsCounted(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("all", run.GraphFrom(run.Edge{Parent: "all"}))
	for i := range 10 {
		r.Feed("all", fmt.Sprintf("error %d", i))
	}
	r.Finish(1)
	if _, err := store.Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}

	results, dropped := InStore(base, mustQuery(t, "error"), 4)
	if len(results[0].Hits) != 4 {
		t.Errorf("hits = %d", len(results[0].Hits))
	}
	if dropped != 6 {
		t.Errorf("dropped = %d", dropped)
	}
}

func TestAnInvalidPatternIsAnErrorNotAPanic(t *testing.T) {
	if _, err := NewQuery("(unclosed"); err == nil {
		t.Error("expected an error")
	}
}

// A single line longer than [bufio.Scanner]'s 64KB token cap must not stop the search dead —
// a run that dumped a minified bundle would otherwise lose everything after it.
func TestAVeryLongLineDoesNotTruncateTheSearch(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a"}))
	long := make([]byte, 200_000)
	for i := range long {
		long[i] = 'x'
	}
	r.Feed("a", string(long))
	r.Feed("a", "error: after the blob")
	r.Finish(1)
	if _, err := store.Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}

	results, _ := InStore(base, mustQuery(t, "after the blob"), 100)
	if len(results) != 1 || len(results[0].Hits) != 1 {
		t.Fatalf("the line past the blob went missing: %v", results)
	}
	if results[0].Hits[0].LineNo != 2 {
		t.Errorf("line numbering drifted: %d", results[0].Hits[0].LineNo)
	}
}
