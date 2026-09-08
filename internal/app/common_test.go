package app

import (
	"testing"

	"github.com/ddromanidis/taskui/internal/keys"
)

// The footer has always pinned `? keys` to its right edge, on every screen. The detail
// panel was the one that then did not answer it.
func TestTheHelpKeyWorksOnEveryScreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*App)
	}{
		{"picker", func(*App) {}},
		{"detail", func(a *App) { press(a, Char('d')) }},
		{"history", func(a *App) { a.OpenHistory() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := appAt(t, "backend:lint")
			tc.open(a)
			press(a, Char('?'))
			if a.Screen != ScreenHelp {
				t.Errorf("`?` did not open the keymap: screen = %v", a.Screen)
			}
			press(a, Char('?'))
			if a.Screen == ScreenHelp {
				t.Error("and did not close it again")
			}
		})
	}
}

// `keys: quit: z` used to apply on six screens out of eight, because the detail panel and
// the `?` screen matched the character rather than the action and had no map to consult.
func TestQuitIsReboundOnEveryScreen(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*App)
	}{
		{"picker", func(*App) {}},
		{"detail", func(a *App) { press(a, Char('d')) }},
		{"help", func(a *App) { press(a, Char('?')) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := appAt(t, "backend:lint")
			a.Keymap.Rebind(keys.Quit, keys.Plain('z'))
			tc.open(a)

			press(a, Char('q'))
			if a.Confirm != nil {
				t.Fatal("the old key should no longer quit")
			}
			press(a, Char('z'))
			if a.Confirm == nil {
				t.Error("the new one should")
			}
		})
	}
}

// ⌃c is the panic button, and it now means the same thing wherever it is pressed — it used
// to quit from the filter and the run's search line, and do nothing at all from the
// argument prompt.
func TestCtrlCLeavesFromAnyPrompt(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*App)
	}{
		{"filter", func(a *App) { press(a, Char('/')) }},
		{"args", func(a *App) { press(a, Char('a')) }},
		{"jump", func(a *App) { press(a, Char('f')) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := appAt(t, "backend:lint")
			tc.open(a)
			if !a.promptOpen() {
				t.Fatal("the prompt did not open")
			}
			press(a, ctrl('c'))
			if a.Confirm == nil {
				t.Error("⌃c should ask to quit")
			}
		})
	}
}

// Input mode is the exception: every byte is the child's, and ⌃c is the one you most need
// to reach it.
func TestCtrlCGoesToTheChildWhileTyping(t *testing.T) {
	a := longRun(t)
	a.SendingInput = true
	press(a, ctrl('c'))
	if a.Confirm != nil {
		t.Error("⌃c should have gone to the task, not asked to quit")
	}
}

// A prompt still takes the letters, so `q` typed into a filter narrows the list rather
// than closing the tool.
func TestAPromptStillTakesTheQuitKey(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('/'))
	press(a, Char('q'))
	if a.Confirm != nil {
		t.Fatal("`q` should have been typed into the filter")
	}
	if a.Query != "q" {
		t.Errorf("query = %q", a.Query)
	}
}
