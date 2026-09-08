package keys

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The footer is generated, so it cannot drift from the help screen.
func TestFootersAreBuiltFromTheSameTable(t *testing.T) {
	picker := Footer(&Picker, NewKeymap())
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
		if section.Title == "Prompts" {
			continue
		}
		// Spelled, and compared against the key `help` actually sits on: the table names
		// the action now, so asserting on a literal `?` would only be testing the default.
		km := NewKeymap()
		help, ok := km.KeyOf(Help)
		if !ok {
			t.Fatal("no key for help")
		}
		found := false
		for _, b := range Spelled(section, km) {
			if strings.Contains(b.Keys, help.Display()) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s does not mention %s", section.Title, help.Display())
		}
	}
}

// Footers get one line, so keep them plausibly short.
func TestFootersFitAReasonableTerminal(t *testing.T) {
	for _, section := range Sections {
		line := Footer(section, NewKeymap())
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
	if k.Picker(Plain('?')) != None {
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
	clone.Rebind(Pivot, Plain('z'))
	if original.Picker(Plain('z')) != None {
		t.Error("the clone wrote through to the original")
	}
	if clone.Picker(Plain('z')) != Pivot {
		t.Error("the clone did not take the rebinding")
	}
}

// The same key meaning two things on one screen is reported, not silently resolved.
func TestCollidingKeysAreListed(t *testing.T) {
	k := NewKeymap()
	k.Rebind(Pivot, Plain('a'))
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
func unboundKey(t *testing.T, k *Keymap) Chord {
	t.Helper()
	for c := '!'; c <= '~'; c++ {
		if chord := Plain(c); k.Picker(chord) == None && k.Run(chord) == None &&
			k.History(chord) == None && k.Timeline(chord) == None &&
			k.Diff(chord) == None && k.Profile(chord) == None {
			return chord
		}
	}
	t.Fatal("every printable character is bound to something")
	return Chord{}
}

// The config spelling of a binding. Round-tripping matters because `--dump-config` writes
// these back out, and a template that cannot be read again is a template that lies.
func TestChordsParseAndSpellThemselvesBack(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  Chord
		spelt string
	}{
		{"j", Plain('j'), "j"},
		{"G", Plain('G'), "G"},
		{"/", Plain('/'), "/"},
		// `+` is a key, not a dangling modifier — the split only happens with something on
		// both sides of it.
		{"+", Plain('+'), "+"},
		{"space", Plain(' '), "space"},
		{"shift+space", Chord{Key: ' ', Mods: ModShift}, "shift+space"},
		{"ctrl+r", Chord{Key: 'r', Mods: ModCtrl}, "ctrl+r"},
		{"alt+j", Chord{Key: 'j', Mods: ModAlt}, "alt+j"},
		// Order in, canonical order out.
		{"shift+ctrl+r", Chord{Key: 'r', Mods: ModCtrl | ModShift}, "ctrl+shift+r"},
	} {
		got, err := ParseChord(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q parsed as %+v, want %+v", tc.in, got, tc.want)
		}
		if got.String() != tc.spelt {
			t.Errorf("%q spells itself %q, want %q", tc.in, got.String(), tc.spelt)
		}
	}
}

// A binding that could never match is worth refusing at load, not at three in the morning.
func TestUnmatchableChordsAreRefused(t *testing.T) {
	for _, in := range []string{
		"",
		"pp",
		// The terminal sends `G`, never shift+`g`, so this would never fire.
		"shift+g",
		"hyper+j",
		"ctrl+ctrl+j",
	} {
		if got, err := ParseChord(in); err == nil {
			t.Errorf("%q was accepted as %+v", in, got)
		}
	}
}

// A placeholder that names nothing is left on the screen as `{jmup}` rather than silently
// losing its key, which is survivable but ugly. This is what keeps one out of a release.
func TestEveryPlaceholderNamesAnAction(t *testing.T) {
	km := NewKeymap()
	for _, section := range Sections {
		for _, b := range section.Bindings {
			for _, m := range placeholder.FindAllStringSubmatch(b.Keys+" "+b.What+" "+b.Footer, -1) {
				action, ok := ActionByName(m[1])
				if !ok {
					t.Errorf("%s: `%s` names no action", section.Title, m[0])
					continue
				}
				if _, ok := km.KeyOf(action); !ok {
					t.Errorf("%s: `%s` is an action no screen offers", section.Title, m[0])
				}
			}
		}
	}
}

// The help table used to hold the keys as text, so rebinding moved the key and left every
// surface still naming the old one.
func TestTheHelpTableFollowsARebinding(t *testing.T) {
	km := NewKeymap()
	km.Rebind(Jump, Plain('z'))

	var found bool
	for _, b := range Spelled(&Picker, km) {
		if b.Footer == "jump" {
			found = true
			if b.Keys != "z" {
				t.Errorf("the `?` screen still says %q", b.Keys)
			}
		}
	}
	if !found {
		t.Fatal("no jump binding in the picker")
	}
	if footer := Footer(&Picker, km); !strings.Contains(footer, "z jump") {
		t.Errorf("the footer still says %q", footer)
	}
	// And the screen that documents the keymap itself, which was written out by hand.
	if footer := Footer(&HelpSection, km); !strings.Contains(footer, "z find") {
		t.Errorf("the `?` footer still says %q", footer)
	}
}

// Rebinding onto a key a screen answers directly used to be silent: the key that was
// displaced simply stopped working, and nothing here could see it, because it was never in
// the map to collide with.
func TestCollidingWithALiteralKeyIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chord  Chord
		expect string
	}{
		{"the picker's fold key", Plain(' '), "picker: `space` is both fold a group and help"},
		{"a group motion", Plain('}'), "picker: `}` is both next group and help"},
		{"a motion", Plain('j'), "picker: `j` is both move down and help"},
		{"a half page", Chord{Key: 'd', Mods: ModCtrl}, "picker: `ctrl+d` is both half a page down and help"},
		{"a run slot", Plain('3'), "run: `3` is both that slot and help"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			km := NewKeymap()
			km.Rebind(Help, tc.chord)
			var got []string
			for _, c := range km.Conflicts() {
				if strings.Contains(c, "help") {
					got = append(got, c)
				}
			}
			if len(got) == 0 {
				t.Fatalf("nothing reported for %s", tc.chord)
			}
			var ok bool
			for _, c := range got {
				if c == tc.expect {
					ok = true
				}
			}
			if !ok {
				t.Errorf("reported %q, want %q", got, tc.expect)
			}
		})
	}
}

// The defaults must not trip the new check either — every motion is a key no action is on.
func TestTheDefaultsDoNotCollideWithTheLiterals(t *testing.T) {
	if c := NewKeymap().Conflicts(); len(c) > 0 {
		t.Errorf("the shipped keymap reports conflicts: %v", c)
	}
}
