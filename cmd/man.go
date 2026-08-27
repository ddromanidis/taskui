package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ddromanidis/taskui/internal/keys"
)

// The man page's generated halves.
//
// `taskui.1` ships in every release archive and in the Homebrew cask, and the README tells
// people twice that it covers the options — so it is documentation with a distribution
// channel, and it had drifted two rewrites behind the program. Its header still said
// `taskui 0.1.1`, from the Rust version, and it was missing eight flags and all three
// subcommands.
//
// Hand-patching it would fix today and not tomorrow. The prose is worth keeping — the notes
// on peek windows, credentials and stopping are not derivable from anything — but the
// reference sections are, twice over: the flags live in one pflag set and the keys in one
// table that already backs both `?` and the footer. So those are spliced in between markers
// and a test regenerates the file and compares, which is the thing that would have caught
// this on the day it broke.
const (
	beginOptions = `.\" GENERATED OPTIONS — regenerate with ` + "`task man`"
	endOptions   = `.\" END OPTIONS`
	beginKeys    = `.\" GENERATED KEYS — regenerate with ` + "`task man`"
	endKeys      = `.\" END KEYS`
	beginCmds    = `.\" GENERATED COMMANDS — regenerate with ` + "`task man`"
	endCmds      = `.\" END COMMANDS`
)

var manWrite bool

func init() {
	manCmd.Flags().BoolVar(&manWrite, "write", false, "write taskui.1 in place instead of printing it")
	manCmd.Hidden = true // a maintenance command, not part of the interface
	rootCmd.AddCommand(manCmd)
}

var manCmd = &cobra.Command{
	Use:   "man [path]",
	Short: "Regenerate the reference sections of the man page",
	Long: `Rewrite taskui.1's OPTIONS, COMMANDS and KEYS from the tables they come from.

The prose around them is left alone. Run it with --write after adding a flag, a subcommand
or a binding; a test fails if you forget.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "taskui.1"
		if len(args) > 0 {
			path = args[0]
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read %s: %w", path, err)
		}
		next, err := RegenerateMan(string(current))
		if err != nil {
			return err
		}
		if !manWrite {
			fmt.Fprint(cmd.OutOrStdout(), next)
			return nil
		}
		if next == string(current) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is already current\n", path)
			return nil
		}
		// The mode only applies to a file that does not exist yet, and this one is
		// committed — git restores 644 on checkout, which is what a shipped man page wants.
		if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
			return fmt.Errorf("could not write %s: %w", path, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "rewrote %s\n", path)
		return nil
	},
}

// RegenerateMan replaces the generated sections of a man page and returns the whole file.
func RegenerateMan(current string) (string, error) {
	// cobra adds `completion` during Execute, not at construction — so without this the
	// output depended on whether anything had run first, and the page gained or lost a
	// subcommand according to test ordering. Idempotent, so asking again costs nothing.
	rootCmd.InitDefaultCompletionCmd()

	for _, section := range []struct {
		begin, end, body string
	}{
		{beginOptions, endOptions, manOptions()},
		{beginCmds, endCmds, manCommands()},
		{beginKeys, endKeys, manKeys()},
	} {
		next, err := splice(current, section.begin, section.end, section.body)
		if err != nil {
			return "", err
		}
		current = next
	}
	return current, nil
}

// splice replaces what lies between two markers, keeping the markers.
func splice(text, begin, end, body string) (string, error) {
	from := strings.Index(text, begin)
	if from < 0 {
		return "", fmt.Errorf("man page has no %q marker", begin)
	}
	to := strings.Index(text[from:], end)
	if to < 0 {
		return "", fmt.Errorf("man page has no %q after %q", end, begin)
	}
	to += from
	return text[:from+len(begin)] + "\n" + body + text[to:], nil
}

// manOptions renders every flag, in the order they were declared.
func manOptions() string {
	var b strings.Builder
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		b.WriteString(".TP\n")
		if f.Value.Type() == "bool" {
			fmt.Fprintf(&b, ".BR \\-\\-%s\n", roff(f.Name))
		} else {
			fmt.Fprintf(&b, ".BI \\-\\-%s \" %s\"\n", roff(f.Name), strings.ToUpper(placeholder(f)))
		}
		b.WriteString(sentence(generic(f.Usage)) + "\n")
	})
	return b.String()
}

// generic strips this machine out of a flag's help.
//
// `--config` names its default, which is useful at a terminal and wrong in a file that is
// committed and then shipped: the generated page would otherwise carry whichever home
// directory happened to run the generator.
func generic(usage string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		usage = strings.ReplaceAll(usage, home, "~")
	}
	return usage
}

// placeholder is what to call a flag's value in the synopsis.
func placeholder(f *pflag.Flag) string {
	switch f.Name {
	case flagRun, flagGraph, flagTimeline, flagDiff, "task":
		return "task"
	case flagDump:
		return "pivot"
	case "theme", "dump-theme":
		return "theme"
	case "screenshot":
		return "WxH"
	case "search":
		return "pattern"
	case "config":
		return "path"
	case "since":
		return "span"
	default:
		return f.Value.Type()
	}
}

// manCommands renders the subcommands.
func manCommands() string {
	var b strings.Builder
	for _, c := range rootCmd.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		b.WriteString(".TP\n")
		fmt.Fprintf(&b, ".BR taskui \" %s\"\n", roff(c.Name()))
		b.WriteString(sentence(c.Short) + "\n")
		// cobra generates a child per shell for `completion`. Listing bash, fish, powershell
		// and zsh separately in a manual is four entries saying the same thing.
		if c.Name() == "completion" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			b.WriteString(".RS\n.TP\n")
			fmt.Fprintf(&b, ".BR %s \" %s\"\n", roff(c.Name()), roff(sub.Name()))
			b.WriteString(sentence(sub.Short) + "\n.RE\n")
		}
	}
	return b.String()
}

// manKeys renders the keymap from the same table `?` and the footer read.
func manKeys() string {
	var b strings.Builder
	for _, section := range keys.Sections {
		fmt.Fprintf(&b, ".SS %s\n", section.Title)
		if section.Note != "" {
			b.WriteString(sentence(section.Note) + "\n.PP\n")
		}
		for _, binding := range section.Bindings {
			b.WriteString(".TP\n")
			fmt.Fprintf(&b, ".B %s\n", roff(binding.Keys))
			b.WriteString(sentence(binding.What) + "\n")
		}
	}
	return b.String()
}

// roff escapes what troff would otherwise read as markup. A leading dot starts a request,
// and a backslash starts an escape — both appear in ordinary flag help.
func roff(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	s = strings.ReplaceAll(s, "-", `\-`)
	return s
}

// sentence renders a line of help as a paragraph: escaped, capitalised, and ended with a
// full stop, because that is how the rest of the page reads.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "."
	}
	// A line that begins with a dot or an apostrophe is a troff request. `\&` is the
	// zero-width nothing that stops it being one.
	out := strings.ReplaceAll(s, `\`, `\e`)
	out = strings.ReplaceAll(out, "-", `\-`)
	if strings.HasPrefix(out, ".") || strings.HasPrefix(out, "'") {
		out = `\&` + out
	}
	if r := []rune(out); len(r) > 0 && r[0] >= 'a' && r[0] <= 'z' {
		out = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	if !strings.HasSuffix(out, ".") && !strings.HasSuffix(out, "!") && !strings.HasSuffix(out, "?") {
		out += "."
	}
	return out
}
