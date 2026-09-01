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
	var roots []task.Task
	for _, t := range tasks {
		if namespaceOf(t.Name) == "" && len(byVerb[t.Name]) > 0 {
			roots = append(roots, t)
		}
	}

	// Resolved once each and kept, because Elsewhere asks every aggregate about every task
	// and re-walking a graph per question would be dozens of process spawns for an answer
	// already on hand.
	reached := map[string]map[string]bool{}
	for _, r := range roots {
		set := map[string]bool{}
		for _, name := range reach(r.Name) {
			set[name] = true
		}
		reached[r.Name] = set
	}

	var out []finding
	for _, r := range roots {
		// Which namespaces this aggregate got to at all, by any route and at any depth.
		covered := map[string]bool{}
		for name := range reached[r.Name] {
			covered[namespaceOf(name)] = true
		}

		// Grouped by namespace so a namespace answering a verb twice is one finding rather
		// than two, and ordered by name so two runs report the same thing twice.
		seen := map[string]bool{}
		var order []string
		claims := map[string][]string{}
		for _, t := range byVerb[r.Name] {
			ns := namespaceOf(t.Name)
			if covered[ns] || exemptedBy(exempt, t.Name) {
				continue
			}
			if !seen[ns] {
				seen[ns] = true
				order = append(order, ns)
			}
			claims[ns] = append(claims[ns], t.Name)
		}
		slices.Sort(order)

		for _, ns := range order {
			f := finding{Aggregate: r.Name, Desc: r.Desc, Namespace: ns, Tasks: claims[ns]}
			for _, other := range roots {
				if other.Name == r.Name {
					continue
				}
				if slices.ContainsFunc(f.Tasks, func(n string) bool { return reached[other.Name][n] }) {
					f.Elsewhere = append(f.Elsewhere, other.Name)
				}
			}
			slices.Sort(f.Elsewhere)
			out = append(out, f)
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
func printLint(out io.Writer, root string, tasks []task.Task) int {
	reach := func(name string) []string { return graph.Resolve(root, name).Reachable(name) }
	found := coverage(tasks, reach, coverPatterns(root))

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
