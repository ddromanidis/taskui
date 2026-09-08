package app

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ddromanidis/taskui/internal/run"
)

// ctrl builds a control chord the way a terminal reports one, so these go through the same
// flattening a real keypress does rather than around it.
func ctrl(c rune) Key { return fromTea(tea.KeyPressMsg{Code: c, Mod: tea.ModCtrl}) }

// special builds one of the named keys — Home, PgDn and the rest.
func special(code rune) Key { return fromTea(tea.KeyPressMsg{Code: code}) }

// A run with enough output to have somewhere to move to.
func longRun(t *testing.T) *App {
	t.Helper()
	a := appAt(t, "backend:lint")
	a.Screen = ScreenRun
	a.OpenRunForTest(run.Detached("backend:lint", run.GraphFrom(run.Edge{Parent: "backend:lint"})))
	for i := range 40 {
		a.Run.Feed("backend:lint", fmt.Sprintf("line %d", i))
	}
	press(a, Char('O'))
	return a
}

// `^f` and `^b` move by what is actually on screen. PgDn and PgUp used to move by a
// hardcoded 15, which on a tall terminal was two thirds of a page and on a short one was
// four of them.
func TestAFullPageIsTheViewport(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	a.Viewport = 2
	if len(a.PickerRows) < 3 {
		t.Fatalf("%d rows is not enough to page through", len(a.PickerRows))
	}
	a.Cursor = 0

	press(a, ctrl('f'))
	if a.Cursor != 2 {
		t.Errorf("`^f` should move a viewport: cursor = %d, want 2", a.Cursor)
	}
	press(a, ctrl('b'))
	if a.Cursor != 0 {
		t.Errorf("`^b` should come back: cursor = %d", a.Cursor)
	}

	// The keys that say so do the same thing, rather than a different number of rows.
	press(a, special(tea.KeyPgDown))
	if a.Cursor != 2 {
		t.Errorf("PgDn should be `^f`: cursor = %d", a.Cursor)
	}
	press(a, special(tea.KeyPgUp))
	if a.Cursor != 0 {
		t.Errorf("PgUp should be `^b`: cursor = %d", a.Cursor)
	}
}

// The run view is the screen with the most rows to travel and the one that had no Home or
// End, because its copy of the movement block was the shortest.
func TestHomeAndEndReachTheRunView(t *testing.T) {
	a := longRun(t)
	last := len(a.RunRows) - 1
	if last <= 5 {
		t.Fatalf("%d rows is not enough to test a jump", last)
	}

	press(a, special(tea.KeyEnd))
	if a.RunCursor != last {
		t.Errorf("End should reach the last row: %d, want %d", a.RunCursor, last)
	}
	press(a, special(tea.KeyHome))
	if a.RunCursor != 0 {
		t.Errorf("Home should reach the first: %d", a.RunCursor)
	}
}

// `^d` on the run view, which had it, and on the `?` screen and the detail panel, which
// did not — the point of dispatching the motions once is that a screen cannot be missed.
func TestHalfPageReachesEveryScreen(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		a := longRun(t)
		a.Viewport = 10
		press(a, ctrl('d'))
		if a.RunCursor != 5 {
			t.Errorf("cursor = %d, want half of 10", a.RunCursor)
		}
	})

	t.Run("help", func(t *testing.T) {
		a := appAt(t, "backend:lint")
		press(a, Char('?'))
		a.Viewport = 10
		press(a, ctrl('d'))
		if a.HelpOffset != 5 {
			t.Errorf("offset = %d, want half of 10", a.HelpOffset)
		}
		press(a, ctrl('u'))
		if a.HelpOffset != 0 {
			t.Errorf("offset = %d, want back to the top", a.HelpOffset)
		}
	})

	t.Run("detail", func(t *testing.T) {
		a := appAt(t, "backend:lint")
		press(a, Char('d'))
		if a.Screen != ScreenDetail {
			t.Fatalf("screen = %v", a.Screen)
		}
		a.Viewport = 10
		press(a, ctrl('d'))
		if a.DetailOffset != 5 {
			t.Errorf("offset = %d, want half of 10", a.DetailOffset)
		}
	})
}

// The filter takes the characters and leaves the motions, so narrowing the list and then
// picking from it is one gesture. `j` is a letter of a task name here; ↓ never is.
func TestTheFilterTakesLettersAndLeavesMotions(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('/'))
	if !a.Filtering {
		t.Fatal("the filter did not open")
	}

	press(a, Char('j'))
	if a.Query != "j" {
		t.Errorf("`j` should have been typed: query = %q", a.Query)
	}

	a.PopQuery()
	a.Cursor = 0
	press(a, special(tea.KeyDown))
	if a.Query != "" {
		t.Errorf("↓ is not a letter: query = %q", a.Query)
	}
	if a.Cursor != 1 {
		t.Errorf("↓ should move the list behind the filter: cursor = %d", a.Cursor)
	}
}

// The argument line edits with Home and End, so those must not reach the list behind it —
// the prompts that answer a motion key keep it.
func TestTheArgsPromptKeepsItsEditingKeys(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	a.Cursor = 1
	press(a, Char('a'))
	if !a.EnteringArgs {
		t.Fatal("the args prompt did not open")
	}
	for _, c := range "--dry" {
		press(a, Char(c))
	}

	press(a, special(tea.KeyHome))
	if a.ArgsCursor != 0 {
		t.Errorf("Home should move the args cursor: %d", a.ArgsCursor)
	}
	if a.Cursor != 1 {
		t.Errorf("and not the list behind it: cursor = %d", a.Cursor)
	}

	press(a, special(tea.KeyEnd))
	if a.ArgsCursor != len("--dry") {
		t.Errorf("End should reach the end of the line: %d", a.ArgsCursor)
	}
}

// Input mode sends `^d` to the child as EOF. A motion key that outranked it would make the
// one screen you cannot get a shell to close impossible to close.
func TestInputModeKeepsItsControlKeys(t *testing.T) {
	a := longRun(t)
	a.RunCursor = len(a.RunRows) - 1
	a.SendingInput = true

	press(a, ctrl('d'))
	if a.RunCursor != len(a.RunRows)-1 {
		t.Errorf("`^d` should have gone to the child, not moved the cursor: %d", a.RunCursor)
	}
}

// The search line takes ↑ and ↓ for its matches and leaves the rest, so the output you are
// searching is still yours to page through while you type the query.
func TestTheSearchLineOnlyTakesTheArrows(t *testing.T) {
	a := longRun(t)
	a.Viewport = 10
	a.RunCursor = 0
	press(a, Char('/'))
	if !a.Searching {
		t.Fatal("the search line did not open")
	}

	press(a, ctrl('d'))
	if a.RunCursor != 5 {
		t.Errorf("`^d` should still page the run: cursor = %d", a.RunCursor)
	}
	if a.SearchInput != "" {
		t.Errorf("and not be typed: %q", a.SearchInput)
	}
}

// `--keys` could reach letters, ⇥ and ⏎ and nothing else, which left every motion key
// undemonstrable from a screenshot — the control chords most of all.
func TestKeysFromReadsControlChordsAndEsc(t *testing.T) {
	got := KeysFrom("a^d^^\t\n\x1b^U")
	want := []Key{
		Char('a'),
		Ctrl('d'),
		Char('^'),
		Tab(),
		Enter(),
		Esc(),
		// Case is folded, so `^U` and `^u` are the one chord a terminal reports.
		Ctrl('u'),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A trailing caret has nothing to modify. It must not read past the end of the string.
func TestATrailingCaretIsItself(t *testing.T) {
	got := KeysFrom("g^")
	if len(got) != 2 || got[1] != Char('^') {
		t.Errorf("got %+v", got)
	}
}

// The whole point: a motion key now drives a headless frame.
func TestAControlChordDrivesTheApp(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.SetFoldAll(true)
	a.Viewport = 2
	a.Cursor = 0
	for _, k := range KeysFrom("^f") {
		press(a, k)
	}
	if a.Cursor != 2 {
		t.Errorf("cursor = %d, want a page down", a.Cursor)
	}
}
