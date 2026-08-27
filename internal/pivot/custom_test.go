package pivot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/task"
)

// drawTree draws a tree the way the picker would, marking groups with a trailing slash, so
// the assertions read like the screen.
func drawTree(tree *Tree) string {
	var b strings.Builder
	for _, row := range tree.Flatten(func(string) bool { return true }) {
		n := tree.Nodes[row.Node]
		b.WriteString(strings.Repeat("  ", row.Depth))
		b.WriteString(n.Label)
		if n.IsGroup() {
			b.WriteString("/")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func build(t *testing.T, p Pivot, names []string) *Tree {
	t.Helper()
	tasks := Fixture(names)
	all := make([]int, len(tasks))
	for i := range tasks {
		all[i] = i
	}
	return Build(p, tasks, all)
}

// --- ByPath ---------------------------------------------------------------------------

func TestAPathPivotGroupsByWhateverItIsGiven(t *testing.T) {
	p := ByPath("first-letter", func(x task.Task) []string {
		return []string{string(x.Name[0])}
	})
	got := drawTree(build(t, p, []string{"apple", "avocado", "banana"}))
	want := "a/\n  apple\n  avocado\nb/\n  banana\n"
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// A leaf shows its full name. In a custom pivot the grouping is orthogonal to the name —
// grouping by owner does not make the owner part of what the task is called — so flattening
// the name into the leaf would throw away the only thing identifying it.
func TestAPathPivotLeavesKeepTheirWholeName(t *testing.T) {
	p := ByPath("layer", func(task.Task) []string { return []string{"all"} })
	got := drawTree(build(t, p, []string{"backend:migrate:down"}))
	if !strings.Contains(got, "backend:migrate:down") {
		t.Errorf("got:\n%s", got)
	}
}

func TestAPathPivotNests(t *testing.T) {
	p := ByPath("two", func(x task.Task) []string {
		return strings.SplitN(x.Name, ":", 2)
	})
	got := drawTree(build(t, p, []string{"a:one", "a:two", "b:three"}))
	if !strings.Contains(got, "a/\n  one/\n    a:one\n") {
		t.Errorf("got:\n%s", got)
	}
}

// A task the pivot has no answer for still has to be reachable. Dropping it would leave the
// list holding fewer tasks than the header claims.
func TestATaskThePivotCannotPlaceGoesToOther(t *testing.T) {
	p := ByPath("only-a", func(x task.Task) []string {
		if strings.HasPrefix(x.Name, "a") {
			return []string{"a"}
		}
		return nil
	})
	tree := build(t, p, []string{"apple", "banana", "cherry"})
	got := drawTree(tree)
	if !strings.Contains(got, OtherGroup) {
		t.Fatalf("no other group:\n%s", got)
	}
	if !strings.Contains(got, "banana") || !strings.Contains(got, "cherry") {
		t.Errorf("a homeless task went missing:\n%s", got)
	}
	// Every task is still counted.
	total := 0
	for _, r := range tree.Roots {
		total += tree.Nodes[r].Count
	}
	if total != 3 {
		t.Errorf("counted %d of 3", total)
	}
}

// `(other)` is where you look last, so it goes last.
func TestOtherSortsToTheBottom(t *testing.T) {
	p := ByPath("z-only", func(x task.Task) []string {
		if strings.HasPrefix(x.Name, "z") {
			return []string{"zeds"}
		}
		return nil
	})
	lines := strings.Split(strings.TrimSpace(drawTree(build(t, p, []string{"apple", "zebra"}))), "\n")
	if !strings.Contains(lines[len(lines)-2], OtherGroup) && !strings.Contains(lines[0], "zeds") {
		t.Errorf("got:\n%s", strings.Join(lines, "\n"))
	}
}

// --- regex specs ------------------------------------------------------------------------

func TestARegexSpecGroupsByItsCaptures(t *testing.T) {
	p, err := Spec{Name: "layer", Regex: `^([^:]+):([^:]+)`, Path: []string{"{1}", "{2}"}}.Compile(".")
	if err != nil {
		t.Fatal(err)
	}
	got := drawTree(build(t, p, []string{"backend:migrate:up", "backend:lint", "site:build"}))
	for _, want := range []string{"backend/", "  migrate/", "  lint/", "site/"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// One level from the first capture is what almost every naming convention wants.
func TestARegexSpecDefaultsToOneLevelOfTheFirstCapture(t *testing.T) {
	p, err := Spec{Name: "ns", Regex: `^([^:]+):`}.Compile(".")
	if err != nil {
		t.Fatal(err)
	}
	got := drawTree(build(t, p, []string{"backend:lint", "site:build"}))
	if !strings.Contains(got, "backend/") || !strings.Contains(got, "site/") {
		t.Errorf("got:\n%s", got)
	}
}

// With no capture groups there is nothing to refer to, so the match itself is the group.
func TestARegexWithNoGroupsUsesTheWholeMatch(t *testing.T) {
	p, err := Spec{Name: "kind", Regex: `^[a-z]+`}.Compile(".")
	if err != nil {
		t.Fatal(err)
	}
	got := drawTree(build(t, p, []string{"backend:lint", "backend:test"}))
	if !strings.Contains(got, "backend/") {
		t.Errorf("got:\n%s", got)
	}
}

// A name the pattern does not match is not an error — it is a task in `(other)`.
func TestARegexThatDoesNotMatchPoolsRatherThanFails(t *testing.T) {
	p, _ := Spec{Name: "ns", Regex: `^([^:]+):`}.Compile(".")
	got := drawTree(build(t, p, []string{"backend:lint", "standalone"}))
	if !strings.Contains(got, OtherGroup) || !strings.Contains(got, "standalone") {
		t.Errorf("got:\n%s", got)
	}
}

// A segment that came out empty would be a group with no name. Dropping it lets one pattern
// serve tasks at different depths.
func TestAnEmptySegmentIsDropped(t *testing.T) {
	p, _ := Spec{Name: "two", Regex: `^([^:]+):?([^:]*)`, Path: []string{"{1}", "{2}"}}.Compile(".")
	got := drawTree(build(t, p, []string{"backend:lint", "standalone"}))
	if !strings.Contains(got, "standalone/\n  standalone") {
		t.Errorf("a one-level name should make a one-level group:\n%s", got)
	}
}

func TestTheWaysASpecCanBeWrong(t *testing.T) {
	for _, c := range []struct {
		what string
		spec Spec
		says string
	}{
		{"no name", Spec{Regex: "x"}, "needs a name"},
		{"neither form", Spec{Name: "n"}, "needs a `regex` or a `command`"},
		{"both forms", Spec{Name: "n", Regex: "a", Command: []string{"b"}}, "pick one"},
		{"bad pattern", Spec{Name: "n", Regex: "^(["}, "missing closing"},
		{"capture out of range", Spec{Name: "n", Regex: `^(\w+)`, Path: []string{"{4}"}}, "group 4"},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := c.spec.Compile(".")
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("error is %q, want it to mention %q", err, c.says)
			}
		})
	}
}

// --- command specs ------------------------------------------------------------------------

// script writes an executable pivot program and returns the directory holding it.
func script(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pivot.sh")
	// Executable on purpose: the point of the fixture is that it can be run.
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The protocol is the whole contract: names in on stdin, `name<TAB>path` back on stdout.
func TestACommandPivotGroupsByWhatTheProgramSays(t *testing.T) {
	dir := script(t, `
while IFS= read -r name; do
  case "$name" in
    *deploy*) printf '%s\tdangerous\n' "$name" ;;
    *)        printf '%s\tsafe/ordinary\n' "$name" ;;
  esac
done
`)
	p, err := Spec{Name: "risk", Command: []string{"./pivot.sh"}}.Compile(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := drawTree(build(t, p, []string{"deploy:prod", "backend:lint"}))
	if !strings.Contains(got, "dangerous/\n  deploy:prod") {
		t.Errorf("got:\n%s", got)
	}
	if !strings.Contains(got, "safe/\n  ordinary/\n    backend:lint") {
		t.Errorf("nested paths should nest:\n%s", got)
	}
}

// A pivot that cannot answer should leave a usable list, not an empty one.
func TestAMissingProgramPoolsEverythingRatherThanEmptyingTheList(t *testing.T) {
	p, err := Spec{Name: "risk", Command: []string{"./does-not-exist"}}.Compile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := drawTree(build(t, p, []string{"a", "b"}))
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("tasks went missing when the program did:\n%s", got)
	}
	if !strings.Contains(got, OtherGroup) {
		t.Errorf("got:\n%s", got)
	}
}

func TestAProgramThatFailsIsTreatedTheSameWay(t *testing.T) {
	dir := script(t, "cat > /dev/null; exit 3\n")
	p, _ := Spec{Name: "risk", Command: []string{"./pivot.sh"}}.Compile(dir)
	got := drawTree(build(t, p, []string{"a"}))
	if !strings.Contains(got, "a") {
		t.Errorf("got:\n%s", got)
	}
}

// A line the program got wrong costs that line, not the run.
func TestMalformedOutputIsSkippedLineByLine(t *testing.T) {
	dir := script(t, `
cat > /dev/null
printf 'a\tgood\n'
printf 'this line has no tab\n'
printf '\n'
printf 'b\t\n'
`)
	p, _ := Spec{Name: "risk", Command: []string{"./pivot.sh"}}.Compile(dir)
	got := drawTree(build(t, p, []string{"a", "b"}))
	if !strings.Contains(got, "good/\n  a") {
		t.Errorf("the good line was lost:\n%s", got)
	}
	// `b` was given an empty path, which is no answer at all.
	if !strings.Contains(got, OtherGroup) || !strings.Contains(got, "b") {
		t.Errorf("got:\n%s", got)
	}
}

// The program is asked about every task, not the visible ones. A filter changes what is
// shown and cannot change where a task belongs — and asking about the whole list is what
// makes the answer cacheable, which is what keeps a process off every keystroke.
func TestTheProgramIsAskedAboutEveryTaskNotJustTheVisibleOnes(t *testing.T) {
	dir := script(t, "wc -l | tr -d ' ' | while read -r n; do printf 'a\\tsaw%s\\n' \"$n\"; done\n")
	p, _ := Spec{Name: "count", Command: []string{"./pivot.sh"}}.Compile(dir)

	tasks := Fixture([]string{"a", "b", "c"})
	tree := Build(p, tasks, []int{0, 1}) // only two are visible
	if got := drawTree(tree); !strings.Contains(got, "saw3") {
		t.Errorf("the program should have seen all three:\n%s", got)
	}
}

// Rebuild runs on every keystroke of a filter. Spawning a process each time would make the
// pivot the slowest thing in the program.
func TestTheProgramIsNotRunAgainForTheSameTaskList(t *testing.T) {
	dir := script(t, `
cat > /dev/null
count=$(cat runs 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > runs
printf 'a\tcalled%s\n' "$count"
`)
	p, _ := Spec{Name: "count", Command: []string{"./pivot.sh"}}.Compile(dir)
	tasks := Fixture([]string{"a", "b"})

	for range 5 {
		Build(p, tasks, []int{0, 1})
	}
	blob, err := os.ReadFile(filepath.Join(dir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(blob)); got != "1" {
		t.Errorf("the program ran %s times for five rebuilds of one task list", got)
	}

	// A different Taskfile is a different question.
	Build(p, Fixture([]string{"a", "b", "c"}), []int{0, 1, 2})
	blob, _ = os.ReadFile(filepath.Join(dir, "runs"))
	if got := strings.TrimSpace(string(blob)); got != "2" {
		t.Errorf("a changed task list should have asked again, ran %s times", got)
	}
}

// --- the file pivot -----------------------------------------------------------------------

func TestTheFilePivotGroupsByTaskfile(t *testing.T) {
	tasks := []task.Task{
		{Name: "backend:lint", Where: task.Where{File: "/proj/backend/Taskfile.yml", Line: 3}},
		{Name: "backend:test", Where: task.Where{File: "/proj/backend/Taskfile.yml", Line: 9}},
		{Name: "site:build", Where: task.Where{File: "/proj/site/Taskfile.yml", Line: 4}},
	}
	tree := Build(File(), tasks, []int{0, 1, 2})
	got := drawTree(tree)
	if !strings.Contains(got, "backend/Taskfile.yml/") || !strings.Contains(got, "site/Taskfile.yml/") {
		t.Errorf("got:\n%s", got)
	}
}

// The listing arrives on a background goroutine, so for the first moment of a session no
// task knows where it lives. That has to be a usable list, not an empty one.
func TestTheFilePivotBeforeTheListingArrives(t *testing.T) {
	tasks := Fixture([]string{"a", "b"})
	got := drawTree(Build(File(), tasks, []int{0, 1}))
	if !strings.Contains(got, OtherGroup) || !strings.Contains(got, "a") {
		t.Errorf("got:\n%s", got)
	}
}

func TestBuiltinsAreNamedAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Builtins() {
		if p.Name == "" {
			t.Error("a built-in with no name")
		}
		if seen[p.Name] {
			t.Errorf("two built-ins called %q", p.Name)
		}
		seen[p.Name] = true
		if p.Build == nil {
			t.Errorf("%q builds nothing", p.Name)
		}
	}
	for _, want := range []string{DomainName, VerbName, FileName} {
		if !seen[want] {
			t.Errorf("no built-in called %q", want)
		}
	}
}
