package keys

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The footer is generated, so it cannot drift from the help screen.
func TestFootersAreBuiltFromTheSameTable(t *testing.T) {
	picker := Footer(&Picker)
	if !strings.Contains(picker, "⏎ run") {
		t.Errorf("footer = %q", picker)
	}
	// The picker's footer names the target pivot itself, so `p` is not in the table.
	if strings.Contains(picker, "pivot") {
		t.Errorf("footer = %q", picker)
	}
	if !strings.Contains(picker, "h history") {
		t.Errorf("footer = %q", picker)
	}
	// Not marked for the footer, so it stays in the full help only.
	if strings.Contains(picker, "everything") {
		t.Errorf("footer = %q", picker)
	}
}

// A binding shown in the footer still carries its long form for the `?` screen. Every one
// of them, rather than one chosen example — this used to name `i`, and stopped meaning
// anything the moment `i` was demoted out of the footer for space.
func TestFooterBindingsKeepTheirFullExplanation(t *testing.T) {
	for _, section := range Sections {
		for _, b := range section.Bindings {
			if b.Footer == "" {
				continue
			}
			if b.What == "" {
				t.Errorf("%s: `%s` has a footer label and no explanation", section.Title, b.Keys)
			}
			// The footer label is the abbreviation; if it is not shorter, one of them is
			// doing the other's job.
			if len(b.What) <= len(b.Footer) {
				t.Errorf("%s: `%s` explains itself as %q, no longer than its label %q",
					section.Title, b.Keys, b.What, b.Footer)
			}
		}
	}
}

// A binding kept out of the footer still has to be in the `?` screen, which is the only
// place left that names it.
func TestDemotedBindingsAreStillExplained(t *testing.T) {
	for _, section := range Sections {
		for _, b := range section.Bindings {
			if b.Keys == "" || b.What == "" {
				t.Errorf("%s: %+v is missing a key or an explanation", section.Title, b)
			}
		}
	}
}

func TestEverySectionDocumentsTheHelpKeyOrIsAPrompt(t *testing.T) {
	for _, section := range Sections {
		if section.Title == "Prompts" || section.Title == "Detail" {
			continue
		}
		found := false
		for _, b := range section.Bindings {
			if strings.Contains(b.Keys, "?") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not mention ?", section.Title)
		}
	}
}

// Footers get one line, so keep them plausibly short.
func TestFootersFitAReasonableTerminal(t *testing.T) {
	for _, section := range Sections {
		line := Footer(section)
		if n := utf8.RuneCountInString(line); n >= 110 {
			t.Errorf("%s: %d chars: %q", section.Title, n, line)
		}
	}
}

// Rebinding an action moves it on every screen that offers it.
func TestRebindingAppliesEverywhereTheActionIsOffered(t *testing.T) {
	k := NewKeymap()
	// Derived rather than hardcoded. Rebinding onto a key some action already uses is a
	// shadowing conflict, which is a different thing with its own test below — and every
	// hardcoded "free" key in this test so far has stopped being free as the keymap grew.
	key := unboundKey(t, k)
	k.Rebind(Help, key)
	if k.Picker(key) != Help || k.Run(key) != Help || k.History(key) != Help {
		t.Error("the rebinding did not reach every screen")
	}
	if k.Picker('?') != None {
		t.Error("the old key should be free")
	}
	// The screens added later have to take the rebinding too, or `?` stops working on
	// exactly the screens nobody remembers to check.
	if k.Timeline(key) != Help || k.Diff(key) != Help || k.Profile(key) != Help {
		t.Error("the rebinding missed one of the later screens")
	}
}

// Cloning is what lets a config be applied without mutating the defaults.
func TestCloningLeavesTheOriginalAlone(t *testing.T) {
	original := NewKeymap()
	clone := original.Clone()
	clone.Rebind(Pivot, 'z')
	if original.Picker('z') != None {
		t.Error("the clone wrote through to the original")
	}
	if clone.Picker('z') != Pivot {
		t.Error("the clone did not take the rebinding")
	}
}

// The same key meaning two things on one screen is reported, not silently resolved.
func TestCollidingKeysAreListed(t *testing.T) {
	k := NewKeymap()
	k.Rebind(Pivot, 'a')
	found := false
	for _, c := range k.Conflicts() {
		if strings.Contains(c, "picker") && strings.Contains(c, "both") {
			found = true
		}
	}
	if !found {
		t.Errorf("conflicts = %v", k.Conflicts())
	}
}

// The defaults must not collide with each other on any screen.
func TestTheDefaultsDoNotCollide(t *testing.T) {
	if c := NewKeymap().Conflicts(); len(c) != 0 {
		t.Errorf("the shipped keymap shadows itself: %v", c)
	}
}

// unboundKey is a character no action answers to on any screen.
//
// Every screen, so that a key free in the picker but taken in the run view is not mistaken
// for a free one — which is how each hardcoded choice here rotted in turn.
func unboundKey(t *testing.T, k *Keymap) rune {
	t.Helper()
	for c := '!'; c <= '~'; c++ {
		if k.Picker(c) == None && k.Run(c) == None && k.History(c) == None &&
			k.Timeline(c) == None && k.Diff(c) == None && k.Profile(c) == None {
			return c
		}
	}
	t.Fatal("every printable character is bound to something")
	return 0
}
