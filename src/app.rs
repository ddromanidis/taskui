//! Application state: the task list, the active pivot, fold state, filter and cursor.

use crate::pivot::{self, Mode, Row, Tree};
use crate::run::{Run, Status};
use crate::search::{self, LiveHit};
use crate::store;
use crate::task::Task;
use crate::theme::{Config as ThemeConfig, Theme};
use nucleo_matcher::pattern::{CaseMatching, Normalization, Pattern};
use nucleo_matcher::{Config, Matcher, Utf32Str};
use std::collections::{HashMap, HashSet};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Screen {
    /// Browsing the Taskfile.
    Picker,
    /// Watching a run.
    Run,
    /// Browsing runs that already happened.
    History,
    /// The keymap.
    Help,
    /// What a task is and what it will run.
    Detail,
}

/// A row in the run view. Output lines are rows of the same list as the tasks that
/// produced them, which is what makes one fold tree hold both.
#[derive(Debug, Clone)]
pub enum RunRow {
    Task {
        name: String,
        depth: usize,
        open: bool,
    },
    Line {
        task: String,
        index: usize,
        depth: usize,
    },
}

/// Why a run is waiting for a yes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConfirmReason {
    /// The task is on the `.taskui-danger` list.
    TouchesProduction,
    /// Something else is still running and starting this would kill it.
    WouldStopRunning,
}

pub struct App {
    pub tasks: Vec<Task>,
    pub mode: Mode,
    pub tree: Tree,
    pub rows: Vec<Row>,
    pub cursor: usize,
    /// Viewport offset, kept here so scrolling survives rebuilds.
    pub offset: usize,

    /// Fold state is per-mode: bouncing between pivots must not collapse what you opened
    /// on the other side.
    expanded: HashMap<&'static str, HashSet<String>>,

    pub filtering: bool,
    pub query: String,
    matcher: Matcher,

    pub root: std::path::PathBuf,
    pub status: Option<String>,
    pub theme: Theme,
    pub keymap: crate::keys::Keymap,

    pub screen: Screen,
    pub run: Option<Run>,
    pub run_rows: Vec<RunRow>,
    pub run_cursor: usize,
    pub run_offset: usize,
    run_expanded: HashSet<String>,
    /// While true the view tracks whatever is running. Any manual cursor move turns it
    /// off — once you have gone looking for something, the view should stop moving under
    /// you.
    pub following: bool,
    /// So a failure yanks the view exactly once, not on every subsequent poll.
    focused_failure: Option<String>,

    /// Output search. Distinct from `query`, which filters task *names* in the picker —
    /// 122 short strings versus potentially megabytes of output, so they get separate
    /// affordances even though both are bound to `/`.
    pub search: Option<search::Query>,
    pub searching: bool,
    pub search_input: String,
    pub search_hits: Vec<LiveHit>,
    pub search_idx: usize,
    /// Show only matching lines, grouped under the task that produced them.
    pub filter_matches: bool,
    pub search_error: Option<String>,
    /// Where the finished run was written, if it was.
    pub saved_to: Option<std::path::PathBuf>,

    /// How each task went last time, so browsing answers "what is broken right now"
    /// without opening anything.
    pub outcomes: std::collections::HashMap<String, store::Outcome>,

    pub history: Vec<store::Manifest>,
    pub history_cursor: usize,
    pub history_offset: usize,
    /// History is scoped to this project by default. Runs from every project in one list
    /// stops being useful the moment you use taskui in two repos.
    pub history_all_projects: bool,
    /// Jump-to-task. Unlike the filter, the list stays whole and only the cursor moves —
    /// which is what you want when you are looking *at* the tree rather than narrowing it.
    pub jumping: bool,
    pub jump_query: String,
    pub jump_matches: Vec<usize>,
    pub jump_idx: usize,
    /// Where the cursor was before the jump, so `esc` really does cancel.
    jump_origin: usize,

    /// Half of a `gg`. Any other key clears it, so a stray `g` cannot lurk.
    pub pending_g: bool,
    /// Where `?` was pressed, so `esc` puts you back rather than somewhere arbitrary.
    help_return: Option<Screen>,
    pub help_offset: u16,

    /// The task the detail panel is describing, and what `--summary` said about it.
    pub detail_of: Option<String>,
    pub detail: crate::graph::Detail,
    pub detail_offset: u16,

    /// Re-run the watched task when the source changes.
    pub watching: Option<String>,
    watcher: Option<crate::watch::Watch>,

    /// Body height of the last frame, so `^d` can move by half a screen.
    pub viewport: usize,

    /// Lines of context kept either side of a hit in the filtered view. A `FAIL:` line
    /// without the assertion underneath it is the half that does not tell you anything.
    pub filter_context: usize,

    /// Args prompt. Plenty of tasks need them — `wt:new NAME=backend`,
    /// `backend:test -- -p ingest` — and a runner that cannot pass arguments can only
    /// run half of a real Taskfile.
    pub entering_args: bool,
    pub args_input: String,
    /// Caret position, in characters. Pre-filling `NAME=` is only useful if the caret
    /// lands after the `=`, ready for the value.
    pub args_cursor: usize,
    /// Which task the prompt is for.
    pub args_target: Option<String>,

    /// What a pending confirmation is about, so the bar can say why it is asking.
    pub confirm_reason: ConfirmReason,

    /// A dangerous task waiting on a yes. `⏎` runs things for real, and a fuzzy filter
    /// puts every task one keypress away, so the ones that touch production get a stop.
    pub confirm: Option<(String, Vec<String>)>,

    /// Keystrokes go to the running task instead of to taskui. Deliberately a mode: half
    /// the run view's keys are single letters, and `y` meaning both "yes" and "move the
    /// cursor" would be intolerable.
    pub sending_input: bool,
    /// Sticky: the next run goes out interleaved so the task can ask questions. A toggle
    /// rather than a separate key, so `⏎` and the args prompt both honour it.
    pub interactive_next: bool,
    /// Sticky: the next run passes `--force`, so go-task's up-to-date checks do not skip
    /// it. Without this, `⏎` on a cached task prints one cryptic line and looks broken.
    pub force_next: bool,

    /// Cross-run search over the archive, from the history list.
    pub history_searching: bool,
    pub history_query: String,
    /// Run id -> hits, for the runs that matched.
    pub history_hits: std::collections::BTreeMap<String, usize>,
}

impl App {
    pub fn new(tasks: Vec<Task>, root: std::path::PathBuf) -> Self {
        let mut app = App {
            tasks,
            mode: Mode::Domain,
            tree: Tree::default(),
            rows: Vec::new(),
            cursor: 0,
            offset: 0,
            expanded: HashMap::new(),
            filtering: false,
            query: String::new(),
            matcher: Matcher::new(Config::DEFAULT),
            root,
            status: None,
            theme: Theme::default(),
            keymap: crate::keys::Keymap::default(),
            screen: Screen::Picker,
            run: None,
            run_rows: Vec::new(),
            run_cursor: 0,
            run_offset: 0,
            run_expanded: HashSet::new(),
            following: true,
            focused_failure: None,
            search: None,
            searching: false,
            search_input: String::new(),
            search_hits: Vec::new(),
            search_idx: 0,
            filter_matches: false,
            search_error: None,
            saved_to: None,
            outcomes: std::collections::HashMap::new(),
            history: Vec::new(),
            history_cursor: 0,
            history_offset: 0,
            history_all_projects: false,
            jumping: false,
            jump_query: String::new(),
            jump_matches: Vec::new(),
            jump_idx: 0,
            jump_origin: 0,
            pending_g: false,
            help_return: None,
            help_offset: 0,
            detail_of: None,
            detail: crate::graph::Detail::default(),
            detail_offset: 0,
            watching: None,
            watcher: None,
            viewport: 20,
            filter_context: 2,
            entering_args: false,
            args_input: String::new(),
            args_cursor: 0,
            args_target: None,
            confirm: None,
            confirm_reason: ConfirmReason::TouchesProduction,
            sending_input: false,
            interactive_next: false,
            force_next: false,
            history_searching: false,
            history_query: String::new(),
            history_hits: std::collections::BTreeMap::new(),
        };
        app.rebuild(None);
        app.reload_outcomes();
        app
    }

    pub fn reload_outcomes(&mut self) {
        self.outcomes = store::last_outcomes(&store::state_dir(), &self.root);
    }

    /// Apply a loaded config. Anything wrong with the file is surfaced rather than
    /// swallowed — a colour that silently does nothing is worse than one that says why.
    pub fn with_config(mut self, config: ThemeConfig) -> Self {
        self.theme = config.theme;
        self.keymap = config.keymap;
        if !config.problems.is_empty() {
            self.status = Some(format!("config: {}", config.problems.join("; ")));
        }
        self
    }

    /// Kick off `task <name>` and switch to the run view.
    pub fn start_run(&mut self, name: &str) -> anyhow::Result<()> {
        self.start_run_with(name, &[])
    }

    /// Return to a run already in progress.
    pub fn resume_run(&mut self) -> bool {
        if self.run.is_none() {
            self.status = Some("no run to go back to".into());
            return false;
        }
        self.screen = Screen::Run;
        self.status = None;
        true
    }

    /// Run it, unless something needs a yes first.
    pub fn request_run(&mut self, name: &str, args: &[String]) {
        // Selecting the task that is already running means "show me it", not "start a
        // second one" — and starting a second one would kill the first, which on a
        // half-finished deploy is the worst thing this tool could do.
        if self
            .run
            .as_ref()
            .is_some_and(|r| !r.finished() && r.root == name)
        {
            self.resume_run();
            return;
        }

        if self.confirm.is_none() {
            let dangerous = self.tasks.iter().any(|t| t.name == name && t.dangerous);
            if dangerous {
                self.confirm_reason = ConfirmReason::TouchesProduction;
                self.confirm = Some((name.to_string(), args.to_vec()));
                return;
            }
            // Starting anything new drops the current Run, and dropping it kills the
            // process group. Say so rather than doing it silently.
            if self.run_in_flight() {
                self.confirm_reason = ConfirmReason::WouldStopRunning;
                self.confirm = Some((name.to_string(), args.to_vec()));
                return;
            }
        }
        self.confirm = None;
        if let Err(e) = self.start_run_with(name, args) {
            self.status = Some(format!("could not start `task {name}`: {e}"));
        }
    }

    pub fn confirm_yes(&mut self) {
        let Some((name, args)) = self.confirm.take() else {
            return;
        };
        if let Err(e) = self.start_run_with(&name, &args) {
            self.status = Some(format!("could not start `task {name}`: {e}"));
        }
    }

    pub fn confirm_no(&mut self) {
        if self.confirm.take().is_some() {
            self.status = Some("not run".into());
        }
    }

    pub fn start_run_with(&mut self, name: &str, args: &[String]) -> anyhow::Result<()> {
        let run = Run::start(
            &self.root,
            name,
            args,
            self.interactive_next,
            self.force_next,
        )?;
        self.run = Some(run);
        self.screen = Screen::Run;
        self.run_cursor = 0;
        self.run_offset = 0;
        self.run_expanded.clear();
        self.following = true;
        self.focused_failure = None;
        self.status = None;
        self.search = None;
        self.searching = false;
        self.search_input.clear();
        self.search_hits.clear();
        self.search_idx = 0;
        self.filter_matches = false;
        self.search_error = None;
        self.saved_to = None;
        self.rebuild_run_rows();
        Ok(())
    }

    /// Open the args prompt for the task under the cursor, pre-filled with the variables
    /// the task actually asks for.
    ///
    /// `requires: { vars: [NAME] }` is a declaration, not prose, so `NAME=` can be filled
    /// in with confidence — and only the key, never the example value, which would be
    /// handing you someone else's argument. The caret lands after the last `=`.
    pub fn begin_args(&mut self, name: &str) {
        self.entering_args = true;
        self.args_target = Some(name.to_string());
        self.status = None;

        // One `--summary`, ~40ms, only when the prompt opens.
        let mut keys = crate::graph::required_vars(&self.root, name);
        if keys.is_empty() {
            // Nothing declared: fall back to a `KEY=value` shape in the description.
            if let Some(hint) = self.args_hint() {
                keys = crate::task::keys_in_hint(&hint);
            }
        }

        self.args_input = keys
            .iter()
            .map(|k| format!("{k}="))
            .collect::<Vec<_>>()
            .join(" ");
        self.args_cursor = self.args_input.chars().count();
    }

    pub fn cancel_args(&mut self) {
        self.entering_args = false;
        self.args_target = None;
        self.args_input.clear();
        self.args_cursor = 0;
    }

    fn byte_at(&self, cursor: usize) -> usize {
        self.args_input
            .char_indices()
            .nth(cursor)
            .map(|(i, _)| i)
            .unwrap_or(self.args_input.len())
    }

    pub fn args_insert(&mut self, c: char) {
        let at = self.byte_at(self.args_cursor);
        self.args_input.insert(at, c);
        self.args_cursor += 1;
    }

    pub fn args_backspace(&mut self) {
        if self.args_cursor == 0 {
            return;
        }
        let at = self.byte_at(self.args_cursor - 1);
        self.args_input.remove(at);
        self.args_cursor -= 1;
    }

    pub fn args_delete(&mut self) {
        if self.args_cursor >= self.args_input.chars().count() {
            return;
        }
        let at = self.byte_at(self.args_cursor);
        self.args_input.remove(at);
    }

    pub fn args_move(&mut self, delta: isize) {
        let len = self.args_input.chars().count() as isize;
        self.args_cursor = (self.args_cursor as isize + delta).clamp(0, len) as usize;
    }

    pub fn args_home(&mut self) {
        self.args_cursor = 0;
    }

    pub fn args_end(&mut self) {
        self.args_cursor = self.args_input.chars().count();
    }

    pub fn confirm_args(&mut self) {
        let Some(name) = self.args_target.clone() else {
            return;
        };
        let args = crate::task::split_args(&self.args_input);
        self.cancel_args();
        self.request_run(&name, &args);
    }

    /// The usage hint for whatever the args prompt is aimed at.
    pub fn args_hint(&self) -> Option<String> {
        let target = self.args_target.as_ref()?;
        self.tasks.iter().find(|t| &t.name == target)?.args_hint()
    }

    pub fn begin_jump(&mut self) {
        self.jumping = true;
        self.jump_query.clear();
        self.jump_matches.clear();
        self.jump_idx = 0;
        self.jump_origin = self.cursor;
        self.status = None;
    }

    pub fn push_jump(&mut self, c: char) {
        self.jump_query.push(c);
        self.apply_jump();
    }

    pub fn pop_jump(&mut self) {
        self.jump_query.pop();
        self.apply_jump();
    }

    /// Keep the cursor where the jump left it.
    pub fn accept_jump(&mut self) {
        self.jumping = false;
        self.jump_query.clear();
    }

    /// Put the cursor back where it started.
    pub fn cancel_jump(&mut self) {
        self.jumping = false;
        self.jump_query.clear();
        self.jump_matches.clear();
        self.cursor = self.jump_origin.min(self.rows.len().saturating_sub(1));
    }

    pub fn jump_step(&mut self, delta: isize) {
        if self.jump_matches.is_empty() {
            return;
        }
        let n = self.jump_matches.len() as isize;
        self.jump_idx =
            (((self.jump_idx as isize + delta) % n) + n) as usize % self.jump_matches.len();
        self.goto_match();
    }

    fn apply_jump(&mut self) {
        if self.jump_query.is_empty() {
            self.jump_matches.clear();
            self.cursor = self.jump_origin.min(self.rows.len().saturating_sub(1));
            return;
        }
        self.jump_matches = self.matching_tasks(&self.jump_query.clone());
        self.jump_idx = 0;
        self.goto_match();
    }

    /// Move to the current match, opening whatever folds hide it. The tree itself is left
    /// alone — that is the whole difference from the filter.
    fn goto_match(&mut self) {
        let Some(&ti) = self.jump_matches.get(self.jump_idx) else {
            return;
        };
        self.rebuild(Some(ti));
    }

    /// Show what the task under the cursor actually is: its description, what it
    /// requires, and the commands it will run — before running it.
    pub fn open_detail(&mut self) {
        let Some(ti) = self.selected_task() else {
            self.status = Some("nothing here to describe — space folds it".into());
            return;
        };
        let name = self.tasks[ti].name.clone();
        // One `--summary`, the same call the graph and the args prompt already make.
        self.detail = crate::graph::detail(&self.root, &name);
        self.detail_of = Some(name);
        self.detail_offset = 0;
        self.screen = Screen::Detail;
        self.status = None;
    }

    pub fn close_detail(&mut self) {
        self.detail_of = None;
        self.screen = Screen::Picker;
    }

    pub fn detail_scroll(&mut self, delta: i16) {
        self.detail_offset = (self.detail_offset as i16 + delta).max(0) as u16;
    }

    /// Watch the project and re-run this task whenever something changes.
    pub fn toggle_watch(&mut self) {
        if self.watching.take().is_some() {
            self.watcher = None;
            self.status = Some("watch off".into());
            return;
        }
        let Some(name) = self.run.as_ref().map(|r| r.root.clone()) else {
            self.status = Some("nothing to watch — run something first".into());
            return;
        };
        match crate::watch::Watch::start(&self.root) {
            Ok(w) => {
                self.watcher = Some(w);
                self.watching = Some(name.clone());
                self.status = Some(format!(
                    "watching — `task {name}` re-runs when files change"
                ));
            }
            Err(e) => self.status = Some(format!("could not watch this directory: {e}")),
        }
    }

    /// Re-run if the watcher has settled on a change.
    pub fn poll_watch(&mut self) -> bool {
        let Some(name) = self.watching.clone() else {
            return false;
        };
        let Some(changed) = self.watcher.as_mut().and_then(|w| w.poll()) else {
            return false;
        };
        // Never stack runs on top of each other: a save during a build would otherwise
        // kill the build that is already checking the previous save.
        if self.run_in_flight() {
            return false;
        }

        let args = self
            .run
            .as_ref()
            .map(|r| r.args.clone())
            .unwrap_or_default();
        if let Some(r) = self.run.as_ref() {
            self.interactive_next = r.interactive;
            self.force_next = r.force;
        }
        let file = changed
            .file_name()
            .map(|f| f.to_string_lossy().to_string())
            .unwrap_or_default();

        // Deliberately bypasses the confirmation: watch mode is opt-in, on a task you
        // just ran, and a `y` prompt firing on every keystroke would be unusable. Which
        // is also why arming it on a production task is a bad idea.
        if let Err(e) = self.start_run_with(&name, &args) {
            self.status = Some(format!("could not re-run `task {name}`: {e}"));
            return false;
        }
        self.watching = Some(name);
        self.status = Some(format!("{file} changed — re-running"));
        true
    }

    /// Copy text to the system clipboard.
    ///
    /// Shelling out rather than taking a dependency: one of these three exists on any
    /// machine that has a clipboard at all, and a crate for it would pull in a windowing
    /// stack for the sake of `pbcopy`.
    pub fn copy(&mut self, text: &str, what: &str) {
        const TOOLS: [(&str, &[&str]); 4] = [
            ("pbcopy", &[]),
            ("wl-copy", &[]),
            ("xclip", &["-selection", "clipboard"]),
            ("xsel", &["--clipboard", "--input"]),
        ];
        for (tool, args) in TOOLS {
            let child = std::process::Command::new(tool)
                .args(args)
                .stdin(std::process::Stdio::piped())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .spawn();
            let Ok(mut child) = child else { continue };
            let wrote = child
                .stdin
                .take()
                .map(|mut pipe| std::io::Write::write_all(&mut pipe, text.as_bytes()).is_ok())
                .unwrap_or(false);
            let _ = child.wait();
            if wrote {
                let lines = text.lines().count();
                self.status = Some(match lines {
                    0 | 1 => format!("copied {what}"),
                    n => format!("copied {what} — {n} lines"),
                });
                return;
            }
        }
        self.status = Some("no clipboard tool found (pbcopy, wl-copy, xclip, xsel)".into());
    }

    /// The line under the cursor in the run view.
    pub fn yank_line(&mut self) {
        let Some(RunRow::Line { task, index, .. }) = self.run_rows.get(self.run_cursor).cloned()
        else {
            self.yank_task_output();
            return;
        };
        let text = self
            .run
            .as_ref()
            .and_then(|r| r.tasks.get(&task))
            .and_then(|t| t.lines.get(index))
            .map(|l| l.plain.clone())
            .unwrap_or_default();
        self.copy(&text, "line");
    }

    /// Everything the task under the cursor printed.
    pub fn yank_task_output(&mut self) {
        let Some(name) = self.run_selected_task() else {
            return;
        };
        let text = self
            .run
            .as_ref()
            .and_then(|r| r.tasks.get(&name))
            .map(|t| {
                t.lines
                    .iter()
                    .map(|l| l.plain.as_str())
                    .collect::<Vec<_>>()
                    .join("\n")
            })
            .unwrap_or_default();
        self.copy(&text, &format!("{name} output"));
    }

    /// Half a screen, as `^d` and `^u` do.
    pub fn half_page(&self) -> isize {
        (self.viewport / 2).max(1) as isize
    }

    /// `gg` — first row of whatever is on screen.
    pub fn goto_top(&mut self) {
        match self.screen {
            Screen::Picker => {
                self.cursor = 0;
                self.offset = 0;
            }
            Screen::Run => {
                self.run_cursor = 0;
                self.run_offset = 0;
                self.following = false;
            }
            Screen::History => {
                self.history_cursor = 0;
                self.history_offset = 0;
            }
            Screen::Help => self.help_offset = 0,
            Screen::Detail => self.detail_offset = 0,
        }
    }

    /// `G` — last row.
    pub fn goto_bottom(&mut self) {
        match self.screen {
            Screen::Picker => self.cursor = self.rows.len().saturating_sub(1),
            Screen::Run => {
                self.run_cursor = self.run_rows.len().saturating_sub(1);
                // Jumping to the end of a live run is the same intent as following it.
                self.following = self.run.as_ref().is_some_and(|r| !r.finished());
            }
            Screen::History => self.history_cursor = self.history.len().saturating_sub(1),
            // Clamped during rendering, so overshooting is harmless.
            Screen::Help => self.help_offset = u16::MAX,
            Screen::Detail => self.detail_offset = u16::MAX,
        }
    }

    /// `?` from anywhere; `esc` comes back to where you were.
    pub fn toggle_help(&mut self) {
        match self.help_return.take() {
            Some(previous) => self.screen = previous,
            None => {
                self.help_return = Some(self.screen);
                self.screen = Screen::Help;
                self.help_offset = 0;
                self.status = None;
            }
        }
    }

    pub fn help_scroll(&mut self, delta: i16) {
        self.help_offset = (self.help_offset as i16 + delta).max(0) as u16;
    }

    /// Open the most recent stored run for this project, as `--last` does.
    pub fn open_last_run(&mut self) -> bool {
        let base = store::state_dir();
        let here = self.root.display().to_string();
        let Some(manifest) = store::list(&base).into_iter().find(|m| m.dir == here) else {
            return false;
        };
        self.history = vec![manifest];
        self.history_cursor = 0;
        self.open_stored_run();
        true
    }

    /// Load the archive and switch to it.
    pub fn open_history(&mut self) {
        self.reload_history();
        self.history_cursor = 0;
        self.history_offset = 0;
        self.screen = Screen::History;
        self.status = if self.history.is_empty() {
            Some("no stored runs yet — run something and it will land here".into())
        } else {
            None
        };
    }

    fn reload_history(&mut self) {
        let all = store::list(&store::state_dir());
        let here = self.root.display().to_string();
        self.history = if self.history_all_projects {
            all
        } else {
            all.into_iter().filter(|m| m.dir == here).collect()
        };
    }

    pub fn begin_history_search(&mut self) {
        self.history_searching = true;
        self.history_query.clear();
        self.history_hits.clear();
        self.status = None;
        self.reload_history();
    }

    pub fn clear_history_search(&mut self) {
        self.history_searching = false;
        self.history_query.clear();
        self.history_hits.clear();
        self.reload_history();
        self.history_cursor = 0;
    }

    pub fn push_history_search(&mut self, c: char) {
        self.history_query.push(c);
        self.apply_history_search();
    }

    pub fn pop_history_search(&mut self) {
        self.history_query.pop();
        self.apply_history_search();
    }

    /// Grep the archive and keep only the runs that matched.
    ///
    /// This is the "when did this start failing" question — the one thing you could not
    /// ask before runs were stored, and the reason the archive exists at all.
    fn apply_history_search(&mut self) {
        self.history_hits.clear();
        self.reload_history();
        if self.history_query.is_empty() {
            self.history_cursor = 0;
            return;
        }
        let Ok(query) = search::Query::new(&self.history_query) else {
            // Half-typed regex: leave the list alone rather than emptying it.
            return;
        };
        let (results, _) = search::search_store(&store::state_dir(), &query, 200);
        for r in results {
            self.history_hits.insert(r.manifest.id, r.hits.len());
        }
        self.history
            .retain(|m| self.history_hits.contains_key(&m.id));
        self.history_cursor = 0;
    }

    pub fn toggle_history_scope(&mut self) {
        self.history_all_projects = !self.history_all_projects;
        let keep = self.history.get(self.history_cursor).map(|m| m.id.clone());
        self.reload_history();
        // Stay on the same run across the widening, as the pivot does in the picker.
        self.history_cursor = keep
            .and_then(|id| self.history.iter().position(|m| m.id == id))
            .unwrap_or(0);
        self.status = None;
    }

    pub fn set_filter_context(&mut self, delta: isize) {
        let next = (self.filter_context as isize + delta).clamp(0, 20) as usize;
        if next == self.filter_context {
            return;
        }
        self.filter_context = next;
        if self.filter_matches {
            self.rebuild_run_rows();
            self.jump_to_hit();
        }
        self.status = Some(format!("{} lines of context", self.filter_context));
    }

    pub fn history_move_cursor(&mut self, delta: isize) {
        if self.history.is_empty() {
            return;
        }
        let last = self.history.len() as isize - 1;
        self.history_cursor = (self.history_cursor as isize + delta).clamp(0, last) as usize;
    }

    /// Reopen the run under the cursor. It lands in the same run view a live run uses —
    /// same folding, same search — because it is the same structure, just read off disk.
    pub fn open_stored_run(&mut self) {
        let Some(manifest) = self.history.get(self.history_cursor).cloned() else {
            return;
        };
        match store::load(&store::state_dir(), &manifest) {
            Ok(run) => {
                self.run = Some(run);
                self.screen = Screen::Run;
                self.run_cursor = 0;
                self.run_offset = 0;
                self.run_expanded.clear();
                self.following = false;
                self.focused_failure = None;
                self.saved_to = Some(store::run_dir(&store::state_dir(), &manifest.id));
                self.clear_search();
                self.status = None;
                // Open the failure straight away: reopening a run is nearly always about
                // the thing that broke.
                if let Some(name) = self.first_failure() {
                    self.expand_to(&name);
                    self.rebuild_run_rows();
                    self.cursor_to_task(&name);
                } else {
                    self.rebuild_run_rows();
                }
            }
            Err(e) => self.status = Some(format!("could not read that run: {e}")),
        }

        // Arriving from a cross-run search: land with the same query applied, so the run
        // opens on the thing you were looking for rather than making you retype it.
        if !self.history_query.is_empty() {
            self.search_input = self.history_query.clone();
            self.filter_matches = true;
            self.apply_search();
        }
    }

    /// The deepest failed task — the one that actually broke, rather than an aggregate
    /// that merely contains it.
    fn first_failure(&self) -> Option<String> {
        let run = self.run.as_ref()?;
        run.order
            .iter()
            .find(|n| {
                run.tasks
                    .get(*n)
                    .map(|t| t.status == Status::Failed)
                    .unwrap_or(false)
                    && run.graph.children(n).is_empty()
            })
            .cloned()
    }

    /// Drain the capture thread and refresh the run view. Returns true if anything moved.
    pub fn poll_run(&mut self) -> bool {
        let Some(run) = &mut self.run else {
            return false;
        };
        if !run.poll() {
            return false;
        }
        self.follow();
        self.refresh_search();
        self.rebuild_run_rows();
        self.save_if_finished();
        true
    }

    /// Persist the run once, the moment it ends.
    fn save_if_finished(&mut self) {
        if self.saved_to.is_some() {
            return;
        }
        let Some(run) = &self.run else { return };
        if !run.finished() {
            return;
        }
        match store::save(&store::state_dir(), &self.root, run) {
            Ok(path) => {
                let masked = run.redacted_secrets;
                self.saved_to = Some(path);
                // The picker's ✓/✗ column is only useful if it is current.
                let outcomes = store::last_outcomes(&store::state_dir(), &self.root);
                self.outcomes = outcomes;
                self.status = Some(match masked {
                    0 => "saved — no dotenv values found to mask".to_string(),
                    1 => "saved — 1 secret masked".to_string(),
                    n => format!("saved — {n} secrets masked"),
                });
            }
            Err(e) => self.status = Some(format!("could not save this run: {e}")),
        }
    }

    /// Re-run the current query against the buffers, which have grown since last time.
    fn refresh_search(&mut self) {
        let (Some(q), Some(run)) = (&self.search, &self.run) else {
            return;
        };
        self.search_hits = search::search_run(run, q);
        if self.search_idx >= self.search_hits.len() {
            self.search_idx = self.search_hits.len().saturating_sub(1);
        }
    }

    /// Compile what has been typed and jump to the first hit.
    pub fn apply_search(&mut self) {
        if self.search_input.is_empty() {
            // Keep the prompt and its mode; only the compiled query goes away, so
            // backspacing to empty shows the whole run again rather than dropping you out.
            let was_filtering = self.filter_matches;
            let still_typing = self.searching;
            self.clear_search();
            self.searching = still_typing;
            self.filter_matches = was_filtering && still_typing;
            self.rebuild_run_rows();
            return;
        }
        match search::Query::new(&self.search_input) {
            Ok(q) => {
                self.search_error = None;
                self.search = Some(q);
                self.search_idx = 0;
                self.following = false;
                self.refresh_search();
                self.rebuild_run_rows();
                self.jump_to_hit();
            }
            // A half-typed regex is the normal state during incremental search, so this
            // is reported quietly rather than treated as a failure.
            Err(e) => {
                self.search_error = Some(
                    e.to_string()
                        .lines()
                        .next()
                        .unwrap_or("bad pattern")
                        .to_string(),
                );
                self.search = None;
                self.search_hits.clear();
            }
        }
    }

    pub fn clear_search(&mut self) {
        self.search = None;
        self.searching = false;
        self.search_input.clear();
        self.search_hits.clear();
        self.search_idx = 0;
        self.search_error = None;
        self.filter_matches = false;
        self.rebuild_run_rows();
    }

    pub fn search_step(&mut self, delta: isize) {
        if self.search_hits.is_empty() {
            return;
        }
        let n = self.search_hits.len() as isize;
        self.search_idx =
            (((self.search_idx as isize + delta) % n) + n) as usize % self.search_hits.len();
        self.jump_to_hit();
    }

    /// Put the cursor on the current hit, opening whatever fold hides it.
    fn jump_to_hit(&mut self) {
        let Some(hit) = self.search_hits.get(self.search_idx).cloned() else {
            return;
        };
        self.expand_to(&hit.task);
        self.rebuild_run_rows();
        if let Some(pos) = self.run_rows.iter().position(|r| {
            matches!(r, RunRow::Line { task, index, .. } if *task == hit.task && *index == hit.index)
        }) {
            self.run_cursor = pos;
        }
    }

    /// `f` with a query already running toggles the filtered view; `f` with nothing
    /// running opens the prompt *already* filtering, so typing narrows the run live
    /// instead of making you search first and convert afterwards.
    pub fn toggle_filter_matches(&mut self) {
        if self.search.is_none() {
            self.begin_filter();
            return;
        }
        self.filter_matches = !self.filter_matches;
        self.following = false;
        self.rebuild_run_rows();
        self.jump_to_hit();
    }

    /// Open the prompt in filter mode.
    pub fn begin_filter(&mut self) {
        self.searching = true;
        self.filter_matches = true;
        self.search_input.clear();
        self.search_error = None;
        self.status = None;
    }

    pub fn push_search(&mut self, c: char) {
        self.search_input.push(c);
        self.apply_search();
    }

    pub fn pop_search(&mut self) {
        self.search_input.pop();
        self.apply_search();
    }

    /// Keep the view pointed at the interesting thing: whatever is running, or — once
    /// something breaks — the task that broke.
    fn follow(&mut self) {
        let Some(run) = &self.run else { return };

        // A failure wins over following, and pins the view there.
        let failure = run
            .order
            .iter()
            .find(|n| {
                run.tasks
                    .get(*n)
                    .map(|t| t.status == Status::Failed)
                    .unwrap_or(false)
                    && run.graph.children(n).is_empty()
            })
            .cloned();
        let _ = &failure;

        if let Some(name) = failure {
            if self.focused_failure.as_deref() != Some(&name) {
                self.focused_failure = Some(name.clone());
                self.following = false;
                self.expand_to(&name);
                self.rebuild_run_rows();
                self.cursor_to_task(&name);
            }
            return;
        }

        if self.following {
            if let Some(active) = run
                .order
                .iter()
                .rev()
                .find(|n| {
                    run.tasks
                        .get(*n)
                        .map(|t| t.status == Status::Running)
                        .unwrap_or(false)
                })
                .cloned()
            {
                self.expand_to(&active);
                self.rebuild_run_rows();
                self.cursor_to_task(&active);
            }
        }
    }

    /// Open a task and every task that invoked it.
    fn expand_to(&mut self, name: &str) {
        let Some(run) = &self.run else { return };
        let mut chain = vec![name.to_string()];
        // Walk up through the graph so the target is not buried behind a closed parent.
        let mut frontier = vec![name.to_string()];
        while let Some(current) = frontier.pop() {
            for (parent, children) in &run.graph.edges {
                if children.contains(&current) && !chain.contains(parent) {
                    chain.push(parent.clone());
                    frontier.push(parent.clone());
                }
            }
        }
        for t in chain {
            self.run_expanded.insert(t);
        }
    }

    fn cursor_to_task(&mut self, name: &str) {
        if let Some(pos) = self
            .run_rows
            .iter()
            .position(|r| matches!(r, RunRow::Task { name: n, .. } if n == name))
        {
            self.run_cursor = pos;
        }
    }

    pub fn rebuild_run_rows(&mut self) {
        let Some(run) = &self.run else {
            self.run_rows.clear();
            return;
        };

        // In filter mode the whole run collapses to matching lines under the tasks that
        // produced them: a hit is not useful if you cannot see which task said it.
        let filtering = self.filter_matches && self.search.is_some();
        // Each hit drags its neighbours in with it: `--- FAIL: TestOrderTotal` on its own
        // hides `order_test.go:88: want 1200, got 1180`, which is the useful half.
        let hit_lines: HashSet<(String, usize)> = if filtering {
            let mut set = HashSet::new();
            for h in &self.search_hits {
                let len = run.tasks.get(&h.task).map(|t| t.lines.len()).unwrap_or(0);
                let lo = h.index.saturating_sub(self.filter_context);
                let hi = (h.index + self.filter_context + 1).min(len);
                for i in lo..hi {
                    set.insert((h.task.clone(), i));
                }
            }
            set
        } else {
            HashSet::new()
        };
        let hit_tasks: HashSet<String> = hit_lines.iter().map(|(t, _)| t.clone()).collect();

        let mut rows = Vec::new();
        let mut seen = HashSet::new();
        // (task, depth), pushed in reverse so siblings come out in invocation order.
        let mut stack = vec![(run.root.clone(), 0usize)];

        while let Some((name, depth)) = stack.pop() {
            // A diamond — `app:css` reached from both `check` and `build` — is shown at
            // its first position rather than duplicated.
            if !seen.insert(name.clone()) {
                continue;
            }

            if filtering && !hit_tasks.contains(&name) {
                // Skip the row but keep walking: a parent with no hits of its own may
                // still contain a child that has them.
                for c in run.graph.children(&name).iter().rev() {
                    stack.push((c.clone(), depth));
                }
                continue;
            }

            let open = filtering || self.run_expanded.contains(&name);
            rows.push(RunRow::Task {
                name: name.clone(),
                depth,
                open,
            });

            if open {
                if let Some(t) = run.tasks.get(&name) {
                    for i in 0..t.lines.len() {
                        if filtering && !hit_lines.contains(&(name.clone(), i)) {
                            continue;
                        }
                        rows.push(RunRow::Line {
                            task: name.clone(),
                            index: i,
                            depth: depth + 1,
                        });
                    }
                }
            }
            for c in run.graph.children(&name).iter().rev() {
                stack.push((c.clone(), depth + 1));
            }
        }

        self.run_rows = rows;
        if self.run_cursor >= self.run_rows.len() {
            self.run_cursor = self.run_rows.len().saturating_sub(1);
        }
    }

    pub fn run_move_cursor(&mut self, delta: isize) {
        if self.run_rows.is_empty() {
            return;
        }
        // Any deliberate move means the user is reading, not watching.
        self.following = false;
        let last = self.run_rows.len() as isize - 1;
        self.run_cursor = (self.run_cursor as isize + delta).clamp(0, last) as usize;
    }

    pub fn run_toggle_fold(&mut self) {
        let Some(RunRow::Task { name, .. }) = self.run_rows.get(self.run_cursor).cloned() else {
            return;
        };
        self.following = false;
        if !self.run_expanded.remove(&name) {
            self.run_expanded.insert(name.clone());
        }
        self.rebuild_run_rows();
        self.cursor_to_task(&name);
    }

    pub fn toggle_force(&mut self) {
        self.force_next = !self.force_next;
        self.status = Some(if self.force_next {
            "force: the next run ignores go-task's up-to-date checks".into()
        } else {
            "force off".to_string()
        });
    }

    pub fn toggle_interactive(&mut self) {
        self.interactive_next = !self.interactive_next;
        self.status = Some(if self.interactive_next {
            "interactive: the next run can be typed at, but output is attributed by command".into()
        } else {
            "interactive off".to_string()
        });
    }

    pub fn begin_input(&mut self) {
        if !self.run_in_flight() {
            self.status = Some("nothing is running to type at".into());
            return;
        }
        self.sending_input = true;
        self.following = true;
        self.status = None;
    }

    pub fn end_input(&mut self) {
        self.sending_input = false;
    }

    /// Forward keystrokes to the task's terminal.
    pub fn send_input(&mut self, bytes: &[u8]) {
        let ok = self
            .run
            .as_mut()
            .map(|r| r.send_input(bytes))
            .unwrap_or(false);
        if !ok {
            // Silence after a keystroke is ambiguous enough already; a write that failed
            // must not look the same as one that landed.
            self.status = Some(
                "that keystroke went nowhere — the task has finished or closed its input".into(),
            );
            self.sending_input = false;
        }
    }

    /// A non-interactive run that has gone quiet. Under `--output prefixed` a task
    /// blocked on a prompt produces *nothing*, so silence is the only clue there is.
    pub fn possibly_stuck(&self) -> bool {
        self.run.as_ref().is_some_and(|r| {
            !r.finished()
                && !r.interactive
                && !r.graph.edges.is_empty()
                && r.silent_for() > std::time::Duration::from_secs(15)
        })
    }

    /// Is the task sitting on an unanswered question?
    pub fn awaiting_input(&self) -> bool {
        self.run
            .as_ref()
            .is_some_and(|r| !r.finished() && r.looks_like_a_prompt())
    }

    /// Stop the running task.
    pub fn cancel_run(&mut self) {
        let Some(run) = &mut self.run else { return };
        if run.finished() {
            self.status = Some("that run has already finished".into());
            return;
        }
        run.cancel();
        self.status = Some("cancelled".into());
    }

    /// True while a child process is still out there. Quitting without dealing with it
    /// would leave the build running with nothing watching it.
    pub fn run_in_flight(&self) -> bool {
        self.run.as_ref().is_some_and(|r| !r.finished())
    }

    /// Re-run the task under the cursor, keeping the args the run was started with.
    pub fn rerun_selected(&mut self) {
        let Some(name) = self.run_selected_task() else {
            return;
        };
        let args = self
            .run
            .as_ref()
            // Only the root was invoked with these args; a child was not.
            .filter(|r| r.root == name)
            .map(|r| r.args.clone())
            .unwrap_or_default();
        // Re-run it the way it was run: non-interactively it would hang again, and
        // without `--force` a cached task would simply decline.
        if let Some(r) = self.run.as_ref() {
            self.interactive_next = r.interactive;
            self.force_next = r.force;
        }
        self.request_run(&name, &args);
    }

    /// The task under the cursor, whether the cursor is on it or on one of its lines.
    pub fn run_selected_task(&self) -> Option<String> {
        match self.run_rows.get(self.run_cursor)? {
            RunRow::Task { name, .. } => Some(name.clone()),
            RunRow::Line { task, .. } => Some(task.clone()),
        }
    }

    fn fold_set(&mut self) -> &mut HashSet<String> {
        self.expanded.entry(self.mode.label()).or_default()
    }

    /// Which tasks pass the current filter.
    fn visible(&mut self) -> Vec<usize> {
        if self.query.is_empty() {
            return (0..self.tasks.len()).collect();
        }
        self.matching_tasks(&self.query.clone())
    }

    /// Tasks matching `query`, fuzzily, over the full colon path and the aliases — so
    /// `blint` finds `backend:lint` and `t` finds the `test` alias.
    ///
    /// Shared by the filter and the jump so the two can never disagree about what counts
    /// as a match.
    fn matching_tasks(&mut self, query: &str) -> Vec<usize> {
        let pattern = Pattern::parse(query, CaseMatching::Smart, Normalization::Smart);
        let mut buf = Vec::new();
        let mut scored: Vec<(u32, usize)> = Vec::new();
        for (i, t) in self.tasks.iter().enumerate() {
            let mut best = pattern.score(Utf32Str::new(&t.name, &mut buf), &mut self.matcher);
            for a in &t.aliases {
                let s = pattern.score(Utf32Str::new(a, &mut buf), &mut self.matcher);
                best = match (best, s) {
                    (Some(b), Some(s)) => Some(b.max(s)),
                    (b, s) => b.or(s),
                };
            }
            if let Some(score) = best {
                scored.push((score, i));
            }
        }
        // Keep tree order rather than score order — the tree *is* the organisation, and
        // resorting it by score would destroy the grouping the user is looking at.
        scored.sort_by_key(|(_, i)| *i);
        scored.into_iter().map(|(_, i)| i).collect()
    }

    /// Rebuild the tree and the flattened rows.
    ///
    /// `keep` is a task index to stay parked on across the rebuild — the property that
    /// makes toggling a pivot feel like a pivot rather than a navigation reset. Its
    /// ancestors get opened so it is actually on screen afterwards.
    pub fn rebuild(&mut self, keep: Option<usize>) {
        let visible = self.visible();
        self.tree = pivot::build(self.mode, &self.tasks, &visible);

        if let Some(ti) = keep {
            if let Some(ancestors) = self.tree.ancestors_of_task(ti) {
                let set = self.expanded.entry(self.mode.label()).or_default();
                for a in ancestors {
                    set.insert(a);
                }
            }
        }

        let filtering = !self.query.is_empty();
        let set = self.expanded.entry(self.mode.label()).or_default().clone();
        // While filtering, every group is open: a hit hidden behind a fold is a hit you
        // did not find.
        self.rows = self
            .tree
            .flatten(&|key: &str| filtering || set.contains(key));

        match keep.and_then(|ti| {
            self.rows
                .iter()
                .position(|r| self.tree.nodes[r.node].task == Some(ti))
        }) {
            Some(pos) => self.cursor = pos,
            None => self.cursor = self.cursor.min(self.rows.len().saturating_sub(1)),
        }
    }

    pub fn selected_task(&self) -> Option<usize> {
        self.rows
            .get(self.cursor)
            .and_then(|r| self.tree.nodes[r.node].task)
    }

    pub fn selected_node(&self) -> Option<&pivot::Node> {
        self.rows.get(self.cursor).map(|r| &self.tree.nodes[r.node])
    }

    pub fn toggle_mode(&mut self) {
        let keep = self.selected_task();
        self.mode = self.mode.toggled();
        self.rebuild(keep);
    }

    pub fn toggle_fold(&mut self) {
        let Some(row) = self.rows.get(self.cursor) else {
            return;
        };
        let node = &self.tree.nodes[row.node];
        if !node.is_group() {
            return;
        }
        let key = node.key.clone();
        let set = self.fold_set();
        if !set.remove(&key) {
            set.insert(key.clone());
        }
        self.rebuild(None);
        // Stay parked on the group itself, so collapsing does not leave the cursor adrift
        // wherever the row that used to be at this index ended up.
        if let Some(pos) = self
            .rows
            .iter()
            .position(|r| self.tree.nodes[r.node].key == key)
        {
            self.cursor = pos;
        }
    }

    pub fn set_fold_all(&mut self, open: bool) {
        let keep = self.selected_task();
        let keys: Vec<String> = self
            .tree
            .nodes
            .iter()
            .filter(|n| n.is_group())
            .map(|n| n.key.clone())
            .collect();
        let set = self.fold_set();
        set.clear();
        if open {
            set.extend(keys);
        }
        self.rebuild(keep);
    }

    pub fn move_cursor(&mut self, delta: isize) {
        if self.rows.is_empty() {
            return;
        }
        let last = self.rows.len() - 1;
        let next = self.cursor as isize + delta;
        self.cursor = next.clamp(0, last as isize) as usize;
    }

    pub fn push_query(&mut self, c: char) {
        let keep = self.selected_task();
        self.query.push(c);
        self.rebuild(keep);
    }

    pub fn pop_query(&mut self) {
        let keep = self.selected_task();
        self.query.pop();
        self.rebuild(keep);
    }

    pub fn clear_query(&mut self) {
        let keep = self.selected_task();
        self.query.clear();
        self.filtering = false;
        self.rebuild(keep);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pivot::fixture;

    fn app_with(names: &[&str]) -> App {
        App::new(fixture(names), std::path::PathBuf::from("/tmp/repo"))
    }

    fn sample() -> App {
        app_with(&[
            "all",
            "build",
            "fmt",
            "lint",
            "app:build",
            "app:fmt",
            "app:lint",
            "backend:build",
            "backend:fmt",
            "backend:lint",
            "backend:migrate",
            "backend:migrate:down",
            "infra:lint",
            "site:build",
        ])
    }

    fn park_on(app: &mut App, name: &str) -> usize {
        app.set_fold_all(true);
        let ti = app
            .tasks
            .iter()
            .position(|t| t.name == name)
            .expect("task exists");
        app.cursor = app
            .rows
            .iter()
            .position(|r| app.tree.nodes[r.node].task == Some(ti))
            .expect("task has a row");
        ti
    }

    fn visible_task_names(app: &App) -> Vec<&str> {
        app.rows
            .iter()
            .filter_map(|r| app.tree.nodes[r.node].task)
            .map(|ti| app.tasks[ti].name.as_str())
            .collect()
    }

    /// The property that makes `g` read as a pivot rather than a navigation reset: the
    /// same task stays under the cursor, at its new address, with folds opened to reveal
    /// it.
    #[test]
    fn selection_survives_the_pivot() {
        let mut app = sample();
        let ti = park_on(&mut app, "backend:lint");
        assert_eq!(app.mode, Mode::Domain);

        app.toggle_mode();
        assert_eq!(app.mode, Mode::Verb);
        assert_eq!(
            app.selected_task(),
            Some(ti),
            "same task after pivoting to verb"
        );

        app.toggle_mode();
        assert_eq!(app.selected_task(), Some(ti), "and again on the way back");
    }

    /// Fold state is kept per mode, so bouncing between pivots does not collapse what you
    /// opened on the other side.
    #[test]
    fn fold_state_is_remembered_per_mode() {
        let mut app = sample();
        park_on(&mut app, "backend:lint");
        let domain_rows = app.rows.len();

        app.toggle_mode();
        app.set_fold_all(false);
        app.toggle_mode();

        assert_eq!(app.mode, Mode::Domain);
        assert_eq!(app.rows.len(), domain_rows, "domain folds were left alone");
    }

    /// A hit hidden behind a fold is a hit you did not find, so filtering opens
    /// everything — without disturbing the fold state you get back when you clear it.
    #[test]
    fn filtering_reveals_matches_and_restores_folds_after() {
        let mut app = sample();
        let collapsed = app.rows.len();

        for c in "lint".chars() {
            app.push_query(c);
        }
        let names = visible_task_names(&app);
        assert!(
            names.contains(&"backend:lint"),
            "matches are on screen, not behind a fold"
        );
        assert!(
            names.iter().all(|n| n.contains("lint")),
            "only matches: {names:?}"
        );

        app.clear_query();
        assert_eq!(
            app.rows.len(),
            collapsed,
            "folds are as they were before the filter"
        );
    }

    /// The whole difference from the filter: the list stays as it was, and only the
    /// cursor moves. Folds hiding the match are opened.
    #[test]
    fn jumping_moves_the_cursor_without_narrowing_the_list() {
        let mut app = sample();
        let rows_before = app.rows.len();

        app.begin_jump();
        for c in "backend:lint".chars() {
            app.push_jump(c);
        }

        assert_eq!(
            app.selected_task()
                .map(|ti| app.tasks[ti].name.clone())
                .as_deref(),
            Some("backend:lint")
        );
        assert!(
            app.rows.len() >= rows_before,
            "the list was not filtered down: {} -> {}",
            rows_before,
            app.rows.len()
        );
        assert!(app.query.is_empty(), "and the filter was never touched");
    }

    /// `esc` really cancels: the cursor goes back where it started.
    #[test]
    fn cancelling_a_jump_restores_the_cursor() {
        let mut app = sample();
        let origin = park_on(&mut app, "app:fmt");

        app.begin_jump();
        for c in "backend:lint".chars() {
            app.push_jump(c);
        }
        assert_ne!(app.selected_task(), Some(origin));

        app.cancel_jump();
        assert_eq!(app.selected_task(), Some(origin), "back where we started");
    }

    /// Stepping wraps, as the output search does.
    #[test]
    fn jump_steps_through_every_match() {
        let mut app = sample();
        app.begin_jump();
        for c in "lint".chars() {
            app.push_jump(c);
        }
        let n = app.jump_matches.len();
        assert!(n > 1, "several tasks match lint");

        let first = app.selected_task();
        app.jump_step(1);
        assert_ne!(app.selected_task(), first);
        for _ in 1..n {
            app.jump_step(1);
        }
        assert_eq!(app.selected_task(), first, "wrapped back round");
    }

    /// Accepting keeps the cursor where the jump left it.
    #[test]
    fn accepting_a_jump_keeps_the_position() {
        let mut app = sample();
        app.begin_jump();
        for c in "infra:lint".chars() {
            app.push_jump(c);
        }
        let landed = app.selected_task();
        app.accept_jump();
        assert!(!app.jumping);
        assert_eq!(app.selected_task(), landed);
    }

    /// Fuzzy, over the full colon path — `blint` should find `backend:lint`.
    #[test]
    fn filter_matches_fuzzily_across_the_path() {
        let mut app = sample();
        for c in "blint".chars() {
            app.push_query(c);
        }
        assert!(visible_task_names(&app).contains(&"backend:lint"));
    }

    /// A group row that is also a task must stay runnable — selecting `backend:migrate`
    /// should offer the task, not just the fold.
    #[test]
    fn group_that_is_also_a_task_is_still_selectable() {
        let mut app = sample();
        let ti = park_on(&mut app, "backend:migrate");
        let node = app.selected_node().expect("a node under the cursor");
        assert!(node.is_group(), "parents backend:migrate:down");
        assert_eq!(app.selected_task(), Some(ti), "and is runnable itself");
    }
}

#[cfg(test)]
mod run_view_tests {
    use super::*;
    use crate::pivot::fixture;
    use crate::run::Run;

    fn app_with_run(root: &str, edges: &[(&str, &[&str])]) -> App {
        let mut app = App::new(fixture(&[root]), std::env::temp_dir());
        app.run = Some(Run::detached(root, Run::graph_from(edges)));
        app.screen = Screen::Run;
        app.rebuild_run_rows();
        app
    }

    fn task_rows(app: &App) -> Vec<String> {
        app.run_rows
            .iter()
            .filter_map(|r| match r {
                RunRow::Task { name, depth, .. } => Some(format!("{}{name}", "  ".repeat(*depth))),
                RunRow::Line { .. } => None,
            })
            .collect()
    }

    #[test]
    fn the_run_tree_follows_invocation_order() {
        let app = app_with_run(
            "all",
            &[("all", &["lint", "test"]), ("lint", &["app:lint"])],
        );
        assert_eq!(task_rows(&app), ["all", "  lint", "    app:lint", "  test"]);
    }

    /// A task reached by two paths — atlas's `app:css`, invoked from both `check` and
    /// `build` — is one node in the graph and must be one row, not two.
    #[test]
    fn a_diamond_is_shown_once() {
        let app = app_with_run(
            "all",
            &[
                ("all", &["check", "build"]),
                ("check", &["app:css"]),
                ("build", &["app:css"]),
            ],
        );
        let rows = task_rows(&app);
        assert_eq!(rows.iter().filter(|r| r.trim() == "app:css").count(), 1);
        assert_eq!(rows, ["all", "  check", "    app:css", "  build"]);
    }

    /// Output lines are rows of the same list, so one fold tree holds both.
    #[test]
    fn unfolding_a_task_inlines_its_output() {
        let mut app = app_with_run("a", &[("a", &[])]);
        if let Some(run) = &mut app.run {
            run.feed("a", "first");
            run.feed("a", "second");
        }
        app.rebuild_run_rows();
        assert_eq!(app.run_rows.len(), 1, "collapsed: just the task");

        app.run_cursor = 0;
        app.run_toggle_fold();
        assert_eq!(app.run_rows.len(), 3, "task plus its two lines");
        assert!(matches!(&app.run_rows[1], RunRow::Line { task, index: 0, .. } if task == "a"));
    }

    /// Moving the cursor means you have gone looking for something; the view must stop
    /// yanking you back to whatever is running.
    #[test]
    fn manual_movement_stops_following() {
        let mut app = app_with_run("all", &[("all", &["lint"])]);
        assert!(app.following);
        app.run_move_cursor(1);
        assert!(!app.following);
    }

    /// The cursor can sit on an output line; `r` should still know which task to re-run.
    #[test]
    fn a_line_row_reports_its_owning_task() {
        let mut app = app_with_run("a", &[("a", &[])]);
        if let Some(run) = &mut app.run {
            run.feed("a", "hello");
        }
        app.rebuild_run_rows();
        app.run_cursor = 0;
        app.run_toggle_fold();
        app.run_cursor = 1; // the output line
        assert!(matches!(app.run_rows[1], RunRow::Line { .. }));
        assert_eq!(app.run_selected_task().as_deref(), Some("a"));
    }
    fn searchable_app() -> App {
        let mut app = app_with_run("ci", &[("ci", &["build", "test"])]);
        if let Some(run) = &mut app.run {
            run.feed("build", "compiling core");
            run.feed("build", "compiling api");
            run.feed("test", "running 42 tests");
            run.feed("test", "--- FAIL: TestOrderTotal");
            run.feed("test", "3 migrations pending");
        }
        app.rebuild_run_rows();
        app
    }

    fn rendered(app: &App) -> Vec<String> {
        app.run_rows
            .iter()
            .map(|r| match r {
                RunRow::Task { name, .. } => format!("task {name}"),
                RunRow::Line { task, index, .. } => {
                    let run = app.run.as_ref().unwrap();
                    format!("line {}", run.tasks[task].lines[*index].plain)
                }
            })
            .collect()
    }

    /// Filter mode collapses the run to matching lines, and drops the tasks that have
    /// none — including the root, which would otherwise be a permanent empty header.
    #[test]
    fn filter_mode_shows_only_matching_lines_under_their_tasks() {
        let mut app = searchable_app();
        app.filter_context = 0;
        app.search_input = "pending".into();
        app.apply_search();
        app.toggle_filter_matches();

        assert_eq!(rendered(&app), ["task test", "line 3 migrations pending"]);
    }

    /// A `FAIL:` line on its own hides the assertion underneath it, which is the half
    /// that says what actually broke — so a hit drags its neighbours in with it.
    #[test]
    fn filter_mode_keeps_context_around_each_hit() {
        let mut app = searchable_app();
        app.filter_context = 1;
        app.search_input = "FAIL".into();
        app.apply_search();
        app.toggle_filter_matches();

        assert_eq!(
            rendered(&app),
            [
                "task test",
                "line running 42 tests",
                "line --- FAIL: TestOrderTotal",
                "line 3 migrations pending",
            ]
        );
    }

    /// Context is adjustable, and widening it does not lose the hit.
    #[test]
    fn context_can_be_widened_and_narrowed() {
        let mut app = searchable_app();
        app.search_input = "FAIL".into();
        app.apply_search();
        app.toggle_filter_matches();

        app.set_filter_context(-10);
        assert_eq!(app.filter_context, 0);
        assert_eq!(
            rendered(&app),
            ["task test", "line --- FAIL: TestOrderTotal"]
        );

        app.set_filter_context(1);
        assert_eq!(rendered(&app).len(), 4, "the hit plus one line either side");
    }

    /// A hit behind a closed fold is a hit you did not find.
    #[test]
    fn jumping_to_a_hit_opens_the_fold_hiding_it() {
        let mut app = searchable_app();
        assert!(
            app.run_rows
                .iter()
                .all(|r| matches!(r, RunRow::Task { .. })),
            "all collapsed"
        );

        app.search_input = "FAIL".into();
        app.apply_search();

        assert_eq!(app.search_hits.len(), 1);
        assert!(
            matches!(&app.run_rows[app.run_cursor], RunRow::Line { task, .. } if task == "test")
        );
    }

    /// `n` past the last hit comes back to the first rather than sticking.
    #[test]
    fn stepping_through_hits_wraps() {
        let mut app = searchable_app();
        app.search_input = "compiling".into();
        app.apply_search();
        assert_eq!(app.search_hits.len(), 2);
        assert_eq!(app.search_idx, 0);

        app.search_step(1);
        assert_eq!(app.search_idx, 1);
        app.search_step(1);
        assert_eq!(app.search_idx, 0, "wrapped forward");
        app.search_step(-1);
        assert_eq!(app.search_idx, 1, "and backward");
    }

    /// Half-typed regexes are the normal state during incremental search.
    #[test]
    fn an_incomplete_pattern_reports_rather_than_failing() {
        let mut app = searchable_app();
        app.search_input = "(unclosed".into();
        app.apply_search();
        assert!(app.search_error.is_some());
        assert!(app.search.is_none());

        app.search_input = "(unclosed)".into();
        app.apply_search();
        assert!(app.search_error.is_none());
    }

    /// Searching means you have gone looking; the view must stop chasing the run.
    #[test]
    fn searching_stops_following() {
        let mut app = searchable_app();
        app.following = true;
        app.search_input = "FAIL".into();
        app.apply_search();
        assert!(!app.following);
    }
}
