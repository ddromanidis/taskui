// Package cover answers the one claim a Taskfile makes that nothing else can check: that an
// aggregate covers what its name says it covers.
//
// `task test` says "Run all automated tests" and runs `deps: [backend:test]`. Whether that
// is the whole truth depends on which namespaces have a `test` and which of them the
// execution graph actually reaches, and those are two facts kept in different files with a
// `desc:` string in between claiming they agree. Nothing checks the claim: go-task runs
// what it is told, yamllint reads syntax, and the description is prose.
//
// taskui already holds both halves — `internal/task` for the flat list of colon paths,
// `internal/graph` for what a root reaches — so the check is the set difference between
// them, and this package is the difference plus a way to say "that one is on purpose".
//
// Reachability, not names, is what makes it correct. xerum's `lint` never calls `api:lint`;
// it calls `api:check`, which reaches `api:tenant:lint` two levels down. A check that
// matched names would report that as a gap and be wrong.
//
// It lives here rather than beside `--lint` because two things ask the question now. The
// picker annotates a namespace with the aggregates that reach it, `--lint` prints the ones
// that do not, and a namespace the tree calls covered while the linter calls it a gap would
// be a tool arguing with itself.
package cover

import (
	"maps"
	"slices"
	"strings"

	"github.com/ddromanidis/taskui/internal/task"
)

// File is the opt-in file listing tasks no aggregate is expected to reach.
//
// The same shape as `.taskui-danger`, for the same reason: one glob per line, `#` to the end
// of a line is a comment, and a repository that does not have one loses nothing. Deliberate
// exclusions are the majority of what a first run reports — a deploy that must not fire from
// a local gate, a docs build, an artifact for another platform — and a check whose findings
// are mostly correct-as-written is a check people stop reading.
const File = ".taskui-cover"

// A Finding is one namespace an aggregate claims and does not reach.
//
// Tasks is what that namespace answers the verb with, which is nearly always one name; it is
// a list because nothing stops a namespace from having `api:tenant:check` and
// `api:control:check` and the report should name both rather than pick.
type Finding struct {
	// Aggregate is the root task whose graph was walked, and Desc is what it says it does —
	// the claim being checked, worth printing beside the thing that contradicts it.
	Aggregate string
	Desc      string
	Namespace string
	Tasks     []string
	// Elsewhere is the other aggregates that do reach it. Non-empty turns a gap into a note:
	// xerum's `api:check` is missing from `check` and reached by `lint`, which is deliberate
	// and documented — `check` promises not to codegen. That is worth saying and not worth
	// failing over, so a finding with somewhere to point does not count towards the exit
	// code.
	Elsewhere []string
}

// Note reports whether this is the softer kind: covered, just not by the aggregate that
// shares its verb.
func (f Finding) Note() bool { return len(f.Elsewhere) > 0 }

// NamespaceOf is the first segment of a colon path, and "" for a root-level task.
func NamespaceOf(name string) string {
	ns, _, found := strings.Cut(name, ":")
	if !found {
		return ""
	}
	return ns
}

// Coverage is the check itself, over a list of tasks and a way to ask what a root reaches.
//
// reach is injected rather than called, because resolving a graph spawns a `task --summary`
// per node and a test should not need go-task installed to state what this does with a
// given shape.
//
// The rule is per namespace and not per task, which is a deliberate loosening. xerum's root
// `build` calls `app:dist` and not `app:build` — on purpose, because dist stages both apps
// where a deploy consumes them — and a per-task rule reports that, and `app:build` is not
// missing from anything. Measured across both repositories the per-task rule found eleven
// things where this one finds four, and the seven were all correct as written. A namespace
// the aggregate reaches at all has been given its chance to run.
func Coverage(tasks []task.Task, reach func(string) []string, exempt []string) []Finding {
	return BuildGrid(tasks, reach, exempt).Findings()
}

// State is what one aggregate does about one namespace.
type State int

const (
	// Absent means the namespace does not answer this verb at all, which is most of the grid
	// and is not a finding — `site` has no `test` and should not.
	Absent State = iota
	Covered
	// CoveredElsewhere is not reached by the aggregate sharing its verb, but reached by
	// another.
	CoveredElsewhere
	Exempt
	Gap
)

// Grid is the whole answer: every aggregate against every namespace that answers any
// aggregate's verb.
//
// Built once and projected three ways. The picker annotates a namespace from it, `--lint`
// prints the disagreements, `--matrix` prints the table they came out of, and computing
// them separately would be three chances to disagree about what covered means.
type Grid struct {
	// Rows in the order the Taskfile declares them, so a matrix reads down the file rather
	// than alphabetically.
	Rows []task.Task
	// Columns sorted, and only namespaces that answer at least one aggregate's verb — a
	// column of nothing but `—` is a column about a namespace nobody asked about.
	Columns []string
	Cells   map[string]map[string]State
	// Tasks is what a namespace answers a verb with, for naming it in a Finding.
	Tasks     map[string]map[string][]string
	Elsewhere map[string]map[string][]string
}

func BuildGrid(tasks []task.Task, reach func(string) []string, exemptions []string) Grid {
	// The verb every namespaced task answers, so an aggregate can ask who claims its name.
	byVerb := map[string][]task.Task{}
	for _, t := range tasks {
		if NamespaceOf(t.Name) != "" {
			byVerb[t.Verb()] = append(byVerb[t.Verb()], t)
		}
	}

	// An aggregate is a root-level task some namespace also answers. `wt:new` has no root
	// task and `precommit` has no namespace answering it; neither is making a claim about
	// coverage, so neither is checked.
	g := Grid{
		Cells:     map[string]map[string]State{},
		Tasks:     map[string]map[string][]string{},
		Elsewhere: map[string]map[string][]string{},
	}
	for _, t := range tasks {
		if NamespaceOf(t.Name) == "" && len(byVerb[t.Name]) > 0 {
			g.Rows = append(g.Rows, t)
		}
	}

	// Resolved once each and kept, because Elsewhere asks every aggregate about every task
	// and re-walking a graph per question would be dozens of process spawns for an answer
	// already on hand.
	reached := map[string]map[string]bool{}
	for _, r := range g.Rows {
		set := map[string]bool{}
		for _, name := range reach(r.Name) {
			set[name] = true
		}
		reached[r.Name] = set
	}

	columns := map[string]bool{}
	for _, r := range g.Rows {
		// Which namespaces this aggregate got to at all, by any route and at any depth.
		got := map[string]bool{}
		for name := range reached[r.Name] {
			got[NamespaceOf(name)] = true
		}

		g.Cells[r.Name] = map[string]State{}
		g.Tasks[r.Name] = map[string][]string{}
		g.Elsewhere[r.Name] = map[string][]string{}

		for _, t := range byVerb[r.Name] {
			ns := NamespaceOf(t.Name)
			columns[ns] = true
			g.Tasks[r.Name][ns] = append(g.Tasks[r.Name][ns], t.Name)

			switch {
			case got[ns]:
				g.Cells[r.Name][ns] = Covered
			// Already settled as covered by one of this namespace's other tasks; a second
			// task must not downgrade it.
			case g.Cells[r.Name][ns] == Covered:
			case exemptedBy(exemptions, t.Name):
				// Only if nothing else in this namespace is still asking to be reported.
				if g.Cells[r.Name][ns] == Absent {
					g.Cells[r.Name][ns] = Exempt
				}
			default:
				g.Cells[r.Name][ns] = Gap
			}
		}

		// Whether somewhere else reaches it, which softens a gap into a note.
		for ns, names := range g.Tasks[r.Name] {
			if g.Cells[r.Name][ns] != Gap {
				continue
			}
			for _, other := range g.Rows {
				if other.Name == r.Name {
					continue
				}
				if slices.ContainsFunc(names, func(n string) bool { return reached[other.Name][n] }) {
					g.Elsewhere[r.Name][ns] = append(g.Elsewhere[r.Name][ns], other.Name)
				}
			}
			if len(g.Elsewhere[r.Name][ns]) > 0 {
				slices.Sort(g.Elsewhere[r.Name][ns])
				g.Cells[r.Name][ns] = CoveredElsewhere
			}
		}
	}

	g.Columns = slices.Sorted(maps.Keys(columns))
	return g
}

// Findings is the grid with everything that is working left out.
func (g Grid) Findings() []Finding {
	var out []Finding
	for _, r := range g.Rows {
		// Sorted, so two runs report the same thing in the same order.
		for _, ns := range g.Columns {
			s := g.Cells[r.Name][ns]
			if s != Gap && s != CoveredElsewhere {
				continue
			}
			out = append(out, Finding{
				Aggregate: r.Name,
				Desc:      r.Desc,
				Namespace: ns,
				Tasks:     g.Tasks[r.Name][ns],
				Elsewhere: g.Elsewhere[r.Name][ns],
			})
		}
	}
	return out
}

// Reaches is the grid read the other way up: for each namespace, the aggregates that run it.
//
// The linter's projection asks "what does this aggregate miss"; the picker's asks "what runs
// this namespace", which is the same cells transposed. Aggregates come back in the order the
// Taskfile declares them, because that is the order the rows are in and a list that
// reshuffled between rebuilds would be one you had to re-read every time.
func (g Grid) Reaches() map[string][]string {
	out := map[string][]string{}
	for _, r := range g.Rows {
		for _, ns := range g.Columns {
			if g.Cells[r.Name][ns] == Covered {
				out[ns] = append(out[ns], r.Name)
			}
		}
	}
	return out
}

// exemptedBy reports whether any glob covers this task name.
func exemptedBy(exempt []string, name string) bool {
	return slices.ContainsFunc(exempt, func(p string) bool { return task.GlobMatch(p, name) })
}
