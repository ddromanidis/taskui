package app

import (
	"fmt"

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
	if l, at, found := a.firstLocationIn(task); found {
		a.openLocationFrom(l, fmt.Sprintf(" (from line %d of `%s`)", at+1, task))
		return
	}
	a.Status = "no file:line here — `" + task + "` did not print one"
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

// HasLocation reports whether a line has something `e` could open. The renderer asks per
// visible row, so this is syntax only — no filesystem, no editor lookup.
func (a *App) HasLocation(text string) bool {
	_, ok := loc.First(text)
	return ok
}
