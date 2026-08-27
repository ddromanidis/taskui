package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

func (a *App) tick() tea.Cmd {
	d := idleTick
	if a.AnyInFlight() {
		d = liveTick
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) Init() tea.Cmd { return a.tick() }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.Width, a.Height = msg.Width, msg.Height
		return a, nil

	case tickMsg:
		a.PollRun()
		a.PollWatch()
		return a, a.tick()

	case tea.KeyMsg:
		if a.handleKey(fromTea(msg)) {
			a.shutdown()
			return a, tea.Quit
		}
		a.PollRun()
		a.PollWatch()
		return a, nil
	}
	return a, nil
}

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
	ctrl bool
}

func fromTea(msg tea.KeyMsg) Key {
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return Key{kind: keyChar, ch: msg.Runes[0]}
		}
		return Key{kind: keyOther}
	case tea.KeySpace:
		return Key{kind: keyChar, ch: ' '}
	case tea.KeyEnter:
		return Key{kind: keyEnter}
	case tea.KeyEsc:
		return Key{kind: keyEsc}
	case tea.KeyTab:
		return Key{kind: keyTab}
	case tea.KeyShiftTab:
		return Key{kind: keyBackTab}
	case tea.KeyBackspace:
		return Key{kind: keyBackspace}
	case tea.KeyDelete:
		return Key{kind: keyDelete}
	case tea.KeyUp:
		return Key{kind: keyUp}
	case tea.KeyDown:
		return Key{kind: keyDown}
	case tea.KeyLeft:
		return Key{kind: keyLeft}
	case tea.KeyRight:
		return Key{kind: keyRight}
	case tea.KeyHome:
		return Key{kind: keyHome}
	case tea.KeyEnd:
		return Key{kind: keyEnd}
	case tea.KeyPgUp:
		return Key{kind: keyPageUp}
	case tea.KeyPgDown:
		return Key{kind: keyPageDown}
	case tea.KeyCtrlC:
		return Key{kind: keyChar, ch: 'c', ctrl: true}
	case tea.KeyCtrlD:
		return Key{kind: keyChar, ch: 'd', ctrl: true}
	case tea.KeyCtrlU:
		return Key{kind: keyChar, ch: 'u', ctrl: true}
	default:
		return Key{kind: keyOther}
	}
}

// Char builds a plain character keypress, for tests and for `--keys`.
func Char(c rune) Key { return Key{kind: keyChar, ch: c} }

// Enter is the ⏎ key. It, Esc and Tab are the non-character keys `--keys` can feed.
func Enter() Key { return Key{kind: keyEnter} }

// Esc is the escape key.
func Esc() Key { return Key{kind: keyEsc} }

// Tab is the ⇥ key.
func Tab() Key { return Key{kind: keyTab} }

func (k Key) isChar(c rune) bool { return k.kind == keyChar && k.ch == c && !k.ctrl }

func (k Key) isCtrl(c rune) bool { return k.kind == keyChar && k.ch == c && k.ctrl }

// action says which action, if any, this key means on screen.
//
// Dispatch goes through actions rather than literal characters, which is what makes the
// `keys:` block in `config.yaml` work: the map is consulted here, and the handlers below
// only ever see actions.
func (a *App) action(k Key, screen Screen) keys.Action {
	if k.kind != keyChar || k.ctrl {
		return keys.None
	}
	switch screen {
	case ScreenPicker:
		return a.Keymap.Picker(k.ch)
	case ScreenRun:
		return a.Keymap.Run(k.ch)
	case ScreenHistory:
		return a.Keymap.History(k.ch)
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
	a.Confirm = &Confirm{Kind: ConfirmQuit, Live: a.InFlightCount()}
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
	if a.handleVimMotion(k) {
		return false
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
func (a *App) handleConfirmKey(k Key) bool {
	// Only ConfirmYes knows what was being asked, and only the quit answer ends the loop —
	// so the teardown hangs off its return value rather than off the key.
	if k.isChar('y') || k.isChar('Y') {
		return a.ConfirmYes()
	}
	a.ConfirmNo()
	return false
}

// handleVimMotion handles `gg` and `G` once for every screen.
//
// It returns true if the key was consumed. `g` on its own only arms the pair; anything
// else disarms it, so a forgotten `g` cannot silently swallow the next keystroke.
func (a *App) handleVimMotion(k Key) bool {
	// Prompts own every key while they are open.
	if a.EnteringArgs || a.Searching || a.SendingInput || a.HistorySearching || a.Jumping {
		return false
	}
	switch {
	case k.isChar('g'):
		if a.PendingG {
			a.PendingG = false
			a.GotoTop()
		} else {
			a.PendingG = true
		}
		return true
	case k.isChar('G'):
		a.PendingG = false
		a.GotoBottom()
		return true
	default:
		a.PendingG = false
		return false
	}
}

func (a *App) handleDetailKey(k Key) bool {
	switch {
	case k.isChar('q'), k.isCtrl('c'):
		return a.quit()
	case k.kind == keyEsc, k.isChar('s'):
		a.CloseDetail()
	case k.isChar('j'), k.kind == keyDown:
		a.DetailScroll(1)
	case k.isChar('k'), k.kind == keyUp:
		a.DetailScroll(-1)
	case k.kind == keyPageDown:
		a.DetailScroll(10)
	case k.kind == keyPageUp:
		a.DetailScroll(-10)
	// Running it is the point of having read this.
	case k.kind == keyEnter:
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.RequestRun(name, nil)
		}
	case k.isChar('a'):
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.BeginArgs(name)
		}
	}
	return false
}

func (a *App) handleHelpKey(k Key) bool {
	switch {
	case k.isChar('q'), k.isCtrl('c'):
		return a.quit()
	case k.isChar('?'), k.kind == keyEsc:
		a.ToggleHelp()
	case k.isChar('j'), k.kind == keyDown:
		a.HelpScroll(1)
	case k.isChar('k'), k.kind == keyUp:
		a.HelpScroll(-1)
	case k.kind == keyPageDown:
		a.HelpScroll(10)
	case k.kind == keyPageUp:
		a.HelpScroll(-10)
	}
	return false
}

func (a *App) handleArgsKey(k Key) {
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
	case k.kind == keyChar && !k.ctrl:
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
	case k.kind == keyChar && !k.ctrl:
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
	case k.kind == keyChar && !k.ctrl:
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

	case act() == keys.Quit, k.isCtrl('c'):
		return a.quit()

	case k.isChar('j'), k.kind == keyDown:
		a.MoveCursor(1)
	case k.isChar('k'), k.kind == keyUp:
		a.MoveCursor(-1)
	case k.isCtrl('d'):
		a.MoveCursor(a.HalfPage())
	case k.isCtrl('u'):
		a.MoveCursor(-a.HalfPage())
	case k.kind == keyPageDown:
		a.MoveCursor(15)
	case k.kind == keyPageUp:
		a.MoveCursor(-15)
	case k.kind == keyHome:
		a.MoveCursor(-len(a.Rows))
	case k.kind == keyEnd:
		a.MoveCursor(len(a.Rows))

	// The pivot. Selection, filter and the other mode's folds all survive it. On `p`, not
	// `g`: `gg` belongs to vim.
	case act() == keys.Pivot:
		a.ToggleMode()

	// Space folds, enter runs — kept strictly separate. A node that is both a group and a
	// task (`backend:migrate`) is then runnable from its own header, so its subtree never
	// has to relist it just to make it reachable.
	case k.isChar(' '):
		a.ToggleFold()
	case k.kind == keyEnter:
		if ti := a.SelectedTask(); ti >= 0 {
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
		if n := a.SelectedNode(); n != nil && n.IsGroup() {
			a.ToggleFold()
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

	case act() == keys.Help:
		a.ToggleHelp()

	// Back to whatever is still running.
	case act() == keys.ResumeRun:
		a.ResumeRun()

	// Past runs.
	case act() == keys.History:
		a.OpenHistory()

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
		case k.kind == keyChar && !k.ctrl:
			a.PushHistorySearch(k.ch)
		}
		return false
	}

	act := func() keys.Action { return a.action(k, ScreenHistory) }

	switch {
	case act() == keys.Quit, k.isCtrl('c'):
		return a.quit()
	case k.kind == keyEsc:
		a.Screen = ScreenPicker
		a.Status = ""

	// Widen to every project, or narrow back to this one.
	case act() == keys.AllProjects:
		a.ToggleHistoryScope()

	case act() == keys.Search:
		a.BeginHistorySearch()
	case act() == keys.Help:
		a.ToggleHelp()

	case k.isChar('j'), k.kind == keyDown:
		a.HistoryMoveCursor(1)
	case k.isChar('k'), k.kind == keyUp:
		a.HistoryMoveCursor(-1)
	case k.isCtrl('d'):
		a.HistoryMoveCursor(a.HalfPage())
	case k.isCtrl('u'):
		a.HistoryMoveCursor(-a.HalfPage())
	case k.kind == keyPageDown:
		a.HistoryMoveCursor(15)
	case k.kind == keyPageUp:
		a.HistoryMoveCursor(-15)
	case k.kind == keyHome:
		a.HistoryMoveCursor(-len(a.History))
	case k.kind == keyEnd:
		a.HistoryMoveCursor(len(a.History))
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
		case k.kind == keyChar:
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
		case k.kind == keyChar && !k.ctrl:
			a.PushSearch(k.ch)
			return false
		}
	}

	act := func() keys.Action { return a.action(k, ScreenRun) }

	switch {
	case k.isCtrl('c'), act() == keys.Quit:
		return a.quit()

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

	case k.isChar('j'), k.kind == keyDown:
		a.RunMoveCursor(1)
	case k.isChar('k'), k.kind == keyUp:
		a.RunMoveCursor(-1)
	case k.isCtrl('d'):
		a.RunMoveCursor(a.HalfPage())
	case k.isCtrl('u'):
		a.RunMoveCursor(-a.HalfPage())

	// Reading an error usually ends with pasting it somewhere.
	case act() == keys.Yank:
		a.YankLine()
	case act() == keys.YankAll:
		a.YankTaskOutput()

	case k.kind == keyPageDown:
		a.RunMoveCursor(15)
	case k.kind == keyPageUp:
		a.RunMoveCursor(-15)
	case k.kind == keyHome:
		a.RunMoveCursor(-len(a.RunRows))
	case k.kind == keyEnd:
		a.RunMoveCursor(len(a.RunRows))

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

	// Re-run with different arguments.
	case act() == keys.Args:
		if name, ok := a.RunSelectedTask(); ok {
			a.BeginArgs(name)
		}

	// Jump straight to a slot, as the bar numbers them. Last, so that rebinding an action
	// onto a digit still wins — the keymap is the thing users can change.
	case k.kind == keyChar && k.ch >= '1' && k.ch <= '9' && !k.ctrl:
		a.FocusSlotNumber(int(k.ch - '0'))
	}
	return false
}

// KeyFor maps one character of a `--keys` string to a keypress. `\t` folds everything and
// `\n` runs, which is how a screenshot exercises the two things a letter cannot reach.
func KeyFor(c rune) Key {
	switch c {
	case '\t':
		return Tab()
	case '\n':
		return Enter()
	default:
		return Char(c)
	}
}
