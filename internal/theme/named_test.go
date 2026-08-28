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
	for _, name := range []string{"default", "90s", "charm", "synthwave", "y2k", "neubrutalism"} {
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

// Most of the shipped themes do not move, and that has to stay true by default: a theme
// that animates costs a redraw for as long as taskui is open.
func TestOnlyTheThemesThatAskForItAnimate(t *testing.T) {
	themesIn(t)
	for _, name := range []string{"default", "90s", "charm", "neubrutalism"} {
		resolved, _ := LoadTheme(name)
		if resolved.Animation.Moves() {
			t.Errorf("%s animates and should not", name)
		}
		if resolved.Animation.MaxLean() != 0 {
			t.Errorf("%s reserves room for a jiggle it does not have", name)
		}
		// A still theme is lit on every frame there is. Getting this wrong would leave the
		// cursor bar switched off in a theme that never asked to blink at all.
		for _, phase := range []int{0, 1, 7, 100} {
			if !resolved.Animation.Lit(phase) {
				t.Errorf("%s went dark at phase %d without asking to", name, phase)
			}
		}
	}
	// synthwave moves its edges and nothing else: it is the loud one to look at, not the one
	// that fidgets.
	synth, _ := LoadTheme("synthwave")
	if !synth.Animation.Moves() {
		t.Error("synthwave should animate")
	}
	if len(synth.Animation.Frames) != 4 {
		t.Errorf("frames = %v", synth.Animation.Frames)
	}
	if synth.Animation.MaxLean() != 0 || !synth.Animation.Lit(1) {
		t.Errorf("synthwave should neither lean nor blink: %+v", synth.Animation)
	}

	// y2k is the one that does everything, and it is meant to be the only one. It leans by
	// exactly one column — two would be a lurch — and it blinks slowly enough that you notice
	// the bar has gone rather than watching it flicker.
	y2k, _ := LoadTheme("y2k")
	if y2k.Animation.MaxLean() != 1 {
		t.Errorf("y2k should lean one column: %v", y2k.Animation.Jiggle)
	}
	dark := 0
	for _, lit := range y2k.Animation.Blink {
		if !lit {
			dark++
		}
	}
	if dark == 0 || dark*4 > len(y2k.Animation.Blink) {
		t.Errorf("y2k should blink, and be lit for most of it: %v", y2k.Animation.Blink)
	}
	// Phase 0 stays lit, or every screenshot and every render test starts on a dark frame.
	if !y2k.Animation.Lit(0) {
		t.Error("the sequence should start lit")
	}

	// One movement, not three. All three sequences are the same length, so they share a
	// period and dip together; lengths that differ would drift and read as unrelated things
	// twitching at the same row. Asserted rather than left to a comment, because a sequence
	// is exactly the kind of thing somebody lengthens by one without noticing what it costs.
	n := len(y2k.Animation.Frames)
	if n < 2 {
		t.Fatalf("y2k should animate its edges: %v", y2k.Animation.Frames)
	}
	if len(y2k.Animation.Jiggle) != n || len(y2k.Animation.Blink) != n {
		t.Errorf("y2k's sequences are %d/%d/%d and must match to stay in step",
			n, len(y2k.Animation.Jiggle), len(y2k.Animation.Blink))
	}

	// …and they dip on the same frames: the lean and the dark are one event, not two.
	for phase := range n {
		leaning := y2k.Animation.Lean(phase) > 0
		if dark := !y2k.Animation.Lit(phase); leaning != dark {
			t.Errorf("phase %d leans=%v dark=%v — the two should move as one", phase, leaning, dark)
		}
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

// The lean is read off the same clock as the frames, so a theme sets its speed by writing
// a longer sequence rather than by asking for a second timer.
func TestTheJiggleCyclesOnItsOwnLength(t *testing.T) {
	a := Animation{Jiggle: []int{0, 0, 1, 1}, Interval: DefaultInterval}
	for phase, want := range map[int]int{0: 0, 1: 0, 2: 1, 3: 1, 4: 0, 6: 1, -1: 1} {
		if got := a.Lean(phase); got != want {
			t.Errorf("Lean(%d) = %d, want %d", phase, got, want)
		}
	}
	if a.MaxLean() != 1 {
		t.Errorf("MaxLean = %d", a.MaxLean())
	}
	// A theme with a jiggle and no frames still moves, and still draws the glyph it was
	// given for the edges it is not animating.
	if !a.Moves() {
		t.Error("a jiggle on its own is movement")
	}
	if got := a.Frame(3, "▌"); got != "▌" {
		t.Errorf("Frame = %q", got)
	}
}

// Turning the animation off has to take the lean with it, or a still theme would go on
// reserving a column for a wobble that never comes.
func TestZeroIntervalStopsTheLeanToo(t *testing.T) {
	a := Animation{Jiggle: []int{0, 1}}
	if a.Moves() || a.Lean(1) != 0 || a.MaxLean() != 0 {
		t.Errorf("a zero interval should leave it still: %+v", a)
	}
}

// One digit per frame, and nothing else.
func TestABadJiggleIsReported(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "loud", "extends: default\nanimation:\n  selection-jiggle: \"0019\"\n")
	write(t, dir, "prose", "extends: default\nanimation:\n  selection-jiggle: \"wobble\"\n")
	write(t, dir, "short", "extends: default\nanimation:\n  selection-jiggle: \"1\"\n")

	for name, want := range map[string]string{
		// 9 columns is a margin, not a wobble.
		"loud":  "one digit",
		"prose": "one digit",
		"short": "at least two",
	} {
		resolved, problems := LoadTheme(name)
		if len(problems) != 1 || !strings.Contains(problems[0], want) {
			t.Errorf("%s: problems = %v", name, problems)
		}
		if resolved.Animation.MaxLean() != 0 {
			t.Errorf("%s: a refused jiggle should leave the theme still", name)
		}
	}
}

// The two halves come apart, and they come apart from your own config rather than only from
// a theme file — keeping the lean and dropping the bounce should not be a fork of y2k.
func TestEitherHalfOfTheAnimationCanBeClearedFromConfig(t *testing.T) {
	themesIn(t)

	still := loadStr(t, "theme: y2k\nanimation:\n  selection-frames: \"\"\n").Theme.Animation
	if len(still.Frames) != 0 {
		t.Errorf("the bounce should be gone: %v", still.Frames)
	}
	if still.MaxLean() != 1 || !still.Moves() {
		t.Errorf("the jiggle should have survived: %+v", still)
	}

	straight := loadStr(t, "theme: y2k\nanimation:\n  selection-jiggle: \"\"\n").Theme.Animation
	if straight.MaxLean() != 0 {
		t.Errorf("the lean should be gone, and its reserved column with it: %+v", straight)
	}
	// However long y2k's sequence happens to be — pinning the count here just makes this
	// test fail every time somebody retimes the theme.
	if len(straight.Frames) < 2 || !straight.Moves() {
		t.Errorf("the bounce should have survived: %+v", straight)
	}
}

// The blink is ours, not the terminal's, so it obeys the same rule as the rest of the
// block: the sequence's length is the speed.
func TestTheBlinkRunsOnTheSameClockAsEverythingElse(t *testing.T) {
	a := Animation{Blink: []bool{true, true, false}, Interval: DefaultInterval}
	for phase, want := range map[int]bool{0: true, 1: true, 2: false, 3: true, 5: false, -1: false} {
		if got := a.Lit(phase); got != want {
			t.Errorf("Lit(%d) = %v, want %v", phase, got, want)
		}
	}
	if !a.Moves() {
		t.Error("a blink on its own is movement")
	}
	// It costs no columns and no rows — it is the same row, drawn without its highlight.
	if a.MaxLean() != 0 {
		t.Errorf("the blink should reserve nothing: %d", a.MaxLean())
	}

	// A theme that does not blink is lit on every frame, or every existing theme just went
	// dark half the time.
	for _, still := range []Animation{{}, {Frames: []string{"▀", "▄"}, Interval: DefaultInterval}} {
		if !still.Lit(0) || !still.Lit(7) {
			t.Errorf("a theme with no blink is always lit: %+v", still)
		}
	}
	// …and so is one whose animation was switched off.
	off := Animation{Blink: []bool{true, false}}
	if !off.Lit(1) {
		t.Error("a zero interval should leave the highlight on, not stuck off")
	}
}

func TestABadBlinkIsReported(t *testing.T) {
	dir := themesIn(t)
	write(t, dir, "morse", "extends: default\nanimation:\n  selection-blink: \"1102\"\n")
	write(t, dir, "solid", "extends: default\nanimation:\n  selection-blink: \"1\"\n")

	for name, want := range map[string]string{"morse": "1 for lit", "solid": "at least two"} {
		resolved, problems := LoadTheme(name)
		if len(problems) != 1 || !strings.Contains(problems[0], want) {
			t.Errorf("%s: problems = %v", name, problems)
		}
		if !resolved.Animation.Lit(1) {
			t.Errorf("%s: a refused sequence should leave the highlight on", name)
		}
	}
}

// y2k pulses rather than flashes, and the colour it pulses to has to be its own — inheriting
// `default` would silently turn the pulse back into the bar dropping out.
func TestY2kPulsesBetweenTwoColours(t *testing.T) {
	themesIn(t)
	y2k, _ := LoadTheme("y2k")
	if y2k.Colors.SelectionBlink.IsDefault() {
		t.Fatal("y2k should name a blink colour")
	}
	if y2k.Colors.SelectionBlink == y2k.Colors.Selection {
		t.Error("pulsing to the colour it already is, is not pulsing")
	}
	// Every other shipped theme leaves it alone, so a theme that does not blink cannot
	// acquire a second selection colour by accident.
	for _, name := range []string{"default", "90s", "charm", "synthwave", "neubrutalism"} {
		resolved, _ := LoadTheme(name)
		if !resolved.Colors.SelectionBlink.IsDefault() {
			t.Errorf("%s should not name a blink colour: %+v", name, resolved.Colors.SelectionBlink)
		}
	}
}
