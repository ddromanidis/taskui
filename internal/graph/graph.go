// Package graph reconstructs the execution graph: which tasks a task invokes.
//
// `task --list-all --json` does not carry dependencies, and parsing the Taskfiles
// ourselves would mean reimplementing `includes:` resolution and then tracking go-task's
// semantics forever. `task --summary <name>` already reports a task's direct edges using
// go-task's own resolver, so recursing that from whatever was invoked reconstructs the
// graph for the cost of a few process spawns.
//
// Note that `--summary` also prints the resolved environment, which for a Taskfile with
// `dotenv:` means real credentials. That output is parsed in memory and dropped; it must
// never be persisted or displayed.
package graph

import (
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Graph maps a task name to the tasks it invokes, in the order it invokes them.
type Graph struct {
	Edges map[string][]string
}

func New() Graph {
	return Graph{Edges: map[string][]string{}}
}

func (g Graph) Children(task string) []string {
	return g.Edges[task]
}

// Names lists every task with an entry, sorted — the deterministic stand-in for Rust's
// ordered map, so anything that iterates the graph renders the same way twice.
func (g Graph) Names() []string {
	out := make([]string, 0, len(g.Edges))
	for k := range g.Edges {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Reachable lists every task reachable from root, including root, depth first.
func (g Graph) Reachable(root string) []string {
	var out []string
	seen := map[string]bool{}
	stack := []string{root}
	for len(stack) > 0 {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[t] {
			continue
		}
		seen[t] = true
		children := g.Children(t)
		for _, c := range slices.Backward(children) {
			stack = append(stack, c)
		}
		out = append(out, t)
	}
	return out
}

// varsHeading is the sub-heading `requires:` nests its names under; it is also a
// top-level section of its own, which is exactly why both parsers have to step over it
// rather than treat it as a boundary.
const varsHeading = "vars:"

type section int

const (
	sectionNone section = iota
	sectionDeps
	sectionCmds
)

// parseSummary returns a task's direct edges, dependencies first (go-task runs those
// before the commands).
func parseSummary(text string) []string {
	at := sectionNone
	deps := []string{}
	var cmds []string

	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		switch strings.TrimSpace(trimmed) {
		case "dependencies:":
			at = sectionDeps
			continue
		case "commands:":
			at = sectionCmds
			continue
		}

		// Items are ` - <thing>`. Anything else — the env dump, the description, blank
		// lines — ends the section we were in.
		item, ok := strings.CutPrefix(trimmed, " - ")
		if !ok {
			if trimmed != "" {
				at = sectionNone
			}
			continue
		}

		switch at {
		case sectionNone:
			// An item outside either section is not an edge.
		case sectionDeps:
			deps = append(deps, strings.TrimSpace(item))
		case sectionCmds:
			// Under `commands:` only `Task: x` entries are edges; the rest are shell.
			if name, ok := strings.CutPrefix(strings.TrimSpace(item), "Task: "); ok {
				cmds = append(cmds, strings.TrimSpace(name))
			}
		}
	}

	return append(deps, cmds...)
}

// RequiredVars lists the variables a task declares with `requires: { vars: [NAME] }`.
//
// This is a fact rather than a guess mined from prose, so the args prompt can pre-fill
// `NAME=` and know it is asking for something real. One `--summary` call, ~40ms, made only
// when the prompt opens.
func RequiredVars(dir, task string) []string {
	return parseRequires(summaryOf(dir, task))
}

func parseRequires(text string) []string {
	var out []string
	inside := false
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "requires:" {
			inside = true
			continue
		}
		if !inside {
			continue
		}
		if name, ok := strings.CutPrefix(trimmed, "- "); ok {
			out = append(out, strings.TrimSpace(name))
			continue
		}
		// The block nests — the names sit under a `vars:` sub-heading — so that one line
		// is stepped over rather than treated as the end of the section.
		if trimmed == varsHeading || trimmed == "" {
			continue
		}
		break
	}
	return out
}

// Detail is what a task actually is: what it says it does, what it needs, and what it will
// run.
type Detail struct {
	Summary      []string
	Requires     []string
	Dependencies []string
	Commands     []string
}

type detailSection int

const (
	detailDescription detailSection = iota
	detailSecrets
	detailRequires
	detailDependencies
	detailCommands
)

// parseDetail turns `task --summary` into something showable.
//
// The `vars:` and `env:` blocks are dropped on the floor. That is not tidiness: `env:` is
// the resolved environment, so for a Taskfile with `dotenv:` it contains live credentials,
// and this output goes on screen.
func parseDetail(text string) Detail {
	var d Detail
	at := detailDescription

	lines := strings.Split(text, "\n")
	// The first line is `task: <name>`, which the UI already knows.
	if len(lines) > 0 {
		lines = lines[1:]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Inside `requires:` the names nest under their own `vars:` sub-heading, which
		// must not be mistaken for the top-level one.
		if at == detailRequires && trimmed == varsHeading {
			continue
		}
		switch trimmed {
		case varsHeading, "env:":
			at = detailSecrets
			continue
		case "requires:":
			at = detailRequires
			continue
		case "dependencies:":
			at = detailDependencies
			continue
		case "commands:":
			at = detailCommands
			continue
		}

		switch at {
		case detailSecrets:
			// Dropped on the floor, deliberately.
		case detailDescription:
			// go-task's stand-in for "no description" is noise here.
			if trimmed != "" && !strings.HasPrefix(trimmed, "(task does not have") {
				d.Summary = append(d.Summary, trimmed)
			}
		case detailRequires:
			if name, ok := strings.CutPrefix(trimmed, "- "); ok {
				d.Requires = append(d.Requires, strings.TrimSpace(name))
			}
		case detailDependencies:
			if name, ok := strings.CutPrefix(trimmed, "- "); ok {
				d.Dependencies = append(d.Dependencies, strings.TrimSpace(name))
			}
		case detailCommands:
			// Commands keep their shape: a multi-line shell block is one command, and
			// reflowing it would misrepresent what runs.
			if right := strings.TrimRight(line, " \t\r"); right != "" {
				d.Commands = append(d.Commands, strings.TrimPrefix(right, " - "))
			}
		}
	}
	return d
}

// DescribeTask makes one `--summary` call, when the detail panel opens.
func DescribeTask(dir, task string) Detail {
	return parseDetail(summaryOf(dir, task))
}

func summaryOf(dir, task string) string {
	cmd := exec.Command("task", "--summary", task)
	cmd.Dir = dir
	// A task that cannot be summarised is not fatal — it just has no known children.
	out, _ := cmd.Output()
	return string(out)
}

// Resolve walks outward from root, one `--summary` per distinct task.
func Resolve(dir, root string) Graph {
	g, _ := ResolveDetailed(dir, root)
	return g
}

// ResolveDetailed is Resolve, but also hands back the root task's raw `--summary` text.
//
// That text contains the resolved environment — which is exactly what the redactor needs
// in order to know what to mask. It is returned rather than stored so the caller is forced
// to decide what happens to it; it must not be persisted or displayed.
func ResolveDetailed(dir, root string) (Graph, string) {
	// The root's own summary is wanted verbatim, so it is fetched here; everything below
	// it is resolved concurrently.
	rootSummary := summaryOf(dir, root)
	return resolveParallel(root, dir), rootSummary
}

// lanes is enough to hide the latency without spawning a process per task in a wide graph.
const lanes = 8

// resolveParallel resolves one frontier at a time, fetching each frontier's tasks
// concurrently.
//
// One `--summary` is a process spawn — around 40ms — and a big aggregate has dozens of
// them. Serially that was over a second of dead time before an aggregate produced a single
// line. The graph is a level-order walk anyway, so each level's calls are independent.
//
// Tasks are memoised and revisits short-circuit, so a diamond (`all` reaching `lint` and
// `check`, both reaching `backend:*`) costs one call per node, and a cycle terminates
// instead of spinning.
func resolveParallel(root, dir string) Graph {
	g := New()
	frontier := []string{root}

	for len(frontier) > 0 {
		var next []string

		for start := 0; start < len(frontier); start += lanes {
			end := min(start+lanes, len(frontier))
			batch := frontier[start:end]

			results := make([][]string, len(batch))
			var wg sync.WaitGroup
			for i, task := range batch {
				wg.Go(func() {
					results[i] = parseSummary(summaryOf(dir, task))
				})
			}
			wg.Wait()

			for i, task := range batch {
				for _, c := range results[i] {
					if _, known := g.Edges[c]; !known && !contains(next, c) {
						next = append(next, c)
					}
				}
				g.Edges[task] = results[i]
			}
		}

		// Anything already resolved by an earlier level is not revisited, so a diamond
		// costs one call and a cycle terminates.
		frontier = frontier[:0]
		for _, t := range next {
			if _, known := g.Edges[t]; !known {
				frontier = append(frontier, t)
			}
		}
	}

	return g
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
