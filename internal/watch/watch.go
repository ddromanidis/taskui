// Package watch re-runs a task when the files it cares about change.
//
// The loop this replaces is `task check`, read the output, edit, press `r`, repeat. Watch
// mode removes the third and fourth steps.
package watch

import (
	"errors"
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
//
// The directory names are matched against the path *below the watched root*, not against
// the whole thing. Callers hand this absolute paths, so matching all of it meant any
// ancestor on the list poisoned everything under it — and one of the names on the list is
// `.worktrees`, which is exactly where this tool expects to be run from. In a linked
// worktree the walk skipped every subdirectory and the poll dropped every event, so watch
// mode registered nothing and then silently never fired again. A checkout under `dist`, or
// anywhere below a dotted home directory, did the same.
func isNoise(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
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
	// names, when set, is the file names this watch is about — everything else in the
	// directories it registered is ignored.
	names map[string]bool
	// root is what noise is judged relative to. Kept on the watch rather than passed down,
	// because Poll calls addTree again for every directory that appears, and judging those
	// against themselves would ignore where they sit.
	root string
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
	w := &Watch{watcher: watcher, Settle: 400 * time.Millisecond, root: dir}
	if err := w.addTree(dir); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return w, nil
}

// Files watches a named set of files rather than a whole tree.
//
// The tree walk Start does is the wrong shape for a Taskfile: it sits at the top of a
// project whose build output churns, and watching all of it to hear about one file would
// fire on every artefact. fsnotify reports on directories either way, so the parents are
// what gets registered and the names are what filter the events back down.
//
// Matched by base name, not by path. An event carries the directory as it was registered,
// so comparing whole paths would depend on which spelling of a symlinked temp directory the
// caller happened to pass — and a second file of the same name in another watched directory
// is, for the only caller this has, exactly the thing worth hearing about.
func Files(paths []string) (*Watch, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watch{watcher: watcher, Settle: 400 * time.Millisecond, names: map[string]bool{}}
	dirs := map[string]bool{}
	for _, path := range paths {
		if path == "" {
			continue
		}
		w.names[filepath.Base(path)] = true
		dirs[filepath.Dir(path)] = true
	}
	if len(w.names) == 0 {
		_ = watcher.Close()
		return nil, errors.New("no files to watch")
	}
	for dir := range dirs {
		_ = watcher.Add(dir)
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
		if path != root && isNoise(w.root, path) {
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
			// A named watch answers for its own files and nothing else — including the
			// editor droppings beside them, which never match a name it was given.
			if w.names != nil {
				if !w.names[filepath.Base(event.Name)] {
					continue
				}
				w.LastChanged = event.Name
				sawAny = true
				continue
			}
			if isNoise(w.root, event.Name) {
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
