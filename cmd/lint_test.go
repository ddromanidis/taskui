package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/task"
)

// codocShape is the arrangement this check was written against, reduced to the parts that
// decide anything: a root `test` that reaches only the backend while `web:test` exists, a
// root `build` that reaches two of the four namespaces claiming the verb, and a `lint` with
// nothing wrong with it.
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

func gapNames(found []finding) []string {
	var out []string
	for _, f := range found {
		if !f.note() {
			out = append(out, strings.Join(f.Tasks, ","))
		}
	}
	return out
}

// The finding that motivated the whole thing: `web:test` exists, root `test` says "Run all
// automated tests", and the graph never gets there.
func TestCoverageFindsTheDeclaredButUnreached(t *testing.T) {
	tasks, reach := codocShape()
	got := gapNames(coverage(tasks, reach, nil))

	want := []string{"web:test", "site:build", "deploy:backend:build"}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s is declared and unreached; not reported. got %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("want %d gaps, got %d: %v", len(want), len(got), got)
	}
}

// An aggregate whose namespaces are all reached has nothing to say, and neither has a root
// task nobody answers (`precommit`) or a namespace with no root task (`wt:new`). A check
// that reported those would be reporting the ordinary shape of a Taskfile.
func TestCoverageIsQuietWhenThereIsNothingToSay(t *testing.T) {
	tasks, reach := codocShape()
	for _, f := range coverage(tasks, reach, nil) {
		if f.Aggregate == "lint" || f.Aggregate == "precommit" {
			t.Errorf("%s has nothing wrong with it, reported anyway: %+v", f.Aggregate, f)
		}
	}
}

// Reachability and not names. xerum's `lint` never calls `api:lint` — it calls `api:check`,
// which reaches `api:tenant:lint` two levels down — and a check that matched names would
// call that a gap and be wrong.
func TestCoverageFollowsTheGraphRatherThanTheName(t *testing.T) {
	tasks := []task.Task{
		{Name: "lint", Desc: "Lint all source code"},
		{Name: "api:check"},
		{Name: "api:tenant:lint"},
	}
	reach := func(name string) []string {
		if name == "lint" {
			return []string{"lint", "api:check", "api:tenant:lint"}
		}
		return []string{name}
	}
	if found := coverage(tasks, reach, nil); len(found) != 0 {
		t.Errorf("api is reached through api:check; reported anyway: %+v", found)
	}
}

// Per namespace, not per task. xerum's root `build` calls `app:dist` rather than
// `app:build`, on purpose, and `app:build` is not missing from anything — the namespace got
// its chance to run.
func TestCoverageAsksAboutNamespacesNotTasks(t *testing.T) {
	tasks := []task.Task{
		{Name: "build", Desc: "Build all components"},
		{Name: "app:build"},
		{Name: "app:dist"},
	}
	reach := func(name string) []string {
		if name == "build" {
			return []string{"build", "app:dist"}
		}
		return []string{name}
	}
	if found := coverage(tasks, reach, nil); len(found) != 0 {
		t.Errorf("app was reached through app:dist; reported anyway: %+v", found)
	}
}

// Covered by another aggregate is a note and not a gap: xerum's `api:check` is deliberately
// absent from `check`, which promises not to codegen, and present in `lint`. Saying so beats
// both failing over it and staying silent.
func TestCoverageDowngradesWhatAnotherAggregateReaches(t *testing.T) {
	tasks := []task.Task{
		{Name: "check", Desc: "Type-check everything"},
		{Name: "lint", Desc: "Lint all source code"},
		{Name: "api:check"},
		{Name: "api:lint"},
	}
	reach := func(name string) []string {
		switch name {
		case "check":
			return []string{"check"}
		case "lint":
			return []string{"lint", "api:check", "api:lint"}
		}
		return []string{name}
	}

	found := coverage(tasks, reach, nil)
	if len(found) != 1 {
		t.Fatalf("want one finding, got %+v", found)
	}
	if !found[0].note() {
		t.Errorf("api:check is reached by lint, so this is a note, not a gap: %+v", found[0])
	}
	if !slices.Contains(found[0].Elsewhere, "lint") {
		t.Errorf("the note should name lint as what covers it: %+v", found[0])
	}
}

// A glob silences the deliberate ones, which on both real repositories is most of a first
// run — a deploy that must not fire from a local gate, a docs build.
func TestCoverageHonoursTheExemptions(t *testing.T) {
	tasks, reach := codocShape()
	got := gapNames(coverage(tasks, reach, []string{"deploy:*", "site:build"}))

	if !slices.Equal(got, []string{"web:test"}) {
		t.Errorf("deploy:* and site:build are exempt, so only web:test is left; got %v", got)
	}
}

func TestCoverPatternsReadsGlobsAndSkipsComments(t *testing.T) {
	dir := t.TempDir()
	body := "# the deploy pipeline runs from CI, never from a local gate\ndeploy:*\n\nsite:build # its own workflow\n"
	if err := os.WriteFile(filepath.Join(dir, CoverFile), []byte(body), 0o600); err != nil {
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
