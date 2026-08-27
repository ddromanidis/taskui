package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildOutputIsIgnored(t *testing.T) {
	for _, path := range []string{
		"/proj/target/debug/taskui",
		"/proj/.git/index",
		"/proj/.task/checksum/lint",
		"/proj/app/node_modules/x/index.js",
	} {
		if !isNoise(path) {
			t.Errorf("%s should be ignored", path)
		}
	}
}

// Editors write a lot of things that are not source changes.
func TestEditorScratchFilesAreIgnored(t *testing.T) {
	for _, path := range []string{
		"/proj/src/.main.go.swp",
		"/proj/src/main.go~",
		"/proj/src/4913",
		"/proj/.DS_Store",
	} {
		if !isNoise(path) {
			t.Errorf("%s should be ignored", path)
		}
	}
}

func TestSourceFilesAreNot(t *testing.T) {
	for _, path := range []string{
		"/proj/src/main.go",
		"/proj/Taskfile.yml",
		"/proj/backend/internal/api/api.go",
	} {
		if isNoise(path) {
			t.Errorf("%s should not be ignored", path)
		}
	}
}

// One save is several events; firing on the first would build a half-written file.
func TestABurstOfEventsSettlesBeforeFiring(t *testing.T) {
	dir := t.TempDir()
	w, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Settle = 120 * time.Millisecond

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Give the platform watcher a moment to deliver.
	deadline := time.Now().Add(5 * time.Second)
	fired := ""
	for time.Now().Before(deadline) {
		if path, ok := w.Poll(); ok {
			fired = path
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if fired == "" {
		t.Fatal("the change never fired")
	}
	if _, ok := w.Poll(); ok {
		t.Error("and it should only fire once")
	}
}
