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

type Mode int

const (
	// Domain is an n-level split on `:` — `backend` > `migrate` > `down`.
	Domain Mode = iota
	// Verb is a two-level split on the last segment — `lint` > {api:lint, app:lint, …}.
	// The transpose of Domain: it collects the cross-cutting concerns that the domain tree
	// scatters.
	Verb
)

func (m Mode) Label() string {
	if m == Verb {
		return "verb"
	}
	return "domain"
}

func (m Mode) Toggled() Mode {
	if m == Verb {
		return Domain
	}
	return Verb
}

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

// RootGroup holds tasks with no namespace. Pinned to the top: in practice these are the
// daily drivers (`all`, `build`, `test`, `dev`, …).
const RootGroup = "(root)"

// OtherGroup is the verb-mode bucket for verbs that only ever appear once. Grouping a
// singleton reads worse than not grouping it at all, so they land here, flat.
const OtherGroup = "other"

func Build(mode Mode, tasks []task.Task, visible []int) *Tree {
	if mode == Verb {
		return buildVerb(tasks, visible)
	}
	return buildDomain(tasks, visible)
}

func buildDomain(tasks []task.Task, visible []int) *Tree {
	tree := &Tree{}
	// key -> node index, so repeated prefixes reuse the same node.
	index := map[string]int{}

	// Namespaced tasks first, so every group node exists before the unnamespaced ones are
	// placed. Otherwise a root-level `build` would land in (root) and `build:release`
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

		root, ok := index[RootGroup]
		if !ok {
			root = tree.push(RootGroup, RootGroup)
			tree.Roots = append(tree.Roots, root)
			index[RootGroup] = root
		}
		leaf := tree.push(name, name)
		tree.Nodes[leaf].Task = ti
		tree.Nodes[root].Children = append(tree.Nodes[root].Children, leaf)
	}

	sortDomain(tree)
	recount(tree)
	return tree
}

// sortDomain orders within a node: plain tasks first, then subgroups, each alphabetical.
// Keeps the leaf verbs of a namespace (`backend:build`, `backend:fmt`, …) together at the
// top instead of interleaving them with `backend:migrate`'s subtree.
func sortDomain(tree *Tree) {
	for i := range tree.Nodes {
		kids := tree.Nodes[i].Children
		sort.SliceStable(kids, func(a, b int) bool {
			na, nb := tree.Nodes[kids[a]], tree.Nodes[kids[b]]
			if na.IsGroup() != nb.IsGroup() {
				return !na.IsGroup()
			}
			return na.Label < nb.Label
		})
	}
	roots := tree.Roots
	sort.SliceStable(roots, func(a, b int) bool {
		// (root) is pinned first; everything else alphabetical.
		ka, kb := tree.Nodes[roots[a]].Key, tree.Nodes[roots[b]].Key
		if (ka != RootGroup) != (kb != RootGroup) {
			return ka == RootGroup
		}
		return ka < kb
	})
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

	// Singletons carry no grouping value — pool them.
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

	// Size descending, so the cross-cutting concerns you pivoted to find are on top.
	sort.SliceStable(groups, func(a, b int) bool {
		if len(groups[a].members) != len(groups[b].members) {
			return len(groups[a].members) > len(groups[b].members)
		}
		return groups[a].verb < groups[b].verb
	})

	tree := &Tree{}
	for _, g := range groups {
		// Root aggregate first, then the rest alphabetically. `lint` sits directly above
		// `app:lint`, `backend:lint`, `infra:lint` — which is exactly what `task lint`
		// will do, so the verb pivot doubles as a static preview of that run.
		members := g.members
		sort.SliceStable(members, func(a, b int) bool {
			na, nb := tasks[members[a]].Name, tasks[members[b]].Name
			ca, cb := strings.Contains(na, ":"), strings.Contains(nb, ":")
			if ca != cb {
				return !ca
			}
			return na < nb
		})
		gi := tree.push(g.verb, "verb:"+g.verb)
		tree.Roots = append(tree.Roots, gi)
		for _, ti := range members {
			// Leaves show the full colon path: flattening the domain into the label is the
			// entire point of this pivot, so we must not re-nest it.
			leaf := tree.push(tasks[ti].Name, fmt.Sprintf("verb:%s/%s", g.verb, tasks[ti].Name))
			tree.Nodes[leaf].Task = ti
			tree.Nodes[gi].Children = append(tree.Nodes[gi].Children, leaf)
		}
	}

	if len(singles) > 0 {
		sort.SliceStable(singles, func(a, b int) bool {
			return tasks[singles[a]].Name < tasks[singles[b]].Name
		})
		gi := tree.push(OtherGroup, "verb:"+OtherGroup)
		tree.Roots = append(tree.Roots, gi) // last
		for _, ti := range singles {
			leaf := tree.push(tasks[ti].Name, fmt.Sprintf("verb:%s/%s", OtherGroup, tasks[ti].Name))
			tree.Nodes[leaf].Task = ti
			tree.Nodes[gi].Children = append(tree.Nodes[gi].Children, leaf)
		}
	}

	recount(tree)
	return tree
}

func recount(tree *Tree) {
	for _, r := range tree.Roots {
		countSubtree(tree, r)
	}
}

func countSubtree(tree *Tree, idx int) int {
	n := 0
	if tree.Nodes[idx].Task != NoTask {
		n = 1
	}
	for _, c := range tree.Nodes[idx].Children {
		n += countSubtree(tree, c)
	}
	tree.Nodes[idx].Count = n
	return n
}

// Fixture builds a task list from bare names, for tests.
func Fixture(names []string) []task.Task {
	out := make([]task.Task, 0, len(names))
	for _, n := range names {
		out = append(out, task.Task{Name: n})
	}
	return out
}
