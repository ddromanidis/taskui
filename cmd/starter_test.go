package cmd

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ddromanidis/taskui/internal/theme"
)

func press(m *starterModel, r rune) {
	m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
}

func TestTheOfferReadsEveryAnswer(t *testing.T) {
	cases := []struct {
		key  rune
		want starterChoice
	}{
		{'g', starterWrite},
		{'e', starterEdit},
		{'q', starterQuit},
	}
	for _, c := range cases {
		m := &starterModel{root: "/src/acme", config: theme.DefaultConfig()}
		press(m, c.key)
		if m.choice != c.want {
			t.Errorf("%c chose %v, want %v", c.key, m.choice, c.want)
		}
	}
}

// A key nobody bound must not be read as consent to write a file.
func TestAnUnboundKeyDoesNotAnswerTheOffer(t *testing.T) {
	m := &starterModel{root: "/src/acme", config: theme.DefaultConfig()}
	press(m, 'x')
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if m.choice != starterQuit {
		t.Errorf("choice = %v, want the default", m.choice)
	}
	if cmd != nil {
		t.Error("an unbound key should not quit the offer")
	}
}

// Bubble Tea cuts a line that overruns the terminal, and the root is an absolute path with
// no length limit of its own — so the frame has to fit the width it was told about.
func TestTheOfferFitsTheTerminalItIsDrawnIn(t *testing.T) {
	root := "/Users/somebody/src/a/very/deeply/nested/scratch/directory/for/testing"
	for _, width := range []int{40, 60, 80, 120} {
		m := &starterModel{root: root, config: theme.DefaultConfig(), width: width}
		for line := range strings.SplitSeq(frameOf(m), "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("at %d columns a line is %d wide: %q", width, got, line)
			}
		}
	}
}

func TestTheOfferNamesBothWaysOut(t *testing.T) {
	m := &starterModel{root: "/src/acme", config: theme.DefaultConfig(), width: 80}
	frame := ansi.Strip(frameOf(m))
	for _, want := range []string{"no Taskfile", "/src/acme", "Taskfile.yml", "$EDITOR"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the offer never mentions %q:\n%s", want, frame)
		}
	}
}

func frameOf(m *starterModel) string {
	return strings.TrimRight(m.View().Content, "\n")
}

func TestElideKeepsTheEndOfAPath(t *testing.T) {
	got := elide("/one/two/three/four/five", 12)
	if !strings.HasSuffix(got, "four/five") {
		t.Errorf("elide = %q, want the tail kept", got)
	}
	if n := len([]rune(got)); n != 12 {
		t.Errorf("elide = %q, %d runes, want 12", got, n)
	}
	if short := elide("/one/two", 12); short != "/one/two" {
		t.Errorf("a path that fits was changed: %q", short)
	}
}

func TestBlockIndentsTheLinesItMade(t *testing.T) {
	out := block("one two three four five six seven eight", "  ", "  ", 24)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("nothing wrapped: %q", out)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line lost its indent: %q", line)
		}
		if len([]rune(line)) > 24 {
			t.Errorf("line is %d wide: %q", len([]rune(line)), line)
		}
	}
}
