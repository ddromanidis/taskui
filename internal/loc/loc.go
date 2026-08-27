// Package loc finds the file locations that tools print, and works out how to open them.
//
// The loop this closes is the one the rest of taskui stops one step short of: a task fails,
// you read `internal/app/view.go:212:5: undefined: foo`, and then you leave and retype that
// path into an editor. Every compiler, test runner and linter worth putting in a Taskfile
// says where it went wrong; taskui already knows which task printed each line and what
// directory that task ran in, which is exactly the context that makes a bare `view_test.go`
// resolvable. Nothing else on the machine has both halves.
package loc

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Loc is a place in a file that a tool named in its output.
type Loc struct {
	// Path is exactly as the tool wrote it — possibly relative, possibly a bare basename.
	// Resolving it is a separate step, because it needs a project to resolve against.
	Path string
	Line int
	// Col is 0 when the tool did not say. Plenty do not.
	Col int
	// Start and End are byte offsets of the whole reference within the line it came from,
	// so the renderer can underline the part that is actually a link.
	Start, End int
}

// pattern matches the near-universal `path:line[:col]` form.
//
// The extension is required, and that requirement is doing almost all of the work. Without
// it every duration (`12:34`), timestamp (`10:30:45`) and port (`localhost:8080`) in a
// build log becomes a candidate, and a highlight that lands on a third of the numbers on
// screen teaches you to ignore it. With it, a false positive needs to look like a filename,
// which in practice means it is one.
//
// The leading group keeps the match from starting mid-path: without it, `src/app/x.go:4`
// would also match at `app/x.go:4`, and the offsets used for highlighting would point at
// the wrong half of the reference.
var pattern = regexp.MustCompile(
	`(?:^|[\s"'` + "`" + `(\[<>=,;])([^\s:"'` + "`" + `()\[\]<>,;]*[^\s:"'` + "`" + `()\[\]<>,;.]\.[A-Za-z0-9_+]{1,10}):(\d+)(?::(\d+))?`,
)

// pyPattern is CPython's traceback form, which is the one common shape that is not
// `path:line` — and it is not worth missing, because a traceback is the single most likely
// thing to be on screen when a Python task fails.
var pyPattern = regexp.MustCompile(`File "([^"]+)", line (\d+)`)

// All returns every location referenced in one line of output, in the order they appear.
//
// Syntax only: nothing here touches the filesystem. This runs on every visible line of
// every frame, so it has to be cheap, and whether the file exists is a question the jump
// asks once rather than one the renderer asks sixty times a second.
func All(text string) []Loc {
	var out []Loc
	for _, m := range pattern.FindAllStringSubmatchIndex(text, -1) {
		path := text[m[2]:m[3]]
		lineNo, err := strconv.Atoi(text[m[4]:m[5]])
		if err != nil || lineNo <= 0 {
			continue
		}
		col := 0
		if m[6] >= 0 {
			col, _ = strconv.Atoi(text[m[6]:m[7]])
		}
		out = append(out, Loc{Path: path, Line: lineNo, Col: col, Start: m[2], End: m[1]})
	}
	for _, m := range pyPattern.FindAllStringSubmatchIndex(text, -1) {
		lineNo, err := strconv.Atoi(text[m[4]:m[5]])
		if err != nil || lineNo <= 0 {
			continue
		}
		out = append(out, Loc{Path: text[m[2]:m[3]], Line: lineNo, Start: m[0], End: m[1]})
	}
	return out
}

// First is All's common case: the reference a jump should take.
func First(text string) (Loc, bool) {
	all := All(text)
	if len(all) == 0 {
		return Loc{}, false
	}
	return all[0], true
}

// skipped are directories that cannot hold a source file worth jumping to, and would
// otherwise dominate the index — `node_modules` alone can be more files than the project.
var skipped = map[string]bool{
	".git": true, ".task": true, "node_modules": true, "target": true,
	"dist": true, "vendor": true, "build": true, ".venv": true,
	"__pycache__": true, ".worktrees": true, ".terraform": true, ".next": true,
}

// maxIndexed caps the basename index. A repository large enough to blow through this is one
// where a bare-basename reference was ambiguous anyway.
const maxIndexed = 40000

// Resolver turns a path a tool printed into a path that can be opened.
//
// The hard case is the one Go's own test output produces:
//
//	--- FAIL: TestWrap (0.00s)
//	    view_test.go:88: want 3, got 4
//
// `view_test.go` is relative to the package directory, which is not the directory the run
// started in, and nothing in the line says which package it was. So a path that does not
// resolve directly gets looked up by basename against an index of the project, built once
// and only if something actually needs it — most references are relative to the root and
// never reach that path.
type Resolver struct {
	root  string
	once  sync.Once
	index map[string][]string
}

func NewResolver(root string) *Resolver { return &Resolver{root: root} }

// Resolve returns an absolute path, and whether the file was found.
//
// Ambiguous is not the same as missing: two packages with a `main.go` both match a bare
// `main.go`, and the shallowest is the better guess but is still a guess. The caller is
// told so it can say so, because silently opening the wrong file is the one outcome worse
// than opening nothing.
//
// The three results are the absolute path, whether the name was ambiguous, and whether it
// was found at all.
func (r *Resolver) Resolve(path string) (string, bool, bool) {
	if path == "" {
		return "", false, false
	}
	if filepath.IsAbs(path) {
		if isFile(path) {
			return path, false, true
		}
		return "", false, false
	}
	// Relative to the project is the overwhelmingly common case, and it is exact — no
	// index, no ambiguity.
	direct := filepath.Join(r.root, path)
	if isFile(direct) {
		return direct, false, true
	}
	// …and relative to the working directory, for a taskui pointed somewhere else.
	if cwd, err := os.Getwd(); err == nil && cwd != r.root {
		if p := filepath.Join(cwd, path); isFile(p) {
			return p, false, true
		}
	}

	r.once.Do(r.build)
	candidates := r.index[filepath.Base(path)]
	if len(candidates) == 0 {
		return "", false, false
	}
	// A reference of `app/view.go` should beat a same-named file elsewhere, so prefer the
	// candidates that actually end with what was written before falling back to basename
	// alone.
	if suffixed := matchingSuffix(candidates, path); len(suffixed) > 0 {
		candidates = suffixed
	}
	return filepath.Join(r.root, candidates[0]), len(candidates) > 1, true
}

// matchingSuffix keeps only the candidates whose path ends at a separator boundary with the
// reference — `internal/app/view.go` matches `app/view.go` but `myapp/view.go` does not.
func matchingSuffix(candidates []string, path string) []string {
	want := filepath.ToSlash(path)
	var out []string
	for _, c := range candidates {
		slashed := filepath.ToSlash(c)
		if slashed == want || strings.HasSuffix(slashed, "/"+want) {
			out = append(out, c)
		}
	}
	return out
}

// build indexes the project by basename, shallowest first.
//
// Shallowest first is the tie-break: with `main.go` at the root and another six directories
// down, the root one is the likelier subject of a bare reference. It is a heuristic, and
// Resolve reports when it had to use it.
func (r *Resolver) build() {
	r.index = map[string][]string{}
	count := 0
	_ = filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not worth failing the lookup
		}
		name := d.Name()
		if d.IsDir() {
			if path != r.root && (skipped[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= maxIndexed {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(r.root, path)
		if relErr != nil {
			//nolint:nilerr // a path the walk reached but cannot be expressed relative to
			// the root is one file missing from the index, not a reason to abandon it
			return nil
		}
		r.index[name] = append(r.index[name], rel)
		count++
		return nil
	})
	for name, paths := range r.index {
		if len(paths) > 1 {
			r.index[name] = byDepth(paths)
		}
	}
}

func byDepth(paths []string) []string {
	out := append([]string(nil), paths...)
	depth := func(p string) int { return strings.Count(filepath.ToSlash(p), "/") }
	// A hand-rolled insertion sort rather than sort.Slice: these slices are two or three
	// entries in every realistic case, and this keeps the ordering stable without a
	// comparator that has to spell out the tie-break twice.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && depth(out[j]) < depth(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
