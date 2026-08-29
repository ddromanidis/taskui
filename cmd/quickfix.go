package cmd

// `--quickfix` prints a run's failures as absolute `file:line:col: message`, which is the
// one form every editor already knows how to walk.
//
// Nothing else can produce it. `go test` prints `order_test.go:88` relative to the
// directory the test ran in; an editor resolves that against its own working directory and
// the jump opens nothing. taskui knows which task printed the line and where that task ran,
// and `loc.Resolver` turns the reference into an absolute path — which it already did, for
// the `e` key. This is that answer, addressed to something other than a person:
//
//	set errorformat=%f:%l:%c:\ %m
//	:cexpr system('taskui --quickfix')

import (
	"fmt"
	"io"
	"strings"

	"github.com/ddromanidis/taskui/internal/loc"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

// printQuickfix is `--quickfix`: the most recent stored run of this project, as an error
// list. The newest run is what `--last` already means, and it is what you have just watched
// fail.
func printQuickfix(out io.Writer, root, only string) error {
	base := store.StateDir()
	for _, m := range store.List(base) {
		if m.Dir != root {
			continue
		}
		r, err := store.Load(base, m)
		if err != nil {
			return fmt.Errorf("reading run %s: %w", m.ID, err)
		}
		writeQuickfix(out, r, m.Dir, only)
		return nil
	}
	return fmt.Errorf("no stored runs for this project yet")
}

// writeQuickfix prints one run's failures and returns how many entries it wrote.
//
// Order is execution order with the failed tasks kept — a quickfix list is walked from the
// top, so the first entry should be the first thing that broke. Named a task with `--task`
// and its status stops mattering: you asked for that one.
func writeQuickfix(out io.Writer, r *run.Run, dir, only string) int {
	resolver := loc.NewResolver(dir)
	written := 0

	for _, name := range interesting(r, only) {
		task := r.Tasks[name]
		if task == nil {
			continue
		}
		for _, line := range task.Lines {
			// go-task's own echo of the command is structure, not a report. It routinely
			// names files — `go test ./order_test.go` — and every one of those would be an
			// entry pointing at a file nothing has complained about yet.
			if line.IsCommand {
				continue
			}
			for _, ref := range loc.All(line.Plain) {
				path, ambiguous, ok := resolver.Resolve(ref.Path)
				// Ambiguous is a guess, and a guess in a list you walk without looking is
				// worse than a shorter list: `]q` lands you in a file you have never seen
				// and you spend the next minute working out why.
				if !ok || ambiguous {
					continue
				}
				col := ref.Col
				if col == 0 {
					// Plenty of tools do not say. Column one is where a caller with no
					// column lands anyway, and `%c` has to be given a number.
					col = 1
				}
				if _, err := fmt.Fprintf(out, "%s:%d:%d: %s\n",
					path, ref.Line, col, message(line.Plain, ref, name)); err != nil {
					// Piping into `head` closes the pipe on us, which is not a failure.
					return written
				}
				written++
			}
		}
	}
	return written
}

// interesting is the tasks worth scanning: the one that was asked for, or the ones that
// failed.
//
// The fallback matters more than it looks. go-task reports the failure against a task by
// name, but a run can end non-zero with nothing marked — a shell that died before any task
// claimed the output, a failure printed by the root itself — and a `--quickfix` that
// answered "nothing" on a run you just watched fail would be useless exactly when it is
// wanted. So a failed run with no failed task offers everything it has.
func interesting(r *run.Run, only string) []string {
	if only != "" {
		return []string{only}
	}
	order := spoke(r)
	var failed []string
	for _, name := range order {
		if t := r.Tasks[name]; t != nil && t.Status == run.Failed {
			failed = append(failed, name)
		}
	}
	if len(failed) > 0 {
		return failed
	}
	if r.HasExit && r.Exit == 0 {
		return nil
	}
	return order
}

// spoke is the tasks that produced output, in the order they first produced it — which for
// a stored run is the order the manifest kept, and for a live one is the order things
// actually happened. A task that never said anything has no location to offer either way.
func spoke(r *run.Run) []string {
	out := make([]string, 0, len(r.Order))
	for _, name := range r.Order {
		// The empty bucket is where output no task claimed goes. It is not a task, and
		// naming it in an error list would be naming nothing.
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// message is the line with the reference cut out of it, because the path is already the
// first two columns of the entry and repeating it there costs the width the actual error
// wanted.
func message(text string, ref loc.Loc, task string) string {
	if ref.Start < 0 || ref.End > len(text) || ref.Start > ref.End {
		return strings.TrimSpace(text)
	}
	msg := strings.TrimSpace(text[:ref.Start] + text[ref.End:])
	msg = strings.TrimSpace(strings.TrimPrefix(msg, ":"))
	if msg == "" {
		// A bare reference on a line of its own — a stack frame, usually. The task name is
		// more use in the list than an empty column.
		return task
	}
	return msg
}
