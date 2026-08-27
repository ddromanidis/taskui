package pivot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/task"
)

// Shape lifted from a real Taskfile: root aggregates, several namespaces, and namespaces
// that nest three deep.
func sample() []task.Task {
	return Fixture([]string{
		"all",
		"build",
		"fmt",
		"lint",
		"app:build",
		"app:fmt",
		"app:lint",
		"backend:build",
		"backend:fmt",
		"backend:lint",
		"backend:migrate",
		"backend:migrate:down",
		"backend:migrate:prod",
		"infra:lint",
		"site:build",
		"wt:ls",
	})
}

func all(tasks []task.Task) []int {
	out := make([]int, len(tasks))
	for i := range tasks {
		out[i] = i
	}
	return out
}

func render(tree *Tree) []string {
	var out []string
	for _, r := range tree.Flatten(func(string) bool { return true }) {
		out = append(out, strings.Repeat("  ", r.Depth)+tree.Nodes[r.Node].Label)
	}
	return out
}

func TestDomainPinsRootGroupFirst(t *testing.T) {
	tasks := sample()
	tree := Build(Domain(), tasks, all(tasks))
	if got := tree.Nodes[tree.Roots[0]].Label; got != RootGroup {
		t.Errorf("first root = %q", got)
	}
	if got := tree.Nodes[tree.Roots[0]].Count; got != 4 {
		t.Errorf("(root) count = %d", got)
	}
	var rest []string
	for _, r := range tree.Roots[1:] {
		rest = append(rest, tree.Nodes[r].Label)
	}
	want := []string{"app", "backend", "infra", "site", "wt"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %v, want %v", rest, want)
	}
}

// The same must hold when the parent has no namespace of its own: `build` and
// `build:release` were being shown as two unrelated roots.
func TestARootTaskThatNamesANamespaceOwnsIt(t *testing.T) {
	tasks := Fixture([]string{"all", "build", "build:release", "test", "test:one"})
	tree := Build(Domain(), tasks, all(tasks))

	var roots []string
	for _, r := range tree.Roots {
		roots = append(roots, tree.Nodes[r].Label)
	}
	if want := []string{RootGroup, "build", "test"}; !reflect.DeepEqual(roots, want) {
		t.Errorf("one `build`, not two: %v", roots)
	}

	var build *Node
	for i := range tree.Nodes {
		if tree.Nodes[i].Key == "build" {
			build = &tree.Nodes[i]
		}
	}
	if build == nil || !build.IsGroup() {
		t.Fatalf("build should parent build:release: %+v", build)
	}
	if build.Task == NoTask {
		t.Error("and should still be runnable itself")
	}
	if build.Count != 2 {
		t.Errorf("count = %d", build.Count)
	}

	// (root) keeps only what genuinely has no namespace.
	var kids []string
	for _, c := range tree.Nodes[tree.Roots[0]].Children {
		kids = append(kids, tree.Nodes[c].Label)
	}
	if !reflect.DeepEqual(kids, []string{"all"}) {
		t.Errorf("(root) kids = %v", kids)
	}
}

// `backend:migrate` applies migrations and parents `backend:migrate:down`. The node has to
// be both, or one of the two disappears from the UI.
func TestDomainNodeCanBeBothGroupAndTask(t *testing.T) {
	tasks := sample()
	tree := Build(Domain(), tasks, all(tasks))
	var migrate *Node
	for i := range tree.Nodes {
		if tree.Nodes[i].Key == "backend:migrate" {
			migrate = &tree.Nodes[i]
		}
	}
	if migrate == nil {
		t.Fatal("no backend:migrate node")
	}
	if !migrate.IsGroup() {
		t.Error("should parent its subtasks")
	}
	if migrate.Task == NoTask {
		t.Error("should still be runnable itself")
	}
	if migrate.Count != 3 {
		t.Errorf("itself plus down plus prod: %d", migrate.Count)
	}
}

func TestDomainPutsPlainTasksBeforeSubgroups(t *testing.T) {
	tasks := sample()
	lines := render(Build(Domain(), tasks, all(tasks)))
	at := -1
	for i, l := range lines {
		if l == "backend" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no backend row: %v", lines)
	}
	want := []string{"  build", "  fmt", "  lint", "  migrate"}
	if got := lines[at+1 : at+5]; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestVerbGroupsByLastSegmentSizeDescending(t *testing.T) {
	tasks := sample()
	tree := Build(Verb(), tasks, all(tasks))
	type gc struct {
		label string
		count int
	}
	var groups []gc
	for _, r := range tree.Roots {
		groups = append(groups, gc{tree.Nodes[r].Label, tree.Nodes[r].Count})
	}
	// build and lint tie at 4; ties break alphabetically.
	if groups[0] != (gc{"build", 4}) || groups[1] != (gc{"lint", 4}) || groups[2] != (gc{"fmt", 3}) {
		t.Errorf("groups = %v", groups)
	}
}

// Grouping a verb that only appears once reads worse than not grouping it, so the
// singletons pool into one flat bucket, pinned last.
func TestVerbPoolsSingletonsIntoOtherLast(t *testing.T) {
	tasks := sample()
	tree := Build(Verb(), tasks, all(tasks))
	last := tree.Roots[len(tree.Roots)-1]
	if tree.Nodes[last].Label != OtherGroup {
		t.Fatalf("last root = %q", tree.Nodes[last].Label)
	}
	var members []string
	for _, c := range tree.Nodes[last].Children {
		members = append(members, tree.Nodes[c].Label)
	}
	want := []string{"all", "backend:migrate", "backend:migrate:down", "backend:migrate:prod", "wt:ls"}
	if !reflect.DeepEqual(members, want) {
		t.Errorf("members = %v, want %v", members, want)
	}
}

// The pivot must flatten the domain into the leaf label — re-nesting it would undo the
// point of the view.
func TestVerbLeavesCarryTheFullColonPath(t *testing.T) {
	tasks := sample()
	tree := Build(Verb(), tasks, all(tasks))
	var lint *Node
	for _, r := range tree.Roots {
		if tree.Nodes[r].Label == "lint" {
			lint = &tree.Nodes[r]
		}
	}
	if lint == nil {
		t.Fatal("no lint group")
	}
	var members []string
	for _, c := range lint.Children {
		members = append(members, tree.Nodes[c].Label)
		if tree.Nodes[c].IsGroup() {
			t.Errorf("%q was re-nested", tree.Nodes[c].Label)
		}
	}
	want := []string{"lint", "app:lint", "backend:lint", "infra:lint"}
	if !reflect.DeepEqual(members, want) {
		t.Errorf("members = %v, want %v", members, want)
	}
}
