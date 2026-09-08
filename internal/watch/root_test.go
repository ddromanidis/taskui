package watch

import (
	"os"
	"path/filepath"
	"testing"
)

// The watched root is routinely a directory whose ancestors are on the ignore list: a
// linked git worktree lives under `.worktrees`, which this list names. Matching the whole
// absolute path made every file below such a root noise, so the walk registered nothing and
// the poll dropped everything — watch mode came up and then never fired.
func TestNoiseIsJudgedBelowTheWatchedRoot(t *testing.T) {
	for _, parent := range []string{".worktrees", "dist", "node_modules", ".config"} {
		t.Run(parent, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), parent, "project")
			if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			if isNoise(root, filepath.Join(root, "src", "main.go")) {
				t.Error("a source file under the root counted as noise")
			}
			if isNoise(root, filepath.Join(root, "src")) {
				t.Error("a source directory under the root counted as noise")
			}
		})
	}
}

// What the list is actually for still works: the ignored names below the root, and the
// droppings editors leave beside real files.
func TestNoiseBelowTheRootIsStillNoise(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"node_modules", "dist", ".git", "target/debug", "src/.hidden",
		"src/main.go~", "src/.main.go.swp", "src/4913",
	} {
		if !isNoise(root, filepath.Join(root, rel)) {
			t.Errorf("%s should be noise", rel)
		}
	}
}

// A watch registers directories that appear later by walking them, and those walks must be
// judged against the watch's root rather than against themselves.
func TestANewDirectoryIsJudgedAgainstTheWatchRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".worktrees", "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := Start(root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.root != root {
		t.Fatalf("root = %q, want %q", w.root, root)
	}
	if isNoise(w.root, filepath.Join(root, "pkg", "thing.go")) {
		t.Error("a file in a directory that appeared later counted as noise")
	}
}
