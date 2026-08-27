package loc

import (
	"os"
	"path/filepath"
	"testing"
)

// The point of the extension requirement is that these all come from real tools, and a
// parser that handles four of them is not worth having.
func TestTheShapesRealToolsPrint(t *testing.T) {
	for _, c := range []struct {
		what string
		text string
		path string
		line int
		col  int
	}{
		{"go build", "internal/app/view.go:212:5: undefined: foo", "internal/app/view.go", 212, 5},
		{"go vet", "./main.go:9:2: unreachable code", "./main.go", 9, 2},
		{"go test", "    view_test.go:88: want 3, got 4", "view_test.go", 88, 0},
		{"golangci-lint", "internal/run/run.go:40:1: cyclop: high complexity", "internal/run/run.go", 40, 1},
		{"rustc", "error[E0308]: at src/main.rs:12:9", "src/main.rs", 12, 9},
		{"gcc", "hello.c:5:12: warning: unused variable", "hello.c", 5, 12},
		{"tsc", "src/index.ts(4,7): oops — but the colon form too: src/x.ts:4:7", "src/x.ts", 4, 7},
		{"eslint-ish", "/abs/path/app.jsx:22:1  error  no-undef", "/abs/path/app.jsx", 22, 1},
		{"node stack", "    at Object.<anonymous> (/srv/app/server.js:10:15)", "/srv/app/server.js", 10, 15},
		{"python", `  File "/srv/app/main.py", line 42, in handler`, "/srv/app/main.py", 42, 0},
		{"pytest", "tests/test_api.py:31: AssertionError", "tests/test_api.py", 31, 0},
		{"ruby", "app/models/user.rb:17:in `save'", "app/models/user.rb", 17, 0},
		{"shellcheck", "In scripts/deploy.sh:4:1: note", "scripts/deploy.sh", 4, 1},
		{"yaml", "Taskfile.yml:31: did not find expected key", "Taskfile.yml", 31, 0},
	} {
		t.Run(c.what, func(t *testing.T) {
			got, ok := First(c.text)
			if !ok {
				t.Fatalf("found nothing in %q", c.text)
			}
			if got.Path != c.path || got.Line != c.line || got.Col != c.col {
				t.Errorf("got %s:%d:%d, want %s:%d:%d", got.Path, got.Line, got.Col, c.path, c.line, c.col)
			}
		})
	}
}

// Every one of these appears constantly in build output. A highlight that lands on them is
// a highlight you learn to ignore, which costs more than the feature is worth.
func TestNumbersThatAreNotLocations(t *testing.T) {
	for _, text := range []string{
		"elapsed 10:30:45",
		"ratio 12:34",
		"listening on localhost:8080",
		"  ok   github.com/x/y  1.234s",
		"--- FAIL: TestThing (0.00s)",
		"progress: 45:100",
		"time=2026-08-27T13:39:02Z level=info",
		"key: value",
		"[13:22:01] built in 4s",
	} {
		if got, ok := First(text); ok {
			t.Errorf("%q matched %s:%d — it should not have", text, got.Path, got.Line)
		}
	}
}

// A URL has a colon and digits and turns up in output all the time. `example.com:8080`
// looks exactly like a file with a `.com` extension, so the syntax alone cannot rule it
// out — but the scheme's `//` can, and anything left over dies at the filesystem.
func TestURLsDoNotBecomeLocations(t *testing.T) {
	for _, text := range []string{
		"fetching https://example.com:8080/x",
		"see http://localhost:3000",
	} {
		for _, l := range All(text) {
			if l.Path == "example.com" || l.Path == "localhost" {
				t.Errorf("%q produced %s:%d", text, l.Path, l.Line)
			}
		}
	}
}

// The offsets are what the renderer underlines. Pointing at the wrong half of the line is
// worse than not pointing at all.
func TestTheOffsetsCoverTheWholeReference(t *testing.T) {
	text := "internal/app/view.go:212:5: undefined: foo"
	got, ok := First(text)
	if !ok {
		t.Fatal("found nothing")
	}
	if want := "internal/app/view.go:212:5"; text[got.Start:got.End] != want {
		t.Errorf("offsets cover %q, want %q", text[got.Start:got.End], want)
	}
}

// A path deeper in the line still has to be measured from its own start, not from an
// earlier segment that happens to look like a path too.
func TestAPathIsNotMatchedFromItsMiddle(t *testing.T) {
	text := "compiling src/app/view.go:4:1"
	got, _ := First(text)
	if got.Path != "src/app/view.go" {
		t.Errorf("got %q, want the whole path", got.Path)
	}
}

func TestEveryReferenceOnALine(t *testing.T) {
	text := "moved a.go:1:1 to b.go:2:2"
	all := All(text)
	if len(all) != 2 {
		t.Fatalf("found %d, want 2", len(all))
	}
	if all[0].Path != "a.go" || all[1].Path != "b.go" {
		t.Errorf("got %s and %s", all[0].Path, all[1].Path)
	}
}

// --- resolution ---------------------------------------------------------------------

func project(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		path := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAPathRelativeToTheProjectResolves(t *testing.T) {
	root := project(t, "internal/app/view.go")
	abs, ambiguous, ok := NewResolver(root).Resolve("internal/app/view.go")
	if !ok || ambiguous {
		t.Fatalf("ok=%v ambiguous=%v", ok, ambiguous)
	}
	if abs != filepath.Join(root, "internal/app/view.go") {
		t.Errorf("got %s", abs)
	}
}

// This is the case that matters most: `go test` prints the basename and nothing else, and
// without the index every Go test failure would be unreachable.
func TestABareBasenameIsFoundByIndex(t *testing.T) {
	root := project(t, "internal/app/view_test.go", "README.md")
	abs, ambiguous, ok := NewResolver(root).Resolve("view_test.go")
	if !ok {
		t.Fatal("not found")
	}
	if ambiguous {
		t.Error("only one candidate, should not be ambiguous")
	}
	if abs != filepath.Join(root, "internal/app/view_test.go") {
		t.Errorf("got %s", abs)
	}
}

func TestTwoFilesWithOneNameTakeTheShallowestAndSaySo(t *testing.T) {
	root := project(t, "cmd/api/main.go", "cmd/deep/down/here/main.go")
	abs, ambiguous, ok := NewResolver(root).Resolve("main.go")
	if !ok {
		t.Fatal("not found")
	}
	if !ambiguous {
		t.Error("two candidates should be reported as ambiguous")
	}
	if abs != filepath.Join(root, "cmd/api/main.go") {
		t.Errorf("got %s, want the shallowest", abs)
	}
}

// A file that sits exactly where the output said it does is not a guess, however many other
// files share its name — so it must not be reported as one.
func TestADirectHitIsNeverAmbiguous(t *testing.T) {
	root := project(t, "main.go", "cmd/deep/down/main.go")
	abs, ambiguous, ok := NewResolver(root).Resolve("main.go")
	if !ok || ambiguous {
		t.Fatalf("ok=%v ambiguous=%v", ok, ambiguous)
	}
	if abs != filepath.Join(root, "main.go") {
		t.Errorf("got %s", abs)
	}
}

// A partial path is more information than a basename, and it should be used: `app/view.go`
// must not resolve to `other/view.go` just because that one is shallower.
func TestAPartialPathBeatsAShallowerBasename(t *testing.T) {
	root := project(t, "view.go", "internal/app/view.go")
	abs, _, ok := NewResolver(root).Resolve("app/view.go")
	if !ok {
		t.Fatal("not found")
	}
	if abs != filepath.Join(root, "internal/app/view.go") {
		t.Errorf("got %s, want the one that matches the suffix", abs)
	}
}

// `myapp/view.go` ends with the same letters as `app/view.go` but is a different path.
func TestSuffixMatchingRespectsPathBoundaries(t *testing.T) {
	root := project(t, "src/myapp/view.go")
	abs, _, ok := NewResolver(root).Resolve("app/view.go")
	if !ok {
		t.Fatal("the basename fallback should still find it")
	}
	if abs != filepath.Join(root, "src/myapp/view.go") {
		t.Errorf("got %s", abs)
	}
}

func TestAMissingFileIsNotResolved(t *testing.T) {
	root := project(t, "main.go")
	if _, _, ok := NewResolver(root).Resolve("nope.go"); ok {
		t.Error("resolved something that is not there")
	}
}

// The index exists to find source, and node_modules is not source — it is also frequently
// larger than everything else combined.
func TestTheIndexSkipsBuildOutput(t *testing.T) {
	root := project(t, "node_modules/left-pad/index.js", "src/index.js")
	abs, _, ok := NewResolver(root).Resolve("index.js")
	if !ok {
		t.Fatal("not found")
	}
	if abs != filepath.Join(root, "src/index.js") {
		t.Errorf("got %s, want the one outside node_modules", abs)
	}
}

func TestADirectoryIsNotAFile(t *testing.T) {
	root := project(t, "internal/app/view.go")
	if _, _, ok := NewResolver(root).Resolve("internal"); ok {
		t.Error("a directory should not resolve")
	}
}
