/*
Copyright © 2026 Dmitry Romanidis
Licensed under the MIT licence. See LICENSE.
*/

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/romanidis/taskui/internal/loc"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
)

// What to do about a directory that has no Taskfile.
//
// The old answer was go-task's: `task --list-all` fails, and taskui printed its stderr —
// "No Taskfile found … Run `task --init` to create a new Taskfile". Two things wrong with
// passing that on. It names somebody else's command, from a program the reader may not have
// meant to be talking to; and `task --init` writes a Taskfile whose only task is `default:`,
// which is the one name taskui does not list. Follow the advice and the next thing you see
// is an empty picker.
//
// So taskui answers for itself, and offers to write the file it would like to be pointed
// at. Only when there is somebody there to ask: a pipe, a script or a `--list` gets the
// same refusal it always did, worded to say what would fix it.

// starterChoice is what the offer came back with.
type starterChoice int

const (
	starterQuit starterChoice = iota
	// starterWrite writes the file and carries straight on into the picker.
	starterWrite
	// starterEdit writes it and opens it, for somebody who came here to write their own
	// and wants the shape rather than the example.
	starterEdit
)

// offerStarter deals with a root that has no Taskfile, and reports whether one now exists.
//
// A false with no error is somebody who said no, which is not a failure and should not be
// reported as one.
func offerStarter(root string, config theme.Config) (bool, error) {
	if !interactiveTerminal() {
		return false, noTaskfileHere(root)
	}

	m := &starterModel{root: root, config: config}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return false, err
	}
	if m.choice == starterQuit {
		return false, nil
	}

	path, err := task.WriteStarter(root)
	if err != nil {
		return false, err
	}
	fmt.Fprintf(os.Stderr, "taskui: wrote %s\n", path)

	if m.choice == starterEdit {
		if err := editStarter(path); err != nil {
			return false, err
		}
	}
	return true, nil
}

// noTaskfileHere is the non-interactive answer: the same two ways out, said rather than
// offered.
func noTaskfileHere(root string) error {
	return fmt.Errorf("no Taskfile in %s, or in any directory above it\n"+
		"       run `taskui` here with no flags to have a starter one written for you, "+
		"or add your own %s and try again", root, task.StarterName)
}

// interactiveTerminal is whether there is a person on the other end of both halves of this
// — a prompt needs a screen to draw on and a keyboard to answer with, and `taskui | less`
// has neither.
func interactiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// editStarter opens the file that was just written.
//
// Synchronously, and only for a terminal editor: this is the last thing before the picker
// starts, so a `vim` has to be finished with the terminal before Bubble Tea takes it, and a
// `code` that opens its own window must not be waited for.
func editStarter(path string) error {
	editor, ok := loc.EditorFor(loc.Loc{Path: path, Line: 1}, path)
	if !ok {
		fmt.Fprintf(os.Stderr, "taskui: $EDITOR is not set — edit %s when you like\n", path)
		return nil
	}
	//nolint:gosec // this is $EDITOR being run on purpose; the argv is built from the
	// variable the user set and a path this program wrote a moment ago.
	cmd := exec.Command(editor.Name, editor.Args...)
	if !editor.Terminal {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("could not start %s: %w", editor.Name, err)
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", editor.Name, err)
	}
	return nil
}

// starterModel is the offer. Not on the alternate screen: it is a short question, and the
// answer — including "no" — is worth leaving behind in the scrollback.
type starterModel struct {
	root   string
	config theme.Config
	width  int
	choice starterChoice
}

func (m *starterModel) Init() tea.Cmd { return nil }

func (m *starterModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		return m, nil
	}
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	key := tea.Key(press)
	switch {
	case key.Code == 'g' || key.Code == tea.KeyEnter:
		m.choice = starterWrite
	case key.Code == 'e':
		m.choice = starterEdit
	case key.Code == 'q' || key.Code == tea.KeyEscape ||
		(key.Code == 'c' && key.Mod&tea.ModCtrl != 0):
		m.choice = starterQuit
	default:
		return m, nil
	}
	return m, tea.Quit
}

func (m *starterModel) View() tea.View {
	c := m.config.Theme.Colors
	key := styleFor(c.Accent).Bold(true)
	dim := styleFor(c.Dim)
	faint := styleFor(c.Faint)

	// Bubble Tea cuts a line that overruns the terminal, and two things here have no length
	// of their own: the root, which is an absolute path, and the prose. So the path is
	// elided from the left — the end of a path is the part that says where you are — and
	// everything else is wrapped while it is still plain text, which is what keeps the
	// measuring honest.
	width := m.width
	if width <= 0 {
		width = 80
	}
	inner := min(width, 78)

	var b strings.Builder
	b.WriteString("\n" + key.Render(block("no Taskfile here, nor in any directory above",
		"  ", "  ", inner)) + "\n")
	b.WriteString(dim.Render("  "+elide(tilde(m.root), inner-2)) + "\n\n")

	option := func(k, text string) {
		lines := wrap(text, inner-8)
		fmt.Fprintf(&b, "    %s   %s\n", key.Render(k), lines[0])
		for _, line := range lines[1:] {
			b.WriteString("        " + line + "\n")
		}
	}
	option("g", "write a starter "+task.StarterName+" here, and browse it")
	option("e", "write it and open it in $EDITOR first")
	option("q", "quit, and add your own "+task.StarterName)

	b.WriteString("\n" + dim.Render(block("The starter is a hello-world with the parts "+
		"taskui reads spelled out: a desc:, a name that folds into a namespace, an args "+
		"hint. Edit it into your own — the picker reloads whenever the file changes.",
		"  ", "  ", inner)) + "\n\n")
	b.WriteString("  " + faint.Render("g/⏎ · e · q") + "\n")
	return tea.NewView(b.String())
}

// tilde writes a path the way it is said out loud, which is also the cheapest way to make
// it shorter without losing anything.
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}

// elide cuts an over-long path from the left. The tail is the part that answers "where am
// I"; the head is the part you already know.
func elide(path string, width int) string {
	runes := []rune(path)
	if len(runes) <= width || width < 4 {
		return path
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

// block is wrap plus indentation: first in front of the first line, rest in front of the
// ones the wrap produced. Which is the part lipgloss's Width does not do for a block whose
// indent was written outside it.
func block(text, first, rest string, width int) string {
	lines := wrap(text, width-len([]rune(rest)))
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
			continue
		}
		lines[i] = rest + lines[i]
	}
	return strings.Join(lines, "\n")
}

// wrap word-wraps plain text to limit columns, and always returns at least one line.
//
// Plain text only: everything here is wrapped before the colour goes on, so counting runes
// is counting columns.
func wrap(text string, limit int) []string {
	limit = max(limit, 20)
	var lines []string
	line := ""
	for word := range strings.FieldsSeq(text) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= limit:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	return append(lines, line)
}

// styleFor is app's `fg`, which is not exported and is one line.
func styleFor(c theme.Color) lipgloss.Style {
	if c.IsDefault() {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(c.Lip())
}
