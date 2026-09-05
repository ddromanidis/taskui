package app

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ddromanidis/taskui/internal/store"
)

// Completion for the args prompt.
//
// Half a real Taskfile needs arguments, and the prompt already knows which ones: it opens
// pre-filled with the `requires: vars` the task declares. What it could not do was finish
// a *value* — the path you are pointing at, or the `ENV=staging` you typed last Tuesday —
// so a prompt that knew the shape of the answer still made you type all of it, exactly.
//
// Three sources, in the order they are worth offering:
//
//   - the variables the task asks for, which is a declaration and therefore certain;
//   - the arguments this same task was actually run with before, out of the archive —
//     the strongest signal there is, because you chose it once already;
//   - files and directories under the project, for the arguments that are paths.
//
// Cycling rather than a longest-common-prefix insert: this is one footer line, the
// candidate is shown in place as you tab through it, and a prefix insert that stops on an
// ambiguity would need somewhere else to say why it stopped.

// argsCompletion is one run of ⇥-completion: the input either side of the word being
// completed, and where in the candidates we are.
//
// The two halves are captured when the cycle is armed rather than recomputed per keypress,
// so tabbing past a candidate longer than the word cannot walk the boundaries.
type argsCompletion struct {
	head, tail string
	cands      []string
	idx        int
}

// maxArgCandidates caps what one ⇥ offers.
//
// A project root with two hundred files would otherwise arm a cycle you cannot tab out of,
// and the fix for a list that long is to type another letter, not to press ⇥ again.
const maxArgCandidates = 50

// CompleteArgs advances the args prompt's completion: +1 for ⇥, -1 for ⇧⇥.
//
// The first press gathers the candidates and shows the first; the rest walk the same list.
// Anything else typed into the prompt drops the state, so the list can never be stale.
func (a *App) CompleteArgs(delta int) {
	if a.argsComp == nil {
		head, word, tail := a.argsWordAtCursor()
		cands := a.argsCandidates(word)
		if len(cands) == 0 {
			// Said out loud: a ⇥ that silently does nothing is indistinguishable from a
			// prompt that does not complete at all, which is what this exists to fix.
			a.Status = "no completion for `" + word + "`"
			return
		}
		a.argsComp = &argsCompletion{head: head, tail: tail, cands: cands}
	} else {
		n := len(a.argsComp.cands)
		a.argsComp.idx = (a.argsComp.idx + delta + n) % n
	}
	a.applyArgsCompletion()
}

func (a *App) applyArgsCompletion() {
	c := a.argsComp
	pick := c.cands[c.idx]
	a.ArgsInput = c.head + pick + c.tail
	a.ArgsCursor = len([]rune(c.head)) + len([]rune(pick))
	a.Status = ""
}

// argsWordAtCursor splits the input around the word the cursor sits in.
//
// The word ends *at* the cursor, not at the next space: completing `ENV=pro|d` should offer
// what starts with `pro` and leave the `d` where you left it, the same way a shell does.
func (a *App) argsWordAtCursor() (string, string, string) {
	runes := []rune(a.ArgsInput)
	at := clamp(a.ArgsCursor, 0, len(runes))
	start := at
	for start > 0 && runes[start-1] != ' ' {
		start--
	}
	return string(runes[:start]), string(runes[start:at]), string(runes[at:])
}

// argsCandidates is what ⇥ offers for one word.
func (a *App) argsCandidates(word string) []string {
	// Past the `=` the key is settled and only its value is in question, so the name is
	// held aside and put back on whatever comes out.
	if key, value, ok := strings.Cut(word, "="); ok {
		var out []string
		for _, v := range dedupe(concat(a.pastValues(key, value), a.pathsMatching(value))) {
			out = append(out, key+"="+v)
		}
		return cap50(out)
	}
	return cap50(dedupe(concat(
		a.varCandidates(word),
		a.pastArgs(word),
		a.hintCandidates(word),
		a.pathsMatching(word),
	)))
}

// hintCandidates is the examples the description spells out, whole.
//
// They come last of the three that are about *this* task because they are the author's
// guess where the other two are declarations and decisions — but they are the only source
// that works on a Taskfile you have never run, which is the first time you need one.
func (a *App) hintCandidates(prefix string) []string {
	var out []string
	for _, t := range a.Tasks {
		if t.Name != a.ArgsTarget {
			continue
		}
		for _, hint := range t.ArgsHints() {
			if strings.HasPrefix(hint, prefix) {
				out = append(out, hint)
			}
		}
	}
	return out
}

// varCandidates is the variables the task asks for, as `NAME=`.
//
// Taken from what BeginArgs already looked up: the prompt spends one `--summary` opening,
// and asking again on every ⇥ would spend it forty times over for an answer that cannot
// have changed.
func (a *App) varCandidates(prefix string) []string {
	var out []string
	for _, v := range a.argsVars {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v+"=")
		}
	}
	sort.Strings(out)
	return out
}

// pastArgs is every argument this task has been run with before, newest run first.
func (a *App) pastArgs(prefix string) []string {
	var out []string
	for _, args := range a.argsHistory() {
		for _, arg := range args {
			if strings.HasPrefix(arg, prefix) {
				out = append(out, arg)
			}
		}
	}
	return out
}

// pastValues is the values this task has been run with for one variable, newest first.
func (a *App) pastValues(key, prefix string) []string {
	var out []string
	for _, args := range a.argsHistory() {
		for _, arg := range args {
			k, v, ok := strings.Cut(arg, "=")
			if ok && k == key && strings.HasPrefix(v, prefix) {
				out = append(out, v)
			}
		}
	}
	return out
}

// argsHistory is the archived argument lists for the task the prompt is aimed at, newest
// run first.
//
// Read once per prompt and on the first ⇥ rather than when the prompt opens: it is a walk
// of every manifest in the archive, and most trips through this prompt never press ⇥.
func (a *App) argsHistory() [][]string {
	if a.argsPastRead {
		return a.argsPast
	}
	a.argsPastRead = true
	for _, m := range store.List(a.stateDir) {
		if m.Dir == a.Root && m.Root == a.ArgsTarget && len(m.Args) > 0 {
			a.argsPast = append(a.argsPast, m.Args)
		}
	}
	return a.argsPast
}

// pathsMatching completes a path against the project, directories keeping their slash so
// the next ⇥ walks into them.
//
// Relative to the project root rather than to the working directory, because that is where
// the task will run and therefore what its own paths mean. Hidden entries stay hidden
// unless you have typed the dot that asks for them — a project root is mostly dotfiles, and
// offering them all would bury the three files you meant.
func (a *App) pathsMatching(prefix string) []string {
	dir, stem := path.Split(prefix)
	entries, err := os.ReadDir(filepath.Join(a.Root, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, stem) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(stem, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		out = append(out, dir+name)
	}
	sort.Strings(out)
	return out
}

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// dedupe keeps the first of each, so the order the sources were offered in survives.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func cap50(in []string) []string {
	if len(in) > maxArgCandidates {
		return in[:maxArgCandidates]
	}
	return in
}
