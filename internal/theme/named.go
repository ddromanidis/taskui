package theme

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// builtin holds the themes that ship inside the binary. They are ordinary theme files with
// no special standing: `--dump-theme` prints one, you edit it, you drop it in your themes
// directory, and yours wins.
//
//go:embed themes/*.yaml
var builtin embed.FS

// ThemesDir is where taskui looks for themes you wrote: `$XDG_CONFIG_HOME/taskui/themes`,
// else `~/.config/taskui/themes`.
func ThemesDir() string {
	return filepath.Join(filepath.Dir(ConfigPath()), "themes")
}

// DefaultThemeName is what you get without asking for anything.
const DefaultThemeName = "default"

// maxExtends bounds an `extends:` chain. Deep enough for anything real, shallow enough that
// a cycle is caught rather than followed.
const maxExtends = 8

// File is one theme as it was written down: a name, an optional parent, and the two blocks
// that make up a look.
type File struct {
	Name      string
	Extends   string
	Colors    map[string]string
	Glyphs    map[string]string
	Animation map[string]string
	// Source says where it came from, so a problem can name a file rather than a theme.
	Source string
}

// ListThemes names every theme available, built-in and yours, without duplicates.
func ListThemes() []string {
	seen := map[string]bool{}
	entries, _ := builtin.ReadDir("themes")
	for _, e := range entries {
		seen[strings.TrimSuffix(e.Name(), ".yaml")] = true
	}
	if local, err := os.ReadDir(ThemesDir()); err == nil {
		for _, e := range local {
			if before, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
				seen[before] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// readTheme finds one theme by name. A file in your themes directory shadows a built-in of
// the same name, so shipping `default.yaml` is not a decision you are stuck with.
func readTheme(name string) (File, error) {
	local := filepath.Join(ThemesDir(), name+".yaml")
	if blob, err := os.ReadFile(local); err == nil {
		return parseTheme(name, local, blob)
	}
	blob, err := builtin.ReadFile("themes/" + name + ".yaml")
	if err != nil {
		return File{}, fmt.Errorf("no theme called %q — try one of: %s",
			name, strings.Join(ListThemes(), ", "))
	}
	return parseTheme(name, "built-in", blob)
}

func parseTheme(name, source string, blob []byte) (File, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(string(blob))); err != nil {
		return File{}, fmt.Errorf("%s: %w", source, err)
	}
	return File{
		Name:      name,
		Extends:   v.GetString("extends"),
		Colors:    v.GetStringMapString("colors"),
		Glyphs:    v.GetStringMapString("glyphs"),
		Animation: v.GetStringMapString("animation"),
		Source:    source,
	}, nil
}

// LoadTheme resolves a theme by name, following `extends:` down to the base.
//
// A theme that extends another is the point of the whole arrangement: most of what makes a
// look is agreement, and a file that says "the default, but magenta and blocky" is a better
// artefact than one that restates all fifty-eight values to change nine of them.
//
// Problems are returned alongside a usable theme rather than instead of one. A bad colour
// in a theme file should cost you that colour, not the tool.
func LoadTheme(name string) (Theme, []string) {
	if name == "" {
		name = DefaultThemeName
	}

	// Walk up the chain first, then apply from the base down, so a child's values land on
	// top of its parent's.
	var chain []File
	seen := map[string]bool{}
	var problems []string

	for at := name; at != ""; {
		if seen[at] {
			problems = append(problems, fmt.Sprintf("theme %q extends itself", at))
			break
		}
		seen[at] = true

		file, err := readTheme(at)
		if err != nil {
			problems = append(problems, err.Error())
			break
		}
		chain = append(chain, file)
		if len(chain) > maxExtends {
			problems = append(problems, fmt.Sprintf("theme %q: `extends` runs more than %d deep", name, maxExtends))
			break
		}
		at = file.Extends
	}

	theme := Theme{Colors: DefaultColors(), Glyphs: DefaultGlyphs(), Name: name}
	for _, file := range slices.Backward(chain) {
		for _, p := range applyColors(&theme.Colors, file.Colors) {
			problems = append(problems, file.Name+": "+p)
		}
		for _, p := range applyGlyphs(&theme.Glyphs, file.Glyphs) {
			problems = append(problems, file.Name+": "+p)
		}
		for _, p := range applyAnimation(&theme.Animation, file.Animation) {
			problems = append(problems, file.Name+": "+p)
		}
	}
	return theme, problems
}

// DumpTheme prints a theme as a file you can edit: every value it resolved to, annotated,
// ready to be dropped in the themes directory under a new name.
func DumpTheme(name string) (string, []string) {
	theme, problems := LoadTheme(name)

	var b strings.Builder
	fmt.Fprintf(&b, "# taskui theme %q, fully resolved.\n", theme.Name)
	fmt.Fprintf(&b, "# Save it as %s/<your-name>.yaml and select it with `theme: <your-name>`\n", ThemesDir())
	b.WriteString("# in config.yaml, or `taskui --theme <your-name>`.\n")
	b.WriteString("#\n")
	b.WriteString("# Everything here is optional. A theme that only overrides what it cares about is\n")
	b.WriteString("# easier to read and survives changes to the one it extends:\n")
	b.WriteString("#\n")
	b.WriteString("#   extends: default\n")
	b.WriteString("#   colors:\n")
	b.WriteString("#     accent: bright-magenta\n")
	b.WriteString("#\n")
	b.WriteString("# Colours: an ANSI name (red, bright-blue), a #rrggbb, or a 0-255 palette index.\n")
	b.WriteString("# Glyphs: one terminal column each, except the wordmark.\n")
	b.WriteString("\n")
	b.WriteString(theme.Colors.ToYAML())
	b.WriteString("\n")
	b.WriteString(theme.Glyphs.ToYAML())
	b.WriteString("\n")
	b.WriteString(theme.Animation.ToYAML())
	return b.String(), problems
}
