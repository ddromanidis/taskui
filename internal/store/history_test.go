package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveMany archives n runs of distinct tasks in one project, oldest first.
func saveMany(t *testing.T, base, project string, n int) {
	t.Helper()
	for i := range n {
		if _, err := Save(base, project, agedRun("t"+string(rune('a'+i)), "task", true, n-i)); err != nil {
			t.Fatal(err)
		}
	}
}

// The reason the ledger exists: one project's runs used to evict another's, because the cap
// counted every project's against the same fifty. A timeline with one point in it and a
// `--flaky` that never fires are what that cost.
func TestOneProjectsRunsDoNotEvictAnothersHistory(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/alpha", 3)
	// Enough to push alpha's runs past any plausible output cap.
	saveMany(t, base, "/beta", 4)
	if _, err := Prune(base, 2); err != nil {
		t.Fatal(err)
	}

	alpha := 0
	for _, m := range List(base) {
		if m.Dir == "/alpha" {
			alpha++
		}
	}
	if alpha != 3 {
		t.Errorf("alpha's three runs should survive beta's; %d left", alpha)
	}
}

// A timeline is built from verdicts and durations, which the ledger holds, so it keeps
// answering after the text is gone. This is the whole point of splitting the two.
func TestATimelineOutlivesTheOutput(t *testing.T) {
	base := t.TempDir()
	for i := range 4 {
		if _, err := Save(base, "/proj", agedRun("run"+string(rune('a'+i)), "test", i%2 == 0, 4-i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Prune(base, 1); err != nil {
		t.Fatal(err)
	}

	if got := len(Timeline(base, "/proj", "test")); got != 4 {
		t.Errorf("the timeline should still hold four points, got %d", got)
	}
}

// A diff needs the text, so it looks past runs that are only remembered. Comparing against a
// run whose lines are gone would report every line as deleted, which is worse than saying
// there is nothing to compare with.
func TestADiffSkipsRunsWhoseOutputIsGone(t *testing.T) {
	base := t.TempDir()
	if _, err := Save(base, "/proj", agedRun("old", "test", true, 9)); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(base, "/proj", agedRun("mid", "test", true, 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(base, "/proj", agedRun("new", "test", false, 1)); err != nil {
		t.Fatal(err)
	}
	// Keeps the newest two, so `old` is remembered without its text.
	if _, err := Prune(base, 2); err != nil {
		t.Fatal(err)
	}

	green, ok := LastGreen(base, "/proj", "test", "")
	if !ok {
		t.Fatal("there is still a green run with output")
	}
	if !HasOutput(base, green.RunID) {
		t.Errorf("%s was picked to diff against and has no output", green.RunID)
	}
	if green.Root != "mid" {
		t.Errorf("want the newest green run that still has text, got %q", green.Root)
	}
}

// Reopening a run whose output was pruned says so. Rebuilding it would produce a run with
// every task empty, which reads as "it printed nothing".
func TestLoadingAPrunedRunSaysTheOutputIsGone(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/proj", 3)
	if _, err := Prune(base, 1); err != nil {
		t.Fatal(err)
	}

	all := List(base)
	oldest := all[len(all)-1]
	_, err := Load(base, oldest)
	if err == nil {
		t.Fatal("want an error for a run with no output left")
	}
	if !strings.Contains(err.Error(), "no longer stored") {
		t.Errorf("the message should say what happened, got %q", err)
	}
}

// An archive written before the ledger existed is absorbed by the next save rather than
// needing a migration step of its own.
func TestAnOlderArchiveIsAbsorbedOnTheNextSave(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/proj", 2)

	// What an archive from before the ledger looks like: run directories, no history file.
	if err := os.Remove(historyPath(base)); err != nil {
		t.Fatal(err)
	}
	if got := len(List(base)); got != 2 {
		t.Fatalf("the directories alone should still list, got %d", got)
	}

	if _, err := Save(base, "/proj", agedRun("third", "task", true, 1)); err != nil {
		t.Fatal(err)
	}
	if got := len(readHistory(base)); got != 3 {
		t.Errorf("the ledger should have absorbed the two older runs, holds %d", got)
	}
}

// Merged on id, so a run that is in both places is one run.
func TestARunInBothPlacesIsListedOnce(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/proj", 3)
	if got := len(List(base)); got != 3 {
		t.Errorf("listed %d", got)
	}
}

// The file is appended to by several processes at once, so a half-written last line is a
// thing that can happen. One unreadable run is not a reason to lose the others.
func TestATornLineDoesNotLoseTheLedger(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/proj", 3)

	blob, err := os.ReadFile(historyPath(base))
	if err != nil {
		t.Fatal(err)
	}
	torn := string(blob) + `{"id":"tor`
	if err := os.WriteFile(historyPath(base), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := len(readHistory(base)); got != 3 {
		t.Errorf("want the three good lines, got %d", got)
	}
}

// Compaction keeps the newest of each project rather than the newest overall, which is the
// same mistake the global cap made.
func TestCompactionKeepsEachProjectsNewest(t *testing.T) {
	base := t.TempDir()
	var b strings.Builder
	write := func(id, dir string, started int64) {
		b.WriteString(`{"version":1,"id":"` + id + `","root":"r","dir":"` + dir +
			`","started_unix":` + itoa(started) + "}\n")
	}
	// More lines than compactAt, so compaction runs.
	for i := range compactAt + 10 {
		write("beta"+itoa(int64(i)), "/beta", int64(1000+i))
	}
	write("alpha-1", "/alpha", 1)
	if err := os.WriteFile(historyPath(base), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := compactHistory(base); err != nil {
		t.Fatal(err)
	}

	kept := readHistory(base)
	alpha, beta := 0, 0
	for _, m := range kept {
		switch m.Dir {
		case "/alpha":
			alpha++
		case "/beta":
			beta++
		}
	}
	if alpha != 1 {
		t.Errorf("alpha's single old run should survive beta's flood, got %d", alpha)
	}
	if beta != KeepHistory {
		t.Errorf("beta should be trimmed to %d, got %d", KeepHistory, beta)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// The ledger lives beside the runs rather than in a config directory: deleting the state
// directory has always been how you remove everything taskui accumulated.
func TestTheLedgerLivesBesideTheRuns(t *testing.T) {
	base := t.TempDir()
	saveMany(t, base, "/proj", 1)
	if _, err := os.Stat(filepath.Join(base, "history.ndjson")); err != nil {
		t.Errorf("want the ledger in the state directory: %v", err)
	}
}
