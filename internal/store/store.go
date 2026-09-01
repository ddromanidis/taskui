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
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ddromanidis/taskui/internal/graph"
	"github.com/ddromanidis/taskui/internal/run"
)

// KeepRuns is how many runs' *output* to keep. A full `task all` on a large repo is a lot of
// build output and it accumulates fast.
const KeepRuns = 50

// KeepHistory is how many runs to remember per project, which is a different number because
// it buys a different thing.
//
// Output is unbounded — kilobytes for a `task fmt`, megabytes for a `task all` carrying build
// logs — so it has to be capped tightly, and KeepRuns caps it across every project at once.
// A ledger entry is the manifest without any of that: 461 bytes at the median and 1.7KB at
// the worst, so ten thousand of them is under 5MB.
//
// Capping the two together was the mistake. A busy afternoon in one repository would evict
// another's history entirely, and with it the three things the archive is kept for: a
// timeline has one point to draw, `--flaky` needs one commit to appear twice, and neither
// survives an eviction that counts every project's runs against the same fifty. Only a diff
// genuinely needs the text, so only a diff stays bounded by KeepRuns.
const KeepHistory = 2000

// compactAt is how many lines the ledger reaches before it is rewritten.
//
// Rewriting on every save would be a read-modify-write of the whole file per run, which is
// both the slow way and the racy way. Appending is neither: one line per run, opened
// O_APPEND, and a write below PIPE_BUF is atomic — several taskui processes (six slots, more
// than one repository, an agent per worktree) can append at once without a lock.
const compactAt = 10000

type TaskEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Note is why it did not run, when it did not. Omitted-friendly, so manifests written
	// before skips were explained still load.
	Note       string `json:"note,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Lines      int    `json:"lines"`
	// File is the basename, without extension: `<file>.txt` and `<file>.ansi`.
	File string `json:"file"`
}

// ManifestVersion is the archive format this build writes, and the highest it will read.
//
// The archive is the one thing here that users accumulate and cannot regenerate, so the
// moment its shape stops being ours is the moment it needs a number on it. Every field added
// so far has been `omitempty`, which is why old runs still load — but that is care, not a
// policy, and it only works while changes are additive. A reader that meets a version it
// does not know skips the run rather than guessing at it: an old binary garbling a newer
// archive is worse than one admitting it cannot read it.
const ManifestVersion = 1

type Manifest struct {
	// Version is the format. Absent means 0, which is every manifest written before this
	// existed — all of them readable, since nothing has changed shape yet.
	Version int    `json:"version"`
	ID      string `json:"id"`
	// Root is the task that was invoked.
	Root string `json:"root"`
	// Args are the extra argv it was invoked with. Omitted-friendly so manifests written
	// before args existed still load.
	Args []string `json:"args,omitempty"`
	// Force likewise defaults to false for older manifests.
	Force bool `json:"force,omitempty"`
	// Dir is the project directory it ran in.
	Dir string `json:"dir"`
	// Commit is the git revision the project was at. Recorded so "passed and failed at the
	// same commit" — which is what flaky means and what alternating outcomes only hint at —
	// is a fact rather than a guess. Empty for a directory that is not a git checkout, and
	// for every manifest written before this existed.
	Commit      string `json:"commit,omitempty"`
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

// historyPath is the ledger: one manifest per line, newest last.
//
// Beside `runs/` rather than in `~/.config`, because it is written by the program and not by
// you — deleting the state directory has always been how you remove everything taskui
// accumulated, and a second home for half of it would quietly stop being true.
func historyPath(base string) string { return filepath.Join(base, "history.ndjson") }

// appendHistory adds one run to the ledger.
//
// Errors are returned but a caller is expected to ignore them: the run directory is already
// written at this point, and losing the ledger line costs a row in a timeline. Failing the
// save over it would cost the output as well.
func appendHistory(base string, m Manifest) error {
	blob, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(historyPath(base), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(blob, '\n'))
	return err
}

// readHistory parses the ledger, oldest first.
//
// A line that will not parse is skipped rather than fatal. The file is append-only from
// several processes, so a torn last line is a thing that can happen; one unreadable run is
// not a reason to lose the other two thousand.
func readHistory(base string) []Manifest {
	blob, err := os.ReadFile(historyPath(base))
	if err != nil {
		return nil
	}
	var out []Manifest
	for line := range strings.SplitSeq(string(blob), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m Manifest
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		// Written by something newer than this build. Reading it would be guessing.
		if m.Version > ManifestVersion {
			continue
		}
		out = append(out, m)
	}
	return out
}

// compactHistory rewrites the ledger keeping the newest KeepHistory runs of each project, and
// does nothing until the file is long enough to be worth it.
//
// The rewrite is the one moment the append-only story does not hold: a run finishing inside
// the rename loses its line. That is one row in one timeline, once every few thousand runs,
// against a lock on every write for the rest of them.
func compactHistory(base string) error {
	all := readHistory(base)
	if len(all) <= compactAt {
		return nil
	}

	// Newest first per project, so the tail of each is what gets dropped.
	sortNewestFirst(all)
	kept := map[string]int{}
	keep := make([]Manifest, 0, len(all))
	for _, m := range all {
		if kept[m.Dir] >= KeepHistory {
			continue
		}
		kept[m.Dir]++
		keep = append(keep, m)
	}

	var b strings.Builder
	for _, m := range slices.Backward(keep) {
		blob, err := json.Marshal(m)
		if err != nil {
			continue
		}
		b.Write(blob)
		b.WriteByte('\n')
	}

	tmp := historyPath(base) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, historyPath(base))
}

// sortNewestFirst is the order every reader wants: most recent run first, ties broken by id
// so two runs that started in the same second still order the same way twice.
func sortNewestFirst(m []Manifest) {
	sort.SliceStable(m, func(i, j int) bool {
		if m[i].StartedUnix != m[j].StartedUnix {
			return m[i].StartedUnix > m[j].StartedUnix
		}
		return m[i].ID > m[j].ID
	})
}

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
	// Before this run's own directory exists, or it would be absorbed here and appended
	// again below.
	backfillHistory(base)

	started := time.Now().Unix()
	if r.HasDuration {
		started -= int64(r.Duration.Seconds())
	}
	// Seconds alone collide when two runs finish in the same second, which happens
	// constantly with fast tasks; the task name disambiguates most of them, and a counter
	// takes the rest. Two runs of the *same* task inside one second used to be one run —
	// which is exactly the pair a timeline is built to show you, so silently keeping the
	// second and dropping the first is the worst place for that to happen.
	id := uniqueID(base, fmt.Sprintf("%d-%s", started, safeName(r.Root)))

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
			Note:       t.Note,
			DurationMs: t.Duration().Milliseconds(),
			Lines:      len(t.Lines),
			File:       file,
		})
	}

	manifest := Manifest{
		Version:         ManifestVersion,
		ID:              id,
		Root:            r.Root,
		Args:            r.Args,
		Force:           r.Force,
		Dir:             projectDir,
		Commit:          headCommit(projectDir),
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

	// Best-effort: the run is already safely on disk, and failing to remember it is not a
	// reason to report the save as failed. Before the prune, or the prune would delete
	// output whose only record is the directory it is about to remove.
	_ = appendHistory(base, manifest)
	_ = compactHistory(base)

	if _, err := Prune(base, KeepRuns); err != nil {
		return "", err
	}
	return dir, nil
}

// backfillHistory writes any run directory the ledger has not heard of into it.
//
// This is the migration and it needs no flag day: the first save after an upgrade absorbs
// whatever the fifty surviving directories still hold, and from then on the ledger is ahead
// of them. It also picks up anything an older build wrote in the meantime, so running two
// versions of taskui alternately does not lose runs.
func backfillHistory(base string) {
	known := map[string]bool{}
	for _, m := range readHistory(base) {
		known[m.ID] = true
	}
	missing := make([]Manifest, 0)
	for _, m := range scanRuns(base) {
		if !known[m.ID] {
			missing = append(missing, m)
		}
	}
	sortNewestFirst(missing)
	// Oldest first, so the file stays in the order it would have been written in.
	for _, m := range slices.Backward(missing) {
		_ = appendHistory(base, m)
	}
}

// uniqueID appends a counter until the id names a directory that does not exist yet.
//
// Zero-padded so the suffixes still sort the way List expects: `.10` has to come after
// `.02`, and lexically it only does with the padding.
func uniqueID(base, want string) string {
	if !exists(filepath.Join(runsDir(base), want)) {
		return want
	}
	for n := 1; n < 100; n++ {
		candidate := fmt.Sprintf("%s.%02d", want, n)
		if !exists(filepath.Join(runsDir(base), candidate)) {
			return candidate
		}
	}
	// A hundred runs of one task inside one second is not a case worth more code than this;
	// the last one wins, as it always did.
	return want
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// headCommit is the project's git revision, or empty.
//
// Best effort by design: not every project is a checkout, and a run in one that is not is
// still a run worth keeping. A dirty tree is reported as the commit plus `-dirty`, because
// two runs of uncommitted work are not two runs of the same code and calling them flaky
// would be wrong.
func headCommit(dir string) string {
	head, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil || head == "" {
		return ""
	}
	if status, err := gitOutput(dir, "status", "--porcelain"); err == nil && status != "" {
		return head + "-dirty"
	}
	return head
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func exitOf(r *run.Run) int {
	if r.HasExit {
		return r.Exit
	}
	return -1
}

// List returns every run taskui remembers, newest first — which is more than it still holds
// the output of.
//
// Both sources, merged on id. The ledger is the long memory and outlives KeepRuns; the
// directories are what an older build wrote and what the current one is still holding text
// for, and a run present in only one of them is a real run either way. Nothing here writes:
// a directory the ledger has not heard of is absorbed by the next Save.
func List(base string) []Manifest {
	out := readHistory(base)
	seen := make(map[string]bool, len(out))
	for _, m := range out {
		seen[m.ID] = true
	}
	for _, m := range scanRuns(base) {
		if !seen[m.ID] {
			out = append(out, m)
		}
	}
	sortNewestFirst(out)
	return out
}

// scanRuns reads the manifest out of every run directory.
func scanRuns(base string) []Manifest {
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
		if json.Unmarshal(blob, &m) != nil {
			continue
		}
		// Written by something newer than this build. Reading it would be guessing.
		if m.Version > ManifestVersion {
			continue
		}
		out = append(out, m)
	}
	return out
}

// HasOutput reports whether a remembered run still has its text on disk.
//
// A ledger entry outlives its directory by design, so everything that wants to read what a
// run printed — the diff, the quickfix list, reopening it — has to ask first rather than
// discover it as an empty file.
func HasOutput(base, id string) bool {
	return exists(filepath.Join(RunDir(base, id), "manifest.json"))
}

func RunDir(base, id string) string { return filepath.Join(runsDir(base), id) }

// Load rebuilds a stored run so it can be folded and searched like a live one.
//
// Reading `.txt` and `.ansi` side by side is what gives an archived run its colour back:
// the stripped half is what search matches on, the escaped half is what renders. They are
// written a line at a time from the same buffer, so they stay in step.
func Load(base string, manifest Manifest) (*run.Run, error) {
	// The ledger remembers runs whose output has been pruned. Rebuilding one of those gives a
	// run with every task empty, which reads as "it printed nothing" rather than as "that is
	// no longer here" — so say which it is.
	if !HasOutput(base, manifest.ID) {
		return nil, fmt.Errorf("the output of %s is no longer stored (kept: the last %d runs)",
			manifest.ID, KeepRuns)
	}
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
		restored := run.RestoredTask(
			run.StatusFromString(entry.Status),
			lines,
			time.Duration(entry.DurationMs)*time.Millisecond,
		)
		restored.Note = entry.Note
		tasks[entry.Name] = restored
	}

	edges := manifest.Edges
	if edges == nil {
		edges = map[string][]string{}
	}

	return run.FromStored(run.Stored{
		ID:              manifest.ID,
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

// Point is one appearance of a task in the archive: how it went that time, and when.
type Point struct {
	RunID string
	// Root is the run it was part of. `test:one` reached from a `task all` and from a `task
	// test:one` are the same task and different circumstances, and the difference explains
	// most of the surprising durations.
	Root     string
	WhenUnix int64
	// Commit is the git revision the project was at, or empty.
	Commit     string
	Status     string
	DurationMs int64
	Lines      int
	// File is the basename its output was written under, for reading it back.
	File string
}

func (p Point) Ok() bool { return p.Status == "Ok" }

// Command is how the run this task was part of was invoked, for naming it on screen.
func (p Point) Command() string { return "task " + p.Root }

// Timeline is every stored appearance of one task, newest first.
//
// This is the question the archive was kept for and could not answer: `--search` greps for
// a string across runs, and the history list is every run in order — neither of them is
// "how has this one task been going". The manifests have held the answer all along.
//
// Pending and skipped appearances are dropped: a task go-task decided was up to date did
// not run, and a row saying so is a row that makes the trend harder to read.
func Timeline(base, project, task string) []Point {
	var out []Point
	for _, m := range List(base) {
		if project != "" && m.Dir != project {
			continue
		}
		for _, e := range m.Tasks {
			if e.Name != task || e.Status == "Pending" || e.Status == "Skipped" {
				continue
			}
			out = append(out, Point{
				RunID: m.ID, Root: m.Root, WhenUnix: m.StartedUnix, Commit: m.Commit,
				Status: e.Status, DurationMs: e.DurationMs, Lines: e.Lines, File: e.File,
			})
		}
	}
	return out
}

// LastGreen is the most recent stored run in which this task succeeded.
//
// `skip` is a run id to ignore, so a diff of a stored run against the archive does not find
// itself.
// Runs whose output has been pruned are passed over rather than returned: a timeline shows
// them because a verdict and a duration are all it draws, but a diff needs the text, and
// comparing against a run whose lines are gone reports every line as deleted.
func LastGreen(base, project, task, skip string) (Point, bool) {
	for _, p := range Timeline(base, project, task) {
		if p.Ok() && p.RunID != skip && HasOutput(base, p.RunID) {
			return p, true
		}
	}
	return Point{}, false
}

// Previous is the most recent stored appearance at all, green or not — the comparison you
// want when the task has never passed and "what changed since last time" is still a real
// question.
func Previous(base, project, task, skip string) (Point, bool) {
	for _, p := range Timeline(base, project, task) {
		if p.RunID != skip && HasOutput(base, p.RunID) {
			return p, true
		}
	}
	return Point{}, false
}

// Output reads back what one task printed in one stored run, stripped of escapes.
//
// The `.txt` half rather than the `.ansi` half: this feeds the diff, and two lines that
// differ only in the colour they were painted are not a difference anyone wants reported.
func Output(base string, p Point) []string {
	return readLines(filepath.Join(RunDir(base, p.RunID), p.File+".txt"))
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

// Flake is a task that has both passed and failed at the same commit.
type Flake struct {
	Task string
	// Commit is where it happened, and Passed/Failed how many times each way.
	Commit         string
	Passed, Failed int
	// LastUnix is the most recent of the runs involved, for ordering the report.
	LastUnix int64
}

// Flaky finds the tasks whose result depends on something other than the code.
//
// Same commit, both outcomes. That is the whole definition, and it is why the commit is
// recorded at all — alternating results across a series only *suggest* flakiness, because
// the obvious explanation is that somebody broke it and fixed it. Two different answers to
// the same question is not suggestive of anything, it is the thing itself.
//
// Runs from a dirty tree are excluded: `headCommit` marks them, and two runs of uncommitted
// work are not two runs of the same code.
func Flaky(base, project string) []Flake {
	type key struct{ task, commit string }
	seen := map[key]*Flake{}

	for _, m := range List(base) {
		if (project != "" && m.Dir != project) || m.Commit == "" || strings.HasSuffix(m.Commit, "-dirty") {
			continue
		}
		for _, e := range m.Tasks {
			if e.Status != "Ok" && e.Status != "Failed" {
				continue
			}
			k := key{e.Name, m.Commit}
			f, ok := seen[k]
			if !ok {
				f = &Flake{Task: e.Name, Commit: m.Commit}
				seen[k] = f
			}
			if e.Status == "Ok" {
				f.Passed++
			} else {
				f.Failed++
			}
			f.LastUnix = max(f.LastUnix, m.StartedUnix)
		}
	}

	var out []Flake
	for _, f := range seen {
		if f.Passed > 0 && f.Failed > 0 {
			out = append(out, *f)
		}
	}
	// Most recent first, then by name so the order is stable when timestamps collide.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastUnix != out[j].LastUnix {
			return out[i].LastUnix > out[j].LastUnix
		}
		return out[i].Task < out[j].Task
	})
	return out
}

// Short is the commit, abbreviated for display.
func (f Flake) Short() string {
	if len(f.Commit) > 7 {
		return f.Commit[:7]
	}
	return f.Commit
}
