package app

import (
	"fmt"
	"strings"

	"github.com/ddromanidis/taskui/internal/diff"
	"github.com/ddromanidis/taskui/internal/store"
)

// DiffRow is one row of a rendered diff: an edit, plus whether it is the elision marker
// between hunks.
type DiffRow struct {
	Op   diff.Op
	Text string
	// Old and New are line numbers on their own side, 0 where the line is not on that side.
	Old, New int
	Gap      bool
}

// OpenTimeline shows how one task has been going, run after run.
//
// The archive has always held this and could not be asked for it: `--search` greps for a
// string across every run, and the history list is every run in order. Neither of those is
// "how has `test` been doing", which is the question you actually have when something that
// used to work does not.
func (a *App) OpenTimeline(task string) {
	if task == "" {
		a.Status = "nothing to chart here — space folds it"
		return
	}
	// Always this project, whatever the history list is scoped to. That toggle is made on
	// another screen and is not shown here, and a series that mixes two codebases is not a
	// series — the same task name in a different repo is a different task.
	a.TimelineOf = task
	a.Timeline = store.Timeline(a.stateDir, a.Root, task)
	// Both outcomes at one commit. Worth saying here above all places: this screen is where
	// you come to decide whether a failure means something, and "it also passed at this
	// exact revision" is the answer that stops you looking for a cause in the code.
	a.TimelineFlakes = nil
	for _, f := range store.Flaky(a.stateDir, a.Root) {
		if f.Task == task {
			a.TimelineFlakes = append(a.TimelineFlakes, f)
		}
	}
	a.TimelineCursor = 0
	a.TimelineOffset = 0
	a.timelineReturn = a.Screen
	a.Screen = ScreenTimeline
	a.Status = ""
	if len(a.Timeline) == 0 {
		a.Status = "`" + task + "` has not run since taskui started keeping runs"
	}
}

func (a *App) CloseTimeline() {
	a.Screen = a.timelineReturn
	if a.Screen == ScreenTimeline || a.Screen == ScreenDiff {
		a.Screen = ScreenPicker
	}
	a.Status = ""
}

func (a *App) TimelineMoveCursor(delta int) {
	if len(a.Timeline) == 0 {
		return
	}
	a.TimelineCursor = clamp(a.TimelineCursor+delta, 0, len(a.Timeline)-1)
}

// SelectedPoint is the timeline row under the cursor.
func (a *App) SelectedPoint() (store.Point, bool) {
	if a.TimelineCursor >= len(a.Timeline) {
		return store.Point{}, false
	}
	return a.Timeline[a.TimelineCursor], true
}

// OpenTimelineRun reopens the stored run the cursor is on, in the run view.
func (a *App) OpenTimelineRun() {
	point, ok := a.SelectedPoint()
	if !ok {
		return
	}
	for i, m := range a.History {
		if m.ID == point.RunID {
			a.HistoryCursor = i
			a.OpenStoredRun()
			return
		}
	}
	// The history list is loaded lazily and may not have been opened yet, or may be scoped
	// to the other project. Reload it against this point rather than telling the user to go
	// and open a different screen first.
	a.reloadHistory()
	for i, m := range a.History {
		if m.ID == point.RunID {
			a.HistoryCursor = i
			a.OpenStoredRun()
			return
		}
	}
	a.Status = "that run is no longer in the archive"
}

// DiffAgainstLastGreen is `D` from the run view: what changed in this task since it last
// passed.
//
// Last *green* rather than last run, because "it worked before" is the comparison that
// isolates the failure — diffing two consecutive failures usually shows only that the
// timestamps moved. When the task has never passed there is nothing green to compare
// against, and the previous run is the honest second choice rather than an error.
func (a *App) DiffAgainstLastGreen() {
	name, ok := a.RunSelectedTask()
	if !ok {
		a.Status = "nothing here to compare"
		return
	}
	if a.Run == nil {
		return
	}
	newer := make([]string, 0, 64)
	if t, found := a.Run.Tasks[name]; found {
		for _, l := range t.Lines {
			newer = append(newer, l.Plain)
		}
	}
	if len(newer) == 0 {
		a.Status = "`" + name + "` printed nothing in this run"
		return
	}

	project := a.Root
	skip := a.Run.StoredID()
	point, ok := store.LastGreen(a.stateDir, project, name, skip)
	against := "when it last passed"
	if !ok {
		point, ok = store.Previous(a.stateDir, project, name, skip)
		against = "the run before"
		if !ok {
			a.Status = "no earlier run of `" + name + "` to compare against"
			return
		}
		if !point.Ok() {
			against = "the run before — which failed too"
		}
	}
	a.showDiff(name, store.Output(a.stateDir, point), newer, against, point)
}

// DiffTimelinePoint is `⇧D` from the timeline: what changed at the run under the cursor.
//
// Not the row immediately below it. The trend across the header is `✓✓✓✗✗`, and the question
// that shape puts in your head is what happened at the turn — so this looks back for the
// most recent earlier run that ended *differently* and compares against that. Against the
// adjacent row, a `⇧D` on the newest of five consecutive failures answers "nothing changed",
// which is true, useless, and lands exactly where the question was most worth asking.
//
// A run of unbroken same-outcome results all the way back has no turn to find, and then the
// row below is the honest answer.
func (a *App) DiffTimelinePoint() {
	point, ok := a.SelectedPoint()
	if !ok {
		return
	}
	if a.TimelineCursor+1 >= len(a.Timeline) {
		a.Status = "that is the earliest stored run of `" + a.TimelineOf + "` — nothing before it"
		return
	}

	before, against := a.Timeline[a.TimelineCursor+1], "the run before"
	for _, earlier := range a.Timeline[a.TimelineCursor+1:] {
		if earlier.Ok() != point.Ok() {
			before = earlier
			against = "when it last passed"
			if point.Ok() {
				against = "the last failure"
			}
			break
		}
	}

	a.showDiff(
		a.TimelineOf,
		store.Output(a.stateDir, before),
		store.Output(a.stateDir, point),
		against,
		before,
	)
}

// showDiff builds the diff view.
func (a *App) showDiff(task string, older, newer []string, against string, base store.Point) {
	edits := diff.Lines(older, newer)
	a.DiffOf = task
	a.DiffAgainst = base
	a.DiffAgainstWhat = against
	a.DiffStat = diff.Count(edits)
	a.diffEdits = edits
	a.DiffCursor = 0
	a.DiffOffset = 0
	a.diffReturn = a.Screen
	a.Screen = ScreenDiff
	a.rebuildDiffRows()
	a.Status = ""
	if a.DiffStat.Added == 0 && a.DiffStat.Removed == 0 {
		a.Status = "identical — `" + task + "` printed exactly the same thing both times"
	}
}

// rebuildDiffRows re-elides the shared stretches at the current context width.
func (a *App) rebuildDiffRows() {
	kept := diff.Hunks(a.diffEdits, a.DiffContext)
	a.DiffRows = make([]DiffRow, 0, len(kept))
	for _, e := range kept {
		a.DiffRows = append(a.DiffRows, DiffRow{
			Op: e.Op, Text: e.Text, Old: e.OldLine, New: e.NewLine, Gap: diff.IsGap(e),
		})
	}
	a.DiffCursor = clamp(a.DiffCursor, 0, max(0, len(a.DiffRows)-1))
}

func (a *App) DiffMoveCursor(delta int) {
	if len(a.DiffRows) == 0 {
		return
	}
	a.DiffCursor = clamp(a.DiffCursor+delta, 0, len(a.DiffRows)-1)
}

// SetDiffContext widens or narrows the unchanged lines kept around each change.
//
// Zero is allowed: a diff of nothing but the changes is the tightest possible answer, and
// on a log where every change is self-explanatory it is the right one.
func (a *App) SetDiffContext(delta int) {
	next := clamp(a.DiffContext+delta, 0, 20)
	if next == a.DiffContext {
		return
	}
	a.DiffContext = next
	rebuilt := a.DiffCursor
	a.rebuildDiffRows()
	a.DiffCursor = clamp(rebuilt, 0, max(0, len(a.DiffRows)-1))
	a.Status = fmt.Sprintf("%d %s of context", a.DiffContext, plural(a.DiffContext, "line", "lines"))
}

func (a *App) CloseDiff() {
	a.Screen = a.diffReturn
	if a.Screen == ScreenDiff {
		a.Screen = ScreenPicker
	}
	a.Status = ""
}

// TimelineTaskFor is the task `H` should chart, from wherever it was pressed.
func (a *App) TimelineTaskFor() string {
	switch a.Screen {
	case ScreenRun:
		if name, ok := a.RunSelectedTask(); ok {
			return name
		}
	case ScreenPicker:
		if ti := a.SelectedTask(); ti >= 0 {
			return a.Tasks[ti].Name
		}
	case ScreenDiff:
		return a.DiffOf
	case ScreenProfile:
		if cost, ok := a.SelectedCost(); ok {
			return cost.Name
		}
	case ScreenHistory, ScreenHelp, ScreenDetail, ScreenTimeline:
		// Nothing sensible to chart from these.
	}
	return ""
}

// DiffSummary is the one line that says whether opening this was worth it.
func (a *App) DiffSummary() string {
	var parts []string
	if a.DiffStat.Added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", a.DiffStat.Added))
	}
	if a.DiffStat.Removed > 0 {
		parts = append(parts, fmt.Sprintf("-%d", a.DiffStat.Removed))
	}
	if len(parts) == 0 {
		return "no change"
	}
	return strings.Join(parts, " ")
}
