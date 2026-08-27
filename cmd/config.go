package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ddromanidis/taskui/internal/loc"
	"github.com/ddromanidis/taskui/internal/theme"
)

// The config commands.
//
// A subcommand rather than making `--config` do double duty. `--config` conventionally
// takes a path, and a flag that is *sometimes* valueless is a real ambiguity rather than a
// stylistic one: with an optional value, `taskui --config my.yaml` parses as `--config`
// with no value plus a positional `my.yaml`, so it would open an editor and then browse a
// Taskfile in a directory named after your config file. Nothing in the wild does that —
// `git config --edit`, `npm config edit` and `crontab -e` all put the verb somewhere it
// cannot be confused with a path.
func init() {
	configInitCmd.Flags().BoolVar(&configForce, "force", false,
		"replace an existing config with the defaults")
	configCmd.AddCommand(configPathCmd, configEditCmd, configInitCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Where your config lives, and how to edit it",
	Long: `Where taskui reads its settings from, and whether anything is there yet.

The file is optional: a missing one is the ordinary case, not an error.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		out := cmd.OutOrStdout()
		path := theme.ConfigPath()

		state := "not there yet — every setting is at its default"
		if info, err := os.Stat(path); err == nil {
			state = fmt.Sprintf("%d bytes", info.Size())
		}
		fmt.Fprintf(out, "config   %s\n         %s\n", path, state)
		fmt.Fprintf(out, "themes   %s\n", theme.ThemesDir())
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  taskui config edit    open it, creating it from the defaults first")
		fmt.Fprintln(out, "  taskui config init    just create it")
		fmt.Fprintln(out, "  taskui config path    print the path and nothing else")
		fmt.Fprintln(out, "  taskui --dump-config  print every setting at its default, annotated")
		return nil
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the path to the config file",
	Long: `Print the path and nothing else, whether or not anything is there.

For scripts: ` + "`$EDITOR \"$(taskui config path)\"`" + ` does by hand what ` + "`config edit`" + ` does.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), theme.ConfigPath())
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a starter config, if there is not one already",
	Long: `Write the annotated defaults to the config path.

An existing file is never touched — it is the one file in this tool that holds decisions you
made by hand, and overwriting it because a flag was ambiguous would be unforgivable. Use
` + "`--force`" + ` if replacing it really is what you want.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, created, err := ensureConfig(configForce)
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s already exists — `taskui config edit` opens it\n", path)
		}
		return nil
	},
}

var configForce bool

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the config in $EDITOR",
	Long: `Open the config file in $VISUAL, or $EDITOR.

It is created from the annotated defaults first if it is not there, because an editor opened
on an empty buffer is a worse starting point than one showing every setting there is.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path, created, err := ensureConfig(false)
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(cmd.ErrOrStderr(), "created %s\n", path)
		}
		return openInEditor(cmd.OutOrStdout(), path)
	},
}

// ensureConfig makes sure there is a file to open, and says whether it had to make one.
func ensureConfig(force bool) (string, bool, error) {
	path := theme.ConfigPath()
	if _, err := os.Stat(path); err == nil && !force {
		return path, false, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("could not read %s: %w", path, err)
	}

	// Owner-only, like the archive. Nothing here is secret, but nothing here is anybody
	// else's business either, and a config that arrives group-readable is a decision nobody
	// made on purpose.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, fmt.Errorf("could not create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(theme.DumpConfig()), 0o600); err != nil {
		return "", false, fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, true, nil
}

// openInEditor hands the terminal to $EDITOR and waits.
//
// Not the run view's detached path: this is a foreground command in a shell, there is no UI
// to preserve, and the point is that you come back when you are done editing. A windowed
// editor still gets its own window and returns immediately, which is what it always does.
func openInEditor(out io.Writer, path string) error {
	editor, ok := loc.EditorFor(loc.Loc{Path: path, Line: 1}, path)
	if !ok {
		fmt.Fprintf(out, "%s\n", path)
		return errors.New("set $EDITOR or $VISUAL, or open the path above yourself")
	}

	//nolint:gosec // this is $EDITOR being run on purpose, on a path this program chose
	editing := exec.Command(editor.Name, editor.Args...)
	editing.Stdin, editing.Stdout, editing.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := editing.Run(); err != nil {
		return fmt.Errorf("could not run %s: %w", editor.Name, err)
	}
	return nil
}
