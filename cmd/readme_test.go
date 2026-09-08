package cmd

import (
	"os"
	"strings"
	"testing"
)

func readmeFile(t *testing.T) string {
	t.Helper()
	blob, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(blob)
}

// The generated tables are committed, so they have to match what regenerating produces.
// `task man` is the fix when this fails.
func TestTheReadmeKeyTablesAreUpToDate(t *testing.T) {
	current := readmeFile(t)
	next, err := RegenerateReadme(current)
	if err != nil {
		t.Fatal(err)
	}
	if next != current {
		t.Error("README.md's key tables are out of date — run `task man`")
	}
}

// Belt and braces, as for the man page: regeneration could be correct and a whole table
// still be missing its markers, which would leave the README quietly short of a screen.
//
// The five are named rather than derived from Sections, because which screens the README
// covers is an editorial choice: it has no detail-panel or history section at all, and the
// `?` screen documents itself in the program.
func TestEveryReadmeKeyTableIsGenerated(t *testing.T) {
	readme := readmeFile(t)
	covered := []string{"Picker", "Run", "Timeline", "Diff", "Profile"}
	for _, title := range covered {
		if !strings.Contains(readme, beginTable+title+" ") {
			t.Errorf("the README has no %s key table", title)
		}
	}
	// And none escaped: a table written by hand alongside the generated ones is exactly
	// the drift this replaced, so there must be no more tables than there are markers.
	if got := strings.Count(readme, "| key | |"); got != len(covered) {
		t.Errorf("%d key tables in the README, %d of them generated", got, len(covered))
	}
}

// Splicing has to fail loudly rather than silently produce a README with an empty table.
func TestRegeneratingAReadmeWithABadMarkerFails(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"unknown section", "# t\n" + beginTable + "Nonesuch -->\n" + endTable + "\n"},
		{"no section named", "# t\n" + beginTable + "-->\n" + endTable + "\n"},
		{"no end marker", "# t\n" + beginTable + "Picker -->\nnothing closes this\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RegenerateReadme(tc.in); err == nil {
				t.Error("no error")
			}
		})
	}
}

// A README with no markers at all is left exactly as it was, so the command is safe to
// point at the wrong file.
func TestAReadmeWithNoMarkersIsUntouched(t *testing.T) {
	const plain = "# taskui\n\nno tables here.\n"
	next, err := RegenerateReadme(plain)
	if err != nil {
		t.Fatal(err)
	}
	if next != plain {
		t.Errorf("rewrote a file with no markers: %q", next)
	}
}
