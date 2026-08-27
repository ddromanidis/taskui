// Package store keeps finished runs on disk so they can be searched later.
//
// The format is deliberately boring: a directory per run, a `manifest.json` for the
// structure, and two plain files per task. `<task>.txt` is the ANSI-stripped text — what
// search reads, and what plain `rg` from your shell reads too — while `<task>.ansi` keeps
// the escape sequences so an archived run still renders in colour. A format that needs
// taskui to read it would be a worse format.
//
// Everything written here has already been through the redact package, and the directory
// is locked to the owner regardless.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/run"
)

// KeepRuns is how many runs to keep. A full `task all` on a large repo is a lot of build
// output and it accumulates fast.
const KeepRuns = 50

type TaskEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Lines      int    `json:"lines"`
	// File is the basename, without extension: `<file>.txt` and `<file>.ansi`.
	File string `json:"file"`
}

type Manifest struct {
	ID string `json:"id"`
	// Root is the task that was invoked.
	Root string `json:"root"`
	// Args are the extra argv it was invoked with. Omitted-friendly so manifests written
	// before args existed still load.
	Args []string `json:"args,omitempty"`
	// Force likewise defaults to false for older manifests.
	Force bool `json:"force,omitempty"`
	// Dir is the project directory it ran in.
	Dir         string `json:"dir"`
	StartedUnix int64  `json:"started_unix"`
	DurationMs  int64  `json:"duration_ms"`
	Exit        int    `json:"exit"`
	// RedactedSecrets is how many distinct secrets were masked out of this run.
	RedactedSecrets int                 `json:"redacted_secrets"`
	Tasks           []TaskEntry         `json:"tasks"`
	Edges           map[string][]string `json:"edges"`
}

func (m Manifest) Failed() bool { return m.Exit != 0 }

func (m Manifest) Command() string {
	force := ""
	if m.Force {
		force = " --force"
	}
	if len(m.Args) == 0 {
		return "task " + m.Root + force
	}
	return "task " + m.Root + force + " " + strings.Join(m.Args, " ")
}

// StateDir is `$XDG_STATE_HOME/taskui` if set, else `~/.local/state/taskui`.
func StateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "taskui")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".local/state/taskui")
}

func runsDir(base string) string { return filepath.Join(base, "runs") }

// safeName exists because task names contain colons and can contain slashes; neither
// belongs in a filename.
func safeName(task string) string {
	var b strings.Builder
	for _, c := range task {
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if alnum || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

func lockDown(path string, dir bool) error {
	mode := os.FileMode(0o600)
	if dir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

// Save writes a finished run into base and returns the directory it landed in.
func Save(base, projectDir string, r *run.Run) (string, error) {
	started := time.Now().Unix()
	if r.HasDuration {
		started -= int64(r.Duration.Seconds())
	}
	// Seconds alone collide when two runs finish in the same second, which happens
	// constantly with fast tasks; the task name disambiguates.
	id := fmt.Sprintf("%d-%s", started, safeName(r.Root))

	dir := filepath.Join(runsDir(base), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := lockDown(runsDir(base), true); err != nil {
		return "", err
	}
	if err := lockDown(dir, true); err != nil {
		return "", err
	}

	var entries []TaskEntry
	for _, name := range r.TaskNames() {
		t := r.Tasks[name]
		file := safeName(name)

		var plain, ansi strings.Builder
		for _, l := range t.Lines {
			plain.WriteString(l.Plain)
			plain.WriteByte('\n')
			ansi.WriteString(l.Raw)
			ansi.WriteByte('\n')
		}

		txt := filepath.Join(dir, file+".txt")
		if err := os.WriteFile(txt, []byte(plain.String()), 0o600); err != nil {
			return "", err
		}
		if err := lockDown(txt, false); err != nil {
			return "", err
		}

		esc := filepath.Join(dir, file+".ansi")
		if err := os.WriteFile(esc, []byte(ansi.String()), 0o600); err != nil {
			return "", err
		}
		if err := lockDown(esc, false); err != nil {
			return "", err
		}

		entries = append(entries, TaskEntry{
			Name:       name,
			Status:     t.Status.String(),
			DurationMs: t.Duration().Milliseconds(),
			Lines:      len(t.Lines),
			File:       file,
		})
	}

	manifest := Manifest{
		ID:              id,
		Root:            r.Root,
		Args:            r.Args,
		Force:           r.Force,
		Dir:             projectDir,
		StartedUnix:     started,
		DurationMs:      r.Duration.Milliseconds(),
		Exit:            exitOf(r),
		RedactedSecrets: r.RedactedSecrets,
		Tasks:           entries,
		Edges:           r.Graph.Edges,
	}

	blob, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return "", err
	}
	if err := lockDown(path, false); err != nil {
		return "", err
	}

	if _, err := Prune(base, KeepRuns); err != nil {
		return "", err
	}
	return dir, nil
}

func exitOf(r *run.Run) int {
	if r.HasExit {
		return r.Exit
	}
	return -1
}

// List returns every stored run, newest first.
func List(base string) []Manifest {
	entries, err := os.ReadDir(runsDir(base))
	if err != nil {
		return nil
	}
	var out []Manifest
	for _, e := range entries {
		blob, err := os.ReadFile(filepath.Join(runsDir(base), e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(blob, &m) == nil {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedUnix != out[j].StartedUnix {
			return out[i].StartedUnix > out[j].StartedUnix
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func RunDir(base, id string) string { return filepath.Join(runsDir(base), id) }

// Load rebuilds a stored run so it can be folded and searched like a live one.
//
// Reading `.txt` and `.ansi` side by side is what gives an archived run its colour back:
// the stripped half is what search matches on, the escaped half is what renders. They are
// written a line at a time from the same buffer, so they stay in step.
func Load(base string, manifest Manifest) (*run.Run, error) {
	dir := RunDir(base, manifest.ID)
	tasks := map[string]*run.TaskRun{}
	var order []string

	for _, entry := range manifest.Tasks {
		plain := readLines(filepath.Join(dir, entry.File+".txt"))
		raws := readLines(filepath.Join(dir, entry.File+".ansi"))

		lines := make([]run.Line, 0, len(plain))
		for i, p := range plain {
			// Fall back to the stripped text if the sidecar is short or missing — losing
			// colour is survivable, losing the line is not.
			r := p
			if i < len(raws) {
				r = raws[i]
			}
			lines = append(lines, run.Restored(r, p))
		}

		if len(lines) > 0 {
			order = append(order, entry.Name)
		}
		tasks[entry.Name] = run.RestoredTask(
			run.StatusFromString(entry.Status),
			lines,
			time.Duration(entry.DurationMs)*time.Millisecond,
		)
	}

	edges := manifest.Edges
	if edges == nil {
		edges = map[string][]string{}
	}

	return run.FromStored(run.Stored{
		Root:            manifest.Root,
		Args:            manifest.Args,
		Graph:           graph.Graph{Edges: edges},
		Tasks:           tasks,
		Order:           order,
		Exit:            manifest.Exit,
		Duration:        time.Duration(manifest.DurationMs) * time.Millisecond,
		RedactedSecrets: manifest.RedactedSecrets,
	}), nil
}

// readLines splits a file the way Rust's `str::lines` does: no trailing empty element for
// a file that ends in a newline.
func readLines(path string) []string {
	blob, err := os.ReadFile(path)
	if err != nil || len(blob) == 0 {
		return nil
	}
	text := strings.TrimSuffix(string(blob), "\n")
	if text == "" {
		return nil
	}
	out := strings.Split(text, "\n")
	for i := range out {
		out[i] = strings.TrimSuffix(out[i], "\r")
	}
	return out
}

// Outcome is how a task went, and when.
type Outcome struct {
	Ok       bool
	WhenUnix int64
}

// LastOutcomes reports the last outcome of every task seen in this project's stored runs.
//
// Keyed by task name, newest wins. Built from the per-task entries rather than just the
// run roots, so a single `task all` teaches it about `lint`, `backend:lint` and every
// other task that run touched.
func LastOutcomes(base, project string) map[string]Outcome {
	out := map[string]Outcome{}
	// List is newest first, so the first sighting of a task is its latest.
	for _, manifest := range List(base) {
		if manifest.Dir != project {
			continue
		}
		for _, entry := range manifest.Tasks {
			if entry.Status == "Pending" || entry.Status == "Skipped" {
				continue
			}
			if _, seen := out[entry.Name]; seen {
				continue
			}
			out[entry.Name] = Outcome{Ok: entry.Status == "Ok", WhenUnix: manifest.StartedUnix}
		}
	}
	return out
}

// Prune drops the oldest runs beyond keep.
func Prune(base string, keep int) (int, error) {
	all := List(base)
	removed := 0
	for i := keep; i < len(all); i++ {
		if os.RemoveAll(RunDir(base, all[i].ID)) == nil {
			removed++
		}
	}
	return removed, nil
}
