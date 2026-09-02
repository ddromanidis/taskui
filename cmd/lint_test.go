package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/cover"
	"github.com/ddromanidis/taskui/internal/task"
)

func TestCoverPatternsReadsGlobsAndSkipsComments(t *testing.T) {
	dir := t.TempDir()
	body := "# the deploy pipeline runs from CI, never from a local gate\ndeploy:*\n\nsite:build # its own workflow\n"
	if err := os.WriteFile(filepath.Join(dir, cover.File), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := coverPatterns(dir); !slices.Equal(got, []string{"deploy:*", "site:build"}) {
		t.Errorf("got %v", got)
	}
}

// A repository with no such file is the normal case and loses nothing.
func TestCoverPatternsIsEmptyWithoutTheFile(t *testing.T) {
	if got := coverPatterns(t.TempDir()); len(got) != 0 {
		t.Errorf("want none, got %v", got)
	}
}

// --- the matrix --------------------------------------------------------------------

// codocShape is the arrangement this check was written against, reduced to the parts that
// decide anything — the same fixture the rule's own tests use, restated here because the
// matrix is a separate projection of it and a shared fixture across packages would be a
// dependency between two things that only happen to agree.
func codocShape() ([]task.Task, func(string) []string) {
	tasks := []task.Task{
		{Name: "test", Desc: "Run all automated tests"},
		{Name: "build", Desc: "Build all components"},
		{Name: "lint", Desc: "Lint all source code"},
		{Name: "precommit", Desc: "What the git hook runs"},
		{Name: "backend:test"},
		{Name: "web:test"},
		{Name: "backend:build"},
		{Name: "web:build"},
		{Name: "site:build"},
		{Name: "deploy:backend:build"},
		{Name: "backend:lint"},
		{Name: "web:lint"},
		{Name: "wt:new"},
	}
	reach := func(name string) []string {
		switch name {
		case "test":
			return []string{"test", "backend:test"}
		case "build":
			return []string{"build", "backend:build", "web:build"}
		case "lint":
			return []string{"lint", "backend:lint", "web:lint"}
		}
		return []string{name}
	}
	return tasks, reach
}

func matrixOf(t *testing.T, tasks []task.Task, reach func(string) []string, exempt []string) string {
	t.Helper()
	var buf bytes.Buffer
	printMatrix(&buf, cover.BuildGrid(tasks, reach, exempt))
	return buf.String()
}

// A row per aggregate, a column per namespace that answers one, and every state visible at
// once — which is the table people write by hand in a comment above the aggregates.
func TestTheMatrixShowsEveryStateAtOnce(t *testing.T) {
	tasks := []task.Task{
		{Name: "check", Desc: "Type-check everything"},
		{Name: "lint", Desc: "Lint all source code"},
		{Name: "test", Desc: "Run all automated tests"},
		{Name: "api:check"},
		{Name: "api:lint"},
		{Name: "backend:check"},
		{Name: "backend:lint"},
		{Name: "backend:test"},
		{Name: "deploy:test"},
		{Name: "web:test"},
	}
	reach := func(name string) []string {
		switch name {
		case "check":
			return []string{"check", "backend:check"}
		case "lint":
			return []string{"lint", "api:check", "api:lint", "backend:lint"}
		case "test":
			return []string{"test", "backend:test"}
		}
		return []string{name}
	}

	got := matrixOf(t, tasks, reach, []string{"deploy:*"})
	want := []string{
		"api backend deploy web",
		// api answers check but only lint reaches it; api answers lint and lint reaches it.
		"check · ✓ — —",
		"lint ✓ ✓ — —",
		// deploy:test is exempt, web:test is the real gap.
		"test — ✓ ~ ✗",
	}
	// Column padding is a rendering decision and asserting on it makes the test fail when
	// somebody widens a column; the marks and their order are the content.
	flat := squeeze(got)
	for _, line := range want {
		if !strings.Contains(flat, line) {
			t.Errorf("want a row %q in:\n%s", line, got)
		}
	}
}

// squeeze collapses runs of spaces so a row can be compared without its padding.
func squeeze(s string) string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	return strings.Join(out, "\n")
}

// The gap count is what the exit code is decided on, and a matrix has to agree with the list
// about how many there are — they are two views of one grid, and a table that said something
// different from the report would be worse than no table.
func TestTheMatrixAgreesWithTheReport(t *testing.T) {
	tasks, reach := codocShape()
	g := cover.BuildGrid(tasks, reach, nil)

	var buf bytes.Buffer
	fromMatrix := printMatrix(&buf, g)
	fromReport := 0
	for _, f := range g.Findings() {
		if !f.Note() {
			fromReport++
		}
	}
	if fromMatrix != fromReport {
		t.Errorf("matrix counted %d gaps, the report %d", fromMatrix, fromReport)
	}
}

// Only namespaces that answer some aggregate's verb get a column. `wt` answers none, and a
// column of nothing but `—` is a column about a namespace nobody asked about.
func TestTheMatrixLeavesOutNamespacesNobodyAsksAbout(t *testing.T) {
	tasks, reach := codocShape()
	if got := matrixOf(t, tasks, reach, nil); strings.Contains(got, "wt") {
		t.Errorf("wt answers no aggregate and should have no column:\n%s", got)
	}
}

// A Taskfile with no aggregates has no table, and says so rather than printing a heading
// with nothing under it.
func TestTheMatrixSaysWhenThereIsNothingToTabulate(t *testing.T) {
	tasks := []task.Task{{Name: "build"}, {Name: "wt:new"}}
	got := matrixOf(t, tasks, func(n string) []string { return []string{n} }, nil)
	if !strings.Contains(got, "no aggregate tasks here") {
		t.Errorf("got %q", got)
	}
}
