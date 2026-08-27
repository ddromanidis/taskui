package app

import (
	"fmt"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

// Detach lets the focused run outlive taskui.
//
// The child is a session leader in its own process group, so it does not depend on taskui
// staying alive — verified rather than assumed: SIGKILL taskui and the run keeps going,
// reparented to init, still doing work. Writes to the pty whose master has closed return
// EIO and are ignored by every shell that matters. So "detached" needs to change exactly one
// thing: quitting stops signalling it.
//
// What it cannot do is keep showing you the output. Once taskui is gone the master is
// closed and everything the run prints after that is gone with it. That is why detaching
// archives what it has: the alternative is a two-hour run leaving nothing behind.
func (a *App) Detach() {
	if a.Run == nil {
		a.Status = "nothing here to detach"
		return
	}
	if a.Run.Finished() {
		a.Status = "`" + a.Run.Root + "` has already finished — nothing to let go of"
		return
	}
	if a.IsDetached(a.FocusSeq) {
		a.Status = "`" + a.Run.Root + "` is already detached — `x` still stops it"
		return
	}

	if a.detached == nil {
		a.detached = map[uint64]bool{}
	}
	a.detached[a.FocusSeq] = true

	// Archived now, not on quit: at quit taskui is on its way out, and a detached run has no
	// end to wait for — this is the last moment its output can be written down at all.
	// Unfinished, so `saveIfFinished` will not do it; store.Save is happy either way, and a
	// partial record beats none.
	kept := ""
	if a.SavedTo == "" {
		if dir, err := store.Save(a.stateDir, a.Root, a.Run); err == nil {
			a.SavedTo = dir
			a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
			kept = " — output so far is in the archive"
		}
	}

	a.Status = fmt.Sprintf("`%s` will keep running when you quit%s", a.Run.Root, kept)
}

// IsDetached says whether a slot has been let go of.
func (a *App) IsDetached(seq uint64) bool { return a.detached[seq] }

// DetachedCount is how many runs would survive quitting.
func (a *App) DetachedCount() int {
	n := 0
	for _, slot := range a.Slots() {
		if a.detached[slot.Seq] && !a.finishedSeq(slot.Seq) {
			n++
		}
	}
	return n
}

// finishedSeq reports whether the run in a slot has stopped. A detached run that ended on
// its own is not something quitting has to warn about.
func (a *App) finishedSeq(seq uint64) bool {
	if a.FocusSeq == seq && a.Run != nil {
		return a.Run.Finished()
	}
	for _, p := range a.Parked {
		if p.Seq == seq {
			return p.Run.Finished()
		}
	}
	return true
}

// attachedRuns is every live run that quitting is still responsible for.
func (a *App) attachedRuns() []attached {
	var out []attached
	if a.Run != nil && !a.Run.Finished() && !a.detached[a.FocusSeq] {
		out = append(out, attached{seq: a.FocusSeq, run: a.Run})
	}
	for _, p := range a.Parked {
		if !p.Run.Finished() && !a.detached[p.Seq] {
			out = append(out, attached{seq: p.Seq, run: p.Run})
		}
	}
	return out
}

// attached pairs a live run with the slot it sits in.
type attached struct {
	seq uint64
	run *run.Run
}
