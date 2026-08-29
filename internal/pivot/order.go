package pivot

import (
	"sort"
	"strings"

	"github.com/ddromanidis/taskui/internal/task"
)

// Ordering: one pass, after the tree is built.
//
// Each of the three built-in pivots used to sort its own nodes its own way — domain
// alphabetically with subgroups sunk to the bottom, verb by group size with the bare
// aggregate hoisted, path pivots alphabetically with `(other)` pushed last. Three rules in
// three places, none of them nameable, none of them yours.
//
// They are one rule now, and the differences between them are data on the nodes: Rank for
// the handful of rows that must sit at an end whatever else is true, and Facets for what
// there is to compare. A pivot names the order it wants to be read in; the config can name
// a different one; and the comparator below is the only place that decides anything.

// By names the key rows are compared on.
type By string

const (
	// ByNatural defers to whatever the active pivot says it wants. The zero value, so a
	// caller that has no opinion gets each grouping read the way it was designed to be.
	ByNatural By = ""
	// ByName is alphabetical, which is what you want when you know what you are looking for.
	ByName By = "name"
	// ByFile is the order the tasks are written in the Taskfile — the one order a human
	// chose on purpose. Needs the locations from the JSON listing, which arrive a beat after
	// the first frame; until then every task compares equal and falls through to the name.
	ByFile By = "file"
	// ByRecent is most recently run first, from the archive.
	ByRecent By = "recent"
	// ByFailed puts what is broken on top, most recent first within it.
	ByFailed By = "failed"
	// BySize is the biggest group first — the verb pivot's own rule, available everywhere.
	BySize By = "size"
)

// Orders lists every ordering, for the config validator and its error messages.
func Orders() []By { return []By{ByNatural, ByName, ByFile, ByRecent, ByFailed, BySize} }

// String spells ByNatural as `default`, which is what it is called in a config file. The
// empty string is the right zero value in Go and the wrong thing to write in YAML.
func (b By) String() string {
	if b == ByNatural {
		return "default"
	}
	return string(b)
}

// ParseBy reads an ordering out of a config file. Reported rather than guessed at: an order
// that silently stayed on the default is indistinguishable from one that does not work.
func ParseBy(text string) (By, bool) {
	name := strings.ToLower(strings.TrimSpace(text))
	if name == "default" || name == "" {
		return ByNatural, true
	}
	for _, b := range Orders() {
		if b != ByNatural && string(b) == name {
			return b, true
		}
	}
	return ByNatural, false
}

// OrderNames spells every ordering, for help text.
func OrderNames() []string {
	out := make([]string, 0, len(Orders()))
	for _, b := range Orders() {
		out = append(out, b.String())
	}
	return out
}

// Outcome is how a task went last time — the two facts ordering needs, and no more. The
// archive's own Outcome is this shape; it is restated rather than imported so that the
// package that decides what sits above what does not depend on the one that stores runs.
type Outcome struct {
	Ok       bool
	WhenUnix int64
}

// Order is everything that decides what sits above what.
//
// The zero value is what taskui has always done: each pivot's own order, subgroups below
// the plain tasks of the node they sit in, nothing pinned.
type Order struct {
	By By
	// Interleave sorts a node's subgroups among its plain tasks rather than below them.
	//
	// Off by default, which keeps a namespace's own verbs together as one block instead of
	// splitting them apart with a subtree. Worth turning on with `recent` or `failed`, where
	// the whole point is that the interesting row rises to the top and a group holding it
	// would otherwise still be stuck below every leaf beside it.
	Interleave bool
	// Pins are patterns hoisted above everything else, in the order they are written.
	//
	// Matched against task names with the same `*` globbing as `.taskui-danger`. A group
	// rises with anything it contains, so pinning `backend:test` also lifts `backend` to the
	// top of the roots — a pin you have to go looking for is not a pin.
	Pins []string
	// Ran says how a task went last time. A nil func — or a task the archive has never seen
	// — makes `recent` and `failed` fall through to the name rather than inventing an order.
	Ran func(name string) (Outcome, bool)
}

// Rank is the coarse bucket a node sits in, and it beats every other consideration.
//
// For the few rows whose position is part of what the pivot means rather than a matter of
// taste: `(root)` opens the domain tree, `(other)` closes any tree that has one, and a verb
// group's own aggregate sits directly above its fan-out.
const (
	RankFirst  = -1
	RankNormal = 0
	RankLast   = 1
)

// noPin sorts every unpinned node below every pinned one.
const noPin = int(^uint(0) >> 1)

// Facets are what an Order compares, aggregated over a node's whole subtree.
//
// A group is as recent as its most recent task and as broken as its worst one, because a
// group is a thing you fold and what you want to know before opening it is whether there is
// anything in there worth opening.
type Facets struct {
	// Newest is the last time anything in this subtree ran, or 0 for never.
	Newest int64
	// Broken says whether anything in it failed the last time it ran.
	Broken bool
	// Where is the earliest declaration site in the subtree, zero if unknown.
	Where task.Where
	// Pin is the index of the pin pattern this node answers to, or noPin.
	Pin int
}

// finish fills in the counts and facets and then sorts the whole tree.
func (o Order) finish(tree *Tree, tasks []task.Task, natural By) {
	for _, r := range tree.Roots {
		o.gather(tree, tasks, r)
	}
	o.sort(tree, natural)
}

// gather walks a subtree filling in Count and Facets, and returns what it found so the
// parent can fold it into its own.
func (o Order) gather(tree *Tree, tasks []task.Task, idx int) (int, Facets) {
	facets := Facets{Pin: noPin}
	count := 0

	if ti := tree.Nodes[idx].Task; ti != NoTask && ti < len(tasks) {
		count = 1
		t := tasks[ti]
		facets.Where = t.Where
		if o.Ran != nil {
			if outcome, seen := o.Ran(t.Name); seen {
				facets.Newest = outcome.WhenUnix
				facets.Broken = !outcome.Ok
			}
		}
		facets.Pin = o.pinOf(t.Name)
	} else {
		// A node with no task of its own is a pure namespace, and the only names it has are
		// the ones the pivot gave it: `backend:migrate` in the domain tree, `api/Taskfile.yml`
		// in the file tree. Its label is *not* consulted when it has a task, because in the
		// domain tree a label is one path segment — matching it would let `pin: [release]`
		// hoist `build:release`, which is not the task you named.
		facets.Pin = min(o.pinOf(tree.Nodes[idx].Key), o.pinOf(tree.Nodes[idx].Label))
	}

	for _, c := range tree.Nodes[idx].Children {
		n, sub := o.gather(tree, tasks, c)
		count += n
		facets.Newest = max(facets.Newest, sub.Newest)
		facets.Broken = facets.Broken || sub.Broken
		facets.Pin = min(facets.Pin, sub.Pin)
		facets.Where = earlier(facets.Where, sub.Where)
	}

	tree.Nodes[idx].Count = count
	tree.Nodes[idx].Facets = facets
	return count, facets
}

// earlier is the earlier of two declaration sites, treating an unknown one as no answer at
// all rather than as the top of the file.
func earlier(a, b task.Where) task.Where {
	switch {
	case !b.Ok():
		return a
	case !a.Ok():
		return b
	case b.File != a.File:
		if b.File < a.File {
			return b
		}
		return a
	case b.Line < a.Line:
		return b
	default:
		return a
	}
}

func (o Order) pinOf(name string) int {
	for i, pattern := range o.Pins {
		if task.GlobMatch(pattern, name) {
			return i
		}
	}
	return noPin
}

// sort orders every child list and the roots by the same comparator.
func (o Order) sort(tree *Tree, natural By) {
	by := o.By
	if by == ByNatural {
		by = natural
	}
	order := func(ids []int) {
		sort.SliceStable(ids, func(a, b int) bool {
			return o.less(tree.Nodes[ids[a]], tree.Nodes[ids[b]], by)
		})
	}
	for i := range tree.Nodes {
		order(tree.Nodes[i].Children)
	}
	order(tree.Roots)
}

// less is the whole ordering, in the order the questions are asked.
func (o Order) less(a, b Node, by By) bool {
	// What you pinned, then what the pivot insists on, then whether it is a group at all —
	// all three are about *where a row belongs*, and they settle it before anything as
	// negotiable as which key you are sorting on gets a say.
	if a.Facets.Pin != b.Facets.Pin {
		return a.Facets.Pin < b.Facets.Pin
	}
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	if !o.Interleave && a.IsGroup() != b.IsGroup() {
		return !a.IsGroup()
	}
	if decided, ok := compare(a, b, by); ok {
		return decided
	}
	// Every order ends here, so two rows that tie on the key are still in a fixed place
	// rather than wherever the last rebuild happened to leave them.
	if a.Label != b.Label {
		return a.Label < b.Label
	}
	return a.Key < b.Key
}

// compare answers the ordering's own question, or says it has no answer and leaves the two
// rows to the name.
func compare(a, b Node, by By) (bool, bool) {
	switch by {
	case ByFile:
		if a.Facets.Where.Ok() != b.Facets.Where.Ok() {
			// A task whose location has not arrived yet sinks rather than claiming line 0.
			return a.Facets.Where.Ok(), true
		}
		if a.Facets.Where.File != b.Facets.Where.File {
			return a.Facets.Where.File < b.Facets.Where.File, true
		}
		if a.Facets.Where.Line != b.Facets.Where.Line {
			return a.Facets.Where.Line < b.Facets.Where.Line, true
		}
	case ByRecent:
		if a.Facets.Newest != b.Facets.Newest {
			return a.Facets.Newest > b.Facets.Newest, true
		}
	case ByFailed:
		if a.Facets.Broken != b.Facets.Broken {
			return a.Facets.Broken, true
		}
		// Within either half, most recent first: the failure you just caused is the one you
		// are here about.
		if a.Facets.Newest != b.Facets.Newest {
			return a.Facets.Newest > b.Facets.Newest, true
		}
	case BySize:
		if a.Count != b.Count {
			return a.Count > b.Count, true
		}
	case ByNatural, ByName:
	}
	return false, false
}
