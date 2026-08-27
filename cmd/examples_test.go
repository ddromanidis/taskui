package cmd

import (
	"strings"
	"testing"
)

// Every frame is rendered at print time from the sample project, which is the point: a
// hand-drawn one is stale the moment a column moves. These check the rendering still
// happens and still says something.

func TestEveryExampleRendersSomething(t *testing.T) {
	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) {
			out, err := execute(t, "examples", ex.name, "--width", "90")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, ex.title) {
				t.Errorf("no title in:\n%s", out)
			}
			if len(strings.Split(out, "\n")) < 8 {
				t.Errorf("suspiciously short:\n%s", out)
			}
		})
	}
}

// A frame that came out empty would print as blank lines and nobody would notice.
func TestTheFramesAreRealRenderedScreens(t *testing.T) {
	out, err := execute(t, "examples", "browse", "--width", "90")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"taskui",       // the header
		"domain·verb",  // the pivot names
		"17 tasks",     // the sample project's size
		"backend",      // a namespace from it
		"space o fold", // the footer, from the keymap table
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered frames are missing %q:\n%s", want, out)
		}
	}
}

// The whole page has to fit the width it was asked for, or it wraps in the terminal and
// every frame in it is ruined.
func TestNothingOverflowsTheRequestedWidth(t *testing.T) {
	for _, width := range []string{"60", "80", "100", "200"} {
		out, err := execute(t, "examples", "--width", width)
		if err != nil {
			t.Fatal(err)
		}
		limit := clampWidth(atoi(t, width))
		for i, line := range strings.Split(out, "\n") {
			if n := len([]rune(line)); n > limit {
				t.Fatalf("width %s: line %d is %d wide (limit %d): %q", width, i+1, n, limit, line)
			}
		}
	}
}

// A tab is one rune and several columns, so a width check that counted runes would pass a
// line that overflowed on screen. Nothing printed here may contain one.
func TestNoTabsInAnyExample(t *testing.T) {
	out, err := execute(t, "examples", "--width", "90")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\t") {
			t.Errorf("line %d has a tab, which is wider than it measures: %q", i+1, line)
		}
	}
}

// A blank row inside a frame gets no indent: trailing whitespace is invisible on screen and
// noise in a file, and these are meant to be pasteable.
func TestNoTrailingWhitespace(t *testing.T) {
	out, err := execute(t, "examples", "--width", "90")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(out, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

func TestAnUnknownTopicListsTheRealOnes(t *testing.T) {
	_, err := execute(t, "examples", "nope")
	if err == nil {
		t.Fatal("no error")
	}
	for _, ex := range examples {
		if !strings.Contains(err.Error(), ex.name) {
			t.Errorf("the error does not mention %q: %v", ex.name, err)
		}
	}
}

// The examples must never read the real archive: they run on whatever machine happens to
// invoke them, and a demo that showed somebody's actual failing test would be a bug.
func TestTheSampleProjectIsSelfContained(t *testing.T) {
	a := sampleApp()
	if a.StateDir() == "" || !strings.Contains(a.StateDir(), "nonexistent") {
		t.Errorf("state dir is %q — it should point at nothing", a.StateDir())
	}
	if len(a.Tasks) != len(sampleTasks) {
		t.Errorf("built %d tasks from %d", len(a.Tasks), len(sampleTasks))
	}
}

// Every example has to be reachable by name, and no two by the same one.
func TestExampleNamesAreUniqueAndTitled(t *testing.T) {
	seen := map[string]bool{}
	for _, ex := range examples {
		if ex.name == "" || ex.title == "" {
			t.Errorf("%+v is missing a name or a title", ex)
		}
		if seen[ex.name] {
			t.Errorf("two examples called %q", ex.name)
		}
		seen[ex.name] = true
		if len(ex.parts) == 0 {
			t.Errorf("%q has no content", ex.name)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
