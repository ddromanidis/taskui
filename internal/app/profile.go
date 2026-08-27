package app

import (
	"sort"
	"time"

	"github.com/ddromanidis/taskui/internal/run"
)

// Cost is one task's share of a run.
type Cost struct {
	Name     string
	Duration time.Duration
	Lines    int
	Status   run.Status
	// Self is Duration minus whatever its children accounted for. An aggregate that only
	// invokes other tasks has a large Duration and almost no Self, and reporting the first
	// as though it were the second would put `all` at the top of every profile saying
	// nothing.
	Self time.Duration
	// Children is how many tasks ran underneath it, for telling an aggregate from a leaf.
	Children int
}

// Profile is where a run's time went, slowest first.
//
// Every task already carries its own clock and the run view already shows it — beside the
// task, in tree order, which is the right place to answer "is this step slow" and the wrong
// one to answer "what makes this take four minutes". That second question is about the
// whole run at once, and it is a sort, not a walk.
//
// Self time is what makes it useful. A `task all` that invokes six things has a duration
// equal to the sum of theirs, so ranking by duration puts every aggregate above every task
// that did any work. Subtracting the children leaves the time a task spent on its own
// commands, which is the time that would actually go away if you made it faster.
func (a *App) Profile() []Cost {
	if a.Run == nil {
		return nil
	}
	out := make([]Cost, 0, len(a.Run.Tasks))
	for name, t := range a.Run.Tasks {
		d, ok := t.Elapsed()
		if !ok || t.Status == run.Skipped || t.Status == run.Pending {
			continue
		}
		children := a.Run.Graph.Children(name)
		self := d
		for _, child := range children {
			if c, ok := a.Run.Tasks[child]; ok {
				if cd, ok := c.Elapsed(); ok {
					self -= cd
				}
			}
		}
		// A parent that overlapped its children, or a clock that rounded the wrong way, can
		// take this below zero. Zero is the honest floor: it did not spend negative time.
		self = max(0, self)

		out = append(out, Cost{
			Name: name, Duration: d, Lines: len(t.Lines),
			Status: t.Status, Self: self, Children: len(children),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Self != out[j].Self {
			return out[i].Self > out[j].Self
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ProfileTotal is the run's own elapsed time — the denominator the shares are of.
//
// The run's clock rather than the sum of the tasks': under `--output prefixed` two tasks can
// overlap, so the sum can exceed the wall clock and shares computed against it would not
// add up to anything.
func (a *App) ProfileTotal() time.Duration {
	if a.Run == nil {
		return 0
	}
	return elapsedOf(a.Run)
}

// OpenProfile shows it.
func (a *App) OpenProfile() {
	if a.Run == nil {
		a.Status = "nothing running to profile"
		return
	}
	a.ProfileRows = a.Profile()
	a.ProfileCursor = 0
	a.ProfileOffset = 0
	a.profileReturn = a.Screen
	a.Screen = ScreenProfile
	a.Status = ""
	if len(a.ProfileRows) == 0 {
		a.Status = "nothing in this run has finished yet"
	}
}

// refreshProfile keeps a profile of a live run current.
//
// A profile that froze at the moment you pressed `z` would be showing a run that no longer
// exists — during a slow build, which is exactly when you would open it. It stops updating
// once the run does, so a finished profile holds still while you read it.
//
// The cursor follows its task rather than its index: the list is sorted by time, so rows
// overtake each other as the numbers move, and an index-holding cursor would drift onto
// whatever happened to slide underneath it.
func (a *App) refreshProfile() {
	if a.Screen != ScreenProfile || a.Run == nil || a.Run.Finished() {
		return
	}
	on := ""
	if cost, ok := a.SelectedCost(); ok {
		on = cost.Name
	}
	a.ProfileRows = a.Profile()
	for i, c := range a.ProfileRows {
		if c.Name == on {
			a.ProfileCursor = i
			return
		}
	}
	a.ProfileCursor = clamp(a.ProfileCursor, 0, max(0, len(a.ProfileRows)-1))
}

func (a *App) CloseProfile() {
	a.Screen = a.profileReturn
	if a.Screen == ScreenProfile {
		a.Screen = ScreenRun
	}
	a.Status = ""
}

func (a *App) ProfileMoveCursor(delta int) {
	if len(a.ProfileRows) == 0 {
		return
	}
	a.ProfileCursor = clamp(a.ProfileCursor+delta, 0, len(a.ProfileRows)-1)
}

// SelectedCost is the row under the cursor.
func (a *App) SelectedCost() (Cost, bool) {
	if a.ProfileCursor >= len(a.ProfileRows) {
		return Cost{}, false
	}
	return a.ProfileRows[a.ProfileCursor], true
}

// GotoProfiledTask leaves the profile for the task it names, in the run view.
func (a *App) GotoProfiledTask() {
	cost, ok := a.SelectedCost()
	if !ok {
		return
	}
	a.Screen = ScreenRun
	a.Following = false
	a.RunExpand(cost.Name)
	a.RebuildRunRows()
	a.cursorToTask(cost.Name)
	a.Status = ""
}

// RefreshLive brings whatever is on screen up to date with the run behind it. The Bubble
// Tea loop does this on every tick; the headless driver has its own loop and needs the same.
func (a *App) RefreshLive() { a.refreshProfile() }
