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
	// Problems lists anything wrong with the file, surfaced in the UI rather than
	// swallowed — a colour that silently does nothing is worse than one that says why.
	Problems []string
}

func DefaultConfig() Config {
	return Config{
		Theme:     DefaultTheme(),
		Keymap:    keys.NewKeymap(),
		PeekLines: DefaultPeekLines,
	}
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

// applyKeys points actions at different keys. Unknown names and multi-character values are
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
		runes := []rune(value)
		if len(runes) != 1 {
			problems = append(problems, fmt.Sprintf("keys: %s must be a single character, not %q", name, value))
			continue
		}
		keymap.Rebind(action, runes[0])
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

	pivots, problems := readPivots(v)
	config.Pivots = pivots
	config.Problems = append(config.Problems, problems...)

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
	fmt.Fprintf(&b, "# taskui config. Drop this at %s\n", ConfigPath())
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
	b.WriteString("# Rebind any action to a different single character.\n")
	b.WriteString("# The same action keeps its meaning on every screen that offers it.\n")
	b.WriteString("keys:\n")
	for _, a := range keys.All() {
		quoted := string(a.Key)
		if !isAlphanumeric(a.Key) {
			quoted = fmt.Sprintf("%q", string(a.Key))
		}
		fmt.Fprintf(&b, "  %s: %s\n", a.Name, quoted)
	}
	b.WriteString("\n")
	b.WriteString("# How many lines a task shows when its output is folded to a peek.\n")
	fmt.Fprintf(&b, "peek-lines: %d\n", DefaultPeekLines)
	return b.String()
}

func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
