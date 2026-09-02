package cmd

// `--lint` and `--matrix` are the two ways of printing what `internal/cover` worked out:
// the disagreements, or the whole table they came out of. The check itself lives there
// because the picker asks the same question — see that package's doc.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ddromanidis/taskui/internal/cover"
	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/task"
)

// coverPatterns reads `.taskui-cover`, or nothing if there is not one.
func coverPatterns(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, cover.File))
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
	g := cover.BuildGrid(tasks, reach, coverPatterns(root))
	if matrix {
		return printMatrix(out, g)
	}
	found := g.Findings()

	gaps := 0
	last := ""
	for _, f := range found {
		if !f.Note() {
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
		if f.Note() {
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
		fmt.Fprintf(out, "\nA gap that is deliberate belongs in %s, one glob per line.\n", cover.File)
	}
	return gaps
}

// marks are what a cell says, in the order the legend lists them.
var marks = map[cover.State]string{
	cover.Absent:           "—",
	cover.Covered:          "✓",
	cover.CoveredElsewhere: "·",
	cover.Exempt:           "~",
	cover.Gap:              "✗",
}

// printMatrix prints the whole grid rather than only the disagreements.
//
// This table gets written by hand. xerum's root Taskfile carries one as a comment — verbs
// down the side, namespaces across the top, a tick where the aggregate covers the domain —
// and it had already drifted from the Taskfile under it by the time this was written. The
// table is derived from two things taskui reads anyway, so deriving it is strictly better
// than keeping it in step.
func printMatrix(out io.Writer, g cover.Grid) int {
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
			if s == cover.Gap {
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
		marks[cover.Covered], marks[cover.CoveredElsewhere], marks[cover.Gap],
		marks[cover.Exempt], marks[cover.Absent])
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
