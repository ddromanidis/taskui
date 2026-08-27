package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ddromanidis/taskui/internal/graph"
)

func one(t *testing.T, text string) LineEvent {
	t.Helper()
	events := parseLine(text)
	line, ok := events[0].(LineEvent)
	if !ok {
		t.Fatalf("expected a line, got %#v", events[0])
	}
	return line
}

// A run that never ends must not grow without end either. `docker compose logs -f` left up
// over an afternoon is the case this exists for.
func TestAFullBufferDropsItsOldestLines(t *testing.T) {
	r := Detached("tail", graph.New())
	r.Apply(GraphReady{Graph: GraphFrom(Edge{"tail", nil})})
	for i := range MaxLines + DropBlock {
		r.Feed("tail", fmt.Sprintf("line %d", i))
	}

	task := r.Tasks["tail"]
	if len(task.Lines) > MaxLines {
		t.Errorf("capped at %d", len(task.Lines))
	}
	if task.Dropped != DropBlock {
		t.Errorf("dropped = %d, want %d", task.Dropped, DropBlock)
	}
	if got, want := task.Lines[0].Plain, fmt.Sprintf("line %d", DropBlock); got != want {
		t.Errorf("oldest kept = %q, want %q", got, want)
	}
	if got, want := task.Lines[len(task.Lines)-1].Plain, fmt.Sprintf("line %d", MaxLines+DropBlock-1); got != want {
		t.Errorf("newest = %q, want %q", got, want)
	}
}

// provisional is an index into the very buffer pushLine shifts, so a drop has to move it
// or the next partial write lands on an unrelated line.
//
// Driven through pushLine rather than through events on purpose. No event sequence can
// currently reach this: a partial is always the last line of its task, and the next event
// for that task either grows it in place or supersedes it, so the buffer never overflows
// underneath one. That is a property of the event arms, though, not of the buffer — the
// fixup and this test are what stop the next arm from quietly invalidating it.
func TestADropShiftsTheIndexThePendingPromptIsHeldAt(t *testing.T) {
	r := Detached("tail", graph.New())
	r.Apply(GraphReady{Graph: GraphFrom(Edge{"tail", nil})})
	for i := range MaxLines - 1 {
		r.Feed("tail", fmt.Sprintf("line %d", i))
	}
	r.Apply(Partial{Text: "Proceed? (y/n) "})
	if r.provisional == nil {
		t.Fatal("a prompt should be pending")
	}
	before := r.provisional.index

	r.pushLine("tail", newLine("one more", false))

	if r.provisional == nil {
		t.Fatal("still pending")
	}
	if got, want := r.provisional.index, before-DropBlock; got != want {
		t.Errorf("index = %d, want %d — it should move with the buffer", got, want)
	}
	if got, _ := r.PendingPrompt(); got != "Proceed? (y/n) " {
		t.Errorf("prompt = %q", got)
	}
}

func TestCommandEchoIsAttributedAndMarked(t *testing.T) {
	line := one(t, `task: [backend:lint] cargo clippy --all`)
	if line.Task != "backend:lint" {
		t.Errorf("task = %q", line.Task)
	}
	if !line.IsCommand {
		t.Error("should be marked as a command echo")
	}
	if !strings.Contains(line.Raw, "cargo clippy") {
		t.Errorf("raw = %q", line.Raw)
	}
}

// The prefix is structure, not content — it is stripped so search and display see the line
// the tool actually printed.
func TestPrefixedOutputIsAttributedAndTheTagRemoved(t *testing.T) {
	line := one(t, "[backend:lint] warning: unused variable")
	if line.Task != "backend:lint" {
		t.Errorf("task = %q", line.Task)
	}
	if line.Raw != "warning: unused variable" {
		t.Errorf("raw = %q", line.Raw)
	}
	if line.IsCommand {
		t.Error("not a command echo")
	}
}

// Output that merely starts with a bracket must not invent a task.
func TestBracketedOutputThatIsNotATaskTagIsLeftAlone(t *testing.T) {
	line := one(t, "[2026-08-25 18:04:11] server listening")
	if line.Task != "" {
		t.Errorf("task = %q", line.Task)
	}
	if line.Raw != "[2026-08-25 18:04:11] server listening" {
		t.Errorf("raw = %q", line.Raw)
	}
}

// go-task nests the message; the innermost name is the task that actually failed, not the
// aggregate that contained it.
func TestFailureReportsTheInnermostTask(t *testing.T) {
	events := parseLine(`task: Failed to run task "all": task: Failed to run task "backend:lint": exit status 1`)
	failed, ok := events[0].(FailedEvent)
	if !ok {
		t.Fatalf("expected a failure, got %#v", events[0])
	}
	if failed.Task != "backend:lint" {
		t.Errorf("culprit = %q", failed.Task)
	}
}

func TestAnsiIsStrippedForSearchButKeptForDisplay(t *testing.T) {
	line := newLine("\x1b[31merror\x1b[0m: boom", false)
	if line.Plain != "error: boom" {
		t.Errorf("plain = %q", line.Plain)
	}
	if !strings.Contains(line.Raw, "\x1b") {
		t.Error("colour should survive for rendering")
	}
}

// A parent keeps running while its children work; a sibling that has stopped producing
// output has finished.
func TestAParentStaysRunningWhileAChildWorks(t *testing.T) {
	r := Detached("all", GraphFrom(
		Edge{"all", []string{"lint", "test"}},
		Edge{"lint", []string{"backend:lint"}},
	))

	r.Feed("all", "starting")
	r.Feed("lint", "linting")
	r.Feed("backend:lint", "clippy")

	for name, want := range map[string]Status{"all": Running, "lint": Running, "backend:lint": Running} {
		if got := r.Tasks[name].Status; got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	// Moving to a different branch closes out the one we left.
	r.Feed("test", "testing")
	if got := r.Tasks["backend:lint"].Status; got != Ok {
		t.Errorf("backend:lint = %v", got)
	}
	if got := r.Tasks["lint"].Status; got != Ok {
		t.Errorf("lint = %v", got)
	}
	if got := r.Tasks["all"].Status; got != Running {
		t.Errorf("all = %v, still the root", got)
	}
}

func TestAFailurePropagatesToEverythingThatInvokedIt(t *testing.T) {
	r := Detached("all", GraphFrom(
		Edge{"all", []string{"lint"}},
		Edge{"lint", []string{"backend:lint"}},
	))
	r.Feed("backend:lint", "error: boom")
	r.ApplyFailed("backend:lint")

	for _, name := range []string{"backend:lint", "lint", "all"} {
		if got := r.Tasks[name].Status; got != Failed {
			t.Errorf("%s = %v, want Failed", name, got)
		}
	}
}

// Tasks in the graph that never produced output were never reached.
func TestUnreachedTasksAreSkippedNotPassed(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{"all", []string{"lint", "test"}}))
	r.Feed("lint", "linting")
	r.ApplyFailed("lint")
	r.Finish(1)

	if got := r.Tasks["lint"].Status; got != Failed {
		t.Errorf("lint = %v", got)
	}
	if got := r.Tasks["test"].Status; got != Skipped {
		t.Errorf("test = %v, want Skipped", got)
	}
	if !r.Finished() {
		t.Error("the run should be finished")
	}
}

// A clean exit closes out whatever was still open.
func TestACleanExitSettlesRunningTasksAsOk(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{"all", []string{"lint"}}))
	r.Feed("all", "go")
	r.Feed("lint", "linting")
	r.Finish(0)

	if got := r.Tasks["all"].Status; got != Ok {
		t.Errorf("all = %v", got)
	}
	if got := r.Tasks["lint"].Status; got != Ok {
		t.Errorf("lint = %v", got)
	}
	if r.Exit != 0 || !r.HasExit {
		t.Errorf("exit = %d", r.Exit)
	}
}

// A running task must report time as it goes, not only once it stops — otherwise the only
// live figure on screen is the total, which does not say which step is slow.
func TestARunningTaskReportsElapsedTime(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{"all", []string{"slow"}}))
	r.Feed("slow", "working")

	ticking, ok := r.Tasks["slow"].Elapsed()
	if !ok {
		t.Fatal("a running task has a clock")
	}
	if _, ok := r.Tasks["all"].Elapsed(); !ok {
		t.Error("so does its parent")
	}

	r.Finish(0)
	finished, _ := r.Tasks["slow"].Elapsed()
	if finished < ticking {
		t.Errorf("settled at %v, was %v", finished, ticking)
	}
}

// Never started means no time, rather than a misleading zero.
func TestAnUnreachedTaskHasNoClock(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{"all", []string{"a", "b"}}))
	r.Feed("a", "hello")
	r.Finish(0)
	if _, ok := r.Tasks["b"].Elapsed(); ok {
		t.Error("an unreached task should have no clock")
	}
}

// Progress output redraws in place; only the final state is real.
func TestCarriageReturnsCollapseToTheLastWrite(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Downloading  10%\rDownloading  50%\rDownloading 100%", "Downloading 100%"},
		{"plain line", "plain line"},
		// A trailing `\r` is not a write; the text before it stands.
		{"done\r", "done"},
		{"", ""},
	} {
		if got := applyOverwrites(c.in); got != c.want {
			t.Errorf("applyOverwrites(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// npm and npx spinners rub out each frame with backspaces rather than `\r`, which is why
// `\|/-\|/-Need to install…` arrived with every frame still attached.
func TestBackspacesRubOutThePreviousCharacter(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"\b|\b/\b-\b", ""},
		{"\\\b|\b/\b-\bNeed to install", "Need to install"},
		{"abc\b\b", "a"},
		// More backspaces than characters is not a panic.
		{"a\b\b\b", ""},
	} {
		if got := applyOverwrites(c.in); got != c.want {
			t.Errorf("applyOverwrites(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A prompt never gets a newline, so a strictly line-based reader shows nothing and the run
// looks hung. The unterminated tail has to surface as a line of its own.
func TestAnUnterminatedPromptIsShown(t *testing.T) {
	r := Detached("deploy", GraphFrom(Edge{"deploy", nil}))
	r.Apply(LineEvent{Task: "deploy", Raw: "Deploying"})
	r.Apply(Partial{Text: "Do you want to proceed? (y/n) "})

	if got := len(r.Tasks["deploy"].Lines); got != 2 {
		t.Errorf("lines = %d", got)
	}
	if got, _ := r.PendingPrompt(); got != "Do you want to proceed? (y/n) " {
		t.Errorf("prompt = %q", got)
	}
	if !r.LooksLikeAPrompt() {
		t.Error("should look like a prompt")
	}
}

// Reads arrive in chunks, so the tail grows. It must be replaced, not stacked up.
func TestAGrowingPartialReplacesItself(t *testing.T) {
	r := Detached("deploy", GraphFrom(Edge{"deploy", nil}))
	r.Apply(Partial{Text: "Do you want"})
	r.Apply(Partial{Text: "Do you want to proceed? "})

	if got := len(r.Tasks["deploy"].Lines); got != 1 {
		t.Errorf("one line, not one per read: %d", got)
	}
	if got, _ := r.PendingPrompt(); got != "Do you want to proceed? " {
		t.Errorf("prompt = %q", got)
	}
}

// Once the newline arrives, the completed line supersedes the provisional one.
func TestACompletedLineSupersedesItsPartial(t *testing.T) {
	r := Detached("deploy", GraphFrom(Edge{"deploy", nil}))
	r.Apply(Partial{Text: "half a li"})
	r.Apply(LineEvent{Task: "deploy", Raw: "half a line, now whole"})

	if got := len(r.Tasks["deploy"].Lines); got != 1 {
		t.Errorf("lines = %d", got)
	}
	if got := r.Tasks["deploy"].Lines[0].Plain; got != "half a line, now whole" {
		t.Errorf("line = %q", got)
	}
	if _, ok := r.PendingPrompt(); ok {
		t.Error("no longer waiting")
	}
}

// Ordinary wrapped output is not a question; the hint must not cry wolf.
func TestPlainOutputIsNotMistakenForAPrompt(t *testing.T) {
	r := Detached("build", GraphFrom(Edge{"build", nil}))
	r.Apply(Partial{Text: "   Compiling taskui v0.1.0"})
	if r.LooksLikeAPrompt() {
		t.Error("ordinary output read as a prompt")
	}
}

// A failure before any task starts still has to be visible: it lands on the task that was
// invoked.
func TestRunLevelErrorsAreAttributedToTheRoot(t *testing.T) {
	r := Detached("wt:new", GraphFrom(Edge{"wt:new", nil}))
	r.Apply(LineEvent{Raw: `task: Task "wt:new" cancelled because it is missing required variables: NAME`})
	r.Finish(206)

	root := r.Tasks["wt:new"]
	if len(root.Lines) != 1 {
		t.Fatalf("the error was swallowed: %d lines", len(root.Lines))
	}
	if !strings.Contains(root.Lines[0].Plain, "missing required variables") {
		t.Errorf("line = %q", root.Lines[0].Plain)
	}
}

// Untagged lines belong to whoever spoke last, not to nobody.
func TestUntaggedLinesFollowTheActiveTask(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{"all", []string{"lint"}}))
	r.Feed("lint", "first")
	r.Apply(LineEvent{Raw: "continuation"})
	if got := len(r.Tasks["lint"].Lines); got != 2 {
		t.Fatalf("lines = %d", got)
	}
	if got := r.Tasks["lint"].Lines[1].Plain; got != "continuation" {
		t.Errorf("line = %q", got)
	}
}

// An aggregate whose commands are all `task:` invocations never emits a line tagged with
// its own name — every line belongs to a child. It still ran, and must not be reported as
// skipped.
func TestAnAggregateWithNoOutputOfItsOwnStillRan(t *testing.T) {
	r := Detached("agg", GraphFrom(
		Edge{"agg", []string{"lint"}},
		Edge{"lint", []string{"a", "b"}},
	))

	r.Feed("a", "a hello")
	if got := r.Tasks["agg"].Status; got != Running {
		t.Errorf("agg = %v", got)
	}
	if got := r.Tasks["lint"].Status; got != Running {
		t.Errorf("lint = %v", got)
	}

	r.Feed("b", "b hello")
	r.Finish(0)

	if got := r.Tasks["agg"].Status; got != Ok {
		t.Errorf("agg = %v", got)
	}
	if got := r.Tasks["lint"].Status; got != Ok {
		t.Errorf("lint = %v", got)
	}
	if _, ok := r.Tasks["agg"].Elapsed(); !ok {
		t.Error("and it should have a duration")
	}
}

// Opening the chain must go top-down, so Order reads the way the run reads.
func TestOrderOpensAncestorsBeforeTheTaskItself(t *testing.T) {
	r := Detached("agg", GraphFrom(
		Edge{"agg", []string{"lint"}},
		Edge{"lint", []string{"a"}},
	))
	r.Feed("a", "hello")
	if want := []string{"agg", "lint", "a"}; !reflect.DeepEqual(r.Order, want) {
		t.Errorf("order = %v, want %v", r.Order, want)
	}
}

// --- Tests against real `task` processes. -----------------------------------------

func needsGoTask(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not on PATH")
	}
}

func taskfile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pollUntil(r *Run, limit time.Duration, done func() bool) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		r.Poll()
		if done() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.Poll()
}

// Cancelling has to reap the shell commands, not just `task` itself — they are what is
// actually doing the work, and they outlive their parent unless the whole process group is
// signalled.
func TestCancellingStopsTheChildAndItsCommands(t *testing.T) {
	needsGoTask(t)
	dir := taskfile(t, "version: \"3\"\ntasks:\n  sleeper:\n    cmds: ['echo started', 'sleep 30']\n")

	r, err := Start(dir, "sleeper", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the command to actually be running.
	pollUntil(r, 15*time.Second, func() bool {
		task, ok := r.Tasks["sleeper"]
		return ok && len(task.Lines) > 0
	})
	if r.Finished() {
		t.Fatal("still sleeping when we cancel")
	}

	r.Cancel()

	// The exit event should follow promptly; without group-killing, `sleep 30` would hold
	// the pty open and this would time out.
	pollUntil(r, 10*time.Second, r.Finished)

	if !r.Cancelled() {
		t.Error("should be marked cancelled")
	}
	if !r.Finished() {
		t.Error("the run should have ended instead of hanging on `sleep 30`")
	}
}

// stillRunning asks whether anything on this machine is still running a command containing
// marker. The marker is unique per test, so this cannot see a leftover from another run of
// the suite and call it a failure.
func stillRunning(marker string) bool {
	out, err := exec.Command("ps", "-eo", "command").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), marker)
}

// A command that catches SIGTERM and declines to die must not outlive the run.
//
// This is the case the polite signals cannot reach and, crucially, the one a second
// keypress cannot reach either: go-task exits the instant it is signalled, and under
// `--output prefixed` every command writes into go-task's pipe rather than the pty — so
// the pty hits EOF and the run reports itself finished while the survivor is still there.
// Nothing is left holding it open to notice. Reaping the group has to happen on the way
// out, from the one place that still knows the group's name.
func TestACommandThatIgnoresSigtermDoesNotOutliveTheRun(t *testing.T) {
	needsGoTask(t)
	marker := fmt.Sprintf("taskui-stubborn-marker-%d", os.Getpid())
	// Deliberately a real `/bin/sh`: go-task runs commands through its own embedded
	// interpreter, whose `trap` only knows EXIT — so the shell that has to ignore the
	// signal is spawned explicitly. `trap '' TERM HUP` ignores both, and the loop means
	// killing the `sleep` underneath it achieves nothing.
	//
	// The marker rides along as the shell's $0, which is where `ps` will show it.
	dir := taskfile(t, fmt.Sprintf(
		"version: \"3\"\ntasks:\n  stubborn:\n    cmds:\n      - /bin/sh -c \"trap '' TERM HUP; echo armed; while :; do sleep 1; done\" %s\n",
		marker,
	))

	r, err := Start(dir, "stubborn", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}

	pollUntil(r, 15*time.Second, func() bool {
		task, ok := r.Tasks["stubborn"]
		if !ok {
			return false
		}
		for _, l := range task.Lines {
			if strings.Contains(l.Plain, "armed") {
				return true
			}
		}
		return false
	})
	if r.Finished() {
		t.Fatal("the trap should be armed and looping")
	}
	if !stillRunning(marker) {
		t.Fatal("the stubborn shell is not up")
	}

	r.Cancel()
	pollUntil(r, 15*time.Second, r.Finished)
	if !r.Finished() {
		t.Fatal("the run should have ended")
	}

	// Reported finished only after the group was taken, so by here it is already gone — the
	// wait is for the OS to catch up, not for anything else to happen.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && stillRunning(marker) {
		time.Sleep(50 * time.Millisecond)
	}
	if stillRunning(marker) {
		t.Error("the shell that ignored SIGTERM was orphaned rather than going with the run")
	}
}

// Can a task be typed at under `--output prefixed`?
//
// go-task wraps stdout and stderr for prefixing but leaves stdin alone, so the answer
// should be yes even though the prompt itself never becomes visible. If it is, blind input
// beats forcing a whole re-run of something like a deploy.
func TestStdinReachesTheChildEvenWhenOutputIsPrefixed(t *testing.T) {
	needsGoTask(t)
	dir := taskfile(
		t,
		"version: \"3\"\ntasks:\n  ask:\n    cmds:\n      - 'printf \"Proceed? \"; read ans; echo; echo \"got:$ans\"'\n",
	)

	// interactive = false, i.e. --output prefixed.
	r, err := Start(dir, "ask", nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Kill()

	// No prompt will appear, so wait for the command echo instead.
	pollUntil(r, 20*time.Second, func() bool {
		task, ok := r.Tasks["ask"]
		return ok && len(task.Lines) > 0
	})
	time.Sleep(300 * time.Millisecond)
	if r.Finished() {
		t.Fatal("should be blocked on the read")
	}

	if !r.SendInput([]byte("y\r")) {
		t.Fatal("could not write to the pty")
	}

	pollUntil(r, 20*time.Second, r.Finished)

	var output strings.Builder
	for _, l := range r.Tasks["ask"].Lines {
		output.WriteString(l.Plain)
	}
	if !r.Finished() {
		t.Fatalf("it should have proceeded: %q", output.String())
	}
	if !strings.Contains(output.String(), "got:y") {
		t.Errorf("the task should have seen the answer: %q", output.String())
	}
}

// The whole interactive loop against a real `task` process: the prompt surfaces without a
// newline, the answer goes back down the pty, and the task proceeds.
//
// This is the `wrangler`/`terraform` case — before this, such a task simply hung with a
// blank screen and the only way out was killing it.
func TestAnswersAnInteractivePrompt(t *testing.T) {
	needsGoTask(t)
	dir := taskfile(
		t,
		"version: \"3\"\ntasks:\n  ask:\n    cmds:\n      - 'printf \"Proceed? (y/n) \"; read ans; echo; echo \"got:$ans\"'\n",
	)

	// Interactive: under `--output prefixed` the prompt never arrives at all.
	r, err := Start(dir, "ask", nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Kill()

	pollUntil(r, 20*time.Second, r.LooksLikeAPrompt)
	prompt, _ := r.PendingPrompt()
	if !strings.Contains(prompt, "Proceed?") {
		t.Fatalf("the prompt should have surfaced: %q", prompt)
	}
	if r.Finished() {
		t.Fatal("should still be waiting on us")
	}

	if !r.SendInput([]byte("y\r")) {
		t.Fatal("the answer did not reach the pty")
	}

	pollUntil(r, 20*time.Second, r.Finished)

	var output strings.Builder
	for _, l := range r.Tasks["ask"].Lines {
		output.WriteString(l.Plain)
	}
	if !r.Finished() {
		t.Fatalf("it hung instead of proceeding: %q", output.String())
	}
	if !strings.Contains(output.String(), "got:y") {
		t.Errorf("the task should have seen the answer: %q", output.String())
	}
}

// go-task announces a skip with no `[name]` prefix, so the line lands in the parent's
// output — several rows from the `⏸` it explains. The reason has to be copied onto the task
// it is about.
func TestASkipIsExplainedOnTheTaskItIsAbout(t *testing.T) {
	r := Detached("all", GraphFrom(
		Edge{Parent: "all", Children: []string{"cached", "always"}},
	))
	r.Feed("", `task: Task "cached" is up to date`)
	r.Feed("always", "working")
	r.Finish(0)

	if got := r.Tasks["cached"].Note; got != "up to date" {
		t.Errorf("cached note = %q", got)
	}
	if r.Tasks["cached"].Status != Skipped {
		t.Errorf("cached status = %v", r.Tasks["cached"].Status)
	}
	if got := r.Tasks["always"].Note; got != "" {
		t.Errorf("always should have no note, got %q", got)
	}
	// The announcement is real output and stays where go-task printed it.
	var found bool
	for _, l := range r.Tasks["all"].Lines {
		if strings.Contains(l.Plain, "is up to date") {
			found = true
		}
	}
	if !found {
		t.Error("the announcement was moved rather than copied")
	}
}

// The pty delivers CRLF. An anchored match without a trim explained the first of two
// consecutive skips and silently missed the second.
func TestASkipIsStillReadWithACarriageReturnOnIt(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{Parent: "all", Children: []string{"one", "two"}}))
	r.Feed("", "\x1b[35mtask: Task \"one\" is up to date\r")
	r.Feed("", "\x1b[0m\x1b[35mtask: Task \"two\" is up to date\r")
	r.Finish(0)

	for _, name := range []string{"one", "two"} {
		if got := r.Tasks[name].Note; got != "up to date" {
			t.Errorf("%s note = %q", name, got)
		}
	}
}

func TestAFailedPreconditionIsExplainedToo(t *testing.T) {
	r := Detached("guarded", GraphFrom(Edge{Parent: "guarded"}))
	r.Feed("", "task: required.txt is missing")
	r.Feed("", `task: Failed to run task "guarded": task: precondition not met`)
	r.Finish(201)

	if got := r.Tasks["guarded"].Note; got != "precondition not met" {
		t.Errorf("note = %q", got)
	}
}

// Inside an aggregate the report is nested — the parent wraps the child's failure — and the
// task with the unsatisfied precondition is the innermost one, not the one that invoked it.
func TestANestedPreconditionNamesTheInnermostTask(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{Parent: "all", Children: []string{"guarded"}}))
	r.Feed("", `task: Failed to run task "all": task: Failed to run task "guarded": task: precondition not met`)
	r.Finish(201)

	if got := r.Tasks["guarded"].Note; got != "precondition not met" {
		t.Errorf("guarded note = %q", got)
	}
	if got := r.Tasks["all"].Note; got != "" {
		t.Errorf("all should not be blamed for its child's precondition, got %q", got)
	}
}

// A line that arrives in pieces and is completed later takes a different path into storage.
// A check placed after that path's early return reads only the lines that arrived whole.
func TestASkipIsReadEvenWhenItsLineArrivedInPieces(t *testing.T) {
	r := Detached("all", GraphFrom(Edge{Parent: "all", Children: []string{"cached"}}))
	r.Apply(Partial{Text: `task: Task "cached" is u`})
	r.Feed("", `task: Task "cached" is up to date`)
	r.Finish(0)

	if got := r.Tasks["cached"].Note; got != "up to date" {
		t.Errorf("note = %q", got)
	}
}

// A task that ran is not a task that was skipped, whatever else is in the output.
func TestAnOrdinaryLineIsNotASkipNotice(t *testing.T) {
	for _, text := range []string{
		`echo 'task: Task "x" is up to date'`,
		`task: Task "x" is up to date, probably`,
		`# task: Task "x" is up to date`,
	} {
		if _, _, ok := skipReason(text); ok {
			t.Errorf("%q was read as a skip", text)
		}
	}
}
