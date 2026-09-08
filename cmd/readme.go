package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ddromanidis/taskui/internal/keys"
)

// The README's key tables, generated.
//
// They were the last hand-written copy of the keymap, and the one that went stale: they
// carried no `^d` or `^f`, no Home or End, no `gg` outside the three smallest tables, no
// `?` row, and they spelled the last-row key `⇧G` where the program says `G`. The man page
// had already been through this and came out the other side generated with a test on it —
// so this is the same machinery pointed at the other file, rather than a second habit.
//
// Only the tables are spliced. The prose around them is what the README is for, and none
// of it is derivable from anything.
const (
	beginTable = "<!-- GENERATED KEYS "
	endTable   = "<!-- END KEYS -->"
)

var readmeWrite bool

func init() {
	readmeCmd.Flags().BoolVar(&readmeWrite, "write", false, "write README.md in place instead of printing it")
	readmeCmd.Hidden = true // a maintenance command, as `man` is
	rootCmd.AddCommand(readmeCmd)
}

var readmeCmd = &cobra.Command{
	Use:   "readme [path]",
	Short: "Regenerate the README's key tables",
	Long: `Rewrite the key tables in README.md from the same table ` + "`?`" + ` and the footer read.

The prose around them is left alone. Run it with --write after adding or moving a binding;
a test fails if you forget.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "README.md"
		if len(args) > 0 {
			path = args[0]
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", path, err)
		}
		next, err := RegenerateReadme(string(current))
		if err != nil {
			return err
		}
		if !readmeWrite {
			fmt.Fprint(cmd.OutOrStdout(), next)
			return nil
		}
		if next == string(current) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is already current\n", path)
			return nil
		}
		if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
			return fmt.Errorf("could not write %s: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "rewrote %s\n", path)
		return nil
	},
}

// RegenerateReadme replaces every generated key table and returns the whole file.
//
// The markers name their section, so the README decides which table goes where and keeps
// the sentence that introduces each one. A marker naming a section that no longer exists is
// an error rather than an empty table — a heading with nothing under it answers the
// question worse than no heading at all.
func RegenerateReadme(current string) (string, error) {
	// The page documents what taskui ships with, not what one machine's config did to it.
	defaults := keys.NewKeymap()

	var out strings.Builder
	rest := current
	for {
		from := strings.Index(rest, beginTable)
		if from < 0 {
			out.WriteString(rest)
			break
		}
		// The marker runs `<!-- GENERATED KEYS <title> ... -->`; the title is the first word
		// after the prefix.
		head := rest[from:]
		shut := strings.Index(head, "-->")
		if shut < 0 {
			return "", fmt.Errorf("README has an unterminated %q marker", beginTable)
		}
		title := strings.Fields(head[len(beginTable):shut])
		if len(title) == 0 {
			return "", fmt.Errorf("a %q marker names no section", beginTable)
		}
		section := sectionNamed(title[0])
		if section == nil {
			return "", fmt.Errorf("README asks for a %q key table, which is not a section", title[0])
		}
		to := strings.Index(head, endTable)
		if to < 0 {
			return "", fmt.Errorf("README has no %q after the %s table", endTable, title[0])
		}

		out.WriteString(rest[:from])
		out.WriteString(head[:shut+len("-->")])
		out.WriteString("\n")
		out.WriteString(readmeTable(section, defaults))
		rest = head[to:]
	}
	return out.String(), nil
}

// sectionNamed finds a section by its title, which is what a marker names it by.
func sectionNamed(title string) *keys.Section {
	for _, s := range keys.Sections {
		if strings.EqualFold(s.Title, title) {
			return s
		}
	}
	return nil
}

// readmeTable renders one section as a markdown table.
func readmeTable(section *keys.Section, km *keys.Keymap) string {
	var b strings.Builder
	b.WriteString("\n| key | |\n|---|---|\n")
	for _, binding := range keys.Spelled(section, km) {
		var cells []string
		for key := range strings.FieldsSeq(binding.Keys) {
			cells = append(cells, "`"+key+"`")
		}
		fmt.Fprintf(&b, "| %s | %s |\n", strings.Join(cells, " "), markdown(binding.What))
	}
	b.WriteString("\n")
	return b.String()
}

// markdown keeps a description inside its table cell. A pipe would end the cell early and
// silently drop the rest of the sentence.
func markdown(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
