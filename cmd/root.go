/*
Copyright © 2026 Dmitry Romanidis
Licensed under the MIT licence. See LICENSE.
*/

// Package cmd is taskui's command line: everything that can happen before, instead of, or
// alongside the TUI.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ddromanidis/taskui/internal/app"
	"github.com/ddromanidis/taskui/internal/diff"
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
	timeline   string
	diffTask   string
	flaky      bool
	searchTask string
	since      string
	keys       string
	themeName  string
	listThemes bool
	dumpTheme  string
	colour     bool
	phase      int
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
  taskui --search FAIL --task test --since 2d
  taskui --run all              run headlessly and print the captured tree
  taskui --timeline test        how ` + "`test`" + ` has been going, run after run
  taskui --diff test            what changed since ` + "`test`" + ` last passed
  taskui --flaky                tasks that went both ways at one commit
  taskui --dump-config          print every colour at its default`,
	Args:          cobra.MaximumNArgs(1),
	Version:       versionString(),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          rootRun,
}

// versionString prefers what the linker stamped, and falls back to what the build itself
// knows. A `go install github.com/ddromanidis/taskui@latest` gets no ldflags, so without
// this every such binary calls itself "dev" — including the ones built from a tag, which
// is the one case where the version is not in doubt.
func versionString() string {
	v, c, d := version, commit, date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = strings.TrimPrefix(info.Main.Version, "v")
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "none" && s.Value != "":
				c = s.Value
				if len(c) > 7 {
					c = c[:7]
				}
			case s.Key == "vcs.time" && d == "unknown" && s.Value != "":
				d = s.Value
			}
		}
	}
	return fmt.Sprintf("%s (commit %s, built %s)", v, c, d)
}

func init() {
	cobra.OnInitialize(initConfig)

	f := rootCmd.Flags()
	f.BoolVar(&opts.list, "list", false, "print the tasks and exit — useful for checking discovery without a terminal")
	f.StringVar(&opts.dump, "dump", "", "print a pivot fully expanded and exit: domain|verb|file|…")
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
	f.StringVar(&opts.timeline, "timeline", "", "print how one task has gone, run after run")
	f.StringVar(&opts.diffTask, "diff", "", "print what changed in one task since it last passed")
	f.BoolVar(&opts.flaky, "flaky", false, "print tasks that both passed and failed at one commit")
	f.StringVar(&opts.searchTask, "task", "", "narrow --search to one task's output")
	f.StringVar(&opts.since, "since", "", "narrow --search to runs newer than this: 90m, 2d, 3w")
	f.StringVar(
		&opts.keys,
		"keys",
		"",
		"keys to play before a --screenshot, as if typed: \\t is ⇥, \\n is ⏎, everything else is itself",
	)
	f.StringVar(&opts.themeName, "theme", "", "look to use — see --list-themes")
	f.BoolVar(
		&opts.colour,
		"colour",
		false,
		"keep the colour in a --screenshot, for looking at a theme rather than diffing it",
	)
	f.BoolVar(&opts.colour, "color", false, "alias for --colour")
	f.IntVar(&opts.phase, "phase", 0, "which animation frame a --screenshot is taken at, for themes that move")
	f.BoolVar(&opts.listThemes, "list-themes", false, "list every theme, built-in and yours, and exit")
	f.StringVar(
		&opts.dumpTheme,
		"dump-theme",
		"",
		"print a theme fully resolved, ready to edit into your own, and exit",
	)

	rootCmd.SetVersionTemplate("taskui {{.Version}}\n")

	// Last, because it needs every flag above to exist.
	registerCompletions()
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

	if opts.listThemes {
		return listThemes(cmd)
	}

	if opts.dumpTheme != "" {
		text, problems := theme.DumpTheme(opts.dumpTheme)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "taskui:", p)
		}
		if len(problems) > 0 {
			return errors.New("that theme did not load cleanly")
		}
		fmt.Print(text)
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
	// The flag beats the config file, so a look can be tried without committing to it.
	if opts.themeName != "" {
		picked, problems := theme.LoadTheme(opts.themeName)
		config.Theme = picked
		config.Problems = append(config.Problems, problems...)
	}

	// Searching the archive reads stored runs, not the project — it must work from
	// anywhere, including a directory with no Taskfile in it.
	if opts.searchFor != "" {
		scope := search.Scope{Task: opts.searchTask}
		if opts.since != "" {
			ago, err := parseSince(opts.since)
			if err != nil {
				return err
			}
			scope.Since = time.Now().Add(-ago)
		}
		return searchStored(opts.searchFor, scope)
	}

	// Like --search, these read the archive rather than the project — but unlike it they
	// are scoped to one project, so they need the root and not a Taskfile.
	if opts.timeline != "" {
		return printTimeline(cmd.OutOrStdout(), root, opts.timeline)
	}
	if opts.diffTask != "" {
		return printDiff(cmd.OutOrStdout(), root, opts.diffTask)
	}
	if opts.flaky {
		return printFlaky(cmd.OutOrStdout(), root)
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
		return dumpPivot(opts.dump, app.New(tasks, root).WithConfig(config).Pivots, tasks)
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
		a.StartEnrichment()
		if err := a.StartRun(opts.runTask); err != nil {
			return err
		}
		a.AwaitDetails(detailGrace)
		drive(a, opts.keys)
		return screenshot(a, opts.screenshot, "")
	}

	a := app.New(tasks, root).WithConfig(config)

	if opts.last && !a.OpenLastRun() {
		return fmt.Errorf("no stored runs for this project yet")
	}

	if opts.screenshot != "" {
		a.StartEnrichment()
		a.AwaitDetails(detailGrace)
		return screenshot(a, opts.screenshot, opts.keys)
	}

	// Where each task is written, and whether it is up to date. Started here rather than in
	// New because it shells out, and only the interactive path has a use for the answer.
	a.StartEnrichment()

	program := tea.NewProgram(a, tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// listThemes names what is available, because "yours shadows the built-in of the same
// name" is only a useful rule if you can watch it happen.
func listThemes(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	for _, name := range theme.ListThemes() {
		resolved, problems := theme.LoadTheme(name)
		note := ""
		if len(problems) > 0 {
			note = "  (" + problems[0] + ")"
		}
		fmt.Fprintf(out, "%-12s %s%s\n", name, resolved.Glyphs.Wordmark, note)
	}
	fmt.Fprintf(out, "\nyours go in %s\n", theme.ThemesDir())
	fmt.Fprintf(out, "start one with `taskui --dump-theme default > %s/mine.yaml`\n", theme.ThemesDir())
	return nil
}

// searchStored greps every stored run, newest first, grouped by run and task.
func searchStored(pattern string, scope search.Scope) error {
	base := store.StateDir()
	query, err := search.NewQuery(pattern)
	if err != nil {
		return err
	}
	results, dropped := search.InStoreScoped(base, query, 50, scope)

	if len(results) == 0 {
		where := ""
		if scope.Task != "" {
			where += " in `" + scope.Task + "`"
		}
		if !scope.Since.IsZero() {
			where += " since " + scope.Since.Format("2006-01-02 15:04")
		}
		fmt.Printf("no matches for /%s/%s in %d stored runs\n", pattern, where, len(store.List(base)))
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

func dumpPivot(mode string, pivots []pivot.Pivot, tasks []task.Task) error {
	var chosen pivot.Pivot
	for _, p := range pivots {
		if p.Name == mode {
			chosen = p
		}
	}
	if chosen.Name == "" {
		return fmt.Errorf("--dump expects one of %s, not %q",
			strings.Join(pivotNamesFor(pivots), ", "), mode)
	}

	all := make([]int, len(tasks))
	for i := range tasks {
		all[i] = i
	}
	tree := pivot.Build(chosen, tasks, all)
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

// printTimeline is `--timeline`: one task's stored runs, newest first.
//
// Tab-separated and one run per line, like `--list`, so the answer to "when did this start
// failing" is available to a script and not only to a pair of eyes.
func printTimeline(out io.Writer, root, taskName string) error {
	points := store.Timeline(store.StateDir(), root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	for _, p := range points {
		status := "ok"
		if !p.Ok() {
			status = "failed"
		}
		fmt.Fprintf(out, "%s\t%s\t%dms\t%d lines\t%s\n",
			time.Unix(p.WhenUnix, 0).Format(time.RFC3339), status, p.DurationMs, p.Lines, p.Command())
	}
	fmt.Fprintf(out, "-- %d runs\n", len(points))
	return nil
}

// printDiff is `--diff`: what changed in one task between its last run and the last one
// that passed.
//
// Unified-ish rather than exactly `diff -u`: there are no `@@` hunk headers, because the
// line numbers are on every row instead and a header that has to be cross-referenced with
// the rows below it is a worse answer than the rows carrying it themselves.
func printDiff(out io.Writer, root, taskName string) error {
	base := store.StateDir()
	points := store.Timeline(base, root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	newest := points[0]
	older, ok := store.LastGreen(base, root, taskName, newest.RunID)
	against := "when it last passed"
	if !ok {
		older, ok = store.Previous(base, root, taskName, newest.RunID)
		against = "the run before"
		if !ok {
			return fmt.Errorf("only one stored run of %q — nothing to compare it against", taskName)
		}
	}

	edits := diff.Lines(store.Output(base, older), store.Output(base, newest))
	stat := diff.Count(edits)
	fmt.Fprintf(out, "--- %s  (%s, %s)\n", taskName, against, ago(older.WhenUnix))
	fmt.Fprintf(out, "+++ %s  (%s)\n", taskName, ago(newest.WhenUnix))
	for _, e := range diff.Hunks(edits, 3) {
		switch {
		case diff.IsGap(e):
			fmt.Fprintln(out, "...")
		case e.Op == diff.Ins:
			fmt.Fprintln(out, "+"+e.Text)
		case e.Op == diff.Del:
			fmt.Fprintln(out, "-"+e.Text)
		default:
			fmt.Fprintln(out, " "+e.Text)
		}
	}
	fmt.Fprintf(out, "-- +%d -%d\n", stat.Added, stat.Removed)
	return nil
}

// printFlaky is `--flaky`: the tasks whose result did not depend on the code.
//
// Exits non-zero when it finds any, so it composes into a script the way a check should —
// `taskui --flaky || echo "look into it"`.
func printFlaky(out io.Writer, root string) error {
	flakes := store.Flaky(store.StateDir(), root)
	if len(flakes) == 0 {
		fmt.Fprintln(out, "-- no task has gone both ways at one commit")
		return nil
	}
	for _, f := range flakes {
		fmt.Fprintf(out, "%s\t%s\t%d passed\t%d failed\t%s\n",
			f.Task, f.Short(), f.Passed, f.Failed, ago(f.LastUnix))
	}
	fmt.Fprintf(out, "-- %d flaky\n", len(flakes))
	return exitBecause(ExitFound, "%d %s went both ways at one commit",
		len(flakes), plural(len(flakes), "task", "tasks"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// parseSince reads a span. Go's own durations plus the units people actually use for an
// archive: `2d` and `3w` are the natural way to say how far back to look, and
// [time.ParseDuration] stops at hours.
func parseSince(text string) (time.Duration, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, nil
	}
	unit := trimmed[len(trimmed)-1]
	scale := time.Duration(0)
	switch unit {
	case 'd':
		scale = 24 * time.Hour
	case 'w':
		scale = 7 * 24 * time.Hour
	}
	if scale > 0 {
		n, err := strconv.Atoi(strings.TrimSpace(trimmed[:len(trimmed)-1]))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--since %q: expected a number before %q", text, string(unit))
		}
		return time.Duration(n) * scale, nil
	}

	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("--since %q: try 90m, 2d or 3w", text)
	}
	if d < 0 {
		return 0, fmt.Errorf("--since %q: cannot look forwards", text)
	}
	return d, nil
}

// ago is a rough "how long back", for the headless output. The TUI has its own; this one
// only has to be readable in a pipe.
func ago(unix int64) string {
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
		if exit != 0 {
			return exitWith(exit)
		}
		return nil
	}
	fmt.Printf("saved to %s  (%d secrets masked)\n", path, r.RedactedSecrets)
	// `--run` means run this and be it. The tree above already ends in the exit line, so
	// there is nothing left to say — only a status to carry.
	if exit != 0 {
		return exitWith(exit)
	}
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
		a.RefreshLive()
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
	a.RefreshLive()
}

// detailGrace is how long a one-frame render waits for the JSON listing. Generous, because
// the alternative is a frame that differs run to run depending on how the race went — which
// is the opposite of what `--screenshot` is for.
const detailGrace = 5 * time.Second

func screenshot(a *app.App, size, feed string) error {
	w, h, err := parseSize(size)
	if err != nil {
		return err
	}
	for _, c := range feed {
		a.HandleKey(app.KeyFor(c))
	}
	a.Phase = opts.phase
	if opts.colour {
		// Nothing here is a terminal, so lipgloss would otherwise decide there is no point
		// colouring anything. Saying otherwise is the whole request.
		lipgloss.SetColorProfile(termenv.TrueColor)
		fmt.Println(a.RenderFrame(w, h))
		return nil
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
