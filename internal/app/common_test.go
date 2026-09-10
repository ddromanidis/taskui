package app

import (
	"strings"
	"testing"

	"github.com/romanidis/taskui/internal/keys"
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

// Every footer pins a pointer to the full keymap on its right edge. It named `?` outright,
// so a rebound help key left every screen in the program pointing at a key that did nothing.
func TestTheFooterPointerFollowsAReboundHelpKey(t *testing.T) {
	a := appAt(t, "backend:lint")
	a.Keymap.Rebind(keys.Help, keys.Plain('z'))
	a.Width, a.Height = 90, 12

	lines := a.RenderHeadless(90, 12)
	footer := lines[len(lines)-1]
	if !strings.HasSuffix(strings.TrimSpace(footer), "z keys") {
		t.Errorf("footer = %q", footer)
	}
	if strings.Contains(footer, "? keys") {
		t.Errorf("footer still points at the old key: %q", footer)
	}
}

// The `?` screen is what a first-time reader opens to find out how to leave, and there is
// no `? keys` on it to point them anywhere else.
func TestTheKeymapScreenSaysHowToQuit(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	lines := a.RenderHeadless(90, 12)
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "q quit") {
		t.Errorf("footer = %q", footer)
	}
}

// With a query kept, `esc` clears it rather than closing the screen — so the footer must
// not offer both meanings of the one key on the one line.
func TestTheKeptQueryFooterDoesNotOfferEscTwice(t *testing.T) {
	a := appAt(t, "backend:lint")
	press(a, Char('?'))
	press(a, Char('f'))
	for _, c := range "quit" {
		press(a, Char(c))
	}
	press(a, Enter())
	if a.HelpFinding || a.HelpQuery == "" {
		t.Fatalf("wanted a kept query: finding=%v query=%q", a.HelpFinding, a.HelpQuery)
	}
	footer := a.RenderHeadless(90, 12)[11]
	if !strings.Contains(footer, "esc clear") {
		t.Errorf("footer = %q", footer)
	}
	if strings.Contains(footer, "close") {
		t.Errorf("`esc` is offered as close and clear at once: %q", footer)
	}
}
