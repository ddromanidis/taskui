package keys

import (
	"os"
	"regexp"
	"testing"
)

// The Neovim plugin reaches a task by typing at the terminal — esc, the jump key, the name
// — because it drives the real interface rather than a socket. That makes `jump_key` in
// lua/taskui/config.lua the one taskui binding living outside this package, and moving
// `jump` here silently broke `:TaskUI run` until the plugin's own integration tests caught
// it at release time, which is far too late and needs Neovim installed to happen at all.
//
// This is the same claim the rest of this package makes about the `?` screen, the footers,
// the man page and the README, extended to the last surface that spells a key by hand.
func TestThePluginAgreesWithTheJumpKey(t *testing.T) {
	blob, err := os.ReadFile("../../lua/taskui/config.lua")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*jump_key\s*=\s*"([^"]*)"`).FindSubmatch(blob)
	if m == nil {
		t.Fatal("lua/taskui/config.lua has no jump_key — `:TaskUI run` needs one to type")
	}

	chord, ok := NewKeymap().KeyOf(Jump)
	if !ok {
		t.Fatal("no key for jump")
	}
	if got, want := string(m[1]), chord.Display(); got != want {
		t.Errorf("the plugin types %q to jump and taskui reads %q — "+
			"set jump_key in lua/taskui/config.lua to %q", got, want, want)
	}
}

// The plugin types the key as one character. A jump moved onto a chord would arrive at the
// terminal as an escape sequence, not as the two bytes the plugin sends.
func TestTheJumpKeyIsSomethingThePluginCanType(t *testing.T) {
	chord, ok := NewKeymap().KeyOf(Jump)
	if !ok {
		t.Fatal("no key for jump")
	}
	if chord.Mods != 0 {
		t.Errorf("jump is on %s, which the Neovim plugin cannot type at a terminal", chord)
	}
}
