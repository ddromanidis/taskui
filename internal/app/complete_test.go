package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/pivot"
	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
	"github.com/ddromanidis/taskui/internal/task"
)

// seedArgs archives one finished run of a task, invoked with these arguments.
func seedArgs(t *testing.T, a *App, name string, args []string) {
	t.Helper()
	r := run.Detached(name, run.GraphFrom(run.Edge{Parent: name}))
	r.Args = args
	r.Finish(0)
	if _, err := store.Save(a.stateDir, a.Root, r); err != nil {
		t.Fatal(err)
	}
}

// promptFor opens the args prompt on a task without the `--summary` the real one spends:
// these tests are about what ⇥ offers, not about where the declarations come from.
func promptFor(t *testing.T, a *App, target string) *App {
	t.Helper()
	a.EnteringArgs = true
	a.ArgsTarget = target
	// The archive is walked lazily; saying it has been read keeps the tests off the disk
	// unless they put something there themselves.
	a.argsPastRead = true
	return a
}

func typeArgs(a *App, input string) {
	a.ArgsInput = input
	a.ArgsCursor = len([]rune(input))
	a.argsComp = nil
}

// The variables a task declares come first: they are the one source that is a statement of
// fact rather than a guess.
func TestTabCompletesTheVariablesATaskAsksFor(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	a.argsVars = []string{"REGION", "ENV"}
	typeArgs(a, "")

	press(a, Tab())
	if a.ArgsInput != "ENV=" {
		t.Errorf("first candidate = %q, want ENV=", a.ArgsInput)
	}
	if a.ArgsCursor != len("ENV=") {
		t.Errorf("cursor = %d, want it after the =", a.ArgsCursor)
	}
	press(a, Tab())
	if a.ArgsInput != "REGION=" {
		t.Errorf("second candidate = %q, want REGION=", a.ArgsInput)
	}
	// ⇧⇥ walks back up the same list, and the list wraps.
	press(a, Key{kind: keyBackTab})
	if a.ArgsInput != "ENV=" {
		t.Errorf("⇧⇥ = %q, want ENV= again", a.ArgsInput)
	}

	// A prefix narrows it, the same way typing more does anywhere else.
	typeArgs(a, "RE")
	press(a, Tab())
	if a.ArgsInput != "REGION=" {
		t.Errorf("completing RE = %q", a.ArgsInput)
	}
}

// Past the `=` only the value is in question, and the values this task has actually been
// run with are the best answers there are.
func TestTabCompletesAValueFromPastRuns(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	a.argsPast = [][]string{{"ENV=staging"}, {"ENV=prod"}, {"REGION=eu"}}
	typeArgs(a, "ENV=")

	press(a, Tab())
	if a.ArgsInput != "ENV=staging" {
		t.Errorf("first value = %q, want ENV=staging", a.ArgsInput)
	}
	press(a, Tab())
	if a.ArgsInput != "ENV=prod" {
		t.Errorf("second value = %q, want ENV=prod", a.ArgsInput)
	}
	// Another variable's values are not this variable's.
	press(a, Tab())
	if a.ArgsInput != "ENV=staging" {
		t.Errorf("the list should wrap after two, got %q", a.ArgsInput)
	}
}

// The word ends at the cursor, so completing in the middle of a line leaves the rest of it
// alone.
func TestCompletingMidLineKeepsWhatFollows(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	a.argsPast = [][]string{{"ENV=prod"}}
	a.ArgsInput = "ENV= -- --verbose"
	a.ArgsCursor = len("ENV=")

	press(a, Tab())
	if a.ArgsInput != "ENV=prod -- --verbose" {
		t.Errorf("input = %q", a.ArgsInput)
	}
	if a.ArgsCursor != len("ENV=prod") {
		t.Errorf("cursor = %d, want the end of what was completed", a.ArgsCursor)
	}
}

// Paths complete against the project root, because that is where the task will run.
func TestTabCompletesPathsUnderTheProject(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Taskfile.yml", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(pivot.Fixture([]string{"test"}), dir)
	a.SetStateDir(t.TempDir())
	promptFor(t, a, "test")

	typeArgs(a, "-- Task")
	press(a, Tab())
	if a.ArgsInput != "-- Taskfile.yml" {
		t.Errorf("input = %q, want -- Taskfile.yml", a.ArgsInput)
	}

	// A directory keeps its slash, so the next ⇥ walks into it rather than stopping at it.
	typeArgs(a, "int")
	press(a, Tab())
	if a.ArgsInput != "internal/" {
		t.Errorf("input = %q, want internal/", a.ArgsInput)
	}

	// A project root is mostly dotfiles; they wait until you type the dot.
	typeArgs(a, "")
	press(a, Tab())
	if strings.Contains(a.ArgsInput, ".hidden") {
		t.Errorf("bare ⇥ offered a dotfile: %q", a.ArgsInput)
	}
	typeArgs(a, ".hid")
	press(a, Tab())
	if a.ArgsInput != ".hidden" {
		t.Errorf("input = %q, want .hidden", a.ArgsInput)
	}
}

// The candidates are only true for the word they were built from, so anything that changes
// that word ends the cycle.
func TestTypingEndsTheCompletionCycle(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	a.argsVars = []string{"ENV", "EXTRA"}
	typeArgs(a, "E")

	press(a, Tab())
	if a.argsComp == nil {
		t.Fatal("⇥ should have armed a cycle")
	}
	press(a, Char('X'))
	if a.argsComp != nil {
		t.Error("typing should have ended the cycle")
	}
	// And the next ⇥ is built from what is now in the prompt: had the old list survived it
	// would have offered `EXTRA=` here, over a word that no longer starts that way.
	press(a, Tab())
	if a.ArgsInput != "ENV=X" {
		t.Errorf("input = %q, want the typed word left alone", a.ArgsInput)
	}
	if !strings.Contains(a.Status, "no completion") {
		t.Errorf("status = %q", a.Status)
	}
}

// Nothing to offer is worth saying: a ⇥ that does nothing at all looks exactly like a
// prompt that cannot complete.
func TestTabSaysSoWhenThereIsNothingToOffer(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	typeArgs(a, "zzz")

	press(a, Tab())
	if !strings.Contains(a.Status, "no completion") {
		t.Errorf("status = %q", a.Status)
	}
	if a.ArgsInput != "zzz" {
		t.Errorf("a failed completion should leave the input alone, got %q", a.ArgsInput)
	}
}

// The footer lists where ⇥ goes next while you are cycling — the alternatives, not the one
// already spelled out in the prompt beside them.
func TestTheFooterListsTheOtherCandidates(t *testing.T) {
	a := promptFor(t, appWith(t, []string{"deploy"}), "deploy")
	a.argsVars = []string{"ENV", "REGION"}
	typeArgs(a, "")

	press(a, Tab())
	footer := footerOf(a)
	for _, want := range []string{"task deploy ENV=", "REGION=", "1/2"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q is missing %q", footer, want)
		}
	}
	if strings.Count(footer, "ENV=") != 1 {
		t.Errorf("the active candidate should be on the line once, in the prompt: %q", footer)
	}
}

// Every example a description spells out is a completion, which turns a convention nobody
// had to adopt into a set of presets.
func TestTabCompletesTheExamplesInTheDescription(t *testing.T) {
	a := New([]task.Task{{
		Name: "backend:test",
		Desc: "Run tests: task backend:test -- -p ingest, or task backend:test -- -p api",
	}}, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	promptFor(t, a, "backend:test")
	typeArgs(a, "")

	press(a, Tab())
	if a.ArgsInput != "-- -p ingest" {
		t.Errorf("first example = %q", a.ArgsInput)
	}
	press(a, Tab())
	if a.ArgsInput != "-- -p api" {
		t.Errorf("second example = %q, want the one the hint had no room for", a.ArgsInput)
	}
}

// What you ran last time beats an empty line, and it comes back quoted the way it went in.
func TestThePromptOpensOnWhatYouRanLastTime(t *testing.T) {
	a := appWith(t, []string{"site:new"})
	a.BeginArgs("site:new")
	if a.ArgsInput != "" {
		t.Fatalf("nothing has been run yet, so the prompt should be empty, got %q", a.ArgsInput)
	}

	// A run in the archive for this task, and one for another, which is not an answer here.
	seedArgs(t, a, "site:new", []string{"--", "My Post Title"})
	seedArgs(t, a, "backend:test", []string{"--", "-p", "ingest"})

	a.BeginArgs("site:new")
	if a.ArgsInput != `-- "My Post Title"` {
		t.Errorf("input = %q, want the quoting that makes it one argument again", a.ArgsInput)
	}
	if a.ArgsCursor != len([]rune(a.ArgsInput)) {
		t.Errorf("cursor = %d, want the end of the line", a.ArgsCursor)
	}
	// And it splits back into what was actually run, not into four arguments.
	if got := task.SplitArgs(a.ArgsInput); len(got) != 2 || got[1] != "My Post Title" {
		t.Errorf("splits to %q", got)
	}

	// The footer says where a line you did not type came from, and stops the moment you
	// touch it.
	if !strings.Contains(footerOf(a), "last run") {
		t.Errorf("footer %q should say the line is last time's", footerOf(a))
	}
	press(a, Char('x'))
	if strings.Contains(footerOf(a), "last run") {
		t.Errorf("footer %q is still calling an edited line last time's", footerOf(a))
	}
}

// A variable the task asks for wins over last time's answer: its value is the part that
// changes per run, which is the one thing last time's is reliably wrong about.
func TestAVariableBeatsWhatYouRanLastTime(t *testing.T) {
	a := New([]task.Task{{
		Name: "wt:new",
		Desc: "Create an agent worktree (NAME=add_x)",
	}}, t.TempDir())
	a.SetStateDir(t.TempDir())
	seedArgs(t, a, "wt:new", []string{"NAME=backend"})

	a.BeginArgs("wt:new")
	if a.ArgsInput != "NAME=" {
		t.Errorf("input = %q, want the key alone", a.ArgsInput)
	}
	if strings.Contains(footerOf(a), "last run") {
		t.Error("a line that came from the task is not last time's")
	}
	// It is still one ⇥ away, which is the point of it being a completion rather than a
	// default.
	press(a, Tab())
	if a.ArgsInput != "NAME=backend" {
		t.Errorf("⇥ = %q, want last time's value", a.ArgsInput)
	}
}
