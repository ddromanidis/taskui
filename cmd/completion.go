package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/ddromanidis/taskui/internal/app"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/theme"
)

// Completion for the values, not just the names.
//
// cobra gives `taskui completion zsh` away free, and what it gives away completes flag
// *names* and stops — which is the half you did not need, since you can see the flags in
// `--help`. What you cannot see is the hundred task names in somebody else's Taskfile, and
// that is precisely what this program already knows how to discover.

// The flag names that more than one file has to agree on: what completes a task name, and
// what the man page calls its placeholder. Named because the two would otherwise be two
// spellings of the same decision, and only one of them would get updated.
const (
	flagRun      = "run"
	flagGraph    = "graph"
	flagTimeline = "timeline"
	flagDiff     = "diff"
	flagDump     = "dump"
)

// registerCompletions is called from root.go's init rather than being one of its own.
//
// Go runs a package's init functions in file-name order, so this file's would run before
// root.go had registered any flags — and RegisterFlagCompletionFunc against a flag that does
// not exist yet is an error, which is how this was found.
func registerCompletions() {
	for _, name := range []string{flagRun, flagGraph, flagTimeline, flagDiff} {
		mustComplete(name, completeTasks)
	}
	mustComplete(flagDump, completePivots)
	mustComplete("theme", completeThemes)
	mustComplete("dump-theme", completeThemes)

	// The positional argument is a project directory.
	rootCmd.ValidArgsFunction = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
}

// mustComplete wires a completion, and panics if the flag is not there.
//
// A completion registered against a misspelled flag fails silently and for good: nothing
// breaks, the shell simply offers nothing, and you would find out months later. This is
// package initialisation of a literal name, so a panic here is a compile error that happens
// slightly late rather than a runtime hazard.
func mustComplete(flag string, fn func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)) {
	if err := rootCmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		panic("completion for --" + flag + ": " + err.Error())
	}
}

// completeTasks offers the tasks of whichever project is being talked about.
//
// The directory comes from the argument if one has been typed and the working directory
// otherwise, which matches what the command itself would do. Discovery shells out to
// go-task, so this costs what `--list` costs — under a tenth of a second, and a shell that
// is waiting for you to press tab can afford it.
func completeTasks(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	dir := "."
	if len(args) > 0 && args[0] != "" {
		dir = args[0]
	}
	tasks, err := task.Discover(dir)
	if err != nil {
		// No Taskfile here, or no go-task. Falling back to filenames would offer a list of
		// things that are certainly not tasks, so offer nothing.
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if !strings.HasPrefix(t.Name, prefix) {
			continue
		}
		// zsh and fish show the text after the tab; bash ignores it. A description is the
		// difference between choosing from a hundred names and reading a hundred names.
		if t.Desc != "" {
			out = append(out, t.Name+"\t"+t.Desc)
		} else {
			out = append(out, t.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completePivots offers the groupings, including any the config added.
func completePivots(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	dir := "."
	if len(args) > 0 && args[0] != "" {
		dir = args[0]
	}
	// Through the app so that a `pivots:` block in the config is offered too — the built-ins
	// alone would be a list that quietly disagreed with what `--dump` accepts.
	a := app.New(nil, dir).WithConfig(theme.FromViper(v))
	out := make([]string, 0, len(a.Pivots))
	for _, p := range a.Pivots {
		if strings.HasPrefix(p.Name, prefix) {
			out = append(out, p.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeThemes(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, name := range theme.ListThemes() {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeExamples offers the topics, with their titles.
func completeExamples(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, ex := range examples {
		if strings.HasPrefix(ex.name, prefix) {
			out = append(out, ex.name+"\t"+ex.title)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// pivotNamesFor is what `--dump`'s error message lists, kept next to the completion so the
// two cannot come to disagree about what is on offer.
func pivotNamesFor(pivots []pivot.Pivot) []string {
	out := make([]string, 0, len(pivots))
	for _, p := range pivots {
		out = append(out, p.Name)
	}
	return out
}
