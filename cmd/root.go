/*
Copyright © 2026 Dmitry Romanidis
Licensed under the MIT licence. See LICENSE.
*/

// Package cmd is taskui's command line: everything that can happen before, instead of, or
// alongside the TUI.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ddromanidis/taskui/internal/app"
	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/search"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/theme"
)

// Stamped by the linker at release time; see the Taskfile and .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type options struct {
	list       bool
	dump       string
	graph      string
	runTask    string
	screenshot string
	args       string
	last       bool
	configPath string
	dumpConfig bool
	searchFor  string
	keys       string
}

var opts options

// v is the Viper the config is read through. Kept package-level so cobra's OnInitialize
// can fill it before Run.
var v = viper.New()

var rootCmd = &cobra.Command{
	Use:   "taskui [directory]",
	Short: "Fold, pivot and search your Taskfile",
	Long: `A folding, searchable front end for go-task.

Browse the tasks in a Taskfile, run them, and keep their output around to fold
and search afterwards — live and across previous runs.`,
	Example: `  taskui                        browse the Taskfile here
  taskui ~/src/myrepo           …or somewhere else
  taskui --search 'FAIL|error'  grep every stored run, from anywhere
  taskui --run all              run headlessly and print the captured tree
  taskui --dump-config          print every colour at its default`,
	Args:          cobra.MaximumNArgs(1),
	Version:       versionString(),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          rootRun,
}

func versionString() string {
	return fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "taskui:", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	f := rootCmd.Flags()
	f.BoolVar(&opts.list, "list", false, "print the tasks and exit — useful for checking discovery without a terminal")
	f.StringVar(&opts.dump, "dump", "", "print a pivot fully expanded and exit: domain|verb")
	f.StringVar(&opts.graph, "graph", "", "print the execution graph reachable from a task and exit")
	f.StringVar(&opts.runTask, "run", "", "run a task headlessly and print the captured tree")
	f.StringVar(&opts.screenshot, "screenshot", "", "render one frame to stdout and exit, e.g. 90x30")
	f.StringVar(&opts.args, "args", "", "arguments for --run, split shell-style: --args '-- -p ingest'")
	f.BoolVar(&opts.last, "last", false, "open the most recent run for this project instead of the picker")
	f.StringVar(&opts.configPath, "config", "", "config file (default is "+theme.ConfigPath()+")")
	f.BoolVar(
		&opts.dumpConfig,
		"dump-config",
		false,
		"print an annotated config.yaml with every colour at its default, and exit",
	)
	f.StringVar(&opts.searchFor, "search", "", "search stored runs and exit")
	f.StringVar(
		&opts.keys,
		"keys",
		"",
		"keys to feed before a --screenshot: g pivots, \\t folds all, / starts a filter, j/k move",
	)

	rootCmd.SetVersionTemplate("taskui {{.Version}}\n")
}

// initConfig points Viper at the config file and binds `TASKUI_*` to the same keys.
func initConfig() {
	if err := theme.Setup(v, opts.configPath); err != nil {
		fmt.Fprintf(os.Stderr, "taskui: %v\n", err)
	}
}

func rootRun(cmd *cobra.Command, args []string) error {
	if opts.dumpConfig {
		fmt.Print(theme.DumpConfig())
		return nil
	}

	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		root = dir
	}

	config := theme.FromViper(v)

	// Searching the archive reads stored runs, not the project — it must work from
	// anywhere, including a directory with no Taskfile in it.
	if opts.searchFor != "" {
		return searchStored(opts.searchFor)
	}

	tasks, err := task.Discover(root)
	if err != nil {
		return err
	}

	if opts.list {
		out := cmd.OutOrStdout()
		for _, t := range tasks {
			// Ignore write errors: piping into `head` closes the pipe on us, and that is
			// not a failure of ours.
			if _, err := fmt.Fprintf(out, "%s\t%s\n", t.Name, t.Desc); err != nil {
				return nil //nolint:nilerr // piping into `head` closes the pipe on us
			}
		}
		fmt.Fprintf(out, "-- %d tasks\n", len(tasks))
		return nil
	}

	if opts.dump != "" {
		return dumpPivot(opts.dump, tasks)
	}

	if opts.graph != "" {
		return printGraph(root, opts.graph)
	}

	if opts.runTask != "" {
		// `--run` alone prints the captured tree; paired with `--screenshot` it renders
		// the actual run view, which is how the TUI gets verified without a terminal.
		if opts.screenshot == "" {
			return runHeadless(root, opts.runTask, task.SplitArgs(opts.args))
		}
		a := app.New(tasks, root).WithConfig(config)
		if err := a.StartRun(opts.runTask); err != nil {
			return err
		}
		drive(a, opts.keys)
		return screenshot(a, opts.screenshot, "")
	}

	a := app.New(tasks, root).WithConfig(config)

	if opts.last && !a.OpenLastRun() {
		return fmt.Errorf("no stored runs for this project yet")
	}

	if opts.screenshot != "" {
		return screenshot(a, opts.screenshot, opts.keys)
	}

	program := tea.NewProgram(a, tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// searchStored greps every stored run, newest first, grouped by run and task.
func searchStored(pattern string) error {
	base := store.StateDir()
	query, err := search.NewQuery(pattern)
	if err != nil {
		return err
	}
	results, dropped := search.InStore(base, query, 50)

	if len(results) == 0 {
		fmt.Printf("no matches for /%s/ in %d stored runs\n", pattern, len(store.List(base)))
		return nil
	}

	total := 0
	for _, r := range results {
		mark := "✓"
		if r.Manifest.Failed() {
			mark = "✗"
		}
		fmt.Printf("\n%s %s  task %s  (%d hits)\n", mark, r.Manifest.ID, r.Manifest.Root, len(r.Hits))
		current := ""
		for _, hit := range r.Hits {
			if hit.Task != current {
				fmt.Printf("  %s\n", hit.Task)
				current = hit.Task
			}
			fmt.Printf("    %5d  %s\n", hit.LineNo, hit.Text)
			total++
		}
	}

	fmt.Printf("\n%d hits in %d runs\n", total, len(results))
	if dropped > 0 {
		// Never let a capped result read as a complete one.
		fmt.Printf("%d further hits not shown (per-run cap)\n", dropped)
	}
	return nil
}

func dumpPivot(mode string, tasks []task.Task) error {
	m := pivot.Domain
	if mode == "verb" {
		m = pivot.Verb
	} else if mode != "domain" {
		return fmt.Errorf("--dump expects domain or verb, not %q", mode)
	}

	all := make([]int, len(tasks))
	for i := range tasks {
		all[i] = i
	}
	tree := pivot.Build(m, tasks, all)
	for _, row := range tree.Flatten(func(string) bool { return true }) {
		n := tree.Nodes[row.Node]
		glyph := " "
		// A group row that is also a task is marked, since the tree alone cannot show that
		// `backend:migrate` is runnable as well as foldable.
		runnable := " "
		count := ""
		if n.IsGroup() {
			glyph = "▾"
			count = fmt.Sprintf("  %d", n.Count)
			if n.Task != pivot.NoTask {
				runnable = "*"
			}
		}
		if _, err := fmt.Printf(
			"%s%s%s%s%s\n",
			strings.Repeat("  ", row.Depth),
			glyph,
			runnable,
			n.Label,
			count,
		); err != nil {
			return nil //nolint:nilerr // piping into `head` closes the pipe on us
		}
	}
	return nil
}

func printGraph(root, rootTask string) error {
	g := graph.Resolve(root, rootTask)
	// Print the tree, marking revisits rather than expanding them twice.
	seen := map[string]bool{}
	type frame struct {
		name  string
		depth int
	}
	stack := []frame{{rootTask, 0}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		repeat := seen[top.name]
		seen[top.name] = true
		mark := ""
		if repeat {
			mark = "  (already shown)"
		}
		if _, err := fmt.Printf("%s%s%s\n", strings.Repeat("  ", top.depth), top.name, mark); err != nil {
			return nil //nolint:nilerr // piping into `head` closes the pipe on us
		}
		if !repeat {
			children := g.Children(top.name)
			for _, c := range slices.Backward(children) {
				stack = append(stack, frame{c, top.depth + 1})
			}
		}
	}
	return nil
}

// runHeadless runs a task to completion and prints what the capture layer reconstructed:
// the execution tree, each task's status and duration, and its output indented beneath it.
func runHeadless(dir, target string, argv []string) error {
	r, err := run.Start(dir, target, argv, false, false)
	if err != nil {
		return err
	}
	for !r.Finished() {
		r.Poll()
		time.Sleep(20 * time.Millisecond)
	}
	r.Poll()

	type frame struct {
		name  string
		depth int
	}
	stack := []frame{{target, 0}}
	seen := map[string]bool{}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[top.name] {
			continue
		}
		seen[top.name] = true

		pad := strings.Repeat("  ", top.depth)
		if t, ok := r.Tasks[top.name]; ok {
			secs := ""
			if d, ok := t.Elapsed(); ok {
				secs = fmt.Sprintf("%.2fs", d.Seconds())
			}
			// Ignore write errors throughout: piping into `head` closes the pipe on us.
			fmt.Printf("%s%s %s  %s\n", pad, t.Status.Glyph(), top.name, secs)
			for _, l := range t.Lines {
				marker := "│"
				if l.IsCommand {
					marker = "$"
				}
				// Raw, so colour shows on a terminal and survives `cat -v` inspection.
				fmt.Printf("%s  %s %s\n", pad, marker, l.Raw)
			}
		}
		children := r.Graph.Children(top.name)
		for _, c := range slices.Backward(children) {
			stack = append(stack, frame{c, top.depth + 1})
		}
	}
	exit := -1
	if r.HasExit {
		exit = r.Exit
	}
	fmt.Printf("\nexit %d  in %.2fs\n", exit, r.Duration.Seconds())

	// Store it just as the TUI would: a run is a run whichever way it was started, and
	// `--run` output you cannot search later would be a trap.
	path, err := store.Save(store.StateDir(), dir, r)
	if err != nil {
		fmt.Printf("not saved: %v\n", err)
		return nil
	}
	fmt.Printf("saved to %s  (%d secrets masked)\n", path, r.RedactedSecrets)
	return nil
}

// drive plays keys into a live run, then lets it finish.
//
// The plain screenshot path applies keys to a finished run, which cannot exercise anything
// interactive — an interactive run never finishes on its own, so waiting first is a
// deadlock. Keys are paced so the child has time to reach its prompt between them.
func drive(a *app.App, feed string) {
	deadline := time.Now().Add(30 * time.Second)
	pending := []rune(feed)
	nextKey := time.Now().Add(400 * time.Millisecond)

	for {
		a.PollRun()
		if time.Now().After(deadline) {
			break
		}
		if time.Now().After(nextKey) || time.Now().Equal(nextKey) {
			if len(pending) > 0 {
				a.HandleKey(app.KeyFor(pending[0]))
				pending = pending[1:]
				nextKey = time.Now().Add(400 * time.Millisecond)
			} else if a.Run != nil && a.Run.Finished() {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.PollRun()
}

func screenshot(a *app.App, size, feed string) error {
	w, h, err := parseSize(size)
	if err != nil {
		return err
	}
	for _, c := range feed {
		a.HandleKey(app.KeyFor(c))
	}
	for _, l := range a.RenderHeadless(w, h) {
		if _, err := fmt.Println(l); err != nil {
			return nil //nolint:nilerr // piping into `head` closes the pipe on us
		}
	}
	return nil
}

func parseSize(size string) (int, int, error) {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--screenshot expects WxH, e.g. 90x30")
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}
