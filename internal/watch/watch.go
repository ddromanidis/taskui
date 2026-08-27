// Package watch re-runs a task when the files it cares about change.
//
// The loop this replaces is `task check`, read the output, edit, press `r`, repeat. Watch
// mode removes the third and fourth steps.
package watch

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ignored lists directories never worth watching: build output churns constantly, and
// watching it means every run triggers the next one forever.
var ignored = [...]string{
	"target",
	".git",
	".task",
	"node_modules",
	"dist",
	"public",
	".worktrees",
	".wrangler",
	".terraform",
}

// isNoise decides whether a change is worth a re-run. Editors write swap files, lock files
// and backups constantly, and each one would fire a build.
func isNoise(path string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		for _, dir := range ignored {
			if part == dir {
				return true
			}
		}
	}
	name := filepath.Base(path)
	// Vim swap files, Emacs autosaves and locks, editor backups, macOS metadata.
	return strings.HasPrefix(name, ".") ||
		strings.HasSuffix(name, "~") ||
		strings.HasSuffix(name, ".swp") ||
		strings.HasSuffix(name, ".swx") ||
		strings.HasSuffix(name, ".tmp") ||
		name == "4913" // vim's write-probe file
}

type Watch struct {
	watcher *fsnotify.Watcher
	// Settle collapses the burst a single save produces.
	Settle       time.Duration
	pendingSince time.Time
	LastChanged  string
}

// Start watches dir and everything under it.
//
// fsnotify is not recursive, so the tree is walked once and every directory worth watching
// is registered; directories created later are picked up as their creation event arrives.
func Start(dir string) (*Watch, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watch{watcher: watcher, Settle: 400 * time.Millisecond}
	if err := w.addTree(dir); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return w, nil
}

func (w *Watch) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is not worth failing the watch
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && isNoise(path) {
			return filepath.SkipDir
		}
		_ = w.watcher.Add(path)
		return nil
	})
}

// Close stops watching.
func (w *Watch) Close() {
	if w.watcher != nil {
		_ = w.watcher.Close()
	}
}

// Poll returns the path that triggered a re-run, once the burst has settled.
//
// A single save produces a write, a rename and a metadata change; firing on the first
// would start a build against a half-written file.
func (w *Watch) Poll() (string, bool) {
	sawAny := false
drain:
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return "", false
			}
			if isNoise(event.Name) {
				continue
			}
			// A directory that has just appeared has to be watched too, or everything
			// created inside it is invisible.
			if event.Op&fsnotify.Create != 0 {
				_ = w.addTree(event.Name)
			}
			w.LastChanged = event.Name
			sawAny = true
		case <-w.watcher.Errors:
			continue
		default:
			break drain
		}
	}

	if sawAny {
		w.pendingSince = time.Now()
		return "", false
	}
	if w.pendingSince.IsZero() {
		return "", false
	}
	if time.Since(w.pendingSince) >= w.Settle {
		w.pendingSince = time.Time{}
		return w.LastChanged, w.LastChanged != ""
	}
	return "", false
}
