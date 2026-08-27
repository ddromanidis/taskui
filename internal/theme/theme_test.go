package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/keys"
)

func loadStr(t *testing.T, yaml string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestColourNamesHexAndIndicesAllParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Color
		ok   bool
	}{
		{"red", Red, true},
		{"  Cyan ", Cyan, true},
		{"bright-blue", BrightBlue, true},
		{"purple", Magenta, true},
		{"#1e2030", Color{Kind: KindRGB, R: 0x1e, G: 0x20, B: 0x30}, true},
		{"240", Color{Kind: KindIndexed, Index: 240}, true},
		{"chartreuse", Color{}, false},
		{"#abc", Color{}, false},
	} {
		got, ok := ParseColor(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseColor(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// A missing file is the normal case, not an error.
func TestNoConfigMeansDefaults(t *testing.T) {
	c := Load("/definitely/not/here/config.yaml")
	if c.Theme.Colors != DefaultColors() {
		t.Error("theme drifted from the defaults")
	}
	if len(c.Problems) != 0 {
		t.Errorf("problems = %v", c.Problems)
	}
}

func TestOnlyTheNamedColoursChange(t *testing.T) {
	c := loadStr(t, "colors:\n  accent: magenta\n  status-failed: '#ff5555'\n")
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors.Accent != Magenta {
		t.Errorf("accent = %+v", c.Theme.Colors.Accent)
	}
	if want := (Color{Kind: KindRGB, R: 0xff, G: 0x55, B: 0x55}); c.Theme.Colors.StatusFailed != want {
		t.Errorf("status-failed = %+v", c.Theme.Colors.StatusFailed)
	}
	// Untouched keys keep their defaults.
	if c.Theme.Colors.StatusOk != DefaultColors().StatusOk || c.Theme.Colors.Dim != DefaultColors().Dim {
		t.Error("untouched keys moved")
	}
}

// Both spellings work, because arguing about it would be sillier than supporting it.
func TestColoursIsAcceptedToo(t *testing.T) {
	c := loadStr(t, "colours:\n  accent: green\n")
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors.Accent != Green {
		t.Errorf("accent = %+v", c.Theme.Colors.Accent)
	}
}

// A colour that silently does nothing is worse than one that says why.
func TestABadColourIsReportedAndTheRestStillApplies(t *testing.T) {
	c := loadStr(t, "colors:\n  accent: chartreuse\n  mode: red\n")
	if c.Theme.Colors.Mode != Red {
		t.Error("the good one should still have taken")
	}
	if c.Theme.Colors.Accent != DefaultColors().Accent {
		t.Error("the bad one should have been ignored")
	}
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "chartreuse") {
		t.Errorf("problems = %v", c.Problems)
	}
}

// A typo'd key is a typo, not a silently ignored line.
func TestAnUnknownKeyIsReported(t *testing.T) {
	c := loadStr(t, "colors:\n  acccent: red\n")
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0], "acccent") {
		t.Errorf("problems = %v", c.Problems)
	}
}

// The defaults have to be legible on a terminal whose theme we know nothing about.
// DarkGray is ANSI bright black — a hair off the background in most dark colourschemes.
// `faint` and `rule` are deliberately allowed to use it, because they paint alignment cues
// rather than words; everything that carries meaning has to stay above it.
func TestNoDefaultIsBrightBlack(t *testing.T) {
	d := DefaultColors()
	for name, colour := range map[string]Color{
		"dim":            d.Dim,
		"status-skipped": d.StatusSkipped,
		"status-pending": d.StatusPending,
		"text":           d.Text,
	} {
		if colour == DarkGray {
			t.Errorf("%s is invisible on a dark theme", name)
		}
	}
}

func TestTheSelectionDefaultsToReverseVideo(t *testing.T) {
	if !DefaultColors().Selection.IsDefault() {
		t.Error("selection should default to reverse video")
	}
	c := loadStr(t, "colors:\n  selection: '#1e2030'\n")
	if want := (Color{Kind: KindRGB, R: 0x1e, G: 0x20, B: 0x30}); c.Theme.Colors.Selection != want {
		t.Errorf("still overridable: %+v", c.Theme.Colors.Selection)
	}
}

func TestKeysCanBeRebound(t *testing.T) {
	c := loadStr(t, "keys:\n  pivot: P\n  filter-matches: z\n")
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Keymap.Picker('P') != keys.Pivot {
		t.Error("the new key does not pivot")
	}
	if c.Keymap.Picker('p') != keys.None {
		t.Error("the old key should be free")
	}
	if c.Keymap.Run('z') != keys.FilterMatches {
		t.Error("filter-matches did not move")
	}
}

// A rebinding that silently does nothing is worse than one that says why.
func TestBadKeySettingsAreReported(t *testing.T) {
	c := loadStr(t, "keys:\n  nonsense: x\n  pivot: pp\n")
	if len(c.Problems) != 2 {
		t.Fatalf("problems = %v", c.Problems)
	}
	joined := strings.Join(c.Problems, "\n")
	if !strings.Contains(joined, "nonsense") || !strings.Contains(joined, "single character") {
		t.Errorf("problems = %v", c.Problems)
	}
}

// A key bound twice shadows rather than doing both, so say so.
func TestCollidingKeysAreReported(t *testing.T) {
	c := loadStr(t, "keys:\n  pivot: a\n")
	found := false
	for _, p := range c.Problems {
		if strings.Contains(p, "both") {
			found = true
		}
	}
	if !found {
		t.Errorf("problems = %v", c.Problems)
	}
}

// `--dump-config` must produce a file taskui can read back unchanged, or it is a template
// that lies about the defaults.
func TestTheDumpedConfigRoundTrips(t *testing.T) {
	c := loadStr(t, DefaultColors().ToYAML())
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors != DefaultColors() {
		t.Error("the dump does not round-trip")
	}
}

// The whole `--dump-config` output, keys block and all, has to load cleanly too.
func TestTheWholeDumpedFileRoundTrips(t *testing.T) {
	c := loadStr(t, DumpConfig())
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors != DefaultColors() {
		t.Error("colours drifted")
	}
	if c.PeekLines != DefaultPeekLines {
		t.Errorf("peek-lines = %d", c.PeekLines)
	}
}

// How big the peek window is depends on what your tools print, so it is settable — and a
// value that would make the state indistinguishable from hidden is reported rather than
// quietly accepted.
func TestPeekLinesIsConfigurableWithinReason(t *testing.T) {
	if DefaultConfig().PeekLines != DefaultPeekLines {
		t.Error("default peek-lines drifted")
	}

	c := loadStr(t, "peek-lines: 12\n")
	if len(c.Problems) != 0 || c.PeekLines != 12 {
		t.Errorf("peek-lines = %d, problems = %v", c.PeekLines, c.Problems)
	}

	zero := loadStr(t, "peek-lines: 0\n")
	if zero.PeekLines != DefaultPeekLines {
		t.Errorf("zero should keep the default: %d", zero.PeekLines)
	}
	if len(zero.Problems) == 0 || !strings.Contains(zero.Problems[0], "at least 1") {
		t.Errorf("problems = %v", zero.Problems)
	}

	huge := loadStr(t, "peek-lines: 5000\n")
	if huge.PeekLines != DefaultPeekLines || len(huge.Problems) == 0 {
		t.Errorf("peek-lines = %d, problems = %v", huge.PeekLines, huge.Problems)
	}
}

// Including the non-name colours.
func TestRgbAndIndexedValuesRoundTrip(t *testing.T) {
	colours := DefaultColors()
	colours.Accent = Color{Kind: KindRGB, R: 0x1e, G: 0x20, B: 0x30}
	colours.Dim = Color{Kind: KindIndexed, Index: 240}

	c := loadStr(t, colours.ToYAML())
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors.Accent != colours.Accent || c.Theme.Colors.Dim != colours.Dim {
		t.Errorf("accent = %+v dim = %+v", c.Theme.Colors.Accent, c.Theme.Colors.Dim)
	}
}

func TestAnEmptyConfigIsValid(t *testing.T) {
	c := loadStr(t, "colors: {}\n")
	if len(c.Problems) != 0 {
		t.Errorf("problems = %v", c.Problems)
	}
	if c.Theme.Colors != DefaultColors() {
		t.Error("theme drifted")
	}
}

// Every field is settable — the table is the whole point, so nothing gets forgotten.
func TestEveryKeyIsConfigurable(t *testing.T) {
	if len(colorFields) <= 20 {
		t.Fatalf("found %d keys", len(colorFields))
	}
	lines := []string{"colors:"}
	for _, f := range colorFields {
		lines = append(lines, "  "+f.key+": red")
	}
	c := loadStr(t, strings.Join(lines, "\n"))
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors.Accent != Red || c.Theme.Colors.ConfirmBg != Red || c.Theme.Colors.Selection != Red {
		t.Error("not every key took")
	}
}
