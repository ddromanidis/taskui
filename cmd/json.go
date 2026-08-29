package cmd

// `--json` is the machine-readable form of the print-and-exit commands, for a front end
// that is not this terminal — an editor plugin, a CI step, a dashboard.
//
// The engine is already separate from the screen: `internal/run`, `store`, `task` and the
// rest know nothing about Bubble Tea. So this is not a second implementation of anything.
// It is the same run, the same archive and the same discovery, addressed to a program.
//
// Three shapes, each pairing with a flag that already existed:
//
//   - `--list --json` — the tasks, with what the archive knows about how each went
//   - `--run <task> --json` — the run as newline-delimited JSON, one event per line, ending
//     when the run does
//   - `--timeline <task> --json` — one task's stored runs
//
// The streaming form is deliberately one process per run rather than a long-lived daemon.
// The archive on disk is already the shared state, so separate processes see each other's
// history for free, and a caller that wants several runs at once has a process table to
// multiplex them with.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
)

// eventTask names the event a task's status change goes out as. It shares its spelling
// with the `--task` flag and nothing else: one is a kind of event, the other is a flag,
// and a constant they both used would be a coincidence pretending to be a decision.
const eventTask = "task"

// listing is `--list --json`.
type listing struct {
	Project string     `json:"project"`
	Tasks   []taskJSON `json:"tasks"`
}

type taskJSON struct {
	// Name is the full colon path, which is what every other command takes as an argument.
	Name      string   `json:"name"`
	Desc      string   `json:"desc,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	Dangerous bool     `json:"dangerous,omitempty"`
	// Taskfile and Line are where it is written, for a caller that wants to open it.
	Taskfile string `json:"taskfile,omitempty"`
	Line     int    `json:"line,omitempty"`
	UpToDate bool   `json:"up_to_date,omitempty"`
	// Last is how the archive says it went, absent when it has never run here.
	Last *outcomeJSON `json:"last,omitempty"`
}

type outcomeJSON struct {
	Ok   bool  `json:"ok"`
	When int64 `json:"when_unix"`
}

// printTaskList is `--list --json`.
//
// The locations and up-to-date flags come from `task --list-all --json`, which is slow
// enough on a workspace full of `sources:` globs that the TUI fetches it in the background.
// Here there is no frame to get out of the way of, so it is waited for — and if the call
// fails, the listing goes out without those two fields rather than not at all.
func printTaskList(out io.Writer, root string, tasks []task.Task) error {
	outcomes := store.LastOutcomes(store.StateDir(), root)
	details, err := task.Details(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskui: %v\n", err)
	}

	page := listing{Project: root, Tasks: make([]taskJSON, 0, len(tasks))}
	for _, t := range tasks {
		entry := taskJSON{
			Name:      t.Name,
			Desc:      t.Desc,
			Aliases:   t.Aliases,
			Dangerous: t.Dangerous,
		}
		if d, ok := details[t.Name]; ok {
			entry.Taskfile, entry.Line, entry.UpToDate = d.Where.File, d.Where.Line, d.UpToDate
		}
		if o, ok := outcomes[t.Name]; ok {
			entry.Last = &outcomeJSON{Ok: o.Ok, When: o.WhenUnix}
		}
		page.Tasks = append(page.Tasks, entry)
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

// printTimelineJSON is `--timeline <task> --json`: the same points the TUI's timeline draws.
func printTimelineJSON(out io.Writer, root, taskName string) error {
	points := store.Timeline(store.StateDir(), root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	type pointJSON struct {
		RunID      string `json:"run_id"`
		Root       string `json:"root"`
		When       int64  `json:"when_unix"`
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms"`
		Lines      int    `json:"lines"`
		Commit     string `json:"commit,omitempty"`
	}
	page := make([]pointJSON, 0, len(points))
	for _, p := range points {
		page = append(page, pointJSON{
			RunID: p.RunID, Root: p.Root, When: p.WhenUnix,
			Status: p.Status, DurationMs: p.DurationMs, Lines: p.Lines, Commit: p.Commit,
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

// --- the run stream ---------------------------------------------------------------------

// The events, one JSON object per line. Separate types rather than one struct with
// everything in it: a protocol whose every line carries `"secrets": 0` is a protocol nobody
// enjoys reading, and `omitempty` on a line index would drop the first line of every task.
type (
	runStarted struct {
		Type    string   `json:"type"` // "run"
		Root    string   `json:"root"`
		Dir     string   `json:"dir"`
		Args    []string `json:"args,omitempty"`
		Started int64    `json:"started_unix"`
	}
	graphEvent struct {
		Type  string              `json:"type"` // "graph"
		Edges map[string][]string `json:"edges"`
	}
	taskEvent struct {
		Type string `json:"type"` // "task"
		Name string `json:"name"`
		// Status is Pending, Running, Ok, Failed or Skipped — the names the archive uses.
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	lineEvent struct {
		Type string `json:"type"` // "line"
		Task string `json:"task"`
		// Index counts from the start of the run, not from the start of the buffer: a task
		// that outlives the 20,000-line cap drops lines off the front, and an index that
		// slid with them would renumber everything a consumer had already stored.
		Index   int    `json:"index"`
		Text    string `json:"text"`
		Command bool   `json:"command,omitempty"`
	}
	promptEvent struct {
		Type string `json:"type"` // "prompt"
		Text string `json:"text"`
	}
	exitEvent struct {
		Type       string `json:"type"` // "exit"
		Code       int    `json:"code"`
		DurationMs int64  `json:"duration_ms"`
		Saved      string `json:"saved,omitempty"`
		Secrets    int    `json:"redacted_secrets,omitempty"`
	}
)

// streamRun is `--run <task> --json`: the run as it happens, one event per line.
//
// The events are differences against the run's own state rather than the engine's internal
// event stream. That is deliberate: `Run.Poll` folds raw events into state — a line arriving
// is also a task starting — and a consumer wants the folded version. "Task went from
// pending to running" has no event of its own and is exactly what a front end draws.
func streamRun(out io.Writer, dir, target string, argv []string) error {
	r, err := run.Start(dir, target, argv, false, false)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	send := func(v any) {
		// A consumer that closed the pipe is a consumer that has stopped caring; the run
		// still gets stopped and saved below.
		_ = enc.Encode(v)
	}

	send(runStarted{
		Type: "run", Root: r.Root, Dir: dir,
		Args: argv, Started: r.Started.Unix(),
	})

	// A front end that started this stream stops it by signalling the process — `jobstop`
	// in Neovim, ^C at a shell. Without a handler that kills taskui and leaves `task` and
	// everything under it running, because the child is a session leader of its own: the
	// property that lets the group be reaped is the same one that lets it survive us.
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopping)
	go func() {
		if _, ok := <-stopping; ok {
			r.Cancel()
		}
	}()

	sender := &deltas{seen: map[string]int{}, said: map[string]taskState{}}
	for {
		r.Poll()
		sender.flush(r, send)
		if r.Finished() {
			break
		}
		if text, waiting := r.PendingPrompt(); waiting && text != sender.prompt {
			sender.prompt = text
			send(promptEvent{Type: "prompt", Text: text})
		}
		time.Sleep(20 * time.Millisecond)
	}
	// One more, for whatever landed between the last poll and the exit.
	r.Poll()
	sender.flush(r, send)

	exit := -1
	if r.HasExit {
		exit = r.Exit
	}
	// Saved like any other run: a run is a run whichever front end started it, and one that
	// left nothing behind could not be searched, diffed or timelined afterwards.
	saved, err := store.Save(store.StateDir(), dir, r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskui: not saved: %v\n", err)
	}
	send(exitEvent{
		Type: "exit", Code: exit,
		DurationMs: r.Duration.Milliseconds(),
		Saved:      saved, Secrets: r.RedactedSecrets,
	})
	if exit != 0 {
		return exitWith(exit)
	}
	return nil
}

// deltas remembers what has already gone out, so each poll sends only what is new.
type deltas struct {
	// seen is how many lines of each task have been sent, counted from the start of the
	// run so that lines dropped off the front of the buffer do not renumber anything.
	seen   map[string]int
	said   map[string]taskState
	graph  bool
	prompt string
}

// taskState is what was last said about a task. The duration is part of it because it
// arrives after the status does — a task is Ok some milliseconds before the clock settles —
// and a consumer that was told `Ok` with no duration would never hear the figure.
type taskState struct {
	status     run.Status
	durationMs int64
}

func (d *deltas) flush(r *run.Run, send func(any)) {
	if !d.graph && len(r.Graph.Edges) > 0 {
		d.graph = true
		send(graphEvent{Type: "graph", Edges: r.Graph.Edges})
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
			send(taskEvent{
				Type: eventTask, Name: name, Status: now.status.String(),
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
		// Where the sent count lands in the buffer as it stands now. Negative means the
		// lines it referred to have been dropped, so start from what is left.
		at := max(0, d.seen[name]-task.Dropped)
		for i := at; i < len(task.Lines); i++ {
			send(lineEvent{
				Type: "line", Task: name, Index: task.Dropped + i,
				// A command echo goes out as the command: `command: true` and `task`
				// already carry what the `task: [name] ` prefix was there to say, and a
				// consumer that had to strip it is a consumer that would forget to.
				Text: run.CommandText(task.Lines[i]), Command: task.Lines[i].IsCommand,
			})
		}
		d.seen[name] = task.Dropped + len(task.Lines)

		if changed {
			announce()
		}
	}
}

// settled reports whether a status is one a task does not come back from.
func settled(s run.Status) bool {
	return s == run.Ok || s == run.Failed || s == run.Skipped
}
