package cmd

// `--lint` checks the one claim a Taskfile makes that nothing else can check: that an
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
// them, and this file is the difference plus a way to say "that one is on purpose".
//
// Reachability, not names, is what makes it correct. xerum's `lint` never calls `api:lint`;
// it calls `api:check`, which reaches `api:tenant:lint` two levels down. A check that
// matched names would report that as a gap and be wrong.

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/task"
)

// CoverFile is the opt-in file listing tasks no aggregate is expected to reach.
//
// The same shape as `.taskui-danger`, for the same reason: one glob per line, `#` to the end
// of a line is a comment, and a repository that does not have one loses nothing. Deliberate
// exclusions are the majority of what a first run reports — a deploy that must not fire from
// a local gate, a docs build, an artifact for another platform — and a check whose findings
// are mostly correct-as-written is a check people stop reading.
const CoverFile = ".taskui-cover"

// A finding is one namespace an aggregate claims and does not reach.
//
// Tasks is what that namespace answers the verb with, which is nearly always one name; it is
// a list because nothing stops a namespace from having `api:tenant:check` and
// `api:control:check` and the report should name both rather than pick.
type finding struct {
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

// note reports whether this is the softer kind: covered, just not by the aggregate that
// shares its verb.
func (f finding) note() bool { return len(f.Elsewhere) > 0 }

// namespaceOf is the first segment of a colon path, and "" for a root-level task.
func namespaceOf(name string) string {
	ns, _, found := strings.Cut(name, ":")
	if !found {
		return ""
	}
	return ns
}

// coverage is the check itself, over a list of tasks and a way to ask what a root reaches.
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
func coverage(tasks []task.Task, reach func(string) []string, exempt []string) []finding {
	return buildGrid(tasks, reach, exempt).findings()
}

// state is what one aggregate does about one namespace.
type state int

const (
	// absent: the namespace does not answer this verb at all, which is most of the grid and
	// is not a finding — `site` has no `test` and should not.
	absent state = iota
	covered
	// elsewhere: not reached by the aggregate sharing its verb, but reached by another.
	elsewhere
	exempt
	gap
)

// grid is the whole answer: every aggregate against every namespace that answers any
// aggregate's verb.
//
// Built once and projected twice. `--lint` prints the disagreements, `--matrix` prints the
// table they came out of, and computing them separately would be two chances to disagree
// about what covered means.
type grid struct {
	// Rows in the order the Taskfile declares them, so a matrix reads down the file rather
	// than alphabetically.
	Rows []task.Task
	// Columns sorted, and only namespaces that answer at least one aggregate's verb — a
	// column of nothing but `—` is a column about a namespace nobody asked about.
	Columns []string
	Cells   map[string]map[string]state
	// Tasks is what a namespace answers a verb with, for naming it in a finding.
	Tasks     map[string]map[string][]string
	Elsewhere map[string]map[string][]string
}

func buildGrid(tasks []task.Task, reach func(string) []string, exemptions []string) grid {
	// The verb every namespaced task answers, so an aggregate can ask who claims its name.
	byVerb := map[string][]task.Task{}
	for _, t := range tasks {
		if namespaceOf(t.Name) != "" {
			byVerb[t.Verb()] = append(byVerb[t.Verb()], t)
		}
	}

	// An aggregate is a root-level task some namespace also answers. `wt:new` has no root
	// task and `precommit` has no namespace answering it; neither is making a claim about
	// coverage, so neither is checked.
	g := grid{
		Cells:     map[string]map[string]state{},
		Tasks:     map[string]map[string][]string{},
		Elsewhere: map[string]map[string][]string{},
	}
	for _, t := range tasks {
		if namespaceOf(t.Name) == "" && len(byVerb[t.Name]) > 0 {
			g.Rows = append(g.Rows, t)
		}
	}

	// Resolved once each and kept, because elsewhere asks every aggregate about every task
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
			got[namespaceOf(name)] = true
		}

		g.Cells[r.Name] = map[string]state{}
		g.Tasks[r.Name] = map[string][]string{}
		g.Elsewhere[r.Name] = map[string][]string{}

		for _, t := range byVerb[r.Name] {
			ns := namespaceOf(t.Name)
			columns[ns] = true
			g.Tasks[r.Name][ns] = append(g.Tasks[r.Name][ns], t.Name)

			switch {
			case got[ns]:
				g.Cells[r.Name][ns] = covered
			// Already settled as covered by one of this namespace's other tasks; a second
			// task must not downgrade it.
			case g.Cells[r.Name][ns] == covered:
			case exemptedBy(exemptions, t.Name):
				// Only if nothing else in this namespace is still asking to be reported.
				if g.Cells[r.Name][ns] == absent {
					g.Cells[r.Name][ns] = exempt
				}
			default:
				g.Cells[r.Name][ns] = gap
			}
		}

		// Whether somewhere else reaches it, which softens a gap into a note.
		for ns, names := range g.Tasks[r.Name] {
			if g.Cells[r.Name][ns] != gap {
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
				g.Cells[r.Name][ns] = elsewhere
			}
		}
	}

	g.Columns = slices.Sorted(maps.Keys(columns))
	return g
}

// findings is the grid with everything that is working left out.
func (g grid) findings() []finding {
	var out []finding
	for _, r := range g.Rows {
		// Sorted, so two runs report the same thing in the same order.
		for _, ns := range g.Columns {
			s := g.Cells[r.Name][ns]
			if s != gap && s != elsewhere {
				continue
			}
			out = append(out, finding{
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

// exemptedBy reports whether any glob covers this task name.
func exemptedBy(exempt []string, name string) bool {
	return slices.ContainsFunc(exempt, func(p string) bool { return task.GlobMatch(p, name) })
}

// coverPatterns reads `.taskui-cover`, or nothing if there is not one.
func coverPatterns(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, CoverFile))
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// printLint is `--lint`: walk every aggregate's graph and report what it claims and does not
// reach. Returns the number of gaps, which is what the exit code is decided on — notes are
// printed and not counted.
func printLint(out io.Writer, root string, tasks []task.Task, matrix bool) int {
	reach := func(name string) []string { return graph.Resolve(root, name).Reachable(name) }
	g := buildGrid(tasks, reach, coverPatterns(root))
	if matrix {
		return printMatrix(out, g)
	}
	found := g.findings()

	gaps := 0
	last := ""
	for _, f := range found {
		if !f.note() {
			gaps++
		}
		if f.Aggregate != last {
			if last != "" {
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "%s — %s\n", f.Aggregate, f.Desc)
			last = f.Aggregate
		}
		names := strings.Join(f.Tasks, ", ")
		if f.note() {
			fmt.Fprintf(out, "  · %-28s covered by %s — not by %s\n",
				names, strings.Join(f.Elsewhere, ", "), f.Aggregate)
			continue
		}
		fmt.Fprintf(out, "  ✗ %-28s declared, never reached\n", names)
	}

	if len(found) > 0 {
		fmt.Fprintln(out)
	}
	notes := len(found) - gaps
	fmt.Fprintf(out, "%d %s, %d %s\n",
		gaps, plural(gaps, "gap", "gaps"), notes, plural(notes, "note", "notes"))
	if gaps > 0 {
		fmt.Fprintf(out, "\nA gap that is deliberate belongs in %s, one glob per line.\n", CoverFile)
	}
	return gaps
}

// marks are what a cell says, in the order the legend lists them.
var marks = map[state]string{
	absent:    "—",
	covered:   "✓",
	elsewhere: "·",
	exempt:    "~",
	gap:       "✗",
}

// printMatrix prints the whole grid rather than only the disagreements.
//
// This table gets written by hand. xerum's root Taskfile carries one as a comment — verbs
// down the side, namespaces across the top, a tick where the aggregate covers the domain —
// and it had already drifted from the Taskfile under it by the time this was written. The
// table is derived from two things taskui reads anyway, so deriving it is strictly better
// than keeping it in step.
func printMatrix(out io.Writer, g grid) int {
	if len(g.Rows) == 0 {
		fmt.Fprintln(out, "no aggregate tasks here — nothing claims to cover a namespace")
		return 0
	}

	label := 0
	for _, r := range g.Rows {
		label = max(label, len([]rune(r.Name)))
	}
	width := make([]int, len(g.Columns))
	for i, ns := range g.Columns {
		width[i] = max(len([]rune(ns)), 1)
	}

	fmt.Fprint(out, strings.Repeat(" ", label+2))
	for i, ns := range g.Columns {
		fmt.Fprintf(out, "%s  ", pad(ns, width[i]))
	}
	fmt.Fprintln(out)

	gaps := 0
	for _, r := range g.Rows {
		fmt.Fprintf(out, "%s  ", pad(r.Name, label))
		for i, ns := range g.Columns {
			s := g.Cells[r.Name][ns]
			if s == gap {
				gaps++
			}
			// Centred under the heading, so a wide namespace name does not drag its column
			// of marks off to one side.
			fmt.Fprintf(out, "%s  ", centre(marks[s], width[i]))
		}
		fmt.Fprintln(out)
	}

	fmt.Fprintf(out, "\n%s reached   %s covered by another aggregate   %s never reached   "+
		"%s exempt   %s not declared\n",
		marks[covered], marks[elsewhere], marks[gap], marks[exempt], marks[absent])
	return gaps
}

// pad left-aligns to a width counted in runes, which is what a terminal column is.
func pad(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func centre(s string, width int) string {
	n := width - len([]rune(s))
	if n <= 0 {
		return s
	}
	left := n / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-left)
}
