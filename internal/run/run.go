// Package run runs a task and splits its output back apart, one bucket per task.
//
// `task` writes a single stream. We drive it with `--output prefixed`, which tags every
// output line with the task that produced it, and pair that with the execution graph from
// the graph package — the stream says who spoke, the graph says who called whom.
//
// Colour needs care. `--output prefixed` makes go-task pipe every command through its own
// prefixing writer, so a command's stdout is a pipe regardless of what taskui does —
// measured: isatty reports false inside prefixed mode even when go-task itself is on a
// pty. Tools that auto-detect therefore turn colour off, and no amount of pty gets it
// back. Forcing it by environment does: `CARGO_TERM_COLOR=always` restores cargo and
// clippy's colour through the pipe intact.
//
// The pty is still worth having — it keeps go-task's own output coloured and stops the
// usual switch to block buffering when stdout is not a terminal — it just is not what
// makes the tools colour.
package run

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/redact"
	"github.com/ddromanidis/taskui/internal/task"
)

type Status int

const (
	// Pending means in the graph but not reached yet.
	Pending Status = iota
	Running
	Ok
	Failed
	// Skipped means the run ended before this task was reached — usually because
	// something upstream failed.
	Skipped
)

func (s Status) Glyph() string {
	switch s {
	case Running:
		return "▶"
	case Ok:
		return "✓"
	case Failed:
		return "✗"
	case Skipped:
		return "⏸"
	default:
		return "·"
	}
}

// String is the name persisted in a manifest, and parsed back by the store.
func (s Status) String() string {
	switch s {
	case Running:
		return "Running"
	case Ok:
		return "Ok"
	case Failed:
		return "Failed"
	case Skipped:
		return "Skipped"
	default:
		return "Pending"
	}
}

func StatusFromString(s string) Status {
	switch s {
	case "Ok":
		return Ok
	case "Failed":
		return Failed
	case "Running":
		return Running
	case "Skipped":
		return Skipped
	default:
		return Pending
	}
}

// Line is one captured line, kept twice on purpose: Raw still has its escape sequences for
// rendering, Plain is what search runs over. Searching the raw bytes would miss matches
// wherever a colour change lands mid-word.
type Line struct {
	Raw   string
	Plain string
	// IsCommand is true for go-task's own `task: [name] <cmd>` echo, which is structure
	// rather than output and is worth rendering differently.
	IsCommand bool
}

// Restored rebuilds a line from the two stored halves. IsCommand is not persisted because
// it is derivable — it is exactly go-task's `task: [name] …` echo — and a marker in the
// `.txt` file would make the archive worse to grep.
func Restored(raw, plain string) Line {
	return Line{Raw: raw, Plain: plain, IsCommand: strings.HasPrefix(plain, "task: [")}
}

func newLine(raw string, isCommand bool) Line {
	return Line{Raw: raw, Plain: ansi.Strip(raw), IsCommand: isCommand}
}

// CommandText is a command echo with go-task's prefix taken off: `task: [test] go test
// ./...` becomes `go test ./...`.
//
// The prefix is an artifact of how the output is captured — `--output prefixed` is what
// tags every line with the task that printed it — rather than anything the command said.
// Whoever is showing the line already knows which task it belongs to, so restating it costs
// fifteen columns of a build log to say nothing.
//
// A line that is not a command echo comes back unchanged: there is nothing to strip, and a
// caller that has to check first is a caller that will forget to.
func CommandText(l Line) string {
	if !l.IsCommand {
		return l.Plain
	}
	text := strings.TrimPrefix(l.Plain, "task: ")
	if _, rest, ok := strings.Cut(text, "] "); ok {
		return rest
	}
	return text
}

// MaxLines caps the output kept per task.
//
// A run used to be a thing that ended, so the buffers could grow as far as they liked. A
// tailed container log does not end, and at a few hundred bytes a line an afternoon of one
// is gigabytes of resident memory. Old lines are dropped from the front and counted, so
// the view can say what it threw away rather than quietly rewriting history.
const MaxLines = 20_000

// DropBlock is how much goes at once. Draining a single line from the front of a
// 20,000-element slice on every line that arrives is quadratic; doing it once per 4,000 is
// not.
const DropBlock = 4_000

type TaskRun struct {
	Status Status
	// Note is why this task did not do what you expected, in go-task's own words — "up to
	// date", "precondition not met". It is attributed here rather than left where it was
	// printed: go-task announces a skip with no `[name]` prefix, so the line lands in the
	// *parent's* output, several rows away from the `⏸` it explains. A skipped task with no
	// reason beside it is the whole reason `⇧R` exists.
	Note  string
	Lines []Line
	// Dropped counts the lines that fell off the front of Lines to stay under MaxLines.
	Dropped  int
	started  time.Time
	duration time.Duration
	settled  bool
}

// Elapsed is time on the clock: the final figure once the task has finished, and a ticking
// one while it is still going. Showing nothing until a task completes means the only live
// timing on screen is the total, which is the least useful of them — during a slow build
// what you want to know is which step is taking it.
func (t *TaskRun) Elapsed() (time.Duration, bool) {
	if t.settled {
		return t.duration, true
	}
	if t.started.IsZero() {
		return 0, false
	}
	return time.Since(t.started), true
}

// CommandStatus is how the command echoed on line i went.
//
// go-task announces a command before running it and says nothing when it returns, so the
// verdict is read off the shape of what came after: another echo means this one finished,
// and the last echo carries the task's own status — a task that failed failed in its final
// command, and one still going is still inside it.
//
// A failure the Taskfile swallows (`ignore_error:`) is reported as a success, which is
// what it is from outside: go-task went on to the next command.
func (t *TaskRun) CommandStatus(i int) Status {
	if i < 0 || i >= len(t.Lines) || !t.Lines[i].IsCommand {
		return Pending
	}
	for _, l := range t.Lines[i+1:] {
		if l.IsCommand {
			return Ok
		}
	}
	return t.Status
}

// UnderCommand says how the line at i sits under the command that produced it: whether
// there is one above it in this task at all, and whether it is the last line before the next
// command starts.
//
// It is what lets output be drawn as a command's output rather than as loose lines. The
// answer is positional, like [TaskRun.CommandStatus] and for the same reason: go-task
// announces a command and never announces its end, so the end is where the next one begins.
func (t *TaskRun) UnderCommand(i int) (bool, bool) {
	if i < 0 || i >= len(t.Lines) || t.Lines[i].IsCommand {
		return false, false
	}
	under := false
	for j := i - 1; j >= 0; j-- {
		if t.Lines[j].IsCommand {
			under = true
			break
		}
	}
	if !under {
		return false, false
	}
	// The last line of the group is the one the next command follows, or the last line
	// there is.
	return true, i+1 >= len(t.Lines) || t.Lines[i+1].IsCommand
}

// Duration is the settled figure only, for the archive.
func (t *TaskRun) Duration() time.Duration {
	if t.settled {
		return t.duration
	}
	return 0
}

func RestoredTask(status Status, lines []Line, duration time.Duration) *TaskRun {
	return &TaskRun{Status: status, Lines: lines, duration: duration, settled: true}
}

func newTaskRun() *TaskRun { return &TaskRun{Status: Pending} }

// Event is what the capture goroutine sends back to the UI.
type Event interface{ event() }

// GraphReady arrives first: resolving the graph costs one `task --summary` per node — over
// a second for a large aggregate — so it happens on the worker goroutine and arrives here
// rather than freezing the UI at the moment you press enter.
type GraphReady struct{ Graph graph.Graph }

// Partial is output with no newline yet — an interactive prompt. `Do you want to proceed?
// (y/n) ` never terminates its line, so a strictly line-based reader shows nothing at all
// and the run just appears to hang.
type Partial struct{ Text string }

// Redacting says how many distinct secrets the redactor is masking, so the UI can say
// whether output has been through it.
type Redacting struct{ N int }

// LineEvent is one complete line. Task is empty when the line carried no `[name]` tag.
type LineEvent struct {
	Task      string
	Raw       string
	IsCommand bool
}

// FailedEvent is go-task reporting a specific task as the failure.
type FailedEvent struct{ Task string }

// Exited ends the run.
type Exited struct{ Code int }

func (GraphReady) event()  {}
func (Partial) event()     {}
func (Redacting) event()   {}
func (LineEvent) event()   {}
func (FailedEvent) event() {}
func (Exited) event()      {}

// stopGrace is how long the process group gets after go-task has gone, before the rest is
// taken.
//
// go-task exits the moment it is signalled, which is not the moment its commands are done
// reacting: a compose stack stopping containers is still working, and interrupting it is
// how you end up with half a stack. Long enough for that, short enough that stopping still
// feels like stopping.
const stopGrace = time.Second

// stop records whether this run was stopped rather than left to finish, shared with the
// capture goroutine.
//
// The capture goroutine is the only place a stopped run can be cleaned up from. Killing
// the group needs the leader's pid, and the leader stops being a safe thing to name the
// moment it is waited on — that pid goes back into circulation, and signalling a recycled
// one means signalling a stranger. Between EOF and that wait is the whole window, and it
// belongs to the goroutine that owns both.
type stop struct {
	// requested: something asked this run to stop.
	requested atomic.Bool
	// now: …and asked again, or is quitting: skip what is left of the grace.
	now atomic.Bool
}

// Stored is a finished run being rebuilt from the archive.
type Stored struct {
	// ID is the archive directory this came out of. Carried so a stored run can be told
	// apart from the archive entry it *is* — diffing a run against itself is a diff of
	// nothing, and finding that out by producing it is worse than not offering it.
	ID              string
	Root            string
	Args            []string
	Graph           graph.Graph
	Tasks           map[string]*TaskRun
	Order           []string
	Exit            int
	Duration        time.Duration
	RedactedSecrets int
}

type Run struct {
	Root string
	// Args are the extra argv passed after the task name — `NAME=backend`, `-- -p ingest`.
	Args []string
	// Interactive means it ran with `--output interleaved` so the task could ask questions.
	Interactive bool
	// Force means it ran with `--force`, ignoring go-task's up-to-date checks.
	Force bool
	Graph graph.Graph
	Tasks map[string]*TaskRun
	// Order lists tasks in the order they first produced output.
	Order []string
	// Exit is set once the process is gone.
	Exit    int
	HasExit bool
	// Started is when this run began. Public so a caller can tell one run from another.
	Started     time.Time
	Duration    time.Duration
	HasDuration bool
	// RedactedSecrets is the number of secrets being masked out of this run's output.
	RedactedSecrets int
	// Sent is what has been typed at the task, echoed back so you can see the keystroke
	// landed. Under `--output prefixed` a task may produce nothing for a long time after
	// answering, and without this there is no way to tell "sent and waiting" from "not
	// sent at all".
	Sent string

	// stored marks a run loaded from the archive rather than executed here.
	stored   bool
	storedID string

	mu     sync.Mutex
	proc   *os.Process
	master *os.File
	stop   stop
	// reapMu keeps the group signal and the wait on the leader from overlapping.
	reapMu sync.Mutex

	// reaped is set by the capture goroutine on its way out. Read from the escalation
	// goroutine, which is why it is an atomic rather than HasExit.
	reaped atomic.Bool

	cancelled bool
	// killed records whether the polite signals have already been sent and ignored. Kept
	// so a second `x` can escalate rather than sending a process the same signal it just
	// sat through.
	killed bool

	// provisional is where the not-yet-terminated line lives, so the next read replaces it
	// rather than stacking up a copy per 8KB chunk.
	provisional *provisionalLine
	lastOutput  time.Time
	active      string
	hasActive   bool
	events      *queue
	drained     bool
}

// queue is an unbounded event queue: the capture goroutine must never block on a UI that
// is between frames, and a bounded channel would eventually wedge the pty read — which
// wedges the child writing into it.
type queue struct {
	mu    sync.Mutex
	items []Event
}

func (q *queue) push(e Event) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
}

func (q *queue) drain() []Event {
	q.mu.Lock()
	out := q.items
	q.items = nil
	q.mu.Unlock()
	return out
}

type provisionalLine struct {
	task  string
	index int
}

// Start runs `task <root>` in dir. It returns immediately; call Poll to drain.
//
// interactive swaps `--output prefixed` for `--output interleaved`, which is the only way
// a prompt ever reaches us: go-task's prefixer is itself line-based, so a `Proceed? (y/n) `
// with no newline is held inside it forever and the run just looks hung. Measured — under
// prefixed the prompt never appears at all.
//
// The cost is per-line attribution. Interleaved output still carries go-task's
// `task: [name] <cmd>` announcements, so lines are attributed to whichever task last
// spoke, which is correct for a sequential run and wrong under parallel `deps:`.
// Interactive runs are inherently sequential, so that trade is worth making — but only
// when asked for.
func Start(dir, root string, args []string, interactive, force bool) (*Run, error) {
	r := &Run{
		Root:        root,
		Args:        append([]string(nil), args...),
		Interactive: interactive,
		Force:       force,
		Graph:       graph.New(),
		Tasks:       map[string]*TaskRun{},
		Started:     time.Now(),
		lastOutput:  time.Now(),
		events:      &queue{},
	}

	go func() {
		// Resolve first: the tree should be on screen, greyed out, before any output
		// arrives to fill it in. The same call yields the environment dump the redactor is
		// built from, so masking is in place before the first line.
		redactor := redact.Empty()
		// A graph we could not resolve is not fatal — we still capture output, just
		// without the nesting. Redaction is then empty, which is why the run view says so
		// rather than implying output has been checked.
		g, summary := graph.ResolveDetailed(dir, root)
		if len(g.Edges) > 0 {
			redactor = redact.FromSummary(summary)
			r.send(GraphReady{Graph: g})
		}
		r.send(Redacting{N: redactor.Len()})

		if err := r.capture(dir, redactor); err != nil {
			r.send(LineEvent{Raw: fmt.Sprintf("taskui: could not start `task %s`: %v", root, err)})
			r.send(Exited{Code: -1})
		}
	}()

	return r, nil
}

func (r *Run) send(e Event) { r.events.push(e) }

func (r *Run) Finished() bool  { return r.HasExit }
func (r *Run) Cancelled() bool { return r.cancelled }

// Killed is true once SIGKILL has gone out. There is nothing louder left to try, so the UI
// stops offering to stop it harder.
func (r *Run) Killed() bool { return r.killed }

// IsStored is true when this Run came off disk rather than off a pty. The run view uses it
// to avoid implying a stored run is still doing something.
func (r *Run) IsStored() bool { return r.stored }

// StoredID is the archive id a stored run came from, and empty for a live one.
func (r *Run) StoredID() string { return r.storedID }

// TaskNames lists every task with a bucket, sorted — the deterministic stand-in for Rust's
// ordered map.
func (r *Run) TaskNames() []string {
	out := make([]string, 0, len(r.Tasks))
	for k := range r.Tasks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SendInput sends keystrokes to the running task. `wrangler` and friends ask questions;
// without this the only answer taskui can give is to kill them.
func (r *Run) SendInput(bytes []byte) bool {
	if r.Finished() {
		return false
	}
	r.mu.Lock()
	master := r.master
	r.mu.Unlock()
	if master == nil {
		return false
	}
	if _, err := master.Write(bytes); err != nil {
		return false
	}

	// Printable characters as themselves; control keys as something readable.
	var b strings.Builder
	b.WriteString(r.Sent)
	for _, c := range bytes {
		switch {
		case c == '\r' || c == '\n':
			b.WriteRune('⏎')
		case c == 0x7f:
			b.WriteRune('⌫')
		case c == 0x03:
			b.WriteString("^C")
		case c == 0x04:
			b.WriteString("^D")
		case c == '\t':
			b.WriteRune('⇥')
		case c >= 0x21 && c <= 0x7e, c == ' ':
			b.WriteByte(c)
		default:
			b.WriteRune('·')
		}
	}
	// Only the tail matters; this is a receipt, not a transcript.
	sent := []rune(b.String())
	if len(sent) > 40 {
		sent = sent[len(sent)-40:]
	}
	r.Sent = string(sent)
	return true
}

// SilentFor is how long the task has produced nothing.
//
// A run under `--output prefixed` that is blocked on a prompt looks exactly like one that
// is slow: go-task's prefixer holds the unterminated question, so nothing arrives. Silence
// is the only signal there is.
func (r *Run) SilentFor() time.Duration { return time.Since(r.lastOutput) }

// PendingPrompt is the unterminated tail, if the task is sitting on one.
func (r *Run) PendingPrompt() (string, bool) {
	if r.provisional == nil {
		return "", false
	}
	t, ok := r.Tasks[r.provisional.task]
	if !ok || r.provisional.index >= len(t.Lines) {
		return "", false
	}
	text := strings.TrimRight(t.Lines[r.provisional.index].Plain, "\r")
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// LooksLikeAPrompt guesses whether the tail is a question. Used only to nudge the user
// toward the input key — a wrong guess costs nothing but a missing hint.
func (r *Run) LooksLikeAPrompt() bool {
	text, ok := r.PendingPrompt()
	if !ok {
		return false
	}
	t := strings.TrimRight(text, " \t")
	lower := strings.ToLower(t)
	return strings.HasSuffix(t, "?") ||
		strings.HasSuffix(t, ":") ||
		strings.HasSuffix(t, ">") ||
		strings.HasSuffix(t, ")") ||
		strings.Contains(lower, "(y/n)") ||
		strings.Contains(lower, "[y/n]")
}

// Cancel stops the run, politely.
//
// Killing the `task` process alone is not enough: it is the shell commands beneath it that
// are doing the work, and they survive it — verified by killing taskui mid-run and
// watching `sleep` carry on. The pty puts the child in its own session, so the whole group
// can be signalled at once, which is what actually reaps the tree.
//
// Both signals sent here are catchable, deliberately: `docker compose up` takes SIGTERM as
// "stop the containers", and skipping that would leave the stack running while taskui
// reported it stopped. What happens when they are ignored is Kill's problem.
func (r *Run) Cancel() {
	if r.Finished() {
		return
	}
	r.cancelled = true
	// Tell the capture goroutine this was a stop, not an ending. It is what turns the
	// group's survivors into its problem rather than nobody's.
	r.stop.requested.Store(true)
	r.mu.Lock()
	proc := r.proc
	r.mu.Unlock()
	if proc != nil {
		// SIGTERM first so the tools get to clean up after themselves.
		_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)
		// SIGHUP is the backstop for a child that never became a group leader — and is
		// still catchable, which is the point.
		_ = proc.Signal(syscall.SIGHUP)
		go r.reapGroup()
	}
}

// reapGroup takes what is left of the process group once the grace is up.
//
// This runs on its own goroutine rather than after the capture loop, and that is the whole
// point. go-task catches both polite signals and then waits for its commands, so a command
// that ignores signals keeps it alive; go-task is the session leader holding the pty; and
// while anything in the group holds the pty slave open, the master never reaches EOF and
// the capture goroutine stays blocked in its read. Hanging the cleanup off the end of that
// read meant the cleanup could only run once the thing it was cleaning up had already let
// go.
//
// macOS hid this. BSD revokes the controlling terminal when the session leader dies, so
// taking the leader was enough to force the EOF and everything downstream ran. Linux does
// not revoke, so the same run simply never ended — which is exactly what
// `a command that ignores SIGTERM does not outlive the run` was written to catch, and did,
// on the first CI run that put it on Linux.
//
// Killing the group is what frees the pty on both, so the read ends because the run is
// over rather than the run ending because the read did.
func (r *Run) reapGroup() {
	// go-task exits the instant it is signalled; its commands are still reacting. Wait out
	// the grace before insisting, unless somebody already has.
	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) && !r.stop.now.Load() {
		if r.reaped.Load() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Under the same lock the capture goroutine waits on, so the group can never be
	// signalled after the leader has been reaped: that pid goes straight back into
	// circulation, and signalling a recycled one means signalling a stranger.
	r.reapMu.Lock()
	defer r.reapMu.Unlock()
	if r.reaped.Load() {
		return
	}
	r.mu.Lock()
	proc := r.proc
	r.mu.Unlock()
	if proc != nil {
		_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
		_ = proc.Signal(syscall.SIGKILL)
	}
}

// Kill insists, and stops waiting about it.
//
// SIGTERM and SIGHUP can both be caught, and plenty of things catch them: a shell script
// with a `trap`, a runtime waiting on a network call that is never going to return.
// SIGKILL cannot be, so it is what is left when the polite ones have been sent and the
// process is still there.
//
// Not what stopping does first. SIGKILL runs no cleanup handler, which on a compose stack
// means the containers stay up with nothing left to take them down — so this is a second,
// deliberate press rather than the opening move.
func (r *Run) Kill() {
	r.cancelled = true
	r.killed = true
	// Set both: a Kill that arrives before anything was asked politely still has to leave
	// the capture goroutine a stopped run to clean up after.
	r.stop.requested.Store(true)
	r.stop.now.Store(true)
	if r.Finished() {
		// The capture goroutine has already reaped the group on its way out, and the pid
		// that named it belongs to somebody else by now. Setting the flags above is all
		// there is left to do — signalling anything here would be signalling a stranger.
		return
	}
	// `now` is already set, so this takes the group immediately rather than waiting out a
	// grace nobody asked for a second time.
	go r.reapGroup()
}

// Command is what was actually invoked, for the header and the history list.
func (r *Run) Command() string {
	force := ""
	if r.Force {
		force = " --force"
	}
	if len(r.Args) == 0 {
		return "task " + r.Root + force
	}
	return "task " + r.Root + force + " " + task.JoinArgs(r.Args)
}

// FromStored rebuilds a finished run from the archive.
func FromStored(s Stored) *Run {
	return &Run{
		Root:            s.Root,
		Args:            s.Args,
		Graph:           s.Graph,
		Tasks:           s.Tasks,
		Order:           s.Order,
		Exit:            s.Exit,
		HasExit:         true,
		Started:         time.Now(),
		Duration:        s.Duration,
		HasDuration:     true,
		RedactedSecrets: s.RedactedSecrets,
		stored:          true,
		storedID:        s.ID,
		lastOutput:      time.Now(),
	}
}

// Detached builds a Run with no child process behind it, for tests that exercise state
// rather than capture.
func Detached(root string, g graph.Graph) *Run {
	tasks := map[string]*TaskRun{}
	for _, name := range g.Reachable(root) {
		tasks[name] = newTaskRun()
	}
	return &Run{
		Root:       root,
		Graph:      g,
		Tasks:      tasks,
		Started:    time.Now(),
		lastOutput: time.Now(),
	}
}

// Feed pushes one line at a task, as a capture would.
func (r *Run) Feed(task, text string) {
	r.apply(LineEvent{Task: task, Raw: text})
}

// ApplyFailed marks a task as the reported failure.
func (r *Run) ApplyFailed(task string) { r.apply(FailedEvent{Task: task}) }

// Finish ends the run with an exit code.
func (r *Run) Finish(exit int) { r.apply(Exited{Code: exit}) }

// Apply hands an event straight to the state machine, bypassing the channel.
func (r *Run) Apply(e Event) { r.apply(e) }

// Edge is one parent and the tasks it invokes.
type Edge struct {
	Parent   string
	Children []string
}

// GraphFrom builds a graph from parent/children pairs.
func GraphFrom(edges ...Edge) graph.Graph {
	g := graph.New()
	for _, e := range edges {
		g.Edges[e.Parent] = e.Children
	}
	return g
}

// Poll drains whatever the capture goroutine has produced. It returns true if anything
// changed, so the UI can skip redrawing when nothing has.
func (r *Run) Poll() bool {
	if r.events == nil || r.drained {
		return false
	}
	// Drain first, then apply: apply may retire the queue.
	batch := r.events.drain()
	for _, e := range batch {
		r.apply(e)
	}
	return len(batch) > 0
}

// pushLine appends to a task's buffer, dropping the oldest block once it is full. It
// returns the index the line landed at.
//
// provisional holds an index into this same slice, so anything that shifts the slice has
// to shift that index with it. Without the fixup the next partial write edits whichever
// line has slid into that slot — output you are still reading, silently rewritten, with
// nothing on screen to say it happened.
func (r *Run) pushLine(task string, line Line) (int, bool) {
	t, ok := r.Tasks[task]
	if !ok {
		return 0, false
	}
	if len(t.Lines) >= MaxLines {
		t.Lines = append([]Line(nil), t.Lines[DropBlock:]...)
		t.Dropped += DropBlock
		if r.provisional != nil && r.provisional.task == task {
			r.provisional.index = max(0, r.provisional.index-DropBlock)
		}
	}
	t.Lines = append(t.Lines, line)
	return len(t.Lines) - 1, true
}

func (r *Run) apply(event Event) {
	switch e := event.(type) {
	case Redacting:
		r.RedactedSecrets = e.N

	case GraphReady:
		for _, name := range e.Graph.Reachable(r.Root) {
			if _, ok := r.Tasks[name]; !ok {
				r.Tasks[name] = newTaskRun()
			}
		}
		r.Graph = e.Graph

	case Partial:
		r.lastOutput = time.Now()
		name := r.Root
		if r.hasActive {
			name = r.active
		}
		r.touch(name)
		if r.provisional != nil && r.provisional.task == name {
			// Same unterminated line growing: replace it in place.
			if t, ok := r.Tasks[name]; ok && r.provisional.index < len(t.Lines) {
				t.Lines[r.provisional.index] = newLine(e.Text, false)
			}
			return
		}
		if index, ok := r.pushLine(name, newLine(e.Text, false)); ok {
			r.provisional = &provisionalLine{task: name, index: index}
		}

	case LineEvent:
		r.lastOutput = time.Now()
		// Untagged, with nothing running yet: go-task's own errors — a missing `requires:`
		// var, an unknown task, a malformed Taskfile — are printed before any task starts,
		// so they carry no `[name]` prefix and there is no active task to inherit.
		// Dropping them left the user with an empty tree and a bare exit code.
		name := e.Task
		if name == "" {
			if r.hasActive {
				name = r.active
			} else {
				name = r.Root
			}
		}
		r.touch(name)
		// Before the provisional branch, which returns early: a line that supersedes an
		// unterminated one takes a different path to storage, and a check placed after that
		// branch reads only the lines that did not. Which is exactly what happened — the
		// first of two identical skips was explained and the second was not, purely by which
		// arrived whole.
		if task, why, ok := skipReason(ansi.Strip(e.Raw)); ok {
			r.apply(Skipping{Task: task, Why: why})
		}
		// A completed line supersedes the provisional one it grew out of.
		if r.provisional != nil && r.provisional.task == name {
			at := r.provisional.index
			r.provisional = nil
			if t, ok := r.Tasks[name]; ok && at < len(t.Lines) {
				t.Lines[at] = newLine(e.Raw, e.IsCommand)
				return
			}
		}
		r.pushLine(name, newLine(e.Raw, e.IsCommand))

	case FailedEvent:
		r.touch(e.Task)
		r.fail(e.Task)

	case Skipping:
		// Deliberately does not touch(): a task go-task decided not to run has not started,
		// and opening it would put it in Order as though it had.
		if t, ok := r.Tasks[e.Task]; ok {
			t.Note = e.Why
		}

	case Exited:
		r.provisional = nil
		r.Exit = e.Code
		r.HasExit = true
		r.Duration = time.Since(r.Started)
		r.HasDuration = true
		r.settle(e.Code)
		r.drained = true
	}
}

// touch notes that name is producing output now, and closes out anything that was running
// and is not an ancestor of it.
func (r *Run) touch(name string) {
	if r.hasActive && r.active == name {
		return
	}

	ancestors := r.ancestorsOf(name)
	now := time.Now()
	for other, t := range r.Tasks {
		if t.Status == Running && other != name && !ancestors[other] {
			// A parent stays Running while its children work; a sibling that has stopped
			// producing output has finished.
			t.Status = Ok
			t.close(now)
		}
	}

	// Open the whole chain, not just the task itself. An aggregate like `lint` whose
	// commands are all `task:` invocations never produces a line tagged with its own name —
	// every line belongs to a child — so it would otherwise look as though it never ran.
	// Ordered from the root down so Order reads top-down.
	var chain []string
	for _, t := range r.Graph.Reachable(r.Root) {
		if ancestors[t] {
			chain = append(chain, t)
		}
	}
	chain = append(chain, name)

	for _, task := range chain {
		// The graph is built from the invoked root, but go-task can report a task we never
		// saw — an alias, or a path `--summary` did not reveal.
		entry, ok := r.Tasks[task]
		if !ok {
			entry = newTaskRun()
			r.Tasks[task] = entry
		}
		if entry.started.IsZero() {
			entry.started = now
			r.Order = append(r.Order, task)
		}
		if entry.Status == Pending {
			entry.Status = Running
		}
	}
	r.active = name
	r.hasActive = true
}

func (t *TaskRun) close(now time.Time) {
	if !t.started.IsZero() {
		t.duration = now.Sub(t.started)
		t.settled = true
	}
}

func (r *Run) ancestorsOf(name string) map[string]bool {
	out := map[string]bool{}
	r.collectAncestors(name, out)
	return out
}

func (r *Run) collectAncestors(name string, out map[string]bool) {
	for _, parent := range r.Graph.Names() {
		for _, c := range r.Graph.Edges[parent] {
			if c != name {
				continue
			}
			if !out[parent] {
				out[parent] = true
				// Walk up. Depth here is tiny, so the repeated scan is fine.
				r.collectAncestors(parent, out)
			}
			break
		}
	}
}

func (r *Run) fail(name string) {
	now := time.Now()
	if t, ok := r.Tasks[name]; ok {
		t.Status = Failed
		t.close(now)
	}
	// A failing task fails everything that invoked it.
	for parent := range r.ancestorsOf(name) {
		if t, ok := r.Tasks[parent]; ok {
			t.Status = Failed
			t.close(now)
		}
	}
}

// settle resolves whatever is still Running once the process is gone.
func (r *Run) settle(exit int) {
	now := time.Now()
	for _, t := range r.Tasks {
		switch t.Status {
		case Running:
			// Only call it good if the run itself succeeded; on a failure with no named
			// culprit, an unfinished task is not a pass.
			if exit == 0 {
				t.Status = Ok
			} else {
				t.Status = Failed
			}
			t.close(now)
		case Pending:
			// Never produced a line and never opened as an ancestor: not reached.
			t.Status = Skipped
		default:
			// Already settled one way or the other.
		}
	}
}

// capture drives `task --output prefixed <root>` on a pty and streams parsed events,
// blocking until the child exits.
func (r *Run) capture(dir string, redactor *redact.Redactor) error {
	mode := "prefixed"
	if r.Interactive {
		mode = "interleaved"
	}
	argv := []string{"--output", mode, r.Root}
	// `--force` before the user's own arguments: theirs may include a `--` separator,
	// after which everything is CLI_ARGS rather than a flag.
	if r.Force {
		argv = append(argv, "--force")
	}
	// Passed through verbatim, already split shell-style: `--` and `NAME=value` are just
	// argv entries to go-task.
	argv = append(argv, r.Args...)

	cmd := exec.Command("task", argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		// Commands run behind go-task's prefixing pipe, so tty detection fails for them.
		// These are the escape hatches the major toolchains honour regardless of tty.
		"CARGO_TERM_COLOR=always", // cargo, clippy
		"CLICOLOR_FORCE=1",        // git, BSD coreutils
		"FORCE_COLOR=1",           // the node ecosystem
	)

	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: 50,
		// Wide, so tools that wrap to the terminal width do not hard-wrap the capture at
		// something narrow. The UI wraps for display instead.
		Cols: 200,
	})
	if err != nil {
		return err
	}

	// Publish the handles before reading, so a cancel arriving immediately still lands.
	r.mu.Lock()
	r.proc = cmd.Process
	r.master = master
	r.mu.Unlock()

	buf := make([]byte, 8192)
	var pending []byte
	for {
		n, err := master.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			// A pty gives us CRLF; split on LF and drop the CR.
			for {
				at := indexByte(pending, '\n')
				if at < 0 {
					break
				}
				line := pending[:at]
				pending = pending[at+1:]
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				// Mask here, at the boundary: nothing unredacted is ever put on the
				// channel, so no later code path can leak what it never received.
				text := redactor.Redact(applyOverwrites(string(line)))
				for _, event := range parseLine(text) {
					r.send(event)
				}
			}

			// Whatever is left has no newline yet. Emit it anyway: a prompt never gets
			// one, and waiting for it means the run looks hung.
			if len(pending) > 0 {
				r.send(Partial{Text: redactor.Redact(applyOverwrites(string(pending)))})
			}
		}
		if err != nil {
			break
		}
	}

	// The pty is at EOF, which means nothing is holding the slave open any more: go-task
	// has gone, and so has anything it left behind that was attached to the terminal. A
	// run that was stopped got there because reapGroup took the group; one that ended on
	// its own got there by finishing.
	//
	// Reaping under the same lock reapGroup uses is what keeps the two apart. A pid names
	// its group only until it is waited on; after that it names whatever the OS hands it
	// to next, and a signal arriving in that window would go to a stranger.
	code := -1
	r.reapMu.Lock()
	if err := cmd.Wait(); err == nil {
		code = 0
	} else if state := cmd.ProcessState; state != nil {
		code = state.ExitCode()
	}
	r.reaped.Store(true)
	r.reapMu.Unlock()

	_ = master.Close()
	r.send(Exited{Code: code})
	return nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// applyOverwrites applies in-place overwrite semantics: carriage return and backspace.
//
// Progress indicators redraw without a newline. `cargo` and downloaders use `\r` to return
// to column zero; npm and npx spinners use `\b` to rub out the previous frame. Kept
// verbatim, one "line" arrives as `10%\r50%\r100%` or `\|/-\|/-Need to install`, which
// renders as control characters, wraps into several rows, and lets a search match a state
// that was never the final answer.
//
// This is not terminal emulation: a short write over a longer one leaves the old tail
// visible on a real terminal and does not here. For progress output that is invisible, and
// the alternative is a column-tracking screen buffer for a cosmetic edge case.
func applyOverwrites(text string) string {
	if !strings.ContainsAny(text, "\r\b") {
		return text
	}
	var out []rune
	// A line ending in `\r` has not been overwritten by anything, so the text before it
	// still stands; without this, "done\r" would come out empty.
	var lastWritten []rune
	for _, c := range text {
		switch c {
		case '\r':
			if len(out) > 0 {
				lastWritten = out
			}
			out = nil
		case '\b':
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return string(lastWritten)
	}
	return string(out)
}

// parseLine turns one line of `task --output prefixed` into events.
//
// Three shapes matter:
//
//	`task: [name] cmd`  go-task echoing the command it is about to run
//	`[name] output`     an output line, tagged by `--output prefixed`
//	`task: Failed to run task "name": …`
func parseLine(text string) []Event {
	stripped := ansi.Strip(text)

	if rest, ok := strings.CutPrefix(stripped, "task: ["); ok {
		if name, _, ok := strings.Cut(rest, "] "); ok {
			return []Event{LineEvent{Task: name, Raw: text, IsCommand: true}}
		}
	}

	if strings.HasPrefix(stripped, "task: ") {
		// `task: Failed to run task "agg": task: Failed to run task "c": exit status 3` —
		// the innermost name is the one that actually failed.
		culprit := ""
		rest := stripped
		const needle = `Failed to run task "`
		for {
			i := strings.Index(rest, needle)
			if i < 0 {
				break
			}
			rest = rest[i+len(needle):]
			if end := strings.Index(rest, `"`); end >= 0 {
				culprit = rest[:end]
			}
		}
		var events []Event
		if culprit != "" {
			events = append(events, FailedEvent{Task: culprit})
		}
		return append(events, LineEvent{Raw: text})
	}

	if rest, ok := strings.CutPrefix(stripped, "["); ok {
		if name, _, ok := strings.Cut(rest, "] "); ok {
			// Trust the tag only if it looks like a task name — output that happens to
			// start with `[` should not invent a task.
			if name != "" && isTaskName(name) {
				raw := text
				if _, after, ok := strings.Cut(text, "] "); ok {
					raw = after
				}
				return []Event{LineEvent{Task: name, Raw: raw}}
			}
		}
	}

	return []Event{LineEvent{Raw: text}}
}

func isTaskName(name string) bool {
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune(":-_.", c) {
			return false
		}
	}
	return true
}

// Skipping records why go-task declined to run a task.
type Skipping struct {
	Task string
	Why  string
}

func (Skipping) event() {}

// go-task's own announcements. It names the task in the message and gives the line no
// `[name]` prefix, so without this the reason and the task it is about end up in different
// places on screen.
var (
	upToDateNotice = regexp.MustCompile(`^task: Task "([^"]+)" is up to date$`)
	// Unanchored, and the *last* match is the one that counts: a failure inside an
	// aggregate is reported nested — `Failed to run task "all": task: Failed to run task
	// "guarded": task: precondition not met` — and the task that actually has the
	// unsatisfied precondition is the innermost one, not the one that invoked it.
	preconditionNotice = regexp.MustCompile(`Failed to run task "([^"]+)": task: precondition not met`)
)

// skipReason reads one line of output for an announcement that a task was not run.
//
// Trimmed first, and that is not cosmetic: the pty delivers CRLF, so a line arrives with a
// trailing carriage return and an anchored match quietly fails on it. Which it did — the
// first of two consecutive skips was explained and the second was not, because only the
// second carried the CR.
func skipReason(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if m := upToDateNotice.FindStringSubmatch(text); m != nil {
		return m[1], "up to date", true
	}
	if all := preconditionNotice.FindAllStringSubmatch(text, -1); len(all) > 0 {
		return all[len(all)-1][1], "precondition not met", true
	}
	return "", "", false
}

// SetDurationForTest pins a task's settled duration, so a test can describe a run's shape
// without depending on how long the test itself took.
func (t *TaskRun) SetDurationForTest(d time.Duration) {
	t.duration = d
	t.settled = true
}
