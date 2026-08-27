package diff

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// render is the shape the assertions read in: `-` gone, `+` arrived, ` ` shared.
func render(edits []Edit) string {
	var b strings.Builder
	for _, e := range edits {
		switch e.Op {
		case Del:
			b.WriteString("-" + e.Text + "\n")
		case Ins:
			b.WriteString("+" + e.Text + "\n")
		case Same:
			if IsGap(e) {
				b.WriteString("...\n")
			} else {
				b.WriteString(" " + e.Text + "\n")
			}
		}
	}
	return b.String()
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestTheOrdinaryCases(t *testing.T) {
	for _, c := range []struct {
		what     string
		old, new string
		want     string
	}{
		{"identical", "a\nb\nc", "a\nb\nc", " a\n b\n c\n"},
		{"one changed", "a\nb\nc", "a\nX\nc", " a\n-b\n+X\n c\n"},
		{"one added", "a\nc", "a\nb\nc", " a\n+b\n c\n"},
		{"one removed", "a\nb\nc", "a\nc", " a\n-b\n c\n"},
		{"empty old", "", "a\nb", "+a\n+b\n"},
		{"empty new", "a\nb", "", "-a\n-b\n"},
		{"both empty", "", "", ""},
		{"appended", "a", "a\nb\nc", " a\n+b\n+c\n"},
		{"truncated", "a\nb\nc", "a", " a\n-b\n-c\n"},
		{"nothing shared", "a\nb", "x\ny", "-a\n-b\n+x\n+y\n"},
	} {
		t.Run(c.what, func(t *testing.T) {
			got := render(Lines(lines(c.old), lines(c.new)))
			if got != c.want {
				t.Errorf("got:\n%swant:\n%s", got, c.want)
			}
		})
	}
}

// The failing-test case this whole feature is for: same log, one assertion flipped.
func TestATestOutputThatStartedFailing(t *testing.T) {
	before := lines("=== RUN   TestA\n--- PASS: TestA\n=== RUN   TestB\n--- PASS: TestB\nPASS\nok  \tpkg\t0.1s")
	after := lines(
		"=== RUN   TestA\n--- PASS: TestA\n=== RUN   TestB\n    b_test.go:9: want 3, got 4\n--- FAIL: TestB\nFAIL\nFAIL\tpkg\t0.1s",
	)
	got := render(Lines(before, after))
	if !strings.Contains(got, "+    b_test.go:9: want 3, got 4") {
		t.Errorf("the new assertion should be an addition:\n%s", got)
	}
	if !strings.Contains(got, " === RUN   TestB") {
		t.Errorf("the shared lines should be shared:\n%s", got)
	}
	if strings.Contains(got, "-=== RUN   TestA") {
		t.Errorf("the untouched head should not appear as a change:\n%s", got)
	}
}

// The line numbers are what make a hunk findable in the real output. A deletion exists only
// on the old side and an insertion only on the new, and claiming otherwise points at the
// wrong line.
func TestLineNumbersReferToTheirOwnSide(t *testing.T) {
	edits := Lines(lines("a\nb\nc"), lines("a\nX\nY\nc"))
	for _, e := range edits {
		switch {
		case e.Op == Del && e.NewLine != 0:
			t.Errorf("deletion %q claims new line %d", e.Text, e.NewLine)
		case e.Op == Ins && e.OldLine != 0:
			t.Errorf("insertion %q claims old line %d", e.Text, e.OldLine)
		case e.Op == Same && (e.OldLine == 0 || e.NewLine == 0):
			t.Errorf("shared line %q is missing a number", e.Text)
		}
	}
	// `c` is line 3 in the old and line 4 in the new.
	last := edits[len(edits)-1]
	if last.Text != "c" || last.OldLine != 3 || last.NewLine != 4 {
		t.Errorf("last edit is %q at old=%d new=%d", last.Text, last.OldLine, last.NewLine)
	}
}

// The trimmed prefix has to keep its numbering too, or every line number after a shared
// head is off by the length of that head.
func TestNumbersSurviveTheTrimmedPrefix(t *testing.T) {
	edits := Lines(lines("a\nb\nc\nd"), lines("a\nb\nX\nd"))
	for _, e := range edits {
		if e.Text == "X" && e.NewLine != 3 {
			t.Errorf("X is at new line %d, want 3", e.NewLine)
		}
		if e.Text == "c" && e.OldLine != 3 {
			t.Errorf("c is at old line %d, want 3", e.OldLine)
		}
		if e.Text == "d" && (e.OldLine != 4 || e.NewLine != 4) {
			t.Errorf("d is at old=%d new=%d, want 4 and 4", e.OldLine, e.NewLine)
		}
	}
}

// Whatever the alignment, applying the deletions to the old sequence must yield the new one.
// This is the property that makes a diff a diff rather than a plausible-looking list.
func TestTheEditsReconstructBothSides(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for range 300 {
		older := randomLines(r, r.Intn(40))
		newer := mutate(r, older)
		edits := Lines(older, newer)

		var gotOld, gotNew []string
		for _, e := range edits {
			switch e.Op {
			case Same:
				gotOld = append(gotOld, e.Text)
				gotNew = append(gotNew, e.Text)
			case Del:
				gotOld = append(gotOld, e.Text)
			case Ins:
				gotNew = append(gotNew, e.Text)
			}
		}
		if strings.Join(gotOld, "\n") != strings.Join(older, "\n") {
			t.Fatalf("old side does not reconstruct\n got %q\nwant %q", gotOld, older)
		}
		if strings.Join(gotNew, "\n") != strings.Join(newer, "\n") {
			t.Fatalf("new side does not reconstruct\n got %q\nwant %q", gotNew, newer)
		}
	}
}

// A diff that keeps every shared line is a correct diff and a useless one — the value is
// that it is short.
func TestHunksDropTheSharedStretches(t *testing.T) {
	var older, newer []string
	for i := range 100 {
		older = append(older, "line "+strconv.Itoa(i))
		newer = append(newer, "line "+strconv.Itoa(i))
	}
	newer[50] = "CHANGED"

	full := Lines(older, newer)
	if len(full) < 100 {
		t.Fatalf("expected the full diff to be long, got %d", len(full))
	}
	hunks := Hunks(full, 2)
	// Two context lines either side, the delete, the insert, and one gap marker on each end.
	if len(hunks) > 12 {
		t.Errorf("hunks kept %d rows, expected a handful:\n%s", len(hunks), render(hunks))
	}
	got := render(hunks)
	if !strings.Contains(got, "+CHANGED") || !strings.Contains(got, "-line 50") {
		t.Errorf("the change itself is missing:\n%s", got)
	}
	if !strings.Contains(got, " line 48") || !strings.Contains(got, " line 52") {
		t.Errorf("the context is missing:\n%s", got)
	}
	if !strings.HasPrefix(got, "...") {
		t.Errorf("the elided head should be marked:\n%s", got)
	}
}

// A gap marker at the end promises more that is not there.
func TestHunksDoNotEndInAGap(t *testing.T) {
	older := append([]string{"CHANGED"}, manyLines(50)...)
	newer := append([]string{"OTHER"}, manyLines(50)...)
	hunks := Hunks(Lines(older, newer), 1)
	if len(hunks) > 0 && IsGap(hunks[len(hunks)-1]) {
		t.Errorf("trailing gap:\n%s", render(hunks))
	}
}

func TestHunksOfAnIdenticalPairAreEmpty(t *testing.T) {
	same := manyLines(20)
	if got := Hunks(Lines(same, same), 3); len(got) != 0 {
		t.Errorf("got %d rows for identical input:\n%s", len(got), render(got))
	}
}

func TestCount(t *testing.T) {
	s := Count(Lines(lines("a\nb\nc"), lines("a\nX\nY\nc")))
	if s.Removed != 1 || s.Added != 2 || s.Same != 2 {
		t.Errorf("got %+v, want 1 removed, 2 added, 2 same", s)
	}
}

// Past the cap there is no useful alignment to find, and the fallback must still be a
// truthful account of both sides rather than a crash or a hang.
func TestTwoUnrelatedLogsFallBackRatherThanGrind(t *testing.T) {
	var older, newer []string
	for i := range maxD + 500 {
		older = append(older, "old "+strconv.Itoa(i))
		newer = append(newer, "new "+strconv.Itoa(i))
	}
	edits := Lines(older, newer)
	s := Count(edits)
	if s.Removed != len(older) || s.Added != len(newer) {
		t.Errorf("got %+v, want everything on both sides accounted for", s)
	}
}

func TestALargeButSimilarPairIsStillFast(t *testing.T) {
	older := manyLines(20000)
	newer := manyLines(20000)
	newer[10000] = "CHANGED"
	s := Count(Lines(older, newer))
	if s.Added != 1 || s.Removed != 1 {
		t.Errorf("got %+v, want one line each way", s)
	}
}

func manyLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "line " + strconv.Itoa(i)
	}
	return out
}

func randomLines(r *rand.Rand, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("l%d", r.Intn(12))
	}
	return out
}

// mutate makes a plausible next run out of a previous one: a few lines changed, dropped or
// inserted, which is what a diff between two runs of the same task actually looks like.
func mutate(r *rand.Rand, in []string) []string {
	out := make([]string, 0, len(in)+4)
	for _, l := range in {
		switch r.Intn(10) {
		case 0: // dropped
		case 1:
			out = append(out, fmt.Sprintf("m%d", r.Intn(12)))
		case 2:
			out = append(out, fmt.Sprintf("n%d", r.Intn(12)), l)
		default:
			out = append(out, l)
		}
	}
	return out
}
