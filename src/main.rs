//! taskui — a folding, searchable front end for go-task.
//!
//! This is the static half: discover the tasks, pivot them two ways, fold and filter.
//! Running tasks and capturing their output comes next; the tree built here is the same
//! tree those runs will hang off.

mod app;
mod graph;
mod keys;
mod pivot;
mod redact;
mod run;
mod search;
mod store;
mod task;
mod theme;
mod ui;
mod watch;

use anyhow::{bail, Result};
use app::{App, Screen};
use clap::Parser;
use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
use keys::Action;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::time::Duration;

#[derive(Parser)]
#[command(
    name = "taskui",
    version,
    about = "Fold, pivot and search your Taskfile",
    long_about = "A folding, searchable front end for go-task.\n\n\
        Browse the tasks in a Taskfile, run them, and keep their output around to fold \
        and search afterwards — live and across previous runs.",
    after_help = "\
EXAMPLES
  taskui                        browse the Taskfile here
  taskui ~/src/myrepo           …or somewhere else
  taskui --search 'FAIL|error'  grep every stored run, from anywhere
  taskui --run all              run headlessly and print the captured tree
  taskui --dump-config          print every colour at its default

KEYS
  ? inside taskui lists every binding.

FILES
  ~/.config/taskui/config.yaml  colours
  ~/.local/state/taskui/runs/   stored runs (last 50)
  .taskui-danger                tasks that need a confirmation before running
"
)]
struct Args {
    /// Directory to look for a Taskfile in (defaults to the current directory).
    #[arg(default_value = ".")]
    dir: PathBuf,

    /// Print the tasks and exit — useful for checking discovery without a terminal.
    #[arg(long)]
    list: bool,

    /// Print a pivot fully expanded and exit: `--dump domain` or `--dump verb`.
    #[arg(long, value_parser = ["domain", "verb"])]
    dump: Option<String>,

    /// Print the execution graph reachable from a task and exit: `--graph all`.
    #[arg(long, value_name = "TASK")]
    graph: Option<String>,

    /// Run a task headlessly and print the captured tree — proves capture without a TUI.
    #[arg(long, value_name = "TASK")]
    run: Option<String>,

    /// Render one frame to stdout and exit, e.g. `--screenshot 90x30`.
    #[arg(long, value_name = "WxH")]
    screenshot: Option<String>,

    /// Arguments for `--run`, split shell-style: `--args '-- -p ingest'`.
    /// Hyphen-leading values are taken verbatim rather than parsed as flags.
    #[arg(long, default_value = "", allow_hyphen_values = true)]
    args: String,

    /// Open the most recent run for this project instead of the picker.
    #[arg(long)]
    last: bool,

    /// Config file to read colours from (default: ~/.config/taskui/config.yaml).
    #[arg(long, value_name = "PATH")]
    config: Option<PathBuf>,

    /// Print an annotated config.yaml with every colour at its default, and exit.
    #[arg(long)]
    dump_config: bool,

    /// Search stored runs and exit: `--search 'migration.*pending'`.
    #[arg(long, value_name = "PATTERN")]
    search: Option<String>,

    /// Keys to feed before a `--screenshot`. `g` pivots, `\t` folds all, `/` starts a
    /// filter, `j`/`k` move.
    #[arg(long, default_value = "")]
    keys: String,
}

fn main() -> Result<()> {
    let args = Args::parse();
    let root = args.dir.canonicalize().unwrap_or(args.dir.clone());

    if args.dump_config {
        let mut out = io::stdout().lock();
        let _ = writeln!(
            out,
            "# taskui colours. Drop this at {}",
            theme::config_path().display()
        );
        let _ = writeln!(
            out,
            "# Every key is optional; anything absent keeps its default."
        );
        let _ = writeln!(
            out,
            "# Values: an ANSI name (red, bright-blue), a #rrggbb, or a 0-255 palette index."
        );
        let _ = writeln!(out, "#");
        let _ = writeln!(
            out,
            "# Names follow your terminal's own scheme; #rrggbb pins the colour exactly."
        );
        let _ = write!(out, "{}", theme::Theme::default().to_yaml());
        let _ = writeln!(out);
        let _ = writeln!(out, "# Rebind any action to a different single character.");
        let _ = writeln!(
            out,
            "# The same action keeps its meaning on every screen that offers it."
        );
        let _ = writeln!(out, "keys:");
        for (_, key, name) in keys::all_actions() {
            let quoted = if key.is_alphanumeric() {
                key.to_string()
            } else {
                format!("\"{key}\"")
            };
            let _ = writeln!(out, "  {name}: {quoted}");
        }
        return Ok(());
    }

    let config = theme::Config::load(&args.config.clone().unwrap_or_else(theme::config_path));

    // Searching the archive reads stored runs, not the project — it must work from
    // anywhere, including a directory with no Taskfile in it.
    if let Some(pattern) = args.search {
        return search_stored(&pattern);
    }

    let tasks = task::discover(&root)?;

    if args.list {
        let mut out = io::stdout().lock();
        for t in &tasks {
            // Ignore write errors: piping into `head` closes the pipe on us, and that is
            // not a failure of ours.
            if writeln!(out, "{}\t{}", t.name, t.desc).is_err() {
                return Ok(());
            }
        }
        let _ = writeln!(out, "-- {} tasks", tasks.len());
        return Ok(());
    }

    if let Some(mode) = args.dump {
        return dump(mode, &tasks);
    }

    if let Some(root_task) = args.graph {
        let g = graph::resolve(&root, &root_task)?;
        let mut out = io::stdout().lock();
        // Print the tree, marking revisits rather than expanding them twice.
        let mut seen = std::collections::HashSet::new();
        let mut stack = vec![(root_task, 0usize)];
        while let Some((t, depth)) = stack.pop() {
            let repeat = !seen.insert(t.clone());
            let mark = if repeat { "  (already shown)" } else { "" };
            if writeln!(out, "{}{t}{mark}", "  ".repeat(depth)).is_err() {
                break;
            }
            if !repeat {
                for c in g.children(&t).iter().rev() {
                    stack.push((c.clone(), depth + 1));
                }
            }
        }
        return Ok(());
    }

    if let Some(target) = args.run {
        // `--run` alone prints the captured tree; paired with `--screenshot` it renders
        // the actual run view, which is how the TUI gets verified without a terminal.
        let Some(size) = args.screenshot else {
            return run_headless(&root, &target, &task::split_args(&args.args));
        };
        let mut app = App::new(tasks, root).with_config(config);
        app.start_run(&target)?;
        drive(&mut app, &args.keys);
        return screenshot(&mut app, &size, "");
    }

    let mut app = App::new(tasks, root).with_config(config);

    if args.last && !app.open_last_run() {
        bail!("no stored runs for this project yet");
    }

    if let Some(size) = args.screenshot {
        return screenshot(&mut app, &size, &args.keys);
    }

    let mut terminal = ratatui::init();
    let result = run(&mut terminal, &mut app);
    ratatui::restore();
    result
}

/// Grep every stored run, newest first, grouped by run and task.
fn search_stored(pattern: &str) -> Result<()> {
    let base = store::state_dir();
    let query = search::Query::new(pattern)?;
    let (results, dropped) = search::search_store(&base, &query, 50);

    let mut out = io::stdout().lock();
    if results.is_empty() {
        let stored = store::list(&base).len();
        writeln!(out, "no matches for /{pattern}/ in {stored} stored runs")?;
        return Ok(());
    }

    let mut total = 0;
    for r in &results {
        let mark = if r.manifest.failed() { "✗" } else { "✓" };
        writeln!(
            out,
            "\n{mark} {}  task {}  ({} hits)",
            r.manifest.id,
            r.manifest.root,
            r.hits.len()
        )?;
        let mut current = String::new();
        for hit in &r.hits {
            if hit.task != current {
                writeln!(out, "  {}", hit.task)?;
                current = hit.task.clone();
            }
            writeln!(out, "    {:>5}  {}", hit.line_no, hit.text)?;
            total += 1;
        }
    }

    writeln!(out, "\n{total} hits in {} runs", results.len())?;
    if dropped > 0 {
        // Never let a capped result read as a complete one.
        writeln!(out, "{dropped} further hits not shown (per-run cap)")?;
    }
    Ok(())
}

fn dump(mode: String, tasks: &[task::Task]) -> Result<()> {
    let mode = if mode == "verb" {
        pivot::Mode::Verb
    } else {
        pivot::Mode::Domain
    };
    let all: Vec<usize> = (0..tasks.len()).collect();
    let tree = pivot::build(mode, tasks, &all);
    let mut out = io::stdout().lock();
    for row in tree.flatten(&|_| true) {
        let n = &tree.nodes[row.node];
        let glyph = if n.is_group() { "▾" } else { " " };
        // A group row that is also a task is marked, since the tree alone cannot show
        // that `backend:migrate` is runnable as well as foldable.
        let runnable = if n.is_group() && n.task.is_some() {
            "*"
        } else {
            " "
        };
        let count = if n.is_group() {
            format!("  {}", n.count)
        } else {
            String::new()
        };
        if writeln!(
            out,
            "{}{glyph}{runnable}{}{count}",
            "  ".repeat(row.depth),
            n.label
        )
        .is_err()
        {
            break;
        }
    }
    Ok(())
}

/// Run a task to completion and print what the capture layer reconstructed: the
/// execution tree, each task's status and duration, and its output indented beneath it.
fn run_headless(dir: &Path, target: &str, argv: &[String]) -> Result<()> {
    let mut r = run::Run::start(dir, target, argv, false, false)?;
    while !r.finished() {
        r.poll();
        std::thread::sleep(Duration::from_millis(20));
    }
    r.poll();

    let mut out = io::stdout().lock();
    let mut stack = vec![(target.to_string(), 0usize)];
    let mut seen = std::collections::HashSet::new();
    while let Some((name, depth)) = stack.pop() {
        if !seen.insert(name.clone()) {
            continue;
        }
        let pad = "  ".repeat(depth);
        if let Some(t) = r.tasks.get(&name) {
            let secs = t
                .elapsed()
                .map(|d| format!("{:.2}s", d.as_secs_f64()))
                .unwrap_or_default();
            // Ignore write errors throughout: piping into `head` closes the pipe on us.
            let _ = writeln!(out, "{pad}{} {name}  {secs}", t.status.glyph());
            for l in &t.lines {
                let marker = if l.is_command { "$" } else { "│" };
                // Raw, so colour shows on a terminal and survives `cat -v` inspection.
                let _ = writeln!(out, "{pad}  {marker} {}", l.raw);
            }
        }
        for c in r.graph.children(&name).iter().rev() {
            stack.push((c.clone(), depth + 1));
        }
    }
    let _ = writeln!(
        out,
        "\nexit {}  in {:.2}s",
        r.exit.unwrap_or(-1),
        r.duration.unwrap_or_default().as_secs_f64()
    );

    // Store it just as the TUI would: a run is a run whichever way it was started, and
    // `--run` output you cannot search later would be a trap.
    match store::save(&store::state_dir(), dir, &r) {
        Ok(path) => {
            let _ = writeln!(
                out,
                "saved to {}  ({} secrets masked)",
                path.display(),
                r.redacted_secrets
            );
        }
        Err(e) => {
            let _ = writeln!(out, "not saved: {e}");
        }
    }
    Ok(())
}

/// Play `keys` into a *live* run, then let it finish.
///
/// The plain screenshot path applies keys to a finished run, which cannot exercise
/// anything interactive — an interactive run never finishes on its own, so waiting first
/// is a deadlock. Keys are paced so the child has time to reach its prompt between them.
fn drive(app: &mut App, keys: &str) {
    let deadline = std::time::Instant::now() + Duration::from_secs(30);
    let mut pending = keys.chars();
    let mut next_key = std::time::Instant::now() + Duration::from_millis(400);

    loop {
        app.poll_run();
        if std::time::Instant::now() > deadline {
            break;
        }
        if std::time::Instant::now() >= next_key {
            match pending.next() {
                Some(c) => {
                    let code = match c {
                        '\t' => KeyCode::Tab,
                        '\n' => KeyCode::Enter,
                        c => KeyCode::Char(c),
                    };
                    handle_key(app, KeyEvent::new(code, KeyModifiers::NONE));
                    next_key = std::time::Instant::now() + Duration::from_millis(400);
                }
                None if app.run.as_ref().is_some_and(|r| r.finished()) => break,
                None => {}
            }
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    app.poll_run();
}

fn screenshot(app: &mut App, size: &str, keys: &str) -> Result<()> {
    let Some((w, h)) = size.split_once(['x', 'X']) else {
        bail!("--screenshot expects WxH, e.g. 90x30");
    };
    let (w, h): (u16, u16) = (w.parse()?, h.parse()?);
    for c in keys.chars() {
        let code = match c {
            '\t' => KeyCode::Tab,
            '\n' => KeyCode::Enter,
            c => KeyCode::Char(c),
        };
        handle_key(app, KeyEvent::new(code, KeyModifiers::NONE));
    }
    let mut out = io::stdout().lock();
    for line in ui::render_headless(app, w, h) {
        if writeln!(out, "{line}").is_err() {
            break;
        }
    }
    Ok(())
}

/// Quit, taking any running child with us. `Run`'s `Drop` does the killing; this exists
/// so the intent is explicit at the call site rather than an accident of scope.
fn quit(app: &mut App) -> bool {
    if app.run_in_flight() {
        app.cancel_run();
    }
    true
}

fn run(terminal: &mut ratatui::DefaultTerminal, app: &mut App) -> Result<()> {
    loop {
        terminal.draw(|f| ui::draw(f, app))?;

        // Poll briefly so a live run redraws as output arrives, rather than only when a
        // key is pressed.
        let tick = if app.run.as_ref().is_some_and(|r| !r.finished()) {
            Duration::from_millis(50)
        } else {
            Duration::from_millis(200)
        };

        if !event::poll(tick)? {
            app.poll_run();
            app.poll_watch();
            continue;
        }
        let Event::Key(key) = event::read()? else {
            continue;
        };
        if key.kind != KeyEventKind::Press {
            continue;
        }
        if handle_key(app, key) {
            return Ok(());
        }
        app.poll_run();
        app.poll_watch();
    }
}

/// Returns true when the app should exit.
/// A dangerous task is waiting on a yes; nothing else gets through until it is answered.
fn handle_confirm_key(app: &mut App, key: KeyEvent) -> bool {
    match key.code {
        KeyCode::Char('y') | KeyCode::Char('Y') => app.confirm_yes(),
        _ => app.confirm_no(),
    }
    false
}

/// `gg` and `G`, handled once for every screen.
///
/// Returns true if the key was consumed. `g` on its own only arms the pair; anything else
/// disarms it, so a forgotten `g` cannot silently swallow the next keystroke.
fn handle_vim_motion(app: &mut App, key: KeyEvent) -> bool {
    // Prompts own every key while they are open.
    if app.entering_args
        || app.searching
        || app.sending_input
        || app.history_searching
        || app.jumping
    {
        return false;
    }
    match key.code {
        KeyCode::Char('g') => {
            if std::mem::take(&mut app.pending_g) {
                app.goto_top();
            } else {
                app.pending_g = true;
            }
            true
        }
        KeyCode::Char('G') => {
            app.pending_g = false;
            app.goto_bottom();
            true
        }
        _ => {
            app.pending_g = false;
            false
        }
    }
}

/// Which action, if any, this key means on `screen`.
///
/// Dispatch goes through actions rather than literal characters, which is what makes the
/// `keys:` block in `config.yaml` work: the map is consulted here, and the handlers below
/// only ever see actions.
fn action(app: &App, key: KeyCode, screen: Screen) -> Option<Action> {
    let KeyCode::Char(c) = key else { return None };
    match screen {
        Screen::Picker => app.keymap.picker(c),
        Screen::Run => app.keymap.run(c),
        Screen::History => app.keymap.history(c),
        _ => None,
    }
}
fn handle_key(app: &mut App, key: KeyEvent) -> bool {
    if app.confirm.is_some() {
        return handle_confirm_key(app, key);
    }
    if app.screen == Screen::Run {
        return handle_run_key(app, key);
    }
    if handle_vim_motion(app, key) {
        return false;
    }
    if app.screen == Screen::History {
        return handle_history_key(app, key);
    }
    if app.screen == Screen::Detail {
        match key.code {
            KeyCode::Char('q') => return quit(app),
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return quit(app)
            }
            KeyCode::Esc | KeyCode::Char('s') => app.close_detail(),
            KeyCode::Char('j') | KeyCode::Down => app.detail_scroll(1),
            KeyCode::Char('k') | KeyCode::Up => app.detail_scroll(-1),
            KeyCode::PageDown => app.detail_scroll(10),
            KeyCode::PageUp => app.detail_scroll(-10),
            // Running it is the point of having read this.
            KeyCode::Enter => {
                if let Some(name) = app.detail_of.clone() {
                    app.close_detail();
                    app.request_run(&name, &[]);
                }
            }
            KeyCode::Char('a') => {
                if let Some(name) = app.detail_of.clone() {
                    app.close_detail();
                    app.begin_args(&name);
                }
            }
            _ => {}
        }
        return false;
    }
    if app.screen == Screen::Help {
        match key.code {
            KeyCode::Char('q') => return quit(app),
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                return quit(app)
            }
            KeyCode::Char('?') | KeyCode::Esc => app.toggle_help(),
            KeyCode::Char('j') | KeyCode::Down => app.help_scroll(1),
            KeyCode::Char('k') | KeyCode::Up => app.help_scroll(-1),
            KeyCode::PageDown => app.help_scroll(10),
            KeyCode::PageUp => app.help_scroll(-10),
            _ => {}
        }
        return false;
    }
    if app.entering_args {
        match key.code {
            KeyCode::Esc => app.cancel_args(),
            KeyCode::Enter => app.confirm_args(),
            KeyCode::Backspace => app.args_backspace(),
            KeyCode::Delete => app.args_delete(),
            KeyCode::Left => app.args_move(-1),
            KeyCode::Right => app.args_move(1),
            KeyCode::Home => app.args_home(),
            KeyCode::End => app.args_end(),
            KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.args_insert(c)
            }
            _ => {}
        }
        return false;
    }

    if app.jumping {
        match key.code {
            KeyCode::Esc => app.cancel_jump(),
            KeyCode::Enter => app.accept_jump(),
            KeyCode::Backspace => app.pop_jump(),
            KeyCode::Down | KeyCode::Tab => app.jump_step(1),
            KeyCode::Up | KeyCode::BackTab => app.jump_step(-1),
            KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => app.push_jump(c),
            _ => {}
        }
        return false;
    }

    if app.filtering && handle_filter_key(app, key) {
        return false;
    }

    match key.code {
        KeyCode::Esc => return quit(app),
        k if action(app, k, Screen::Picker) == Some(Action::Quit) => return quit(app),
        KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => return quit(app),

        KeyCode::Char('j') | KeyCode::Down => app.move_cursor(1),
        KeyCode::Char('k') | KeyCode::Up => app.move_cursor(-1),
        KeyCode::Char('d') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.move_cursor(half);
        }
        KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.move_cursor(-half);
        }
        KeyCode::PageDown => app.move_cursor(15),
        KeyCode::PageUp => app.move_cursor(-15),
        KeyCode::Home => app.move_cursor(-(app.rows.len() as isize)),
        KeyCode::End => app.move_cursor(app.rows.len() as isize),

        // The pivot. Selection, filter and the other mode's folds all survive it.
        // On `p`, not `g`: `gg` belongs to vim.
        k if action(app, k, Screen::Picker) == Some(Action::Pivot) => app.toggle_mode(),

        // Space folds, enter runs — kept strictly separate. A node that is both a group
        // and a task (`backend:migrate`) is then runnable from its own header, so its
        // subtree never has to relist it just to make it reachable.
        KeyCode::Char(' ') => app.toggle_fold(),
        KeyCode::Enter => match app.selected_task() {
            Some(ti) => {
                let name = app.tasks[ti].name.clone();
                app.request_run(&name, &[]);
            }
            None => {
                let what = app
                    .selected_node()
                    .map(|n| n.label.clone())
                    .unwrap_or_default();
                app.status = Some(format!(
                    "`{what}` groups tasks but is not one — space folds it"
                ));
            }
        },
        KeyCode::Right | KeyCode::Left => {
            if app.selected_node().map(|n| n.is_group()).unwrap_or(false) {
                app.toggle_fold();
            }
        }

        KeyCode::Tab => {
            let any_open = app.rows.iter().any(|r| r.open);
            app.set_fold_all(!any_open);
        }

        k if action(app, k, Screen::Picker) == Some(Action::Filter) => {
            app.filtering = true;
            app.status = None;
        }

        // Jump rather than filter: the list stays whole and only the cursor moves.
        k if action(app, k, Screen::Picker) == Some(Action::Jump) => app.begin_jump(),

        // What is this task, and what will it actually run?
        k if action(app, k, Screen::Picker) == Some(Action::Detail) => app.open_detail(),

        // Run with arguments. Half a real Taskfile needs them.
        k if action(app, k, Screen::Picker) == Some(Action::Args) => match app.selected_task() {
            Some(ti) => {
                let name = app.tasks[ti].name.clone();
                app.begin_args(&name);
            }
            None => app.status = Some("nothing to run here — space folds it".into()),
        },

        // Let the next run ask questions.
        k if action(app, k, Screen::Picker) == Some(Action::Interactive) => {
            app.toggle_interactive()
        }

        // Ignore go-task's up-to-date checks on the next run.
        // Ignore go-task's up-to-date checks on the next run.
        k if action(app, k, Screen::Picker) == Some(Action::Force) => app.toggle_force(),

        k if action(app, k, Screen::Picker) == Some(Action::Help) => app.toggle_help(),

        // Back to whatever is still running.
        k if action(app, k, Screen::Picker) == Some(Action::ResumeRun) => {
            app.resume_run();
        }

        // Past runs.
        k if action(app, k, Screen::Picker) == Some(Action::History) => app.open_history(),

        _ => {}
    }
    false
}

/// Keys for the run view. Returns true when the app should exit.
/// Keys for the archive list.
fn handle_history_key(app: &mut App, key: KeyEvent) -> bool {
    if app.history_searching {
        match key.code {
            KeyCode::Esc => app.clear_history_search(),
            KeyCode::Enter => {
                // Keep the query; it carries into the run you open.
                app.history_searching = false;
            }
            KeyCode::Backspace => app.pop_history_search(),
            KeyCode::Down => app.history_move_cursor(1),
            KeyCode::Up => app.history_move_cursor(-1),
            KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.push_history_search(c)
            }
            _ => {}
        }
        return false;
    }

    match key.code {
        k if action(app, k, Screen::History) == Some(Action::Quit) => return quit(app),
        KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => return quit(app),
        KeyCode::Esc => {
            app.screen = Screen::Picker;
            app.status = None;
        }
        // Widen to every project, or narrow back to this one.
        k if action(app, k, Screen::History) == Some(Action::AllProjects) => {
            app.toggle_history_scope()
        }

        k if action(app, k, Screen::History) == Some(Action::Search) => app.begin_history_search(),
        k if action(app, k, Screen::History) == Some(Action::Help) => app.toggle_help(),

        KeyCode::Char('j') | KeyCode::Down => app.history_move_cursor(1),
        KeyCode::Char('k') | KeyCode::Up => app.history_move_cursor(-1),
        KeyCode::Char('d') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.history_move_cursor(half);
        }
        KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.history_move_cursor(-half);
        }
        KeyCode::PageDown => app.history_move_cursor(15),
        KeyCode::PageUp => app.history_move_cursor(-15),
        KeyCode::Home => app.history_move_cursor(-(app.history.len() as isize)),
        KeyCode::End => app.history_move_cursor(app.history.len() as isize),
        KeyCode::Enter => app.open_stored_run(),
        _ => {}
    }
    false
}

fn handle_run_key(app: &mut App, key: KeyEvent) -> bool {
    // Input mode: everything except the escape hatch goes to the child.
    if app.sending_input {
        match key.code {
            KeyCode::Esc => app.end_input(),
            // A pty expects carriage return, not newline.
            KeyCode::Enter => app.send_input(b"\r"),
            KeyCode::Backspace => app.send_input(&[0x7f]),
            KeyCode::Tab => app.send_input(b"\t"),
            KeyCode::Up => app.send_input(b"\x1b[A"),
            KeyCode::Down => app.send_input(b"\x1b[B"),
            KeyCode::Right => app.send_input(b"\x1b[C"),
            KeyCode::Left => app.send_input(b"\x1b[D"),
            KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.send_input(&[0x03])
            }
            KeyCode::Char('d') if key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.send_input(&[0x04])
            }
            KeyCode::Char(c) => {
                let mut buf = [0u8; 4];
                app.send_input(c.encode_utf8(&mut buf).as_bytes());
            }
            _ => {}
        }
        return false;
    }

    if app.entering_args {
        match key.code {
            KeyCode::Esc => app.cancel_args(),
            KeyCode::Enter => app.confirm_args(),
            KeyCode::Backspace => app.args_backspace(),
            KeyCode::Delete => app.args_delete(),
            KeyCode::Left => app.args_move(-1),
            KeyCode::Right => app.args_move(1),
            KeyCode::Home => app.args_home(),
            KeyCode::End => app.args_end(),
            KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.args_insert(c)
            }
            _ => {}
        }
        return false;
    }

    if app.searching {
        match key.code {
            KeyCode::Esc => {
                app.clear_search();
                return false;
            }
            // Keep the query and its highlights; leave the input line.
            KeyCode::Enter => {
                app.searching = false;
                return false;
            }
            KeyCode::Backspace => {
                app.pop_search();
                return false;
            }
            KeyCode::Down => {
                app.search_step(1);
                return false;
            }
            KeyCode::Up => {
                app.search_step(-1);
                return false;
            }
            KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
                app.push_search(c);
                return false;
            }
            _ => {}
        }
    }

    match key.code {
        KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => return quit(app),
        k if action(app, k, Screen::Run) == Some(Action::Quit) => return quit(app),

        // Stop the run without leaving the view.
        k if action(app, k, Screen::Run) == Some(Action::Stop) => app.cancel_run(),

        // Answer whatever the task is asking.
        //
        // This works even in a non-interactive run: go-task wraps stdout and stderr for
        // prefixing but leaves stdin alone, so keystrokes reach the child regardless —
        // verified against a real `task` process. You may not be able to *see* the
        // question, but typing `y⏎` still answers it, which beats re-running a deploy
        // from the start just to be able to type.
        k if action(app, k, Screen::Run) == Some(Action::Input) => app.begin_input(),

        // Re-run whenever the source changes.
        k if action(app, k, Screen::Run) == Some(Action::Watch) => app.toggle_watch(),

        // Re-run this task interactively, when seeing the prompt matters more than not
        // starting over.
        k if action(app, k, Screen::Run) == Some(Action::InteractiveRerun) => {
            if let Some(name) = app.run.as_ref().map(|r| r.root.clone()) {
                app.interactive_next = true;
                let args = app.run.as_ref().map(|r| r.args.clone()).unwrap_or_default();
                app.request_run(&name, &args);
            }
        }

        // Back to the picker. The run keeps going in the background and is still there
        // when you come back.
        KeyCode::Esc => {
            app.screen = Screen::Picker;
            app.status = None;
        }

        KeyCode::Char('j') | KeyCode::Down => app.run_move_cursor(1),
        KeyCode::Char('k') | KeyCode::Up => app.run_move_cursor(-1),
        KeyCode::Char('d') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.run_move_cursor(half);
        }
        KeyCode::Char('u') if key.modifiers.contains(KeyModifiers::CONTROL) => {
            let half = app.half_page();
            app.run_move_cursor(-half);
        }
        // Reading an error usually ends with pasting it somewhere.
        k if action(app, k, Screen::Run) == Some(Action::Yank) => app.yank_line(),
        k if action(app, k, Screen::Run) == Some(Action::YankAll) => app.yank_task_output(),
        KeyCode::PageDown => app.run_move_cursor(15),
        KeyCode::PageUp => app.run_move_cursor(-15),
        KeyCode::Home => app.run_move_cursor(-(app.run_rows.len() as isize)),
        KeyCode::End => app.run_move_cursor(app.run_rows.len() as isize),

        KeyCode::Char(' ') | KeyCode::Right | KeyCode::Left => app.run_toggle_fold(),

        // Search the output. `/` in the picker filters task names; here it searches
        // what those tasks printed. Different corpora, deliberately different jobs.
        k if action(app, k, Screen::Run) == Some(Action::Search) => {
            app.searching = true;
            app.status = None;
        }
        k if action(app, k, Screen::Run) == Some(Action::NextMatch) => app.search_step(1),
        k if action(app, k, Screen::Run) == Some(Action::PrevMatch) => app.search_step(-1),
        // Collapse the run to just the matching lines, kept under their tasks.
        k if action(app, k, Screen::Run) == Some(Action::FilterMatches) => {
            app.toggle_filter_matches()
        }
        // More or less context around each hit.
        k if action(app, k, Screen::Run) == Some(Action::ContextMore) => app.set_filter_context(1),
        k if action(app, k, Screen::Run) == Some(Action::ContextLess) => app.set_filter_context(-1),

        // Resume tracking whatever is running after you have gone looking around.
        k if action(app, k, Screen::Run) == Some(Action::Follow) => app.following = !app.following,

        k if action(app, k, Screen::Run) == Some(Action::History) => app.open_history(),

        // Re-run the task under the cursor — the tight loop when you are fixing one
        // broken step. Note this is a fresh `task <name>`, not a resume of the parent.
        k if action(app, k, Screen::Run) == Some(Action::Rerun) => app.rerun_selected(),

        // Re-run with different arguments.
        k if action(app, k, Screen::Run) == Some(Action::Args) => {
            if let Some(name) = app.run_selected_task() {
                app.begin_args(&name);
            }
        }

        _ => {}
    }
    false
}

/// Returns true if the key was consumed by filter mode.
fn handle_filter_key(app: &mut App, key: KeyEvent) -> bool {
    match key.code {
        KeyCode::Esc => {
            app.clear_query();
            true
        }
        KeyCode::Enter => {
            // Keep the filter applied, leave the input — you filter to narrow the tree,
            // then navigate what is left.
            app.filtering = false;
            true
        }
        KeyCode::Backspace => {
            app.pop_query();
            true
        }
        KeyCode::Down | KeyCode::Up => false,
        KeyCode::Char(c) if !key.modifiers.contains(KeyModifiers::CONTROL) => {
            app.push_query(c);
            true
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pivot::fixture;

    fn app_at(name: &str) -> App {
        let tasks = fixture(&["backend:migrate", "backend:migrate:down", "backend:lint"]);
        // A real directory with no Taskfile: pressing enter starts a run that fails
        // immediately, which is all these tests need — they assert the transition, not
        // the output.
        let mut app = App::new(tasks, std::env::temp_dir());
        app.set_fold_all(true);
        let ti = app.tasks.iter().position(|t| t.name == name).unwrap();
        app.cursor = app
            .rows
            .iter()
            .position(|r| app.tree.nodes[r.node].task == Some(ti))
            .unwrap();
        app
    }

    fn press(app: &mut App, code: KeyCode) {
        handle_key(app, KeyEvent::new(code, KeyModifiers::NONE));
    }

    /// `backend:migrate` is a group *and* a task. Space must fold it without running, so
    /// the two actions never compete for one key.
    #[test]
    fn space_folds_a_group_that_is_also_a_task() {
        let mut app = app_at("backend:migrate");
        assert!(app.selected_node().unwrap().is_group());
        let before = app.rows.len();

        press(&mut app, KeyCode::Char(' '));

        assert!(app.rows.len() < before, "the subtree collapsed");
        assert_eq!(app.status, None, "space does not run anything");
    }

    /// …and enter must run it from its own header, without expanding. That is the whole
    /// reason the subtree does not relist the task inside itself.
    #[test]
    fn enter_runs_a_group_that_is_also_a_task_without_folding() {
        let mut app = app_at("backend:migrate");
        let before = app.rows.len();

        press(&mut app, KeyCode::Enter);

        assert_eq!(
            app.rows.len(),
            before,
            "enter leaves the picker's folds alone"
        );
        assert_eq!(app.screen, Screen::Run);
        assert_eq!(
            app.run.as_ref().map(|r| r.root.as_str()),
            Some("backend:migrate"),
            "the group header ran itself, not a child"
        );
    }

    /// A pure group has nothing to run; say so rather than silently folding.
    #[test]
    fn enter_on_a_pure_group_explains_itself() {
        let mut app = app_at("backend:lint");
        app.cursor = 0; // the `backend` namespace row
        assert!(app.selected_task().is_none());
        let before = app.rows.len();

        press(&mut app, KeyCode::Enter);

        assert_eq!(app.rows.len(), before);
        let status = app.status.as_deref().unwrap_or_default();
        assert!(status.contains("not one"), "{status:?}");
    }

    #[test]
    fn enter_runs_a_plain_leaf() {
        let mut app = app_at("backend:lint");
        press(&mut app, KeyCode::Enter);
        assert_eq!(app.screen, Screen::Run);
        assert_eq!(
            app.run.as_ref().map(|r| r.root.as_str()),
            Some("backend:lint")
        );
    }

    fn app_with_live_run(task: &str) -> App {
        let mut app = app_at("backend:lint");
        app.run = Some(crate::run::Run::detached(
            task,
            crate::run::Run::graph_from(&[(task, &[])]),
        ));
        app.screen = Screen::Picker;
        app
    }

    /// Selecting the task that is already running means "show me it". Starting a second
    /// one would drop the first Run, and dropping it kills the process group — on a
    /// half-finished deploy that is the worst thing this tool could do.
    #[test]
    fn running_the_task_already_running_goes_back_to_it() {
        let mut app = app_with_live_run("backend:lint");
        let before = app.run.as_ref().unwrap().started;

        app.request_run("backend:lint", &[]);

        assert_eq!(app.screen, Screen::Run, "it showed the run");
        assert!(app.confirm.is_none(), "and did not ask about anything");
        assert_eq!(
            app.run.as_ref().unwrap().started,
            before,
            "the same run, not a fresh one"
        );
    }

    /// Starting something *else* while a run is live stops it, so it asks first.
    #[test]
    fn starting_another_task_mid_run_asks_first() {
        let mut app = app_with_live_run("backend:lint");

        app.request_run("backend:migrate", &[]);

        assert_eq!(
            app.confirm_reason,
            crate::app::ConfirmReason::WouldStopRunning
        );
        assert!(app.confirm.is_some(), "waiting on a yes");
        assert_eq!(
            app.run.as_ref().map(|r| r.root.as_str()),
            Some("backend:lint"),
            "the live run is untouched until answered"
        );
    }

    /// `gg` and `G`, as in vim. A lone `g` only arms the pair.
    #[test]
    fn gg_and_g_jump_to_the_ends() {
        let mut app = app_at("backend:lint");
        app.set_fold_all(true);
        let last = app.rows.len() - 1;

        press(&mut app, KeyCode::Char('G'));
        assert_eq!(app.cursor, last);

        press(&mut app, KeyCode::Char('g'));
        assert!(app.pending_g, "armed, and nothing has moved");
        assert_eq!(app.cursor, last);

        press(&mut app, KeyCode::Char('g'));
        assert_eq!(app.cursor, 0);
        assert!(!app.pending_g, "and disarmed");
    }

    /// A forgotten `g` must not swallow the next keystroke.
    #[test]
    fn a_lone_g_is_disarmed_by_anything_else() {
        let mut app = app_at("backend:lint");
        app.set_fold_all(true);

        let before = app.cursor;
        press(&mut app, KeyCode::Char('g'));
        assert!(app.pending_g);
        press(&mut app, KeyCode::Char('j'));
        assert!(!app.pending_g, "disarmed");
        assert_eq!(app.cursor, before + 1, "and `j` still moved");
    }

    /// `p` took over the pivot so `gg` could have `g`.
    #[test]
    fn p_toggles_the_pivot() {
        let mut app = app_at("backend:lint");
        let before = app.mode;
        press(&mut app, KeyCode::Char('p'));
        assert_ne!(app.mode, before);
    }

    /// Leaving a run must not be a one-way door.
    #[test]
    fn v_goes_back_to_the_live_run() {
        let mut app = app_with_live_run("deploy:local:env");
        press(&mut app, KeyCode::Char('v'));
        assert_eq!(app.screen, Screen::Run);
        assert_eq!(
            app.run.as_ref().map(|r| r.root.as_str()),
            Some("deploy:local:env")
        );
    }

    /// Rebinding has to change what the key actually does, not just what `?` claims.
    #[test]
    fn a_rebound_key_dispatches_to_the_new_key() {
        let mut app = app_at("backend:lint");
        app.keymap.rebind(keys::Action::Pivot, 'z');
        let before = app.mode;

        press(&mut app, KeyCode::Char('p'));
        assert_eq!(app.mode, before, "the old key no longer pivots");

        press(&mut app, KeyCode::Char('z'));
        assert_ne!(app.mode, before, "the new one does");
    }

    /// An action means the same thing wherever it is offered.
    #[test]
    fn rebinding_applies_on_every_screen_that_offers_it() {
        let mut app = app_at("backend:lint");
        app.keymap.rebind(keys::Action::Help, 'H');
        press(&mut app, KeyCode::Char('H'));
        assert_eq!(app.screen, Screen::Help);
    }

    /// The regression this pins: `i` used to refuse to type at a normal run and re-ran it
    /// interactively instead, which on a half-finished deploy is the worst possible move.
    /// stdin reaches the child either way, so `i` must open input mode regardless.
    #[test]
    fn i_types_at_a_normal_run_rather_than_restarting_it() {
        let mut app = app_at("backend:lint");
        app.run = Some(crate::run::Run::detached(
            "backend:lint",
            crate::run::Run::graph_from(&[("backend:lint", &[])]),
        ));
        app.screen = Screen::Run;
        assert!(
            !app.run.as_ref().unwrap().interactive,
            "a normal, buffered run"
        );

        press(&mut app, KeyCode::Char('i'));

        assert!(app.sending_input, "input mode opened");
        assert!(!app.interactive_next, "and nothing was re-run");
        assert_eq!(
            app.run.as_ref().map(|r| r.root.as_str()),
            Some("backend:lint"),
            "the same run is still there"
        );
    }

    /// …while ⇧I is the deliberate restart, for when seeing the prompt matters more.
    #[test]
    fn shift_i_re_runs_interactively() {
        let mut app = app_at("backend:lint");
        app.run = Some(crate::run::Run::detached(
            "backend:lint",
            crate::run::Run::graph_from(&[("backend:lint", &[])]),
        ));
        app.screen = Screen::Run;

        press(&mut app, KeyCode::Char('I'));

        assert!(app.interactive_next, "armed for interactive");
        assert!(!app.sending_input, "and not typing at the old one");
    }

    /// Leaving the run view goes back to the picker without discarding the run — it is
    /// still going, and still there when you return.
    #[test]
    fn esc_returns_to_the_picker_and_keeps_the_run() {
        let mut app = app_at("backend:lint");
        press(&mut app, KeyCode::Enter);
        press(&mut app, KeyCode::Esc);
        assert_eq!(app.screen, Screen::Picker);
        assert!(app.run.is_some(), "the run survives");
    }
}
