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

// A binding shown in the footer still carries its long form for the `?` screen.
func TestFooterBindingsKeepTheirFullExplanation(t *testing.T) {
	var input *Binding
	for i := range Run.Bindings {
		if Run.Bindings[i].Keys == "i" {
			input = &Run.Bindings[i]
		}
	}
	if input == nil {
		t.Fatal("no `i` binding in the run section")
	}
	if input.Footer != "input" {
		t.Errorf("footer label = %q", input.Footer)
	}
	if !strings.Contains(input.What, "cannot see the prompt") {
		t.Errorf("what = %q", input.What)
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
	k.Rebind(Help, 'H')
	if k.Picker('H') != Help || k.Run('H') != Help || k.History('H') != Help {
		t.Error("the rebinding did not reach every screen")
	}
	if k.Picker('?') != None {
		t.Error("the old key should be free")
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
