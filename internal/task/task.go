// Package task discovers tasks by shelling out to `task --list-all` and parsing the result.
//
// The obvious call is `--list-all --json`, which additionally carries aliases as an array
// and each task's source location. It is also, measured on a repo with seven `includes:`
// and twenty tasks declaring `sources:`, fifty-six times slower — 4.28s against 0.076s.
// The `--json` form computes up_to_date for every task, which means fingerprinting every
// `sources:` glob, which on a large workspace means walking the build directory. Almost
// all of that 4.28s was system time, not parsing.
//
// Four seconds before the first frame is not a price worth paying for a source location
// nothing reads yet, so this parses the text form instead. If the file pivot or
// jump-to-definition ever land, they should fetch the JSON on a background goroutine and
// fill it in — not block startup on it.
//
// Neither form carries dependencies, so this is only the static half of the picture; the
// execution graph comes from the graph package.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Task is one entry of `task --list-all`.
type Task struct {
	// Name is the full colon path, e.g. `backend:migrate:down`.
	Name    string
	Desc    string
	Aliases []string
	// Dangerous is a heuristic: does the description suggest this touches production or
	// destroys data?
	Dangerous bool
	// Where the task is written, once the JSON listing has arrived. Zero until then, which
	// is why the file pivot lists everything ungrouped for the first moment of a session
	// and then fills in.
	Where Where
}

// Segments splits the name: `backend:migrate:down` -> ["backend", "migrate", "down"].
func (t Task) Segments() []string {
	return strings.Split(t.Name, ":")
}

// Verb is the last segment — what the task pivot groups on.
func (t Task) Verb() string {
	if i := strings.LastIndex(t.Name, ":"); i >= 0 {
		return t.Name[i+1:]
	}
	return t.Name
}

// ArgsHint mines a usage hint from the description, which by convention spells out an
// example: "Scaffold a migration and register it (NAME=add_x)" or "task backend:test --
// -p ingest for one crate".
//
// Shown beside the args prompt rather than pre-filled into it. The descriptions trail off
// into prose often enough — "…-- -p ingest for one crate" — that pre-filling would hand
// you a command that is wrong in a way you might not notice before pressing enter.
func (t Task) ArgsHint() (string, bool) {
	hints := t.ArgsHints()
	if len(hints) == 0 {
		return "", false
	}
	return hints[0], true
}

// ArgsHints is every example the description spells out, in the order they appear.
//
// One description often carries more than one — "task backend:test -- -p ingest, or
// task backend:test -- -p api" — and the first is only the one the hint has room for. The
// args prompt completes against the rest, which makes a convention nobody has to adopt
// into a set of presets for Taskfiles nobody is going to edit.
func (t Task) ArgsHints() []string {
	var out []string
	// Descriptions are written from inside the file that defines the task, so an included
	// one often spells its example with the bare name: `site/Taskfile.yml` says
	// "task new -- …" for what the root calls `site:new`. Try both.
	needles := []string{"task " + t.Name + " "}
	if v := t.Verb(); v != t.Name {
		needles = append(needles, "task "+v+" ")
	}
	for _, needle := range needles {
		rest := t.Desc
		for {
			at := strings.Index(rest, needle)
			if at < 0 {
				break
			}
			rest = rest[at+len(needle):]
			if hint := trimProse(rest); hint != "" && !slices.Contains(out, hint) {
				out = append(out, hint)
			}
		}
	}
	if len(out) == 0 {
		if inner, ok := t.parenHint(); ok {
			out = append(out, inner)
		}
	}
	return out
}

// parenHint reads the bare `NAME=value` conventions, e.g. "(NAME=add_x)" or "(WORD=адрес)".
func (t Task) parenHint() (string, bool) {
	open := strings.Index(t.Desc, "(")
	if open < 0 {
		return "", false
	}
	rel := strings.Index(t.Desc[open:], ")")
	if rel < 0 {
		return "", false
	}
	inner := strings.TrimSpace(t.Desc[open+1 : open+rel])
	fields := strings.Fields(inner)
	if len(fields) == 0 {
		return "", false
	}
	w := fields[0]
	if !strings.Contains(w, "=") {
		return "", false
	}
	first := []rune(w)[0]
	if first < 'A' || first > 'Z' {
		return "", false
	}
	return inner, true
}

// KeysInHint returns the `KEY=` prefixes implied by a hint like `NAME=backend` or
// `WORD=адрес`.
//
// A fallback for tasks that take a variable but do not declare `requires:`. Only the key
// is kept — pre-filling the example value would be handing you someone else's argument.
func KeysInHint(hint string) []string {
	out := []string{}
	for w := range strings.FieldsSeq(hint) {
		k, _, ok := strings.Cut(w, "=")
		if !ok || k == "" {
			continue
		}
		shouty := true
		for _, c := range k {
			if (c < 'A' || c > 'Z') && c != '_' && (c < '0' || c > '9') {
				shouty = false
				break
			}
		}
		if shouty {
			out = append(out, k)
		}
	}
	return out
}

// trimProse cuts a mined hint back to the part that is plausibly an argument.
//
// Descriptions run on: "-- infra:deploy:plan`.", "NAME=backend (the BRANCH survives)".
// The parenthetical and the trailing punctuation are commentary, not arguments.
func trimProse(rest string) string {
	end := len(rest)
	for _, stop := range []string{")", " (", ";", ", "} {
		if at := strings.Index(rest, stop); at >= 0 && at < end {
			end = at
		}
	}
	return strings.TrimRight(strings.TrimSpace(rest[:end]), ".,` ")
}

// JoinArgs is SplitArgs backwards: the line that splits back into these arguments.
//
// Needed the moment a stored argument list is put back in front of you. `-- "My Post
// Title"` comes out of the archive as two arguments, and joining them with a space would
// hand back a line that runs as four.
func JoinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t'\"\\") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range arg {
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	b.WriteByte('"')
	return b.String()
}

// SplitArgs splits an args line the way a shell would, so quoted arguments survive.
//
// `site:new -- "My Post Title"` has to reach go-task as one argument, not three.
func SplitArgs(input string) []string {
	out := []string{}
	var current strings.Builder
	var quote rune
	started := false

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\':
			if i+1 < len(runes) {
				i++
				current.WriteRune(runes[i])
				started = true
			}
		case c == '\'' || c == '"':
			switch {
			case quote == c:
				quote = 0
			case quote != 0:
				current.WriteRune(c)
				started = true
			default:
				// An empty quoted string is still an argument.
				quote = c
				started = true
			}
		case unicode.IsSpace(c) && quote == 0:
			if started {
				out = append(out, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(c)
			started = true
		}
	}
	if started {
		out = append(out, current.String())
	}
	return out
}

// DangerFile is the opt-in file listing tasks that must not be run by accident.
const DangerFile = ".taskui-danger"

// GlobMatch matches a task name against a pattern supporting `*`.
//
// Deliberately not a full glob: task names are colon paths, and `deploy:*` plus exact
// names covers every real case without pulling in a dependency whose semantics would then
// need explaining.
func GlobMatch(pattern, name string) bool {
	parts := strings.Split(pattern, "*")
	first := parts[0]
	if !strings.HasPrefix(name, first) {
		return false
	}
	rest := name[len(first):]
	last := ""
	haveLast := false
	for _, part := range parts[1:] {
		last, haveLast = part, true
		if part == "" {
			continue
		}
		at := strings.Index(rest, part)
		if at < 0 {
			return false
		}
		rest = rest[at+len(part):]
	}
	// A trailing `*` swallows whatever is left; otherwise the pattern must reach the end.
	switch {
	case !haveLast:
		return rest == ""
	case last == "":
		return true
	default:
		return strings.HasSuffix(name, last)
	}
}

// DangerPatterns reads `.taskui-danger`: one pattern per line, `#` comments, blanks
// ignored.
//
// Its presence switches off the description heuristic entirely. A guess and a declaration
// disagreeing about which tasks are dangerous is worse than either alone — once you have
// written the list down, that list is the answer.
func DangerPatterns(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, DangerFile))
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// dangerWords mark a task as one you should not fire off from a fuzzy filter by accident.
//
// This is a heuristic over descriptions and it will both over- and under-match. It is a
// stopgap: the real fix is an explicit marker in the Taskfile, which we can honour later
// without changing anything else here.
var dangerWords = [...]string{
	"production",
	"prod database",
	"applies!",
	"wipe",
	"destroy",
	"claims ",
}

func looksDangerous(name, desc string) bool {
	d := strings.ToLower(desc)
	for _, w := range dangerWords {
		if strings.Contains(d, w) {
			return true
		}
	}
	// `backend:migrate:prod`, `backend:promo:prod`, `deploy:*`
	return strings.HasSuffix(name, ":prod") || strings.HasPrefix(name, "deploy:") || name == "deploy"
}

// parseEntry reads one line of `task --list-all`:
//
//   - build:      Build all components                    (aliases: b)
func parseEntry(line string) (Task, bool) {
	// Stripped even though Ask turns the colour off, because those are two different kinds
	// of promise: NO_COLOR is a request to a program we do not control, and what it costs
	// when it is not honoured is not an error but a project that appears to have no tasks in
	// it. This is the half we can guarantee.
	rest, ok := strings.CutPrefix(ansi.Strip(line), "* ")
	if !ok {
		return Task{}, false
	}
	// Names contain colons, so split at the first whitespace rather than the first colon —
	// `backend:migrate:down:` is one name, not three.
	name, tail := rest, ""
	if at := strings.IndexFunc(rest, unicode.IsSpace); at >= 0 {
		name, tail = rest[:at], rest[at:]
	}
	name = strings.TrimRight(name, ":")
	if name == "" {
		return Task{}, false
	}

	desc := strings.TrimSpace(tail)
	var aliases []string
	// Parsed off the end rather than searched for, so a description that happens to
	// mention the word does not get eaten.
	if strings.HasSuffix(desc, ")") {
		if at := strings.LastIndex(desc, "(aliases: "); at >= 0 {
			for a := range strings.SplitSeq(desc[at+len("(aliases: "):len(desc)-1], ",") {
				if a = strings.TrimSpace(a); a != "" {
					aliases = append(aliases, a)
				}
			}
			desc = strings.TrimSpace(desc[:at])
		}
	}

	return Task{
		Name:      name,
		Desc:      desc,
		Aliases:   aliases,
		Dangerous: looksDangerous(name, desc),
	}, true
}

// Ask builds the command for a question taskui puts to go-task, with colour off.
//
// Every question here is parsed, and go-task colours its answers whenever the environment
// tells it to — which on a CI runner is the normal case, not an exotic one. A listing line
// that arrives as "\x1b[33m* \x1b[0m\x1b[32mbuild\x1b[0m:" starts with an escape rather than
// with `* `, so the parser below matches nothing and discovery reports a project with no
// tasks in it. No error, no empty output, nothing to notice: the worst shape a bug can take.
//
// `NO_COLOR` is the standard way to say so and go-task honours it over anything asking for
// colour the other way. Deliberately not applied to the command that *runs* your task: that
// output is yours, it is shown and archived with its escapes intact, and stripping the colour
// out of a test runner's output would be taking something away rather than asking a question
// plainly.
func Ask(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("task", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd
}

// Discover runs `task --list-all` in dir and returns the tasks, minus the `*:default`
// entries — in a UI where a namespace is itself a selectable row, a task whose only job is
// "show available tasks" is noise.
func Discover(dir string) ([]Task, error) {
	declared := DangerPatterns(dir)

	cmd := Ask(dir, "--list-all")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		exitError := &exec.ExitError{}
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("`task --list-all` failed in %s: %s", dir, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("failed to run `task` — is go-task installed and on PATH? (%w)", err)
	}

	var tasks []Task
	for line := range strings.SplitSeq(string(stdout), "\n") {
		t, ok := parseEntry(line)
		if !ok || t.Name == "default" || strings.HasSuffix(t.Name, ":default") {
			continue
		}
		if len(declared) > 0 {
			t.Dangerous = false
			for _, p := range declared {
				if GlobMatch(p, t.Name) {
					t.Dangerous = true
					break
				}
			}
		}
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

// --- the JSON half -------------------------------------------------------------------

// Where a task is defined.
type Where struct {
	File string
	Line int
}

// Ok reports whether this location was actually filled in.
func (w Where) Ok() bool { return w.File != "" && w.Line > 0 }

// Detail is what `--list-all --json` knows that the text form does not: where each task is
// written, and whether go-task currently considers it up to date.
//
// Kept separate from Task because it arrives later. Discover has to return before the first
// frame; this can take four seconds on a workspace with a lot of `sources:` globs, and the
// UI is perfectly usable in the meantime without it.
type Detail struct {
	Where    Where
	UpToDate bool
}

// jsonListing is the shape of `task --list-all --json`, narrowed to what is read.
type jsonListing struct {
	Tasks []struct {
		Name     string `json:"name"`
		UpToDate bool   `json:"up_to_date"`
		Location struct {
			Taskfile string `json:"taskfile"`
			Line     int    `json:"line"`
		} `json:"location"`
	} `json:"tasks"`
}

// Details runs `task --list-all --json` and returns what it knows, keyed by task name.
//
// Slow on purpose-built repositories: computing `up_to_date` means fingerprinting every
// `sources:` glob, which on a workspace with a build directory in one means walking it.
// Measured at fifty-six times the text form on such a repo — 4.28s against 0.076s — almost
// all of it system time. Which is why nothing calls this before the first frame.
func Details(dir string) (map[string]Detail, error) {
	cmd := Ask(dir, "--list-all", "--json")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("`task --list-all --json` failed in %s: %s", dir, strings.TrimSpace(stderr.String()))
	}

	var listing jsonListing
	if err := json.Unmarshal(stdout, &listing); err != nil {
		return nil, fmt.Errorf("could not read `task --list-all --json`: %w", err)
	}

	out := make(map[string]Detail, len(listing.Tasks))
	for _, t := range listing.Tasks {
		out[t.Name] = Detail{
			Where:    Where{File: t.Location.Taskfile, Line: t.Location.Line},
			UpToDate: t.UpToDate,
		}
	}
	return out, nil
}
