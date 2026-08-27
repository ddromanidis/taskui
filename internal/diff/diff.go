// Package diff compares two runs of the same task, line by line.
//
// The question it answers is the one an archive makes possible and nothing else can: this
// task failed, it passed yesterday, and the useful thing is not the eight hundred lines it
// printed but the five that are new. Reading a whole log to find out what changed in it is
// the work the archive exists to remove.
package diff

// Op is what happened to one line.
type Op int

const (
	// Same is a line both runs printed. Kept, because a diff with no context around it is a
	// diff you cannot place.
	Same Op = iota
	// Del is a line only the older run printed.
	Del
	// Ins is a line only the newer run printed.
	Ins
)

// Edit is one line of the result.
type Edit struct {
	Op   Op
	Text string
	// OldLine and NewLine are 1-based line numbers in their own side, or 0 where the line
	// does not exist on that side. They are what makes a hunk locatable in the real output.
	OldLine, NewLine int
}

// maxD caps the edit distance Myers will search for.
//
// The algorithm is O(ND): fast when the two runs are similar, which is the case worth being
// fast for, and quadratic when they share nothing. Two entirely different logs have no
// useful alignment to find anyway, so past this point the honest answer is "all of that
// went, all of this arrived" — which is what Lines falls back to.
const maxD = 3000

// Lines diffs two sequences of output lines.
//
// Common prefix and suffix are stripped first. That is not just an optimisation: build logs
// are mostly identical run to run, so the interesting middle is usually a few dozen lines
// out of thousands, and trimming turns the expensive case into the cheap one.
func Lines(older, newer []string) []Edit {
	// Trim the head.
	head := 0
	for head < len(older) && head < len(newer) && older[head] == newer[head] {
		head++
	}
	// …and the tail, without letting the two halves overlap on a short input.
	tail := 0
	for tail < len(older)-head && tail < len(newer)-head &&
		older[len(older)-1-tail] == newer[len(newer)-1-tail] {
		tail++
	}

	oldMid := older[head : len(older)-tail]
	newMid := newer[head : len(newer)-tail]

	out := make([]Edit, 0, len(older)+len(newer))
	for i := range head {
		out = append(out, Edit{Op: Same, Text: older[i], OldLine: i + 1, NewLine: i + 1})
	}
	out = append(out, middle(oldMid, newMid, head)...)
	for i := range tail {
		o := len(older) - tail + i
		n := len(newer) - tail + i
		out = append(out, Edit{Op: Same, Text: older[o], OldLine: o + 1, NewLine: n + 1})
	}
	return out
}

// middle diffs the part that actually differs. offset is how many identical lines were
// trimmed off the front, so the line numbers still refer to the real output.
func middle(older, newer []string, offset int) []Edit {
	switch {
	case len(older) == 0 && len(newer) == 0:
		return nil
	case len(older) == 0:
		return allOf(Ins, newer, offset)
	case len(newer) == 0:
		return allOf(Del, older, offset)
	}
	trace, ok := myers(older, newer)
	if !ok {
		// Too far apart to align usefully. Say so by showing both in full rather than
		// producing a plausible-looking alignment of two unrelated logs.
		return append(allOf(Del, older, offset), allOf(Ins, newer, offset)...)
	}
	return backtrack(trace, older, newer, offset)
}

func allOf(op Op, lines []string, offset int) []Edit {
	out := make([]Edit, 0, len(lines))
	for i, text := range lines {
		e := Edit{Op: op, Text: text}
		if op == Del {
			e.OldLine = offset + i + 1
		} else {
			e.NewLine = offset + i + 1
		}
		out = append(out, e)
	}
	return out
}

// myers walks the edit graph one diagonal at a time, keeping a copy of the frontier after
// each step. The frontier history is the trace, and backtracking through it is what turns
// "the distance is 7" into the seven edits themselves.
func myers(older, newer []string) ([][]int, bool) {
	n, m := len(older), len(newer)
	limit := min(n+m, maxD)
	// v is indexed by diagonal k ∈ [-limit, limit], shifted into a slice.
	offset := limit
	v := make([]int, 2*limit+1)
	trace := make([][]int, 0, limit+1)

	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			// Move down when the diagonal below is further along, otherwise right. The
			// bounds checks are what keep the frontier inside the graph.
			switch {
			case k == -d:
				x = v[k+1+offset]
			case k == d:
				x = v[k-1+offset] + 1
			case v[k-1+offset] < v[k+1+offset]:
				x = v[k+1+offset]
			default:
				x = v[k-1+offset] + 1
			}
			y := x - k
			// Slide down the diagonal for as long as the lines agree — the snake.
			for x < n && y < m && older[x] == newer[y] {
				x++
				y++
			}
			v[k+offset] = x
			if x >= n && y >= m {
				trace = append(trace, append([]int(nil), v...))
				return trace, true
			}
		}
	}
	return nil, false
}

// backtrack reads the trace backwards, from the end of both inputs to the start, and turns
// each step into the edit that produced it. The result is built in reverse and flipped,
// because walking it forwards would mean guessing which step came next.
func backtrack(trace [][]int, older, newer []string, offset int) []Edit {
	n, m := len(older), len(newer)
	limit := (len(trace[0]) - 1) / 2
	x, y := n, m
	var rev []Edit

	for d := len(trace) - 1; d > 0 && (x > 0 || y > 0); d-- {
		v := trace[d-1]
		k := x - y
		// Which neighbour we came from is the same decision myers made going forwards.
		var prevK int
		switch {
		case k == -d+1:
			prevK = k + 1
		case k == d-1:
			prevK = k - 1
		case v[k-1+limit] < v[k+1+limit]:
			prevK = k + 1
		default:
			prevK = k - 1
		}
		if prevK < -limit || prevK > limit {
			break
		}
		prevX := v[prevK+limit]
		prevY := prevX - prevK

		// Everything above the neighbour on this diagonal is a snake: lines both sides have.
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Edit{
				Op: Same, Text: older[x],
				OldLine: offset + x + 1, NewLine: offset + y + 1,
			})
		}
		switch {
		case x > prevX:
			x--
			rev = append(rev, Edit{Op: Del, Text: older[x], OldLine: offset + x + 1})
		case y > prevY:
			y--
			rev = append(rev, Edit{Op: Ins, Text: newer[y], NewLine: offset + y + 1})
		}
	}
	// d == 0 leaves whatever prefix the first diagonal slid over.
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Edit{
			Op: Same, Text: older[x],
			OldLine: offset + x + 1, NewLine: offset + y + 1,
		})
	}

	out := make([]Edit, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out
}

// Stat counts what changed, for the one-line summary that says whether opening the diff is
// worth it at all.
type Stat struct{ Added, Removed, Same int }

func Count(edits []Edit) Stat {
	var s Stat
	for _, e := range edits {
		switch e.Op {
		case Ins:
			s.Added++
		case Del:
			s.Removed++
		case Same:
			s.Same++
		}
	}
	return s
}

// Hunks drops the long stretches both runs share, keeping `context` lines either side of
// every change.
//
// A diff of two 800-line logs that differ in five places is 800 rows of which 790 are
// noise; the whole value of the view is that it is short. Returned as one flat list with
// gaps marked, because the alternative — a slice of slices — makes the renderer do the
// flattening anyway.
func Hunks(edits []Edit, context int) []Edit {
	if context < 0 {
		context = 0
	}
	keep := make([]bool, len(edits))
	for i, e := range edits {
		if e.Op == Same {
			continue
		}
		for j := max(0, i-context); j <= min(len(edits)-1, i+context); j++ {
			keep[j] = true
		}
	}

	out := make([]Edit, 0, len(edits))
	skipping := false
	for i, e := range edits {
		if keep[i] {
			skipping = false
			out = append(out, e)
			continue
		}
		if !skipping {
			skipping = true
			out = append(out, Edit{Op: Same, Text: "", OldLine: 0, NewLine: 0})
		}
	}
	// A gap marker at the very end is a promise of more that is not there.
	if len(out) > 0 && out[len(out)-1].isGap() {
		out = out[:len(out)-1]
	}
	return out
}

// IsGap reports the elision marker Hunks leaves where it dropped shared lines.
func IsGap(e Edit) bool { return e.isGap() }

func (e Edit) isGap() bool { return e.Op == Same && e.Text == "" && e.OldLine == 0 && e.NewLine == 0 }
