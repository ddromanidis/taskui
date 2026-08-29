package pivot

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/ddromanidis/taskui/internal/task"
)

// Pivot is one way of grouping the task list.
//
// A pivot is a key function and nothing else — that has been the claim since the package
// comment was written, and until now it was a claim with exactly two implementations
// hardcoded into an enum. It is a value now, so a Taskfile that groups by something this
// tool has never heard of can say so in a config file.
type Pivot struct {
	// Name is what `p` cycles through and what `--dump` takes.
	Name string
	// Build makes the tree. The two built-ins have shapes a plain path cannot express —
	// domain folds a root-level task into its own namespace, verb pools singletons — so
	// this is a function rather than a key.
	Build func(tasks []task.Task, visible []int) *Tree
	// Natural is the order this grouping is meant to be read in, used when the config has
	// no opinion. It is a property of the grouping and not of the reader: `verb` is worth
	// nothing in alphabetical order, because the point of transposing the tree is to find
	// the concerns you did not already know to look for.
	Natural By
}

// Builtins are the pivots that ship.
//
// `file` is here rather than in a config because the information it needs — which Taskfile
// each task is written in — is something taskui already fetches, and because "where do I
// edit this" is a question every project with `includes:` has.
func Builtins() []Pivot {
	return []Pivot{
		{Name: "domain", Build: buildDomain, Natural: ByName},
		{Name: "verb", Build: buildVerb, Natural: BySize},
		ByPath("file", func(t task.Task) []string {
			if !t.Where.Ok() {
				return nil
			}
			return []string{shortestUnique(t.Where.File)}
		}),
	}
}

// shortestUnique trims a Taskfile path to something readable. The full path is the same
// prefix on every row, and the prefix is not the answer to which file this is.
func shortestUnique(path string) string {
	slashed := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(slashed, "/")
	if len(parts) <= 2 {
		return slashed
	}
	// The parent directory disambiguates the dozen `Taskfile.yml`s an `includes:` tree has,
	// and two segments is almost always enough to tell them apart.
	return strings.Join(parts[len(parts)-2:], "/")
}

// Named returns a built-in by name, for callers that want one specific grouping.
func Named(name string) Pivot {
	for _, p := range Builtins() {
		if p.Name == name {
			return p
		}
	}
	return Builtins()[0]
}

// Domain and Verb are the two groupings by name, for the callers that want one directly.
func Domain() Pivot { return Named(DomainName) }
func Verb() Pivot   { return Named(VerbName) }
func File() Pivot   { return Named(FileName) }

// ByPath builds a pivot from a key function: the path a task sits at, outermost first.
//
// Leaves show the full task name rather than the last path segment. In a custom pivot the
// grouping is orthogonal to the name — grouping by owner does not make the owner part of
// what the task is called — so flattening the name into the leaf would throw away the one
// thing that identifies it. The domain pivot is the exception, and it has its own builder.
func ByPath(name string, path func(task.Task) []string) Pivot {
	return Pivot{
		Name: name,
		Build: func(tasks []task.Task, visible []int) *Tree {
			return buildByPath(tasks, visible, name, path)
		},
		// Alphabetical rather than the verb pivot's size ordering: a custom grouping is one
		// the reader defined, so they know what they are looking for and want to find it in
		// the place they expect. Size ordering earns its keep in `verb`, where the whole
		// point is surfacing cross-cutting concerns you had not thought of.
		Natural: ByName,
	}
}

func buildByPath(tasks []task.Task, visible []int, name string, path func(task.Task) []string) *Tree {
	tree := &Tree{}
	index := map[string]int{}

	// Group nodes are created on demand and remembered by their full path, so two tasks
	// under the same group land in the same node however far apart they are in the list.
	group := func(segments []string) int {
		parent := NoTask
		for depth := range segments {
			key := name + ":" + strings.Join(segments[:depth+1], "/")
			node, existing := index[key]
			if !existing {
				node = tree.push(segments[depth], key)
				if parent == NoTask {
					tree.Roots = append(tree.Roots, node)
				} else {
					tree.Nodes[parent].Children = append(tree.Nodes[parent].Children, node)
				}
				index[key] = node
			}
			parent = node
		}
		return parent
	}

	var homeless []int
	for _, ti := range visible {
		segments := path(tasks[ti])
		if len(segments) == 0 {
			homeless = append(homeless, ti)
			continue
		}
		parent := group(segments)
		leaf := tree.push(tasks[ti].Name, fmt.Sprintf("%s:%s/%s",
			name, strings.Join(segments, "/"), tasks[ti].Name))
		tree.Nodes[leaf].Task = ti
		tree.Nodes[parent].Children = append(tree.Nodes[parent].Children, leaf)
	}

	// A task the pivot had no answer for still has to be reachable. Dropping it would mean
	// the list silently holds fewer tasks than the header says.
	if len(homeless) > 0 {
		gi := tree.push(OtherGroup, name+":"+OtherGroup)
		tree.Nodes[gi].Rank = RankLast
		tree.Roots = append(tree.Roots, gi)
		for _, ti := range homeless {
			leaf := tree.push(tasks[ti].Name, fmt.Sprintf("%s:%s/%s", name, OtherGroup, tasks[ti].Name))
			tree.Nodes[leaf].Task = ti
			tree.Nodes[gi].Children = append(tree.Nodes[gi].Children, leaf)
		}
	}

	return tree
}

// --- pivots from a config file ---------------------------------------------------------

// Spec is a pivot as written in `config.yaml`, before it is compiled.
//
// Two forms, and deliberately not a third. `regex` covers a naming convention, which is
// what most teams actually want to group by. `command` covers everything else by handing
// the question to a program, which is the only extension mechanism that survives being
// installed from a release archive — see the note on Compile.
type Spec struct {
	Name string
	// Regex is matched against the task name; Path builds the grouping from its captures.
	Regex string
	Path  []string
	// Command is run once per rebuild with the task names on stdin.
	Command []string
}

// group references in a path template: `{1}` is the first capture.
var capture = regexp.MustCompile(`\{(\d+)\}`)

// Compile turns a spec into a pivot, or says what is wrong with it.
//
// Note what is not here: loading a Go plugin. `plugin.Open` needs the host and the plugin
// built by the same toolchain version, against identical versions of every shared
// dependency, with matching build flags — and it needs cgo. taskui's release binaries are
// built `CGO_ENABLED=0`, where every `plugin.Open` returns "plugin: not implemented", and
// with `-trimpath`, which a locally built plugin would not match even with cgo on. A Go
// plugin would therefore work only for someone who built taskui themselves, on the same
// machine, in the same afternoon. An external command has none of those constraints and can
// be written in anything.
func (s Spec) Compile(dir string) (Pivot, error) {
	switch {
	case s.Name == "":
		return Pivot{}, errors.New("a pivot needs a name")
	case s.Regex != "" && len(s.Command) > 0:
		return Pivot{}, fmt.Errorf(
			"pivot %q: `regex` and `command` are two ways of doing the same thing — pick one",
			s.Name,
		)
	case s.Regex != "":
		return s.compileRegex()
	case len(s.Command) > 0:
		return s.compileCommand(dir), nil
	default:
		return Pivot{}, fmt.Errorf("pivot %q needs a `regex` or a `command`", s.Name)
	}
}

func (s Spec) compileRegex() (Pivot, error) {
	re, err := regexp.Compile(s.Regex)
	if err != nil {
		return Pivot{}, fmt.Errorf("pivot %q: %w", s.Name, err)
	}
	template := s.Path
	if len(template) == 0 {
		// The obvious default: one level, the first capture. A pattern with no captures
		// groups everything it matches under the matched text itself.
		template = []string{"{1}"}
		if re.NumSubexp() == 0 {
			template = []string{"{0}"}
		}
	}
	for _, part := range template {
		for _, m := range capture.FindAllStringSubmatch(part, -1) {
			var n int
			if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil && n > re.NumSubexp() {
				return Pivot{}, fmt.Errorf(
					"pivot %q: `%s` refers to group %d but the pattern has %d",
					s.Name, part, n, re.NumSubexp())
			}
		}
	}

	return ByPath(s.Name, func(t task.Task) []string {
		m := re.FindStringSubmatch(t.Name)
		if m == nil {
			return nil
		}
		out := make([]string, 0, len(template))
		for _, part := range template {
			filled := capture.ReplaceAllStringFunc(part, func(ref string) string {
				var n int
				if _, err := fmt.Sscanf(ref, "{%d}", &n); err != nil || n >= len(m) {
					return ""
				}
				return m[n]
			})
			// An empty segment would be a group with no name. Dropping it lets one pattern
			// serve tasks at different depths.
			if filled != "" {
				out = append(out, filled)
			}
		}
		return out
	}), nil
}

// commandTimeout bounds a pivot program, because a hung one would hang the UI with it.
//
// Generous, because it only has to be paid once: the answer is cached against the task list,
// so a program is asked again when the Taskfile changes and not before.
const commandTimeout = 5 * time.Second

func (s Spec) compileCommand(dir string) Pivot {
	// The cache is the reason this is affordable at all. Rebuild runs on every keystroke of
	// a filter, and spawning a process per keystroke would make the pivot the slowest thing
	// in the program. A filter narrows which tasks are *shown*; it cannot change where a
	// task belongs, so the mapping is asked for once over the whole list and reused.
	var cached map[string][]string
	var against string

	return Pivot{
		Name: s.Name,
		Build: func(tasks []task.Task, visible []int) *Tree {
			if key := fingerprint(tasks); key != against || cached == nil {
				cached = runPivotCommand(dir, s.Command, tasks)
				against = key
			}
			paths := cached
			return buildByPath(tasks, visible, s.Name, func(t task.Task) []string {
				return paths[t.Name]
			})
		},
	}
}

// fingerprint is what the cache is held against: a different task list is a different
// question, and anything else is the same one asked again.
func fingerprint(tasks []task.Task) string {
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(t.Name)
		b.WriteByte('\n')
	}
	return b.String()
}

// runPivotCommand asks a program where every task belongs.
//
// The protocol is the smallest thing that could work: task names in on stdin, one per line;
// `name<TAB>outer/inner` back on stdout. A name it says nothing about pools into `(other)`,
// which is also what happens if the program is missing, fails, or takes too long — a pivot
// that cannot answer should leave you with a usable list, not an empty one.
//
// Every task, not just the visible ones: a filter changes what is shown and cannot change
// where a task belongs, and asking about the whole list is what makes the answer cacheable.
func runPivotCommand(dir string, argv []string, tasks []task.Task) map[string][]string {
	out := map[string][]string{}
	if len(argv) == 0 {
		return out
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	//nolint:gosec // the argv is what the user put in their own config file, which is the
	// whole point of the feature; it is no more privileged than the tasks taskui already runs
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fingerprint(tasks))
	blob, err := cmd.Output()
	if err != nil {
		return out
	}

	scanner := bufio.NewScanner(strings.NewReader(string(blob)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		name, path, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			continue
		}
		var segments []string
		for seg := range strings.SplitSeq(path, "/") {
			if trimmed := strings.TrimSpace(seg); trimmed != "" {
				segments = append(segments, trimmed)
			}
		}
		if len(segments) > 0 {
			out[strings.TrimSpace(name)] = segments
		}
	}
	return out
}
