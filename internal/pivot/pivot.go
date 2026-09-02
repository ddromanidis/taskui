// Package pivot turns the flat task list into a fold tree.
//
// Grouping is a pivot, not a set of bespoke views: one flat list of tasks plus a key
// function, rendered as a fold tree. Adding a grouping means adding a builder here.
package pivot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ddromanidis/taskui/internal/task"
)

// The two groupings that were the whole of this package's vocabulary before pivots became
// values. Kept as names because the rest of the codebase — and the `--dump` flag, and every
// test — says `domain` and `verb` out loud.
const (
	// DomainName is an n-level split on `:` — `backend` > `migrate` > `down`.
	DomainName = "domain"
	// VerbName is a two-level split on the last segment — `lint` > {api:lint, app:lint, …}.
	// The transpose of the domain tree: it collects the cross-cutting concerns that one
	// scatters.
	VerbName = "verb"
	// FileName groups by the Taskfile each task is written in.
	FileName = "file"
)

// NoTask marks a node that groups tasks without being one.
const NoTask = -1

// Node is a node in the fold tree.
//
// Task and Children are independent: a node can be both a runnable task and a group
// header. `backend:migrate` is exactly that — it applies migrations and it is the parent
// of `backend:migrate:down`, `:prod`, `:status`, `:schema`.
type Node struct {
	// Label is what renders on the row.
	Label string
	// Key is a stable identity for fold state — it survives rebuilds and filtering.
	Key string
	// Task indexes into the app's task list, or NoTask.
	Task     int
	Children []int
	// Count is the number of tasks in this subtree, including Task itself.
	Count int
	// Rank pins a row to an end of its parent whatever the ordering says — see the constants
	// in order.go. Set by the builder, because which rows those are is part of what a pivot
	// means: an unnamespaced task opens the domain tree, one the grouping could not place
	// closes any tree that has one.
	Rank int
	// Facets are what the ordering compares, filled in after the tree is built.
	Facets Facets
}

func (n Node) IsGroup() bool { return len(n.Children) > 0 }

type Tree struct {
	Nodes []Node
	Roots []int
}

func (t *Tree) push(label, key string) int {
	t.Nodes = append(t.Nodes, Node{Label: label, Key: key, Task: NoTask})
	return len(t.Nodes) - 1
}

// Row is one visible line of the flattened tree.
type Row struct {
	Node  int
	Depth int
	// Open is meaningful for group rows only: whether this row's children are showing.
	Open bool
}

// Flatten walks depth-first, parents before children, honouring expanded.
func (t *Tree) Flatten(expanded func(string) bool) []Row {
	var rows []Row
	for _, r := range t.Roots {
		t.walk(r, 0, expanded, &rows)
	}
	return rows
}

func (t *Tree) walk(idx, depth int, expanded func(string) bool, out *[]Row) {
	node := t.Nodes[idx]
	open := node.IsGroup() && expanded(node.Key)
	*out = append(*out, Row{Node: idx, Depth: depth, Open: open})
	if open {
		for _, c := range node.Children {
			t.walk(c, depth+1, expanded, out)
		}
	}
}

// AncestorsOfTask lists every ancestor key of the node holding taskIdx, outermost first.
// Used to open the folds needed to reveal a selection after a pivot or a filter change.
func (t *Tree) AncestorsOfTask(taskIdx int) ([]string, bool) {
	for _, r := range t.Roots {
		var path []string
		if t.find(r, taskIdx, &path) {
			// The node itself is not its own ancestor.
			return path[:len(path)-1], true
		}
	}
	return nil, false
}

func (t *Tree) find(idx, taskIdx int, path *[]string) bool {
	node := t.Nodes[idx]
	*path = append(*path, node.Key)
	if node.Task == taskIdx {
		return true
	}
	for _, c := range node.Children {
		if t.find(c, taskIdx, path) {
			return true
		}
	}
	*path = (*path)[:len(*path)-1]
	return false
}

// A bucket that exists only because its members did not group is not a group.
//
// Both trees used to end in one. The domain tree pooled every unnamespaced task into
// `(root)`, and the verb tree pooled every verb answered once into `(other)`; folds start
// closed, so on the first frame both were a single row whose label named nothing but the
// absence of a grouping. In the domain tree that row hid `all`, `fmt`, `test`, `build` —
// the tasks a Taskfile exists to run — behind the one heading on screen that says least
// about what is under it. In the verb tree it hid whatever the transpose had failed to
// find a concern in, which is the half of the list you did not already know about.
//
// So they are not folds any more. A task with nothing to group with is a top-level row of
// its own, and the position those buckets carried survives as a Rank: unnamespaced tasks
// open the domain tree, once-answered verbs close the verb tree. Same rows, same order,
// one less keypress and one less label that means nothing.

// Build makes the tree for a pivot and puts it in order.
//
// Two steps, deliberately separate: a builder decides what is inside what, and the Order
// decides what sits above what. Grouping is the pivot's business and ordering is yours.
func Build(p Pivot, tasks []task.Task, visible []int, ord Order) *Tree {
	build := p.Build
	if build == nil {
		build = buildDomain
	}
	tree := build(tasks, visible)
	ord.finish(tree, tasks, p.Natural)
	return tree
}

func buildDomain(tasks []task.Task, visible []int) *Tree {
	tree := &Tree{}
	// key -> node index, so repeated prefixes reuse the same node.
	index := map[string]int{}

	// Namespaced tasks first, so every group node exists before the unnamespaced ones are
	// placed. Otherwise a root-level `build` would stand on its own and `build:release`
	// would then create a second, unrelated `build` node beside it.
	for _, ti := range visible {
		segs := tasks[ti].Segments()
		if len(segs) == 1 {
			continue
		}

		// Walk the prefix, creating nodes as needed, and attach the task to the node that
		// matches its full path — which may already exist as a group.
		parent := NoTask
		for depth := range segs {
			key := strings.Join(segs[:depth+1], ":")
			node, existing := index[key]
			if !existing {
				node = tree.push(segs[depth], key)
				if parent == NoTask {
					tree.Roots = append(tree.Roots, node)
				} else {
					tree.Nodes[parent].Children = append(tree.Nodes[parent].Children, node)
				}
				index[key] = node
			}
			if depth == len(segs)-1 {
				tree.Nodes[node].Task = ti
			}
			parent = node
		}
	}

	for _, ti := range visible {
		name := tasks[ti].Name
		if strings.Contains(name, ":") {
			continue
		}

		// A root-level task whose name is also a namespace belongs to that namespace, not
		// beside it. `build` and `build:release` are one thing with a subtask, the same
		// shape as `backend:migrate` and `backend:migrate:down`.
		if node, ok := index[name]; ok {
			tree.Nodes[node].Task = ti
			continue
		}

		// Otherwise it is a row of the tree itself, above the namespaces rather than inside
		// anything. Ranked first rather than sorted there: these are the daily drivers, and
		// `fmt` landing between `infra` and `site` because of where an `f` sorts would be an
		// accident wearing the clothes of a decision.
		leaf := tree.push(name, name)
		tree.Nodes[leaf].Task = ti
		tree.Nodes[leaf].Rank = RankFirst
		tree.Roots = append(tree.Roots, leaf)
	}

	return tree
}

func buildVerb(tasks []task.Task, visible []int) *Tree {
	byVerb := map[string][]int{}
	for _, ti := range visible {
		v := tasks[ti].Verb()
		byVerb[v] = append(byVerb[v], ti)
	}
	verbs := make([]string, 0, len(byVerb))
	for v := range byVerb {
		verbs = append(verbs, v)
	}
	sort.Strings(verbs)

	// A verb one task answers is not a grouping — set those aside.
	type group struct {
		verb    string
		members []int
	}
	var groups []group
	var singles []int
	for _, v := range verbs {
		if members := byVerb[v]; len(members) > 1 {
			groups = append(groups, group{v, members})
		} else {
			singles = append(singles, members...)
		}
	}

	// Size descending is this pivot's natural order — see Builtins. The cross-cutting
	// concerns you pivoted in order to find are the ones with the most members in them.
	tree := &Tree{}
	for _, g := range groups {
		gi := tree.push(g.verb, "verb:"+g.verb)
		tree.Roots = append(tree.Roots, gi)
		for _, ti := range g.members {
			// Leaves show the full colon path: flattening the domain into the label is the
			// entire point of this pivot, so we must not re-nest it.
			leaf := tree.push(tasks[ti].Name, fmt.Sprintf("verb:%s/%s", g.verb, tasks[ti].Name))
			tree.Nodes[leaf].Task = ti
			// The root aggregate sits directly above its own fan-out: `lint` above
			// `app:lint`, `backend:lint`, `infra:lint` — which is exactly what `task lint`
			// will do, so the verb pivot doubles as a static preview of that run. A position
			// that carries a meaning, so it is a Rank rather than a sort key.
			if !strings.Contains(tasks[ti].Name, ":") {
				tree.Nodes[leaf].Rank = RankFirst
			}
			tree.Nodes[gi].Children = append(tree.Nodes[gi].Children, leaf)
		}
	}

	// A verb only one task answers is a row of the tree itself. Ranked last, so the concerns
	// this pivot was opened to find still sit above the tasks it found none in — but visible,
	// under their own names, which is the only thing that says what they are.
	for _, ti := range singles {
		leaf := tree.push(tasks[ti].Name, "verb:"+tasks[ti].Name)
		tree.Nodes[leaf].Task = ti
		tree.Nodes[leaf].Rank = RankLast
		tree.Roots = append(tree.Roots, leaf)
	}

	return tree
}

// Fixture builds a task list from bare names, for tests.
func Fixture(names []string) []task.Task {
	out := make([]task.Task, 0, len(names))
	for _, n := range names {
		out = append(out, task.Task{Name: n})
	}
	return out
}
