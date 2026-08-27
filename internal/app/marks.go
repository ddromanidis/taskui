package app

import (
	"fmt"
	"sort"
	"strings"
)

// ToggleMark marks or unmarks the task under the cursor.
//
// Slots already hold several runs at once, and the whole point of them is that a run
// outlives your attention — but getting three going meant three trips back through the
// picker, remembering which two you had already started. Marking is the missing half:
// choose the set, then start it.
func (a *App) ToggleMark() {
	ti := a.SelectedTask()
	if ti < 0 {
		a.Status = "`" + a.selectedLabel() + "` groups tasks but is not one — space folds it"
		return
	}
	name := a.Tasks[ti].Name
	if a.marked == nil {
		a.marked = map[string]bool{}
	}
	if a.marked[name] {
		delete(a.marked, name)
	} else {
		a.marked[name] = true
	}
	a.Status = a.markSummary()
}

func (a *App) selectedLabel() string {
	if n := a.SelectedNode(); n != nil {
		return n.Label
	}
	return ""
}

// Marked is the set, in the order the picker lists them — which is the order they were
// chosen in as far as anyone is concerned, and stable between frames either way.
func (a *App) Marked() []string {
	out := make([]string, 0, len(a.marked))
	for name := range a.marked {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (a *App) IsMarked(name string) bool { return a.marked[name] }

func (a *App) ClearMarks() {
	if len(a.marked) == 0 {
		return
	}
	a.marked = nil
	a.Status = "marks cleared"
}

func (a *App) markSummary() string {
	n := len(a.marked)
	if n == 0 {
		return "no tasks marked"
	}
	return fmt.Sprintf("%d marked — ⏎ runs them, ⇧M clears", n)
}

// RunMarked starts every marked task, each in its own slot.
//
// Capped at the slots there are, and it says what it left behind rather than silently
// starting the first six: a batch that quietly did less than you asked is worse than one
// that refused.
func (a *App) RunMarked() {
	names := a.Marked()
	if len(names) == 0 {
		return
	}

	room := MaxSlots - a.openSlots()
	for _, name := range names {
		// A task already in a slot does not need one.
		if a.liveSlot(name) {
			room++
		}
	}
	if room <= 0 {
		a.Status = fmt.Sprintf("every slot is taken — ⇧X closes one (%d marked)", len(names))
		return
	}

	// Anything on the danger list turns the whole batch into one question. Asking per task
	// would put a modal prompt between each pair of starts, which is not a confirmation, it
	// is an obstacle course.
	if a.Confirm == nil {
		var dangerous []string
		for _, name := range names {
			for _, t := range a.Tasks {
				if t.Name == name && t.Dangerous {
					dangerous = append(dangerous, name)
				}
			}
		}
		if len(dangerous) > 0 {
			a.Confirm = &Confirm{
				Kind:   ConfirmRunMarked,
				Name:   strings.Join(dangerous, ", "),
				Reason: TouchesProduction,
				Live:   len(names),
			}
			return
		}
	}
	a.Confirm = nil
	a.startMarked(names, room)
}

// startMarked is the part that actually runs things, past every question.
func (a *App) startMarked(names []string, room int) {
	started, skipped := 0, 0
	for _, name := range names {
		if a.liveSlot(name) {
			continue
		}
		if started >= room {
			skipped++
			continue
		}
		if err := a.StartRunWith(name, nil); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", name, err)
			return
		}
		started++
	}

	a.marked = nil
	switch {
	case started == 0:
		a.Status = "those are all running already"
	case skipped > 0:
		a.Status = fmt.Sprintf("started %d — %d left unstarted, no slots free", started, skipped)
	default:
		a.Status = fmt.Sprintf("started %d %s", started, plural(started, "task", "tasks"))
	}
}
