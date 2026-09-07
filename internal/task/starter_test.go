package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindPrefersTheNameGoTaskWouldPick(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Taskfile.dist.yml", "Taskfile.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("version: '3'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := Find(dir), filepath.Join(dir, "Taskfile.yml"); got != want {
		t.Errorf("Find = %q, want %q", got, want)
	}
}

func TestFindIsEmptyWhereThereIsNone(t *testing.T) {
	if got := Find(t.TempDir()); got != "" {
		t.Errorf("Find = %q, want none", got)
	}
}

// A subdirectory of a project is not a project without a Taskfile — it is the same project,
// seen from further in. This is what stops the offer firing inside every repo that has one.
func TestFindUpWalksToTheProjectAbove(t *testing.T) {
	dir := t.TempDir()
	top := filepath.Join(dir, "Taskfile.yml")
	if err := os.WriteFile(top, []byte("version: '3'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "src", "pkg")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if got := FindUp(deep); got != top {
		t.Errorf("FindUp = %q, want %q", got, top)
	}
}

func TestWriteStarterLeavesSomethingGoTaskCanRead(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteStarter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, StarterName); path != want {
		t.Fatalf("wrote %q, want %q", path, want)
	}
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not installed")
	}
	tasks, err := Discover(dir)
	if err != nil {
		t.Fatalf("go-task could not read the starter: %v", err)
	}
	// Every task has a description, because the picker's whole left column is descriptions
	// and a starter that showed a blank one would be teaching the wrong lesson. And at
	// least one name has a colon in it, because folding is what taskui is for.
	if len(tasks) == 0 {
		t.Fatal("the starter lists no tasks — `task --init` writes a `default:`, which is " +
			"the one name Discover drops; this must not have gone the same way")
	}
	folds := false
	for _, task := range tasks {
		if task.Desc == "" {
			t.Errorf("%s has no desc", task.Name)
		}
		if strings.Contains(task.Name, ":") {
			folds = true
		}
	}
	if !folds {
		t.Error("nothing in the starter folds into a namespace")
	}
}

// The only caller has already established there is no Taskfile here, so anything in the way
// is a surprise — and a surprise is not something to overwrite.
func TestWriteStarterWillNotClobber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StarterName)
	if err := os.WriteFile(path, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteStarter(dir); err == nil {
		t.Fatal("expected a refusal")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "mine\n" {
		t.Errorf("the file was overwritten: %q", body)
	}
}
