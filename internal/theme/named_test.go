package theme

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// themesIn points the theme search at a scratch directory, so a test never reads — or
// writes — the themes somebody actually has.
func themesIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "taskui", "themes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The themes that ship have to load cleanly, or the first thing a new user sees is an
// error about the look they did not choose.
func TestEveryBuiltInThemeLoadsWithoutProblems(t *testing.T) {
	themesIn(t)
	names := ListThemes()
	if len(names) < 3 {
		t.Fatalf("expected the shipped themes, got %v", names)
	}
	for _, name := range names {
		resolved, problems := LoadTheme(name)
		if len(problems) != 0 {
			t.Errorf("%s: %v", name, problems)
		}
		if resolved.Name != name {
			t.Errorf("%s resolved as %q", name, resolved.Name)
		}
		if resolved.Glyphs.Wordmark == "" {
			t.Errorf("%s has no wordmark", name)
		}
	}
}

// The point of `extends`: say what is different, inherit the rest.
func TestAThemeInheritsFromTheOneItExtends(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "mine", "extends: default\ncolors:\n  accent: magenta\n")

	resolved, problems := LoadTheme("mine")
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Colors.Accent != Magenta {
		t.Errorf("accent = %+v", resolved.Colors.Accent)
	}
	if resolved.Colors.StatusOk != DefaultColors().StatusOk {
		t.Error("everything else should have come from the parent")
	}
	if resolved.Glyphs.FoldOpen != DefaultGlyphs().FoldOpen {
		t.Error("including the glyphs")
	}
}

// A chain, so a theme can extend a theme that extends a theme.
func TestExtendsFollowsAWholeChain(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "base", "extends: default\ncolors:\n  accent: red\n  dim: blue\n")
	write(t, dir, "top", "extends: base\ncolors:\n  accent: green\n")

	resolved, problems := LoadTheme("top")
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Colors.Accent != Green {
		t.Error("the child should win")
	}
	if resolved.Colors.Dim != Blue {
		t.Error("and the middle of the chain should still be applied")
	}
}

// A theme that extends itself is a mistake, not a hang.
func TestAnExtendsCycleIsReportedRatherThanFollowed(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "loop", "extends: loop\ncolors:\n  accent: red\n")

	resolved, problems := LoadTheme("loop")
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, " "), "extends itself") {
		t.Errorf("problems = %v", problems)
	}
	// …and it still hands back something usable.
	if resolved.Colors.Accent != Red {
		t.Error("what it did manage to read should still apply")
	}
}

// Yours shadows a built-in of the same name, so nothing that ships is a decision you are
// stuck with.
func TestALocalThemeShadowsABuiltInOfTheSameName(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "90s", "colors:\n  accent: green\n")

	resolved, _ := LoadTheme("90s")
	if resolved.Colors.Accent != Green {
		t.Errorf("accent = %+v — the local file should have won", resolved.Colors.Accent)
	}
	// Listed once, not twice.
	seen := 0
	for _, name := range ListThemes() {
		if name == "90s" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("listed %d times", seen)
	}
}

// A name nobody has says so, and says what there is instead.
func TestAnUnknownThemeNamesWhatIsAvailable(t *testing.T) {
	themesIn(t)
	_, problems := LoadTheme("nonesuch")
	if len(problems) != 1 {
		t.Fatalf("problems = %v", problems)
	}
	if !strings.Contains(problems[0], "nonesuch") || !strings.Contains(problems[0], "default") {
		t.Errorf("problem = %q", problems[0])
	}
}

// The layout arithmetic is built on knowing how wide a glyph is before it is drawn, so a
// glyph that is not one column is refused rather than allowed to shift a column off the
// edge of somebody else's terminal.
func TestGlyphsMustBeOneColumn(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "wide", "extends: default\nglyphs:\n  rail: \"=>\"\n  fold-open: \"▾\"\n")

	resolved, problems := LoadTheme("wide")
	if len(problems) != 1 || !strings.Contains(problems[0], "one column") {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Glyphs.Rail != DefaultGlyphs().Rail {
		t.Error("the bad one should have been left alone")
	}
	if resolved.Glyphs.FoldOpen != "▾" {
		t.Error("and the good one should still have applied")
	}
}

// The wordmark is a label, not a glyph: it is measured, so it may be as wide as it likes.
func TestTheWordmarkMayBeAnyWidth(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "loud", "extends: default\nglyphs:\n  wordmark: \"░▒▓ TASKUI ▓▒░\"\n")

	resolved, problems := LoadTheme("loud")
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Glyphs.Wordmark != "░▒▓ TASKUI ▓▒░" {
		t.Errorf("wordmark = %q", resolved.Glyphs.Wordmark)
	}
}

// A typo in a glyph name is a typo, not a line that quietly does nothing.
func TestAnUnknownGlyphIsReported(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "typo", "extends: default\nglyphs:\n  fold-opne: \"▾\"\n")

	_, problems := LoadTheme("typo")
	if len(problems) != 1 || !strings.Contains(problems[0], "fold-opne") {
		t.Errorf("problems = %v", problems)
	}
}

// `--dump-theme` has to produce a file taskui reads back unchanged, or it is a starting
// point that lies about where it starts.
func TestADumpedThemeRoundTrips(t *testing.T) {
	dir := themesIn(t)
	for _, name := range []string{"default", "90s", "charm", "synthwave"} {
		text, problems := DumpTheme(name)
		if len(problems) != 0 {
			t.Fatalf("%s: %v", name, problems)
		}
		write(t, dir, "copy-of-"+name, text)

		original, _ := LoadTheme(name)
		reloaded, problems := LoadTheme("copy-of-" + name)
		if len(problems) != 0 {
			t.Fatalf("%s round trip: %v", name, problems)
		}
		if reloaded.Colors != original.Colors {
			t.Errorf("%s: colours drifted through the dump", name)
		}
		if reloaded.Glyphs != original.Glyphs {
			t.Errorf("%s: glyphs drifted through the dump", name)
		}
		if !reflect.DeepEqual(reloaded.Animation, original.Animation) {
			t.Errorf("%s: animation drifted through the dump: %+v vs %+v",
				name, reloaded.Animation, original.Animation)
		}
	}
}

// Picking a theme and then changing one thing about it is two lines, not a fork.
func TestConfigOverridesLandOnTopOfTheChosenTheme(t *testing.T) {
	themesIn(t)
	c := loadStr(t, "theme: 90s\ncolors:\n  accent: green\nglyphs:\n  rail: \"|\"\n")
	if len(c.Problems) != 0 {
		t.Fatalf("problems = %v", c.Problems)
	}
	if c.Theme.Colors.Accent != Green {
		t.Error("the config's own colour should win over the theme's")
	}
	if c.Theme.Glyphs.Rail != "|" {
		t.Errorf("rail = %q", c.Theme.Glyphs.Rail)
	}
	// …and everything it did not mention still comes from the theme.
	ninety, _ := LoadTheme("90s")
	if c.Theme.Glyphs.Wordmark != ninety.Glyphs.Wordmark {
		t.Errorf("wordmark = %q", c.Theme.Glyphs.Wordmark)
	}
}

// --- animation --------------------------------------------------------------------

// Three of the four shipped themes do not move, and that has to stay true by default: a
// theme that animates costs a redraw for as long as taskui is open.
func TestOnlyTheThemeThatAsksForItAnimates(t *testing.T) {
	themesIn(t)
	for _, name := range []string{"default", "90s", "charm"} {
		resolved, _ := LoadTheme(name)
		if resolved.Animation.Moves() {
			t.Errorf("%s animates and should not", name)
		}
	}
	synth, _ := LoadTheme("synthwave")
	if !synth.Animation.Moves() {
		t.Error("synthwave should animate")
	}
	if len(synth.Animation.Frames) != 4 {
		t.Errorf("frames = %v", synth.Animation.Frames)
	}
}

// Frames cycle, and a phase outside the sequence wraps rather than panicking.
func TestFrameCyclesAndWraps(t *testing.T) {
	a := Animation{Frames: []string{"▀", "█", "▄"}, Interval: DefaultInterval}
	for phase, want := range map[int]string{0: "▀", 1: "█", 2: "▄", 3: "▀", 7: "█", -1: "▄"} {
		if got := a.Frame(phase, "x"); got != want {
			t.Errorf("Frame(%d) = %q, want %q", phase, got, want)
		}
	}
	// A theme that does not move always draws the glyph it was given.
	still := Animation{}
	if got := still.Frame(3, "▌"); got != "▌" {
		t.Errorf("got %q", got)
	}
}

// The same one-column rule the glyphs live under, for the same reason. Frames are split
// per character, so the only way to be too wide is to be a double-width one.
func TestAnimationFramesMustBeOneColumn(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "wide", "extends: default\nanimation:\n  selection-frames: \"▀🙂▄\"\n")

	resolved, problems := LoadTheme("wide")
	if len(problems) != 1 || !strings.Contains(problems[0], "one column") {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Animation.Moves() {
		t.Error("a bad sequence should leave the theme still")
	}
}

// One frame is not a sequence.
func TestASingleFrameIsRefused(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "one", "extends: default\nanimation:\n  selection-frames: \"█\"\n")

	_, problems := LoadTheme("one")
	if len(problems) != 1 || !strings.Contains(problems[0], "at least two") {
		t.Errorf("problems = %v", problems)
	}
}

// Faster than the floor is a strobe; slower than the ceiling is not motion.
func TestTheIntervalIsBounded(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "strobe", "extends: default\nanimation:\n  selection-frames: \"▀▄\"\n  interval-ms: 5\n")
	write(t, dir, "glacial", "extends: default\nanimation:\n  selection-frames: \"▀▄\"\n  interval-ms: 9000\n")

	for _, name := range []string{"strobe", "glacial"} {
		resolved, problems := LoadTheme(name)
		if len(problems) != 1 {
			t.Errorf("%s: problems = %v", name, problems)
		}
		// The frames still took; only the timing was refused, so it falls back to the
		// default rather than to nothing.
		if resolved.Animation.Interval != DefaultInterval {
			t.Errorf("%s: interval = %v", name, resolved.Animation.Interval)
		}
	}
}

// Zero is how you turn it off in a theme that extends one which moves.
func TestZeroIntervalStopsAnInheritedAnimation(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "still", "extends: synthwave\nanimation:\n  interval-ms: 0\n")

	resolved, problems := LoadTheme("still")
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if resolved.Animation.Moves() {
		t.Error("should have stopped")
	}
	// …without losing the rest of what it inherited.
	if resolved.Glyphs.Wordmark != "▄▀▄ TASKUI ▄▀▄" {
		t.Errorf("wordmark = %q", resolved.Glyphs.Wordmark)
	}
}

// A typo is a typo.
func TestAnUnknownAnimationKeyIsReported(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "typo", "extends: default\nanimation:\n  selection-frame: \"▀▄\"\n")

	_, problems := LoadTheme("typo")
	if len(problems) != 1 || !strings.Contains(problems[0], "selection-frame") {
		t.Errorf("problems = %v", problems)
	}
}
