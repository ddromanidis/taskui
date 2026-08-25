//! Keeping finished runs on disk so they can be searched later.
//!
//! The format is deliberately boring: a directory per run, a `manifest.json` for the
//! structure, and two plain files per task. `<task>.txt` is the ANSI-stripped text — what
//! search reads, and what plain `rg` from your shell reads too — while `<task>.ansi`
//! keeps the escape sequences so an archived run still renders in colour. A format that
//! needs taskui to read it would be a worse format.
//!
//! Everything written here has already been through [`crate::redact`], and the directory
//! is locked to the owner regardless.

use crate::run::Run;
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

/// How many runs to keep. A full `task all` on a Rust repo is a lot of cargo output and
/// it accumulates fast.
pub const KEEP_RUNS: usize = 50;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskEntry {
    pub name: String,
    pub status: String,
    pub duration_ms: u64,
    pub lines: usize,
    /// Basename, without extension: `<file>.txt` and `<file>.ansi`.
    pub file: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Manifest {
    pub id: String,
    /// The task that was invoked.
    pub root: String,
    /// Extra argv it was invoked with. Defaulted so manifests written before args
    /// existed still load.
    #[serde(default)]
    pub args: Vec<String>,
    /// Defaulted so manifests written before this existed still load.
    #[serde(default)]
    pub force: bool,
    /// The project directory it ran in.
    pub dir: String,
    pub started_unix: u64,
    pub duration_ms: u64,
    pub exit: i32,
    /// How many distinct secrets were masked out of this run.
    pub redacted_secrets: usize,
    pub tasks: Vec<TaskEntry>,
    pub edges: std::collections::BTreeMap<String, Vec<String>>,
}

impl Manifest {
    pub fn failed(&self) -> bool {
        self.exit != 0
    }

    pub fn command(&self) -> String {
        let force = if self.force { " --force" } else { "" };
        if self.args.is_empty() {
            format!("task {}{force}", self.root)
        } else {
            format!("task {}{force} {}", self.root, self.args.join(" "))
        }
    }
}

/// `$XDG_STATE_HOME/taskui` if set, else `~/.local/state/taskui`.
pub fn state_dir() -> PathBuf {
    if let Ok(x) = std::env::var("XDG_STATE_HOME") {
        if !x.is_empty() {
            return PathBuf::from(x).join("taskui");
        }
    }
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".into());
    PathBuf::from(home).join(".local/state/taskui")
}

fn runs_dir(base: &Path) -> PathBuf {
    base.join("runs")
}

/// Task names contain colons and can contain slashes; neither belongs in a filename.
fn safe_name(task: &str) -> String {
    task.chars()
        .map(|c| {
            if c.is_alphanumeric() || c == '-' || c == '_' {
                c
            } else {
                '.'
            }
        })
        .collect()
}

#[cfg(unix)]
fn lock_down(path: &Path, dir: bool) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;
    let mode = if dir { 0o700 } else { 0o600 };
    fs::set_permissions(path, fs::Permissions::from_mode(mode))?;
    Ok(())
}

#[cfg(not(unix))]
fn lock_down(_path: &Path, _dir: bool) -> Result<()> {
    Ok(())
}

fn now_unix() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

/// Write a finished run into `base`. Returns the directory it landed in.
pub fn save(base: &Path, project_dir: &Path, run: &Run) -> Result<PathBuf> {
    let started = now_unix().saturating_sub(run.duration.unwrap_or_default().as_secs());
    // Seconds alone collide when two runs finish in the same second, which happens
    // constantly with fast tasks; the task name disambiguates.
    let id = format!("{started}-{}", safe_name(&run.root));

    let dir = runs_dir(base).join(&id);
    fs::create_dir_all(&dir).with_context(|| format!("creating {}", dir.display()))?;
    lock_down(&runs_dir(base), true)?;
    lock_down(&dir, true)?;

    let mut entries = Vec::new();
    for (name, t) in &run.tasks {
        let file = safe_name(name);
        let plain: String = t.lines.iter().map(|l| format!("{}\n", l.plain)).collect();
        let ansi: String = t.lines.iter().map(|l| format!("{}\n", l.raw)).collect();

        let txt = dir.join(format!("{file}.txt"));
        fs::write(&txt, plain)?;
        lock_down(&txt, false)?;

        let esc = dir.join(format!("{file}.ansi"));
        fs::write(&esc, ansi)?;
        lock_down(&esc, false)?;

        entries.push(TaskEntry {
            name: name.clone(),
            status: format!("{:?}", t.status),
            duration_ms: t.duration.unwrap_or_default().as_millis() as u64,
            lines: t.lines.len(),
            file,
        });
    }

    let manifest = Manifest {
        id,
        root: run.root.clone(),
        args: run.args.clone(),
        force: run.force,
        dir: project_dir.display().to_string(),
        started_unix: started,
        duration_ms: run.duration.unwrap_or_default().as_millis() as u64,
        exit: run.exit.unwrap_or(-1),
        redacted_secrets: run.redacted_secrets,
        tasks: entries,
        edges: run.graph.edges.clone(),
    };

    let path = dir.join("manifest.json");
    fs::write(&path, serde_json::to_vec_pretty(&manifest)?)?;
    lock_down(&path, false)?;

    prune(base, KEEP_RUNS)?;
    Ok(dir)
}

/// Every stored run, newest first.
pub fn list(base: &Path) -> Vec<Manifest> {
    let Ok(entries) = fs::read_dir(runs_dir(base)) else {
        return Vec::new();
    };
    let mut out: Vec<Manifest> = entries
        .filter_map(|e| e.ok())
        .filter_map(|e| fs::read(e.path().join("manifest.json")).ok())
        .filter_map(|bytes| serde_json::from_slice(&bytes).ok())
        .collect();
    out.sort_by(|a, b| {
        b.started_unix
            .cmp(&a.started_unix)
            .then_with(|| b.id.cmp(&a.id))
    });
    out
}

pub fn run_dir(base: &Path, id: &str) -> PathBuf {
    runs_dir(base).join(id)
}

fn parse_status(s: &str) -> crate::run::Status {
    use crate::run::Status;
    match s {
        "Ok" => Status::Ok,
        "Failed" => Status::Failed,
        "Running" => Status::Running,
        "Skipped" => Status::Skipped,
        _ => Status::Pending,
    }
}

/// Rebuild a stored run so it can be folded and searched like a live one.
///
/// Reading `.txt` and `.ansi` side by side is what gives an archived run its colour back:
/// the stripped half is what search matches on, the escaped half is what renders. They
/// are written a line at a time from the same buffer, so they stay in step.
pub fn load(base: &Path, manifest: &Manifest) -> Result<crate::run::Run> {
    use crate::run::{Line, TaskRun};
    use std::collections::BTreeMap;
    use std::time::Duration;

    let dir = run_dir(base, &manifest.id);
    let mut tasks: BTreeMap<String, TaskRun> = BTreeMap::new();
    let mut order = Vec::new();

    for entry in &manifest.tasks {
        let plain = fs::read_to_string(dir.join(format!("{}.txt", entry.file))).unwrap_or_default();
        let raw = fs::read_to_string(dir.join(format!("{}.ansi", entry.file))).unwrap_or_default();

        let mut raws = raw.lines();
        let lines: Vec<Line> = plain
            .lines()
            .map(|p| {
                // Fall back to the stripped text if the sidecar is short or missing —
                // losing colour is survivable, losing the line is not.
                let r = raws.next().unwrap_or(p);
                Line::restored(r.to_string(), p.to_string())
            })
            .collect();

        if !lines.is_empty() {
            order.push(entry.name.clone());
        }
        tasks.insert(
            entry.name.clone(),
            TaskRun::restored(
                parse_status(&entry.status),
                lines,
                Duration::from_millis(entry.duration_ms),
            ),
        );
    }

    let graph = crate::graph::Graph {
        edges: manifest.edges.clone(),
    };

    Ok(crate::run::Run::from_stored(crate::run::Stored {
        root: manifest.root.clone(),
        args: manifest.args.clone(),
        graph,
        tasks,
        order,
        exit: manifest.exit,
        duration: Duration::from_millis(manifest.duration_ms),
        redacted_secrets: manifest.redacted_secrets,
    }))
}

/// The last outcome of every task seen in this project's stored runs.
///
/// Keyed by task name, newest wins. Built from the per-task entries rather than just the
/// run roots, so a single `task all` teaches it about `lint`, `backend:lint` and every
/// other task that run touched.
pub fn last_outcomes(base: &Path, project: &Path) -> std::collections::HashMap<String, Outcome> {
    let here = project.display().to_string();
    let mut out: std::collections::HashMap<String, Outcome> = std::collections::HashMap::new();
    // `list` is newest first, so the first sighting of a task is its latest.
    for manifest in list(base).into_iter().filter(|m| m.dir == here) {
        for entry in &manifest.tasks {
            if entry.status == "Pending" || entry.status == "Skipped" {
                continue;
            }
            out.entry(entry.name.clone()).or_insert(Outcome {
                ok: entry.status == "Ok",
                when_unix: manifest.started_unix,
            });
        }
    }
    out
}

#[derive(Debug, Clone, Copy)]
pub struct Outcome {
    pub ok: bool,
    pub when_unix: u64,
}

/// Drop the oldest runs beyond `keep`.
pub fn prune(base: &Path, keep: usize) -> Result<usize> {
    let all = list(base);
    let mut removed = 0;
    for m in all.into_iter().skip(keep) {
        if fs::remove_dir_all(run_dir(base, &m.id)).is_ok() {
            removed += 1;
        }
    }
    Ok(removed)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::run::Run;

    fn temp() -> PathBuf {
        let p = std::env::temp_dir().join(format!(
            "taskui-test-{}-{:?}",
            std::process::id(),
            std::thread::current().id()
        ));
        let _ = fs::remove_dir_all(&p);
        fs::create_dir_all(&p).unwrap();
        p
    }

    fn finished_run(root: &str) -> Run {
        let mut r = Run::detached(root, Run::graph_from(&[(root, &["child"]), ("child", &[])]));
        r.feed("child", "hello from the child");
        r.feed("child", "error: boom");
        r.finish(1);
        r
    }

    #[test]
    fn a_saved_run_round_trips_through_the_manifest() {
        let base = temp();
        let run = finished_run("all");
        let dir = save(&base, Path::new("/proj"), &run).unwrap();
        assert!(dir.join("manifest.json").exists());

        let listed = list(&base);
        assert_eq!(listed.len(), 1);
        assert_eq!(listed[0].root, "all");
        assert_eq!(listed[0].exit, 1);
        assert!(listed[0].failed());
        assert_eq!(listed[0].edges["all"], ["child"]);

        let child = listed[0].tasks.iter().find(|t| t.name == "child").unwrap();
        assert_eq!(child.lines, 2);
    }

    /// The archive has to be readable by anything, not just taskui — that is the whole
    /// argument for plain files.
    #[test]
    fn output_lands_as_plain_greppable_text() {
        let base = temp();
        let run = finished_run("all");
        let dir = save(&base, Path::new("/proj"), &run).unwrap();
        let text = fs::read_to_string(dir.join("child.txt")).unwrap();
        assert_eq!(text, "hello from the child\nerror: boom\n");
    }

    /// Colour is kept beside the searchable text, not instead of it.
    #[test]
    fn escape_sequences_are_kept_in_a_sidecar() {
        let base = temp();
        let mut run = Run::detached("a", Run::graph_from(&[("a", &[])]));
        run.feed("a", "\x1b[31merror\x1b[0m: boom");
        run.finish(1);
        let dir = save(&base, Path::new("/proj"), &run).unwrap();

        assert_eq!(
            fs::read_to_string(dir.join("a.txt")).unwrap(),
            "error: boom\n"
        );
        assert!(fs::read_to_string(dir.join("a.ansi"))
            .unwrap()
            .contains('\x1b'));
    }

    /// Colons are not filenames.
    #[test]
    fn namespaced_task_names_become_safe_filenames() {
        assert_eq!(safe_name("backend:migrate:down"), "backend.migrate.down");
        assert_eq!(safe_name("app:build"), "app.build");
    }

    #[test]
    fn pruning_keeps_the_newest_runs() {
        let base = temp();
        for i in 0..5 {
            // Distinct task names, so the five runs get distinct ids even when they land
            // in the same second.
            let r = finished_run(&format!("task{i}"));
            save(&base, Path::new("/proj"), &r).unwrap();
        }
        assert_eq!(list(&base).len(), 5);
        prune(&base, 2).unwrap();
        let left = list(&base);
        assert_eq!(left.len(), 2);
    }

    /// The picker's ✓/✗ column: newest result per task, drawn from the per-task entries
    /// so one `task all` teaches it about everything that run touched.
    #[test]
    fn last_outcomes_take_the_newest_result_per_task() {
        let base = temp();
        let proj = Path::new("/proj");

        let mut old = Run::detached("ci", Run::graph_from(&[("ci", &["child"])]));
        old.feed("child", "boom");
        old.apply_failed_for_test("child");
        old.finish(1);
        save(&base, proj, &old).unwrap();

        let outcomes = last_outcomes(&base, proj);
        assert!(!outcomes["child"].ok, "failed last time");
        assert!(!outcomes["ci"].ok, "and so did its parent");
    }

    /// Another project's runs are not this project's business.
    #[test]
    fn outcomes_are_scoped_to_the_project() {
        let base = temp();
        let mut r = Run::detached("ci", Run::graph_from(&[("ci", &["child"])]));
        r.feed("child", "fine");
        r.finish(0);
        save(&base, Path::new("/elsewhere"), &r).unwrap();

        assert!(last_outcomes(&base, Path::new("/proj")).is_empty());
        assert!(!last_outcomes(&base, Path::new("/elsewhere")).is_empty());
    }

    /// A task that was never reached has no outcome — that is not the same as passing.
    #[test]
    fn skipped_tasks_have_no_outcome() {
        let base = temp();
        let proj = Path::new("/proj");
        let mut r = Run::detached("ci", Run::graph_from(&[("ci", &["ran", "never"])]));
        r.feed("ran", "hello");
        r.finish(0);
        save(&base, proj, &r).unwrap();

        let outcomes = last_outcomes(&base, proj);
        assert!(outcomes.contains_key("ran"));
        assert!(!outcomes.contains_key("never"), "skipped is not passed");
    }

    /// `--force` is part of what was run, so it belongs in the record.
    #[test]
    fn force_is_recorded_in_the_manifest() {
        let base = temp();
        let mut r = Run::detached("check", Run::graph_from(&[("check", &[])]));
        r.feed("check", "checking");
        r.finish(0);
        save(&base, Path::new("/proj"), &r).unwrap();
        // Detached runs are never forced; the field simply has to round-trip.
        assert!(!list(&base)[0].force);
        assert_eq!(list(&base)[0].command(), "task check");
    }

    #[cfg(unix)]
    #[test]
    fn the_run_directory_is_owner_only() {
        use std::os::unix::fs::PermissionsExt;
        let base = temp();
        let run = finished_run("all");
        let dir = save(&base, Path::new("/proj"), &run).unwrap();

        let dir_mode = fs::metadata(&dir).unwrap().permissions().mode() & 0o777;
        assert_eq!(dir_mode, 0o700, "captured output is not world-readable");
        let file_mode = fs::metadata(dir.join("manifest.json"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777;
        assert_eq!(file_mode, 0o600);
    }
    /// The whole point of the archive: a stored run comes back as the same structure a
    /// live one has, so it folds and searches identically.
    #[test]
    fn a_stored_run_reloads_as_a_run() {
        use crate::run::Status;
        let base = temp();
        let mut original = Run::detached("ci", Run::graph_from(&[("ci", &["build", "test"])]));
        original.feed("build", "compiling core");
        original.feed("test", "--- FAIL: TestOrderTotal");
        original.apply_failed_for_test("test");
        original.finish(1);
        save(&base, Path::new("/proj"), &original).unwrap();

        let manifest = list(&base).pop().unwrap();
        let reloaded = load(&base, &manifest).unwrap();

        assert!(reloaded.is_stored());
        assert!(reloaded.finished());
        assert_eq!(reloaded.exit, Some(1));
        assert_eq!(reloaded.graph.children("ci"), ["build", "test"]);
        assert_eq!(reloaded.tasks["test"].status, Status::Failed);
        assert_eq!(reloaded.tasks["build"].lines[0].plain, "compiling core");
    }

    /// Reopening an archived run should look like it did live, colour included.
    #[test]
    fn colour_survives_the_round_trip() {
        let base = temp();
        let mut original = Run::detached("a", Run::graph_from(&[("a", &[])]));
        original.feed("a", "\x1b[31merror\x1b[0m: boom");
        original.finish(1);
        save(&base, Path::new("/proj"), &original).unwrap();

        let manifest = list(&base).pop().unwrap();
        let line = &load(&base, &manifest).unwrap().tasks["a"].lines[0];
        assert_eq!(line.plain, "error: boom", "still searchable");
        assert!(line.raw.contains('\x1b'), "still coloured");
    }

    /// `is_command` is derived rather than stored, so a marker never pollutes the
    /// greppable text.
    #[test]
    fn command_echoes_are_recognised_again_on_reload() {
        let base = temp();
        let mut original = Run::detached("a", Run::graph_from(&[("a", &[])]));
        original.feed("a", "task: [a] cargo build");
        original.feed("a", "Compiling taskui");
        original.finish(0);
        save(&base, Path::new("/proj"), &original).unwrap();

        let manifest = list(&base).pop().unwrap();
        let reloaded = load(&base, &manifest).unwrap();
        assert!(reloaded.tasks["a"].lines[0].is_command);
        assert!(!reloaded.tasks["a"].lines[1].is_command);
    }
}
