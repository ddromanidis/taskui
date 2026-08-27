package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// The man page ships in every release archive and in the Homebrew cask, and the README tells
// people it covers the options. It had drifted two rewrites behind — its header said
// `taskui 0.1.1`, from the Rust version, and it was missing eight flags and all three
// subcommands. This is the test that would have said so on the day it happened.

func manPage(t *testing.T) string {
	t.Helper()
	blob, err := os.ReadFile("../taskui.1")
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

// The generated sections are committed, so they have to match what regenerating produces.
// `task man` is the fix when this fails.
func TestTheManPageIsUpToDate(t *testing.T) {
	current := manPage(t)
	next, err := RegenerateMan(current)
	if err != nil {
		t.Fatal(err)
	}
	if next != current {
		t.Error("taskui.1 is out of date — run `task man`")
	}
}

// Belt and braces: the regeneration could be correct and the markers still be missing a
// whole section, which would leave the page silently short.
func TestEveryFlagIsInTheManPage(t *testing.T) {
	page := manPage(t)
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		if !strings.Contains(page, `\-\-`+roff(f.Name)) {
			t.Errorf("--%s is not in the man page", f.Name)
		}
	})
}

func TestEverySubcommandIsInTheManPage(t *testing.T) {
	page := manPage(t)
	for _, c := range rootCmd.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		if !strings.Contains(page, `" `+c.Name()+`"`) {
			t.Errorf("the %q subcommand is not in the man page", c.Name())
		}
	}
}

// The version in the header was the Rust one for the whole life of the Go project. Naming no
// version at all is better than naming a wrong one — the release archive carries the version
// in its own name.
func TestTheManPageDoesNotClaimAVersion(t *testing.T) {
	first, _, _ := strings.Cut(manPage(t), "\n")
	if strings.Contains(first, "0.1") || strings.Contains(first, "0.2") {
		t.Errorf("the header pins a version and will go stale: %q", first)
	}
}

// A generated page that groff cannot parse is worse than a stale one. Escaping is the risk:
// flag help is full of hyphens and backslashes, and a line starting with a dot is a request.
func TestGeneratedTextIsEscapedForTroff(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a-b", `A\-b.`},
		{".hidden", `\&.hidden.`},
		{`a\b`, `A\eb.`},
		{"already ends.", "Already ends."},
		{"", "."},
	} {
		if got := sentence(c.in); got != c.want {
			t.Errorf("sentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The page is committed and then shipped, so it must not carry whichever home directory
// happened to run the generator.
func TestTheManPageHasNobodysHomeDirectoryInIt(t *testing.T) {
	page := manPage(t)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to check for")
	}
	if strings.Contains(page, home) {
		t.Error("the man page names this machine's home directory")
	}
}

// Splicing has to fail loudly on a page whose markers have been removed, rather than
// silently producing one with no options in it.
func TestRegeneratingAPageWithNoMarkersFails(t *testing.T) {
	if _, err := RegenerateMan(".TH TASKUI 1\n.SH NAME\ntaskui\n"); err == nil {
		t.Error("no error for a page with no markers")
	}
}
