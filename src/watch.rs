//! Re-running a task when the files it cares about change.
//!
//! The loop this replaces is `task check`, read the output, edit, press `r`, repeat. Watch
//! mode removes the third and fourth steps.

use anyhow::Result;
use notify::{RecommendedWatcher, RecursiveMode, Watcher as _};
use std::path::{Path, PathBuf};
use std::sync::mpsc::{channel, Receiver};
use std::time::{Duration, Instant};

/// Directories never worth watching: build output churns constantly, and watching it
/// means every run triggers the next one forever.
const IGNORED: [&str; 9] = [
    "target",
    ".git",
    ".task",
    "node_modules",
    "dist",
    "public",
    ".worktrees",
    ".wrangler",
    ".terraform",
];

/// A change is worth a re-run only if it is a source file — editors write swap files,
/// lock files and backups constantly, and each one would fire a build.
fn is_noise(path: &Path) -> bool {
    if path
        .components()
        .any(|c| IGNORED.contains(&c.as_os_str().to_string_lossy().as_ref()))
    {
        return true;
    }
    let name = path
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_default();
    // Vim swap files, Emacs autosaves and locks, editor backups, macOS metadata.
    name.starts_with('.')
        || name.ends_with('~')
        || name.ends_with(".swp")
        || name.ends_with(".swx")
        || name.ends_with(".tmp")
        || name == "4913" // vim's write-probe file
}

pub struct Watch {
    /// Held so the watcher thread stays alive; dropping this stops watching.
    _watcher: RecommendedWatcher,
    rx: Receiver<PathBuf>,
    /// Changes arrive in bursts — one save is several events — so they are collapsed.
    settle: Duration,
    pending_since: Option<Instant>,
    pub last_changed: Option<PathBuf>,
}

impl Watch {
    pub fn start(dir: &Path) -> Result<Watch> {
        let (tx, rx) = channel();
        let mut watcher =
            notify::recommended_watcher(move |res: notify::Result<notify::Event>| {
                let Ok(event) = res else { return };
                for path in event.paths {
                    if !is_noise(&path) {
                        let _ = tx.send(path);
                    }
                }
            })?;
        watcher.watch(dir, RecursiveMode::Recursive)?;
        Ok(Watch {
            _watcher: watcher,
            rx,
            settle: Duration::from_millis(400),
            pending_since: None,
            last_changed: None,
        })
    }

    /// The path that triggered a re-run, once the burst has settled.
    ///
    /// A single save produces a write, a rename and a metadata change; firing on the
    /// first would start a build against a half-written file.
    pub fn poll(&mut self) -> Option<PathBuf> {
        let mut saw_any = false;
        while let Ok(path) = self.rx.try_recv() {
            self.last_changed = Some(path);
            saw_any = true;
        }
        if saw_any {
            self.pending_since = Some(Instant::now());
            return None;
        }
        let since = self.pending_since?;
        if since.elapsed() >= self.settle {
            self.pending_since = None;
            return self.last_changed.clone();
        }
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn build_output_is_ignored() {
        assert!(is_noise(Path::new("/proj/target/debug/taskui")));
        assert!(is_noise(Path::new("/proj/.git/index")));
        assert!(is_noise(Path::new("/proj/.task/checksum/lint")));
        assert!(is_noise(Path::new("/proj/app/node_modules/x/index.js")));
    }

    /// Editors write a lot of things that are not source changes.
    #[test]
    fn editor_scratch_files_are_ignored() {
        assert!(is_noise(Path::new("/proj/src/.main.rs.swp")));
        assert!(is_noise(Path::new("/proj/src/main.rs~")));
        assert!(is_noise(Path::new("/proj/src/4913")));
        assert!(is_noise(Path::new("/proj/.DS_Store")));
    }

    #[test]
    fn source_files_are_not() {
        assert!(!is_noise(Path::new("/proj/src/main.rs")));
        assert!(!is_noise(Path::new("/proj/Taskfile.yml")));
        assert!(!is_noise(Path::new("/proj/backend/crates/api/src/lib.rs")));
    }

    /// One save is several events; firing on the first would build a half-written file.
    #[test]
    fn a_burst_of_events_settles_before_firing() {
        let dir = std::env::temp_dir().join(format!("taskui-watch-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let mut watch = Watch::start(&dir).unwrap();
        watch.settle = Duration::from_millis(120);

        std::fs::write(dir.join("a.rs"), "one").unwrap();
        std::fs::write(dir.join("a.rs"), "two").unwrap();

        // Give the platform watcher a moment to deliver.
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut fired = None;
        while Instant::now() < deadline {
            if let Some(path) = watch.poll() {
                fired = Some(path);
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }

        assert!(fired.is_some(), "the change eventually fired");
        assert!(watch.poll().is_none(), "and only once");
        let _ = std::fs::remove_dir_all(&dir);
    }
}
