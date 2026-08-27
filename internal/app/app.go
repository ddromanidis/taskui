// Package app holds the application state: the task list, the active pivot, fold state,
// filter and cursor — plus the Bubble Tea model that drives them.
package app

import (
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sahilm/fuzzy"

	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/keys"
	"github.com/ddromanidis/taskui/internal/loc"
	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/search"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/theme"
	"github.com/ddromanidis/taskui/internal/watch"
)

type Screen int

const (
	// ScreenPicker is browsing the Taskfile.
	ScreenPicker Screen = iota
	// ScreenRun is watching a run.
	ScreenRun
	// ScreenHistory is browsing runs that already happened.
	ScreenHistory
	// ScreenHelp is the keymap.
	ScreenHelp
	// ScreenDetail is what a task is and what it will run.
	ScreenDetail
	// ScreenTimeline is how one task has gone, run after run.
	ScreenTimeline
	// ScreenDiff is what changed between two runs of one task.
	ScreenDiff
)

// Fold is how much of a task's output is on screen.
//
// Three states rather than open-or-shut, because a run is two things at once: a shape you
// are scanning and a log you are reading. FoldPeek is the resting state — a task folded
// down to nothing is a task you have to open to discover whether it was worth opening, and
// on a 26-node run that is 26 guesses. The last few lines are almost always enough to
// answer it, because the interesting part of a command's output is the end.
type Fold int

const (
	// FoldPeek is a window on the last few lines, one line per row, cut off at the edge
	// rather than wrapped — a single 300-character line must not swallow the whole window.
	// It is the zero value, and therefore the default.
	FoldPeek Fold = iota
	// FoldFull is all of it, wrapped, which is the mode you read in.
	FoldFull
	// FoldHidden is one row: the task, how it went, how much it said. Nothing else.
	FoldHidden
)

// Next cycles hidden → peek → full → hidden.
//
// A cycle rather than a toggle plus a second key: the three states are one axis, and "how
// much of this do I want to see" is a question with an obvious "more" direction.
func (f Fold) Next() Fold {
	switch f {
	case FoldHidden:
		return FoldPeek
	case FoldPeek:
		return FoldFull
	default:
		return FoldHidden
	}
}

// RunRow is a row in the run view. Output lines are rows of the same list as the tasks
// that produced them, which is what makes one fold tree hold both.
type RunRow struct {
	// IsTask distinguishes a task header from one of its output lines.
	IsTask bool
	// Name is the task, for a task row.
	Name string
	// Task is the owning task, for a line row.
	Task  string
	Index int
	Depth int
	// Fold applies to task rows.
	Fold Fold
	// Peek marks a line that is part of a peek window rather than the full output: one
	// row, clipped.
	//
	// Carried on the row rather than looked up per frame because it decides this row's
	// height, and the height of every row is measured twice on every draw — once to choose
	// the column count and once to lay them out.
	Peek bool
}

// ConfirmReason says why a run is waiting for a yes.
type ConfirmReason int

const (
	// TouchesProduction means the task is on the `.taskui-danger` list.
	TouchesProduction ConfirmReason = iota
	// WouldStopRunning means this task's slot already holds a live run, and restarting it
	// kills that one.
	WouldStopRunning
)

type ConfirmKind int

const (
	ConfirmRun ConfirmKind = iota
	ConfirmQuit
	ConfirmStopAll
)

// Confirm is something waiting on a yes.
//
// One mechanism rather than one per question: a confirmation takes over the whole keymap
// while it is up, and a second flag for the key handler to check is a second flag to
// forget to check.
type Confirm struct {
	Kind ConfirmKind
	// Name and Args belong to ConfirmRun: start this task, once the reason has been
	// answered.
	Name   string
	Args   []string
	Reason ConfirmReason
	// Live is how many runs there were when a quit or stop-all question was asked — it is
	// what the prompt says, and re-counting as runs finish under it would make the number
	// move while you read it.
	Live int
}

// stopRun stops a run, escalating on a second ask, and says what happened.
//
// A free function rather than a method because every caller wants to write the answer into
// the app's status line, and the escalation is the whole point of it being one function:
// `x` on a wedged run should not be a key that does nothing the second time you press it —
// but neither should the first press be a SIGKILL, so the two live together where the
// order is obvious.
func stopRun(r *run.Run) string {
	if r.Finished() {
		return "that run has already finished"
	}
	if r.Killed() {
		return fmt.Sprintf("`%s` has had SIGKILL — nothing louder to send, waiting on the OS", r.Root)
	}
	if r.Cancelled() {
		r.Kill()
		return fmt.Sprintf("killed `%s` — SIGKILL to the process group", r.Root)
	}
	r.Cancel()
	return fmt.Sprintf("stopping `%s` — again to kill it outright", r.Root)
}

// MaxSlots is how many runs can be open at once.
//
// The cap is not about memory — it is that every slot may own a process group, and a watch
// loop that could claim slots without bound would spawn containers without bound with it.
// Six is past what fits legibly in the slot bar anyway.
const MaxSlots = 6

// slotView is the view state that belongs to a run rather than to the app: where you had
// scrolled, what you had unfolded, whether you were following.
//
// Kept per slot because the whole point of leaving `docker compose logs -f` in one slot is
// to come back to it — and coming back to the top of a 20,000-line buffer is not coming
// back to it.
type slotView struct {
	cursor         int
	offset         int
	folds          map[string]Fold
	followedOpen   string
	following      bool
	focusedFailure string
	savedTo        string
}

// Parked is a run that is not the one on screen. Its capture goroutine keeps draining and
// its process keeps going: parking is a state of the UI, not of the child.
type Parked struct {
	Run *run.Run
	// Seq is creation order, so a run keeps its position in the slot bar for as long as it
	// is open — a bar whose entries reshuffle when you switch is unusable as a switcher.
	Seq  uint64
	view slotView
}

// SlotInfo is one entry in the slot bar.
type SlotInfo struct {
	Seq     uint64
	Root    string
	Status  run.Status
	Elapsed time.Duration
	Focused bool
}

type App struct {
	Tasks  []task.Task
	Mode   pivot.Mode
	Tree   *pivot.Tree
	Rows   []pivot.Row
	Cursor int
	// Offset is the viewport offset, kept here so scrolling survives rebuilds.
	Offset int

	// expanded is per-mode: bouncing between pivots must not collapse what you opened on
	// the other side.
	expanded map[string]map[string]bool

	Filtering bool
	Query     string

	Root   string
	Status string
	Theme  theme.Theme
	Keymap *keys.Keymap

	Screen Screen
	// Run is the run on screen. The live view fields below belong to this run; the others
	// are in Parked with a copy of theirs.
	Run *run.Run
	// Parked holds runs that are open but not on screen, still going.
	Parked []Parked
	// FocusSeq is which slot Run occupies. Zero before anything has ever run.
	FocusSeq  uint64
	nextSeq   uint64
	RunRows   []RunRow
	RunCursor int
	RunOffset int
	// runFolds is how much of each task's output this slot is showing. Absent means the
	// default, which is a peek.
	runFolds map[string]Fold
	// PeekLines is how many lines a peeking task shows. Configurable.
	PeekLines int
	// followedOpen is the task following opened by itself, so it can be given back when
	// following moves on.
	followedOpen string
	// Following: while true the view tracks whatever is running. Any manual cursor move
	// turns it off — once you have gone looking for something, the view should stop moving
	// under you.
	Following bool
	// focusedFailure exists so a failure yanks the view exactly once, not on every poll.
	focusedFailure string

	// Search is the output search. Distinct from Query, which filters task names in the
	// picker — a couple of hundred short strings versus potentially megabytes of output,
	// so they get separate affordances even though both are bound to `/`.
	Search      *search.Query
	Searching   bool
	SearchInput string
	SearchHits  []search.LiveHit
	SearchIdx   int
	// FilterMatches shows only matching lines, grouped under the task that produced them.
	FilterMatches bool
	SearchError   string
	// SavedTo is where the finished run was written, if it was.
	SavedTo string

	// Outcomes says how each task went last time, so browsing answers "what is broken
	// right now" without opening anything.
	Outcomes map[string]store.Outcome

	History       []store.Manifest
	HistoryCursor int
	HistoryOffset int
	// HistoryAllProjects widens the archive beyond this project. Scoped by default: runs
	// from every project in one list stops being useful the moment you use taskui in two
	// repos.
	HistoryAllProjects bool

	// Jumping moves the cursor without narrowing the list — what you want when you are
	// looking at the tree rather than filtering it.
	Jumping     bool
	JumpQuery   string
	JumpMatches []int
	JumpIdx     int
	// jumpOrigin is where the cursor was before the jump, so `esc` really does cancel.
	jumpOrigin int

	// PendingG is half of a `gg`. Any other key clears it, so a stray `g` cannot lurk.
	PendingG bool
	// EscStreak counts consecutive `esc` presses with nothing left to dismiss.
	//
	// `esc` used to quit from the picker, which put "leave the tool" on the key everyone
	// presses to mean back out of whatever this is. It no longer does — but a key that
	// silently does nothing reads as broken, so the second press in a row says where the
	// exit actually is. Any other key resets it.
	EscStreak int
	// helpReturn is where `?` was pressed, so `esc` puts you back rather than somewhere
	// arbitrary.
	helpReturn   Screen
	inHelp       bool
	HelpOffset   int
	DetailOf     string
	Detail       graph.Detail
	DetailOffset int
	// Watching is the task being re-run when the source changes.
	Watching string
	watcher  *watch.Watch

	// One task's history, and the diff between two of its runs.
	TimelineOf     string
	Timeline       []store.Point
	TimelineCursor int
	TimelineOffset int
	timelineReturn Screen

	DiffOf string
	// DiffAgainstWhat names the older side in words — "when it last passed", "the run
	// before". The header says which comparison you are looking at because there are two,
	// and a diff you have mistaken for the other one is worse than no diff.
	DiffAgainstWhat string
	// DiffSubject is how the newer side was invoked.
	DiffSubject string
	DiffAgainst store.Point
	DiffStat    diff.Stat
	DiffRows    []DiffRow
	DiffCursor  int
	DiffOffset  int
	// DiffContext is how many unchanged lines are kept either side of a change. The whole
	// value of the view is that it is short, so this starts low.
	DiffContext int
	diffEdits   []diff.Edit
	diffReturn  Screen

	// locs indexes the project so a `file:line` in captured output can be opened. Built on
	// first use — most sessions never press `e`.
	locs *loc.Resolver
	// pendingEdit is an editor waiting to be launched, parked here because a key handler
	// cannot return a Bubble Tea command.
	pendingEdit *loc.Editor

	// Viewport is the body height of the last frame, so `^d` can move by half a screen.
	Viewport int

	// FilterContext is the lines of context kept either side of a hit in the filtered
	// view. A `FAIL:` line without the assertion underneath it is the half that does not
	// tell you anything.
	FilterContext int

	// The args prompt. Plenty of tasks need arguments — `wt:new NAME=backend`,
	// `backend:test -- -p ingest` — and a runner that cannot pass them can only run half
	// of a real Taskfile.
	EnteringArgs bool
	ArgsInput    string
	// ArgsCursor is the caret position, in characters. Pre-filling `NAME=` is only useful
	// if the caret lands after the `=`.
	ArgsCursor int
	ArgsTarget string

	// Confirm is whatever is waiting on a yes. `⏎` runs things for real, and a fuzzy
	// filter puts every task one keypress away, so the ones that touch production get a
	// stop — as do the two ways of killing a run you cannot see.
	Confirm *Confirm

	// SendingInput sends keystrokes to the running task instead of to taskui.
	// Deliberately a mode: half the run view's keys are single letters, and `y` meaning
	// both "yes" and "move the cursor" would be intolerable.
	SendingInput bool
	// InteractiveNext is sticky: the next run goes out interleaved so the task can ask
	// questions.
	InteractiveNext bool
	// ForceNext is sticky: the next run passes `--force`, so go-task's up-to-date checks
	// do not skip it.
	ForceNext bool

	// Cross-run search over the archive, from the history list.
	HistorySearching bool
	HistoryQuery     string
	// HistoryHits maps a run id to its hit count, for the runs that matched.
	HistoryHits map[string]int

	// Width and Height are the terminal size, as Bubble Tea reports it.
	Width  int
	Height int

	// Phase is which animation frame the cursor's edges are drawn at. It only moves for a
	// theme that asked to animate; everything else leaves it at zero, which is also what
	// keeps `--screenshot` deterministic.
	Phase int

	// stateDir is where runs are archived. A field rather than a call so tests can point
	// it somewhere disposable.
	stateDir string
}

func New(tasks []task.Task, root string) *App {
	a := &App{
		Tasks:         tasks,
		Mode:          pivot.Domain,
		Tree:          &pivot.Tree{},
		expanded:      map[string]map[string]bool{},
		Root:          root,
		Theme:         theme.DefaultTheme(),
		Keymap:        keys.NewKeymap(),
		Screen:        ScreenPicker,
		runFolds:      map[string]Fold{},
		PeekLines:     theme.DefaultPeekLines,
		Following:     true,
		Outcomes:      map[string]store.Outcome{},
		HistoryHits:   map[string]int{},
		Viewport:      20,
		FilterContext: 2,
		DiffContext:   3,
		Width:         80,
		Height:        24,
		stateDir:      store.StateDir(),
	}
	a.Rebuild(-1)
	a.ReloadOutcomes()
	return a
}

// StateDir is where this app archives its runs.
func (a *App) StateDir() string { return a.stateDir }

// SetStateDir points the archive somewhere else, for tests.
func (a *App) SetStateDir(dir string) {
	a.stateDir = dir
	a.ReloadOutcomes()
}

func (a *App) ReloadOutcomes() {
	a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
}

// WithConfig applies a loaded config. Anything wrong with the file is surfaced rather than
// swallowed — a colour that silently does nothing is worse than one that says why.
func (a *App) WithConfig(config theme.Config) *App {
	a.Theme = config.Theme
	a.Keymap = config.Keymap
	a.PeekLines = config.PeekLines
	if len(config.Problems) > 0 {
		a.Status = "config: " + strings.Join(config.Problems, "; ")
	}
	return a
}

// StartRun kicks off `task <name>` and switches to the run view.
func (a *App) StartRun(name string) error {
	return a.StartRunWith(name, nil)
}

// ResumeRun returns to a run already in progress.
//
// `v` means take me to the thing that is going, so it seeks out a live slot rather than
// reopening whichever one you happened to leave last. It used to only flip the screen,
// which gave the one answer the key must never give: a run that finished twenty minutes
// ago, while the deploy you were asking about carries on in a slot you cannot see.
//
// A live focused slot stays put — there is no reason to move you off a run you are already
// watching — and otherwise it takes the earliest-started live slot, so pressing `v` twice
// lands in the same place instead of hopping between two running tasks. With nothing
// running at all it falls back to the last run, because "show me what just happened" is
// still the useful answer to a key that has nothing live to offer.
func (a *App) ResumeRun() bool {
	if a.Run == nil {
		a.Status = "no run to go back to"
		return false
	}
	if a.Run.Finished() {
		for _, s := range a.Slots() {
			if s.Status == run.Running {
				a.FocusSlot(s.Seq)
				break
			}
		}
	}
	a.Screen = ScreenRun
	a.Status = ""
	return true
}

// RequestRun runs a task, unless something needs a yes first.
//
// Starting a different task no longer disturbs what is already going: it parks the current
// run in its own slot and opens a new one. Starting the task that is already running still
// means "show me it" rather than "start a second one" — one slot per task name, so a
// second copy would have nowhere to live even if it were wanted.
func (a *App) RequestRun(name string, args []string) {
	if a.liveSlot(name) {
		a.focusTask(name)
		a.ResumeRun()
		return
	}

	if a.Confirm == nil {
		for _, t := range a.Tasks {
			if t.Name == name && t.Dangerous {
				a.Confirm = &Confirm{
					Kind:   ConfirmRun,
					Name:   name,
					Args:   append([]string(nil), args...),
					Reason: TouchesProduction,
				}
				return
			}
		}
	}
	a.Confirm = nil
	if err := a.StartRunWith(name, args); err != nil {
		a.Status = fmt.Sprintf("could not start `task %s`: %v", name, err)
	}
}

// ConfirmYes answers whatever is pending. It returns true if the answer was "quit".
func (a *App) ConfirmYes() bool {
	pending := a.Confirm
	a.Confirm = nil
	if pending == nil {
		return false
	}
	switch pending.Kind {
	case ConfirmRun:
		if err := a.StartRunWith(pending.Name, pending.Args); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", pending.Name, err)
		}
		return false
	case ConfirmStopAll:
		a.StopAll()
		return false
	default:
		return true
	}
}

func (a *App) ConfirmNo() {
	pending := a.Confirm
	a.Confirm = nil
	if pending == nil {
		return
	}
	if pending.Kind == ConfirmRun {
		a.Status = "not run"
	} else {
		a.Status = "left running"
	}
}

func (a *App) StartRunWith(name string, args []string) error {
	seq, why := a.claimSlot(name)
	if why != "" {
		a.Status = why
		return nil
	}
	r, err := run.Start(a.Root, name, args, a.InteractiveNext, a.ForceNext)
	if err != nil {
		return err
	}
	// Whatever was in this slot is being replaced. Rust relied on Drop to take its process
	// group with it; here it has to be said out loud, or a restart silently orphans the
	// run it was restarting.
	a.retire(a.Run)
	a.Run = r
	a.FocusSeq = seq
	a.Screen = ScreenRun
	a.RunCursor = 0
	a.RunOffset = 0
	a.runFolds = map[string]Fold{}
	a.followedOpen = ""
	a.Following = true
	a.focusedFailure = ""
	a.Status = ""
	a.ClearSearch()
	a.SavedTo = ""
	a.RebuildRunRows()
	return nil
}

// retire takes a displaced run's process group with it.
func (a *App) retire(r *run.Run) {
	if r != nil && !r.Finished() {
		r.Cancel()
	}
}

// claimSlot finds the slot this run should go in, parking whatever is on screen to make
// room.
//
// Slots are keyed by task name, which is what makes the bar readable — `▶ up` is always
// the same run of `up`. Re-running a task therefore reuses its slot and keeps its
// position, rather than pushing a near-duplicate entry alongside it.
//
// It returns the sequence number to give the new run, or the reason it cannot start.
func (a *App) claimSlot(name string) (uint64, string) {
	// Restarting the run already on screen. The caller replaces it and retires the old
	// one, which is what a restart is.
	if a.Run != nil && a.Run.Root == name {
		return a.FocusSeq, ""
	}
	// Restarting one that was parked: take its slot back so it does not move.
	for i, p := range a.Parked {
		if p.Run.Root == name {
			seq := p.Seq
			a.retire(p.Run)
			a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
			a.parkFocused()
			return seq, ""
		}
	}
	// A genuinely new slot.
	if a.openSlots() >= MaxSlots {
		// A finished run has already been archived, so reclaiming its slot loses nothing
		// you cannot reopen from history. A live one is somebody's compose stack; it is
		// never taken without being asked.
		freed := false
		for i, p := range a.Parked {
			if p.Run.Finished() {
				a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
				freed = true
				break
			}
		}
		if !freed {
			return 0, fmt.Sprintf("all %d run slots are busy — stop one with `x` first", MaxSlots)
		}
	}
	a.parkFocused()
	a.nextSeq++
	return a.nextSeq, ""
}

// claimStoredSlot is where a run read off disk goes.
//
// Deliberately not claimSlot: that keys on task name, and an archived `deploy` would then
// take the slot of the `deploy` you have running right now and kill it. Browsing history
// instead reuses one slot, so paging through twenty old runs does not bury the live ones.
func (a *App) claimStoredSlot() (uint64, string) {
	if a.Run != nil && a.Run.IsStored() {
		return a.FocusSeq, ""
	}
	for i, p := range a.Parked {
		if p.Run.IsStored() {
			seq := p.Seq
			a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
			a.parkFocused()
			return seq, ""
		}
	}
	if a.openSlots() >= MaxSlots {
		freed := false
		for i, p := range a.Parked {
			if p.Run.Finished() {
				a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
				freed = true
				break
			}
		}
		if !freed {
			return 0, fmt.Sprintf("all %d run slots are busy — stop one with `x` first", MaxSlots)
		}
	}
	a.parkFocused()
	a.nextSeq++
	return a.nextSeq, ""
}

func (a *App) openSlots() int {
	n := len(a.Parked)
	if a.Run != nil {
		n++
	}
	return n
}

// parkFocused moves the run on screen into the parking lot, keeping its view state with it.
func (a *App) parkFocused() {
	if a.Run == nil {
		return
	}
	a.Parked = append(a.Parked, Parked{Run: a.Run, Seq: a.FocusSeq, view: a.snapshotView()})
	a.Run = nil
}

func (a *App) snapshotView() slotView {
	folds := make(map[string]Fold, len(a.runFolds))
	maps.Copy(folds, a.runFolds)
	return slotView{
		cursor:         a.RunCursor,
		offset:         a.RunOffset,
		folds:          folds,
		followedOpen:   a.followedOpen,
		following:      a.Following,
		focusedFailure: a.focusedFailure,
		savedTo:        a.SavedTo,
	}
}

func (a *App) restoreView(v slotView) {
	a.RunCursor = v.cursor
	a.RunOffset = v.offset
	a.runFolds = v.folds
	if a.runFolds == nil {
		a.runFolds = map[string]Fold{}
	}
	a.followedOpen = v.followedOpen
	a.Following = v.following
	a.focusedFailure = v.focusedFailure
	a.SavedTo = v.savedTo
	// The query survives a switch but its hits cannot: they are indices into the run you
	// just left. Re-running it against the new one is both cheap and what you meant by
	// keeping the query.
	a.refreshSearch()
	a.RebuildRunRows()
}

// Slots lists every open run, in slot-bar order.
func (a *App) Slots() []SlotInfo {
	describe := func(r *run.Run, seq uint64, focused bool) SlotInfo {
		status := run.Running
		if r.Finished() {
			status = run.Failed
			if r.Exit == 0 {
				status = run.Ok
			}
		}
		elapsed := time.Since(r.Started)
		if r.HasDuration {
			elapsed = r.Duration
		}
		return SlotInfo{Seq: seq, Root: r.Root, Status: status, Elapsed: elapsed, Focused: focused}
	}
	out := make([]SlotInfo, 0, a.openSlots())
	for _, p := range a.Parked {
		out = append(out, describe(p.Run, p.Seq, false))
	}
	if a.Run != nil {
		out = append(out, describe(a.Run, a.FocusSeq, true))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// FocusSlot puts a slot on screen, parking the one that was.
func (a *App) FocusSlot(seq uint64) {
	if a.Run != nil && seq == a.FocusSeq {
		return
	}
	at := -1
	for i, p := range a.Parked {
		if p.Seq == seq {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	target := a.Parked[at]
	a.Parked = append(a.Parked[:at], a.Parked[at+1:]...)
	a.parkFocused()
	a.Run = target.Run
	a.FocusSeq = target.Seq
	a.restoreView(target.view)
	a.Screen = ScreenRun
	a.Status = ""
}

// focusTask focuses whichever slot holds this task, if one does.
func (a *App) focusTask(name string) {
	for _, p := range a.Parked {
		if p.Run.Root == name {
			a.FocusSlot(p.Seq)
			return
		}
	}
}

// CycleSlot steps through the slot bar. delta wraps, because with two slots — the usual
// case, a stack and the thing you are running against it — one key that always goes to the
// other one beats two keys that go in directions you have to think about.
func (a *App) CycleSlot(delta int) {
	slots := a.Slots()
	if len(slots) < 2 {
		a.Status = "only one run open"
		return
	}
	at := 0
	for i, s := range slots {
		if s.Seq == a.FocusSeq {
			at = i
		}
	}
	n := len(slots)
	next := ((at+delta)%n + n) % n
	a.FocusSlot(slots[next].Seq)
}

// FocusSlotNumber jumps straight to slot n, counting from one as the bar labels them.
func (a *App) FocusSlotNumber(n int) {
	slots := a.Slots()
	if n < 1 || n > len(slots) {
		a.Status = fmt.Sprintf("no slot %d", n)
		return
	}
	a.FocusSlot(slots[n-1].Seq)
}

// CloseSlot closes the slot on screen and falls back to the most recent of the rest.
//
// A live run is never closed out from under you — stopping it is a separate, deliberate
// key, and conflating the two would make a mistyped `X` kill a deploy.
func (a *App) CloseSlot() {
	if a.Run == nil {
		return
	}
	if !a.Run.Finished() {
		a.Status = "still running — stop it with `x` before closing it"
		return
	}
	a.Run = nil

	newest := -1
	for i, p := range a.Parked {
		if newest < 0 || p.Seq > a.Parked[newest].Seq {
			newest = i
		}
	}
	if newest < 0 {
		a.FocusSeq = 0
		a.RunRows = nil
		a.RunCursor = 0
		a.RunOffset = 0
		a.runFolds = map[string]Fold{}
		a.focusedFailure = ""
		a.SavedTo = ""
		a.ClearSearch()
		a.Screen = ScreenPicker
		a.Status = "no runs open"
		return
	}
	target := a.Parked[newest]
	a.Parked = append(a.Parked[:newest], a.Parked[newest+1:]...)
	a.Run = target.Run
	a.FocusSeq = target.Seq
	a.restoreView(target.view)
}

// slotRun is the slot holding this task, whether it is on screen or parked.
func (a *App) slotRun(name string) *run.Run {
	if a.Run != nil && a.Run.Root == name {
		return a.Run
	}
	for _, p := range a.Parked {
		if p.Run.Root == name {
			return p.Run
		}
	}
	return nil
}

func (a *App) liveSlot(name string) bool {
	r := a.slotRun(name)
	return r != nil && !r.Finished()
}

// RunningFor is how long this task has been running, if it is running at all.
//
// The picker's outcome column answers "how did it go last time", which is a different
// question from "is it going now" — and with runs living in slots that are not on screen,
// "now" was the one thing the task list stayed silent about. A task running this second
// looked exactly like one that ran yesterday.
func (a *App) RunningFor(name string) (time.Duration, bool) {
	r := a.slotRun(name)
	if r == nil || r.Finished() {
		return 0, false
	}
	return time.Since(r.Started), true
}

// BeginArgs opens the args prompt for a task, pre-filled with the variables the task
// actually asks for.
//
// `requires: { vars: [NAME] }` is a declaration, not prose, so `NAME=` can be filled in
// with confidence — and only the key, never the example value, which would be handing you
// someone else's argument. The caret lands after the last `=`.
func (a *App) BeginArgs(name string) {
	a.EnteringArgs = true
	a.ArgsTarget = name
	a.Status = ""

	// One `--summary`, ~40ms, only when the prompt opens.
	vars := graph.RequiredVars(a.Root, name)
	if len(vars) == 0 {
		// Nothing declared: fall back to a `KEY=value` shape in the description.
		if hint, ok := a.ArgsHint(); ok {
			vars = task.KeysInHint(hint)
		}
	}

	parts := make([]string, 0, len(vars))
	for _, k := range vars {
		parts = append(parts, k+"=")
	}
	a.ArgsInput = strings.Join(parts, " ")
	a.ArgsCursor = len([]rune(a.ArgsInput))
}

func (a *App) CancelArgs() {
	a.EnteringArgs = false
	a.ArgsTarget = ""
	a.ArgsInput = ""
	a.ArgsCursor = 0
}

func (a *App) ArgsInsert(c rune) {
	runes := []rune(a.ArgsInput)
	at := min(a.ArgsCursor, len(runes))
	out := make([]rune, 0, len(runes)+1)
	out = append(out, runes[:at]...)
	out = append(out, c)
	out = append(out, runes[at:]...)
	a.ArgsInput = string(out)
	a.ArgsCursor = at + 1
}

func (a *App) ArgsBackspace() {
	if a.ArgsCursor == 0 {
		return
	}
	runes := []rune(a.ArgsInput)
	at := a.ArgsCursor - 1
	a.ArgsInput = string(append(runes[:at], runes[at+1:]...))
	a.ArgsCursor = at
}

func (a *App) ArgsDelete() {
	runes := []rune(a.ArgsInput)
	if a.ArgsCursor >= len(runes) {
		return
	}
	a.ArgsInput = string(append(runes[:a.ArgsCursor], runes[a.ArgsCursor+1:]...))
}

func (a *App) ArgsMove(delta int) {
	a.ArgsCursor = clamp(a.ArgsCursor+delta, 0, len([]rune(a.ArgsInput)))
}

func (a *App) ArgsHome() { a.ArgsCursor = 0 }
func (a *App) ArgsEnd()  { a.ArgsCursor = len([]rune(a.ArgsInput)) }

func (a *App) ConfirmArgs() {
	name := a.ArgsTarget
	if name == "" {
		return
	}
	args := task.SplitArgs(a.ArgsInput)
	a.CancelArgs()
	a.RequestRun(name, args)
}

// ArgsHint is the usage hint for whatever the args prompt is aimed at.
func (a *App) ArgsHint() (string, bool) {
	if a.ArgsTarget == "" {
		return "", false
	}
	for _, t := range a.Tasks {
		if t.Name == a.ArgsTarget {
			return t.ArgsHint()
		}
	}
	return "", false
}

func (a *App) BeginJump() {
	a.Jumping = true
	a.JumpQuery = ""
	a.JumpMatches = nil
	a.JumpIdx = 0
	a.jumpOrigin = a.Cursor
	a.Status = ""
}

func (a *App) PushJump(c rune) {
	a.JumpQuery += string(c)
	a.applyJump()
}

func (a *App) PopJump() {
	runes := []rune(a.JumpQuery)
	if len(runes) > 0 {
		a.JumpQuery = string(runes[:len(runes)-1])
	}
	a.applyJump()
}

// AcceptJump keeps the cursor where the jump left it.
func (a *App) AcceptJump() {
	a.Jumping = false
	a.JumpQuery = ""
}

// CancelJump puts the cursor back where it started.
func (a *App) CancelJump() {
	a.Jumping = false
	a.JumpQuery = ""
	a.JumpMatches = nil
	a.Cursor = min(a.jumpOrigin, max(0, len(a.Rows)-1))
}

func (a *App) JumpStep(delta int) {
	if len(a.JumpMatches) == 0 {
		return
	}
	n := len(a.JumpMatches)
	a.JumpIdx = ((a.JumpIdx+delta)%n + n) % n
	a.gotoMatch()
}

func (a *App) applyJump() {
	if a.JumpQuery == "" {
		a.JumpMatches = nil
		a.Cursor = min(a.jumpOrigin, max(0, len(a.Rows)-1))
		return
	}
	a.JumpMatches = a.matchingTasks(a.JumpQuery)
	a.JumpIdx = 0
	a.gotoMatch()
}

// gotoMatch moves to the current match, opening whatever folds hide it. The tree itself is
// left alone — that is the whole difference from the filter.
func (a *App) gotoMatch() {
	if a.JumpIdx >= len(a.JumpMatches) {
		return
	}
	a.Rebuild(a.JumpMatches[a.JumpIdx])
}

// OpenDetail shows what the task under the cursor actually is: its description, what it
// requires, and the commands it will run — before running it.
func (a *App) OpenDetail() {
	ti := a.SelectedTask()
	if ti < 0 {
		a.Status = "nothing here to describe — space folds it"
		return
	}
	name := a.Tasks[ti].Name
	// One `--summary`, the same call the graph and the args prompt already make.
	a.Detail = graph.DescribeTask(a.Root, name)
	a.DetailOf = name
	a.DetailOffset = 0
	a.Screen = ScreenDetail
	a.Status = ""
}

func (a *App) CloseDetail() {
	a.DetailOf = ""
	a.Screen = ScreenPicker
}

func (a *App) DetailScroll(delta int) {
	a.DetailOffset = max(0, a.DetailOffset+delta)
}

// ToggleWatch watches the project and re-runs this task whenever something changes.
func (a *App) ToggleWatch() {
	if a.Watching != "" {
		a.Watching = ""
		if a.watcher != nil {
			a.watcher.Close()
			a.watcher = nil
		}
		a.Status = "watch off"
		return
	}
	if a.Run == nil {
		a.Status = "nothing to watch — run something first"
		return
	}
	name := a.Run.Root
	w, err := watch.Start(a.Root)
	if err != nil {
		a.Status = fmt.Sprintf("could not watch this directory: %v", err)
		return
	}
	a.watcher = w
	a.Watching = name
	a.Status = fmt.Sprintf("watching — `task %s` re-runs when files change", name)
}

// PollWatch re-runs if the watcher has settled on a change.
func (a *App) PollWatch() bool {
	if a.Watching == "" || a.watcher == nil {
		return false
	}
	changed, ok := a.watcher.Poll()
	if !ok {
		return false
	}
	name := a.Watching
	// Never stack a task on top of itself: a save during a build would otherwise kill the
	// build that is already checking the previous save. Scoped to the watched task's own
	// slot — something else running in another slot is not this one's business, which is
	// the whole point of the slots.
	if a.liveSlot(name) {
		return false
	}

	var args []string
	if r := a.slotRun(name); r != nil {
		args = r.Args
		a.InteractiveNext = r.Interactive
		a.ForceNext = r.Force
	}

	// Deliberately bypasses the confirmation: watch mode is opt-in, on a task you just
	// ran, and a `y` prompt firing on every keystroke would be unusable. Which is also why
	// arming it on a production task is a bad idea.
	if err := a.StartRunWith(name, args); err != nil {
		a.Status = fmt.Sprintf("could not re-run `task %s`: %v", name, err)
		return false
	}
	a.Watching = name
	a.Status = fmt.Sprintf("%s changed — re-running", filepath.Base(changed))
	return true
}

// clipboardTools are shelled out to rather than taken as a dependency: one of these exists
// on any machine that has a clipboard at all, and a library for it would pull in a
// windowing stack for the sake of `pbcopy`.
var clipboardTools = []struct {
	name string
	args []string
}{
	{"pbcopy", nil},
	{"wl-copy", nil},
	{"xclip", []string{"-selection", "clipboard"}},
	{"xsel", []string{"--clipboard", "--input"}},
}

// Copy puts text on the system clipboard.
func (a *App) Copy(text, what string) {
	for _, tool := range clipboardTools {
		//nolint:gosec // the program and its flags come from the literal table above; the
		// only user data is the text, and that goes in on stdin.
		cmd := exec.Command(tool.name, tool.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			continue
		}
		lines := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
		if text == "" {
			lines = 0
		}
		if lines <= 1 {
			a.Status = "copied " + what
		} else {
			a.Status = fmt.Sprintf("copied %s — %d lines", what, lines)
		}
		return
	}
	a.Status = "no clipboard tool found (pbcopy, wl-copy, xclip, xsel)"
}

// YankLine copies the line under the cursor in the run view.
func (a *App) YankLine() {
	if a.RunCursor >= len(a.RunRows) || a.RunRows[a.RunCursor].IsTask {
		a.YankTaskOutput()
		return
	}
	row := a.RunRows[a.RunCursor]
	text := ""
	if a.Run != nil {
		if t, ok := a.Run.Tasks[row.Task]; ok && row.Index < len(t.Lines) {
			text = t.Lines[row.Index].Plain
		}
	}
	a.Copy(text, "line")
}

// YankTaskOutput copies everything the task under the cursor printed.
func (a *App) YankTaskOutput() {
	name, ok := a.RunSelectedTask()
	if !ok {
		return
	}
	var lines []string
	if a.Run != nil {
		if t, ok := a.Run.Tasks[name]; ok {
			for _, l := range t.Lines {
				lines = append(lines, l.Plain)
			}
		}
	}
	a.Copy(strings.Join(lines, "\n"), name+" output")
}

// HalfPage is what `^d` and `^u` move by.
func (a *App) HalfPage() int {
	return max(1, a.Viewport/2)
}

// GotoTop is `gg` — first row of whatever is on screen.
func (a *App) GotoTop() {
	switch a.Screen {
	case ScreenPicker:
		a.Cursor = 0
		a.Offset = 0
	case ScreenRun:
		a.RunCursor = 0
		a.RunOffset = 0
		a.Following = false
	case ScreenHistory:
		a.HistoryCursor = 0
		a.HistoryOffset = 0
	case ScreenHelp:
		a.HelpOffset = 0
	case ScreenDetail:
		a.DetailOffset = 0
	case ScreenTimeline:
		a.TimelineCursor = 0
		a.TimelineOffset = 0
	case ScreenDiff:
		a.DiffCursor = 0
		a.DiffOffset = 0
	}
}

// GotoBottom is `G` — last row.
func (a *App) GotoBottom() {
	switch a.Screen {
	case ScreenPicker:
		a.Cursor = max(0, len(a.Rows)-1)
	case ScreenRun:
		a.RunCursor = max(0, len(a.RunRows)-1)
		// Jumping to the end of a live run is the same intent as following it.
		a.Following = a.Run != nil && !a.Run.Finished()
	case ScreenHistory:
		a.HistoryCursor = max(0, len(a.History)-1)
	case ScreenHelp:
		// Clamped during rendering, so overshooting is harmless.
		a.HelpOffset = 1 << 20
	case ScreenDetail:
		a.DetailOffset = 1 << 20
	case ScreenTimeline:
		a.TimelineCursor = max(0, len(a.Timeline)-1)
	case ScreenDiff:
		a.DiffCursor = max(0, len(a.DiffRows)-1)
	}
}

// ToggleHelp is `?` from anywhere; `esc` comes back to where you were.
func (a *App) ToggleHelp() {
	if a.inHelp {
		a.Screen = a.helpReturn
		a.inHelp = false
		return
	}
	a.helpReturn = a.Screen
	a.inHelp = true
	a.Screen = ScreenHelp
	a.HelpOffset = 0
	a.Status = ""
}

func (a *App) HelpScroll(delta int) {
	a.HelpOffset = max(0, a.HelpOffset+delta)
}

// OpenLastRun opens the most recent stored run for this project, as `--last` does.
func (a *App) OpenLastRun() bool {
	here := a.Root
	for _, m := range store.List(a.stateDir) {
		if m.Dir != here {
			continue
		}
		a.History = []store.Manifest{m}
		a.HistoryCursor = 0
		a.OpenStoredRun()
		return true
	}
	return false
}

// OpenHistory loads the archive and switches to it.
func (a *App) OpenHistory() {
	a.reloadHistory()
	a.HistoryCursor = 0
	a.HistoryOffset = 0
	a.Screen = ScreenHistory
	if len(a.History) == 0 {
		a.Status = "no stored runs yet — run something and it will land here"
	} else {
		a.Status = ""
	}
}

func (a *App) reloadHistory() {
	all := store.List(a.stateDir)
	if a.HistoryAllProjects {
		a.History = all
		return
	}
	a.History = nil
	for _, m := range all {
		if m.Dir == a.Root {
			a.History = append(a.History, m)
		}
	}
}

func (a *App) BeginHistorySearch() {
	a.HistorySearching = true
	a.HistoryQuery = ""
	a.HistoryHits = map[string]int{}
	a.Status = ""
	a.reloadHistory()
}

func (a *App) ClearHistorySearch() {
	a.HistorySearching = false
	a.HistoryQuery = ""
	a.HistoryHits = map[string]int{}
	a.reloadHistory()
	a.HistoryCursor = 0
}

func (a *App) PushHistorySearch(c rune) {
	a.HistoryQuery += string(c)
	a.applyHistorySearch()
}

func (a *App) PopHistorySearch() {
	runes := []rune(a.HistoryQuery)
	if len(runes) > 0 {
		a.HistoryQuery = string(runes[:len(runes)-1])
	}
	a.applyHistorySearch()
}

// applyHistorySearch greps the archive and keeps only the runs that matched.
//
// This is the "when did this start failing" question — the one thing you could not ask
// before runs were stored, and the reason the archive exists at all.
func (a *App) applyHistorySearch() {
	a.HistoryHits = map[string]int{}
	a.reloadHistory()
	if a.HistoryQuery == "" {
		a.HistoryCursor = 0
		return
	}
	query, err := search.NewQuery(a.HistoryQuery)
	if err != nil {
		// Half-typed regex: leave the list alone rather than emptying it.
		return
	}
	results, _ := search.InStore(a.stateDir, query, 200)
	for _, r := range results {
		a.HistoryHits[r.Manifest.ID] = len(r.Hits)
	}
	kept := a.History[:0]
	for _, m := range a.History {
		if _, ok := a.HistoryHits[m.ID]; ok {
			kept = append(kept, m)
		}
	}
	a.History = kept
	a.HistoryCursor = 0
}

func (a *App) ToggleHistoryScope() {
	a.HistoryAllProjects = !a.HistoryAllProjects
	keep := ""
	if a.HistoryCursor < len(a.History) {
		keep = a.History[a.HistoryCursor].ID
	}
	a.reloadHistory()
	// Stay on the same run across the widening, as the pivot does in the picker.
	a.HistoryCursor = 0
	for i, m := range a.History {
		if m.ID == keep {
			a.HistoryCursor = i
			break
		}
	}
	a.Status = ""
}

func (a *App) SetFilterContext(delta int) {
	next := clamp(a.FilterContext+delta, 0, 20)
	if next == a.FilterContext {
		return
	}
	a.FilterContext = next
	if a.FilterMatches {
		a.RebuildRunRows()
		a.jumpToHit()
	}
	a.Status = fmt.Sprintf("%d lines of context", a.FilterContext)
}

func (a *App) HistoryMoveCursor(delta int) {
	if len(a.History) == 0 {
		return
	}
	a.HistoryCursor = clamp(a.HistoryCursor+delta, 0, len(a.History)-1)
}

// OpenStoredRun reopens the run under the cursor. It lands in the same run view a live run
// uses — same folding, same search — because it is the same structure, just read off disk.
func (a *App) OpenStoredRun() {
	if a.HistoryCursor >= len(a.History) {
		return
	}
	manifest := a.History[a.HistoryCursor]
	r, err := store.Load(a.stateDir, manifest)
	if err != nil {
		a.Status = fmt.Sprintf("could not read that run: %v", err)
		return
	}
	seq, why := a.claimStoredSlot()
	if why != "" {
		a.Status = why
		return
	}
	a.retire(a.Run)
	a.Run = r
	a.FocusSeq = seq
	a.Screen = ScreenRun
	a.RunCursor = 0
	a.RunOffset = 0
	a.runFolds = map[string]Fold{}
	a.Following = false
	a.focusedFailure = ""
	a.SavedTo = store.RunDir(a.stateDir, manifest.ID)
	a.ClearSearch()
	a.Status = ""
	// Open the failure straight away: reopening a run is nearly always about the thing
	// that broke.
	if name, ok := a.firstFailure(); ok {
		a.expandTo(name)
		a.RebuildRunRows()
		a.cursorToTask(name)
	} else {
		a.RebuildRunRows()
	}

	// Arriving from a cross-run search: land with the same query applied, so the run opens
	// on the thing you were looking for rather than making you retype it.
	if a.HistoryQuery != "" {
		a.SearchInput = a.HistoryQuery
		a.FilterMatches = true
		a.ApplySearch()
	}
}

// firstFailure is the deepest failed task — the one that actually broke, rather than an
// aggregate that merely contains it.
func (a *App) firstFailure() (string, bool) {
	if a.Run == nil {
		return "", false
	}
	for _, n := range a.Run.Order {
		t, ok := a.Run.Tasks[n]
		if ok && t.Status == run.Failed && len(a.Run.Graph.Children(n)) == 0 {
			return n, true
		}
	}
	return "", false
}

// PollRun drains every slot's capture goroutine and refreshes the run view. It returns
// true if anything moved.
//
// Parked runs are drained too, and for the same reason they were parked rather than
// killed: a background run that is not being read is still a run, and letting its queue
// back up would lose the output you came back for.
func (a *App) PollRun() bool {
	moved := false
	var finished []int
	for i := range a.Parked {
		if a.Parked[i].Run.Poll() {
			moved = true
			if a.Parked[i].Run.Finished() && a.Parked[i].view.savedTo == "" {
				finished = append(finished, i)
			}
		}
	}
	// Reverse, so an earlier index is not invalidated by a later removal — nothing is
	// removed here today, but this is one edit away from being wrong if that changes.
	for _, f := range slices.Backward(finished) {
		a.saveParked(f)
	}

	if a.Run == nil {
		return moved
	}
	if !a.Run.Poll() {
		return moved
	}
	a.follow()
	a.refreshSearch()
	a.RebuildRunRows()
	a.saveIfFinished()
	return true
}

// saveParked archives a background run the moment it ends, and says so — otherwise the
// only way to find out your build finished is to go and look at it.
func (a *App) saveParked(i int) {
	if i >= len(a.Parked) {
		return
	}
	parked := a.Parked[i]
	name := parked.Run.Root
	ok := parked.Run.Exit == 0
	path, err := store.Save(a.stateDir, a.Root, parked.Run)
	if err != nil {
		a.Status = fmt.Sprintf("could not save `task %s`: %v", name, err)
		return
	}
	a.Parked[i].view.savedTo = path
	a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
	mark := "✗"
	if ok {
		mark = "✓"
	}
	a.Status = fmt.Sprintf("%s `task %s` finished in the background", mark, name)
}

// saveIfFinished persists the run once, the moment it ends.
func (a *App) saveIfFinished() {
	if a.SavedTo != "" || a.Run == nil || !a.Run.Finished() {
		return
	}
	path, err := store.Save(a.stateDir, a.Root, a.Run)
	if err != nil {
		a.Status = fmt.Sprintf("could not save this run: %v", err)
		return
	}
	masked := a.Run.RedactedSecrets
	a.SavedTo = path
	// The picker's ✓/✗ column is only useful if it is current.
	a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
	switch masked {
	case 0:
		a.Status = "saved — no dotenv values found to mask"
	case 1:
		a.Status = "saved — 1 secret masked"
	default:
		a.Status = fmt.Sprintf("saved — %d secrets masked", masked)
	}
}

// refreshSearch re-runs the current query against the buffers, which have grown since last
// time.
func (a *App) refreshSearch() {
	if a.Search == nil || a.Run == nil {
		return
	}
	a.SearchHits = search.InRun(a.Run, a.Search)
	if a.SearchIdx >= len(a.SearchHits) {
		a.SearchIdx = max(0, len(a.SearchHits)-1)
	}
}

// ApplySearch compiles what has been typed and jumps to the first hit.
func (a *App) ApplySearch() {
	if a.SearchInput == "" {
		// Keep the prompt and its mode; only the compiled query goes away, so backspacing
		// to empty shows the whole run again rather than dropping you out.
		wasFiltering := a.FilterMatches
		stillTyping := a.Searching
		a.ClearSearch()
		a.Searching = stillTyping
		a.FilterMatches = wasFiltering && stillTyping
		a.RebuildRunRows()
		return
	}
	q, err := search.NewQuery(a.SearchInput)
	if err != nil {
		// A half-typed regex is the normal state during incremental search, so this is
		// reported quietly rather than treated as a failure.
		a.SearchError = firstLine(err.Error())
		a.Search = nil
		a.SearchHits = nil
		return
	}
	a.SearchError = ""
	a.Search = q
	a.SearchIdx = 0
	a.Following = false
	a.refreshSearch()
	a.RebuildRunRows()
	a.jumpToHit()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "bad pattern"
	}
	return s
}

func (a *App) ClearSearch() {
	a.Search = nil
	a.Searching = false
	a.SearchInput = ""
	a.SearchHits = nil
	a.SearchIdx = 0
	a.SearchError = ""
	a.FilterMatches = false
	a.RebuildRunRows()
}

func (a *App) SearchStep(delta int) {
	if len(a.SearchHits) == 0 {
		return
	}
	n := len(a.SearchHits)
	a.SearchIdx = ((a.SearchIdx+delta)%n + n) % n
	a.jumpToHit()
}

// jumpToHit puts the cursor on the current hit, opening whatever fold hides it.
func (a *App) jumpToHit() {
	if a.SearchIdx >= len(a.SearchHits) {
		return
	}
	hit := a.SearchHits[a.SearchIdx]
	a.expandTo(hit.Task)
	a.RebuildRunRows()
	for i, r := range a.RunRows {
		if !r.IsTask && r.Task == hit.Task && r.Index == hit.Index {
			a.RunCursor = i
			return
		}
	}
}

// ToggleFilterMatches turns the filtered view on and off.
//
// `f` with a query already running toggles it; `f` with nothing running opens the prompt
// already filtering, so typing narrows the run live instead of making you search first and
// convert afterwards.
func (a *App) ToggleFilterMatches() {
	if a.Search == nil {
		a.BeginFilter()
		return
	}
	a.FilterMatches = !a.FilterMatches
	a.Following = false
	a.RebuildRunRows()
	a.jumpToHit()
}

// BeginFilter opens the prompt in filter mode.
func (a *App) BeginFilter() {
	a.Searching = true
	a.FilterMatches = true
	a.SearchInput = ""
	a.SearchError = ""
	a.Status = ""
}

func (a *App) PushSearch(c rune) {
	a.SearchInput += string(c)
	a.ApplySearch()
}

func (a *App) PopSearch() {
	runes := []rune(a.SearchInput)
	if len(runes) > 0 {
		a.SearchInput = string(runes[:len(runes)-1])
	}
	a.ApplySearch()
}

// follow keeps the view pointed at the interesting thing: whatever is running, or — once
// something breaks — the task that broke.
func (a *App) follow() {
	if a.Run == nil {
		return
	}

	// A failure wins over following, and pins the view there.
	if name, ok := a.firstFailure(); ok {
		if a.focusedFailure != name {
			a.focusedFailure = name
			a.Following = false
			// Whatever was merely running is no longer the point.
			a.releaseFollowed(name)
			a.expandTo(name)
			a.RebuildRunRows()
			a.cursorToTask(name)
		}
		return
	}

	if !a.Following {
		return
	}
	// Following moves the view, not the fold.
	//
	// It used to open the running task in full, from back when folded meant empty and a
	// peek did not exist. Now that every task rests on a window of its own last few lines,
	// expanding one of them undoes the thing the window is for: a single task with a
	// thousand lines of output becomes the whole screen, and the twenty-five peeks around
	// it — the point of watching a run rather than tailing a log — are pushed off it.
	// Peeks tail on their own, so the running task is already showing its latest lines
	// wherever it sits.
	for _, name := range slices.Backward(a.Run.Order) {
		t, ok := a.Run.Tasks[name]
		if !ok || t.Status != run.Running {
			continue
		}
		// Hand back anything an earlier follow did open, so a run that started before this
		// behaviour changed does not leave a full task behind it.
		a.releaseFollowed("")
		a.RebuildRunRows()
		a.cursorToTask(name)
		return
	}
}

// releaseFollowed gives back the task following opened, unless it is keep.
//
// Only the one we opened, and only while it is still how we left it. A task the user
// opened is theirs; closing it because something else started running would be the tool
// arguing with the person using it.
func (a *App) releaseFollowed(keep string) {
	if a.followedOpen == "" || a.followedOpen == keep {
		return
	}
	previous := a.followedOpen
	if a.FoldOf(previous) == FoldFull {
		a.runFolds[previous] = FoldPeek
	}
	a.followedOpen = ""
}

// expandTo opens one task's output all the way, for following and for failures.
//
// Only that task. This used to walk up the graph opening every ancestor too, which was the
// only way to see anything back when folded meant empty — a parent showing nothing gave no
// clue that the thing you cared about was underneath it. Every task now peeks by default,
// so the chain already speaks for itself, and opening four ancestors in full to land on
// one leaf just buries the leaf again.
func (a *App) expandTo(name string) {
	a.runFolds[name] = FoldFull
}

func (a *App) cursorToTask(name string) {
	for i, r := range a.RunRows {
		if r.IsTask && r.Name == name {
			a.RunCursor = i
			return
		}
	}
}

type lineKey struct {
	task  string
	index int
}

func (a *App) RebuildRunRows() {
	if a.Run == nil {
		a.RunRows = nil
		return
	}
	r := a.Run

	// What the cursor is on, so that rebuilding does not slide the view out from under it.
	// RunCursor is an index into a list that changes shape on every poll: a task higher up
	// the tree printing one more line shifts every row below it, and reading a stack trace
	// in the last task of a live run meant watching it creep past the cursor a line at a
	// time. The row is found again by identity below.
	anchored := false
	var anchorTask string
	anchorLine := -1
	anchorOffset := 0
	if a.RunCursor < len(a.RunRows) {
		row := a.RunRows[a.RunCursor]
		anchored = true
		if row.IsTask {
			anchorTask = row.Name
		} else {
			anchorTask = row.Task
			anchorLine = row.Index
			// How far below its own task's row this line sat. Kept as well as the line
			// index because a peek window slides as output arrives — index 40 drops out of
			// a five-line window that has moved on, while "the third row under this task"
			// still means something.
			for back := a.RunCursor - 1; back >= 0; back-- {
				if a.RunRows[back].IsTask {
					anchorOffset = a.RunCursor - back
					break
				}
			}
		}
	}

	// In filter mode the whole run collapses to matching lines under the tasks that
	// produced them: a hit is not useful if you cannot see which task said it.
	filtering := a.FilterMatches && a.Search != nil
	// Each hit drags its neighbours in with it: `--- FAIL: TestOrderTotal` on its own
	// hides `order_test.go:88: want 1200, got 1180`, which is the useful half.
	hitLines := map[lineKey]bool{}
	hitTasks := map[string]bool{}
	if filtering {
		for _, h := range a.SearchHits {
			n := 0
			if t, ok := r.Tasks[h.Task]; ok {
				n = len(t.Lines)
			}
			lo := max(0, h.Index-a.FilterContext)
			hi := min(h.Index+a.FilterContext+1, n)
			for i := lo; i < hi; i++ {
				hitLines[lineKey{h.Task, i}] = true
				hitTasks[h.Task] = true
			}
		}
	}

	var rows []RunRow
	seen := map[string]bool{}
	type frame struct {
		name  string
		depth int
	}
	// Pushed in reverse so siblings come out in invocation order.
	stack := []frame{{r.Root, 0}}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// A diamond — `app:css` reached from both `check` and `build` — is shown at its
		// first position rather than duplicated.
		if seen[top.name] {
			continue
		}
		seen[top.name] = true

		children := r.Graph.Children(top.name)

		if filtering && !hitTasks[top.name] {
			// Skip the row but keep walking: a parent with no hits of its own may still
			// contain a child that has them.
			for _, c := range slices.Backward(children) {
				stack = append(stack, frame{c, top.depth})
			}
			continue
		}

		// Filtering answers the question the fold state usually answers — you asked for
		// these lines by searching for them — so it opens everything it keeps.
		fold := a.FoldOf(top.name)
		if filtering {
			fold = FoldFull
		}
		rows = append(rows, RunRow{IsTask: true, Name: top.name, Depth: top.depth, Fold: fold})

		if fold != FoldHidden {
			if t, ok := r.Tasks[top.name]; ok {
				// A peek is a window on the end of the buffer, not the start: a task that
				// is still going has its news at the bottom, and one that failed put the
				// reason there.
				from := 0
				if fold == FoldPeek {
					from = max(0, len(t.Lines)-a.PeekLines)
				}
				for i := from; i < len(t.Lines); i++ {
					if filtering && !hitLines[lineKey{top.name, i}] {
						continue
					}
					rows = append(rows, RunRow{
						Task:  top.name,
						Index: i,
						Depth: top.depth + 1,
						Peek:  fold == FoldPeek,
					})
				}
			}
		}
		for _, c := range slices.Backward(children) {
			stack = append(stack, frame{c, top.depth + 1})
		}
	}

	a.RunRows = rows
	switch {
	case anchored:
		a.RunCursor = a.locate(anchorTask, anchorLine, anchorOffset)
	case a.RunCursor >= len(a.RunRows):
		a.RunCursor = max(0, len(a.RunRows)-1)
	}
}

// locate finds where the row the cursor was on has ended up.
//
// Exact line first; failing that, the task's own row plus however far down it the cursor
// sat. The fallback is what keeps a cursor inside a peek window from jumping to the task
// header every time the window slides — and it stops at this task's last line rather than
// walking into the next task's row.
func (a *App) locate(taskName string, line, offset int) int {
	last := max(0, len(a.RunRows)-1)
	if line >= 0 {
		for i, r := range a.RunRows {
			if !r.IsTask && r.Task == taskName && r.Index == line {
				return i
			}
		}
	}
	head := -1
	for i, r := range a.RunRows {
		if r.IsTask && r.Name == taskName {
			head = i
			break
		}
	}
	if head < 0 {
		// The task itself is gone — filtered out, or a different run is in the slot.
		return min(a.RunCursor, last)
	}
	own := 0
	for i := head + 1; i < len(a.RunRows); i++ {
		if a.RunRows[i].IsTask || a.RunRows[i].Task != taskName {
			break
		}
		own++
	}
	return min(head+min(offset, own), last)
}

func (a *App) RunMoveCursor(delta int) {
	if len(a.RunRows) == 0 {
		return
	}
	// Any deliberate move means the user is reading, not watching.
	a.Following = false
	a.RunCursor = clamp(a.RunCursor+delta, 0, len(a.RunRows)-1)
}

// FoldOf is how much of this task's output is showing.
func (a *App) FoldOf(name string) Fold {
	return a.runFolds[name]
}

// RunToggleFold is `o` on a task: hidden, peek, full, round again.
func (a *App) RunToggleFold() {
	if a.RunCursor >= len(a.RunRows) || !a.RunRows[a.RunCursor].IsTask {
		return
	}
	name := a.RunRows[a.RunCursor].Name
	a.Following = false
	// Touching a task's fold takes it over: following must not hand back something you
	// have since decided to keep open.
	if a.followedOpen == name {
		a.followedOpen = ""
	}
	a.runFolds[name] = a.FoldOf(name).Next()
	a.RebuildRunRows()
	a.cursorToTask(name)
}

// OpenRunForTest opens a ready-made run in a fresh slot, parking whatever was there. The
// real parking path, so a test that uses it is testing the same code a keypress would.
func (a *App) OpenRunForTest(r *run.Run) {
	a.parkFocused()
	a.nextSeq++
	a.FocusSeq = a.nextSeq
	a.Run = r
	a.runFolds = map[string]Fold{}
	a.RunCursor = 0
	a.RebuildRunRows()
}

// RunSetFold forces a task's fold state, for tests.
func (a *App) RunSetFold(name string, fold Fold) {
	a.runFolds[name] = fold
	a.RebuildRunRows()
}

// RunIsExpanded reports whether a task is showing all of its output.
func (a *App) RunIsExpanded(name string) bool { return a.FoldOf(name) == FoldFull }

// RunExpand opens a task all the way.
func (a *App) RunExpand(name string) { a.RunSetFold(name, FoldFull) }

// Follow is the test-visible entry point to the follow logic.
func (a *App) Follow() { a.follow() }

// RunToggleFoldAll is `⇧O` in the run view: move every task to the same state at once.
//
// Opening everything is how you read a run end to end; closing it is how you get back to
// the shape of what ran once you have. Mixed states go to full first, because mixed almost
// always means "I opened two of these and now want the rest" — and from there it is the
// same cycle a single `o` walks. On a long run this moves the cursor a very long way, so
// it stays pinned to the task you were on.
func (a *App) RunToggleFoldAll() {
	if a.Run == nil {
		return
	}
	here, hadHere := a.RunSelectedTask()
	names := a.Run.TaskNames()
	every := func(f Fold) bool {
		for _, k := range names {
			if a.FoldOf(k) != f {
				return false
			}
		}
		return true
	}
	next := FoldFull
	switch {
	case every(FoldFull):
		next = FoldHidden
	case every(FoldHidden):
		next = FoldPeek
	}
	a.runFolds = map[string]Fold{}
	for _, k := range names {
		a.runFolds[k] = next
	}
	a.followedOpen = ""
	a.Following = false
	a.RebuildRunRows()
	if hadHere {
		a.cursorToTask(here)
	}
}

func (a *App) ToggleForce() {
	a.ForceNext = !a.ForceNext
	if a.ForceNext {
		a.Status = "force: the next run ignores go-task's up-to-date checks"
	} else {
		a.Status = "force off"
	}
}

func (a *App) ToggleInteractive() {
	a.InteractiveNext = !a.InteractiveNext
	if a.InteractiveNext {
		a.Status = "interactive: the next run can be typed at, but output is attributed by command"
	} else {
		a.Status = "interactive off"
	}
}

func (a *App) BeginInput() {
	if !a.RunInFlight() {
		a.Status = "nothing is running to type at"
		return
	}
	a.SendingInput = true
	a.Following = true
	a.Status = ""
}

func (a *App) EndInput() { a.SendingInput = false }

// SendInput forwards keystrokes to the task's terminal.
func (a *App) SendInput(bytes []byte) {
	ok := a.Run != nil && a.Run.SendInput(bytes)
	if !ok {
		// Silence after a keystroke is ambiguous enough already; a write that failed must
		// not look the same as one that landed.
		a.Status = "that keystroke went nowhere — the task has finished or closed its input"
		a.SendingInput = false
	}
}

// PossiblyStuck reports a non-interactive run that has gone quiet. Under `--output
// prefixed` a task blocked on a prompt produces nothing, so silence is the only clue there
// is.
func (a *App) PossiblyStuck() bool {
	return a.Run != nil &&
		!a.Run.Finished() &&
		!a.Run.Interactive &&
		len(a.Run.Graph.Edges) > 0 &&
		a.Run.SilentFor() > 15*time.Second
}

// AwaitingInput reports whether the task is sitting on an unanswered question.
func (a *App) AwaitingInput() bool {
	return a.Run != nil && !a.Run.Finished() && a.Run.LooksLikeAPrompt()
}

// CancelRun stops the run on screen, escalating if it has already been asked once.
func (a *App) CancelRun() {
	if a.Run == nil {
		return
	}
	a.Status = stopRun(a.Run)
}

// CancelTask stops the run in this task's slot, wherever that slot is.
//
// The point of addressing a run by task name is that it reaches the ones you are not
// looking at: from the picker there is no run "on screen" at all, and switching to a slot
// merely to stop it means loading a 20,000-line buffer to press one key.
func (a *App) CancelTask(name string) {
	if r := a.slotRun(name); r != nil {
		a.Status = stopRun(r)
		return
	}
	a.Status = fmt.Sprintf("`%s` is not running", name)
}

// RequestStopAll stops every live slot without leaving. It asks first — this reaches runs
// that are not on screen, and a stack you deliberately left up is exactly what it would
// take down.
func (a *App) RequestStopAll() {
	live := a.InFlightCount()
	if live == 0 {
		a.Status = "nothing is running"
		return
	}
	a.Confirm = &Confirm{Kind: ConfirmStopAll, Live: live}
}

// StopAll stops every live slot, reporting what was reached.
func (a *App) StopAll() {
	live := a.InFlightCount()
	a.CancelAll()
	switch live {
	case 0:
		a.Status = "nothing is running"
	case 1:
		a.Status = "stopping 1 run"
	default:
		a.Status = fmt.Sprintf("stopping %d runs", live)
	}
}

// RunInFlight is true while the run on screen is still going.
func (a *App) RunInFlight() bool { return a.Run != nil && !a.Run.Finished() }

// AnyInFlight is true while any slot still has a child out there. Quitting without dealing
// with them would leave containers running with nothing watching them.
func (a *App) AnyInFlight() bool {
	if a.RunInFlight() {
		return true
	}
	for _, p := range a.Parked {
		if !p.Run.Finished() {
			return true
		}
	}
	return false
}

// InFlightCount is how many slots are still going, for the quit prompt.
func (a *App) InFlightCount() int {
	n := 0
	if a.RunInFlight() {
		n++
	}
	for _, p := range a.Parked {
		if !p.Run.Finished() {
			n++
		}
	}
	return n
}

// CancelAll stops every slot. Used on the way out.
func (a *App) CancelAll() {
	if a.Run != nil {
		a.Run.Cancel()
	}
	for _, p := range a.Parked {
		p.Run.Cancel()
	}
}

// KillAll SIGKILLs every slot still standing. Only used on the way out, once SIGTERM has
// been sent and given time to work: a process that ignored it is about to be orphaned, and
// an orphaned container is worse than a skipped cleanup handler.
func (a *App) KillAll() {
	if a.Run != nil {
		a.Run.Kill()
	}
	for _, p := range a.Parked {
		p.Run.Kill()
	}
}

// RerunSelected re-runs the task under the cursor, keeping the args the run was started
// with.
func (a *App) RerunSelected() { a.rerunSelectedWith(false) }

// ForceRerunSelected is `⇧R`: the same re-run, but with `--force`.
//
// The tight loop when you are fixing one broken step is `r`, and `r` inherits the original
// run's flags — so a task go-task considers up to date declines to run again and you get a
// green tick that proves nothing. Reaching for the picker's `F` means leaving the output
// you are working against. This is the same key with the checks off, which is what you
// wanted the second time you pressed `r`.
func (a *App) ForceRerunSelected() { a.rerunSelectedWith(true) }

// force is an override, not a setting: false still inherits whatever the run used, so
// plain `r` keeps re-running a forced run forced.
func (a *App) rerunSelectedWith(force bool) {
	name, ok := a.RunSelectedTask()
	if !ok {
		return
	}
	var args []string
	// Only the root was invoked with these args; a child was not.
	if a.Run != nil && a.Run.Root == name {
		args = a.Run.Args
	}
	// Re-run it the way it was run: non-interactively it would hang again, and without
	// `--force` a cached task would simply decline.
	if a.Run != nil {
		a.InteractiveNext = a.Run.Interactive
		a.ForceNext = force || a.Run.Force
	}
	// Re-running a task whose slot is still live means restarting it, and a restart kills
	// what is in there. On a stack you deliberately left up that is worth a yes — and it
	// is the only way to bounce one without stopping it by hand first.
	if a.liveSlot(name) {
		a.Confirm = &Confirm{Kind: ConfirmRun, Name: name, Args: args, Reason: WouldStopRunning}
		return
	}
	a.RequestRun(name, args)
}

// RunSelectedTask is the task under the cursor, whether the cursor is on it or on one of
// its lines.
func (a *App) RunSelectedTask() (string, bool) {
	if a.RunCursor >= len(a.RunRows) {
		return "", false
	}
	row := a.RunRows[a.RunCursor]
	if row.IsTask {
		return row.Name, true
	}
	return row.Task, true
}

func (a *App) foldSet() map[string]bool {
	label := a.Mode.Label()
	set, ok := a.expanded[label]
	if !ok {
		set = map[string]bool{}
		a.expanded[label] = set
	}
	return set
}

// visible reports which tasks pass the current filter.
func (a *App) visible() []int {
	if a.Query == "" {
		out := make([]int, len(a.Tasks))
		for i := range a.Tasks {
			out[i] = i
		}
		return out
	}
	return a.matchingTasks(a.Query)
}

// matchingTasks lists tasks matching query, fuzzily, over the full colon path and the
// aliases — so `blint` finds `backend:lint` and `t` finds the `test` alias.
//
// Shared by the filter and the jump so the two can never disagree about what counts as a
// match. Results keep tree order rather than score order — the tree is the organisation,
// and resorting it by score would destroy the grouping the user is looking at.
func (a *App) matchingTasks(query string) []int {
	haystack := make([]string, 0, len(a.Tasks))
	owner := make([]int, 0, len(a.Tasks))
	for i, t := range a.Tasks {
		haystack = append(haystack, t.Name)
		owner = append(owner, i)
		for _, alias := range t.Aliases {
			haystack = append(haystack, alias)
			owner = append(owner, i)
		}
	}

	// Smart case, as nucleo and fzf do it: a lowercase query matches loosely, and any
	// uppercase in the query makes the whole thing case-sensitive. The library only offers
	// the case-insensitive half, so the exact pass is layered on top of it.
	exact := hasUpper(query)
	matched := map[int]bool{}
	for _, m := range fuzzy.FindNoSort(query, haystack) {
		if exact && !subsequence(query, haystack[m.Index]) {
			continue
		}
		matched[owner[m.Index]] = true
	}

	var out []int
	for i := range a.Tasks {
		if matched[i] {
			out = append(out, i)
		}
	}
	return out
}

func hasUpper(s string) bool {
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			return true
		}
	}
	return false
}

// subsequence is the case-sensitive half of smart case.
func subsequence(pattern, target string) bool {
	p := []rune(pattern)
	if len(p) == 0 {
		return true
	}
	at := 0
	for _, c := range target {
		if c == p[at] {
			at++
			if at == len(p) {
				return true
			}
		}
	}
	return false
}

// Rebuild rebuilds the tree and the flattened rows.
//
// keep is a task index to stay parked on across the rebuild — the property that makes
// toggling a pivot feel like a pivot rather than a navigation reset. Its ancestors get
// opened so it is actually on screen afterwards. Pass -1 for none.
func (a *App) Rebuild(keep int) {
	visible := a.visible()
	a.Tree = pivot.Build(a.Mode, a.Tasks, visible)

	if keep >= 0 {
		if ancestors, ok := a.Tree.AncestorsOfTask(keep); ok {
			set := a.foldSet()
			for _, k := range ancestors {
				set[k] = true
			}
		}
	}

	filtering := a.Query != ""
	set := a.foldSet()
	// While filtering, every group is open: a hit hidden behind a fold is a hit you did
	// not find.
	a.Rows = a.Tree.Flatten(func(key string) bool { return filtering || set[key] })

	if keep >= 0 {
		for i, r := range a.Rows {
			if a.Tree.Nodes[r.Node].Task == keep {
				a.Cursor = i
				return
			}
		}
	}
	a.Cursor = min(a.Cursor, max(0, len(a.Rows)-1))
}

// SelectedTask is the task under the cursor, or pivot.NoTask.
func (a *App) SelectedTask() int {
	if a.Cursor >= len(a.Rows) {
		return pivot.NoTask
	}
	return a.Tree.Nodes[a.Rows[a.Cursor].Node].Task
}

// SelectedNode is the tree node under the cursor.
func (a *App) SelectedNode() *pivot.Node {
	if a.Cursor >= len(a.Rows) {
		return nil
	}
	return &a.Tree.Nodes[a.Rows[a.Cursor].Node]
}

func (a *App) ToggleMode() {
	keep := a.SelectedTask()
	a.Mode = a.Mode.Toggled()
	a.Rebuild(keep)
}

func (a *App) ToggleFold() {
	node := a.SelectedNode()
	if node == nil || !node.IsGroup() {
		return
	}
	key := node.Key
	set := a.foldSet()
	if set[key] {
		delete(set, key)
	} else {
		set[key] = true
	}
	a.Rebuild(-1)
	// Stay parked on the group itself, so collapsing does not leave the cursor adrift
	// wherever the row that used to be at this index ended up.
	for i, r := range a.Rows {
		if a.Tree.Nodes[r.Node].Key == key {
			a.Cursor = i
			return
		}
	}
}

// ToggleFoldAll is `O` in the picker: close everything, or — if it is already all closed —
// open it.
//
// A toggle rather than a pair of keys because the two states are each other's only useful
// destination: you collapse to see the shape of the tree, then expand to get back to work.
func (a *App) ToggleFoldAll() {
	// Decided from the fold set rather than from what is on screen. Collapsing keeps the
	// path to the cursor open so the selection is not lost, which means a row is always
	// open afterwards — an "is anything open?" test therefore answers yes forever and the
	// toggle only ever works once.
	var groups []string
	for _, n := range a.Tree.Nodes {
		if n.IsGroup() {
			groups = append(groups, n.Key)
		}
	}
	open := a.expanded[a.Mode.Label()]
	allOpen := len(groups) > 0
	for _, g := range groups {
		if !open[g] {
			allOpen = false
			break
		}
	}
	a.SetFoldAll(!allOpen)
}

func (a *App) SetFoldAll(open bool) {
	keep := a.SelectedTask()
	var groups []string
	for _, n := range a.Tree.Nodes {
		if n.IsGroup() {
			groups = append(groups, n.Key)
		}
	}
	set := a.foldSet()
	for k := range set {
		delete(set, k)
	}
	if open {
		for _, k := range groups {
			set[k] = true
		}
	}
	a.Rebuild(keep)
}

func (a *App) MoveCursor(delta int) {
	if len(a.Rows) == 0 {
		return
	}
	a.Cursor = clamp(a.Cursor+delta, 0, len(a.Rows)-1)
}

func (a *App) PushQuery(c rune) {
	keep := a.SelectedTask()
	a.Query += string(c)
	a.Rebuild(keep)
}

func (a *App) PopQuery() {
	keep := a.SelectedTask()
	runes := []rune(a.Query)
	if len(runes) > 0 {
		a.Query = string(runes[:len(runes)-1])
	}
	a.Rebuild(keep)
}

func (a *App) ClearQuery() {
	keep := a.SelectedTask()
	a.Query = ""
	a.Filtering = false
	a.Rebuild(keep)
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
