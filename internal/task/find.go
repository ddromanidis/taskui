package task

import (
	"os"
	"path/filepath"
)

// Filenames are the files go-task will read a project's task list from, in the order it
// tries them. Kept here rather than in whoever needs them this week: the watcher, the
// "there is nothing here yet" offer and any future caller all have to agree on what counts
// as a Taskfile, and two copies of this list would agree only until one of them changed.
var Filenames = []string{
	"Taskfile.yml", "Taskfile.yaml",
	"taskfile.yml", "taskfile.yaml",
	"Taskfile.dist.yml", "Taskfile.dist.yaml",
	"taskfile.dist.yml", "taskfile.dist.yaml",
}

// Find is the Taskfile in dir itself, or "" if there is none.
//
// The first name that exists, and then stop — the way go-task itself picks one. On a
// case-insensitive filesystem every spelling stats the same file, so trying them all would
// make the answer differ between macOS and Linux for the same project.
func Find(dir string) string {
	for _, name := range Filenames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// FindUp is the Taskfile that governs dir: the one in it, or the nearest above it.
//
// Walking up is what go-task does, so a subdirectory of a project is not a project without
// a Taskfile — it is the same project, seen from further in. An offer to write a starter
// file that ignored this would fire in every `src/` of every repo that already has one.
//
// go-task stops the walk at a change of ownership; this stops at the filesystem root. The
// difference only shows on a tree you do not own, where go-task refuses to read a Taskfile
// this would find — and reporting go-task's own refusal is better than offering to write a
// second Taskfile underneath it.
func FindUp(dir string) string {
	for {
		if path := Find(dir); path != "" {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
