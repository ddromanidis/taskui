package pivot

import (
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/task"
)

// ordered builds the domain tree over bare names with a given ordering, so the assertions
// below read as the screen does.
func ordered(tasks []task.Task, ord Order) string {
	all := make([]int, len(tasks))
	for i := range tasks {
		all[i] = i
	}
	return drawTree(Build(Domain(), tasks, all, ord))
}

func want(t *testing.T, got, expected string) {
	t.Helper()
	if got != expected {
		t.Errorf("got:\n%swant:\n%s", got, expected)
	}
}

// ran answers the archive lookup from a table of task name to how it went and when.
func ran(table map[string]Outcome) func(string) (Outcome, bool) {
	return func(name string) (Outcome, bool) {
		outcome, ok := table[name]
		return outcome, ok
	}
}

// --- what the default is ----------------------------------------------------------------

// The rule that has always been there, now that it is one comparator rather than three:
// a namespace's own leaves sit above its subtrees, so `docker` follows `lint` despite `d`
// sorting before `l`.
func TestSubgroupsSinkBelowThePlainTasksBesideThem(t *testing.T) {
	tasks := Fixture([]string{"backend:lint", "backend:build", "backend:docker:up"})
	want(t, ordered(tasks, Order{}), "backend/\n  build\n  lint\n  docker/\n    up\n")
}

func TestMixedGroupsSortWithTheTasksInstead(t *testing.T) {
	tasks := Fixture([]string{"backend:lint", "backend:build", "backend:docker:up"})
	want(t, ordered(tasks, Order{Interleave: true}),
		"backend/\n  build\n  docker/\n    up\n  lint\n")
}

// The verb pivot's size ordering is a property of the grouping rather than a preference, so
// it survives being expressed as `Natural` rather than as its own sort function.
func TestVerbStillLeadsWithItsBiggestGroup(t *testing.T) {
	tasks := Fixture([]string{"a:build", "b:build", "c:build", "a:lint", "b:lint"})
	all := []int{0, 1, 2, 3, 4}
	got := drawTree(Build(Verb(), tasks, all, Order{}))
	want(t, got, "build/\n  a:build\n  b:build\n  c:build\nlint/\n  a:lint\n  b:lint\n")
}

// …and an explicit ordering overrules it, which is the entire point of the config key.
// `zzz` has three members and `aaa` two, so the two orders disagree about which leads.
func TestAnExplicitOrderOverrulesThePivotsOwn(t *testing.T) {
	tasks := Fixture([]string{"a:zzz", "b:zzz", "c:zzz", "a:aaa", "b:aaa"})
	all := []int{0, 1, 2, 3, 4}

	got := drawTree(Build(Verb(), tasks, all, Order{}))
	if !strings.HasPrefix(got, "zzz/") {
		t.Errorf("verb leads with its biggest group by default, got:\n%s", got)
	}

	got = drawTree(Build(Verb(), tasks, all, Order{By: ByName}))
	if !strings.HasPrefix(got, "aaa/") {
		t.Errorf("`sort: name` should overrule it, got:\n%s", got)
	}

	// And the transpose: `size` asked for in a pivot that would not have chosen it.
	got = drawTree(Build(Domain(), Fixture([]string{"aaa:one", "zzz:one", "zzz:two"}),
		[]int{0, 1, 2}, Order{By: BySize}))
	if !strings.HasPrefix(got, "zzz/") {
		t.Errorf("`sort: size` should lead with the bigger namespace, got:\n%s", got)
	}
}

// --- the orders themselves --------------------------------------------------------------

func TestFileOrderFollowsTheTaskfile(t *testing.T) {
	tasks := Fixture([]string{"dev", "build", "test"})
	// Written dev, build, test — the order somebody chose, and the one alphabetising throws
	// away.
	tasks[0].Where = task.Where{File: "Taskfile.yml", Line: 4}
	tasks[1].Where = task.Where{File: "Taskfile.yml", Line: 9}
	tasks[2].Where = task.Where{File: "Taskfile.yml", Line: 14}
	want(t, ordered(tasks, Order{By: ByFile}), "(root)/\n  dev\n  build\n  test\n")
}

// The locations arrive a beat after the first frame. Until they do, `file` has nothing to
// say and falls through to the name rather than pretending everything is at line zero.
func TestFileOrderFallsBackToTheNameBeforeTheListingArrives(t *testing.T) {
	tasks := Fixture([]string{"dev", "build", "test"})
	want(t, ordered(tasks, Order{By: ByFile}), "(root)/\n  build\n  dev\n  test\n")
}

func TestAKnownLocationOutranksAnUnknownOne(t *testing.T) {
	tasks := Fixture([]string{"aaa", "zzz"})
	tasks[1].Where = task.Where{File: "Taskfile.yml", Line: 2}
	want(t, ordered(tasks, Order{By: ByFile}), "(root)/\n  zzz\n  aaa\n")
}

func TestRecentPutsTheLastThingYouRanOnTop(t *testing.T) {
	tasks := Fixture([]string{"build", "lint", "test"})
	order := Order{By: ByRecent, Ran: ran(map[string]Outcome{
		"build": {Ok: true, WhenUnix: 100},
		"test":  {Ok: true, WhenUnix: 300},
	})}
	// `lint` has never run, so it sorts below both and keeps its alphabetical place there.
	want(t, ordered(tasks, order), "(root)/\n  test\n  build\n  lint\n")
}

func TestFailedPutsWhatIsBrokenOnTop(t *testing.T) {
	tasks := Fixture([]string{"build", "lint", "test"})
	order := Order{By: ByFailed, Ran: ran(map[string]Outcome{
		"build": {Ok: true, WhenUnix: 300},
		"lint":  {Ok: false, WhenUnix: 100},
		"test":  {Ok: false, WhenUnix: 200},
	})}
	// Broken first, and within it the most recent failure — the one you are here about.
	want(t, ordered(tasks, order), "(root)/\n  test\n  lint\n  build\n")
}

// A group is as recent as its most recent task and as broken as its worst one, because what
// you want to know about a fold is whether there is anything in there worth opening.
func TestAGroupCarriesTheStateOfWhatIsInside(t *testing.T) {
	tasks := Fixture([]string{"a:one", "b:two"})
	order := Order{By: ByFailed, Interleave: true, Ran: ran(map[string]Outcome{
		"b:two": {Ok: false, WhenUnix: 100},
	})}
	want(t, ordered(tasks, order), "b/\n  two\na/\n  one\n")
}

// Without the archive there is nothing to sort on, and inventing an order would be worse
// than admitting there isn't one.
func TestRecentWithoutAnArchiveIsAlphabetical(t *testing.T) {
	tasks := Fixture([]string{"test", "build"})
	want(t, ordered(tasks, Order{By: ByRecent}), "(root)/\n  build\n  test\n")
}

// --- pins ---------------------------------------------------------------------------

func TestPinsLeadInTheOrderTheyAreWritten(t *testing.T) {
	tasks := Fixture([]string{"build", "dev", "lint", "test"})
	want(t, ordered(tasks, Order{Pins: []string{"test", "dev"}}),
		"(root)/\n  test\n  dev\n  build\n  lint\n")
}

// A pin you have to go looking for is not a pin: the group holding it rises too, all the way
// to the top of the list.
func TestAGroupRisesWithWhatItHolds(t *testing.T) {
	tasks := Fixture([]string{"aaa:one", "zzz:deploy"})
	want(t, ordered(tasks, Order{Pins: []string{"zzz:deploy"}}),
		"zzz/\n  deploy\naaa/\n  one\n")
}

func TestPinsGlobAndCanNameAGroupDirectly(t *testing.T) {
	tasks := Fixture([]string{"aaa:one", "zzz:deploy", "zzz:build"})
	want(t, ordered(tasks, Order{Pins: []string{"zzz"}}),
		"zzz/\n  build\n  deploy\naaa/\n  one\n")

	want(t, ordered(tasks, Order{Pins: []string{"*:deploy"}}),
		"zzz/\n  deploy\n  build\naaa/\n  one\n")
}

// Pinning beats the ranks the pivot itself insists on, `(root)` included. You asked for this
// row by name; nothing the grouping believes should outrank that.
func TestAPinOutranksTheRootGroup(t *testing.T) {
	tasks := Fixture([]string{"dev", "zzz:deploy"})
	want(t, ordered(tasks, Order{Pins: []string{"zzz:*"}}),
		"zzz/\n  deploy\n(root)/\n  dev\n")
}

// --- parsing --------------------------------------------------------------------------

func TestParsingAnOrder(t *testing.T) {
	for _, c := range []struct {
		text string
		by   By
		ok   bool
	}{
		{"name", ByName, true},
		{"  RECENT ", ByRecent, true},
		{"default", ByNatural, true},
		{"", ByNatural, true},
		{"alphabetical", ByNatural, false},
	} {
		by, ok := ParseBy(c.text)
		if by != c.by || ok != c.ok {
			t.Errorf("ParseBy(%q) = %q, %v; want %q, %v", c.text, by, ok, c.by, c.ok)
		}
	}
}

// The zero value is the right thing in Go and the wrong thing to write in a config file.
func TestTheDefaultOrderIsSpelledDefault(t *testing.T) {
	if got := ByNatural.String(); got != "default" {
		t.Errorf("ByNatural prints as %q", got)
	}
}

// A label in the domain tree is one path segment, so matching it would let `pin: [release]`
// hoist `build:release` — a task you did not name and, in the tree, a different row.
func TestAPinNamesTheTaskAndNotAPathSegment(t *testing.T) {
	tasks := Fixture([]string{"aaa:one", "build:release", "release"})
	got := ordered(tasks, Order{Pins: []string{"release"}})
	want(t, got, "(root)/\n  release\naaa/\n  one\nbuild/\n  release\n")
}

// A namespace with no task of its own has only the names the pivot gave it, and both work.
func TestAPureNamespaceIsPinnedByItsPath(t *testing.T) {
	tasks := Fixture([]string{"aaa:one", "zzz:migrate:up"})
	want(t, ordered(tasks, Order{Pins: []string{"zzz:migrate"}}),
		"zzz/\n  migrate/\n    up\naaa/\n  one\n")
}
