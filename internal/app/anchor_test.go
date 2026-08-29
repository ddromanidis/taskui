package app

import (
	"testing"

	"github.com/ddromanidis/taskui/internal/run"
)

// Following moves the view to what is running. What it must not do is move it
// off what you are reading — and a task you have opened and put the cursor
// inside is one you are reading.

// Reading a fully-open task while it is still printing: the cursor must stay on
// the line it is on.
func TestCursorStaysWhileAnExpandedTaskPrints(t *testing.T) {
	a := appWithRun(t, "test", run.Edge{Parent: "test"})
	for i := range 20 {
		a.Run.Feed("test", "line "+string(rune('a'+i)))
	}
	a.RunExpand("test")
	a.RebuildRunRows()

	// Park in the middle of the output.
	a.RunCursor = 10
	was := a.RunRows[a.RunCursor]
	t.Logf("parked on %+v following=%v", was, a.Following)

	for i := range 5 {
		a.Run.Feed("test", "more "+string(rune('a'+i)))
	}
	a.follow()
	a.RebuildRunRows()

	now := a.RunRows[a.RunCursor]
	t.Logf("now on %+v", now)
	if now.IsTask || now.Index != was.Index {
		t.Errorf("cursor moved from line %d to %+v", was.Index, now)
	}
}

// Following still does its job when the cursor is somewhere else: bringing what
// is running into view is the whole point of it.
func TestFollowingStillMovesToWhatIsRunning(t *testing.T) {
	a := appWithRun(t, "ci",
		run.Edge{Parent: "ci", Children: []string{"build", "test"}},
		run.Edge{Parent: "build"}, run.Edge{Parent: "test"})
	a.Run.Feed("build", "compiling")
	a.RebuildRunRows()
	a.RunCursor = 0 // on ci, the root
	a.Following = true

	a.Run.Feed("test", "running")
	a.follow()
	a.RebuildRunRows()

	row := a.RunRows[a.RunCursor]
	if !row.IsTask || row.Name != "test" {
		t.Errorf("cursor is on %+v, want the task that is running", row)
	}
}

// The same in the picker, where a run is unfolded under its task.
func TestPickerCursorStaysWhileAnExpandedTaskPrints(t *testing.T) {
	a := appWith(t, []string{"test"})
	a.SetFoldAll(true)
	a.Run = run.Detached("test", run.GraphFrom(run.Edge{Parent: "test"}))
	a.Screen = ScreenPicker
	for i := range 20 {
		a.Run.Feed("test", "line "+string(rune('a'+i)))
	}
	a.RunSetFold("test", FoldFull)
	a.RebuildPickerRows()

	for i, row := range a.PickerRows {
		if row.IsRun() && !row.Run.IsTask && row.Run.Index == 8 {
			a.Cursor = i
		}
	}
	was := a.PickerRows[a.Cursor]
	t.Logf("parked on %+v", was.Run)

	for i := range 5 {
		a.Run.Feed("test", "more "+string(rune('a'+i)))
	}
	a.RebuildPickerRows()

	now := a.PickerRows[a.Cursor]
	t.Logf("now on %+v", now.Run)
	if !now.IsRun() || now.Run.IsTask || now.Run.Index != was.Run.Index {
		t.Errorf("cursor moved from line %d to %+v", was.Run.Index, now)
	}
}
