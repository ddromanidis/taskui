package task

import (
	"reflect"
	"testing"
)

func TestParsesAPlainEntry(t *testing.T) {
	got, ok := parseEntry("* all:                           Everything: format, lint, test, build")
	if !ok {
		t.Fatal("expected an entry")
	}
	if got.Name != "all" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Everything: format, lint, test, build" {
		t.Errorf("desc = %q", got.Desc)
	}
	if len(got.Aliases) != 0 {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

func TestParsesAliasesOffTheEnd(t *testing.T) {
	got, _ := parseEntry("* build:      Build all components        (aliases: b)")
	if got.Name != "build" || got.Desc != "Build all components" {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got.Aliases, []string{"b"}) {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

func TestParsesNamespacedNames(t *testing.T) {
	got, _ := parseEntry("* backend:migrate:down:   Roll back the most recent migration")
	if got.Name != "backend:migrate:down" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Roll back the most recent migration" {
		t.Errorf("desc = %q", got.Desc)
	}
}

// A description ending in a parenthesis is not an alias list.
func TestAParenthesisedDescriptionIsNotEaten(t *testing.T) {
	got, _ := parseEntry("* setup:   Install tools and dependencies (safe to re-run)")
	if got.Desc != "Install tools and dependencies (safe to re-run)" {
		t.Errorf("desc = %q", got.Desc)
	}
	if len(got.Aliases) != 0 {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

// `--list-all` includes tasks with no description at all.
func TestHandlesAMissingDescription(t *testing.T) {
	got, _ := parseEntry("* sec:secrets:dir:")
	if got.Name != "sec:secrets:dir" || got.Desc != "" {
		t.Errorf("got %+v", got)
	}
}

func TestIgnoresTheHeaderAndBlankLines(t *testing.T) {
	if _, ok := parseEntry("task: Available tasks for this project:"); ok {
		t.Error("header parsed as a task")
	}
	if _, ok := parseEntry(""); ok {
		t.Error("blank line parsed as a task")
	}
}

func taskWith(name, desc string) Task {
	return Task{Name: name, Desc: desc}
}

func TestMinesAUsageHintFromTheDescription(t *testing.T) {
	x := taskWith("backend:test", "Run Rust workspace tests (task backend:test -- -p ingest for one crate)")
	hint, _ := x.ArgsHint()
	if hint != "-- -p ingest for one crate" {
		t.Errorf("hint = %q", hint)
	}
}

// Included tasks describe themselves by their local name.
func TestMinesAHintWrittenWithTheBareName(t *testing.T) {
	x := taskWith("site:new", `New post — usage: task new -- "My Post Title" (title is slugified)`)
	hint, _ := x.ArgsHint()
	if hint != `-- "My Post Title"` {
		t.Errorf("hint = %q", hint)
	}
}

// The commentary after the arguments is not part of them.
func TestTrimsTrailingProseFromAHint(t *testing.T) {
	for _, c := range []struct{ name, desc, want string }{
		{"wt:rm", "Remove an agent's worktree: task wt:rm NAME=backend (the BRANCH survives)", "NAME=backend"},
		{"deploy:local", "Full deploy. Pass a target with `task deploy:local -- infra:deploy:plan`.", "-- infra:deploy:plan"},
		{"dev:run", "Run the backend once: task dev:run -- <args>, default `serve`", "-- <args>"},
	} {
		hint, _ := taskWith(c.name, c.desc).ArgsHint()
		if hint != c.want {
			t.Errorf("%s: hint = %q, want %q", c.name, hint, c.want)
		}
	}
}

func TestMinesBareAssignmentConventions(t *testing.T) {
	x := taskWith("backend:gen:migration", "Scaffold a migration and register it (NAME=add_x)")
	hint, _ := x.ArgsHint()
	if hint != "NAME=add_x" {
		t.Errorf("hint = %q", hint)
	}
}

// A parenthetical that is just prose is not a usage hint.
func TestDoesNotInventAHintFromOrdinaryProse(t *testing.T) {
	x := taskWith("setup", "Install tools and dependencies (safe to re-run)")
	if hint, ok := x.ArgsHint(); ok {
		t.Errorf("invented %q", hint)
	}
}

// A hint of the `KEY=value` shape tells us the key even when the task does not declare
// `requires:`.
func TestExtractsKeysFromAnAssignmentHint(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"NAME=backend", []string{"NAME"}},
		{"WORD=адрес", []string{"WORD"}},
		{"A=1 B=2", []string{"A", "B"}},
	} {
		if got := KeysInHint(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("KeysInHint(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// `--` style arguments carry no key to pre-fill.
func TestExtractsNoKeysFromADashDashHint(t *testing.T) {
	for _, in := range []string{`-- "My Post Title"`, "-- -p ingest"} {
		if got := KeysInHint(in); len(got) != 0 {
			t.Errorf("KeysInHint(%q) = %v", in, got)
		}
	}
}

// Quoted arguments have to reach go-task intact.
func TestSplitsArgsLikeAShell(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"-- convert report.pdf", []string{"--", "convert", "report.pdf"}},
		{`-- "My Post Title"`, []string{"--", "My Post Title"}},
		{"NAME=backend", []string{"NAME=backend"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{"", []string{}},
	} {
		if got := SplitArgs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitArgs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestQuotesInsideWordsAndEscapesSurvive(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`MSG="hello world"`, []string{"MSG=hello world"}},
		{`path\ with\ spaces`, []string{"path with spaces"}},
		// An empty quoted string is a real argument, and `--` is one too.
		{`-- '' empty`, []string{"--", "", "empty"}},
	} {
		if got := SplitArgs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitArgs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestGlobsMatchColonPaths(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"deploy:*", "deploy:backend", true},
		{"deploy:*", "deploy:logs:archive", true},
		{"deploy:*", "dev:up", false},
		{"backend:migrate:prod", "backend:migrate:prod", true},
		{"backend:migrate:prod", "backend:migrate:down", false},
		{"*:prod", "backend:promo:prod", true},
		{"*", "anything", true},
	} {
		if got := GlobMatch(c.pattern, c.name); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
}

// Production-touching tasks are flagged so they are not one fuzzy keypress away.
func TestFlagsProductionTasks(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{"* deploy:backend:   Deploy the backend", true},
		{"* backend:migrate:prod:   Apply migrations", true},
		{"* backend:lint:   Rust lints", false},
	} {
		got, _ := parseEntry(c.line)
		if got.Dangerous != c.want {
			t.Errorf("%q dangerous = %v", c.line, got.Dangerous)
		}
	}
}

// --- talking to go-task ----------------------------------------------------------------

// go-task colours its output whenever the environment asks it to, and on a CI runner that
// is the ordinary case. A listing line that arrives coloured starts with an escape rather
// than with `* `, so the parser matches nothing and a project with forty tasks in it is
// reported as having none — no error, no empty output, nothing to notice.
//
// This is what that looked like: it cost a release, because the failure reached CI as a
// panic three packages away, in a test asserting on the first task of an empty list.
func TestAskTurnsTheColourOff(t *testing.T) {
	cmd := Ask(t.TempDir(), "--list-all")
	found := false
	for _, kv := range cmd.Env {
		if kv == "NO_COLOR=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("every question we parse has to be asked in plain text: %v", cmd.Env)
	}
	if cmd.Args[0] != "task" || cmd.Args[1] != "--list-all" {
		t.Errorf("args = %v", cmd.Args)
	}
}

// And the parser is not left relying on that alone: NO_COLOR is a request to a program we
// do not control, and the cost of it being ignored is a silent empty list rather than an
// error. Stripping is the half we can guarantee.
func TestAColouredListingStillParses(t *testing.T) {
	coloured := "\x1b[33m* \x1b[0m\x1b[32mbuild\x1b[0m\x1b[0m:       Compile it\x1b[0m"
	got, ok := parseEntry(coloured)
	if !ok {
		t.Fatalf("a coloured line parsed as nothing: %q", coloured)
	}
	if got.Name != "build" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Compile it" {
		t.Errorf("desc = %q", got.Desc)
	}
}
