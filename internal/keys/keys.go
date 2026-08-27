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
	Edit
	Timeline
	Diff
	Profile
	Mark
	ClearMarks
	Detach
	RerunFailed
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
	{Edit, 'e', "edit"},
	{Timeline, 'H', "timeline"},
	{Diff, 'D', "diff"},
	{Profile, 'T', "profile"},
	{Mark, 'm', "mark"},
	{ClearMarks, 'M', "clear-marks"},
	{Detach, 'A', "detach"},
	{RerunFailed, 'F', "rerun-failed"},
}

var pickerActions = []Action{
	Pivot,
	Args,
	Detail,
	Jump,
	Filter,
	History,
	Timeline,
	Edit,
	Mark,
	ClearMarks,
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
	RerunFailed,
	Args,
	Stop,
	StopAll,
	Input,
	InteractiveRerun,
	Follow,
	Watch,
	Yank,
	YankAll,
	Edit,
	History,
	Timeline,
	Diff,
	Profile,
	Detach,
	Help,
	Quit,
}

var historyActions = []Action{Search, AllProjects, Help, Quit}

// The timeline is a list of one task's runs, and the diff is what changed between two of
// them — so `D` belongs there as much as it does in the run view.
var timelineActions = []Action{Diff, Help, Quit}

var profileActions = []Action{Edit, Help, Quit}

// The diff view can reach an editor too: a `file:line` in a line that just appeared is
// the most direct answer the tool has to "what broke".
var diffActions = []Action{Edit, ContextMore, ContextLess, Help, Quit}

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
	picker   []bound
	run      []bound
	history  []bound
	timeline []bound
	diff     []bound
	profile  []bound
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
		picker:   build(pickerActions),
		run:      build(runActions),
		history:  build(historyActions),
		timeline: build(timelineActions),
		diff:     build(diffActions),
		profile:  build(profileActions),
	}
}

// Clone is what lets a config be applied without mutating the defaults.
func (k *Keymap) Clone() *Keymap {
	cp := func(in []bound) []bound { return append([]bound(nil), in...) }
	return &Keymap{
		picker: cp(k.picker), run: cp(k.run), history: cp(k.history),
		timeline: cp(k.timeline), diff: cp(k.diff), profile: cp(k.profile),
	}
}

// Rebind points an action at a different key, wherever that action is available.
func (k *Keymap) Rebind(action Action, key rune) {
	for _, m := range [][]bound{k.picker, k.run, k.history, k.timeline, k.diff, k.profile} {
		for i := range m {
			if m[i].action == action {
				m[i].key = key
			}
		}
	}
}

func (k *Keymap) Picker(key rune) Action   { return look(k.picker, key) }
func (k *Keymap) Run(key rune) Action      { return look(k.run, key) }
func (k *Keymap) History(key rune) Action  { return look(k.history, key) }
func (k *Keymap) Timeline(key rune) Action { return look(k.timeline, key) }
func (k *Keymap) Diff(key rune) Action     { return look(k.diff, key) }
func (k *Keymap) Profile(key rune) Action  { return look(k.profile, key) }

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
		{"timeline", k.timeline},
		{"diff", k.diff},
		{"profile", k.profile},
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
		b("p", "cycle the grouping: by domain, by verb, by file, and any your config added"),
		f("space o", "fold or unfold a group", "fold"),
		f("⇧O ⇥", "fold or unfold everything", "all"),
		f("⏎", "run the task, or every marked one", "run"),
		f("m", "mark a task to run alongside others", "mark"),
		b("⇧M", "clear every mark"),
		f("a", "run it with arguments", "args"),
		// No footer label: arming a modifier for the next run is secondary to running one, and
		// the footer is the one place where everything competes for the same line.
		b("i", "arm interactive mode for the next run"),
		b("⇧F", "arm --force: ignore go-task's up-to-date checks"),
		f("/", "filter the list down to matching tasks", "filter"),
		f("t", "jump to a task, leaving the list intact", "jump"),
		f("s", "what this task is, and what it will run", "detail"),
		f("v", "go to whatever is running, or the last run", "watch"),
		b("⇧H", "how this one task has been going, run after run"),
		b("e", "open this task's own definition in $EDITOR"),
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
		f("⇧O", "move every task through the same three states", "all"),
		f("/", "search the output", "search"),
		// No footer label: like `[ ]`, it only means anything once a search is running, and
		// the footer has to make room for the slot switcher.
		b("n N", "next / previous match"),
		f("f", "filter to matching lines only", "filter"),
		// No footer label: it only means anything once you are already filtering, and the
		// footer has to make room for the slot switcher.
		b("[ ]", "less / more context around each hit"),
		f("r", "re-run this task, same arguments", "rerun"),
		b("⇧R", "the same, with --force — ignore go-task's up-to-date checks"),
		f("⇧F", "re-run everything in this run that failed, each in its own slot", "failed"),
		b("a", "re-run it with different arguments"),
		// No footer label: when a task actually is waiting, the run view says so in a bar of
		// its own that names this key — which is the moment you need to be told, and the
		// footer is not it.
		b("i", "type at the running task — works even when you cannot see the prompt"),
		b("⇧I", "re-run this task interactively, so prompts are visible"),
		f("x", "stop the run — press it again to SIGKILL the group", "stop"),
		b("⇧K", "stop every run, not just this one"),
		b("y", "copy the line under the cursor"),
		b("⇧Y", "copy everything this task printed"),
		f("e", "open the file:line under the cursor in $EDITOR", "edit"),
		b("⇧D", "what changed since this task last passed"),
		b("⇧H", "how this one task has been going, run after run"),
		b("⇧T", "where this run's time went, slowest first"),
		b("w", "resume following the running task"),
		b("⇧W", "watch: re-run this task whenever the source changes"),
		b("h", "past runs"),
		f("⇥ ⇧⇥", "switch to the next / previous run", "switch"),
		b("1…9", "switch straight to that slot"),
		b("⇧X", "close the slot — only once its run has stopped"),
		b("⇧A", "detach: let this run outlive taskui, output stops here"),
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

var TimelineSection = Section{
	Title: "Timeline",
	Note:  "one task, run after run",
	Bindings: []Binding{
		b("j k ↑ ↓", "move"),
		b("gg G", "first / last row"),
		f("⏎", "open that run", "open"),
		f("⇧D", "what changed at this run — against the last one that went differently", "diff"),
		b("?", "this screen"),
		f("esc", "back to wherever you opened this from", "back"),
		b("q", "quit"),
	},
}

var DiffSection = Section{
	Title: "Diff",
	Note:  "what changed between two runs of one task",
	Bindings: []Binding{
		b("j k ↑ ↓", "scroll"),
		b("gg G", "first / last row"),
		f("[ ]", "less / more unchanged context", "context"),
		f("e", "open the file:line under the cursor in $EDITOR", "edit"),
		b("?", "this screen"),
		f("esc", "back to the run, or to the timeline", "back"),
		b("q", "quit"),
	},
}

var ProfileSection = Section{
	Title: "Profile",
	Note:  "where a run's time went",
	Bindings: []Binding{
		b("j k ↑ ↓", "move"),
		b("gg G", "first / last row"),
		f("⏎", "go to that task in the run", "go to"),
		f("e", "open its definition in $EDITOR", "edit"),
		b("?", "this screen"),
		f("esc", "back to the run", "back"),
		b("q", "quit"),
	},
}

var DetailSection = Section{
	Title: "Detail",
	Note:  "what a task is, before you run it",
	Bindings: []Binding{
		b("j k", "scroll"),
		f("⏎", "run it", "run"),
		f("a", "run it with arguments", "args"),
		f("e", "open this task's own definition in $EDITOR", "edit"),
		f("s esc", "back to the picker", "back"),
		b("q", "quit"),
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

var Sections = []*Section{
	&Picker, &Run, &HistorySection, &TimelineSection, &DiffSection, &ProfileSection, &DetailSection,
	&Prompts,
}

// FooterHints is the bindings a section puts in the footer, in table order, each already
// split into the keys and the label so the renderer can style them separately.
func FooterHints(section *Section) []Binding {
	var out []Binding
	for _, b := range section.Bindings {
		if b.Footer != "" {
			out = append(out, b)
		}
	}
	return out
}

// Footer builds the footer line for a section: the bindings worth the space, in table
// order.
func Footer(section *Section) string {
	var parts []string
	for _, b := range FooterHints(section) {
		parts = append(parts, b.Keys+" "+b.Footer)
	}
	return strings.Join(parts, "   ")
}

// FooterFits is how many of a section's hints fit in width, given that `reserve` columns
// are already spoken for.
//
// It stops at a binding boundary rather than at a character. The footer used to be built
// at full length and clipped by the renderer, which ended it mid-word — `t jump   s deta` —
// and a hint you cannot finish reading is worse than one that was never offered.
func FooterFits(hints []Binding, width, reserve int) int {
	used := 0
	for i, b := range hints {
		cost := utf8.RuneCountInString(b.Keys) + 1 + utf8.RuneCountInString(b.Footer)
		if i > 0 {
			cost += 3 // the gap between hints
		}
		if used+cost > width-reserve {
			return i
		}
		used += cost
	}
	return len(hints)
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
