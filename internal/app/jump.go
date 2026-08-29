package app

import (
	"fmt"
	"strings"

	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/events"
	"github.com/ddromanidis/taskui/internal/loc"
)

// resolver indexes the project lazily, and only once.
//
// Built on demand rather than in New: most sessions never press `e`, and walking the tree
// for a key nobody used is a cost paid by everyone to benefit no one.
func (a *App) resolver() *loc.Resolver {
	if a.locs == nil {
		a.locs = loc.NewResolver(a.Root)
	}
	return a.locs
}

// TakeEdit hands over the editor the last keypress asked for, and clears it.
//
// The key handlers cannot return a Bubble Tea command, so the intent is parked here and
// Update collects it. Storing the intent rather than the command is also what lets a test
// assert what would have been launched without launching anything.
func (a *App) TakeEdit() (loc.Editor, bool) {
	if a.pendingEdit == nil {
		return loc.Editor{}, false
	}
	e := *a.pendingEdit
	a.pendingEdit = nil
	return e, true
}

// EditUnderCursor opens whatever file the row under the cursor points at.
//
// The row is tried first and the task second. A failing Go test prints its assertion on one
// line and the `--- FAIL` on another, and pressing `e` on the wrong one of those two should
// not be a dead keystroke — so a row with no location of its own falls back to the first one
// its task printed, which for a compiler is the error and for a test runner is the first
// failing assertion. Both are the place you wanted to go.
func (a *App) EditUnderCursor() {
	text, task, ok := a.rowTextForEdit()
	if !ok {
		a.Status = "nothing here to open"
		return
	}
	if l, found := loc.First(text); found {
		a.openLocation(l)
		return
	}
	if l, note, found := a.fallbackLocation(task); found {
		a.openLocationFrom(l, note)
		return
	}
	if a.Screen == ScreenDiff {
		a.Status = "no file:line anywhere in this diff"
		return
	}
	a.Status = "no file:line here — `" + task + "` did not print one"
}

// fallbackLocation is where `e` goes when the row under the cursor names no file, and the
// note explaining where it came from.
//
// It has to be per screen because the two screens are looking at different things. The run
// view has the task's captured output in memory; a diff opened from a timeline does not —
// `a.Run` there is whatever happened to be open before, which is a different run or none at
// all, and reading it would answer a question about the wrong thing.
func (a *App) fallbackLocation(task string) (loc.Loc, string, bool) {
	if a.Screen == ScreenDiff {
		if l, at, ok := a.firstLocationInDiff(); ok {
			return l, fmt.Sprintf(" (from line %d of the diff)", at+1), true
		}
		return loc.Loc{}, "", false
	}
	if l, at, ok := a.firstLocationIn(task); ok {
		return l, fmt.Sprintf(" (from line %d of `%s`)", at+1, task), true
	}
	return loc.Loc{}, "", false
}

// firstLocationInDiff prefers the lines that arrived. In a diff, what is new is what you
// are there about — a location on a line both runs printed is the one that was already
// fine.
func (a *App) firstLocationInDiff() (loc.Loc, int, bool) {
	for _, want := range []bool{true, false} {
		for i, row := range a.DiffRows {
			if row.Gap || (row.Op == diff.Ins) != want {
				continue
			}
			if found, ok := loc.First(row.Text); ok {
				return found, i, true
			}
		}
	}
	return loc.Loc{}, 0, false
}

// rowTextForEdit is the text `e` should look in, the task it belongs to, and whether there
// is anything here at all.
func (a *App) rowTextForEdit() (string, string, bool) {
	if a.Screen == ScreenDiff {
		if a.DiffCursor < len(a.DiffRows) {
			row := a.DiffRows[a.DiffCursor]
			return row.Text, a.DiffOf, true
		}
		return "", "", false
	}
	if a.Run == nil || a.RunCursor >= len(a.RunRows) {
		return "", "", false
	}
	row := a.RunRows[a.RunCursor]
	if row.IsTask {
		return "", row.Name, true
	}
	if t, found := a.Run.Tasks[row.Task]; found && row.Index < len(t.Lines) {
		return t.Lines[row.Index].Plain, row.Task, true
	}
	return "", row.Task, true
}

// firstLocationIn is the first file location this task printed, and which line it was on.
func (a *App) firstLocationIn(task string) (loc.Loc, int, bool) {
	if a.Run == nil {
		return loc.Loc{}, 0, false
	}
	t, ok := a.Run.Tasks[task]
	if !ok {
		return loc.Loc{}, 0, false
	}
	for i, l := range t.Lines {
		if found, ok := loc.First(l.Plain); ok {
			return found, i, true
		}
	}
	return loc.Loc{}, 0, false
}

func (a *App) openLocation(l loc.Loc) { a.openLocationFrom(l, "") }

// openLocationFrom resolves a location and parks the editor command for Update to run.
//
// Every way this can fail says what it was trying to do. A key that silently does nothing
// is indistinguishable from a key that is broken, and this one has four separate ways of
// not working — the file is not there, the name is ambiguous, no editor is configured, or
// the editor is one whose line-number spelling is unknown.
func (a *App) openLocationFrom(l loc.Loc, note string) {
	where := fmt.Sprintf("%s:%d", l.Path, l.Line)
	abs, ambiguous, ok := a.resolver().Resolve(l.Path)
	if !ok {
		a.Status = where + " — no such file under " + baseName(a.Root) + note
		return
	}

	// With a host attached — an editor showing this terminal — the file is its to open.
	// Launching $EDITOR here would put a second editor inside the first one's window.
	if a.HasHost() {
		reason := note
		if ambiguous {
			reason += " — several files share that name"
		}
		a.events.Send(events.Edit{
			Type: "edit", Path: abs, Line: l.Line, Col: l.Col, Note: strings.TrimSpace(reason),
		})
		a.Status = fmt.Sprintf("opening %s:%d in the editor", relativeTo(a.Root, abs), l.Line) + reason
		return
	}

	editor, ok := loc.EditorFor(l, abs)
	if !ok {
		a.Status = "set $EDITOR or $VISUAL to open " + where
		return
	}
	a.pendingEdit = &editor

	a.Status = fmt.Sprintf("opening %s:%d in %s", relativeTo(a.Root, abs), l.Line, baseName(editor.Name))
	if ambiguous {
		a.Status += " — several files share that name"
	}
	a.Status += note
}

// locationsIn is what the renderer draws as links. Syntax only — the filesystem is not
// consulted until a key is actually pressed, because this runs on every visible row of
// every frame and the answer is only needed once.
func locationsIn(text string) []loc.Loc { return loc.All(text) }

// EditDefinition opens the Taskfile a task is written in, at its own line.
//
// The same key as the one that opens a `file:line` from output, because it is the same
// intent: take me to the thing on screen. In a run that is whatever the error named; in the
// picker there is no error, and the thing on screen is the task.
func (a *App) EditDefinition(name string) {
	if name == "" {
		a.Status = "nothing here to open — space folds it"
		return
	}
	where, ok := a.WhereIs(name)
	if !ok {
		// The listing arrives on a background goroutine and can take seconds on a workspace
		// with a lot of `sources:` globs. Saying which of the two it is beats a bare no.
		if a.Details == nil {
			a.Status = "still reading the Taskfile — try `e` again in a moment"
		} else {
			a.Status = "go-task did not say where `" + name + "` is defined"
		}
		return
	}
	a.openLocationFrom(loc.Loc{Path: where.File, Line: where.Line}, "")
}
