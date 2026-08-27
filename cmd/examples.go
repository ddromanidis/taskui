package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/ddromanidis/taskui/internal/app"
	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/theme"
)

// The examples command.
//
// Every frame below is rendered by the real renderer, at the width of the terminal it is
// printed into, from a sample project built in this file. Nothing here is a mock-up drawn by
// hand — a hand-drawn one is out of date the first time a column moves, and this is
// documentation whose whole job is to look like what you will see.
func init() {
	examplesCmd.Flags().IntVar(&examplesWidth, "width", 0,
		"render at this width instead of the terminal's")
	// Built from the list rather than written out, so `--help` cannot come to disagree with
	// what the command actually has.
	names := make([]string, 0, len(examples))
	for _, ex := range examples {
		names = append(names, "  "+ex.name+strings.Repeat(" ", max(1, 10-len(ex.name)))+ex.title)
	}
	examplesCmd.Long += "\n\n" + strings.Join(names, "\n")
	rootCmd.AddCommand(examplesCmd)
}

var examplesWidth int

var examplesCmd = &cobra.Command{
	Use:   "examples [topic]",
	Short: "Worked examples, rendered from a sample project",
	Long: `Worked examples of using taskui, in the order you are likely to need them.

Every frame is real output: the sample project below is built in memory and drawn by the
same renderer the live UI uses, at your terminal's width and in your theme.

With no topic, every example is printed. Name one to see just that.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		width := exampleWidth()

		if len(args) == 0 {
			for i, ex := range examples {
				if i > 0 {
					fmt.Fprintln(out)
				}
				ex.write(out, width)
			}
			return nil
		}

		for _, ex := range examples {
			if ex.name == args[0] {
				ex.write(out, width)
				return nil
			}
		}
		var names []string
		for _, ex := range examples {
			names = append(names, ex.name)
		}
		return fmt.Errorf("no example called %q — try one of %s", args[0], strings.Join(names, ", "))
	},
}

// exampleWidth is how wide to draw. The terminal's, so the frames are the shape you will
// actually get; 80 when there is no terminal, which is what a pipe or a file wants.
func exampleWidth() int {
	if examplesWidth > 0 {
		return clampWidth(examplesWidth)
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return clampWidth(w)
	}
	return 80
}

// clampWidth keeps a frame legible. Below the floor the layout starts dropping columns,
// which makes for a poor advertisement; above the ceiling the prose beside it turns into
// one very long line.
func clampWidth(w int) int {
	return max(60, min(w-2, 100))
}

type example struct {
	name  string
	title string
	// parts are prose and frames in the order they should read. A frame is drawn from the
	// sample project at print time.
	parts []part
}

type part struct {
	text  string
	frame func(width int) []string
	// shell is a block of commands, shown as-is.
	shell []string
}

func text(s string) part              { return part{text: s} }
func shell(lines ...string) part      { return part{shell: lines} }
func frame(f func(int) []string) part { return part{frame: f} }

func (e example) write(out io.Writer, width int) {
	fmt.Fprintf(out, "%s\n%s\n", e.title, strings.Repeat("─", min(width, len([]rune(e.title)))))
	// One blank line before every part rather than around the frames only: two paragraphs
	// printed back to back read as one, which is how the first draft of this looked.
	for _, p := range e.parts {
		fmt.Fprintln(out)
		switch {
		case p.frame != nil:
			writeBlock(out, p.frame(width-2))
		case len(p.shell) > 0:
			writeBlock(out, p.shell)
		default:
			fmt.Fprintln(out, wrapText(p.text, width))
		}
	}
}

// writeBlock indents a frame. A row that is empty gets no indent — trailing whitespace on a
// blank line is invisible on screen and noise in a file, and these are meant to be pasteable.
func writeBlock(out io.Writer, lines []string) {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(out)
			continue
		}
		fmt.Fprintf(out, "  %s\n", line)
	}
}

// wrapText breaks prose at word boundaries. The frames beside it are already exactly as wide
// as they should be; the sentences have to be made to match.
func wrapText(s string, width int) string {
	var out []string
	line := ""
	for word := range strings.FieldsSeq(s) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// --- the sample project -----------------------------------------------------------------

// The tasks the examples keep referring to. Named constants because they appear in the
// fixture, in the run, and in the prose, and a demo where the text talks about one task and
// the frame shows another would be worse than no demo.
const (
	sampleAll  = "all"
	sampleFmt  = "fmt"
	sampleLint = "lint"
	sampleTest = "backend:test"
)

// sampleTasks is a Taskfile shaped like a real one: namespaces, cross-cutting verbs, a
// namespace that is also a task, and descriptions long enough to wrap.
var sampleTasks = []struct{ name, desc string }{
	{sampleAll, "Everything: format, lint, test, build"},
	{"build", "Compile the workspace"},
	{"check", "Type-check and vet only — the fastest feedback there is"},
	{sampleFmt, "Format every source file"},
	{sampleLint, "Lint everything, then check the architecture"},
	{"test", "Run the whole suite with the race detector"},
	{"api:gen", "Regenerate the server adapter from the contract"},
	{"api:lint", "Validate the OpenAPI spec"},
	{"backend:build", "Build the tenant binary with the version stamped in"},
	{"backend:lint", "Lint both tag sets, then the architecture"},
	{sampleTest, "Run the backend suite with the race detector"},
	{"backend:migrate", "Apply pending migrations to the local database"},
	{"backend:migrate:down", "Roll back every migration — development only"},
	{"backend:migrate:status", "What has been applied, and what has not"},
	{"deploy:prod", "Ship to production"},
	{"site:build", "Build the static site"},
	{"site:lint", "Check the site's links and markup"},
}

// sampleApp builds the project the examples are drawn from.
func sampleApp() *app.App {
	names := make([]string, 0, len(sampleTasks))
	for _, t := range sampleTasks {
		names = append(names, t.name)
	}
	tasks := pivot.Fixture(names)
	for i := range tasks {
		tasks[i].Desc = sampleTasks[i].desc
		tasks[i].Where = task.Where{File: "/src/acme/Taskfile.yml", Line: 3 + i*4}
	}
	tasks[1].Aliases = []string{"b"}
	tasks[5].Aliases = []string{"t"}
	tasks[14].Dangerous = true

	a := app.New(tasks, "/src/acme").WithConfig(theme.FromViper(v))
	// Somewhere that does not exist, so nothing here reads or writes the real archive.
	a.SetStateDir("/nonexistent/taskui-examples")

	hour := time.Now().Add(-time.Hour).Unix()
	a.Outcomes = map[string]store.Outcome{
		"check":        {Ok: true, WhenUnix: hour},
		"fmt":          {Ok: true, WhenUnix: hour},
		"lint":         {Ok: true, WhenUnix: hour},
		"backend:test": {Ok: false, WhenUnix: time.Now().Add(-9 * time.Minute).Unix()},
		"backend:lint": {Ok: true, WhenUnix: hour},
		"api:lint":     {Ok: true, WhenUnix: hour},
	}
	a.SetFoldAll(false)
	return a
}

// sampleRun is a `task ci` that failed in the middle, with output worth looking at.
func sampleRun() *run.Run {
	r := run.Detached(sampleAll, run.GraphFrom(
		run.Edge{Parent: "all", Children: []string{"fmt", "lint", "backend:test"}},
		run.Edge{Parent: sampleFmt},
		run.Edge{Parent: sampleLint},
		run.Edge{Parent: sampleTest},
	))
	r.Feed(sampleFmt, "task: [fmt] gofmt -l -w .")
	r.Feed(sampleFmt, "3 files reformatted")
	r.Feed(sampleLint, "task: [lint] golangci-lint run")
	r.Feed(sampleLint, "0 issues.")
	r.Feed(sampleTest, "task: [backend:test] go test -race ./...")
	r.Feed(sampleTest, "=== RUN   TestOrderTotal")
	r.Feed(sampleTest, "    order_test.go:88: want 1200, got 1180")
	r.Feed(sampleTest, "--- FAIL: TestOrderTotal (0.01s)")
	r.Feed(sampleTest, "FAIL\tacme/backend\t1.31s")
	r.ApplyFailed(sampleTest)
	r.Feed("", `task: Failed to run task "all": task: Failed to run task "backend:test": exit status 1`)
	r.Finish(1)
	// Plausible clocks. A demo whose header says `0ms` is showing you a shape the real thing
	// never has.
	r.Duration, r.HasDuration = 2100*time.Millisecond, true
	r.Tasks[sampleFmt].SetDurationForTest(208 * time.Millisecond)
	r.Tasks[sampleLint].SetDurationForTest(502 * time.Millisecond)
	r.Tasks[sampleTest].SetDurationForTest(1310 * time.Millisecond)
	// The aggregate's own clock, so the profile's "inc." column has something true to say.
	r.Tasks[sampleAll].SetDurationForTest(2100 * time.Millisecond)
	return r
}

// sampleTimeline is one task's stored runs: three passes, then it started failing.
func sampleTimeline() []store.Point {
	now := time.Now()
	return []store.Point{
		{RunID: "e", Root: sampleAll, WhenUnix: now.Add(-9 * time.Minute).Unix(),
			Status: "Failed", DurationMs: 1310, Lines: 44, Commit: "d1f091a7"},
		{RunID: "d", Root: sampleAll, WhenUnix: now.Add(-2 * time.Hour).Unix(),
			Status: "Failed", DurationMs: 1280, Lines: 44, Commit: "d1f091a7"},
		{RunID: "c", Root: sampleAll, WhenUnix: now.Add(-3 * time.Hour).Unix(),
			Status: "Ok", DurationMs: 1190, Lines: 31, Commit: "9b4c2e1f"},
		{RunID: "b", Root: sampleTest, WhenUnix: now.Add(-5 * time.Hour).Unix(),
			Status: "Ok", DurationMs: 88, Lines: 31, Commit: "9b4c2e1f"},
		{RunID: "a", Root: sampleAll, WhenUnix: now.Add(-26 * time.Hour).Unix(),
			Status: "Ok", DurationMs: 1210, Lines: 30, Commit: "41aa08c3"},
	}
}

// sampleDiff is what changed between the last pass and the first failure.
func sampleDiff() []app.DiffRow {
	return []app.DiffRow{
		{Op: diff.Same, Gap: true},
		{Op: diff.Same, Text: "=== RUN   TestOrderTotal", Old: 12, New: 12},
		{Op: diff.Del, Text: "--- PASS: TestOrderTotal (0.01s)", Old: 13},
		{Op: diff.Ins, Text: "    order_test.go:88: want 1200, got 1180", New: 13},
		{Op: diff.Ins, Text: "--- FAIL: TestOrderTotal (0.01s)", New: 14},
		{Op: diff.Same, Text: "=== RUN   TestRefund", Old: 14, New: 15},
		{Op: diff.Same, Gap: true},
		{Op: diff.Del, Text: "ok  \tacme/backend\t1.19s", Old: 41},
		{Op: diff.Ins, Text: "FAIL\tacme/backend\t1.31s", New: 43},
	}
}

// draw renders one frame of the sample project after the given keys have been pressed.
//
// Colour when there is a terminal to show it in, plain text when there is not — the same
// frame either way, since the escapes are all lipgloss decides not to emit into a pipe.
func draw(height int, keys string, setup func(*app.App)) func(int) []string {
	return func(width int) []string {
		a := sampleApp()
		if setup != nil {
			setup(a)
		}
		for _, c := range keys {
			a.HandleKey(app.KeyFor(c))
		}
		if !stdoutIsTerminal() {
			return a.RenderHeadless(width, height)
		}
		return strings.Split(a.RenderFrame(width, height), "\n")
	}
}

func stdoutIsTerminal() bool { return term.IsTerminal(os.Stdout.Fd()) }

// --- the examples -------------------------------------------------------------------------

//nolint:gochecknoglobals // this is the content, and it has to be a value to be indexed by name
var examples = []example{{
	name:  "browse",
	title: "Finding a task in a Taskfile you did not write",
	parts: []part{
		text("Opening a project gives you its shape, not its list. Namespaces are folds, and the " +
			"number on the right is how many tasks are inside."),
		frame(draw(11, "", nil)),
		text("`space` or `o` opens one. `⇧O` or `⇥` opens everything at once, which on a large " +
			"Taskfile is how you go from shape to detail and back."),
		frame(draw(14, "\t", nil)),
		text("Each row says how that task went last time, in a column you can run your eye down. " +
			"A blank there means never run, which is not the same as passed."),
		text("Three ways in, and they answer different questions. `/` filters — it hides " +
			"everything that does not match, which is what you want when you are looking for all " +
			"the linting tasks:"),
		frame(draw(11, "/lint", nil)),
		text("`t` jumps instead: the tree stays whole and only the cursor moves, which is what you " +
			"want when the surroundings still matter. Both match fuzzily over the whole colon " +
			"path, so `blint` finds `backend:lint`."),
		text("And `p` regroups. `domain` splits the name on `:`; `verb` collects the last segment, " +
			"gathering the cross-cutting concerns the domain tree scatters:"),
		frame(draw(12, "p", nil)),
		text("The root aggregate sits directly above its own fan-out, so the verb pivot doubles as " +
			"a preview of what `task lint` will do — without running anything. `p` again reaches " +
			"`file`, which groups by the Taskfile each task is written in."),
	},
}, {
	name:  "run",
	title: "Running something and reading what happened",
	parts: []part{
		text("`⏎` runs the task under the cursor. The view follows the run, and the moment " +
			"something fails it opens that task and stops there:"),
		frame(draw(18, "", func(a *app.App) {
			a.OpenRunForTest(sampleRun())
			a.Screen = app.ScreenRun
			a.Follow()
		})),
		text("The failure is open in full. Everything that finished dropped back to a peek — `▿`, " +
			"the last few lines it printed. That is the resting state, so a run you have not " +
			"touched still tells you what each step said rather than only how long it took."),
		text("`o` cycles a task hidden → peek → full, `⇧O` does it to all of them, and " +
			"`peek-lines:` in your config sets how many lines a peek shows."),
		text("`esc` leaves without stopping anything; the picker then says what is still going and " +
			"`v` takes you back. `x` stops a run — from the picker too, on whichever task the " +
			"cursor is on, so a background run does not have to be opened just to be stopped."),
	},
}, {
	name:  "search",
	title: "Finding the error in two thousand lines",
	parts: []part{
		text("`/` searches the output; `n` and `N` step through the matches in execution order. " +
			"`f` collapses the run to just the matching lines, kept under the tasks that produced " +
			"them, with the tasks that had no hits dropped entirely:"),
		frame(draw(12, "/FAIL\nf", func(a *app.App) {
			a.OpenRunForTest(sampleRun())
			a.Screen = app.ScreenRun
		})),
		text("`[` and `]` widen and narrow the context around each hit — a `--- FAIL:` on its own " +
			"hides the assertion underneath it, which is the half that says what broke."),
		text("`y` copies the line under the cursor, `⇧Y` everything the task printed."),
	},
}, {
	name:  "edit",
	title: "Going to the file it broke in",
	parts: []part{
		text("Any `file:line` in captured output is underlined, and `e` opens it in `$EDITOR` at " +
			"that line — the step this tool used to leave you to do by hand."),
		frame(draw(13, "jjjjjjjjjj", func(a *app.App) {
			a.OpenRunForTest(sampleRun())
			a.Screen = app.ScreenRun
			a.RunExpand(sampleTest)
		})),
		text("`go test` prints a bare basename relative to its package directory, which is neither " +
			"where the run started nor named anywhere in the line. taskui indexes the project the " +
			"first time you press `e` and finds it; two files with the same name resolve to the " +
			"shallower one and it says it had to guess."),
		text("`e` on a line that names no file falls back to the first location the task printed " +
			"and says where it came from, so it is never a dead keystroke. In the picker there is " +
			"no error to follow, so `e` opens the task's own definition in the Taskfile."),
	},
}, {
	name:  "history",
	title: "\"When did this start failing?\"",
	parts: []part{
		text("Every finished run is kept. `h` lists them all; `⇧H` asks the narrower and more " +
			"useful question — how has this one task been going:"),
		frame(draw(11, "", func(a *app.App) {
			a.TimelineOf = sampleTest
			a.Timeline = sampleTimeline()
			a.Screen = app.ScreenTimeline
		})),
		text("The trend across the top is where it turned. Bars scale to the slowest run in the " +
			"list, so a step that got slower is a shape rather than five numbers to compare by " +
			"hand, and the right-hand column is the run each appearance was part of."),
		text("`⇧D` then answers what actually changed, against the last run that went differently " +
			"— not the row below, which on the newest of three consecutive failures would answer " +
			"\"nothing\". Everything both runs shared elides to a `⋮`."),
		frame(draw(11, "", func(a *app.App) {
			a.DiffOf = sampleTest
			a.DiffAgainstWhat = "when it last passed"
			a.DiffAgainst = sampleTimeline()[2]
			a.DiffRows = sampleDiff()
			a.DiffStat = diff.Stat{Added: 3, Removed: 2}
			a.Screen = app.ScreenDiff
		})),
		text("Each run records the git revision it happened at, which makes flakiness a fact " +
			"rather than a guess: the same commit, both outcomes."),
		shell(
			"$ taskui --flaky",
			"backend:test   d1f091a   4 passed   4 failed   just now",
			"-- 1 flaky",
		),
	},
}, {
	name:  "profile",
	title: "Where the time went",
	parts: []part{
		text("`⇧T` in a run ranks its tasks by self time — what each spent on its own commands, " +
			"with its children's subtracted."),
		frame(draw(10, "T", func(a *app.App) {
			a.OpenRunForTest(sampleRun())
			a.Screen = app.ScreenRun
		})),
		text("Ranked by total instead, every aggregate outranks every task that did any work and " +
			"the profile reports that `all` is the slow one — which is true and useless. `all` " +
			"sits at the bottom with its inclusive figure beside it."),
		text("It stays current while the run does and holds still once it stops. `⏎` goes to that " +
			"task in the run view."),
	},
}, {
	name:  "several",
	title: "Running several things, and leaving them running",
	parts: []part{
		text("`m` marks a task; `⏎` then runs every marked one, each in its own slot."),
		frame(draw(12, "\tjmjjjm", nil)),
		text("A batch containing anything on the danger list asks once for the whole set rather " +
			"than putting a prompt between each pair of starts. More marks than free slots starts " +
			"what it can and says what it left."),
		text("Slots hold six runs at once. `⇥` and `⇧⇥` switch between them, `1`…`9` jump " +
			"straight to one, and each keeps its own scroll position, folds and output."),
		text("`⇧A` detaches one: quitting stops being responsible for it and it keeps running " +
			"afterwards. Its output stops there, so detaching archives what it has — otherwise a " +
			"two-hour run would leave nothing behind."),
	},
}, {
	name:  "shell",
	title: "Driving it from a script",
	parts: []part{
		text("Every screen renders without a terminal, which is how this page is drawn."),
		shell(
			"taskui --list                  # tasks it found",
			"taskui --dump verb             # a pivot, expanded",
			"taskui --graph all             # the execution graph",
			"taskui --run all               # run, print the tree",
			"taskui --search 'FAIL|error'   # grep stored runs",
			"taskui --timeline backend:test # one task's runs",
			"taskui --diff backend:test     # what changed",
			"taskui --flaky                 # both ways at one commit",
			"taskui --screenshot 120x30     # one frame to stdout",
		),
		text("`--timeline` and `--flaky` are tab-separated, and `--flaky` exits non-zero when it " +
			"finds any, so it composes:"),
		shell("taskui --flaky || echo 'worth looking into'"),
		text("And `taskui config edit` opens your settings; `taskui examples <topic>` prints just " +
			"one of these."),
	},
}}
