// Package keys is the keymap, as data.
//
// Both the `?` screen and the one-line footer hints are generated from this table, so they
// cannot disagree with each other. They could still disagree with the handlers in the app
// package — nothing here dispatches anything — but a single table that both surfaces read
// from is the difference between one place to update and three.
package keys

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Action is everything a single keypress can ask for.
//
// Dispatch goes through this rather than matching characters directly, which is what makes
// rebinding possible: the config maps a character to an action, and the handlers only ever
// see actions.
type Action int

const (
	None Action = iota
	Pivot
	Args
	Detail
	Jump
	Filter
	History
	ResumeRun
	Interactive
	Force
	Help
	Quit
	Search
	NextMatch
	PrevMatch
	FilterMatches
	ContextMore
	ContextLess
	Rerun
	ForceRerun
	Stop
	StopAll
	Input
	InteractiveRerun
	Follow
	Watch
	Yank
	YankAll
	AllProjects
	Top
	Bottom
	Fold
	FoldAll
	CloseSlot
)

type binding struct {
	action Action
	key    rune
	name   string
}

// defaults pair each action with its default key and the config name used to rebind it.
var defaults = []binding{
	{Pivot, 'p', "pivot"},
	{Args, 'a', "args"},
	{Detail, 's', "detail"},
	{Jump, 't', "jump"},
	{Filter, '/', "filter"},
	{History, 'h', "history"},
	{ResumeRun, 'v', "watch-run"},
	{Interactive, 'i', "interactive"},
	{Force, 'F', "force"},
	{Help, '?', "help"},
	{Quit, 'q', "quit"},
	{Search, '/', "search"},
	{NextMatch, 'n', "next-match"},
	{PrevMatch, 'N', "prev-match"},
	{FilterMatches, 'f', "filter-matches"},
	{ContextMore, ']', "context-more"},
	{ContextLess, '[', "context-less"},
	{Rerun, 'r', "rerun"},
	{ForceRerun, 'R', "force-rerun"},
	{Stop, 'x', "stop"},
	{StopAll, 'K', "stop-all"},
	{Input, 'i', "input"},
	{InteractiveRerun, 'I', "interactive-rerun"},
	{Follow, 'w', "follow"},
	{Watch, 'W', "watch"},
	{Yank, 'y', "yank"},
	{YankAll, 'Y', "yank-all"},
	{AllProjects, 'a', "all-projects"},
	{Top, 'g', "top"},
	{Bottom, 'G', "bottom"},
	{Fold, 'o', "fold"},
	{FoldAll, 'O', "fold-all"},
	{CloseSlot, 'X', "close-slot"},
}

var pickerActions = []Action{
	Pivot,
	Args,
	Detail,
	Jump,
	Filter,
	History,
	ResumeRun,
	Interactive,
	Force,
	Fold,
	FoldAll,
	// Stopping from here is not a convenience: with several slots open the picker is the
	// only screen that can reach a run without first loading its buffer to look at it.
	Stop,
	StopAll,
	Help,
	Quit,
}

var runActions = []Action{
	Fold,
	FoldAll,
	CloseSlot,
	Search,
	NextMatch,
	PrevMatch,
	FilterMatches,
	ContextMore,
	ContextLess,
	Rerun,
	ForceRerun,
	Args,
	Stop,
	StopAll,
	Input,
	InteractiveRerun,
	Follow,
	Watch,
	Yank,
	YankAll,
	History,
	Help,
	Quit,
}

var historyActions = []Action{Search, AllProjects, Help, Quit}

func defaultKey(action Action) rune {
	for _, d := range defaults {
		if d.action == action {
			return d.key
		}
	}
	return 0
}

// ActionName is the config name for an action, e.g. `filter-matches`.
func ActionName(action Action) string {
	for _, d := range defaults {
		if d.action == action {
			return d.name
		}
	}
	return ""
}

func ActionByName(name string) (Action, bool) {
	for _, d := range defaults {
		if d.name == name {
			return d.action, true
		}
	}
	return None, false
}

// All lists every action with its default key and config name, in table order.
func All() []struct {
	Action Action
	Key    rune
	Name   string
} {
	out := make([]struct {
		Action Action
		Key    rune
		Name   string
	}, 0, len(defaults))
	for _, d := range defaults {
		out = append(out, struct {
			Action Action
			Key    rune
			Name   string
		}{d.action, d.key, d.name})
	}
	return out
}

type bound struct {
	key    rune
	action Action
}

// Keymap says which character triggers which action.
//
// Screens are separate maps because the same key means different things depending on where
// you are — `a` is "run with arguments" in the picker and "all projects" in the history
// list, and `i` arms interactive mode in one and types at the task in the other.
type Keymap struct {
	picker  []bound
	run     []bound
	history []bound
}

func NewKeymap() *Keymap {
	build := func(actions []Action) []bound {
		out := make([]bound, 0, len(actions))
		for _, a := range actions {
			out = append(out, bound{defaultKey(a), a})
		}
		return out
	}
	return &Keymap{
		picker:  build(pickerActions),
		run:     build(runActions),
		history: build(historyActions),
	}
}

// Clone is what lets a config be applied without mutating the defaults.
func (k *Keymap) Clone() *Keymap {
	cp := func(in []bound) []bound { return append([]bound(nil), in...) }
	return &Keymap{picker: cp(k.picker), run: cp(k.run), history: cp(k.history)}
}

// Rebind points an action at a different key, wherever that action is available.
func (k *Keymap) Rebind(action Action, key rune) {
	for _, m := range [][]bound{k.picker, k.run, k.history} {
		for i := range m {
			if m[i].action == action {
				m[i].key = key
			}
		}
	}
}

func (k *Keymap) Picker(key rune) Action  { return look(k.picker, key) }
func (k *Keymap) Run(key rune) Action     { return look(k.run, key) }
func (k *Keymap) History(key rune) Action { return look(k.history, key) }

// look returns the first match, so a rebinding that collides with another action shadows
// it rather than doing both.
func look(m []bound, key rune) Action {
	for _, b := range m {
		if b.key == key {
			return b.action
		}
	}
	return None
}

// Conflicts lists keys bound to more than one action in the same screen. Reported rather
// than silently resolved — a shadowed key looks like a broken one.
func (k *Keymap) Conflicts() []string {
	var out []string
	for _, screen := range []struct {
		name string
		m    []bound
	}{
		{"picker", k.picker},
		{"run", k.run},
		{"history", k.history},
	} {
		for i, b := range screen.m {
			for _, earlier := range screen.m[:i] {
				if earlier.key == b.key {
					out = append(out, fmt.Sprintf("%s: `%c` is both %s and %s",
						screen.name, b.key, ActionName(earlier.action), ActionName(b.action)))
					break
				}
			}
		}
	}
	return out
}

type Binding struct {
	Keys string
	// What is the full explanation, for the `?` screen.
	What string
	// Footer is a two-or-three word label for the footer, or empty to keep this binding in
	// the full help only. Written out rather than derived from What: shortening prose
	// mechanically produced a 170-character line and labels like "fold or unfold a".
	Footer string
}

type Section struct {
	Title    string
	Note     string
	Bindings []Binding
}

func b(keys, what string) Binding { return Binding{Keys: keys, What: what} }

func f(keys, what, label string) Binding {
	return Binding{Keys: keys, What: what, Footer: label}
}

var Picker = Section{
	Title: "Picker",
	Note:  "browsing the Taskfile",
	Bindings: []Binding{
		b("j k ↑ ↓", "move"),
		b("gg G", "first / last row"),
		b("^d ^u", "half a screen down / up"),
		b("PgUp PgDn Home End", "move faster"),
		// No footer label: the picker's footer names the pivot you would switch to, which
		// is more use than the word "pivot" and would otherwise be printed twice.
		b("p", "toggle the pivot: by domain / by verb"),
		f("space o", "fold or unfold a group", "fold"),
		b("⇧O ⇥", "fold or unfold everything"),
		f("⏎", "run the task", "run"),
		f("a", "run it with arguments", "args"),
		f("i", "arm interactive mode for the next run", "interactive"),
		b("⇧F", "arm --force: ignore go-task's up-to-date checks"),
		f("/", "filter the list down to matching tasks", "filter"),
		f("t", "jump to a task, leaving the list intact", "jump"),
		f("s", "what this task is, and what it will run", "detail"),
		f("v", "go to whatever is running, or the last run", "watch"),
		f("h", "past runs", "history"),
		b("x", "stop this task's run, wherever it is — again to kill it"),
		b("⇧K", "stop every run, staying here"),
		b("?", "this screen"),
		b("esc", "back out of a filter, a jump, a panel — it does not quit"),
		b("q", "quit — always asks first"),
	},
}

var Run = Section{
	Title: "Run",
	Note:  "watching, or reading back, one run",
	Bindings: []Binding{
		b("j k ↑ ↓", "move"),
		b("gg G", "first / last row"),
		b("^d ^u", "half a screen down / up"),
		f("space o", "how much output: hidden, a peek at the last few lines, all of it", "fold"),
		b("⇧O", "move every task through the same three states"),
		f("/", "search the output", "search"),
		f("n N", "next / previous match", "next"),
		f("f", "filter to matching lines only", "filter"),
		// No footer label: it only means anything once you are already filtering, and the
		// footer has to make room for the slot switcher.
		b("[ ]", "less / more context around each hit"),
		f("r", "re-run this task, same arguments", "rerun"),
		b("⇧R", "the same, with --force — ignore go-task's up-to-date checks"),
		f("a", "re-run it with different arguments", "args"),
		f("i", "type at the running task — works even when you cannot see the prompt", "input"),
		b("⇧I", "re-run this task interactively, so prompts are visible"),
		f("x", "stop the run — press it again to SIGKILL the group", "stop"),
		b("⇧K", "stop every run, not just this one"),
		b("y", "copy the line under the cursor"),
		b("⇧Y", "copy everything this task printed"),
		b("w", "resume following the running task"),
		b("⇧W", "watch: re-run this task whenever the source changes"),
		b("h", "past runs"),
		f("⇥ ⇧⇥", "switch to the next / previous run", "switch"),
		b("1…9", "switch straight to that slot"),
		b("⇧X", "close the slot — only once its run has stopped"),
		b("?", "this screen"),
		f("esc", "back to the picker — every run keeps going", "back"),
		b("q", "quit, stopping every run — asks first"),
	},
}

var HistorySection = Section{
	Title: "History",
	Note:  "runs already finished, scoped to this project",
	Bindings: []Binding{
		b("j k ↑ ↓", "move"),
		b("gg G", "first / last row"),
		f("⏎", "reopen the run", "open"),
		f("/", "search across every stored run", "search runs"),
		f("a", "widen to all projects", "all projects"),
		b("?", "this screen"),
		f("esc", "back to the picker", "back"),
		b("q", "quit"),
	},
}

var DetailSection = Section{
	Title: "Detail",
	Note:  "what a task is, before you run it",
	Bindings: []Binding{
		b("j k", "scroll"),
		b("⏎", "run it"),
		b("a", "run it with arguments"),
		b("s esc", "back"),
	},
}

var Prompts = Section{
	Title: "Prompts",
	Note:  "while a prompt is open, these take over",
	Bindings: []Binding{
		b("arguments", "← → Home End Delete edit · ⏎ run · esc cancel"),
		b("search / filter", "⏎ keep the query · esc clear · ↑ ↓ step through matches"),
		b("input", "every key goes to the task · esc stop typing"),
		b("confirmation", "y goes ahead · anything else cancels"),
	},
}

var Sections = []*Section{&Picker, &Run, &HistorySection, &DetailSection, &Prompts}

// Footer builds the footer line for a section: the bindings worth the space, in table
// order.
func Footer(section *Section) string {
	var parts []string
	for _, b := range section.Bindings {
		if b.Footer != "" {
			parts = append(parts, b.Keys+" "+b.Footer)
		}
	}
	return strings.Join(parts, "   ")
}

// WidestKeys is the widest key column across every section, so the `?` screen's
// descriptions line up as one table rather than five.
func WidestKeys() int {
	widest := 10
	for _, s := range Sections {
		for _, b := range s.Bindings {
			if n := utf8.RuneCountInString(b.Keys); n > widest {
				widest = n
			}
		}
	}
	return widest
}
