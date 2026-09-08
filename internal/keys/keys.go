// Package keys is the keymap, as data.
//
// Both the `?` screen and the one-line footer hints are generated from this table, so they
// cannot disagree with each other. They could still disagree with the handlers in the app
// package — nothing here dispatches anything — but a single table that both surfaces read
// from is the difference between one place to update and three.
package keys

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
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
	Order
)

// Mods is what was held down with a key.
type Mods uint8

const (
	ModCtrl Mods = 1 << iota
	ModAlt
	ModShift
)

// Chord is a key and its modifiers — what a binding actually is.
//
// Shift only ever appears here for keys it cannot change: ␣, and whatever else a terminal
// reports unshifted. On a letter the shift is already in the rune, so `G` is a chord with no
// modifiers and `shift+g` is not a way to spell it.
type Chord struct {
	Key  rune
	Mods Mods
}

// Plain is the chord for one unmodified character, which is what every default is.
func Plain(key rune) Chord { return Chord{Key: key} }

// String is the config spelling, so what Conflicts reports and what `--dump-config` writes
// are the same thing you would type back in.
func (c Chord) String() string {
	var b strings.Builder
	for _, m := range []struct {
		bit  Mods
		name string
	}{{ModCtrl, "ctrl"}, {ModAlt, "alt"}, {ModShift, "shift"}} {
		if c.Mods&m.bit != 0 {
			b.WriteString(m.name)
			b.WriteString("+")
		}
	}
	if c.Key == ' ' {
		b.WriteString("space")
	} else {
		b.WriteRune(c.Key)
	}
	return b.String()
}

// Display is how a chord is written in the help and the footer, which is deliberately not
// how String writes it for a config.
//
// `⇧S` reads as a key you press; `S` reads as a letter you type. The config has to be
// typed back in, so it stays `S` there — and the help has to be read, so it gets the
// glyph. Ctrl is `^d`, to match the motion keys that have always been written that way.
func (c Chord) Display() string {
	var b strings.Builder
	if c.Mods&ModCtrl != 0 {
		b.WriteString("^")
	}
	if c.Mods&ModAlt != 0 {
		b.WriteString("alt+")
	}
	// Shift is reported separately only on keys it cannot change; everywhere else it is
	// already in the character. Both are the same press, so both get the glyph.
	if c.Mods&ModShift != 0 || unicode.IsUpper(c.Key) {
		b.WriteString("⇧")
	}
	if c.Key == ' ' {
		b.WriteString("space")
	} else {
		b.WriteRune(c.Key)
	}
	return b.String()
}

// modNames is the vocabulary ParseChord accepts, and the only one.
var modNames = map[string]Mods{"ctrl": ModCtrl, "alt": ModAlt, "shift": ModShift}

// ParseChord reads a binding: a single character, or modifiers and a key joined by `+`.
//
// `space` names the one key you cannot write literally in a config and read back later. `+`
// itself is a character, so a bare `+` parses as that key rather than as an empty modifier —
// which is why the split only happens when there is something on both sides.
func ParseChord(s string) (Chord, error) {
	if s == "" {
		return Chord{}, fmt.Errorf("empty")
	}
	var mods Mods
	for {
		i := strings.Index(s, "+")
		if i <= 0 || i == len(s)-1 {
			break
		}
		name := strings.ToLower(s[:i])
		bit, ok := modNames[name]
		if !ok {
			return Chord{}, fmt.Errorf("`%s` is not a modifier — use ctrl, alt or shift", name)
		}
		if mods&bit != 0 {
			return Chord{}, fmt.Errorf("`%s` twice", name)
		}
		mods |= bit
		s = s[i+1:]
	}

	key := s
	if strings.EqualFold(key, "space") {
		key = " "
	}
	runes := []rune(key)
	if len(runes) != 1 {
		return Chord{}, fmt.Errorf("`%s` is not a single key", key)
	}
	// Shift on a bare letter is the one mistake worth naming: it looks reasonable, and it
	// would silently never match, because the terminal sends `G` and not shift+`g`. With ctrl
	// or alt also held the key produces no text, so the shift is reported separately and does
	// mean something.
	folded := mods&(ModCtrl|ModAlt) == 0
	if folded && mods&ModShift != 0 && unicode.ToUpper(runes[0]) != unicode.ToLower(runes[0]) {
		return Chord{}, fmt.Errorf("shift is already in `%c` — write `%c` instead",
			runes[0], unicode.ToUpper(runes[0]))
	}
	return Chord{Key: runes[0], Mods: mods}, nil
}

type binding struct {
	action Action
	key    rune
	name   string
}

// defaults pair each action with its default key and the config name used to rebind it.
var defaults = []binding{
	{Pivot, 'p', "pivot"},
	{Args, 'a', "args"},
	{Detail, 'd', "detail"},
	{Jump, 'f', "jump"},
	{Filter, '/', "filter"},
	{History, 'h', "history"},
	{ResumeRun, 'v', "view-run"},
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
	{Order, 'S', "sort"},
}

var pickerActions = []Action{
	Pivot,
	Order,
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
	Watch,
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

// The detail panel reads what a task will run, which is the moment you want to run it, run
// it differently, or go and change it. The detail key closes it again, as it opened it.
var detailActions = []Action{Args, Edit, Detail, Help, Quit}

// The `?` screen has three keys of its own, and they are the same three actions they are
// everywhere else — jump finds a binding here exactly as it finds a task in the picker.
var helpActions = []Action{Jump, Help, Quit}

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
	Key    Chord
	Name   string
} {
	out := make([]struct {
		Action Action
		Key    Chord
		Name   string
	}, 0, len(defaults))
	for _, d := range defaults {
		out = append(out, struct {
			Action Action
			Key    Chord
			Name   string
		}{d.action, Plain(d.key), d.name})
	}
	return out
}

type bound struct {
	chord  Chord
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
	detail   []bound
	help     []bound
}

func NewKeymap() *Keymap {
	build := func(actions []Action) []bound {
		out := make([]bound, 0, len(actions))
		for _, a := range actions {
			out = append(out, bound{Plain(defaultKey(a)), a})
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
		detail:   build(detailActions),
		help:     build(helpActions),
	}
}

// Clone is what lets a config be applied without mutating the defaults.
func (k *Keymap) Clone() *Keymap {
	cp := func(in []bound) []bound { return append([]bound(nil), in...) }
	return &Keymap{
		picker: cp(k.picker), run: cp(k.run), history: cp(k.history),
		timeline: cp(k.timeline), diff: cp(k.diff), profile: cp(k.profile),
		detail: cp(k.detail), help: cp(k.help),
	}
}

// screens is every map in the struct, for the operations that mean "all of them" —
// rebinding, cloning, and reporting collisions. A map missing from here is a screen whose
// keys quietly stop being configurable, which is how the detail panel spent a while with a
// hardcoded `q`.
func (k *Keymap) screens() []struct {
	name string
	m    []bound
} {
	return []struct {
		name string
		m    []bound
	}{
		{"picker", k.picker},
		{"run", k.run},
		{"history", k.history},
		{"timeline", k.timeline},
		{"diff", k.diff},
		{"profile", k.profile},
		{"detail", k.detail},
		{"help", k.help},
	}
}

// Rebind points an action at a different key, wherever that action is available.
func (k *Keymap) Rebind(action Action, chord Chord) {
	for _, screen := range k.screens() {
		for i := range screen.m {
			if screen.m[i].action == action {
				screen.m[i].chord = chord
			}
		}
	}
}

func (k *Keymap) Picker(c Chord) Action   { return look(k.picker, c) }
func (k *Keymap) Run(c Chord) Action      { return look(k.run, c) }
func (k *Keymap) History(c Chord) Action  { return look(k.history, c) }
func (k *Keymap) Timeline(c Chord) Action { return look(k.timeline, c) }
func (k *Keymap) Diff(c Chord) Action     { return look(k.diff, c) }
func (k *Keymap) Profile(c Chord) Action  { return look(k.profile, c) }
func (k *Keymap) Detail(c Chord) Action   { return look(k.detail, c) }
func (k *Keymap) Help(c Chord) Action     { return look(k.help, c) }

// KeyOf is the chord an action sits on, wherever it is offered.
//
// One answer for every screen, because there is only ever one: the defaults are built from
// a single table and Rebind reaches every map, so a screen cannot disagree with another
// about where an action lives. That is what lets the help spell a key without first asking
// which screen it is about.
func (k *Keymap) KeyOf(action Action) (Chord, bool) {
	for _, screen := range k.screens() {
		for _, b := range screen.m {
			if b.action == action {
				return b.chord, true
			}
		}
	}
	return Chord{}, false
}

// look returns the first match, so a rebinding that collides with another action shadows
// it rather than doing both.
func look(m []bound, c Chord) Action {
	for _, b := range m {
		if b.chord == c {
			return b.action
		}
	}
	return None
}

// literal is a key a screen answers directly, without consulting the map.
type literal struct {
	chord Chord
	what  string
}

// motions are answered on every screen, by handleNavKey, before the keymap is reached.
var motions = []literal{
	{Plain('j'), "move down"},
	{Plain('k'), "move up"},
	{Plain('g'), "first row"},
	{Plain('G'), "last row"},
	{Chord{Key: 'd', Mods: ModCtrl}, "half a page down"},
	{Chord{Key: 'u', Mods: ModCtrl}, "half a page up"},
	{Chord{Key: 'f', Mods: ModCtrl}, "a page down"},
	{Chord{Key: 'b', Mods: ModCtrl}, "a page up"},
}

// ownLiterals is what a screen answers on its own, past the motions every screen shares.
var ownLiterals = map[string][]literal{
	"picker": {{Plain('{'), "previous group"}, {Plain('}'), "next group"}, {Plain(' '), "fold a group"}},
	"run":    {{Plain(' '), "fold the output"}},
}

func init() {
	// The run view's slots. Written as a loop because nine near-identical lines of table is
	// nine chances to typo a digit.
	for c := '1'; c <= '9'; c++ {
		ownLiterals["run"] = append(ownLiterals["run"], literal{Plain(c), "that slot"})
	}
}

// reservedIn is every key a screen answers without asking the map.
func reservedIn(screen string) []literal {
	out := make([]literal, 0, len(motions)+len(ownLiterals[screen]))
	out = append(out, motions...)
	return append(out, ownLiterals[screen]...)
}

// Conflicts lists keys bound to more than one thing in the same screen. Reported rather
// than silently resolved — a shadowed key looks like a broken one.
//
// The literal keys are checked as well as the mapped ones, which they were not for a long
// while: `keys: help: space` opened the keymap and quietly cost the picker its fold key,
// and nothing here said a word, because the fold key was not in the map to collide with.
// Which of the two survives depends on where in a handler the check happens to sit — so
// the report is the only thing standing between a config and a key that silently stopped
// working.
func (k *Keymap) Conflicts() []string {
	var out []string
	for _, screen := range k.screens() {
		reserved := reservedIn(screen.name)
		for i, b := range screen.m {
			for _, earlier := range screen.m[:i] {
				if earlier.chord == b.chord {
					out = append(out, fmt.Sprintf("%s: `%s` is both %s and %s",
						screen.name, b.chord, ActionName(earlier.action), ActionName(b.action)))
					break
				}
			}
			for _, lit := range reserved {
				if lit.chord == b.chord {
					out = append(out, fmt.Sprintf("%s: `%s` is both %s and %s",
						screen.name, b.chord, lit.what, ActionName(b.action)))
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

// placeholder matches `{action-name}` in a binding's Keys — see Spell.
var placeholder = regexp.MustCompile(`\{([a-z-]+)\}`)

// Spell fills a binding's placeholders from the keymap in force.
//
// The table used to carry the keys as literal text, which meant `keys: jump: z` moved the
// key and left the `?` screen and every footer still saying `t`. A binding written as
// `{jump}` cannot: it names the action, and the spelling comes from the same map dispatch
// reads. Keys with no action behind them — the motions, `⏎`, `esc`, `1…9` — stay literal,
// because there is nothing to rebind them to.
//
// An unknown name is left as it was written rather than blanked, so a typo shows up as
// `{jmup}` on the screen instead of a hint that silently lost its key. TestEveryPlaceholder
// NamesAnAction is what stops one reaching a release.
func Spell(text string, km *Keymap) string {
	return placeholder.ReplaceAllStringFunc(text, func(match string) string {
		action, ok := ActionByName(match[1 : len(match)-1])
		if !ok {
			return match
		}
		chord, ok := km.KeyOf(action)
		if !ok {
			return match
		}
		return chord.Display()
	})
}

// Spelled is a section's bindings with their keys spelled for this keymap. Copies, so the
// table itself stays the unspelled source every surface reads from.
func Spelled(section *Section, km *Keymap) []Binding {
	out := make([]Binding, 0, len(section.Bindings))
	for _, b := range section.Bindings {
		// Every field, not just the keys: the prompts describe themselves in prose, and
		// "opened with {jump}" is as much a promise about a key as a row in the key column.
		b.Keys, b.What, b.Footer = Spell(b.Keys, km), Spell(b.What, km), Spell(b.Footer, km)
		out = append(out, b)
	}
	return out
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

// moving is the motion keys, which are the same on every screen — see handleNavKey in the
// app package, which is the other half of that claim: one set of motions, dispatched ahead
// of anything a screen has of its own. Written once here for the same reason it is
// dispatched once there. Eight copies of a list is eight chances for one of them to drift,
// and the run view really did spend a while with no Home or End.
//
// The vim key is named first and the labelled key second, because the labelled one is the
// one you can find without being told.
var moving = []Binding{
	b("j k ↑ ↓", "move"),
	b("^d ^u", "half a screen down / up"),
	b("^f ^b", "a whole screen — PgDn and PgUp do the same"),
	b("gg G", "first / last row — so do Home and End"),
}

// moves puts the shared motions at the top of a section, ahead of what that screen adds.
//
// Copied rather than appended to in place: one backing array handed out eight times is
// seven sections quietly overwriting each other's bindings.
func moves(rest ...Binding) []Binding {
	return append(append([]Binding(nil), moving...), rest...)
}

// scrolls is moves for a screen that is a document rather than a list.
//
// Same motions, with the first line given a footer label: you arrive on a long page of
// text wanting to know how to get further down it, and that is worth the footer space that
// a list — where the motions are the obvious part — spends on something else.
func scrolls(rest ...Binding) []Binding {
	out := moves(rest...)
	out[0].What, out[0].Footer = "scroll the keymap", "scroll"
	return out
}

var Picker = Section{
	Title: "Picker",
	Note:  "browsing the Taskfile",
	Bindings: moves(
		b("{ }", "previous / next group, over whatever is under this one"),
		// No footer label: the picker's footer names the pivot you would switch to, which
		// is more use than the word "pivot" and would otherwise be printed twice.
		b("{pivot}", "cycle the grouping: by domain, by verb, by file, and any your config added"),
		// No footer label: the footer already carries the pivot's own list in the header's
		// place, and a second cycling key on the same line reads as a duplicate of it.
		b("{sort}", "cycle the order rows are read in: name, file, recent, failed, size"),
		f("space", "fold or unfold a group", "fold"),
		// No footer label: the same fold `space` already offers, on the keys a tree makes you
		// reach for. Naming it twice on the one line everything competes for would cost a
		// hint that is not an alias of anything.
		b("← →", "the same fold, for hands that reach for a tree's keys"),
		f("{fold}", "how much of the run under a task: hidden, a peek, all of it", "output"),
		// No footer label: with `space fold` and `o output` both on the line, the footer is
		// full — and folding the whole tree at once is a thing you go looking for, where
		// the two single-row keys are things you press without thinking.
		b("{fold-all} ⇥", "fold or unfold every group"),
		f("⏎", "run the task, or every marked one — the run unfolds under its row", "run"),
		f("{mark}", "mark a task to run alongside others", "mark"),
		b("{clear-marks}", "clear every mark"),
		f("{args}", "run it with arguments", "args"),
		// No footer label: arming a modifier for the next run is secondary to running one, and
		// the footer is the one place where everything competes for the same line.
		b("{interactive}", "arm interactive mode for the next run — again to disarm"),
		b("{force}", "arm --force: ignore go-task's up-to-date checks — again to disarm"),
		b("{watch}", "watch: re-run the marked set, or this task, whenever the source changes"),
		f("{filter}", "filter the list down to matching tasks", "filter"),
		f("{jump}", "jump to a task, leaving the list intact", "jump"),
		f("{detail}", "what this task is, and what it will run", "detail"),
		f("{view-run}", "the whole screen for whatever is running, or the last run", "watch"),
		b("{timeline}", "how this one task has been going, run after run"),
		b("{edit}", "open this task's own definition in $EDITOR"),
		f("{history}", "past runs", "history"),
		b("{stop}", "stop this task's run, wherever it is — again to kill it"),
		b("{stop-all}", "stop every run, staying here"),
		b("{help}", "this screen"),
		b("esc", "back out of a filter, a jump, a panel — it does not quit"),
		b("{quit}", "quit — always asks first"),
	),
}

var Run = Section{
	Title: "Run",
	Note:  "watching, or reading back, one run",
	Bindings: moves(
		f("space {fold}", "how much output: hidden, a peek at the last few lines, all of it", "fold"),
		// No footer label, as in the picker: an alias for a key already on the line.
		b("← →", "the same three states, for hands that reach for a tree's keys"),
		f("{fold-all}", "move every task through the same three states", "all"),
		f("{search}", "search the output", "search"),
		// No footer label: like `[ ]`, it only means anything once a search is running, and
		// the footer has to make room for the slot switcher.
		b("{next-match} {prev-match}", "next / previous match"),
		f("{filter-matches}", "filter to matching lines only", "filter"),
		// No footer label: it only means anything once you are already filtering, and the
		// footer has to make room for the slot switcher.
		b("{context-less} {context-more}", "less / more context around each hit"),
		f("{rerun}", "re-run this task, same arguments", "rerun"),
		b("{force-rerun}", "the same, with --force — ignore go-task's up-to-date checks"),
		f("{rerun-failed}", "re-run everything in this run that failed, each in its own slot", "failed"),
		b("{args}", "re-run it with different arguments"),
		// No footer label: when a task actually is waiting, the run view says so in a bar of
		// its own that names this key — which is the moment you need to be told, and the
		// footer is not it.
		b("{input}", "type at the running task — works even when you cannot see the prompt"),
		b("{interactive-rerun}", "re-run this task interactively, so prompts are visible"),
		f("{stop}", "stop the run — press it again to SIGKILL the group", "stop"),
		b("{stop-all}", "stop every run, not just this one"),
		b("{yank}", "copy the line under the cursor"),
		b("{yank-all}", "copy everything this task printed"),
		f("{edit}", "open the file:line under the cursor in $EDITOR", "edit"),
		b("{diff}", "what changed since this task last passed"),
		b("{timeline}", "how this one task has been going, run after run"),
		b("{profile}", "where this run's time went, slowest first"),
		b("{follow}", "resume following the running task"),
		b("{watch}", "watch: re-run the marked set, or this task, whenever the source changes"),
		b("{history}", "past runs"),
		f("⇥ ⇧⇥", "switch to the next / previous run", "switch"),
		b("1…9", "switch straight to that slot"),
		b("{close-slot}", "close the slot — only once its run has stopped"),
		b("{detach}", "detach: let this run outlive taskui, output stops here"),
		b("{help}", "this screen"),
		f("esc", "back to the picker — every run keeps going", "back"),
		b("{quit}", "quit, stopping every run — asks first"),
	),
}

var HistorySection = Section{
	Title: "History",
	Note:  "runs already finished, scoped to this project",
	Bindings: moves(
		f("⏎", "reopen the run", "open"),
		f("{search}", "search across every stored run", "search runs"),
		f("{all-projects}", "widen: this repo's other worktrees, then every project", "widen"),
		b("{help}", "this screen"),
		f("esc", "back to the picker", "back"),
		b("{quit}", "quit"),
	),
}

var TimelineSection = Section{
	Title: "Timeline",
	Note:  "one task, run after run",
	Bindings: moves(
		f("⏎", "open that run", "open"),
		f("{diff}", "what changed at this run — against the last one that went differently", "diff"),
		b("{help}", "this screen"),
		f("esc", "back to wherever you opened this from", "back"),
		b("{quit}", "quit"),
	),
}

var DiffSection = Section{
	Title: "Diff",
	Note:  "what changed between two runs of one task",
	Bindings: moves(
		f("{context-less} {context-more}", "less / more unchanged context", "context"),
		f("{edit}", "open the file:line under the cursor in $EDITOR", "edit"),
		b("{help}", "this screen"),
		f("esc", "back to the run, or to the timeline", "back"),
		b("{quit}", "quit"),
	),
}

var ProfileSection = Section{
	Title: "Profile",
	Note:  "where a run's time went",
	Bindings: moves(
		f("⏎", "go to that task in the run", "go to"),
		f("{edit}", "open its definition in $EDITOR", "edit"),
		b("{help}", "this screen"),
		f("esc", "back to the run", "back"),
		b("{quit}", "quit"),
	),
}

var DetailSection = Section{
	Title: "Detail",
	Note:  "what a task is, before you run it",
	Bindings: moves(
		f("⏎", "run it", "run"),
		f("{args}", "run it with arguments", "args"),
		f("{edit}", "open this task's own definition in $EDITOR", "edit"),
		f("{detail} esc", "back to the picker", "back"),
		b("{help}", "this screen"),
		b("{quit}", "quit"),
	),
}

// HelpSection is the `?` screen's own keys.
//
// It was for a long time the one screen with no entry of its own, which made it the one
// screen whose footer had to be written out by hand — and so the one that could disagree
// with the very table it exists to display.
var HelpSection = Section{
	Title: "Keys",
	Note:  "this screen",
	Bindings: scrolls(
		f("{jump}", "find a binding — ⏎ keeps what is left, esc clears the query", "find"),
		f("esc {help}", "close, and go back to where you were", "close"),
		// The one screen where quit earns the space: it is what a first-time reader opened
		// this to find out, and there is no `? keys` here to point them anywhere else.
		f("{quit}", "quit — always asks first", "quit"),
	),
}

var Prompts = Section{
	Title: "Prompts",
	Note:  "while a prompt is open, these take over",
	Bindings: []Binding{
		b("arguments", "⇥ ⇧⇥ complete · ← → Home End Delete edit · ⏎ run · esc cancel"),
		b("search / filter", "⏎ keep the query · esc clear · ↑ ↓ step through matches"),
		b("…while one is open", "the letters are the query; ^d ^u ^f ^b still page what is behind it"),
		b("find (on this screen)", "opened with {jump} · ⏎ keeps what is left · esc clears, then esc closes"),
		b("input", "every key goes to the task · esc stop typing"),
		b("confirmation", "y goes ahead · anything else cancels"),
	},
}

var Sections = []*Section{
	&Picker, &Run, &HistorySection, &TimelineSection, &DiffSection, &ProfileSection, &DetailSection,
	&HelpSection, &Prompts,
}

// FooterHints is the bindings a section puts in the footer, in table order, each already
// split into the keys and the label so the renderer can style them separately.
func FooterHints(section *Section, km *Keymap) []Binding {
	var out []Binding
	for _, b := range Spelled(section, km) {
		if b.Footer != "" {
			out = append(out, b)
		}
	}
	return out
}

// Footer builds the footer line for a section: the bindings worth the space, in table
// order.
func Footer(section *Section, km *Keymap) string {
	var parts []string
	for _, b := range FooterHints(section, km) {
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
func WidestKeys(km *Keymap) int {
	widest := 10
	for _, s := range Sections {
		for _, b := range Spelled(s, km) {
			if n := utf8.RuneCountInString(b.Keys); n > widest {
				widest = n
			}
		}
	}
	return widest
}
