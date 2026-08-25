//! Running a task and splitting its output back apart, one bucket per task.
//!
//! `task` writes a single stream. We drive it with `--output prefixed`, which tags every
//! output line with the task that produced it, and pair that with the execution graph
//! from [`crate::graph`] — the stream says *who* spoke, the graph says *who called whom*.
//!
//! Colour needs care. `--output prefixed` makes go-task pipe every command through its
//! own prefixing writer, so a command's stdout is a pipe *regardless* of what taskui does
//! — measured: `isatty` reports false inside prefixed mode even when go-task itself is on
//! a pty. Tools that auto-detect therefore turn colour off, and no amount of pty gets it
//! back. Forcing it by environment does: `CARGO_TERM_COLOR=always` restores cargo and
//! clippy's colour through the pipe intact.
//!
//! The pty is still worth having — it keeps go-task's own output coloured and stops the
//! usual switch to block buffering when stdout is not a terminal — it just is not what
//! makes the tools colour.

use crate::graph::Graph;
use crate::redact::Redactor;
use anyhow::Result;
use portable_pty::{native_pty_system, CommandBuilder, PtySize};
use std::collections::{BTreeMap, HashSet};
use std::io::{Read, Write};
use std::path::Path;
use std::sync::mpsc::{channel, Receiver, Sender};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Status {
    /// In the graph but not reached yet.
    Pending,
    Running,
    Ok,
    Failed,
    /// The run ended before this task was reached — usually because something upstream
    /// failed.
    Skipped,
}

impl Status {
    pub fn glyph(self) -> &'static str {
        match self {
            Status::Pending => "·",
            Status::Running => "▶",
            Status::Ok => "✓",
            Status::Failed => "✗",
            Status::Skipped => "⏸",
        }
    }
}

/// One captured line, kept twice on purpose: `raw` still has its escape sequences for
/// rendering, `plain` is what search runs over. Searching the raw bytes would miss
/// matches wherever a colour change lands mid-word.
#[derive(Debug, Clone)]
pub struct Line {
    pub raw: String,
    pub plain: String,
    /// True for go-task's own `task: [name] <cmd>` echo, which is structure rather than
    /// output and is worth rendering differently.
    pub is_command: bool,
}

impl Line {
    /// Rebuild a line from the two stored halves. `is_command` is not persisted because
    /// it is derivable — it is exactly go-task's `task: [name] …` echo — and a marker in
    /// the `.txt` file would make the archive worse to grep.
    pub fn restored(raw: String, plain: String) -> Self {
        let is_command = plain.starts_with("task: [");
        Line {
            raw,
            plain,
            is_command,
        }
    }

    fn new(raw: String, is_command: bool) -> Self {
        let plain =
            String::from_utf8_lossy(&strip_ansi_escapes::strip(raw.as_bytes())).into_owned();
        Line {
            raw,
            plain,
            is_command,
        }
    }
}

#[derive(Debug, Clone)]
pub struct TaskRun {
    pub status: Status,
    pub lines: Vec<Line>,
    started: Option<Instant>,
    pub duration: Option<Duration>,
}

impl TaskRun {
    /// Time on the clock: the final figure once the task has finished, and a ticking one
    /// while it is still going. Showing nothing until a task completes means the only
    /// live timing on screen is the total, which is the least useful of them — during a
    /// slow build what you want to know is which step is taking it.
    pub fn elapsed(&self) -> Option<Duration> {
        self.duration.or_else(|| self.started.map(|s| s.elapsed()))
    }

    pub fn restored(status: Status, lines: Vec<Line>, duration: Duration) -> Self {
        TaskRun {
            status,
            lines,
            started: None,
            duration: Some(duration),
        }
    }

    fn new(_name: String) -> Self {
        TaskRun {
            status: Status::Pending,
            lines: Vec::new(),
            started: None,
            duration: None,
        }
    }
}

/// What the reader thread sends back to the UI.
#[derive(Debug)]
pub enum Event {
    /// Resolving the graph costs one `task --summary` per node — over a second for a
    /// large aggregate — so it happens on the worker thread and arrives here rather than
    /// freezing the UI at the moment you press enter.
    GraphReady(Graph),
    /// Output with no newline yet — an interactive prompt. `Do you want to proceed?
    /// (y/n) ` never terminates its line, so a strictly line-based reader shows nothing at
    /// all and the run just appears to hang.
    Partial(String),
    /// How many distinct secrets the redactor is masking, so the UI can say whether
    /// output has been through it.
    Redacting(usize),
    Line {
        task: Option<String>,
        raw: String,
        is_command: bool,
    },
    /// go-task reported a specific task as the failure.
    Failed(String),
    Exited(i32),
}

impl Drop for Run {
    /// A Run that goes out of scope — replaced by a new one, or dropped on quit — must
    /// take its process with it. Otherwise starting a second run silently orphans the
    /// first, and taskui loses the ability to report or stop it.
    fn drop(&mut self) {
        self.cancel();
    }
}

/// Shared handle on the spawned `task` process.
type ChildHandle = Arc<Mutex<Option<Box<dyn portable_pty::Child + Send + Sync>>>>;
type WriterHandle = Arc<Mutex<Option<Box<dyn Write + Send>>>>;

/// A finished run being rebuilt from the archive.
pub struct Stored {
    pub root: String,
    pub args: Vec<String>,
    pub graph: Graph,
    pub tasks: BTreeMap<String, TaskRun>,
    pub order: Vec<String>,
    pub exit: i32,
    pub duration: Duration,
    pub redacted_secrets: usize,
}

pub struct Run {
    pub root: String,
    /// When this run began. Public so a caller can tell one run from another.
    /// Extra argv passed after the task name — `NAME=backend`, `-- -p ingest`.
    pub args: Vec<String>,
    /// Ran with `--output interleaved` so the task could ask questions.
    pub interactive: bool,
    /// Ran with `--force`, ignoring go-task's up-to-date checks.
    pub force: bool,
    pub graph: Graph,
    pub tasks: BTreeMap<String, TaskRun>,
    /// Tasks in the order they first produced output.
    pub order: Vec<String>,
    pub exit: Option<i32>,
    pub started: Instant,
    pub duration: Option<Duration>,
    /// Number of secrets being masked out of this run's output.
    pub redacted_secrets: usize,
    /// Loaded from the archive rather than executed here.
    stored: bool,
    /// Handle on the child, so the run can be cancelled. Shared with the capture thread,
    /// which is the only thing that waits on it.
    child: ChildHandle,
    /// The write half of the pty, for answering prompts.
    writer: WriterHandle,
    cancelled: bool,
    /// Where the not-yet-terminated line lives, so the next read replaces it rather than
    /// stacking up a copy per 8KB chunk.
    provisional: Option<(String, usize)>,
    /// When output last arrived, so a silent run can be distinguished from a slow one.
    last_output: Instant,
    /// What has been typed at the task, echoed back so you can see the keystroke landed.
    /// Under `--output prefixed` a task may produce nothing for a long time after
    /// answering, and without this there is no way to tell "sent and waiting" from "not
    /// sent at all".
    pub sent: String,
    /// Which task lines with no prefix belong to.
    active: Option<String>,
    rx: Option<Receiver<Event>>,
}

impl Run {
    /// Start `task <root>` in `dir`. Returns immediately; call [`Run::poll`] to drain.
    /// Start `task <root>`.
    ///
    /// `interactive` swaps `--output prefixed` for `--output interleaved`, which is the
    /// only way a prompt ever reaches us: go-task's prefixer is itself line-based, so a
    /// `Proceed? (y/n) ` with no newline is held inside it forever and the run just looks
    /// hung. Measured — under `prefixed` the prompt never appears at all.
    ///
    /// The cost is per-line attribution. Interleaved output still carries go-task's
    /// `task: [name] <cmd>` announcements, so lines are attributed to whichever task last
    /// spoke, which is correct for a sequential run and wrong under parallel `deps:`.
    /// Interactive runs are inherently sequential, so that trade is worth making — but
    /// only when asked for.
    pub fn start(
        dir: &Path,
        root: &str,
        args: &[String],
        interactive: bool,
        force: bool,
    ) -> Result<Run> {
        let (tx, rx) = channel();
        let (dir, task) = (dir.to_path_buf(), root.to_string());
        let argv = args.to_vec();
        let child: ChildHandle = Arc::new(Mutex::new(None));
        let child_for_thread = Arc::clone(&child);
        let writer: WriterHandle = Arc::new(Mutex::new(None));
        let writer_for_thread = Arc::clone(&writer);

        std::thread::spawn(move || {
            // Resolve first: the tree should be on screen, greyed out, before any output
            // arrives to fill it in. The same call yields the environment dump the
            // redactor is built from, so masking is in place before the first line.
            let mut redactor = Redactor::empty();
            // A graph we could not resolve is not fatal — we still capture output, just
            // without the nesting. Redaction is then empty, which is why the run view
            // says so rather than implying output has been checked.
            if let Ok((g, summary)) = crate::graph::resolve_detailed(&dir, &task) {
                redactor = Redactor::from_summary(&summary);
                if tx.send(Event::GraphReady(g)).is_err() {
                    return;
                }
            }
            let _ = tx.send(Event::Redacting(redactor.len()));
            let job = Capture {
                dir: &dir,
                root: &task,
                args: &argv,
                interactive,
                force,
                redactor: &redactor,
                child: &child_for_thread,
                writer: &writer_for_thread,
            };
            if let Err(e) = capture(job, &tx) {
                let _ = tx.send(Event::Line {
                    task: None,
                    raw: format!("taskui: could not start `task {task}`: {e}"),
                    is_command: false,
                });
                let _ = tx.send(Event::Exited(-1));
            }
        });

        Ok(Run {
            root: root.to_string(),
            args: args.to_vec(),
            interactive,
            force,
            graph: Graph::default(),
            tasks: BTreeMap::new(),
            order: Vec::new(),
            exit: None,
            started: Instant::now(),
            duration: None,
            redacted_secrets: 0,
            stored: false,
            child,
            writer,
            cancelled: false,
            provisional: None,
            last_output: Instant::now(),
            sent: String::new(),
            active: None,
            rx: Some(rx),
        })
    }

    pub fn finished(&self) -> bool {
        self.exit.is_some()
    }

    pub fn cancelled(&self) -> bool {
        self.cancelled
    }

    /// Send keystrokes to the running task. `wrangler` and friends ask questions; without
    /// this the only answer taskui can give is to kill them.
    pub fn send_input(&mut self, bytes: &[u8]) -> bool {
        if self.finished() {
            return false;
        }
        let ok = {
            let mut guard = match self.writer.lock() {
                Ok(g) => g,
                Err(p) => p.into_inner(),
            };
            match guard.as_mut() {
                Some(w) => w.write_all(bytes).and_then(|_| w.flush()).is_ok(),
                None => false,
            }
        };
        if ok {
            // Printable characters as themselves; control keys as something readable.
            for &byte in bytes {
                match byte {
                    b'\r' | b'\n' => self.sent.push('⏎'),
                    0x7f => self.sent.push('⌫'),
                    0x03 => self.sent.push_str("^C"),
                    0x04 => self.sent.push_str("^D"),
                    b'\t' => self.sent.push('⇥'),
                    b if b.is_ascii_graphic() || b == b' ' => self.sent.push(b as char),
                    _ => self.sent.push('·'),
                }
            }
            // Only the tail matters; this is a receipt, not a transcript.
            let len = self.sent.chars().count();
            if len > 40 {
                self.sent = self.sent.chars().skip(len - 40).collect();
            }
        }
        ok
    }

    /// How long the task has produced nothing.
    ///
    /// A run under `--output prefixed` that is blocked on a prompt looks exactly like one
    /// that is slow: go-task's prefixer holds the unterminated question, so *nothing*
    /// arrives. Silence is the only signal there is.
    pub fn silent_for(&self) -> Duration {
        self.last_output.elapsed()
    }

    /// The unterminated tail, if the task is sitting on one.
    pub fn pending_prompt(&self) -> Option<&str> {
        let (task, idx) = self.provisional.as_ref()?;
        let text = self
            .tasks
            .get(task)?
            .lines
            .get(*idx)?
            .plain
            .trim_end_matches('\r');
        (!text.trim().is_empty()).then_some(text)
    }

    /// Does the tail look like a question? Used only to nudge the user toward the input
    /// key — a wrong guess costs nothing but a missing hint.
    pub fn looks_like_a_prompt(&self) -> bool {
        let Some(text) = self.pending_prompt() else {
            return false;
        };
        let t = text.trim_end();
        t.ends_with('?')
            || t.ends_with(':')
            || t.ends_with('>')
            || t.ends_with(')')
            || t.to_lowercase().contains("(y/n)")
            || t.to_lowercase().contains("[y/n]")
    }

    /// Stop the run.
    ///
    /// Killing the `task` process alone is not enough: it is the shell commands beneath it
    /// that are doing the work, and they survive it — verified by killing taskui mid-run
    /// and watching `sleep` carry on. The pty puts the child in its own session, so the
    /// whole group can be signalled at once, which is what actually reaps the tree.
    pub fn cancel(&mut self) {
        if self.finished() {
            return;
        }
        self.cancelled = true;
        let mut guard = match self.child.lock() {
            Ok(g) => g,
            Err(poisoned) => poisoned.into_inner(),
        };
        if let Some(child) = guard.as_mut() {
            if let Some(pid) = child.process_id() {
                // SIGTERM first so the tools get to clean up after themselves.
                unsafe { libc::killpg(pid as i32, libc::SIGTERM) };
            }
            let _ = child.kill();
        }
    }

    /// What was actually invoked, for the header and the history list.
    pub fn command(&self) -> String {
        let force = if self.force { " --force" } else { "" };
        if self.args.is_empty() {
            format!("task {}{force}", self.root)
        } else {
            format!("task {}{force} {}", self.root, self.args.join(" "))
        }
    }

    /// True when this Run came off disk rather than off a pty. The run view uses it to
    /// avoid implying a stored run is still doing something.
    pub fn is_stored(&self) -> bool {
        self.stored
    }

    /// Rebuild a finished run from the archive.
    pub fn from_stored(s: Stored) -> Run {
        let Stored {
            root,
            args,
            graph,
            tasks,
            order,
            exit,
            duration,
            redacted_secrets,
        } = s;
        Run {
            root,
            args,
            interactive: false,
            force: false,
            graph,
            tasks,
            order,
            exit: Some(exit),
            started: Instant::now(),
            duration: Some(duration),
            redacted_secrets,
            stored: true,
            child: Arc::new(Mutex::new(None)),
            writer: Arc::new(Mutex::new(None)),
            cancelled: false,
            provisional: None,
            last_output: Instant::now(),
            sent: String::new(),
            active: None,
            rx: None,
        }
    }

    /// A Run with no child process behind it, for tests that exercise state rather than
    /// capture.
    #[cfg(test)]
    pub fn detached(root: &str, graph: Graph) -> Run {
        let mut tasks = BTreeMap::new();
        for name in graph.reachable(root) {
            tasks.insert(name.clone(), TaskRun::new(name));
        }
        Run {
            root: root.to_string(),
            args: Vec::new(),
            interactive: false,
            force: false,
            graph,
            tasks,
            order: Vec::new(),
            exit: None,
            started: Instant::now(),
            duration: None,
            redacted_secrets: 0,
            stored: false,
            child: Arc::new(Mutex::new(None)),
            writer: Arc::new(Mutex::new(None)),
            cancelled: false,
            provisional: None,
            last_output: Instant::now(),
            sent: String::new(),
            active: None,
            rx: None,
        }
    }

    #[cfg(test)]
    pub fn feed(&mut self, task: &str, text: &str) {
        self.apply(Event::Line {
            task: Some(task.to_string()),
            raw: text.to_string(),
            is_command: false,
        });
    }

    #[cfg(test)]
    pub fn apply_failed_for_test(&mut self, task: &str) {
        self.apply(Event::Failed(task.to_string()));
    }

    #[cfg(test)]
    pub fn finish(&mut self, exit: i32) {
        self.apply(Event::Exited(exit));
    }

    #[cfg(test)]
    pub fn graph_from(pairs: &[(&str, &[&str])]) -> Graph {
        let mut g = Graph::default();
        for (parent, children) in pairs {
            g.edges.insert(
                parent.to_string(),
                children.iter().map(|c| c.to_string()).collect(),
            );
        }
        g
    }

    /// Drain whatever the reader thread has produced. Returns true if anything changed,
    /// so the UI can skip redrawing when nothing has.
    pub fn poll(&mut self) -> bool {
        let Some(rx) = &self.rx else { return false };
        // Drain first, then apply: `apply` takes `&mut self` and may clear `rx`.
        // try_recv rather than blocking, because the UI owns the event loop.
        let batch: Vec<Event> = std::iter::from_fn(|| rx.try_recv().ok()).collect();
        let changed = !batch.is_empty();
        for event in batch {
            self.apply(event);
        }
        changed
    }

    fn apply(&mut self, event: Event) {
        match event {
            Event::Redacting(n) => self.redacted_secrets = n,
            Event::GraphReady(graph) => {
                for name in graph.reachable(&self.root) {
                    self.tasks
                        .entry(name.clone())
                        .or_insert_with(|| TaskRun::new(name));
                }
                self.graph = graph;
            }
            Event::Partial(text) => {
                self.last_output = Instant::now();
                let name = self.active.clone().unwrap_or_else(|| self.root.clone());
                self.touch(&name);
                match self.provisional.clone() {
                    // Same unterminated line growing: replace it in place.
                    Some((t, i)) if t == name => {
                        if let Some(line) = self.tasks.get_mut(&t).and_then(|t| t.lines.get_mut(i))
                        {
                            *line = Line::new(text, false);
                        }
                    }
                    _ => {
                        if let Some(t) = self.tasks.get_mut(&name) {
                            t.lines.push(Line::new(text, false));
                            self.provisional = Some((name, t.lines.len() - 1));
                        }
                    }
                }
            }
            Event::Line {
                task,
                raw,
                is_command,
            } => {
                self.last_output = Instant::now();
                // Untagged, with nothing running yet: go-task's own errors — a missing
                // `requires:` var, an unknown task, a malformed Taskfile — are printed
                // before any task starts, so they carry no `[name]` prefix and there is no
                // active task to inherit. Dropping them left the user with an empty tree
                // and a bare exit code.
                let name = task
                    .or_else(|| self.active.clone())
                    .unwrap_or_else(|| self.root.clone());
                self.touch(&name);
                // A completed line supersedes the provisional one it grew out of.
                if let Some((t, i)) = self.provisional.clone() {
                    if t == name {
                        self.provisional = None;
                        if let Some(line) = self.tasks.get_mut(&t).and_then(|t| t.lines.get_mut(i))
                        {
                            *line = Line::new(raw, is_command);
                            return;
                        }
                    }
                }
                if let Some(t) = self.tasks.get_mut(&name) {
                    t.lines.push(Line::new(raw, is_command));
                }
            }
            Event::Failed(name) => {
                self.touch(&name);
                self.fail(&name);
            }
            Event::Exited(code) => {
                self.provisional = None;
                self.exit = Some(code);
                self.duration = Some(self.started.elapsed());
                self.settle(code);
                self.rx = None;
            }
        }
    }

    /// Note that `name` is producing output now, and close out anything that was running
    /// and is not an ancestor of it.
    fn touch(&mut self, name: &str) {
        if self.active.as_deref() == Some(name) {
            return;
        }

        let ancestors = self.ancestors_of(name);
        let now = Instant::now();
        for (other, t) in self.tasks.iter_mut() {
            if t.status == Status::Running && other != name && !ancestors.contains(other) {
                // A parent stays Running while its children work; a sibling that has
                // stopped producing output has finished.
                t.status = Status::Ok;
                t.duration = t.started.map(|s| now.duration_since(s));
            }
        }

        // Open the whole chain, not just the task itself. An aggregate like `lint` whose
        // commands are all `task:` invocations never produces a line tagged with its own
        // name — every line belongs to a child — so it would otherwise look as though it
        // never ran. Ordered from the root down so `order` reads top-down.
        let chain: Vec<String> = self
            .graph
            .reachable(&self.root)
            .into_iter()
            .filter(|t| ancestors.contains(t))
            .chain(std::iter::once(name.to_string()))
            .collect();

        for task in chain {
            // The graph is built from the invoked root, but go-task can report a task we
            // never saw — an alias, or a path `--summary` did not reveal.
            let entry = self
                .tasks
                .entry(task.clone())
                .or_insert_with(|| TaskRun::new(task.clone()));
            if entry.started.is_none() {
                entry.started = Some(now);
                self.order.push(task);
            }
            if entry.status == Status::Pending {
                entry.status = Status::Running;
            }
        }
        self.active = Some(name.to_string());
    }

    fn ancestors_of(&self, name: &str) -> HashSet<String> {
        let mut out = HashSet::new();
        for (parent, children) in &self.graph.edges {
            if children.iter().any(|c| c == name) {
                out.insert(parent.clone());
                // Walk up. Depth here is tiny, so the repeated scan is fine.
                for grand in self.ancestors_of(parent) {
                    out.insert(grand);
                }
            }
        }
        out
    }

    fn fail(&mut self, name: &str) {
        let now = Instant::now();
        if let Some(t) = self.tasks.get_mut(name) {
            t.status = Status::Failed;
            t.duration = t.started.map(|s| now.duration_since(s));
        }
        // A failing task fails everything that invoked it.
        for parent in self.ancestors_of(name) {
            if let Some(t) = self.tasks.get_mut(&parent) {
                t.status = Status::Failed;
                t.duration = t.started.map(|s| now.duration_since(s));
            }
        }
    }

    /// Resolve whatever is still Running once the process is gone.
    fn settle(&mut self, exit: i32) {
        let now = Instant::now();
        for t in self.tasks.values_mut() {
            match t.status {
                Status::Running => {
                    // Only call it good if the run itself succeeded; on a failure with no
                    // named culprit, an unfinished task is not a pass.
                    t.status = if exit == 0 {
                        Status::Ok
                    } else {
                        Status::Failed
                    };
                    t.duration = t.started.map(|s| now.duration_since(s));
                }
                // Never produced a line and never opened as an ancestor: not reached.
                Status::Pending => t.status = Status::Skipped,
                _ => {}
            }
        }
    }
}

/// Drive `task --output prefixed <root>` on a pty and stream parsed events, blocking
/// until the child exits.
/// Everything one capture needs. A struct rather than eight positional parameters, which
/// is both what clippy asks for and harder to pass in the wrong order.
struct Capture<'a> {
    dir: &'a Path,
    root: &'a str,
    args: &'a [String],
    interactive: bool,
    force: bool,
    redactor: &'a Redactor,
    child: &'a ChildHandle,
    writer: &'a WriterHandle,
}

fn capture(c: Capture<'_>, tx: &Sender<Event>) -> Result<()> {
    let Capture {
        dir,
        root,
        args,
        interactive,
        force,
        redactor,
        child: handle,
        writer: writer_handle,
    } = c;
    let pty = native_pty_system();
    let pair = pty.openpty(PtySize {
        rows: 50,
        // Wide, so tools that wrap to the terminal width do not hard-wrap the capture at
        // something narrow. The UI wraps for display instead.
        cols: 200,
        pixel_width: 0,
        pixel_height: 0,
    })?;

    let mut cmd = CommandBuilder::new("task");
    let mode = if interactive {
        "interleaved"
    } else {
        "prefixed"
    };
    cmd.args(["--output", mode, root]);
    // `--force` before the user's own arguments: theirs may include a `--` separator,
    // after which everything is `CLI_ARGS` rather than a flag.
    if force {
        cmd.arg("--force");
    }
    // Passed through verbatim, already split shell-style: `--` and `NAME=value` are just
    // argv entries to go-task.
    for a in args {
        cmd.arg(a);
    }
    cmd.cwd(dir);
    cmd.env("TERM", "xterm-256color");
    // Commands run behind go-task's prefixing pipe, so tty detection fails for them.
    // These are the escape hatches the major toolchains honour regardless of tty.
    cmd.env("CARGO_TERM_COLOR", "always"); // cargo, clippy
    cmd.env("CLICOLOR_FORCE", "1"); // git, BSD coreutils
    cmd.env("FORCE_COLOR", "1"); // the node ecosystem

    let child = pair.slave.spawn_command(cmd)?;
    // Publish the handle before reading, so a cancel arriving immediately still lands.
    if let Ok(mut guard) = handle.lock() {
        *guard = Some(child);
    }
    if let Ok(w) = pair.master.take_writer() {
        if let Ok(mut guard) = writer_handle.lock() {
            *guard = Some(w);
        }
    }
    // The slave must be dropped or the reader never sees EOF.
    drop(pair.slave);
    let mut reader = pair.master.try_clone_reader()?;

    let mut buf = [0u8; 8192];
    let mut pending = Vec::new();
    loop {
        match reader.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => {
                pending.extend_from_slice(&buf[..n]);
                // A pty gives us CRLF; split on LF and drop the CR.
                while let Some(pos) = pending.iter().position(|&b| b == b'\n') {
                    let mut line: Vec<u8> = pending.drain(..=pos).collect();
                    line.pop();
                    if line.last() == Some(&b'\r') {
                        line.pop();
                    }
                    // Mask here, at the boundary: nothing unredacted is ever put on the
                    // channel, so no later code path can leak what it never received.
                    let text = String::from_utf8_lossy(&line).into_owned();
                    let text = apply_overwrites(&text);
                    let text = redactor.redact(&text).into_owned();
                    for event in parse_line(&text) {
                        if tx.send(event).is_err() {
                            return Ok(());
                        }
                    }
                }

                // Whatever is left has no newline yet. Emit it anyway: a prompt never
                // gets one, and waiting for it means the run looks hung.
                if !pending.is_empty() {
                    let tail = String::from_utf8_lossy(&pending).into_owned();
                    let tail = apply_overwrites(&tail);
                    let tail = redactor.redact(&tail).into_owned();
                    if tx.send(Event::Partial(tail)).is_err() {
                        return Ok(());
                    }
                }
            }
        }
    }

    // Keep the master alive until the child is reaped, then report the exit code.
    let code = handle
        .lock()
        .ok()
        .and_then(|mut g| {
            g.as_mut()
                .map(|c| c.wait().map(|s| s.exit_code() as i32).unwrap_or(-1))
        })
        .unwrap_or(-1);
    drop(pair.master);
    let _ = tx.send(Event::Exited(code));
    Ok(())
}

/// Apply in-place overwrite semantics: carriage return and backspace.
///
/// Progress indicators redraw without a newline. `cargo` and downloaders use `\r` to
/// return to column zero; npm and npx spinners use `\b` to rub out the previous frame.
/// Kept verbatim, one "line" arrives as `10%\r50%\r100%` or `\|/-\|/-Need to install`,
/// which renders as control characters, wraps into several rows, and lets a search match
/// a state that was never the final answer.
///
/// This is not terminal emulation: a short write over a longer one leaves the old tail
/// visible on a real terminal and does not here. For progress output that is invisible,
/// and the alternative is a column-tracking screen buffer for a cosmetic edge case.
fn apply_overwrites(text: &str) -> String {
    if !text.contains(['\r', '\u{8}']) {
        return text.to_string();
    }
    let mut out = String::new();
    // A line ending in `\r` has not been overwritten by anything, so the text before it
    // still stands; without this, "done\r" would come out empty.
    let mut last_written = String::new();
    for c in text.chars() {
        match c {
            '\r' => {
                if !out.is_empty() {
                    last_written = std::mem::take(&mut out);
                }
                out.clear();
            }
            '\u{8}' => {
                out.pop();
            }
            c => out.push(c),
        }
    }
    if out.is_empty() {
        last_written
    } else {
        out
    }
}

/// Turn one line of `task --output prefixed` into events.
///
/// Three shapes matter:
///   `task: [name] cmd`  go-task echoing the command it is about to run
///   `[name] output`     an output line, tagged by `--output prefixed`
///   `task: Failed to run task "name": …`
fn parse_line(text: &str) -> Vec<Event> {
    let stripped =
        String::from_utf8_lossy(&strip_ansi_escapes::strip(text.as_bytes())).into_owned();

    if let Some(rest) = stripped.strip_prefix("task: [") {
        if let Some((name, _cmd)) = rest.split_once("] ") {
            return vec![Event::Line {
                task: Some(name.to_string()),
                raw: text.to_string(),
                is_command: true,
            }];
        }
    }

    if stripped.starts_with("task: ") {
        // `task: Failed to run task "agg": task: Failed to run task "c": exit status 3`
        // — the innermost name is the one that actually failed.
        let mut culprit = None;
        let mut rest = stripped.as_str();
        while let Some(i) = rest.find("Failed to run task \"") {
            rest = &rest[i + "Failed to run task \"".len()..];
            if let Some(end) = rest.find('"') {
                culprit = Some(rest[..end].to_string());
            }
        }
        let mut events = Vec::new();
        if let Some(name) = culprit {
            events.push(Event::Failed(name));
        }
        events.push(Event::Line {
            task: None,
            raw: text.to_string(),
            is_command: false,
        });
        return events;
    }

    if let Some(rest) = stripped.strip_prefix('[') {
        if let Some((name, _)) = rest.split_once("] ") {
            // Trust the tag only if it looks like a task name — output that happens to
            // start with `[` should not invent a task.
            if !name.is_empty()
                && name
                    .chars()
                    .all(|c| c.is_alphanumeric() || ":-_.".contains(c))
            {
                let raw = text
                    .find("] ")
                    .map(|i| text[i + 2..].to_string())
                    .unwrap_or_else(|| text.to_string());
                return vec![Event::Line {
                    task: Some(name.to_string()),
                    raw,
                    is_command: false,
                }];
            }
        }
    }

    vec![Event::Line {
        task: None,
        raw: text.to_string(),
        is_command: false,
    }]
}

#[cfg(test)]
mod tests {
    use super::*;

    fn one(text: &str) -> (Option<String>, String, bool) {
        match parse_line(text).into_iter().next().unwrap() {
            Event::Line {
                task,
                raw,
                is_command,
            } => (task, raw, is_command),
            other => panic!("expected a line, got {other:?}"),
        }
    }

    #[test]
    fn command_echo_is_attributed_and_marked() {
        let (task, raw, is_command) = one(r#"task: [backend:lint] cargo clippy --all"#);
        assert_eq!(task.as_deref(), Some("backend:lint"));
        assert!(is_command);
        assert!(raw.contains("cargo clippy"));
    }

    /// The prefix is structure, not content — it is stripped so search and display see
    /// the line the tool actually printed.
    #[test]
    fn prefixed_output_is_attributed_and_the_tag_removed() {
        let (task, raw, is_command) = one("[backend:lint] warning: unused variable");
        assert_eq!(task.as_deref(), Some("backend:lint"));
        assert_eq!(raw, "warning: unused variable");
        assert!(!is_command);
    }

    /// Output that merely starts with a bracket must not invent a task.
    #[test]
    fn bracketed_output_that_is_not_a_task_tag_is_left_alone() {
        let (task, raw, _) = one("[2026-08-25 18:04:11] server listening");
        assert_eq!(task, None);
        assert_eq!(raw, "[2026-08-25 18:04:11] server listening");
    }

    /// go-task nests the message; the innermost name is the task that actually failed,
    /// not the aggregate that contained it.
    #[test]
    fn failure_reports_the_innermost_task() {
        let events = parse_line(
            r#"task: Failed to run task "all": task: Failed to run task "backend:lint": exit status 1"#,
        );
        match &events[0] {
            Event::Failed(name) => assert_eq!(name, "backend:lint"),
            other => panic!("expected a failure, got {other:?}"),
        }
    }

    #[test]
    fn ansi_is_stripped_for_search_but_kept_for_display() {
        let line = Line::new("\x1b[31merror\x1b[0m: boom".into(), false);
        assert_eq!(line.plain, "error: boom");
        assert!(line.raw.contains('\x1b'), "colour survives for rendering");
    }

    fn graph_of(pairs: &[(&str, &[&str])]) -> Graph {
        Run::graph_from(pairs)
    }

    fn run_of(root: &str, graph: Graph) -> Run {
        Run::detached(root, graph)
    }

    fn line(run: &mut Run, task: &str, text: &str) {
        run.apply(Event::Line {
            task: Some(task.to_string()),
            raw: text.to_string(),
            is_command: false,
        });
    }

    /// A parent keeps running while its children work; a sibling that has stopped
    /// producing output has finished.
    #[test]
    fn a_parent_stays_running_while_a_child_works() {
        let g = graph_of(&[("all", &["lint", "test"]), ("lint", &["backend:lint"])]);
        let mut run = run_of("all", g);

        line(&mut run, "all", "starting");
        line(&mut run, "lint", "linting");
        line(&mut run, "backend:lint", "clippy");

        assert_eq!(run.tasks["all"].status, Status::Running, "ancestor");
        assert_eq!(run.tasks["lint"].status, Status::Running, "ancestor");
        assert_eq!(run.tasks["backend:lint"].status, Status::Running);

        // Moving to a different branch closes out the one we left.
        line(&mut run, "test", "testing");
        assert_eq!(run.tasks["backend:lint"].status, Status::Ok);
        assert_eq!(run.tasks["lint"].status, Status::Ok);
        assert_eq!(run.tasks["all"].status, Status::Running, "still the root");
    }

    #[test]
    fn a_failure_propagates_to_everything_that_invoked_it() {
        let g = graph_of(&[("all", &["lint"]), ("lint", &["backend:lint"])]);
        let mut run = run_of("all", g);
        line(&mut run, "backend:lint", "error: boom");

        run.apply(Event::Failed("backend:lint".into()));

        assert_eq!(run.tasks["backend:lint"].status, Status::Failed);
        assert_eq!(run.tasks["lint"].status, Status::Failed);
        assert_eq!(run.tasks["all"].status, Status::Failed);
    }

    /// Tasks in the graph that never produced output were never reached.
    #[test]
    fn unreached_tasks_are_skipped_not_passed() {
        let g = graph_of(&[("all", &["lint", "test"])]);
        let mut run = run_of("all", g);
        line(&mut run, "lint", "linting");
        run.apply(Event::Failed("lint".into()));
        run.apply(Event::Exited(1));

        assert_eq!(run.tasks["lint"].status, Status::Failed);
        assert_eq!(run.tasks["test"].status, Status::Skipped);
        assert!(run.finished());
    }

    /// A clean exit closes out whatever was still open.
    #[test]
    fn a_clean_exit_settles_running_tasks_as_ok() {
        let g = graph_of(&[("all", &["lint"])]);
        let mut run = run_of("all", g);
        line(&mut run, "all", "go");
        line(&mut run, "lint", "linting");
        run.apply(Event::Exited(0));

        assert_eq!(run.tasks["all"].status, Status::Ok);
        assert_eq!(run.tasks["lint"].status, Status::Ok);
        assert_eq!(run.exit, Some(0));
    }

    /// Cancelling has to reap the shell commands, not just `task` itself — they are what
    /// is actually doing the work, and they outlive their parent unless the whole process
    /// group is signalled.
    ///
    /// This spawns a real `task` process, so it is slower than the rest of the suite.
    #[test]
    fn cancelling_stops_the_child_and_its_commands() {
        let dir = std::env::temp_dir().join(format!("taskui-cancel-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(
            dir.join("Taskfile.yml"),
            "version: \"3\"\ntasks:\n  sleeper:\n    cmds: ['echo started', 'sleep 30']\n",
        )
        .unwrap();

        let mut run = Run::start(&dir, "sleeper", &[], false, false).unwrap();

        // Wait for the command to actually be running.
        let began = Instant::now();
        while began.elapsed() < Duration::from_secs(15) {
            run.poll();
            if run
                .tasks
                .get("sleeper")
                .is_some_and(|t| !t.lines.is_empty())
            {
                break;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        assert!(!run.finished(), "still sleeping when we cancel");

        run.cancel();

        // The exit event should follow promptly; without group-killing, `sleep 30` would
        // hold the pty open and this would time out.
        let cancelled_at = Instant::now();
        while cancelled_at.elapsed() < Duration::from_secs(10) && !run.finished() {
            run.poll();
            std::thread::sleep(Duration::from_millis(50));
        }

        assert!(run.cancelled());
        assert!(
            run.finished(),
            "the run ended instead of hanging on `sleep 30`"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// Can a task be typed at under `--output prefixed`?
    ///
    /// go-task wraps stdout and stderr for prefixing but leaves stdin alone, so the
    /// answer should be yes even though the prompt itself never becomes visible. If it
    /// is, blind input beats forcing a whole re-run of something like a deploy.
    #[test]
    fn stdin_reaches_the_child_even_when_output_is_prefixed() {
        let dir = std::env::temp_dir().join(format!("taskui-blind-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(
            dir.join("Taskfile.yml"),
            "version: \"3\"\ntasks:\n  ask:\n    cmds:\n      - 'printf \"Proceed? \"; read ans; echo; echo \"got:$ans\"'\n",
        )
        .unwrap();

        // interactive = false, i.e. --output prefixed.
        let mut run = Run::start(&dir, "ask", &[], false, false).unwrap();

        let began = Instant::now();
        // No prompt will appear, so wait for the command echo instead.
        while began.elapsed() < Duration::from_secs(20) {
            run.poll();
            if run.tasks.get("ask").is_some_and(|t| !t.lines.is_empty()) {
                break;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        std::thread::sleep(Duration::from_millis(300));
        assert!(!run.finished(), "blocked on the read");

        assert!(run.send_input(b"y\r"), "wrote to the pty");

        while began.elapsed() < Duration::from_secs(20) && !run.finished() {
            run.poll();
            std::thread::sleep(Duration::from_millis(50));
        }
        run.poll();

        let output: String = run.tasks["ask"]
            .lines
            .iter()
            .map(|l| l.plain.clone())
            .collect();
        assert!(run.finished(), "it proceeded: {output:?}");
        assert!(
            output.contains("got:y"),
            "the task saw the answer: {output:?}"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// A running task must report time as it goes, not only once it stops — otherwise
    /// the only live figure on screen is the total, which does not say which step is slow.
    #[test]
    fn a_running_task_reports_elapsed_time() {
        let g = graph_of(&[("all", &["slow"])]);
        let mut run = run_of("all", g);
        line(&mut run, "slow", "working");

        let ticking = run.tasks["slow"].elapsed();
        assert!(ticking.is_some(), "a running task has a clock");
        assert!(run.tasks["all"].elapsed().is_some(), "so does its parent");

        run.apply(Event::Exited(0));
        let finished = run.tasks["slow"].elapsed().unwrap();
        assert!(
            finished >= ticking.unwrap(),
            "and it settles at the final figure"
        );
    }

    /// Never started means no time, rather than a misleading zero.
    #[test]
    fn an_unreached_task_has_no_clock() {
        let g = graph_of(&[("all", &["a", "b"])]);
        let mut run = run_of("all", g);
        line(&mut run, "a", "hello");
        run.apply(Event::Exited(0));
        assert!(run.tasks["b"].elapsed().is_none());
    }

    /// Progress output redraws in place; only the final state is real.
    #[test]
    fn carriage_returns_collapse_to_the_last_write() {
        assert_eq!(
            apply_overwrites("Downloading  10%\rDownloading  50%\rDownloading 100%"),
            "Downloading 100%"
        );
        assert_eq!(apply_overwrites("plain line"), "plain line");
        // A trailing `\r` is not a write; the text before it stands.
        assert_eq!(apply_overwrites("done\r"), "done");
        assert_eq!(apply_overwrites(""), "");
    }

    /// npm and npx spinners rub out each frame with backspaces rather than `\r`, which is
    /// why `\|/-\|/-Need to install…` arrived with every frame still attached.
    #[test]
    fn backspaces_rub_out_the_previous_character() {
        assert_eq!(apply_overwrites("\u{8}|\u{8}/\u{8}-\u{8}"), "");
        assert_eq!(
            apply_overwrites("\\\u{8}|\u{8}/\u{8}-\u{8}Need to install"),
            "Need to install"
        );
        assert_eq!(apply_overwrites("abc\u{8}\u{8}"), "a");
        // More backspaces than characters is not a panic.
        assert_eq!(apply_overwrites("a\u{8}\u{8}\u{8}"), "");
    }

    /// A prompt never gets a newline, so a strictly line-based reader shows nothing and
    /// the run looks hung. The unterminated tail has to surface as a line of its own.
    #[test]
    fn an_unterminated_prompt_is_shown() {
        let g = graph_of(&[("deploy", &[])]);
        let mut run = run_of("deploy", g);
        run.apply(Event::Line {
            task: Some("deploy".into()),
            raw: "Deploying".into(),
            is_command: false,
        });
        run.apply(Event::Partial("Do you want to proceed? (y/n) ".into()));

        assert_eq!(run.tasks["deploy"].lines.len(), 2);
        assert_eq!(run.pending_prompt(), Some("Do you want to proceed? (y/n) "));
        assert!(run.looks_like_a_prompt());
    }

    /// Reads arrive in chunks, so the tail grows. It must be replaced, not stacked up.
    #[test]
    fn a_growing_partial_replaces_itself() {
        let g = graph_of(&[("deploy", &[])]);
        let mut run = run_of("deploy", g);
        run.apply(Event::Partial("Do you want".into()));
        run.apply(Event::Partial("Do you want to proceed? ".into()));

        assert_eq!(
            run.tasks["deploy"].lines.len(),
            1,
            "one line, not one per read"
        );
        assert_eq!(run.pending_prompt(), Some("Do you want to proceed? "));
    }

    /// Once the newline arrives, the completed line supersedes the provisional one.
    #[test]
    fn a_completed_line_supersedes_its_partial() {
        let g = graph_of(&[("deploy", &[])]);
        let mut run = run_of("deploy", g);
        run.apply(Event::Partial("half a li".into()));
        run.apply(Event::Line {
            task: Some("deploy".into()),
            raw: "half a line, now whole".into(),
            is_command: false,
        });

        assert_eq!(run.tasks["deploy"].lines.len(), 1);
        assert_eq!(run.tasks["deploy"].lines[0].plain, "half a line, now whole");
        assert_eq!(run.pending_prompt(), None, "no longer waiting");
    }

    /// Ordinary wrapped output is not a question; the hint must not cry wolf.
    #[test]
    fn plain_output_is_not_mistaken_for_a_prompt() {
        let g = graph_of(&[("build", &[])]);
        let mut run = run_of("build", g);
        run.apply(Event::Partial("   Compiling taskui v0.1.0".into()));
        assert!(!run.looks_like_a_prompt());
    }

    /// The whole interactive loop against a real `task` process: the prompt surfaces
    /// without a newline, the answer goes back down the pty, and the task proceeds.
    ///
    /// This is the `wrangler`/`terraform` case — before this, such a task simply hung with
    /// a blank screen and the only way out was killing it.
    #[test]
    fn answers_an_interactive_prompt() {
        let dir = std::env::temp_dir().join(format!("taskui-prompt-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(
            dir.join("Taskfile.yml"),
            "version: \"3\"\ntasks:\n  ask:\n    cmds:\n      - 'printf \"Proceed? (y/n) \"; read ans; echo; echo \"got:$ans\"'\n",
        )
        .unwrap();

        // Interactive: under `--output prefixed` the prompt never arrives at all.
        let mut run = Run::start(&dir, "ask", &[], true, false).unwrap();

        let began = Instant::now();
        while began.elapsed() < Duration::from_secs(20) {
            run.poll();
            if run.looks_like_a_prompt() {
                break;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        let prompt = run.pending_prompt().unwrap_or_default().to_string();
        assert!(
            prompt.contains("Proceed?"),
            "the prompt surfaced: {prompt:?}"
        );
        assert!(!run.finished(), "still waiting on us");

        assert!(run.send_input(b"y\r"), "the answer reached the pty");

        while began.elapsed() < Duration::from_secs(20) && !run.finished() {
            run.poll();
            std::thread::sleep(Duration::from_millis(50));
        }
        run.poll();

        assert!(run.finished(), "it proceeded instead of hanging");
        let output: String = run.tasks["ask"]
            .lines
            .iter()
            .map(|l| l.plain.clone())
            .collect();
        assert!(
            output.contains("got:y"),
            "the task saw the answer: {output:?}"
        );
        let _ = std::fs::remove_dir_all(&dir);
    }

    /// A failure before any task starts still has to be visible: it lands on the task
    /// that was invoked.
    #[test]
    fn run_level_errors_are_attributed_to_the_root() {
        let g = graph_of(&[("wt:new", &[])]);
        let mut run = run_of("wt:new", g);
        run.apply(Event::Line {
            task: None,
            raw: r#"task: Task "wt:new" cancelled because it is missing required variables: NAME"#
                .into(),
            is_command: false,
        });
        run.apply(Event::Exited(206));

        let root = &run.tasks["wt:new"];
        assert_eq!(root.lines.len(), 1, "the error is not swallowed");
        assert!(root.lines[0].plain.contains("missing required variables"));
    }

    /// Untagged lines belong to whoever spoke last, not to nobody.
    #[test]
    fn untagged_lines_follow_the_active_task() {
        let g = graph_of(&[("all", &["lint"])]);
        let mut run = run_of("all", g);
        line(&mut run, "lint", "first");
        run.apply(Event::Line {
            task: None,
            raw: "continuation".into(),
            is_command: false,
        });
        assert_eq!(run.tasks["lint"].lines.len(), 2);
        assert_eq!(run.tasks["lint"].lines[1].plain, "continuation");
    }
    /// An aggregate whose commands are all `task:` invocations never emits a line tagged
    /// with its own name — every line belongs to a child. It still ran, and must not be
    /// reported as skipped.
    #[test]
    fn an_aggregate_with_no_output_of_its_own_still_ran() {
        let g = graph_of(&[("agg", &["lint"]), ("lint", &["a", "b"])]);
        let mut run = run_of("agg", g);

        line(&mut run, "a", "a hello");
        assert_eq!(run.tasks["agg"].status, Status::Running);
        assert_eq!(run.tasks["lint"].status, Status::Running);

        line(&mut run, "b", "b hello");
        run.apply(Event::Exited(0));

        assert_eq!(run.tasks["agg"].status, Status::Ok);
        assert_eq!(run.tasks["lint"].status, Status::Ok);
        assert!(run.tasks["agg"].duration.is_some(), "and it has a duration");
    }

    /// Opening the chain must go top-down, so `order` reads the way the run reads.
    #[test]
    fn order_opens_ancestors_before_the_task_itself() {
        let g = graph_of(&[("agg", &["lint"]), ("lint", &["a"])]);
        let mut run = run_of("agg", g);
        line(&mut run, "a", "hello");
        assert_eq!(run.order, ["agg", "lint", "a"]);
    }
}
