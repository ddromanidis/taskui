package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/ddromanidis/taskui/internal/task"
	"github.com/ddromanidis/taskui/internal/watch"
)

// Re-reading the Taskfile when it changes.
//
// The list was read once, at startup, and never again — while `e` opens the very file it
// was read from, in your editor, from inside the program. So the intended loop was already
// "edit the Taskfile, come back", and coming back showed the list from before you edited:
// a task you had just added was not there, and a renamed one was still there under its old
// name until you quit.
//
// Watched by name rather than by walking the project, for the reason the tree walk exists:
// a Taskfile sits at the top of a directory whose build output churns, and a watcher on all
// of it would fire on every artefact. The files are known — go-task's own listing names the
// file each task is written in, which is how `includes:` are picked up as well.
//
// Re-reading is `task --list-all` again, which is the cheap call (~76ms); the JSON listing
// and the coverage walk are the expensive ones and are restarted in the background exactly
// as they were at startup.

// taskfileNames are the files a project's task list can be read from, before go-task has
// been asked. The listing replaces this with the real set — including every `includes:` —
// the moment it lands.
var taskfileNames = []string{
	"Taskfile.yml", "Taskfile.yaml",
	"taskfile.yml", "taskfile.yaml",
	"Taskfile.dist.yml", "Taskfile.dist.yaml",
	"taskfile.dist.yml", "taskfile.dist.yaml",
}

// reloaded is the answer to one re-read: a new list, or the reason there is not one.
type reloaded struct {
	tasks []task.Task
	err   error
}

// WatchTaskfile starts watching whatever the task list was read from.
//
// Opt-in, like StartEnrichment and for the same reason: it touches the filesystem, and the
// several hundred tests that build an App from a fixture have no Taskfile under them.
func (a *App) WatchTaskfile() {
	a.watchingTaskfile = true
	a.rewatchTaskfile()
}

// rewatchTaskfile points the watch at the current set of files.
//
// Called again when the listing lands, because that is when the includes become known: the
// root Taskfile is the only one that can be guessed at, and a project whose tasks live in
// four included files would otherwise only notice edits to the one at the top.
func (a *App) rewatchTaskfile() {
	if !a.watchingTaskfile {
		return
	}
	paths := a.taskfilePaths()
	if slices.Equal(paths, a.watchedTaskfiles) {
		return
	}
	if a.taskfileWatch != nil {
		a.taskfileWatch.Close()
		a.taskfileWatch = nil
	}
	a.watchedTaskfiles = paths
	if len(paths) == 0 {
		return
	}
	w, err := watch.Files(paths)
	if err != nil {
		// Not said out loud: this is a convenience nobody asked for by name, and a status
		// line about an inotify limit would push a real message off the screen.
		return
	}
	a.taskfileWatch = w
}

// taskfilePaths is every file the list is read from, sorted so the set can be compared.
func (a *App) taskfilePaths() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range a.Details {
		if d.Where.File != "" && !seen[d.Where.File] {
			seen[d.Where.File] = true
			out = append(out, d.Where.File)
		}
	}
	// The root Taskfile is watched whether or not the listing has arrived, and whether or
	// not any task is written in it: an `includes:`-only file at the top of a project
	// defines no tasks itself and is exactly where a new namespace gets added.
	//
	// The first name that exists, and then stop — the way go-task itself picks one. On a
	// case-insensitive filesystem every spelling in the list stats the same file, and
	// carrying all eight would make the watched set look different on macOS to the way it
	// looks on Linux for the same project.
	for _, name := range taskfileNames {
		path := filepath.Join(a.Root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
		break
	}
	slices.Sort(out)
	return out
}

// PollTaskfile kicks off a re-read if a Taskfile has changed and settled.
func (a *App) PollTaskfile() bool {
	if a.taskfileWatch == nil {
		return false
	}
	if _, ok := a.taskfileWatch.Poll(); !ok {
		return false
	}
	// One re-read at a time, and never a dropped one: a save that lands while the last read
	// is still running is remembered rather than ignored, because the whole point of this
	// is that the list agrees with the file.
	if a.reloadCh != nil {
		a.reloadPending = true
		return false
	}
	a.startReload()
	return true
}

func (a *App) startReload() {
	ch := make(chan reloaded, 1)
	a.reloadCh = ch
	root := a.Root
	go func() {
		tasks, err := task.Discover(root)
		ch <- reloaded{tasks: tasks, err: err}
		close(ch)
	}()
}

// collectReload takes a finished re-read, if there is one. Non-blocking, like
// collectDetails: it is called from the poll loop, which must not wait for anything.
func (a *App) collectReload() {
	if a.reloadCh == nil {
		return
	}
	select {
	case result := <-a.reloadCh:
		a.reloadCh = nil
		if a.reloadPending {
			a.reloadPending = false
			a.startReload()
		}
		if result.err != nil {
			// A Taskfile is unparseable for as long as it takes to finish typing one, and
			// blanking the list every time a save lands mid-edit would be worse than being
			// briefly out of date. The last good list stays on screen.
			a.Status = "the Taskfile does not parse — keeping the last list that did"
			return
		}
		a.ReplaceTasks(result.tasks)
	default:
	}
}

// ReplaceTasks swaps in a freshly read task list, keeping what the old one was carrying.
//
// The cursor stays on the task it was on, by name — the property that makes a pivot feel
// like a pivot rather than a navigation reset, and the same one that makes a reload feel
// like nothing happened. Folds are keyed by group name and survive on their own. Marks and
// a watched set are dropped where the task behind them is gone, because a mark on a task
// that no longer exists is a run waiting to fail.
func (a *App) ReplaceTasks(tasks []task.Task) {
	here := ""
	if ti := a.SelectedTask(); ti >= 0 && ti < len(a.Tasks) {
		here = a.Tasks[ti].Name
	}

	before := len(a.Tasks)
	a.Tasks = tasks
	live := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		live[t.Name] = true
	}

	keep := -1
	for i, t := range tasks {
		if t.Name == here {
			keep = i
			break
		}
	}
	a.Rebuild(keep)
	a.ReloadOutcomes()

	for name := range a.marked {
		if !live[name] {
			delete(a.marked, name)
		}
	}
	a.Watching = slices.DeleteFunc(a.Watching, func(name string) bool { return !live[name] })
	if len(a.Watching) == 0 && a.watcher != nil {
		a.watcher.Close()
		a.watcher = nil
	}

	// Where each task is written and what reaches what are both answers about the list that
	// just changed. Restarted rather than dropped: they are what `e` and the coverage
	// annotations run on, and stale ones are worse than late ones.
	a.detailCh = nil
	a.reachCh = nil
	if a.enriching {
		a.StartEnrichment()
	}
	if a.covering {
		a.StartCoverage()
	}

	a.Status = reloadSummary(before, len(tasks))
}

func reloadSummary(before, after int) string {
	switch {
	case after > before:
		return fmt.Sprintf("Taskfile changed — %d %s, %d new",
			after, plural(after, "task", "tasks"), after-before)
	case after < before:
		return fmt.Sprintf("Taskfile changed — %d %s, %d gone",
			after, plural(after, "task", "tasks"), before-after)
	default:
		return fmt.Sprintf("Taskfile changed — %d %s", after, plural(after, "task", "tasks"))
	}
}
