package store

import (
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
)

// Two task names that differ only in a character `safeName` flattens used to land on one
// file, and the manifest then handed both tasks whichever of the two survived.
func TestTasksThatFlattenToOneNameKeepTheirOwnOutput(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"a:b", "a.b"}}))
	r.Feed("a:b", "from the colon one")
	r.Feed("a.b", "from the dot one")
	r.Finish(0)

	if _, err := Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}
	stored, err := Load(base, List(base)[0])
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	for _, name := range []string{"a:b", "a.b"} {
		lines := stored.Tasks[name].Lines
		if len(lines) == 0 {
			t.Fatalf("%s stored no output", name)
		}
		seen[name] = lines[0].Plain
	}
	if seen["a:b"] == seen["a.b"] {
		t.Errorf("both tasks came back with %q", seen["a:b"])
	}
	if seen["a:b"] != "from the colon one" {
		t.Errorf("a:b = %q", seen["a:b"])
	}
}

// `search.InRun` promises `n` walks a run the way it happened. Save wrote the names
// sorted, so every archived run came back alphabetical instead.
func TestAStoredRunKeepsTheOrderItRanIn(t *testing.T) {
	base := t.TempDir()
	r := run.Detached("ci", run.GraphFrom(run.Edge{Parent: "ci", Children: []string{"zebra", "alpha"}}))
	// Ran in the order a sort would reverse.
	r.Feed("zebra", "first")
	r.Feed("alpha", "second")
	r.Finish(0)

	if _, err := Save(base, "/proj", r); err != nil {
		t.Fatal(err)
	}
	stored, err := Load(base, List(base)[0])
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, name := range stored.Order {
		if name == "zebra" || name == "alpha" {
			got = append(got, name)
		}
	}
	if len(got) != 2 || got[0] != "zebra" || got[1] != "alpha" {
		t.Errorf("order = %v, want zebra before alpha", got)
	}
}
