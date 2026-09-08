package app

import (
	"fmt"
	"os"
	"os/exec"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/ddromanidis/taskui/internal/keys"
)

// tickMsg drives the poll loop. A run's capture goroutine fills its queue whenever it
// likes; this is what drains it and redraws.
type tickMsg struct{}

// liveTick is the cadence while something is running, idleTick the one while nothing is.
//
// Any slot counts, not just the one on screen: the picker ticks an elapsed time for every
// running task, and a parked run's counter should not advance in 200ms lurches while the
// focused one gets 50.
const (
	liveTick = 50 * time.Millisecond
	idleTick = 200 * time.Millisecond
)

// statusLife is how long a notice holds the footer before it hands the keys back.
//
// The status line and the hint bar are the same row, so a message that never leaves is a
// footer you cannot read — and `grouped by verb` was still sitting there twenty minutes
// later, hiding the only place the keys are listed. Three seconds catches it on the way
// past and gives the row back before you go looking for a binding.
const statusLife = 3 * time.Second

func (a *App) tick() tea.Cmd {
	d := idleTick
	if a.AnyInFlight() {
		d = liveTick
	}
	// A theme that animates needs a frame on its own schedule, and the poll loop is
	// already the thing that wakes up — so it wakes up a little more often rather than a
	// second timer racing it.
	if step := a.Theme.Animation.Interval; a.Theme.Animation.Moves() && step < d {
		d = step
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) Init() tea.Cmd { return a.tick() }

// Update is the loop's one door, which is what lets the status line be timed in one place:
// whatever a message did to it, expireStatus sees the result on the way out.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := a.update(msg)
	a.expireStatus()
	return model, cmd
}

// expireStatus takes the footer back once a notice has had its three seconds.
//
// Timed from here rather than from a setter every caller has to remember: a status is a
// plain field written in a hundred places, and noticing that the text changed is the same
// thing as noticing it was set. Re-arming on a change means a notice that arrives while
// another is showing gets its own three seconds rather than the rest of someone else's.
func (a *App) expireStatus() {
	if a.Status != a.statusShown {
		a.statusShown, a.statusAt = a.Status, time.Now()
		return
	}
	if a.Status != "" && time.Since(a.statusAt) >= statusLife {
		a.Status, a.statusShown = "", ""
	}
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.Width, a.Height = msg.Width, msg.Height
		return a, nil

	case editorFailed:
		a.Status = "could not run your editor: " + msg.err.Error()
		return a, nil

	case tickMsg:
		if a.Theme.Animation.Moves() {
			a.Phase++
		}
		a.PollRun()
		a.PollWatch()
		a.PollTaskfile()
		a.collectDetails()
		a.collectCoverage()
		a.collectReload()
		a.refreshProfile()
		a.noteFinished()
		return a, tea.Batch(a.tick(), a.ringBell())

	case tea.MouseWheelMsg:
		a.handleWheel(tea.Mouse(msg).Button)
		return a, nil

	// Presses only. v2 can also report releases and repeats, but only if a frame asks for
	// them, and nothing here wants a key twice.
	case tea.KeyPressMsg:
		if a.handleKey(fromTea(msg)) {
			a.shutdown()
			return a, tea.Quit
		}
		a.PollRun()
		a.PollWatch()
		a.refreshProfile()
		a.noteFinished()
		return a, tea.Batch(a.launchEditor(), a.ringBell())
	}
	return a, nil
}

// ringBell writes a BEL, if one is owed.
//
// From a command rather than from the model: Update is the only place allowed to touch the
// terminal, and a stray byte written mid-frame lands in the middle of whatever was being
// drawn. BEL is safe to send inside the alternate screen — it moves no cursor and occupies
// no cell — so what the terminal does with it, flash or beep or nothing, stays the
// terminal's business.
func (a *App) ringBell() tea.Cmd {
	if !a.TakeBell() {
		return nil
	}
	return func() tea.Msg {
		fmt.Fprint(os.Stdout, "\a")
		return nil
	}
}

// launchEditor runs whatever `e` asked for, if anything.
//
// A terminal editor gets the terminal: Bubble Tea puts it back the way it found it, runs
// the program attached to the real stdin and stdout, and redraws afterwards. Anything that
// opens its own window is run alongside instead — handing the terminal to a `code --goto`
// that returns in ten milliseconds blacks the UI out for no reason, and on a slow start it
// looks like a crash.
func (a *App) launchEditor() tea.Cmd {
	editor, ok := a.TakeEdit()
	if !ok {
		return nil
	}
	//nolint:gosec // this is $EDITOR being run on purpose; the argv is built from the
	// variable the user set and a path that had to exist on disk to get here.
	cmd := exec.Command(editor.Name, editor.Args...)
	if !editor.Terminal {
		return func() tea.Msg {
			// Detached: the window it opens outlives the keystroke, and its exit status is
			// not something taskui has an opinion about.
			if err := cmd.Start(); err != nil {
				return editorFailed{err}
			}
			go func() { _ = cmd.Wait() }()
			return nil
		}
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return editorFailed{err}
		}
		return nil
	})
}

// editorFailed reports an editor that would not start — a misspelled $EDITOR is otherwise a
// key that appears to do nothing.
type editorFailed struct{ err error }

// shutdownGrace is how long to wait for the slots to report themselves gone before
// insisting.
//
// Deliberately longer than the grace a stopped run gives its own process group, so the
// normal case completes inside it and quitting does not routinely escalate.
const shutdownGrace = 2 * time.Second

// killGrace is how long to wait after insisting. SIGKILL is not instant — the exit still
// has to travel back up the queue — but it is not slow either.
const killGrace = 500 * time.Millisecond

// shutdown leaves, taking every running child with us.
//
// Every slot, not just the one on screen: the whole point of parking a run is that you
// stop looking at it, and a background container left behind on quit is exactly the orphan
// this tool exists to not create.
//
// We wait here rather than leaving. A stopped run only reports itself finished once its
// capture goroutine has taken the process group with it, which means waiting for that
// report is the difference between a stack that is down and a stack that is merely no
// longer being watched. Anything still going when the grace runs out gets SIGKILL, which
// is also what shortens its capture goroutine's own wait.
func (a *App) shutdown() {
	if !a.AnyInFlight() {
		return
	}
	a.CancelAll()
	a.drainUntilGone(shutdownGrace)
	if a.AnyInFlight() {
		a.KillAll()
		a.drainUntilGone(killGrace)
	}
}

func (a *App) drainUntilGone(grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && a.AnyInFlight() {
		a.PollRun()
		time.Sleep(25 * time.Millisecond)
	}
}

// --- keys -------------------------------------------------------------------------

type keyKind int

const (
	keyChar keyKind = iota
	keyEnter
	keyEsc
	keyTab
	keyBackTab
	keyBackspace
	keyDelete
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyOther
)

// Key is the shape the handlers below want: Bubble Tea's key messages carry more
// detail than the dispatch table needs, and flattening them here keeps every handler a
// straight port of the original.
type Key struct {
	kind keyKind
	ch   rune
	mods keys.Mods
}

// fromTea flattens one of Bubble Tea's key events.
//
// The special keys come first because several of them are also control codes — ⇥ is 0x09
// and ⏎ is 0x0d — so asking "is this printable" before asking "is this ⇥" would answer
// wrong. Space is deliberately not among them: it has always arrived here as a character,
// and the handlers match it as one.
func fromTea(msg tea.KeyPressMsg) Key {
	k := tea.Key(msg)
	// Caps lock and num lock are states rather than things you hold, and the terminal has
	// already applied them to the character. Carrying them would make a binding stop working
	// with caps lock on.
	mod := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock)

	special := func(kind keyKind) Key { return Key{kind: kind, mods: modsOf(mod)} }
	switch k.Code {
	case tea.KeyEnter:
		return special(keyEnter)
	case tea.KeyEscape:
		return special(keyEsc)
	case tea.KeyTab:
		// v2 has no separate shift+⇥ code: it reports ⇥ with shift held, which is the same
		// event either way.
		if mod.Contains(tea.ModShift) {
			return Key{kind: keyBackTab, mods: modsOf(mod &^ tea.ModShift)}
		}
		return special(keyTab)
	case tea.KeyBackspace:
		return special(keyBackspace)
	case tea.KeyDelete:
		return special(keyDelete)
	case tea.KeyUp:
		return special(keyUp)
	case tea.KeyDown:
		return special(keyDown)
	case tea.KeyLeft:
		return special(keyLeft)
	case tea.KeyRight:
		return special(keyRight)
	case tea.KeyHome:
		return special(keyHome)
	case tea.KeyEnd:
		return special(keyEnd)
	case tea.KeyPgUp:
		return special(keyPageUp)
	case tea.KeyPgDown:
		return special(keyPageDown)
	}

	// Anything with text is a character, and so is anything printable — a control code
	// arrives as the letter that made it, with ctrl set, and that letter is printable.
	if k.Text == "" && !unicode.IsPrint(k.Code) {
		return Key{kind: keyOther}
	}
	ch := k.Code
	if text := []rune(k.Text); len(text) > 0 {
		// The terminal applies shift itself whenever it changes the character, so `G` arrives
		// as `G`. Keeping the shift as well would make `G` and `⇧g` two different bindings for
		// one keystroke — so it is dropped, and survives only on keys shift cannot change.
		if text[0] != k.Code {
			mod &^= tea.ModShift
		}
		ch = text[0]
	}
	return Key{kind: keyChar, ch: ch, mods: modsOf(mod)}
}

// modsOf keeps the three modifiers that can be bound and discards the rest. Meta, hyper and
// super are not on every keyboard and not in the config's vocabulary.
func modsOf(mod tea.KeyMod) keys.Mods {
	var out keys.Mods
	if mod.Contains(tea.ModCtrl) {
		out |= keys.ModCtrl
	}
	if mod.Contains(tea.ModAlt) {
		out |= keys.ModAlt
	}
	if mod.Contains(tea.ModShift) {
		out |= keys.ModShift
	}
	return out
}

// Char builds a plain character keypress, for tests and for `--keys`.
func Char(c rune) Key { return Key{kind: keyChar, ch: c} }

// Enter is the ⏎ key. It, Esc and Tab are the non-character keys `--keys` can feed.
func Enter() Key { return Key{kind: keyEnter} }

// Esc is the escape key.
func Esc() Key { return Key{kind: keyEsc} }

// Ctrl builds a control chord, for tests and for `--keys`.
func Ctrl(c rune) Key { return Key{kind: keyChar, ch: c, mods: keys.ModCtrl} }

// Tab is the ⇥ key.
func Tab() Key { return Key{kind: keyTab} }

// isChar matches one unmodified character. Modified or not is the whole distinction a
// binding like `shift+space` rests on: if this let ⇧␣ through as ␣, the literal `case`
// above the keymap would swallow it before the binding was ever consulted.
func (k Key) isChar(c rune) bool { return k.kind == keyChar && k.ch == c && k.mods == 0 }

func (k Key) isCtrl(c rune) bool {
	return k.kind == keyChar && k.ch == c && k.mods == keys.ModCtrl
}

// typed is a character meant as text: the prompts take shift, because it is already in the
// character, but not ctrl or alt, which are how you get out of one.
func (k Key) typed() bool {
	return k.kind == keyChar && k.mods&(keys.ModCtrl|keys.ModAlt) == 0
}

// action says which action, if any, this key means on screen.
//
// Dispatch goes through actions rather than literal characters, which is what makes the
// `keys:` block in `config.yaml` work: the map is consulted here, and the handlers below
// only ever see actions.
func (a *App) action(k Key, screen Screen) keys.Action {
	if k.kind != keyChar {
		return keys.None
	}
	c := keys.Chord{Key: k.ch, Mods: k.mods}
	switch screen {
	case ScreenPicker:
		return a.Keymap.Picker(c)
	case ScreenRun:
		return a.Keymap.Run(c)
	case ScreenHistory:
		return a.Keymap.History(c)
	case ScreenTimeline:
		return a.Keymap.Timeline(c)
	case ScreenDiff:
		return a.Keymap.Diff(c)
	case ScreenProfile:
		return a.Keymap.Profile(c)
	case ScreenDetail:
		return a.Keymap.Detail(c)
	case ScreenHelp:
		return a.Keymap.Help(c)
	default:
		return keys.None
	}
}

// quit asks before leaving, if leaving would stop something.
//
// Quitting takes down every slot, and with several open most of them are not on screen —
// so the one keystroke that reaches runs you cannot see is the one that should not be able
// to happen by accident.
//
// It asks even with nothing running. Skipping the prompt when the count happened to be
// zero made `q` mean two different things depending on state you were not looking at: in
// the run view, one key away from `y`, it dropped you out of the tool mid-read the moment
// the last task finished. A key that sometimes exits instantly is a key you learn not to
// press.
func (a *App) quit() bool {
	a.Confirm = &Confirm{Kind: ConfirmQuit, Live: a.InFlightCount(), Detached: a.DetachedCount()}
	return false
}

// HandleKey is the test-visible entry point. It returns true when the app should exit.
func (a *App) HandleKey(k Key) bool { return a.handleKey(k) }

func (a *App) handleKey(k Key) bool {
	// Anything that is not `esc` breaks the streak, so the hint answers a real run of
	// presses rather than two of them ten minutes apart.
	if k.kind != keyEsc {
		a.EscStreak = 0
	}
	if a.Confirm != nil {
		return a.handleConfirmKey(k)
	}
	// Before the per-screen handlers, not after: the run screen returns early, and with
	// this below it `gg` and `G` reached every screen except the one with the most rows to
	// move through. The prompt guards inside it keep a run's own `i` and `/` intact.
	if a.handleNavKey(k) {
		return false
	}
	if taken, quitting := a.handleCommonKey(k); taken {
		return quitting
	}
	switch a.Screen {
	case ScreenRun:
		return a.handleRunKey(k)
	case ScreenHistory:
		return a.handleHistoryKey(k)
	case ScreenDetail:
		return a.handleDetailKey(k)
	case ScreenHelp:
		return a.handleHelpKey(k)
	case ScreenTimeline:
		return a.handleTimelineKey(k)
	case ScreenDiff:
		return a.handleDiffKey(k)
	case ScreenProfile:
		return a.handleProfileKey(k)
	case ScreenPicker:
		// Handled below, once the prompts have had their turn.
	}

	if a.EnteringArgs {
		a.handleArgsKey(k)
		return false
	}
	if a.Jumping {
		a.handleJumpKey(k)
		return false
	}
	if a.Filtering && a.handleFilterKey(k) {
		return false
	}

	return a.handlePickerKey(k)
}

// handleConfirmKey: something is waiting on a yes; nothing else gets through until it is
// answered.
// wheelStep is how many rows a notch of the wheel moves.
//
// One, not the three that terminals and browsers use for scrolling a page. This is not a
// page: the wheel moves a *selection* through a list of tasks, and a selection that jumps
// three rows a notch overshoots what you were reaching for and has to be walked back. Three
// is right when the thing under the wheel is text you are reading past; one is right when it
// is a cursor you are aiming.
const wheelStep = 1

// handleWheel turns a notch of the wheel into the movement the arrow keys already do.
//
// Not a set of per-screen scroll handlers: every screen in this program already answers up
// and down — the picker, the run view, the history list, the timeline, the diff, the
// profile, and the jump and search prompts each in their own way — and a wheel that meant
// anything else on any one of them would be a second navigation model to keep in step with
// the first. Scrolling is therefore *defined* as arrowing, and everything that hangs off
// arrowing comes with it: following stops when you scroll away from what is running,
// because that is already what `k` does.
func (a *App) handleWheel(button tea.MouseButton) {
	// A confirmation reads every key that is not `y` as "no", and a wheel is not an answer
	// to a question. Scrolling past one leaves it standing.
	if a.Confirm != nil {
		return
	}

	var k Key
	switch button {
	case tea.MouseWheelUp:
		k = Key{kind: keyUp}
	case tea.MouseWheelDown:
		k = Key{kind: keyDown}
	default:
		// Horizontal wheels and tilting ones exist. Nothing here scrolls sideways.
		return
	}
	for range wheelStep {
		// The return value is "the app is quitting", which no movement key ever is.
		a.handleKey(k)
	}
}

func (a *App) handleConfirmKey(k Key) bool {
	// Only ConfirmYes knows what was being asked, and only the quit answer ends the loop —
	// so the teardown hangs off its return value rather than off the key.
	if k.isChar('y') || k.isChar('Y') {
		return a.ConfirmYes()
	}
	a.ConfirmNo()
	return false
}

// promptOpen is true while something on screen is taking typed characters — a filter, a
// search, an argument line, or the running child's own stdin.
func (a *App) promptOpen() bool {
	return a.EnteringArgs || a.Searching || a.SendingInput || a.HistorySearching ||
		a.Jumping || a.Filtering || a.HelpFinding
}

// promptOwns says whether the prompt on screen answers this particular motion key itself.
//
// Asked key by key rather than as a blanket "something is open", because these prompts use
// different parts of the keyboard and a motion the open one has no use for should still
// move the list behind it: `^d` pages the run while you are searching it, because the
// search line has nothing to do with `^d` and the output is right there.
//
// The filter and the help's find own no motions at all, which is what makes narrowing and
// then picking one gesture rather than two.
func (a *App) promptOwns(k Key) bool {
	switch {
	// Every key is the child's while you are typing at it — `^d` most of all, since that
	// is the one that closes its stdin.
	case a.SendingInput:
		return true
	// A line editor: this is where the caret goes.
	case a.EnteringArgs:
		return k.kind == keyLeft || k.kind == keyRight || k.kind == keyHome || k.kind == keyEnd
	// ↑ and ↓ step through what the query matched, which is the whole point of typing it.
	case a.Searching, a.HistorySearching, a.Jumping:
		return k.kind == keyUp || k.kind == keyDown
	}
	return false
}

// promptTakes says whether an open prompt should get this key rather than the screen
// behind it. Both of the handlers that run ahead of the per-screen ones ask this, so a
// prompt cannot be answered by one of them and ignored by the other.
func (a *App) promptTakes(k Key) bool {
	if k.typed() {
		return a.promptOpen()
	}
	return a.promptOwns(k)
}

// handleCommonKey answers the two keys that mean the same thing on every screen.
//
// `esc` is deliberately not one of them: it closes a filter here and a panel there and a
// whole run somewhere else, and that difference is the point of it. Quitting and the `?`
// screen have no such difference, and eight copies of them is how the detail panel came to
// advertise `? keys` in its footer and then not answer it.
//
// Returns whether it took the key, and — because leaving has to travel back out to Bubble
// Tea — whether the app is going.
func (a *App) handleCommonKey(k Key) (bool, bool) {
	if a.promptTakes(k) {
		return false, false
	}
	switch {
	case k.isCtrl('c'), a.action(k, a.Screen) == keys.Quit:
		return true, a.quit()
	case a.action(k, a.Screen) == keys.Help:
		a.ToggleHelp()
		return true, false
	}
	return false, false
}

// handleNavKey moves the cursor, on whatever screen is showing.
//
// Every screen here is a list and every list moves the same way, so the keys that move one
// are written once rather than eight times over — which is how the run view came to have no
// Home or End, and the detail panel and the `?` screen no `^d`. MoveBy, GotoTop and
// GotoBottom do the per-screen half; this is only the keys.
//
// It runs before the per-screen handlers, so a motion wins over an action rebound onto the
// same key. That is the bargain `gg` and `G` have always had, now extended to the rest: the
// motions are the one part of the keymap you can rely on without reading it.
//
// Returns true if the key was consumed.
func (a *App) handleNavKey(k Key) bool {
	if a.promptTakes(k) {
		return false
	}

	// `g` on its own only arms the pair. Everything below disarms it, and so does every key
	// that falls through, so a forgotten `g` cannot silently swallow the next keystroke.
	if k.isChar('g') {
		if a.PendingG {
			a.PendingG = false
			a.GotoTop()
		} else {
			a.PendingG = true
		}
		return true
	}
	a.PendingG = false

	switch {
	case k.isChar('j'), k.kind == keyDown:
		a.MoveBy(1)
	case k.isChar('k'), k.kind == keyUp:
		a.MoveBy(-1)
	case k.isCtrl('d'):
		a.MoveBy(a.HalfPage())
	case k.isCtrl('u'):
		a.MoveBy(-a.HalfPage())
	case k.isCtrl('f'), k.kind == keyPageDown:
		a.MoveBy(a.Page())
	case k.isCtrl('b'), k.kind == keyPageUp:
		a.MoveBy(-a.Page())
	case k.isChar('G'), k.kind == keyEnd:
		a.GotoBottom()
	case k.kind == keyHome:
		a.GotoTop()
	default:
		return false
	}
	return true
}

func (a *App) handleDetailKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenDetail) }

	switch {
	case k.kind == keyEsc, act() == keys.Detail:
		a.CloseDetail()
	// Running it is the point of having read this.
	case k.kind == keyEnter:
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.RequestRun(name, nil)
		}
	case act() == keys.Args:
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.BeginArgs(name)
		}
	// Reading what a task will run is the moment you most want to change it.
	case act() == keys.Edit:
		a.EditDefinition(a.DetailOf)
	}
	return false
}

func (a *App) handleHelpKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenHelp) }

	// The find prompt owns every key that is not a way out of it or a way to scroll what it
	// left — `q` and `?` are bindings out there and letters in here, and typing `quit` to
	// look up how to quit must not quit.
	if a.HelpFinding {
		switch {
		case k.kind == keyEsc:
			a.ClearHelpFind()
			return false
		case k.kind == keyEnter:
			// Keep what it narrowed to and give the scroll keys back, as the picker's
			// filter does: you search to find the line, then you read it.
			a.HelpFinding = false
			return false
		case k.kind == keyBackspace:
			a.PopHelpFind()
			return false
		case k.typed():
			a.PushHelpFind(k.ch)
			return false
		}
	}

	switch {
	// `esc` drops the query first and closes the screen second, so backing out of a search
	// does not also throw away the keymap you were reading.
	case k.kind == keyEsc && a.HelpQuery != "":
		a.ClearHelpFind()
	case k.kind == keyEsc:
		a.ToggleHelp()

	// Find a binding in the keymap itself. The same key that finds a task in the picker,
	// because "show me the one I mean" should not change name with the screen — and
	// rebinding `jump` moves both, because Rebind reaches every screen that offers it.
	case act() == keys.Jump:
		a.BeginHelpFind()
	}
	return false
}

func (a *App) handleArgsKey(k Key) {
	// ⇥ walks the completion; everything else ends the cycle it was walking, so the list
	// can never outlive the word it was built for.
	if k.kind == keyTab || k.kind == keyBackTab {
		delta := 1
		if k.kind == keyBackTab {
			delta = -1
		}
		a.CompleteArgs(delta)
		return
	}
	a.argsComp = nil

	switch {
	case k.kind == keyEsc:
		a.CancelArgs()
	case k.kind == keyEnter:
		a.ConfirmArgs()
	case k.kind == keyBackspace:
		a.ArgsBackspace()
	case k.kind == keyDelete:
		a.ArgsDelete()
	case k.kind == keyLeft:
		a.ArgsMove(-1)
	case k.kind == keyRight:
		a.ArgsMove(1)
	case k.kind == keyHome:
		a.ArgsHome()
	case k.kind == keyEnd:
		a.ArgsEnd()
	case k.typed():
		a.ArgsInsert(k.ch)
	}
}

func (a *App) handleJumpKey(k Key) {
	switch {
	case k.kind == keyEsc:
		a.CancelJump()
	case k.kind == keyEnter:
		a.AcceptJump()
	case k.kind == keyBackspace:
		a.PopJump()
	case k.kind == keyDown, k.kind == keyTab:
		a.JumpStep(1)
	case k.kind == keyUp, k.kind == keyBackTab:
		a.JumpStep(-1)
	case k.typed():
		a.PushJump(k.ch)
	}
}

// handleFilterKey returns true if the key was consumed by filter mode.
func (a *App) handleFilterKey(k Key) bool {
	switch {
	case k.kind == keyEsc:
		a.ClearQuery()
		return true
	case k.kind == keyEnter:
		// Keep the filter applied, leave the input — you filter to narrow the tree, then
		// navigate what is left.
		a.Filtering = false
		return true
	case k.kind == keyBackspace:
		a.PopQuery()
		return true
	case k.kind == keyDown, k.kind == keyUp:
		return false
	case k.typed():
		a.PushQuery(k.ch)
		return true
	default:
		return false
	}
}

// hide the order the arms are tried in, which is what makes a rebound key shadow a literal.
//
//nolint:cyclop // one flat dispatch table per screen is the point; splitting it would
func (a *App) handlePickerKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenPicker) }

	switch {
	// `esc` backs out of things — a filter, a jump, a panel, a run — and both of those are
	// handled before this point. Landing here means there was nothing left to back out of,
	// and the answer to that is not "close the tool". The second press in a row says where
	// the exit is, because a key that does nothing at all reads as broken.
	case k.kind == keyEsc:
		a.EscStreak++
		if a.EscStreak > 1 {
			a.Status = "nothing left to leave — press q to quit"
		}

	// Vim's paragraph keys, over the tree's groups: `}` past whatever is under this group
	// to the next header, `{` back to the previous one.
	case k.isChar('}'):
		a.MoveGroup(1)
	case k.isChar('{'):
		a.MoveGroup(-1)

	// The pivot. Selection, filter and the other mode's folds all survive it. On `p`, not
	// `g`: `gg` belongs to vim.
	case act() == keys.Pivot:
		a.ToggleMode()

	// The other half of the same question: `p` is what the tree is, `⇧S` is what sits above
	// what inside it.
	case act() == keys.Order:
		a.CycleOrder()

	// Space folds, enter runs — kept strictly separate. A node that is both a group and a
	// task (`backend:migrate`) is then runnable from its own header, so its subtree never
	// has to relist it just to make it reachable.
	//
	// With a run unfolded under a task there are two things a fold key could mean, so
	// there are two keys: `space` is the tree and `o` is the output. Each falls through to
	// the other where it has nothing of its own to fold, which is most rows — a leaf task
	// has no group to open, and a namespace with nothing running has no output.
	case k.isChar(' '):
		if n := a.SelectedNode(); n != nil && n.IsGroup() && !a.CursorInRun() {
			a.ToggleFold()
		} else {
			a.CycleOutputFold()
		}
	case k.kind == keyEnter:
		// Marks first: having chosen a set, `⏎` means run the set. Running whatever the
		// cursor happens to be on instead would quietly discard the choice.
		if len(a.marked) > 0 {
			a.RunMarked()
		} else if ti := a.SelectedTask(); ti >= 0 {
			a.RequestRun(a.Tasks[ti].Name, nil)
		} else {
			what := ""
			if n := a.SelectedNode(); n != nil {
				what = n.Label
			}
			a.Status = "`" + what + "` groups tasks but is not one — space folds it"
		}
	case k.kind == keyRight, k.kind == keyLeft:
		if n := a.SelectedNode(); n != nil && n.IsGroup() {
			a.ToggleFold()
		}

	case k.kind == keyTab, act() == keys.FoldAll:
		a.ToggleFoldAll()
	case act() == keys.Fold:
		if !a.CycleOutputFold() {
			if n := a.SelectedNode(); n != nil && n.IsGroup() {
				a.ToggleFold()
			}
		}

	case act() == keys.Filter:
		a.Filtering = true
		a.Status = ""

	// Jump rather than filter: the list stays whole and only the cursor moves.
	case act() == keys.Jump:
		a.BeginJump()

	// What is this task, and what will it actually run?
	case act() == keys.Detail:
		a.OpenDetail()

	// Run with arguments. Half a real Taskfile needs them.
	case act() == keys.Args:
		if ti := a.SelectedTask(); ti >= 0 {
			a.BeginArgs(a.Tasks[ti].Name)
		} else {
			a.Status = "nothing to run here — space folds it"
		}

	// Let the next run ask questions.
	case act() == keys.Interactive:
		a.ToggleInteractive()

	// Ignore go-task's up-to-date checks on the next run.
	case act() == keys.Force:
		a.ToggleForce()

	// Re-run whenever the source changes — the marked set if there is one, which is the
	// half of this that only the picker can offer, because marks are made here.
	case act() == keys.Watch:
		a.ToggleWatch()

	// Back to whatever is still running.
	case act() == keys.ResumeRun:
		a.ResumeRun()

	// Past runs.
	case act() == keys.History:
		a.OpenHistory()

	// How this one task has been going — the other half of `h`, scoped to what is under
	// the cursor rather than to the project.
	case act() == keys.Timeline:
		a.OpenTimeline(a.TimelineTaskFor())

	// In a run `e` opens the file an error named; here there is no error, so it opens the
	// thing you are actually looking at — the task's own definition.
	case act() == keys.Edit:
		a.EditDefinition(a.TimelineTaskFor())

	// Stop the run belonging to the task under the cursor. Addressing it by name is what
	// makes this reach the slots that are not on screen — which, from here, is all of them.
	case act() == keys.Stop:
		if ti := a.SelectedTask(); ti >= 0 {
			a.CancelTask(a.Tasks[ti].Name)
		} else {
			a.Status = "nothing to stop here — space folds it"
		}

	case act() == keys.StopAll:
		a.RequestStopAll()

	// Choose a set, then start it. Slots already hold several runs; this is how you fill
	// them without three trips back through the list.
	case act() == keys.Mark:
		a.ToggleMark()
	case act() == keys.ClearMarks:
		a.ClearMarks()
	}
	return false
}

func (a *App) handleHistoryKey(k Key) bool {
	if a.HistorySearching {
		switch {
		case k.kind == keyEsc:
			a.ClearHistorySearch()
		case k.kind == keyEnter:
			// Keep the query; it carries into the run you open.
			a.HistorySearching = false
		case k.kind == keyBackspace:
			a.PopHistorySearch()
		case k.kind == keyDown:
			a.HistoryMoveCursor(1)
		case k.kind == keyUp:
			a.HistoryMoveCursor(-1)
		case k.typed():
			a.PushHistorySearch(k.ch)
		}
		return false
	}

	act := func() keys.Action { return a.action(k, ScreenHistory) }

	switch {
	case k.kind == keyEsc:
		a.Screen = ScreenPicker
		a.Status = ""

	// Widen to every project, or narrow back to this one.
	case act() == keys.AllProjects:
		a.ToggleHistoryScope()

	case act() == keys.Search:
		a.BeginHistorySearch()

	case k.kind == keyEnter:
		a.OpenStoredRun()
	}
	return false
}

//nolint:cyclop // as handlePickerKey: one table, read top to bottom.
func (a *App) handleRunKey(k Key) bool {
	// Input mode: everything except the escape hatch goes to the child.
	if a.SendingInput {
		switch {
		case k.kind == keyEsc:
			a.EndInput()
		// A pty expects carriage return, not newline.
		case k.kind == keyEnter:
			a.SendInput([]byte("\r"))
		case k.kind == keyBackspace:
			a.SendInput([]byte{0x7f})
		case k.kind == keyTab:
			a.SendInput([]byte("\t"))
		case k.kind == keyUp:
			a.SendInput([]byte("\x1b[A"))
		case k.kind == keyDown:
			a.SendInput([]byte("\x1b[B"))
		case k.kind == keyRight:
			a.SendInput([]byte("\x1b[C"))
		case k.kind == keyLeft:
			a.SendInput([]byte("\x1b[D"))
		case k.isCtrl('c'):
			a.SendInput([]byte{0x03})
		case k.isCtrl('d'):
			a.SendInput([]byte{0x04})
		// What was typed, not what was held: a modified key used to arrive here
		// indistinguishable from its bare letter, so ⌃z sent the child a `z`.
		case k.typed():
			a.SendInput([]byte(string(k.ch)))
		}
		return false
	}

	if a.EnteringArgs {
		a.handleArgsKey(k)
		return false
	}

	if a.Searching {
		switch {
		case k.kind == keyEsc:
			a.ClearSearch()
			return false
		// Keep the query and its highlights; leave the input line.
		case k.kind == keyEnter:
			a.Searching = false
			return false
		case k.kind == keyBackspace:
			a.PopSearch()
			return false
		case k.kind == keyDown:
			a.SearchStep(1)
			return false
		case k.kind == keyUp:
			a.SearchStep(-1)
			return false
		case k.typed():
			a.PushSearch(k.ch)
			return false
		}
	}

	act := func() keys.Action { return a.action(k, ScreenRun) }

	switch {
	// Stop the run without leaving the view.
	case act() == keys.Stop:
		a.CancelRun()
	case act() == keys.StopAll:
		a.RequestStopAll()

	// Answer whatever the task is asking.
	//
	// This works even in a non-interactive run: go-task wraps stdout and stderr for
	// prefixing but leaves stdin alone, so keystrokes reach the child regardless —
	// verified against a real `task` process. You may not be able to see the question, but
	// typing `y⏎` still answers it, which beats re-running a deploy from the start just to
	// be able to type.
	case act() == keys.Input:
		a.BeginInput()

	// Re-run whenever the source changes.
	case act() == keys.Watch:
		a.ToggleWatch()

	// Re-run this task interactively, when seeing the prompt matters more than not
	// starting over.
	case act() == keys.InteractiveRerun:
		if a.Run != nil {
			name, args := a.Run.Root, a.Run.Args
			a.InteractiveNext = true
			a.RequestRun(name, args)
		}

	// Back to the picker. The run keeps going in the background and is still there when
	// you come back.
	case k.kind == keyEsc:
		a.Screen = ScreenPicker
		a.Status = ""

	// Reading an error usually ends with pasting it somewhere.
	case act() == keys.Yank:
		a.YankLine()
	case act() == keys.YankAll:
		a.YankTaskOutput()

	// …or with going there. The line under the cursor names a file and a line; this is the
	// step the tool used to leave you to do by hand.
	case act() == keys.Edit:
		a.EditUnderCursor()

	// What changed since this task last worked.
	case act() == keys.Diff:
		a.DiffAgainstLastGreen()

	// How it has been going, run after run.
	case act() == keys.Timeline:
		a.OpenTimeline(a.TimelineTaskFor())

	// Where the whole run's time went. Every task already shows its own clock in tree
	// order, which answers "is this step slow" and not "what makes this take four minutes".
	case act() == keys.Profile:
		a.OpenProfile()

	case k.isChar(' '), k.kind == keyRight, k.kind == keyLeft:
		a.RunToggleFold()
	case act() == keys.Fold:
		a.RunToggleFold()
	case act() == keys.FoldAll:
		a.RunToggleFoldAll()

	// Switch between open runs. Handled here rather than through the keymap because that
	// table is keyed by character and these are not characters.
	case k.kind == keyTab:
		a.CycleSlot(1)
	case k.kind == keyBackTab:
		a.CycleSlot(-1)
	case act() == keys.CloseSlot:
		a.CloseSlot()

	// Let it go. Quitting stops being responsible for it; `x` still is not.
	case act() == keys.Detach:
		a.Detach()

	// Search the output. `/` in the picker filters task names; here it searches what those
	// tasks printed. Different corpora, deliberately different jobs.
	case act() == keys.Search:
		a.Searching = true
		a.Status = ""
	case act() == keys.NextMatch:
		a.SearchStep(1)
	case act() == keys.PrevMatch:
		a.SearchStep(-1)

	// Collapse the run to just the matching lines, kept under their tasks.
	case act() == keys.FilterMatches:
		a.ToggleFilterMatches()

	// More or less context around each hit.
	case act() == keys.ContextMore:
		a.SetFilterContext(1)
	case act() == keys.ContextLess:
		a.SetFilterContext(-1)

	// Resume tracking whatever is running after you have gone looking around.
	case act() == keys.Follow:
		a.Following = !a.Following

	case act() == keys.History:
		a.OpenHistory()

	// Re-run the task under the cursor — the tight loop when you are fixing one broken
	// step. Note this is a fresh `task <name>`, not a resume of the parent.
	case act() == keys.Rerun:
		a.RerunSelected()

	// The same, minus go-task's up-to-date checks — the second thing you want when `r`
	// came back green without having run anything.
	case act() == keys.ForceRerun:
		a.ForceRerunSelected()

	// Everything that broke, at once. After a red `task all` this is the whole loop.
	case act() == keys.RerunFailed:
		a.RerunFailed()

	// Re-run with different arguments.
	case act() == keys.Args:
		if name, ok := a.RunSelectedTask(); ok {
			a.BeginArgs(name)
		}

	// Jump straight to a slot, as the bar numbers them. Last, so that rebinding an action
	// onto a digit still wins — the keymap is the thing users can change.
	case k.typed() && k.ch >= '1' && k.ch <= '9':
		a.FocusSlotNumber(int(k.ch - '0'))
	}
	return false
}

func (a *App) handleTimelineKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenTimeline) }

	switch {
	case k.kind == keyEsc:
		a.CloseTimeline()

	// What changed between this run and the one before it — the question the list is
	// arranged to make you ask.
	case act() == keys.Diff:
		a.DiffTimelinePoint()

	case k.kind == keyEnter:
		a.OpenTimelineRun()
	}
	return false
}

func (a *App) handleProfileKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenProfile) }

	switch {
	case k.kind == keyEsc:
		a.CloseProfile()

	// The point of finding the slow step is going to look at it.
	case k.kind == keyEnter:
		a.GotoProfiledTask()

	case act() == keys.Edit:
		if cost, ok := a.SelectedCost(); ok {
			a.EditDefinition(cost.Name)
		}
	}
	return false
}

func (a *App) handleDiffKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenDiff) }

	switch {
	case k.kind == keyEsc:
		a.CloseDiff()

	// More or less of the unchanged output around each change.
	case act() == keys.ContextMore:
		a.SetDiffContext(1)
	case act() == keys.ContextLess:
		a.SetDiffContext(-1)

	// A line that just appeared often names the file it appeared about.
	case act() == keys.Edit:
		a.EditUnderCursor()
	}
	return false
}

// KeyFor maps one character of a `--keys` string to a keypress.
//
// The control characters are the keys that make them: ⇥ is 0x09, ⏎ is 0x0a, esc is 0x1b.
// That is how a screenshot reaches the three keys no letter can.
func KeyFor(c rune) Key {
	switch c {
	case '\t':
		return Tab()
	case '\n':
		return Enter()
	case 0x1b:
		return Esc()
	default:
		return Char(c)
	}
}

// KeysFrom turns a whole `--keys` string into the presses it names.
//
// One rune is one press, except `^` and the character after it, which is a control chord:
// `^d` is ⌃d. Written that way because the alternative is a recipe carrying a literal 0x04,
// which nobody can read in a Taskfile or edit without a hex editor — and the motion keys
// this reaches are exactly the ones a screenshot could not demonstrate before. `^^` is a
// literal caret, which nothing is bound to but which the escape would otherwise swallow.
func KeysFrom(feed string) []Key {
	runes := []rune(feed)
	out := make([]Key, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		if runes[i] != '^' || i+1 >= len(runes) {
			out = append(out, KeyFor(runes[i]))
			continue
		}
		i++
		if runes[i] == '^' {
			out = append(out, Char('^'))
			continue
		}
		out = append(out, Ctrl(unicode.ToLower(runes[i])))
	}
	return out
}
