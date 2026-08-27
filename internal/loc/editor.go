package loc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Editor is the command to open a location, already split into argv.
type Editor struct {
	// Name is the program, for saying what is about to happen.
	Name string
	Args []string
	// Terminal is whether it takes over this terminal. A `code --goto` returns immediately
	// and should not black out the UI to do it; a `vim` must.
	Terminal bool
}

// terminalEditors are the ones that draw on this terminal, and therefore need it handed
// over. Anything unrecognised is assumed to be one of these: waiting for an editor that had
// already returned costs a keypress, while drawing over one that is mid-screen costs the
// session.
var terminalEditors = map[string]bool{
	"vi": true, "vim": true, "nvim": true,
	"nano": true, "pico": true, "emacs": true, "emacsclient": true,
	"hx": true, "helix": true, "kak": true, "micro": true,
	"ne": true, "joe": true, "mg": true, "ed": true,
}

// windowedEditors return immediately, handing the file to an already-running window.
var windowedEditors = map[string]bool{
	"code": true, "code-insiders": true, "codium": true, "cursor": true, "zed": true,
	"subl": true, "sublime_text": true, "atom": true, "gvim": true, "mate": true,
	"idea": true, "goland": true, "pycharm": true, "webstorm": true, "rubymine": true,
	"clion": true, "phpstorm": true, "rustrover": true, "fleet": true,
}

// EditorFor builds the command that opens loc, from $VISUAL or $EDITOR.
//
// The line number is the entire point, so the argv is per-editor rather than a bare
// `$EDITOR file`: an editor that opens at line 1 when the output said line 212 has done the
// tedious half of the job and left the useful half. Anything unrecognised still gets
// opened, just at the top — which is worse than the alternative but much better than
// refusing.
//
// Flags already in the variable are honoured: `EDITOR="code -w"` is a real thing people
// have set, and dropping the `-w` would silently change what it does.
func EditorFor(l Loc, abs string) (Editor, bool) {
	spec := os.Getenv("VISUAL")
	if strings.TrimSpace(spec) == "" {
		spec = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(spec) == "" {
		return Editor{}, false
	}
	fields := strings.Fields(spec)
	prog := fields[0]
	// Copied rather than resliced: every arm below appends to it, and appending to a slice
	// of `fields` would write into `fields` itself whenever the capacity happened to allow.
	extra := append([]string(nil), fields[1:]...)
	base := strings.TrimSuffix(filepath.Base(prog), ".exe")

	line := strconv.Itoa(l.Line)
	col := l.Col
	if col <= 0 {
		col = 1
	}

	// Each arm produces only the editor-specific part; `extra` is prepended once at the end.
	// Appending to `extra` in every arm reads as nine chances to append to the wrong slice.
	var tail []string
	switch base {
	case "vi", "vim", "nvim", "gvim", "ex":
		// `+N file` is the POSIX spelling and every vi understands it.
		tail = []string{"+" + line, abs}
	case "nano", "pico":
		tail = []string{"+" + line + "," + strconv.Itoa(col), abs}
	case "emacs", "emacsclient", "mg":
		tail = []string{"+" + line + ":" + strconv.Itoa(col), abs}
	case "micro", "ne", "joe":
		tail = []string{"+" + line, abs}
	case "hx", "helix", "kak":
		// Helix and Kakoune both take the location suffixed to the path.
		tail = []string{abs + ":" + line + ":" + strconv.Itoa(col)}
	case "code", "code-insiders", "codium", "cursor", "zed":
		tail = []string{"--goto", abs + ":" + line + ":" + strconv.Itoa(col)}
	case "subl", "sublime_text", "atom", "mate":
		tail = []string{abs + ":" + line + ":" + strconv.Itoa(col)}
	case "idea", "goland", "pycharm", "webstorm", "rubymine", "clion", "phpstorm", "rustrover", "fleet":
		tail = []string{"--line", line, "--column", strconv.Itoa(col), abs}
	default:
		// Unknown: open the file and say nothing about where. `+N` is close to universal
		// among terminal editors, but "close to" is not good enough when being wrong means
		// the editor treats it as a second filename and creates it.
		tail = []string{abs}
	}

	args := append(append([]string(nil), extra...), tail...)

	terminal := true
	switch {
	case windowedEditors[base]:
		terminal = false
	case terminalEditors[base]:
		terminal = true
	}
	return Editor{Name: prog, Args: args, Terminal: terminal}, true
}
