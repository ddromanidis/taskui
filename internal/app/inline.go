package app

// The picker's inline runs.
//
// `⏎` used to leave the list: the run took the screen, and the tasks you were looking at
// went away. It no longer does. Everything the run view knows how to draw is drawn under
// the row the run was started from instead, so starting something costs you neither your
// place in the list nor sight of what you started — and with several slots open, several
// tasks are showing their output at once, which is a thing the run view cannot do at all.
// `v` is still the whole screen, for reading.
//
// The rows are the run view's own [RunRow]s, walked out of the graph by the same function,
// so the two views cannot disagree about what a run contains. The fold states are the run's
// own too, for the same reason: a task opened here is open there.

import (
	"strings"

	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
)

// PickerRow is one row of the picker list: a row of the task tree, or a row of a run
// unfolded under the task it was started from.
//
// One flat list rather than a tree row that draws its run inside itself, because a row is
// the unit the cursor moves by and the viewport scrolls by. Output that is not made of rows
// is output you cannot scroll to the end of — and "show me all of it" has to mean all of
// it, not the first screenful.
type PickerRow struct {
	// Tree indexes Rows on a tree row, and is -1 on a run row.
	Tree int
	// Root names the run a run row came from: the task it was started from, which is also
	// the slot it lives in.
	Root string
	// Run is the row itself, in the shape the run view uses.
	Run RunRow
	// Rail is everything drawn to the left of the row: the guide down from the task the run
	// hangs under, and the branch that says which task inside the run this belongs to. Built
	// once per rebuild rather than per frame, because it depends on what comes *after* a row
	// — whether a task has another sibling below it — and the renderer sees one row at a
	// time.
	Rail string
}

// IsRun reports whether the row belongs to a run rather than to the task tree.
func (r PickerRow) IsRun() bool { return r.Tree < 0 }

// slotFolds is the fold state of the slot rooted at name — the run on screen, or one parked
// behind it.
//
// The map handed back is the slot's own, so a task unfolded in the picker is unfolded when
// you switch to the run view. The fold belongs to the run, not to the screen you happened
// to be on when you set it. Nil when no slot holds that task.
func (a *App) slotFolds(name string) map[string]Fold {
	if a.Run != nil && a.Run.Root == name {
		if a.runFolds == nil {
			a.runFolds = map[string]Fold{}
		}
		return a.runFolds
	}
	for i := range a.Parked {
		if a.Parked[i].Run.Root == name {
			if a.Parked[i].view.folds == nil {
				a.Parked[i].view.folds = map[string]Fold{}
			}
			return a.Parked[i].view.folds
		}
	}
	return nil
}

// BlockFold is how much of a run the picker is showing under its task: hidden is nothing at
// all, peek is every task's last few lines, full is all of it.
//
// Read off the folds the run already has rather than stored beside them. A fourth piece of
// state would be one the run view could contradict — open a task over there, come back, and
// the block would claim to be closed while showing you output.
func (a *App) BlockFold(root string) Fold {
	r := a.slotRun(root)
	if r == nil {
		return FoldHidden
	}
	names := r.TaskNames()
	if len(names) == 0 {
		// Nothing in the graph yet — a run one tick old. Answering "hidden" here would
		// collapse a block that is about to have something in it.
		return FoldPeek
	}
	folds := a.slotFolds(root)
	every := func(f Fold) bool {
		for _, name := range names {
			if folds[name] != f {
				return false
			}
		}
		return true
	}
	switch {
	case every(FoldHidden):
		return FoldHidden
	case every(FoldFull):
		return FoldFull
	default:
		// Mixed reads as a peek: something is showing, and the next press should offer
		// more of it rather than less.
		return FoldPeek
	}
}

// CycleBlockFold is `o` on a task the picker is showing a run under: hidden, peek, full,
// round again — the whole run at once, which is the granularity the picker is for. Inside
// the block the same key still moves one task.
func (a *App) CycleBlockFold(root string) {
	r := a.slotRun(root)
	if r == nil {
		return
	}
	next := a.BlockFold(root).Next()
	folds := a.slotFolds(root)
	for _, name := range r.TaskNames() {
		folds[name] = next
	}
	a.afterFoldChange(root)
}

// CycleTaskFold is the same key on a row inside the block: one task of the run moves along
// the cycle and the rest stay as they are.
func (a *App) CycleTaskFold(root, task string) {
	folds := a.slotFolds(root)
	if folds == nil {
		return
	}
	folds[task] = folds[task].Next()
	a.afterFoldChange(root)
}

// afterFoldChange rebuilds whatever is showing the run whose folds just moved.
func (a *App) afterFoldChange(root string) {
	if a.Run != nil && a.Run.Root == root {
		// Following hands back a fold it opened itself; one you set is yours.
		a.followedOpen = ""
		a.RebuildRunRows()
	}
	a.RebuildPickerRows()
}

// RunUnder is the run the picker is showing under the row at index i, if any.
func (a *App) RunUnder(i int) (*run.Run, string, bool) {
	if i < 0 || i >= len(a.PickerRows) {
		return nil, "", false
	}
	row := a.PickerRows[i]
	if row.IsRun() {
		if r := a.slotRun(row.Root); r != nil {
			return r, row.Root, true
		}
		return nil, "", false
	}
	name, ok := a.taskNameOfTreeRow(row.Tree)
	if !ok {
		return nil, "", false
	}
	r := a.slotRun(name)
	return r, name, r != nil
}

// taskNameOfTreeRow is the task a tree row stands for, if it stands for one.
func (a *App) taskNameOfTreeRow(i int) (string, bool) {
	if i < 0 || i >= len(a.Rows) {
		return "", false
	}
	node := a.Tree.Nodes[a.Rows[i].Node]
	if node.Task == pivot.NoTask {
		return "", false
	}
	return a.Tasks[node.Task].Name, true
}

// RebuildPickerRows lays the tree and every open run out as one list.
//
// Called wherever either half can have changed — the tree on a rebuild, the runs on a poll
// — because the cursor indexes this list, and a stale one points at rows that have moved or
// gone.
func (a *App) RebuildPickerRows() {
	anchor := a.pickerAnchor()

	rows := make([]PickerRow, 0, len(a.Rows))
	for i, row := range a.Rows {
		rows = append(rows, PickerRow{Tree: i})

		name, ok := a.taskNameOfTreeRow(i)
		if !ok {
			continue
		}
		r := a.slotRun(name)
		if r == nil || a.BlockFold(name) == FoldHidden {
			continue
		}
		folds := a.slotFolds(name)
		inline := runRowsFor(r, func(task string) Fold { return folds[task] }, a.PeekLines, nil)
		if len(inline) == 0 {
			continue
		}
		// The run hangs off its task's row the way a wrapped description does, and follows
		// the same rule: a guide below a row promises a sibling, so the last child of a
		// group carries none. The rails are built over the whole block, root row included,
		// so a task knows whether another follows it.
		rails := guides(inline, a.Theme.Glyphs, a.blockRail(i, row))
		// The root's own row is the picker row the block hangs under. Drawing it again
		// would say the same name twice, one line apart.
		for at, rr := range inline[1:] {
			rows = append(rows, PickerRow{Tree: -1, Root: name, Run: rr, Rail: rails[at+1]})
		}
	}

	a.PickerRows = rows
	a.Cursor = a.locatePicker(anchor)
}

// blockRail is the guide the whole block hangs from: the tree indentation of the row it
// belongs to, then that row's own continuation — a vertical while the group has more rows
// below, nothing on the last one, where a vertical would promise a sibling that is not
// there. It is the rule a wrapped description already follows.
func (a *App) blockRail(tree int, row pivot.Row) string {
	g := a.Theme.Glyphs
	indent := strings.Repeat(g.GuideVertical+" ", max(0, row.Depth-1))
	cont := g.GuideVertical
	if a.lastOfParent(tree) {
		cont = " "
	}
	return indent + cont + " "
}

// anchor is the row the cursor was on, by identity rather than by index.
//
// Output arriving under a task above the cursor moves every row below it, and a list that
// slid a line at a time while you read it would be unusable — the same reason the run view
// anchors its own cursor.
type anchor struct {
	ok   bool
	tree int
	root string
	task string
	line int
}

func (a *App) pickerAnchor() anchor {
	if a.Cursor < 0 || a.Cursor >= len(a.PickerRows) {
		return anchor{}
	}
	row := a.PickerRows[a.Cursor]
	if !row.IsRun() {
		return anchor{ok: true, tree: row.Tree, line: -1}
	}
	out := anchor{ok: true, tree: -1, root: row.Root, line: -1}
	if row.Run.IsTask {
		out.task = row.Run.Name
	} else {
		out.task, out.line = row.Run.Task, row.Run.Index
	}
	return out
}

// locatePicker finds where the anchored row ended up: the same row if it is still there,
// its run's task row if the line has scrolled out of a peek window, and the task the block
// belongs to if the run itself has gone.
func (a *App) locatePicker(at anchor) int {
	last := max(0, len(a.PickerRows)-1)
	if !at.ok {
		return min(max(a.Cursor, 0), last)
	}
	if at.tree >= 0 {
		for i, row := range a.PickerRows {
			if row.Tree == at.tree {
				return i
			}
		}
		return min(a.Cursor, last)
	}

	head := -1
	for i, row := range a.PickerRows {
		if !row.IsRun() || row.Root != at.root {
			continue
		}
		switch {
		case at.line >= 0 && !row.Run.IsTask && row.Run.Task == at.task && row.Run.Index == at.line:
			return i
		case at.line < 0 && row.Run.IsTask && row.Run.Name == at.task:
			return i
		}
		if head < 0 && row.Run.IsTask && row.Run.Name == at.task {
			head = i
		}
	}
	if head >= 0 {
		return head
	}
	// The block is gone — folded away, or the slot was replaced. The task it hung under is
	// where the cursor came from, and the nearest thing to where it was.
	for i, row := range a.PickerRows {
		if row.IsRun() {
			continue
		}
		if name, ok := a.taskNameOfTreeRow(row.Tree); ok && name == at.root {
			return i
		}
	}
	return min(a.Cursor, last)
}

// cursorTreeRow is the tree row the cursor is on, or the one the block under the cursor
// hangs beneath. -1 when there is none, which is an empty list.
//
// It is what makes every picker key that acts on "the task under the cursor" keep working
// from inside a run: `⏎` on a line of output re-runs the task that printed it, exactly as
// `r` does in the run view.
func (a *App) cursorTreeRow() int {
	for i := min(a.Cursor, len(a.PickerRows)-1); i >= 0; i-- {
		if !a.PickerRows[i].IsRun() {
			return a.PickerRows[i].Tree
		}
	}
	return -1
}

// pickerIndexOfTree is where a tree row sits in the picker list.
func (a *App) pickerIndexOfTree(tree int) int {
	for i, row := range a.PickerRows {
		if row.Tree == tree {
			return i
		}
	}
	return 0
}

// InlineFold is the state of the block under a picker row, for the fold glyph beside it.
// The second return says whether there is a block there at all.
func (a *App) InlineFold(root string) (Fold, bool) {
	if a.slotRun(root) == nil {
		return FoldHidden, false
	}
	return a.BlockFold(root), true
}

// CursorInRun reports whether the cursor is on a row of a run rather than on a task.
func (a *App) CursorInRun() bool {
	return a.Cursor >= 0 && a.Cursor < len(a.PickerRows) && a.PickerRows[a.Cursor].IsRun()
}

// CycleOutputFold is the fold key aimed at a run: on a task it moves the whole block along
// the cycle, and on a row inside one it moves that one task. It reports whether there was a
// run to move — the caller falls back to folding the tree when there was not.
func (a *App) CycleOutputFold() bool {
	if a.Cursor < 0 || a.Cursor >= len(a.PickerRows) {
		return false
	}
	row := a.PickerRows[a.Cursor]
	if row.IsRun() {
		task := row.Run.Name
		if !row.Run.IsTask {
			task = row.Run.Task
		}
		a.CycleTaskFold(row.Root, task)
		return true
	}
	name, ok := a.taskNameOfTreeRow(row.Tree)
	if !ok || a.slotRun(name) == nil {
		return false
	}
	a.CycleBlockFold(name)
	return true
}
