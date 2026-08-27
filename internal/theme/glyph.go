package theme

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Glyphs are the characters the UI draws its structure out of.
//
// They are themeable for the same reason the colours are: a fold marker, a tree guide and a
// status tick are decisions about how the tool looks, not about what it does. Pulling them
// out of the render code means a theme can be a file rather than a patch.
//
// Every one of them is a single terminal column, and that is enforced when a theme loads.
// The layout arithmetic — where a description starts, where a count ends — is built on
// knowing how wide a glyph is before it is drawn, and a theme that could smuggle in a
// three-column marker could push a column off the edge of the screen on somebody else's
// terminal, where nobody would ever see it happen. Wordmark is the exception: it is a
// label, it is measured, and it may be as wide as it likes.
type Glyphs struct {
	Wordmark  string
	Separator string
	Dot       string

	FoldOpen   string
	FoldClosed string
	FoldPeek   string

	GuideBranch   string
	GuideLast     string
	GuideVertical string

	Rail           string
	SelectionShade string
	Rule           string

	Command string
	Warning string
	Danger  string
	Cursor  string
	Bar     string
	Marked  string

	DiffAdded   string
	DiffRemoved string
	DiffGap     string

	StatusOk      string
	StatusFailed  string
	StatusRunning string
	StatusPending string
	StatusSkipped string
}

type glyphField struct {
	key string
	doc string
	// label marks a glyph that is measured rather than required to be one column.
	label bool
	at    func(*Glyphs) *string
}

var glyphFields = []glyphField{
	{
		"wordmark",
		"The tool's name in the header. A label, so it may be any width.",
		true,
		func(g *Glyphs) *string { return &g.Wordmark },
	},
	{
		"separator",
		"Between the wordmark and whatever the screen is showing.",
		false,
		func(g *Glyphs) *string { return &g.Separator },
	},
	{"dot", "Between the two halves of the pivot.", false, func(g *Glyphs) *string { return &g.Dot }},

	{"fold-open", "A group showing its children.", false, func(g *Glyphs) *string { return &g.FoldOpen }},
	{"fold-closed", "A group hiding them.", false, func(g *Glyphs) *string { return &g.FoldClosed }},
	{
		"fold-peek",
		"A task showing a window on its last few lines — a door left open a crack.",
		false,
		func(g *Glyphs) *string { return &g.FoldPeek },
	},

	{"guide-branch", "A tree branch with siblings below it.", false, func(g *Glyphs) *string { return &g.GuideBranch }},
	{"guide-last", "The last branch of its parent.", false, func(g *Glyphs) *string { return &g.GuideLast }},
	{
		"guide-vertical",
		"The line carrying depth down the left.",
		false,
		func(g *Glyphs) *string { return &g.GuideVertical },
	},

	{"rail", "The cursor's own column.", false, func(g *Glyphs) *string { return &g.Rail }},
	{
		"selection-shade",
		"The column down the right of the selected row. A space leaves it off; a block turns the row into something raised off the screen rather than painted onto it.",
		false,
		func(g *Glyphs) *string { return &g.SelectionShade },
	},
	{
		"rule",
		"What the hairlines between header, body and footer are drawn with.",
		false,
		func(g *Glyphs) *string { return &g.Rule },
	},

	{
		"command",
		"Marks go-task's echo of the command it is about to run.",
		false,
		func(g *Glyphs) *string { return &g.Command },
	},
	{"warning", "Marks go-task's report of what failed.", false, func(g *Glyphs) *string { return &g.Warning }},
	{
		"marked",
		"Marks a task chosen to run alongside others.",
		false,
		func(g *Glyphs) *string { return &g.Marked },
	},
	{
		"bar",
		"The duration bar on a task's timeline, drawn one cell per unit of the slowest run.",
		false,
		func(g *Glyphs) *string { return &g.Bar },
	},
	{"diff-added", "Marks a line only the newer run printed.", false, func(g *Glyphs) *string { return &g.DiffAdded }},
	{
		"diff-removed",
		"Marks a line only the older run printed.",
		false,
		func(g *Glyphs) *string { return &g.DiffRemoved },
	},
	{
		"diff-gap",
		"Marks where a diff elided the lines both runs shared.",
		false,
		func(g *Glyphs) *string { return &g.DiffGap },
	},
	{
		"danger",
		"Marks a task that needs a confirmation before it runs.",
		false,
		func(g *Glyphs) *string { return &g.Danger },
	},
	{"cursor", "The caret in a prompt.", false, func(g *Glyphs) *string { return &g.Cursor }},

	{"status-ok", "A task that succeeded.", false, func(g *Glyphs) *string { return &g.StatusOk }},
	{"status-failed", "A task that failed.", false, func(g *Glyphs) *string { return &g.StatusFailed }},
	{"status-running", "A task still going.", false, func(g *Glyphs) *string { return &g.StatusRunning }},
	{"status-pending", "A task not started yet.", false, func(g *Glyphs) *string { return &g.StatusPending }},
	{"status-skipped", "A task never reached.", false, func(g *Glyphs) *string { return &g.StatusSkipped }},
}

func DefaultGlyphs() Glyphs {
	return Glyphs{
		Wordmark:  "taskui",
		Separator: "▸",
		Dot:       "·",

		FoldOpen:   "▾",
		FoldClosed: "▸",
		FoldPeek:   "▿",

		GuideBranch:   "├",
		GuideLast:     "└",
		GuideVertical: "│",

		Rail:           "▌",
		SelectionShade: " ",
		Rule:           "─",

		Command: "❯",
		Warning: "⚠",
		Danger:  "⚠",
		Cursor:  "█",
		Bar:     "▄",
		Marked:  "◉",

		// `+` and `-` rather than anything prettier: this is the one place in the UI where
		// a reader already has a lifetime of muscle memory for what the marker means.
		DiffAdded:   "+",
		DiffRemoved: "-",
		DiffGap:     "⋮",

		StatusOk:      "✓",
		StatusFailed:  "✗",
		StatusRunning: "▶",
		StatusPending: "·",
		StatusSkipped: "⏸",
	}
}

// applyGlyphs overlays a `glyphs:` block, reporting anything it could not use.
func applyGlyphs(g *Glyphs, block map[string]string) []string {
	if len(block) == 0 {
		return nil
	}
	var bad []string
	used := map[string]bool{}
	for _, f := range glyphFields {
		text, ok := block[f.key]
		if !ok {
			continue
		}
		used[f.key] = true
		if text == "" {
			bad = append(bad, fmt.Sprintf("glyphs: `%s` cannot be empty", f.key))
			continue
		}
		// One column, measured the way a terminal measures it — an emoji is two, and a
		// theme that set one would shift every column to its right by one.
		if !f.label && lipgloss.Width(text) != 1 {
			bad = append(bad, fmt.Sprintf(
				"glyphs: `%s` must be one column wide, and %q is %d",
				f.key, text, lipgloss.Width(text)))
			continue
		}
		*f.at(g) = text
	}
	var unknown []string
	for k := range block {
		if !used[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		bad = append(bad, fmt.Sprintf("glyphs: `%s` is not a glyph", k))
	}
	return bad
}

// ToYAML renders the glyphs as a block a theme file can be built from.
func (g Glyphs) ToYAML() string {
	var b strings.Builder
	b.WriteString("glyphs:\n")
	for _, f := range glyphFields {
		fmt.Fprintf(&b, "  # %s\n  %s: %q\n", f.doc, f.key, *f.at(&g))
	}
	return b.String()
}
