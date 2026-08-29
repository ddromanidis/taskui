// Package theme loads colours from `config.yaml`.
//
// Every colour the UI draws comes from here rather than a literal at the call site, so
// "make it configurable" is a matter of adding a field rather than hunting through the
// rendering code. Anything the file does not mention keeps its default, which means a
// two-line config is valid and an empty one is a no-op.
package theme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/viper"

	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/pivot"
)

// DefaultPeekLines is how many lines a peeking task shows.
//
// Five is enough for a Go test failure's assertion and its file:line, or a compiler error
// and its note — the shapes that actually decide whether you open the thing. Configurable
// because "enough" is a property of the tools you run, not of taskui.
const DefaultPeekLines = 5

// ConfigPath is `$XDG_CONFIG_HOME/taskui/config.yaml`, else `~/.config/taskui/config.yaml`.
func ConfigPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "taskui/config.yaml")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".config/taskui/config.yaml")
}

type Kind int

const (
	// KindDefault means whatever the terminal is already using.
	KindDefault Kind = iota
	// KindNamed is one of the sixteen ANSI colours, so it follows the terminal's own
	// scheme.
	KindNamed
	// KindRGB pins the colour exactly.
	KindRGB
	// KindIndexed is a 0–255 palette index.
	KindIndexed
)

// Color is a colour written as a name, a `#rrggbb`, or a 0–255 terminal palette index.
//
// Names are the sixteen ANSI colours, so a value like `blue` follows whatever the
// terminal's own scheme says blue is — which is usually what someone means when they set a
// colour in a terminal program. `#rrggbb` opts out of that and pins it exactly.
type Color struct {
	Kind Kind
	// ANSI is the 0–15 slot for KindNamed.
	ANSI    uint8
	R, G, B uint8
	Index   uint8
}

func named(ansi uint8) Color  { return Color{Kind: KindNamed, ANSI: ansi} }
func rgb(r, g, b uint8) Color { return Color{Kind: KindRGB, R: r, G: g, B: b} }

// Default is reverse-video's stand-in and the "leave it alone" value.
var Default = Color{Kind: KindDefault}

var (
	Black         = named(0)
	Red           = named(1)
	Green         = named(2)
	Yellow        = named(3)
	Blue          = named(4)
	Magenta       = named(5)
	Cyan          = named(6)
	Gray          = named(7)
	DarkGray      = named(8)
	BrightRed     = named(9)
	BrightGreen   = named(10)
	BrightYellow  = named(11)
	BrightBlue    = named(12)
	BrightMagenta = named(13)
	BrightCyan    = named(14)
	BrightWhite   = named(15)
)

// IsDefault says whether this colour means "whatever the terminal is using".
func (c Color) IsDefault() bool { return c.Kind == KindDefault }

// Lip converts to a lipgloss colour. Only meaningful when IsDefault is false.
func (c Color) Lip() lipgloss.TerminalColor {
	switch c.Kind {
	case KindNamed:
		return lipgloss.Color(strconv.Itoa(int(c.ANSI)))
	case KindIndexed:
		return lipgloss.Color(strconv.Itoa(int(c.Index)))
	case KindRGB:
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B))
	default:
		return lipgloss.NoColor{}
	}
}

// grayName is ANSI 7, which is also what `white` and `grey` mean here.
const grayName = "gray"

var colorNames = []struct {
	name  string
	color Color
}{
	{"black", Black},
	{"red", Red},
	{"green", Green},
	{"yellow", Yellow},
	{"blue", Blue},
	{"magenta", Magenta},
	{"cyan", Cyan},
	{grayName, Gray},
	{"darkgray", DarkGray},
	{"lightred", BrightRed},
	{"lightgreen", BrightGreen},
	{"lightyellow", BrightYellow},
	{"lightblue", BrightBlue},
	{"lightmagenta", BrightMagenta},
	{"lightcyan", BrightCyan},
	{"lightwhite", BrightWhite},
}

// aliases are the spellings that mean the same slot.
var aliases = map[string]string{
	"purple":        "magenta",
	"grey":          grayName,
	"white":         grayName,
	"darkgrey":      "darkgray",
	"brightred":     "lightred",
	"brightgreen":   "lightgreen",
	"brightyellow":  "lightyellow",
	"brightblue":    "lightblue",
	"brightmagenta": "lightmagenta",
	"brightcyan":    "lightcyan",
	"brightwhite":   "lightwhite",
}

// ParseColor reads a colour value from the config.
func ParseColor(text string) (Color, bool) {
	t := strings.ToLower(strings.TrimSpace(text))

	if hex, ok := strings.CutPrefix(t, "#"); ok {
		if len(hex) != 6 {
			return Color{}, false
		}
		var channel [3]uint8
		for i := range channel {
			n, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
			if err != nil {
				return Color{}, false
			}
			channel[i] = uint8(n)
		}
		return rgb(channel[0], channel[1], channel[2]), true
	}

	if n, err := strconv.ParseUint(t, 10, 8); err == nil {
		return Color{Kind: KindIndexed, Index: uint8(n)}, true
	}

	key := strings.NewReplacer("-", "", "_", "").Replace(t)
	if key == "reset" || key == "default" {
		return Default, true
	}
	if canonical, ok := aliases[key]; ok {
		key = canonical
	}
	for _, c := range colorNames {
		if c.name == key {
			return c.color, true
		}
	}
	return Color{}, false
}

// ColorName renders a colour back to something ParseColor accepts.
func ColorName(c Color) string {
	switch c.Kind {
	case KindRGB:
		return fmt.Sprintf(`"#%02x%02x%02x"`, c.R, c.G, c.B)
	case KindIndexed:
		return strconv.Itoa(int(c.Index))
	case KindNamed:
		switch c.ANSI {
		case 7:
			return grayName
		case 8:
			return "dark-gray"
		}
		if c.ANSI >= 9 {
			return "bright-" + strings.TrimPrefix(colorNames[c.ANSI].name, "light")
		}
		return colorNames[c.ANSI].name
	default:
		return "default"
	}
}

// Colors is every colour the UI uses.
type Colors struct {
	Accent         Color
	Dim            Color
	Faint          Color
	Rule           Color
	Text           Color
	Selection      Color
	SelectionLight Color
	SelectionShade Color
	SelectionBlink Color

	Mode   Color
	Alias  Color
	Danger Color

	StatusOk      Color
	StatusRunning Color
	StatusFailed  Color
	StatusSkipped Color
	StatusPending Color

	Command Color

	Search  Color
	MatchFg Color
	MatchBg Color

	Location Color
	Marked   Color

	DiffAdded   Color
	DiffRemoved Color

	Notice      Color
	Stored      Color
	Interactive Color
	WarningFg   Color
	WarningBg   Color
	ConfirmFg   Color
	ConfirmBg   Color
}

// field ties a config key to its field and its documentation, so `--dump-config`, the
// loader and the "every key is configurable" guarantee all read from one table.
type colorField struct {
	key string
	doc string
	at  func(*Colors) *Color
}

var colorFields = []colorField{
	{"accent", "The `taskui` word mark.", func(t *Colors) *Color { return &t.Accent }},
	{"dim", "Secondary text: counts, descriptions, key hints.", func(t *Colors) *Color { return &t.Dim }},
	{
		"faint",
		"Chrome: tree guides, fold glyphs, line numbers. Everything that positions content without being content. Defaults to ANSI bright black, which some dark colourschemes put a hair off the background — losing a tree guide costs you an alignment cue, not a word.",
		func(t *Colors) *Color { return &t.Faint },
	},
	{"rule", "The hairlines under the header and above the footer.", func(t *Colors) *Color { return &t.Rule }},
	{"text", "Ordinary output and task names.", func(t *Colors) *Color { return &t.Text }},
	{
		"selection",
		"Highlighted row. `default` means reverse video, which is legible in every terminal theme; any other colour is used as a background instead.",
		func(t *Colors) *Color { return &t.Selection },
	},
	{
		"selection-light",
		"The lit edge down the left of the selected row.",
		func(t *Colors) *Color { return &t.SelectionLight },
	},
	{
		"selection-shade",
		"The shaded edge down its right. With a `selection-shade` glyph set, the two together read as an extruded bar rather than a highlighted line.",
		func(t *Colors) *Color { return &t.SelectionShade },
	},
	{
		"selection-blink",
		"What the highlighted row turns on the dark frames of an `animation.selection-blink`. Left `default` the bar drops out instead, which is a flash; give it a colour and the bar pulses between the two rather than going away.",
		func(t *Colors) *Color { return &t.SelectionBlink },
	},

	{"mode", "The active pivot in the picker header.", func(t *Colors) *Color { return &t.Mode }},
	{"alias", "Task aliases.", func(t *Colors) *Color { return &t.Alias }},
	{"danger", "The ⚠ marker on tasks that need a confirmation.", func(t *Colors) *Color { return &t.Danger }},

	{"status-ok", "A task that succeeded.", func(t *Colors) *Color { return &t.StatusOk }},
	{"status-running", "A task still going.", func(t *Colors) *Color { return &t.StatusRunning }},
	{"status-failed", "A task that failed.", func(t *Colors) *Color { return &t.StatusFailed }},
	{"status-skipped", "A task never reached.", func(t *Colors) *Color { return &t.StatusSkipped }},
	{"status-pending", "A task not started yet.", func(t *Colors) *Color { return &t.StatusPending }},

	{"command", "go-task's own `task: [name] <cmd>` echo.", func(t *Colors) *Color { return &t.Command }},

	{"search", "The search and filter prompts.", func(t *Colors) *Color { return &t.Search }},
	{"match-fg", "Foreground of a highlighted match.", func(t *Colors) *Color { return &t.MatchFg }},
	{"match-bg", "Background of a highlighted match.", func(t *Colors) *Color { return &t.MatchBg }},

	{
		"location",
		"A `file:line` in captured output — the part `e` opens in your editor. Underlined as well as coloured, so it reads as reachable rather than merely tinted.",
		func(t *Colors) *Color { return &t.Location },
	},

	{"marked", "A task chosen to run alongside others.", func(t *Colors) *Color { return &t.Marked }},

	{"diff-added", "A line only the newer run printed.", func(t *Colors) *Color { return &t.DiffAdded }},
	{"diff-removed", "A line only the older run printed.", func(t *Colors) *Color { return &t.DiffRemoved }},

	{"notice", "Status messages along the bottom.", func(t *Colors) *Color { return &t.Notice }},
	{"stored", "Marks a run loaded from history.", func(t *Colors) *Color { return &t.Stored }},
	{"interactive", "The interactive-mode marker and input bar.", func(t *Colors) *Color { return &t.Interactive }},
	{"warning-fg", "Foreground of the waiting-for-input bar.", func(t *Colors) *Color { return &t.WarningFg }},
	{"warning-bg", "Background of the waiting-for-input bar.", func(t *Colors) *Color { return &t.WarningBg }},
	{"confirm-fg", "Foreground of the production confirmation bar.", func(t *Colors) *Color { return &t.ConfirmFg }},
	{"confirm-bg", "Background of the production confirmation bar.", func(t *Colors) *Color { return &t.ConfirmBg }},
}

func DefaultColors() Colors {
	return Colors{
		Accent: Cyan,
		Dim:    Gray,
		Faint:  DarkGray,
		Rule:   DarkGray,
		Text:   Default,
		// A fixed background colour looks deliberate on the theme it was chosen against
		// and vanishes on every other one, so the default is reverse video.
		Selection: Default,
		// The lit edge matches the accent by default, which is what the rail was drawn in
		// before it had a role of its own.
		SelectionLight: Cyan,
		SelectionShade: DarkGray,
		// No second colour, so a theme that blinks flashes the bar off rather than
		// alternating. Naming one opts into the pulse.
		SelectionBlink: Default,

		Mode:   Yellow,
		Alias:  Blue,
		Danger: Red,

		StatusOk:      Green,
		StatusRunning: Yellow,
		StatusFailed:  Red,
		StatusSkipped: Gray,
		StatusPending: Gray,

		Command: Blue,

		Search:  Magenta,
		MatchFg: Black,
		MatchBg: Magenta,

		// Cyan rather than the accent: a location is a link, and it has to be legible on
		// a failure line that is already red without competing with it for the same job.
		Location: Cyan,
		Marked:   Yellow,

		DiffAdded:   Green,
		DiffRemoved: Red,

		Notice:      Yellow,
		Stored:      Blue,
		Interactive: Green,
		WarningFg:   Black,
		WarningBg:   Yellow,
		ConfirmFg:   Black,
		ConfirmBg:   Red,
	}
}

// applyColors overlays a `colors:` block, reporting anything it could not use.
func applyColors(t *Colors, block map[string]string) []string {
	if len(block) == 0 {
		return nil
	}
	var bad []string
	used := map[string]bool{}
	for _, f := range colorFields {
		text, ok := block[f.key]
		if !ok {
			continue
		}
		used[f.key] = true
		c, ok := ParseColor(text)
		if !ok {
			bad = append(bad, fmt.Sprintf("%s: %q is not a colour", f.key, text))
			continue
		}
		*f.at(t) = c
	}
	// A typo'd key is a typo, not a silently ignored line.
	var unknown []string
	for k := range block {
		if !used[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		bad = append(bad, fmt.Sprintf("colors: `%s` is not a colour setting", k))
	}
	return bad
}

// ToYAML renders an annotated `config.yaml` holding this theme's current values.
//
// Emitted with real values rather than blanks so `--dump-config > config.yaml` gives you a
// file you can edit, not a form to fill in.
func (t Colors) ToYAML() string {
	var b strings.Builder
	b.WriteString("colors:\n")
	for _, f := range colorFields {
		fmt.Fprintf(&b, "  # %s\n  %s: %s\n", f.doc, f.key, ColorName(*f.at(&t)))
	}
	return b.String()
}

// Theme is a whole look: what colour everything is, and what it is drawn with.
//
// Two blocks rather than one flat table because they answer different questions and fail
// differently — a bad colour costs you a colour, a bad glyph could cost you a column.
type Theme struct {
	// Name is the theme this was resolved from, for the header and for error messages.
	Name      string
	Colors    Colors
	Glyphs    Glyphs
	Animation Animation
}

func DefaultTheme() Theme {
	return Theme{Name: DefaultThemeName, Colors: DefaultColors(), Glyphs: DefaultGlyphs()}
}

type Config struct {
	Theme  Theme
	Keymap *keys.Keymap
	// PeekLines is how many lines a peeking task shows.
	PeekLines int
	// Pivots are extra groupings from the config file, appended after the built-ins.
	Pivots []pivot.Spec
	// Order is what sits above what: which key rows are compared on, whether subgroups sink
	// below the plain tasks beside them, and anything pinned to the top.
	Order pivot.Order
	// Bell is when to ring the terminal for a run you are not watching.
	Bell BellMode
	// Mouse says whether to ask the terminal for mouse events, which is what makes the wheel
	// scroll taskui.
	Mouse bool
	// Problems lists anything wrong with the file, surfaced in the UI rather than
	// swallowed — a colour that silently does nothing is worse than one that says why.
	Problems []string
}

func DefaultConfig() Config {
	return Config{
		Theme:     DefaultTheme(),
		Keymap:    keys.NewKeymap(),
		PeekLines: DefaultPeekLines,
		Bell:      BellUnwatched,
		Mouse:     DefaultMouse,
	}
}

// DefaultMouse has the wheel scroll taskui.
//
// On, because a wheel that scrolls the program you are looking at is what a wheel does
// everywhere else, and because the alternative is not "the terminal scrolls instead" but
// "the terminal scrolls something you cannot see": taskui draws on the alternate screen,
// so the scrollback the wheel reaches for is whatever was on the screen before it started.
//
// The cost is real, which is why it is a key rather than a constant. A terminal that is
// forwarding mouse events to a program is not selecting text with them, so drag-to-select
// over taskui's output needs the terminal's own override — shift in most of them, option on
// macOS — until you turn this off.
const DefaultMouse = true

// BellMode says when a finished run should ring the terminal.
type BellMode int

const (
	// BellUnwatched rings for a run that finishes while you are looking at something else.
	// The default, and deliberately narrow: a run you are watching finish needs no bell, and
	// the whole reason to want one is that you walked away from a long one.
	BellUnwatched BellMode = iota
	// BellNever is silence.
	BellNever
	// BellFailed rings only for the ones that broke.
	BellFailed
)

func (b BellMode) String() string {
	switch b {
	case BellNever:
		return "off"
	case BellFailed:
		return "failed"
	default:
		return "on"
	}
}

// parseOnOff reads the spellings of yes and no.
//
// Shared by every switch in this file, so `mouse: yes` and `bell: yes` cannot drift into
// meaning different things — and so a key added later does not have to guess which of the
// three spellings the others accepted.
func parseOnOff(text string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "on", "true", "yes":
		return true, true
	case "off", "false", "no":
		return false, true
	default:
		return false, false
	}
}

// ParseBell reads the setting. Reported rather than guessed at: a bell that silently never
// rings is indistinguishable from one that is broken.
func ParseBell(text string) (BellMode, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "unwatched":
		return BellUnwatched, true
	case "never":
		return BellNever, true
	case "failed", "failures":
		return BellFailed, true
	}
	// `on` is the unwatched bell rather than every bell there could be: it is the default,
	// and what somebody turning the bell on is asking for is the default one.
	if on, ok := parseOnOff(text); ok {
		if on {
			return BellUnwatched, true
		}
		return BellNever, true
	}
	return BellUnwatched, false
}

// readPivots reads the `pivots:` block.
//
// Parsed here because this is where config lives, and compiled in the pivot package because
// that is where the meaning is. Anything wrong with an entry costs that entry and says why —
// a grouping that silently did not appear would look like the feature was missing.
func readPivots(v *viper.Viper) ([]pivot.Spec, []string) {
	raw, ok := v.Get("pivots").([]any)
	if !ok {
		if v.IsSet("pivots") {
			return nil, []string{"pivots: expected a list"}
		}
		return nil, nil
	}

	var out []pivot.Spec
	var problems []string
	seen := map[string]bool{}
	for i, entry := range raw {
		fields, ok := entry.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("pivots[%d]: expected a mapping", i))
			continue
		}
		spec := pivot.Spec{
			Name:    stringField(fields, "name"),
			Regex:   stringField(fields, "regex"),
			Path:    stringsField(fields, "path"),
			Command: stringsField(fields, "command"),
		}
		switch {
		case spec.Name == "":
			problems = append(problems, fmt.Sprintf("pivots[%d]: needs a name", i))
			continue
		// The built-ins are not replaceable: `p` cycles by name, and two entries answering
		// to `domain` would make which one you got depend on the order of a map.
		case spec.Name == DomainPivot || spec.Name == VerbPivot || spec.Name == FilePivot:
			problems = append(problems, fmt.Sprintf("pivots: `%s` is built in", spec.Name))
			continue
		case seen[spec.Name]:
			problems = append(problems, fmt.Sprintf("pivots: `%s` is defined twice", spec.Name))
			continue
		}
		seen[spec.Name] = true
		out = append(out, spec)
	}
	return out, problems
}

// readOrder reads the three keys that decide what sits above what.
//
// Three keys rather than one nested block, because they are three independent answers and a
// block would imply you had to give all of them. Each is optional and each is validated —
// an ordering that silently stayed on the default is indistinguishable from one that does
// not work.
func readOrder(v *viper.Viper) (pivot.Order, []string) {
	var order pivot.Order
	var problems []string

	if v.IsSet("sort") {
		if by, ok := pivot.ParseBy(v.GetString("sort")); ok {
			order.By = by
		} else {
			problems = append(problems, fmt.Sprintf("sort: %q is not one of %s",
				v.GetString("sort"), strings.Join(pivot.OrderNames(), ", ")))
		}
	}

	if v.IsSet("groups") {
		switch value := strings.ToLower(strings.TrimSpace(v.GetString("groups"))); value {
		case "last":
			order.Interleave = false
		case "mixed":
			order.Interleave = true
		default:
			problems = append(problems,
				fmt.Sprintf("groups: %q is not `last` or `mixed`", v.GetString("groups")))
		}
	}

	if v.IsSet("pin") {
		for _, pattern := range v.GetStringSlice("pin") {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				order.Pins = append(order.Pins, pattern)
			}
		}
	}

	return order, problems
}

// The built-in pivot names, repeated here so the config validator does not have to
// construct every pivot to find out what they are called.
const (
	DomainPivot = pivot.DomainName
	VerbPivot   = pivot.VerbName
	FilePivot   = pivot.FileName
)

func stringField(fields map[string]any, key string) string {
	if s, ok := fields[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func stringsField(fields map[string]any, key string) []string {
	switch v := fields[key].(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// applyKeys points actions at different keys. Unknown names and unreadable values are
// reported rather than ignored — a rebinding that silently does nothing is worse than one
// that says why.
func applyKeys(keymap *keys.Keymap, table map[string]string) []string {
	var problems []string
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := table[name]
		action, ok := keys.ActionByName(name)
		if !ok {
			problems = append(problems, fmt.Sprintf("keys: `%s` is not an action", name))
			continue
		}
		chord, err := keys.ParseChord(value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("keys: %s: %s", name, err))
			continue
		}
		keymap.Rebind(action, chord)
	}
	return problems
}

// Load reads path. A missing file is not an error; it is the normal case.
func Load(path string) Config {
	if _, err := os.Stat(path); err != nil {
		return DefaultConfig()
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		config := DefaultConfig()
		config.Problems = append(config.Problems, fmt.Sprintf("%s: %v", path, err))
		return config
	}
	return FromViper(v)
}

// Setup points a Viper at taskui's config, wherever it lives, and binds the environment.
//
// A file is the ordinary way to configure this, but a Viper also gives you `TASKUI_*`
// overrides for free — which is the difference between "edit your config to see what a
// theme looks like" and `TASKUI_PEEK_LINES=20 taskui`.
func Setup(v *viper.Viper, explicit string) error {
	if explicit != "" {
		v.SetConfigFile(explicit)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(filepath.Dir(ConfigPath()))
	}
	v.SetEnvPrefix("TASKUI")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()
	// Bound explicitly: AutomaticEnv only sees a key once something has asked for it, and
	// a scalar with no entry in the file would otherwise never be looked up.
	_ = v.BindEnv("peek-lines")
	_ = v.BindEnv("theme")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); ok {
			// No config file at all is the normal case, not an error.
			return nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

// FromViper builds a config out of whatever a Viper has already read.
//
// The named theme lands first and the config's own `colors:` and `glyphs:` blocks land on
// top of it, so picking a theme and then changing one thing about it is two lines rather
// than a fork.
func FromViper(v *viper.Viper) Config {
	config := DefaultConfig()

	if name := v.GetString("theme"); name != "" {
		theme, problems := LoadTheme(name)
		config.Theme = theme
		config.Problems = append(config.Problems, problems...)
	}

	config.Problems = append(config.Problems, applyColors(&config.Theme.Colors, v.GetStringMapString("colors"))...)
	config.Problems = append(config.Problems, applyColors(&config.Theme.Colors, v.GetStringMapString("colours"))...)
	config.Problems = append(config.Problems, applyGlyphs(&config.Theme.Glyphs, v.GetStringMapString("glyphs"))...)
	config.Problems = append(
		config.Problems,
		applyAnimation(&config.Theme.Animation, v.GetStringMapString("animation"))...)
	config.Problems = append(config.Problems, applyKeys(config.Keymap, v.GetStringMapString("keys"))...)
	config.Problems = append(config.Problems, config.Keymap.Conflicts()...)

	if v.IsSet("bell") {
		if mode, ok := ParseBell(v.GetString("bell")); ok {
			config.Bell = mode
		} else {
			config.Problems = append(config.Problems,
				fmt.Sprintf("bell: %q is not on, off or failed", v.GetString("bell")))
		}
	}

	pivots, problems := readPivots(v)
	config.Pivots = pivots
	config.Problems = append(config.Problems, problems...)

	order, problems := readOrder(v)
	config.Order = order
	config.Problems = append(config.Problems, problems...)

	if v.IsSet("mouse") {
		if on, ok := parseOnOff(v.GetString("mouse")); ok {
			config.Mouse = on
		} else {
			config.Problems = append(config.Problems,
				fmt.Sprintf("mouse: %q is not on or off", v.GetString("mouse")))
		}
	}

	// Zero would make the peek state indistinguishable from hidden, leaving the cycle with
	// two visible stops and one that lies about which it is.
	if v.IsSet("peek-lines") {
		switch n := v.GetInt("peek-lines"); {
		case n == 0:
			config.Problems = append(config.Problems, "peek-lines: must be at least 1")
		case n > 100:
			config.Problems = append(config.Problems, "peek-lines: 100 is already the whole screen")
		default:
			config.PeekLines = n
		}
	}

	return config
}

// DumpConfig is the whole annotated file `--dump-config` prints.
func DumpConfig() string {
	var b strings.Builder
	// Names the path rather than telling you to put it there: this text is now both what
	// `--dump-config` prints and what `config init` writes *to* that path, and "drop this
	// at X" reads oddly in a file that is already X.
	fmt.Fprintf(&b, "# taskui config — %s\n", ConfigPath())
	fmt.Fprintf(&b, "# Pick a look with `theme:`. Available: %s\n", strings.Join(ListThemes(), ", "))
	fmt.Fprintf(&b, "theme: %s\n\n", DefaultThemeName)
	b.WriteString("# Every key is optional; anything absent keeps its default.\n")
	b.WriteString("# Values: an ANSI name (red, bright-blue), a #rrggbb, or a 0-255 palette index.\n")
	b.WriteString("#\n")
	b.WriteString("# Names follow your terminal's own scheme; #rrggbb pins the colour exactly.\n")
	b.WriteString(DefaultColors().ToYAML())
	b.WriteString("\n")
	b.WriteString(DefaultGlyphs().ToYAML())
	b.WriteString("\n")
	b.WriteString("# Ring the terminal when a run finishes while you are looking at something else.\n")
	b.WriteString("# `on`, `off`, or `failed` for only the ones that broke.\n")
	b.WriteString("bell: on\n\n")
	b.WriteString("# Rebind any action to a different key: a character, or `ctrl+`, `alt+` and\n")
	b.WriteString("# `shift+` in front of one — `shift+space`, `ctrl+r`. `space` names the space bar.\n")
	b.WriteString("# Modified keys need a terminal that reports them; without one they never arrive.\n")
	b.WriteString("# The same action keeps its meaning on every screen that offers it.\n")
	b.WriteString("keys:\n")
	for _, a := range keys.All() {
		spelling := a.Key.String()
		if len([]rune(spelling)) == 1 && !isAlphanumeric(a.Key.Key) {
			spelling = fmt.Sprintf("%q", spelling)
		}
		fmt.Fprintf(&b, "  %s: %s\n", a.Name, spelling)
	}
	b.WriteString("\n")
	b.WriteString("# How many lines a task shows when its output is folded to a peek.\n")
	fmt.Fprintf(&b, "peek-lines: %d\n", DefaultPeekLines)
	b.WriteString("\n")
	b.WriteString("# Ask the terminal for mouse events, so the wheel scrolls taskui rather than\n")
	b.WriteString("# whatever is drawing it — a row a notch, on whichever screen you are on.\n")
	b.WriteString("# The cost: a terminal forwarding the mouse is not selecting text with it, so\n")
	b.WriteString("# drag-to-select needs your terminal's override (shift, or option on macOS).\n")
	b.WriteString("# `off` gives the mouse back.\n")
	b.WriteString("mouse: on\n")
	b.WriteString("\n")
	b.WriteString(orderYAML())
	return b.String()
}

// orderYAML documents the ordering keys, which need more explaining than a colour does:
// what each order is for, and the one interaction between two of them that is not obvious.
func orderYAML() string {
	var b strings.Builder
	b.WriteString("# What sits above what in the task list.\n")
	b.WriteString("#\n")
	b.WriteString("#   default  each grouping's own order — alphabetical in `domain` and `file`,\n")
	b.WriteString("#            biggest group first in `verb`\n")
	b.WriteString("#   name     alphabetical, everywhere\n")
	b.WriteString("#   file     the order the tasks are written in the Taskfile\n")
	b.WriteString("#   recent   most recently run first\n")
	b.WriteString("#   failed   what is broken first, most recent first within it\n")
	b.WriteString("#   size     biggest group first\n")
	fmt.Fprintf(&b, "sort: %s\n", pivot.ByNatural)
	b.WriteString("\n")
	b.WriteString("# Where a subgroup sits among the plain tasks of the same namespace.\n")
	b.WriteString("# `last` keeps a namespace's own verbs together above its subtrees; `mixed`\n")
	b.WriteString("# sorts groups and tasks as one list. Worth setting to `mixed` alongside\n")
	b.WriteString("# `sort: recent` or `failed`, where a group holding the row you want to see\n")
	b.WriteString("# would otherwise still sit below every task beside it.\n")
	b.WriteString("groups: last\n")
	b.WriteString("\n")
	b.WriteString("# Task names hoisted to the top of wherever they land, in the order written.\n")
	b.WriteString("# `*` globs, as in `.taskui-danger`. A group rises with anything it holds, so\n")
	b.WriteString("# pinning `backend:test` also lifts `backend` to the top of the list.\n")
	b.WriteString("pin: []\n")
	return b.String()
}

func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
