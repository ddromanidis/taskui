// Package events is taskui's run, addressed to another program.
//
// Two callers, one shape. `--run <task> --json` writes the stream to stdout and exits with
// the run; `--events <path>` writes it from inside the TUI to a socket somebody else is
// listening on, so an editor hosting the terminal can fill a quickfix list, colour a
// statusline and open the file a failure named — without reimplementing any of it.
//
// The events are differences against a run's own state rather than the engine's internal
// event stream. That is deliberate: [github.com/ddromanidis/taskui/internal/run.Run.Poll]
// folds raw events into state — a line arriving is also a task starting — and a consumer
// wants the folded version. "Task went from pending to running" has no event of its own and
// is exactly what a front end draws.
package events

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"

	"github.com/ddromanidis/taskui/internal/run"
)

// The events, one JSON object per line. Separate types rather than one struct with
// everything in it: a protocol whose every line carries `"secrets": 0` is a protocol nobody
// enjoys reading, and `omitempty` on a line index would drop the first line of every task.
type (
	// RunStarted opens a run's events. Everything after it that names this root belongs to
	// this run, until Exit.
	RunStarted struct {
		Type    string   `json:"type"` // "run"
		Root    string   `json:"root"`
		Dir     string   `json:"dir"`
		Args    []string `json:"args,omitempty"`
		Started int64    `json:"started_unix"`
	}
	// Graph is the execution graph, sent once, as soon as it is resolved.
	Graph struct {
		Type  string              `json:"type"` // "graph"
		Root  string              `json:"root"`
		Edges map[string][]string `json:"edges"`
	}
	// Task is a task's status changing, and its duration once that is known.
	Task struct {
		Type string `json:"type"` // "task"
		Root string `json:"root"`
		Name string `json:"name"`
		// Status is Pending, Running, Ok, Failed or Skipped — the names the archive uses.
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	// Line is one line of output.
	Line struct {
		Type string `json:"type"` // "line"
		Root string `json:"root"`
		Task string `json:"task"`
		// Index counts from the start of the run, not from the start of the buffer: a task
		// that outlives the 20,000-line cap drops lines off the front, and an index that
		// slid with them would renumber everything a consumer had already stored.
		Index   int    `json:"index"`
		Text    string `json:"text"`
		Command bool   `json:"command,omitempty"`
	}
	// Prompt is a task waiting to be answered, which a host may want to say out loud: a
	// run that appears to hang is usually one asking a question.
	Prompt struct {
		Type string `json:"type"` // "prompt"
		Root string `json:"root"`
		Text string `json:"text"`
	}
	// Exit ends a run.
	Exit struct {
		Type       string `json:"type"` // "exit"
		Root       string `json:"root"`
		Code       int    `json:"code"`
		DurationMs int64  `json:"duration_ms"`
		Saved      string `json:"saved,omitempty"`
		Secrets    int    `json:"redacted_secrets,omitempty"`
	}
	// Edit is `e` pressed on a location. A host editor opens it; without one, taskui
	// launches $EDITOR itself.
	Edit struct {
		Type string `json:"type"` // "edit"
		Path string `json:"path"`
		Line int    `json:"line"`
		Col  int    `json:"col,omitempty"`
		// Note is why this is not quite where you asked for — an ambiguous name resolved
		// to a guess, a fallback to the task's first location.
		Note string `json:"note,omitempty"`
	}
)

// Sink is where events go. A nil Sink is a working Sink that drops everything, so a caller
// with no host attached needs no branch of its own.
type Sink struct {
	mu  sync.Mutex
	w   io.WriteCloser
	enc *json.Encoder
	// Lines says whether output lines go out too. The `--json` stream sends them, because
	// its consumer is drawing the run; a TUI with a host attached does not, because the
	// terminal in front of you is already showing them.
	Lines bool
}

// Open dials a destination for events.
//
// A unix socket first, which is what a host listening for them offers: a socket says when
// the other end went away, needs no cleanup, and cannot be read half-written. Failing that
// the path is opened as a file, appended to, which is what a shell redirection and a test
// both want.
func Open(path string) (*Sink, error) {
	if path == "" {
		return nil, errors.New("no path to report events to")
	}
	if conn, err := net.Dial("unix", path); err == nil {
		return New(conn), nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return New(f), nil
}

// New wraps an already-open destination.
func New(w io.WriteCloser) *Sink {
	return &Sink{w: w, enc: json.NewEncoder(w)}
}

// Send writes one event. Errors are dropped on purpose: a host that closed the socket has
// stopped caring, and a run must not fail because nobody is listening any more.
func (s *Sink) Send(event any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(event)
}

// Close releases the destination.
func (s *Sink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Close()
}

// Deltas remembers what has already gone out about one run, so each poll sends only what is
// new. One per run: two runs at once are two of these, which is what keeps a busy slot from
// renumbering a quiet one.
type Deltas struct {
	// seen is how many lines of each task have been sent, counted from the start of the
	// run so that lines dropped off the front of the buffer do not renumber anything.
	seen   map[string]int
	said   map[string]taskState
	graph  bool
	opened bool
	closed bool
	prompt string
}

// taskState is what was last said about a task. The duration is part of it because it
// arrives after the status does — a task is Ok some milliseconds before the clock settles —
// and a consumer told `Ok` with no duration would never hear the figure.
type taskState struct {
	status     run.Status
	durationMs int64
}

// NewDeltas starts tracking a run.
func NewDeltas() *Deltas {
	return &Deltas{seen: map[string]int{}, said: map[string]taskState{}}
}

// Start sends the run's opening event, once.
func (d *Deltas) Start(s *Sink, r *run.Run, dir string) {
	if d.opened {
		return
	}
	d.opened = true
	s.Send(RunStarted{
		Type: "run", Root: r.Root, Dir: dir,
		Args: r.Args, Started: r.Started.Unix(),
	})
}

// Flush sends everything that has changed since the last call.
func (d *Deltas) Flush(s *Sink, r *run.Run) {
	if s == nil || r == nil {
		return
	}
	if !d.graph && len(r.Graph.Edges) > 0 {
		d.graph = true
		s.Send(Graph{Type: "graph", Root: r.Root, Edges: r.Graph.Edges})
	}

	for _, name := range r.TaskNames() {
		task := r.Tasks[name]
		if task == nil || name == "" {
			continue
		}
		now := taskState{status: task.Status, durationMs: task.Duration().Milliseconds()}
		was, known := d.said[name]
		changed := !known || was != now
		announce := func() {
			d.said[name] = now
			s.Send(Task{
				Type: "task", Root: r.Root, Name: name, Status: now.status.String(),
				DurationMs: now.durationMs, Note: task.Note,
			})
		}

		// A poll is a batch, so the order within one is chosen rather than observed. A task
		// that has started is announced before its output and one that has finished after
		// it, which is the order the two actually happen in — a consumer never sees `Ok`
		// followed by more lines from the same task.
		if changed && !settled(now.status) {
			announce()
			changed = false
		}
		if s.Lines {
			// Where the sent count lands in the buffer as it stands now. Negative means
			// the lines it referred to have been dropped, so start from what is left.
			at := max(0, d.seen[name]-task.Dropped)
			for i := at; i < len(task.Lines); i++ {
				s.Send(Line{
					Type: "line", Root: r.Root, Task: name, Index: task.Dropped + i,
					// A command echo goes out as the command: `command: true` and `task`
					// already carry what the `task: [name] ` prefix was there to say, and
					// a consumer that had to strip it is one that would forget to.
					Text: run.CommandText(task.Lines[i]), Command: task.Lines[i].IsCommand,
				})
			}
			d.seen[name] = task.Dropped + len(task.Lines)
		}
		if changed {
			announce()
		}
	}

	if text, waiting := r.PendingPrompt(); waiting && text != d.prompt {
		d.prompt = text
		s.Send(Prompt{Type: "prompt", Root: r.Root, Text: text})
	}
}

// Finish sends the run's closing event, once. saved is where it was archived, if it was.
func (d *Deltas) Finish(s *Sink, r *run.Run, saved string) {
	if d.closed || s == nil || r == nil {
		return
	}
	d.closed = true
	code := -1
	if r.HasExit {
		code = r.Exit
	}
	s.Send(Exit{
		Type: "exit", Root: r.Root, Code: code,
		DurationMs: r.Duration.Milliseconds(),
		Saved:      saved, Secrets: r.RedactedSecrets,
	})
}

// Done reports whether this run's closing event has gone out.
func (d *Deltas) Done() bool { return d.closed }

// settled reports whether a status is one a task does not come back from.
func settled(s run.Status) bool {
	return s == run.Ok || s == run.Failed || s == run.Skipped
}
