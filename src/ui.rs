//! Rendering. The tree is the whole screen; the pivot is a line of status, not a menu.

use crate::app::{App, ConfirmReason, RunRow, Screen};
use crate::keys;
use crate::pivot::Mode;
use crate::run::Status;
use crate::theme::Theme;
use ratatui::layout::{Constraint, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{List, ListItem, ListState, Paragraph};
use ratatui::Frame;

/// Width of the line-number gutter in the run view.
const GUTTER: usize = 5;

/// Break `text` to fit `width`, preferring word boundaries but hard-splitting anything
/// that cannot fit — file paths and Rust type signatures routinely exceed a whole line,
/// and leaving them to overflow is how the end of an error message goes missing.
fn wrap(text: &str, width: usize) -> Vec<String> {
    if width == 0 {
        return vec![text.to_string()];
    }
    let mut out = Vec::new();
    let mut line = String::new();
    let mut len = 0usize;

    for word in text.split_inclusive(' ') {
        let wlen = word.chars().count();
        if len + wlen > width && len > 0 {
            out.push(std::mem::take(&mut line));
            len = 0;
        }
        if wlen > width {
            // A single token longer than the line: hard-split it.
            for c in word.chars() {
                if len == width {
                    out.push(std::mem::take(&mut line));
                    len = 0;
                }
                line.push(c);
                len += 1;
            }
        } else {
            line.push_str(word);
            len += wlen;
        }
    }
    if !line.is_empty() || out.is_empty() {
        out.push(line);
    }
    out
}

pub fn draw(f: &mut Frame, app: &mut App) {
    let t = app.theme.clone();
    let t = &t;
    let [header, body, footer] = Layout::vertical([
        Constraint::Length(1),
        Constraint::Min(1),
        Constraint::Length(1),
    ])
    .areas(f.area());

    app.viewport = body.height as usize;

    match app.screen {
        Screen::Picker => {
            draw_header(f, header, app, t);
            draw_tree(f, body, app, t);
            draw_footer(f, footer, app, t);
        }
        Screen::Run => {
            draw_run_header(f, header, app, t);
            draw_run(f, body, app, t);
            draw_run_footer(f, footer, app, t);
        }
        Screen::History => {
            draw_history_header(f, header, app, t);
            draw_history(f, body, app, t);
            draw_history_footer(f, footer, app, t);
        }
        Screen::Help => {
            draw_help_header(f, header, app, t);
            draw_help(f, body, app, t);
            draw_help_footer(f, footer, app, t);
        }
        Screen::Detail => {
            draw_detail_header(f, header, app, t);
            draw_detail(f, body, app, t);
            draw_detail_footer(f, footer, app, t);
        }
    }
}

fn draw_detail_header(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    let name = app.detail_of.clone().unwrap_or_default();
    let mut spans = vec![
        Span::styled(
            "taskui",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ),
        Span::styled(" · ", Style::default().fg(t.dim)),
        Span::styled(name.clone(), Style::default().add_modifier(Modifier::BOLD)),
    ];
    if let Some(o) = app.outcomes.get(&name) {
        let (glyph, colour) = if o.ok {
            ("   ✓ ", t.status_ok)
        } else {
            ("   ✗ ", t.status_failed)
        };
        spans.push(Span::styled(
            glyph,
            Style::default().fg(colour).add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::styled(ago(o.when_unix), Style::default().fg(t.dim)));
    }
    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

fn draw_detail(f: &mut Frame, area: Rect, app: &mut App, t: &Theme) {
    let width = area.width.saturating_sub(4) as usize;
    let d = &app.detail;
    let mut lines: Vec<Line> = Vec::new();

    let section = |lines: &mut Vec<Line>, title: &str| {
        if !lines.is_empty() {
            lines.push(Line::from(""));
        }
        lines.push(Line::from(Span::styled(
            title.to_string(),
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        )));
    };

    if !d.summary.is_empty() {
        for para in &d.summary {
            for chunk in wrap(para, width) {
                lines.push(Line::from(Span::styled(
                    format!("  {chunk}"),
                    Style::default().fg(t.text),
                )));
            }
        }
    }

    if !d.requires.is_empty() {
        section(&mut lines, "requires");
        for v in &d.requires {
            lines.push(Line::from(vec![
                Span::raw("  "),
                Span::styled(
                    format!("{v}="),
                    Style::default().fg(t.mode).add_modifier(Modifier::BOLD),
                ),
                Span::styled("   must be supplied with `a`", Style::default().fg(t.dim)),
            ]));
        }
    }

    if !d.dependencies.is_empty() {
        section(&mut lines, "runs first");
        for dep in &d.dependencies {
            lines.push(Line::from(Span::styled(
                format!("  {dep}"),
                Style::default().fg(t.alias),
            )));
        }
    }

    if !d.commands.is_empty() {
        section(&mut lines, "will run");
        for cmd in &d.commands {
            // Another task, or a shell line — worth telling apart at a glance.
            let (style, text) = match cmd.strip_prefix("Task: ") {
                Some(task) => (Style::default().fg(t.alias), format!("  → {task}")),
                None => (Style::default().fg(t.text), format!("  {cmd}")),
            };
            for chunk in wrap(&text, width) {
                lines.push(Line::from(Span::styled(chunk, style)));
            }
        }
    }

    if lines.is_empty() {
        lines.push(Line::from(Span::styled(
            "  go-task reports nothing about this task",
            Style::default().fg(t.dim),
        )));
    }

    let overflow = (lines.len() as u16).saturating_sub(area.height);
    app.detail_offset = app.detail_offset.min(overflow);
    f.render_widget(Paragraph::new(lines).scroll((app.detail_offset, 0)), area);
}

fn draw_detail_footer(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    if draw_confirm(f, area, app, t) {
        return;
    }
    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "j k scroll   ⏎ run   a args   esc back   q quit",
            Style::default().fg(t.dim),
        ))),
        area,
    );
}

fn draw_help_header(f: &mut Frame, area: Rect, _app: &App, t: &Theme) {
    let spans = vec![
        Span::styled(
            "taskui",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ),
        Span::styled(" · ", Style::default().fg(t.dim)),
        Span::styled("keys", Style::default().add_modifier(Modifier::BOLD)),
        Span::styled(
            "   the footer shows a subset; this is all of them",
            Style::default().fg(t.dim),
        ),
    ];
    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

fn draw_help(f: &mut Frame, area: Rect, app: &mut App, t: &Theme) {
    // Widest key column across every section, so the descriptions line up as one table
    // rather than four.
    let gutter = keys::SECTIONS
        .iter()
        .flat_map(|s| s.bindings.iter())
        .map(|b| b.keys.chars().count())
        .max()
        .unwrap_or(12)
        .max(10);

    let mut lines: Vec<Line> = Vec::new();
    for section in keys::SECTIONS {
        if !lines.is_empty() {
            lines.push(Line::from(""));
        }
        lines.push(Line::from(vec![
            Span::styled(
                section.title.to_string(),
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
            Span::styled(format!("  — {}", section.note), Style::default().fg(t.dim)),
        ]));
        for binding in section.bindings {
            lines.push(Line::from(vec![
                Span::raw("  "),
                Span::styled(
                    format!("{:<gutter$}", binding.keys),
                    Style::default().fg(t.mode).add_modifier(Modifier::BOLD),
                ),
                Span::styled("  ", Style::default()),
                Span::styled(binding.what.to_string(), Style::default().fg(t.text)),
            ]));
        }
    }

    // Clamp so scrolling cannot run off the end into blank space.
    let overflow = (lines.len() as u16).saturating_sub(area.height);
    app.help_offset = app.help_offset.min(overflow);

    f.render_widget(Paragraph::new(lines).scroll((app.help_offset, 0)), area);
}

fn draw_help_footer(f: &mut Frame, area: Rect, _app: &App, t: &Theme) {
    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "j k scroll   ? esc close   q quit",
            Style::default().fg(t.dim),
        ))),
        area,
    );
}

/// Whole minutes and hours, not a timestamp: what you want from a run list is how long
/// ago, and the exact clock time almost never matters.
fn ago(started_unix: u64) -> String {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let secs = now.saturating_sub(started_unix);
    match secs {
        s if s < 60 => "just now".to_string(),
        s if s < 3600 => format!("{}m ago", s / 60),
        s if s < 86_400 => format!("{}h ago", s / 3600),
        s => format!("{}d ago", s / 86_400),
    }
}

fn draw_history_header(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    let failed = app.history.iter().filter(|m| m.failed()).count();
    let scope = if app.history_all_projects {
        "all projects".to_string()
    } else {
        app.root
            .file_name()
            .map(|s| s.to_string_lossy().to_string())
            .unwrap_or_else(|| "this project".into())
    };
    let spans = vec![
        Span::styled(
            "taskui",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ),
        Span::styled(" · ", Style::default().fg(t.dim)),
        Span::styled("history", Style::default().add_modifier(Modifier::BOLD)),
        Span::styled(format!(" · {scope}"), Style::default().fg(t.stored)),
        match app.history_query.is_empty() {
            true => Span::raw(""),
            false => Span::styled(
                format!("   /{}", app.history_query),
                Style::default().fg(t.search),
            ),
        },
        Span::styled(
            format!("   {} runs   {failed} failed", app.history.len()),
            Style::default().fg(t.dim),
        ),
    ];
    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

fn draw_history(f: &mut Frame, area: Rect, app: &mut App, t: &Theme) {
    let items: Vec<ListItem> = app
        .history
        .iter()
        .map(|m| {
            let (glyph, colour) = if m.failed() {
                ("✗", t.status_failed)
            } else {
                ("✓", t.status_ok)
            };
            let lines: usize = m.tasks.iter().map(|t| t.lines).sum();
            ListItem::new(Line::from(vec![
                Span::styled(
                    format!("{glyph} "),
                    Style::default().fg(colour).add_modifier(Modifier::BOLD),
                ),
                Span::styled(
                    format!("{:<10}", ago(m.started_unix)),
                    Style::default().fg(t.dim),
                ),
                Span::styled(
                    format!("{:<30}", m.command()),
                    if m.failed() {
                        Style::default().fg(t.status_failed)
                    } else {
                        Style::default()
                    },
                ),
                Span::styled(
                    format!(
                        "{:>8}  {lines:>6} lines",
                        duration(std::time::Duration::from_millis(m.duration_ms))
                    ),
                    Style::default().fg(t.dim),
                ),
                // Only present when a cross-run search is narrowing the list.
                match app.history_hits.get(&m.id) {
                    Some(n) => Span::styled(
                        format!("   {n} hit{}", if *n == 1 { "" } else { "s" }),
                        Style::default().fg(t.search),
                    ),
                    None => Span::raw(""),
                },
            ]))
        })
        .collect();

    let mut state = ListState::default();
    state.select(Some(app.history_cursor));
    *state.offset_mut() = app.history_offset;
    let list = List::new(items).highlight_style(selection_style(t));
    f.render_stateful_widget(list, area, &mut state);
    app.history_offset = state.offset();
}

fn draw_history_footer(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    if app.history_searching {
        let spans = vec![
            Span::styled("search runs: ", Style::default().fg(t.search)),
            Span::raw(app.history_query.clone()),
            Span::styled("█", Style::default().fg(t.search)),
            Span::styled(
                format!("   {} runs matched", app.history.len()),
                Style::default().fg(t.dim),
            ),
            Span::styled("   ⏎ keep   esc clear", Style::default().fg(t.dim)),
        ];
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }
    if let Some(msg) = &app.status {
        f.render_widget(
            Paragraph::new(Line::from(Span::styled(
                msg.clone(),
                Style::default().fg(t.notice),
            ))),
            area,
        );
        return;
    }
    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            keys::footer(&keys::HISTORY),
            Style::default().fg(t.dim),
        ))),
        area,
    );
}

/// The highlighted-row style.
///
/// Reverse video by default rather than a fixed background colour: a dark slate looks
/// deliberate on the theme it was picked against and is invisible on any other, which is
/// exactly what happened with the original `#282c34`. Reversing whatever the terminal is
/// already using cannot be invisible. Setting `selection` to a real colour opts back into
/// a background.
fn selection_style(t: &Theme) -> Style {
    if t.selection == ratatui::style::Color::Reset {
        Style::default().add_modifier(Modifier::REVERSED)
    } else {
        Style::default()
            .bg(t.selection)
            .add_modifier(Modifier::BOLD)
    }
}

/// Durations at a glance. `0.00s` for a task that took four milliseconds reads as *no
/// information*, and `134.20s` makes you do arithmetic to see it is over two minutes.
fn duration(d: std::time::Duration) -> String {
    let secs = d.as_secs_f64();
    if secs < 1.0 {
        format!("{}ms", d.as_millis())
    } else if secs < 60.0 {
        format!("{secs:.1}s")
    } else {
        format!("{}m{:02}s", d.as_secs() / 60, d.as_secs() % 60)
    }
}

fn status_style(status: Status, t: &Theme) -> Style {
    let colour = match status {
        Status::Pending => t.status_pending,
        Status::Running => t.status_running,
        Status::Ok => t.status_ok,
        Status::Failed => t.status_failed,
        Status::Skipped => t.status_skipped,
    };
    Style::default().fg(colour)
}

fn draw_run_header(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    let Some(run) = &app.run else { return };

    let elapsed = run.duration.unwrap_or_else(|| run.started.elapsed());
    let overall = if run.finished() {
        match run.exit {
            Some(0) => ("✓", t.status_ok),
            _ => ("✗", t.status_failed),
        }
    } else {
        ("▶", t.status_running)
    };

    let mut spans = vec![
        Span::styled(
            "taskui",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ),
        Span::styled(" · ", Style::default().fg(t.dim)),
        Span::styled(
            overall.0,
            Style::default().fg(overall.1).add_modifier(Modifier::BOLD),
        ),
        Span::raw(" "),
        Span::styled(run.command(), Style::default().add_modifier(Modifier::BOLD)),
        Span::styled(
            format!("   {}", duration(elapsed)),
            Style::default().fg(t.dim),
        ),
    ];

    if run.interactive && !run.finished() {
        spans.push(Span::styled(
            "   interactive",
            Style::default().fg(t.interactive),
        ));
    }
    if let Some(watched) = app.watching.as_deref() {
        spans.push(Span::styled(
            format!("   watching {watched}"),
            Style::default().fg(t.interactive),
        ));
    }
    if run.cancelled() {
        spans.push(Span::styled(
            "   cancelled",
            Style::default().fg(t.status_failed),
        ));
    } else if run.is_stored() {
        spans.push(Span::styled(
            "   from history",
            Style::default().fg(t.stored),
        ));
    } else if run.graph.edges.is_empty() {
        spans.push(Span::styled(
            "   resolving graph…",
            Style::default().fg(t.dim),
        ));
    } else if app.following && !run.finished() {
        spans.push(Span::styled("   following", Style::default().fg(t.notice)));
    }

    if let Some(code) = run.exit {
        if code != 0 {
            spans.push(Span::styled(
                format!("   exit {code}"),
                Style::default().fg(t.status_failed),
            ));
        }
    }

    if let Some(q) = &app.search {
        let position = if app.search_hits.is_empty() {
            "no matches".to_string()
        } else {
            format!("{}/{}", app.search_idx + 1, app.search_hits.len())
        };
        spans.push(Span::styled("   /", Style::default().fg(t.dim)));
        spans.push(Span::styled(
            q.pattern.clone(),
            Style::default().fg(t.search),
        ));
        spans.push(Span::styled(
            format!("  {position}"),
            Style::default().fg(t.dim),
        ));
        if app.filter_matches {
            spans.push(Span::styled(
                format!("  filtered ±{}", app.filter_context),
                Style::default().fg(t.search),
            ));
        }
    }

    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

/// Output needs more width than a task name does, so it splits later than the picker.
const MIN_RUN_COLUMN: usize = 60;

/// How many terminal rows one run row occupies once wrapped.
fn run_row_height(app: &App, row: &RunRow, width: usize) -> usize {
    match row {
        RunRow::Task { .. } => 1,
        RunRow::Line { task, index, depth } => {
            let Some(run) = app.run.as_ref() else {
                return 1;
            };
            let text = run
                .tasks
                .get(task)
                .and_then(|t| t.lines.get(*index))
                .map(|l| l.plain.as_str())
                .unwrap_or("");
            let prefix = depth * 2 + GUTTER + 2;
            wrap(text, width.saturating_sub(prefix).max(8)).len()
        }
    }
}

/// Where each column starts, given a first visible row.
///
/// Rows have variable height once wrapped, so columns are filled by accumulating heights
/// rather than by dividing the count.
fn column_bounds(
    heights: &[usize],
    offset: usize,
    height: usize,
    columns: usize,
) -> Vec<(usize, usize)> {
    let mut bounds = Vec::new();
    let mut at = offset;
    for _ in 0..columns {
        let start = at;
        let mut used = 0;
        while at < heights.len() && used + heights[at] <= height {
            used += heights[at];
            at += 1;
        }
        // A single row taller than the column still has to go somewhere.
        if at == start && at < heights.len() {
            at += 1;
        }
        bounds.push((start, at));
        if at >= heights.len() {
            break;
        }
    }
    bounds
}

/// The first row to show, given where the cursor is.
///
/// The cursor is kept vertically centred in the *left* column rather than being allowed
/// to walk to the bottom edge: you read around the thing you are on, and having context
/// on both sides beats having it all above. The columns to the right are a look-ahead, so
/// scrolling moves everything and your place stays where you are looking.
///
/// Clamped at both ends, so a short list never scrolls and the last row can still reach
/// the bottom instead of leaving a screenful of blank below it.
fn offset_for_cursor(heights: &[usize], cursor: usize, height: usize, columns: usize) -> usize {
    if heights.is_empty() {
        return 0;
    }
    let cursor = cursor.min(heights.len() - 1);

    // Walk back from the cursor until half a column is used up.
    let half = height / 2;
    let mut used = 0;
    let mut centred = cursor;
    while centred > 0 {
        let h = heights[centred - 1];
        if used + h > half {
            break;
        }
        used += h;
        centred -= 1;
    }

    // …but never past the point where everything left fits on screen.
    //
    // Filled backwards, one column at a time, rather than dividing by `height * columns`:
    // a column cannot split a row, so three-row items in a four-row column hold one each
    // and waste the rest. Assuming the arithmetic capacity put the offset a row too high
    // and dropped the cursor off the end entirely.
    let mut at = heights.len();
    for _ in 0..columns {
        let before = at;
        let mut used = 0;
        while at > 0 && used + heights[at - 1] <= height {
            used += heights[at - 1];
            at -= 1;
        }
        // A row taller than a whole column still occupies one.
        if at == before && at > 0 {
            at -= 1;
        }
        if at == 0 {
            break;
        }
    }
    let mut offset = centred.min(at);

    // Packing backwards is not always the mirror of packing forwards, so confirm against
    // the real layout rather than trusting the estimate.
    while offset < cursor
        && !column_bounds(heights, offset, height, columns)
            .iter()
            .any(|&(from, to)| cursor >= from && cursor < to)
    {
        offset += 1;
    }
    offset
}

fn draw_run(f: &mut Frame, area: Rect, app: &mut App, t: &Theme) {
    if app.run.is_none() {
        return;
    }
    let height = area.height.max(1) as usize;

    // One column at full width unless the output genuinely overflows, in which case use
    // whatever the width can carry. Heights depend on the column width, so they are
    // measured once to decide and again to lay out.
    let single: usize = app
        .run_rows
        .iter()
        .map(|r| run_row_height(app, r, area.width as usize))
        .sum();
    let columns = if single <= height {
        1
    } else {
        (area.width as usize / MIN_RUN_COLUMN).clamp(1, 3)
    };

    let areas: Vec<Rect> = if columns == 1 {
        vec![area]
    } else {
        Layout::horizontal(vec![Constraint::Ratio(1, columns as u32); columns])
            .split(area)
            .to_vec()
    };
    let col_width = (areas[0].width as usize).saturating_sub(if columns > 1 { 2 } else { 0 });

    let heights: Vec<usize> = app
        .run_rows
        .iter()
        .map(|r| run_row_height(app, r, col_width))
        .collect();

    app.run_offset = offset_for_cursor(&heights, app.run_cursor, height, columns);
    let bounds = column_bounds(&heights, app.run_offset, height, columns);

    for (c, (from, to)) in bounds.iter().copied().enumerate() {
        if from >= to {
            break;
        }
        let column = areas[c];
        let items = run_items(app, t, &app.run_rows[from..to], col_width);

        let mut state = ListState::default();
        // Only the first column carries the cursor; the rest are what comes next.
        if app.run_cursor >= from && app.run_cursor < to {
            state.select(Some(app.run_cursor - from));
        }
        f.render_stateful_widget(
            List::new(items).highlight_style(selection_style(t)),
            column,
            &mut state,
        );
    }
}

/// Build the list items for a slice of run rows.
fn run_items<'a>(app: &'a App, t: &Theme, rows: &[RunRow], width: usize) -> Vec<ListItem<'a>> {
    let Some(run) = app.run.as_ref() else {
        return Vec::new();
    };
    rows.iter()
        .map(|row| match row {
            RunRow::Task { name, depth, open } => {
                let tr = run.tasks.get(name);
                let status = tr.map(|tr| tr.status).unwrap_or(Status::Pending);
                let indent = "  ".repeat(*depth);

                // Only offer a fold glyph where there is something to unfold.
                let has_lines = tr.map(|tr| !tr.lines.is_empty()).unwrap_or(false);
                let glyph = if !has_lines {
                    "  "
                } else if *open {
                    "▾ "
                } else {
                    "▸ "
                };

                let mut spans = vec![
                    Span::raw(indent),
                    Span::styled(glyph, Style::default().fg(t.dim)),
                    Span::styled(
                        format!("{} ", status.glyph()),
                        status_style(status, t).add_modifier(Modifier::BOLD),
                    ),
                    Span::styled(
                        name.clone(),
                        if status == Status::Failed {
                            Style::default()
                                .fg(t.status_failed)
                                .add_modifier(Modifier::BOLD)
                        } else if status == Status::Skipped {
                            Style::default().fg(t.dim)
                        } else {
                            Style::default()
                        },
                    ),
                ];

                let mut tail = String::new();
                if let Some(tr) = tr {
                    // Ticking while the task runs, final once it stops.
                    if let Some(d) = tr.elapsed() {
                        tail.push_str(&format!("  {:>7}", duration(d)));
                    }
                    if !tr.lines.is_empty() && !*open {
                        tail.push_str(&format!("  {} lines", tr.lines.len()));
                    }
                }
                if !tail.is_empty() {
                    spans.push(Span::styled(tail, Style::default().fg(t.dim)));
                }

                ListItem::new(Line::from(spans))
            }

            RunRow::Line { task, index, depth } => {
                let (text, is_command) = run
                    .tasks
                    .get(task)
                    .and_then(|t| t.lines.get(*index))
                    .map(|l| (l.plain.clone(), l.is_command))
                    .unwrap_or_default();

                let indent = "  ".repeat(*depth);
                // The gutter distinguishes go-task's own command echo from the command's
                // actual output.
                let marker = if is_command { "$ " } else { "│ " };
                let prefix = indent.len() + GUTTER + marker.len();
                let room = width.saturating_sub(prefix).max(8);

                let base = if is_command {
                    Style::default().fg(t.alias)
                } else {
                    Style::default()
                };

                // One captured line can become several visual rows; the number and the
                // marker belong only to the first.
                let mut rendered: Vec<Line> = Vec::new();
                for (n, chunk) in wrap(&text, room).into_iter().enumerate() {
                    let mut spans = vec![Span::raw(indent.clone())];
                    if n == 0 {
                        spans.push(Span::styled(
                            format!("{:>width$} ", index + 1, width = GUTTER - 1),
                            Style::default().fg(t.dim),
                        ));
                        spans.push(Span::styled(marker, Style::default().fg(t.dim)));
                    } else {
                        // Continuation: blank gutter, aligned under the text above.
                        spans.push(Span::raw(" ".repeat(GUTTER)));
                        spans.push(Span::styled("  ", Style::default().fg(t.dim)));
                    }

                    // Highlight per chunk, so a match survives being wrapped.
                    match app.search.as_ref().and_then(|q| q.first_match(&chunk)) {
                        Some((start, end)) if end <= chunk.len() => {
                            spans.push(Span::styled(chunk[..start].to_string(), base));
                            spans.push(Span::styled(
                                chunk[start..end].to_string(),
                                Style::default()
                                    .bg(Color::Magenta)
                                    .fg(Color::Black)
                                    .add_modifier(Modifier::BOLD),
                            ));
                            spans.push(Span::styled(chunk[end..].to_string(), base));
                        }
                        _ => spans.push(Span::styled(chunk, base)),
                    }
                    rendered.push(Line::from(spans));
                }

                ListItem::new(rendered)
            }
        })
        .collect()
}

fn draw_run_footer(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    if app.sending_input {
        let mut spans = vec![Span::styled(
            "  input  ",
            Style::default()
                .bg(t.interactive)
                .fg(t.warning_fg)
                .add_modifier(Modifier::BOLD),
        )];
        match app.run.as_ref().and_then(|r| r.pending_prompt()) {
            Some(prompt) => spans.push(Span::styled(
                format!(" {prompt}"),
                Style::default().fg(t.interactive),
            )),
            None => spans.push(Span::styled(
                " keys go to the task",
                Style::default().fg(t.interactive),
            )),
        }
        // The receipt: what has actually gone down the pipe. Without it, "I typed y and
        // nothing happened" cannot be told apart from "y never left the building".
        let sent = app.run.as_ref().map(|r| r.sent.clone()).unwrap_or_default();
        if !sent.is_empty() {
            spans.push(Span::styled("   sent: ", Style::default().fg(t.dim)));
            spans.push(Span::styled(
                sent,
                Style::default()
                    .fg(t.interactive)
                    .add_modifier(Modifier::BOLD),
            ));
        }
        // Typing works either way, but in a buffered run you are doing it blind.
        if app.run.as_ref().is_some_and(|r| !r.interactive) {
            spans.push(Span::styled(
                "   buffered: ⏎ sends a newline, output may lag   ⇧I re-runs visibly",
                Style::default().fg(t.notice),
            ));
        }
        spans.push(Span::styled(
            "   esc to stop typing",
            Style::default().fg(t.dim),
        ));
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }

    // Under `prefixed` a blocked task emits nothing at all, so this is the only warning
    // available — otherwise it reads as an unusually slow build.
    if app.possibly_stuck() {
        let spans = vec![
            Span::styled(
                "  …  ",
                Style::default()
                    .bg(t.warning_bg)
                    .fg(t.warning_fg)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::styled(" no output for a while", Style::default().fg(t.notice)),
            Span::styled(
                "   waiting for input?  i types at it   ⇧I re-runs so you can see it   x stops",
                Style::default().fg(t.dim),
            ),
        ];
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }

    // A task blocked on a question looks identical to a slow one; say which it is.
    if app.awaiting_input() {
        let prompt = app
            .run
            .as_ref()
            .and_then(|r| r.pending_prompt())
            .unwrap_or_default();
        let spans = vec![
            Span::styled(
                "  ?  ",
                Style::default()
                    .bg(t.warning_bg)
                    .fg(t.warning_fg)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::styled(format!(" {prompt}"), Style::default().fg(t.notice)),
            Span::styled("   i to answer   x to stop", Style::default().fg(t.dim)),
        ];
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }

    if draw_confirm(f, area, app, t) {
        return;
    }
    if draw_args_prompt(f, area, app, t) {
        return;
    }
    if app.searching {
        // The prompt says which of the two jobs it is doing: jump to matches, or hide
        // everything that is not one.
        let (label, colour) = if app.filter_matches {
            ("filter: ", Color::Magenta)
        } else {
            ("/", Color::Magenta)
        };
        let mut spans = vec![
            Span::styled(label, Style::default().fg(colour)),
            Span::raw(app.search_input.clone()),
            Span::styled("█", Style::default().fg(colour)),
        ];
        if let Some(err) = &app.search_error {
            spans.push(Span::styled(
                format!("   {err}"),
                Style::default().fg(t.status_failed),
            ));
        } else if app.search.is_some() {
            let tasks = app
                .search_hits
                .iter()
                .map(|h| h.task.as_str())
                .collect::<std::collections::BTreeSet<_>>()
                .len();
            let n = app.search_hits.len();
            spans.push(Span::styled(
                format!(
                    "   {n} match{} in {tasks} task{}",
                    if n == 1 { "" } else { "es" },
                    if tasks == 1 { "" } else { "s" }
                ),
                Style::default().fg(t.dim),
            ));
        }
        spans.push(Span::styled(
            "   ⏎ keep   esc clear",
            Style::default().fg(t.dim),
        ));
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }

    if let Some(msg) = &app.status {
        f.render_widget(
            Paragraph::new(Line::from(Span::styled(
                msg.clone(),
                Style::default().fg(t.notice),
            ))),
            area,
        );
        return;
    }
    let keys = keys::footer(&keys::RUN);
    f.render_widget(
        Paragraph::new(Line::from(Span::styled(keys, Style::default().fg(t.dim)))),
        area,
    );
}

/// Render one frame to plain text off-screen. Backs both the render tests and
/// `--screenshot`, which is how you look at the real thing without a terminal.
pub fn render_headless(app: &mut App, w: u16, h: u16) -> Vec<String> {
    let mut term = ratatui::Terminal::new(ratatui::backend::TestBackend::new(w, h))
        .expect("test backend is infallible");
    term.draw(|f| draw(f, app)).expect("drawing to a buffer");
    let buf = term.backend().buffer().clone();
    (0..h)
        .map(|y| {
            (0..w)
                .map(|x| buf[(x, y)].symbol())
                .collect::<String>()
                .trim_end()
                .to_string()
        })
        .collect()
}

fn draw_header(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    let dir = app
        .root
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_else(|| app.root.display().to_string());

    let shown = app
        .rows
        .iter()
        .filter(|r| app.tree.nodes[r.node].task.is_some())
        .count();

    let mut spans = vec![
        Span::styled(
            "taskui",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ),
        Span::styled(" · ", Style::default().fg(t.dim)),
        Span::raw(dir),
        Span::styled("  group: ", Style::default().fg(t.dim)),
        Span::styled(
            app.mode.label(),
            Style::default().fg(t.mode).add_modifier(Modifier::BOLD),
        ),
    ];

    if app.query.is_empty() {
        spans.push(Span::styled(
            format!("   {} tasks", app.tasks.len()),
            Style::default().fg(t.dim),
        ));
        if app.interactive_next {
            spans.push(Span::styled(
                "   interactive",
                Style::default().fg(t.interactive),
            ));
        }
        if app.force_next {
            spans.push(Span::styled("   force", Style::default().fg(t.notice)));
        }
        // Leaving the run view does not stop the run; say so, or it is easy to forget.
        if let Some(run) = app.run.as_ref().filter(|r| !r.finished()) {
            spans.push(Span::styled(
                format!("   ▶ {} running", run.root),
                Style::default().fg(t.notice),
            ));
        }
    } else {
        spans.push(Span::styled("   filter: ", Style::default().fg(t.dim)));
        spans.push(Span::styled(
            app.query.clone(),
            Style::default().fg(t.search),
        ));
        spans.push(Span::styled(
            format!("   {shown}/{} tasks", app.tasks.len()),
            Style::default().fg(t.dim),
        ));
    }

    f.render_widget(Paragraph::new(Line::from(spans)), area);
}

/// Narrowest column worth splitting into: a task name plus enough description to be worth
/// reading. Below this, one wide column beats two cramped ones.
const MIN_COLUMN: usize = 46;

/// One row of the task tree.
fn tree_item<'a>(app: &'a App, row: &crate::pivot::Row, t: &Theme, width: usize) -> ListItem<'a> {
    // Continuation rows for a wrapped description.
    let mut extra: Vec<Line> = Vec::new();
    let node = &app.tree.nodes[row.node];
    let indent = "  ".repeat(row.depth);

    // A node can be both a group and a task (`backend:migrate`), so the fold
    // glyph and the runnable-ness are decided independently.
    let glyph = if !node.is_group() {
        "  "
    } else if row.open {
        "▾ "
    } else {
        "▸ "
    };

    let mut spans = vec![
        Span::styled(indent, Style::default()),
        Span::styled(glyph, Style::default().fg(t.dim)),
    ];

    let label_style = if node.is_group() {
        Style::default().add_modifier(Modifier::BOLD)
    } else {
        Style::default()
    };
    spans.push(Span::styled(node.label.clone(), label_style));

    let mut used = row.depth * 2 + 2 + node.label.chars().count();

    if node.is_group() {
        let c = format!("  {}", node.count);
        used += c.chars().count();
        spans.push(Span::styled(c, Style::default().fg(t.dim)));
    }

    if let Some(ti) = node.task {
        let task = &app.tasks[ti];
        // How it went last time. A blank column means never run, which is information
        // too — it is not the same as having passed.
        if let Some(o) = app.outcomes.get(&task.name) {
            let (glyph, colour) = if o.ok {
                ("  ✓", t.status_ok)
            } else {
                ("  ✗", t.status_failed)
            };
            spans.push(Span::styled(
                glyph,
                Style::default().fg(colour).add_modifier(Modifier::BOLD),
            ));
            let when = format!(" {}", ago(o.when_unix));
            used += glyph.chars().count() + when.chars().count();
            spans.push(Span::styled(when, Style::default().fg(t.dim)));
        }
        if task.dangerous {
            spans.push(Span::styled(
                "  ⚠",
                Style::default()
                    .fg(t.status_failed)
                    .add_modifier(Modifier::BOLD),
            ));
            used += 3;
        }
        if !task.aliases.is_empty() {
            let a = format!("  ({})", task.aliases.join(", "));
            used += a.chars().count();
            spans.push(Span::styled(a, Style::default().fg(t.alias)));
        }
        // Descriptions wrap into their own column rather than being cut off mid-word —
        // a truncated description is the half that does not tell you anything.
        // Continuation rows are indented to line up under the first.
        // Aligned at column 34 when there is room, tightened when there is not — a
        // three-column layout gives each description about half its column, and starting
        // it at 34 would leave it nothing.
        let desc_col = (width / 2).clamp(used + 2, 34.max(used + 2));
        if !task.desc.is_empty() && desc_col + 12 < width {
            let room = width - desc_col - 1;
            let mut chunks = wrap(&task.desc, room).into_iter();
            if let Some(first) = chunks.next() {
                spans.push(Span::raw(" ".repeat(desc_col.saturating_sub(used))));
                spans.push(Span::styled(first, Style::default().fg(t.dim)));
            }
            for chunk in chunks {
                extra.push(Line::from(vec![
                    Span::raw(" ".repeat(desc_col)),
                    Span::styled(chunk, Style::default().fg(t.dim)),
                ]));
            }
        }
    }

    let mut lines = vec![Line::from(spans)];
    lines.append(&mut extra);
    ListItem::new(lines)
}

/// How many terminal rows a picker row takes once its description wraps.
///
/// Measured by building the row, so the height can never disagree with what is drawn.
fn tree_row_height(app: &App, row: &crate::pivot::Row, t: &Theme, width: usize) -> usize {
    tree_item(app, row, t, width).height()
}

fn draw_tree(f: &mut Frame, area: Rect, app: &mut App, t: &Theme) {
    let height = area.height.max(1) as usize;

    let single: usize = app
        .rows
        .iter()
        .map(|r| tree_row_height(app, r, t, area.width as usize))
        .sum();
    let columns = if single <= height {
        1
    } else {
        (area.width as usize / MIN_COLUMN).clamp(1, 3)
    };

    let areas: Vec<Rect> = if columns == 1 {
        vec![area]
    } else {
        // `split` rather than `areas::<N>`: the const-generic form demands exactly N rects
        // and panics otherwise, which is how a two-column layout used to crash.
        Layout::horizontal(vec![Constraint::Ratio(1, columns as u32); columns])
            .split(area)
            .to_vec()
    };
    // Reserve a gutter so one column's text does not run into the next.
    let width = (areas[0].width as usize).saturating_sub(if columns > 1 { 2 } else { 0 });

    let heights: Vec<usize> = app
        .rows
        .iter()
        .map(|r| tree_row_height(app, r, t, width))
        .collect();

    app.offset = offset_for_cursor(&heights, app.cursor, height, columns);
    let bounds = column_bounds(&heights, app.offset, height, columns);

    for (c, (from, to)) in bounds.iter().copied().enumerate() {
        if from >= to {
            break;
        }
        let items: Vec<ListItem> = app.rows[from..to]
            .iter()
            .map(|row| tree_item(app, row, t, width))
            .collect();

        let mut state = ListState::default();
        // The cursor lives in the first column; the rest are a look-ahead.
        if app.cursor >= from && app.cursor < to {
            state.select(Some(app.cursor - from));
        }
        f.render_stateful_widget(
            List::new(items).highlight_style(selection_style(t)),
            areas[c],
            &mut state,
        );
    }
}

/// The confirmation bar. Takes precedence over every other footer: nothing else is
/// happening until it is answered.
fn draw_confirm(f: &mut Frame, area: Rect, app: &App, t: &Theme) -> bool {
    let Some((name, args)) = &app.confirm else {
        return false;
    };
    let cmd = if args.is_empty() {
        format!("task {name}")
    } else {
        format!("task {name} {}", args.join(" "))
    };
    let spans = vec![
        Span::styled(
            "  ⚠  ",
            Style::default()
                .bg(t.confirm_bg)
                .fg(t.confirm_fg)
                .add_modifier(Modifier::BOLD),
        ),
        Span::styled(" run ", Style::default().fg(t.status_failed)),
        Span::styled(
            cmd,
            Style::default()
                .fg(t.status_failed)
                .add_modifier(Modifier::BOLD),
        ),
        Span::styled(
            match app.confirm_reason {
                ConfirmReason::TouchesProduction => "  —  this one touches production.  ",
                ConfirmReason::WouldStopRunning => "  —  this stops the run already going.  ",
            },
            Style::default().fg(t.status_failed),
        ),
        Span::styled(
            "y",
            Style::default()
                .fg(t.status_failed)
                .add_modifier(Modifier::BOLD),
        ),
        Span::styled(" to run, anything else cancels", Style::default().fg(t.dim)),
    ];
    f.render_widget(Paragraph::new(Line::from(spans)), area);
    true
}

/// The args prompt, shared by both screens. Returns true if it drew.
fn draw_args_prompt(f: &mut Frame, area: Rect, app: &App, t: &Theme) -> bool {
    if !app.entering_args {
        return false;
    }
    let target = app.args_target.clone().unwrap_or_default();
    let before: String = app.args_input.chars().take(app.args_cursor).collect();
    let after: String = app.args_input.chars().skip(app.args_cursor).collect();
    let mut spans = vec![
        Span::styled(format!("task {target} "), Style::default().fg(t.accent)),
        Span::raw(before),
        Span::styled("█", Style::default().fg(t.accent)),
        Span::raw(after),
    ];
    match app.args_hint() {
        // A hint, not a default: the descriptions trail off into prose often enough that
        // pre-filling would hand you a subtly wrong command.
        Some(hint) => spans.push(Span::styled(
            format!("   e.g. {hint}"),
            Style::default().fg(t.dim),
        )),
        None => spans.push(Span::styled(
            "   ⏎ run   esc cancel",
            Style::default().fg(t.dim),
        )),
    }
    f.render_widget(Paragraph::new(Line::from(spans)), area);
    true
}

fn draw_footer(f: &mut Frame, area: Rect, app: &App, t: &Theme) {
    if draw_confirm(f, area, app, t) {
        return;
    }
    if app.jumping {
        let mut spans = vec![
            Span::styled("jump: ", Style::default().fg(t.accent)),
            Span::raw(app.jump_query.clone()),
            Span::styled("█", Style::default().fg(t.accent)),
        ];
        if !app.jump_query.is_empty() {
            spans.push(Span::styled(
                if app.jump_matches.is_empty() {
                    "   no match".to_string()
                } else {
                    format!("   {}/{}", app.jump_idx + 1, app.jump_matches.len())
                },
                Style::default().fg(t.dim),
            ));
        }
        spans.push(Span::styled(
            "   ⇥ next   ⏎ stay   esc go back",
            Style::default().fg(t.dim),
        ));
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }
    if draw_args_prompt(f, area, app, t) {
        return;
    }
    if app.filtering {
        let spans = vec![
            Span::styled("/", Style::default().fg(t.search)),
            Span::raw(app.query.clone()),
            Span::styled("█", Style::default().fg(t.search)),
            Span::styled("   ⏎ accept   esc clear", Style::default().fg(t.dim)),
        ];
        f.render_widget(Paragraph::new(Line::from(spans)), area);
        return;
    }

    if let Some(msg) = &app.status {
        f.render_widget(
            Paragraph::new(Line::from(Span::styled(
                msg.clone(),
                Style::default().fg(t.notice),
            ))),
            area,
        );
        return;
    }

    let other = app.mode.toggled();
    let hint = match other {
        Mode::Domain => "p group by domain",
        Mode::Verb => "p group by verb",
    };
    let keys = format!("{hint}   {}", keys::footer(&keys::PICKER));
    f.render_widget(
        Paragraph::new(Line::from(Span::styled(keys, Style::default().fg(t.dim)))),
        area,
    );
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::app::App;
    use crate::pivot::fixture;

    fn render_to_lines(app: &mut App, w: u16, h: u16) -> Vec<String> {
        render_headless(app, w, h)
    }

    fn sample() -> App {
        let mut tasks = fixture(&[
            "all",
            "lint",
            "app:lint",
            "backend:lint",
            "backend:migrate",
            "backend:migrate:prod",
        ]);
        tasks[5].dangerous = true;
        tasks[1].desc = "Lint all source code".into();
        App::new(tasks, std::path::PathBuf::from("/tmp/atlas"))
    }

    #[test]
    fn header_names_the_active_pivot() {
        let mut app = sample();
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[0].contains("taskui"), "{:?}", lines[0]);
        assert!(lines[0].contains("atlas"), "{:?}", lines[0]);
        assert!(lines[0].contains("group: domain"), "{:?}", lines[0]);

        app.toggle_mode();
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[0].contains("group: verb"), "{:?}", lines[0]);
    }

    /// Collapsed groups show a closed glyph and a subtree count; opening flips it.
    #[test]
    fn groups_render_fold_glyph_and_count() {
        let mut app = sample();
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[1].contains("▸ (root)"), "{:?}", lines[1]);
        assert!(
            lines[1].contains("2"),
            "root holds all + lint: {:?}",
            lines[1]
        );

        app.set_fold_all(true);
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[1].contains("▾ (root)"), "{:?}", lines[1]);
        assert!(lines[2].contains("all"), "{:?}", lines[2]);
    }

    #[test]
    fn tasks_show_descriptions_and_danger_marker() {
        let mut app = sample();
        app.set_fold_all(true);
        let lines = render_to_lines(&mut app, 70, 12);
        let lint = lines
            .iter()
            .find(|l| l.contains("lint") && l.contains("Lint all"))
            .unwrap();
        assert!(lint.contains("Lint all source code"), "{lint:?}");
        let prod = lines.iter().find(|l| l.contains("prod")).unwrap();
        assert!(prod.contains("⚠"), "production tasks are marked: {prod:?}");
    }

    /// Columns appear only when the content does not fit, and never more than the width
    /// can carry. Exercised through rendering rather than arithmetic — the crash this
    /// replaces was in the layout call, which a pure calculation test never reached.
    #[test]
    fn renders_at_one_two_and_three_columns() {
        let mut app = many_tasks(80);
        for (w, h) in [(60, 30), (100, 12), (150, 12)] {
            let lines = render_to_lines(&mut app, w, h);
            assert!(
                lines.iter().any(|l| l.contains("task0")),
                "{w}x{h} rendered nothing"
            );
        }
    }

    fn many_tasks(n: usize) -> App {
        let names: Vec<String> = (0..n).map(|i| format!("task{i:03}")).collect();
        let refs: Vec<&str> = names.iter().map(|s| s.as_str()).collect();
        let mut app = App::new(fixture(&refs), std::path::PathBuf::from("/tmp/repo"));
        app.set_fold_all(true);
        app
    }

    /// With columns the list pages rather than scrolling a line at a time, and the cursor
    /// must stay on screen when it moves past the end of the page.
    #[test]
    fn the_window_pages_to_follow_the_cursor() {
        let mut app = many_tasks(60);
        let (w, h) = (150, 12);
        // Body is height minus the header and footer rows.
        let body = h - 2;
        let capacity = body * 3;

        let first = render_to_lines(&mut app, w, h);
        assert!(
            first[1].contains("(root)"),
            "starts at the top: {:?}",
            first[1]
        );

        app.cursor = capacity as usize + 5;
        let paged = render_to_lines(&mut app, w, h);
        assert!(
            !paged[1].contains("(root)"),
            "the window moved to follow the cursor: {:?}",
            paged[1]
        );
        assert!(
            paged
                .iter()
                .any(|l| l.contains(&format!("task{:03}", capacity + 4))),
            "the cursor's row is on screen"
        );

        // …and back.
        app.cursor = 0;
        let home = render_to_lines(&mut app, w, h);
        assert!(home[1].contains("(root)"), "{:?}", home[1]);
    }

    /// The crash was reached by typing into the filter: narrowing the list changes the
    /// column count underneath the layout.
    #[test]
    fn filtering_across_a_column_count_change_does_not_panic() {
        let mut app = many_tasks(80);
        let (w, h) = (150, 12);

        // Type a filter one character at a time, rendering after each — the list shrinks
        // through every column count on the way down.
        for c in "task01".chars() {
            app.push_query(c);
            let lines = render_to_lines(&mut app, w, h);
            assert!(lines[0].contains("filter"), "{:?}", lines[0]);
        }
        assert!(app.rows.len() < 20, "narrowed to {} rows", app.rows.len());
    }

    /// Columns fill by accumulated height, because a wrapped output line is several rows.
    #[test]
    fn run_columns_fill_by_height_not_by_count() {
        // Second row is a wrapped line worth three terminal rows.
        let heights = [1, 3, 1, 1, 1, 1, 1, 1];
        let bounds = column_bounds(&heights, 0, 5, 2);
        assert_eq!(
            bounds[0],
            (0, 3),
            "1 + 3 fills five rows; the next would overflow"
        );
        assert_eq!(bounds[1], (3, 8), "the rest continues in the second column");
    }

    /// A row taller than the whole column still has to be shown somewhere.
    #[test]
    fn an_oversized_row_still_gets_a_column() {
        let bounds = column_bounds(&[9], 0, 4, 2);
        assert_eq!(bounds[0], (0, 1));
    }

    /// The cursor is kept centred rather than pinned to the bottom edge, so you can read
    /// around the row you are on.
    #[test]
    fn the_cursor_is_kept_centred() {
        let heights = vec![1; 100];
        // Deep in the list: roughly half a column of context above.
        let offset = offset_for_cursor(&heights, 50, 10, 1);
        assert_eq!(offset, 45, "five rows above, four below");

        // Near the top there is nothing to scroll to, so it stays put.
        assert_eq!(offset_for_cursor(&heights, 2, 10, 1), 0);

        // Near the bottom it stops rather than leaving blank space below the last row.
        assert_eq!(offset_for_cursor(&heights, 99, 10, 1), 90);
    }

    /// A list that fits never scrolls at all.
    #[test]
    fn a_short_list_does_not_scroll() {
        let heights = vec![1; 6];
        for cursor in 0..6 {
            assert_eq!(
                offset_for_cursor(&heights, cursor, 10, 1),
                0,
                "cursor {cursor}"
            );
        }
    }

    /// With columns, "everything left fits" means across all of them.
    #[test]
    fn the_end_clamp_accounts_for_every_column() {
        let heights = vec![1; 40];
        // 3 columns of 10 hold the last 30 rows, so it stops at 10.
        assert_eq!(offset_for_cursor(&heights, 39, 10, 3), 10);
    }

    /// Render every screen and every prompt across a spread of terminal shapes.
    ///
    /// Two crashes this session were in layout code that the other tests never reached,
    /// because they all happened to render at one comfortable size. Sizes here are
    /// deliberately awkward: one row of body, a terminal narrower than a task name, and
    /// widths either side of every column threshold.
    #[test]
    fn every_screen_renders_at_every_awkward_size() {
        const SIZES: &[(u16, u16)] = &[
            (20, 3), // barely a terminal
            (40, 4),
            (60, 3),   // one body row
            (80, 24),  // ordinary
            (90, 5),   // wide and very short
            (100, 12), // two columns
            (150, 12), // three columns
            (200, 8),
            (300, 60), // absurdly wide
        ];

        let mut app = many_tasks(60);
        app.set_fold_all(true);
        app.detail_of = Some("task000".into());
        app.detail = crate::graph::Detail {
            summary: vec!["A description".into()],
            requires: vec!["NAME".into()],
            dependencies: vec!["task001".into()],
            commands: vec!["Task: task001".into(), "echo hello".into()],
        };
        app.run = Some(crate::run::Run::detached(
            "task000",
            crate::run::Run::graph_from(&[("task000", &["task001"])]),
        ));
        if let Some(run) = app.run.as_mut() {
            run.feed(
                "task001",
                "a line of output long enough that it has to wrap somewhere sensible",
            );
            run.feed("task001", "error: boom");
        }
        app.rebuild_run_rows();

        for &(w, h) in SIZES {
            for screen in [
                Screen::Picker,
                Screen::Run,
                Screen::History,
                Screen::Help,
                Screen::Detail,
            ] {
                app.screen = screen;
                let lines = render_to_lines(&mut app, w, h);
                assert_eq!(lines.len(), h as usize, "{screen:?} at {w}x{h}");

                // …and again with each prompt open, since prompts take over the footer.
                app.entering_args = true;
                app.args_target = Some("task000".into());
                render_to_lines(&mut app, w, h);
                app.entering_args = false;

                app.searching = true;
                render_to_lines(&mut app, w, h);
                app.searching = false;

                app.jumping = true;
                render_to_lines(&mut app, w, h);
                app.jumping = false;

                app.sending_input = true;
                render_to_lines(&mut app, w, h);
                app.sending_input = false;

                app.confirm = Some(("deploy:backend".into(), vec!["--force".into()]));
                render_to_lines(&mut app, w, h);
                app.confirm = None;
            }
        }
    }

    /// Whatever the cursor is on has to be visible. Scrolling far enough was hiding it.
    #[test]
    fn the_cursor_row_is_always_on_screen() {
        for mode in [crate::pivot::Mode::Domain, crate::pivot::Mode::Verb] {
            let mut app = many_tasks(80);
            app.mode = mode;
            app.set_fold_all(true);

            for (w, h) in [(80, 10), (100, 12), (150, 12), (150, 24)] {
                for cursor in 0..app.rows.len() {
                    app.cursor = cursor;
                    let label = app.tree.nodes[app.rows[cursor].node].label.clone();
                    let lines = render_to_lines(&mut app, w, h);
                    assert!(
                        lines.iter().any(|l| l.contains(&label)),
                        "{mode:?} {w}x{h}: cursor {cursor} is on {label:?}, which is not on screen"
                    );
                }
            }
        }
    }

    /// The selection is only drawn by the column that contains the cursor, so the cursor
    /// has to fall inside one of the column ranges. If it falls past the last one, the row
    /// is off screen and nothing is highlighted at all.
    #[test]
    fn the_cursor_falls_inside_a_column() {
        for heights in [vec![1; 200], vec![3; 60], {
            // Mixed: wrapped descriptions make some rows much taller than others.
            let mut h = vec![1; 120];
            for (i, v) in h.iter_mut().enumerate() {
                if i % 7 == 0 {
                    *v = 4;
                }
            }
            h
        }] {
            for &columns in &[1usize, 2, 3] {
                for &height in &[4usize, 10, 22] {
                    for cursor in 0..heights.len() {
                        let offset = offset_for_cursor(&heights, cursor, height, columns);
                        let bounds = column_bounds(&heights, offset, height, columns);
                        assert!(
                            bounds.iter().any(|&(from, to)| cursor >= from && cursor < to),
                            "cursor {cursor} outside {bounds:?} (offset {offset}, {columns} cols of {height})"
                        );
                    }
                }
            }
        }
    }

    /// Cursors out of step with the rows must not take the renderer down either.
    #[test]
    fn an_out_of_range_cursor_does_not_panic() {
        let mut app = many_tasks(10);
        app.set_fold_all(true);
        app.cursor = 9_999;
        render_to_lines(&mut app, 80, 10);

        app.screen = Screen::History;
        app.history_cursor = 9_999;
        render_to_lines(&mut app, 80, 10);
    }

    #[test]
    fn durations_read_at_a_glance() {
        use std::time::Duration as D;
        assert_eq!(duration(D::from_millis(4)), "4ms");
        assert_eq!(duration(D::from_millis(940)), "940ms");
        assert_eq!(duration(D::from_millis(1500)), "1.5s");
        assert_eq!(duration(D::from_secs(59)), "59.0s");
        assert_eq!(duration(D::from_secs(134)), "2m14s");
        assert_eq!(duration(D::from_secs(3600)), "60m00s");
    }

    /// Descriptions wrap into their column instead of being cut off mid-word. They used
    /// to be suppressed in a narrow column because a truncated description is noise —
    /// wrapping removes the reason for that.
    #[test]
    fn descriptions_wrap_rather_than_truncate() {
        let mut tasks = fixture(&["alpha"]);
        tasks[0].desc = "A description long enough that it cannot possibly fit on one line".into();
        let mut app = App::new(tasks, std::path::PathBuf::from("/tmp/repo"));
        app.set_fold_all(true);

        let narrow = render_to_lines(&mut app, 56, 10).join("\n");
        assert!(narrow.contains("alpha"));
        assert!(narrow.contains("A description"), "shown: {narrow:?}");
        assert!(
            narrow.contains("one line"),
            "and the end is reachable rather than cut: {narrow:?}"
        );
    }

    /// A wrapped description makes its row taller, and the layout has to know that.
    #[test]
    fn a_wrapped_description_makes_its_row_taller() {
        let mut tasks = fixture(&["alpha"]);
        tasks[0].desc = "Short".into();
        let app_short = App::new(tasks.clone(), std::path::PathBuf::from("/tmp/repo"));

        tasks[0].desc = "A description long enough that it cannot possibly fit on one line".into();
        let app_long = App::new(tasks, std::path::PathBuf::from("/tmp/repo"));

        let row = |app: &App| {
            let r = app
                .rows
                .iter()
                .find(|r| app.tree.nodes[r.node].task.is_some());
            tree_row_height(app, r.unwrap(), &Theme::default(), 56)
        };
        let (mut s_app, mut l_app) = (app_short, app_long);
        s_app.set_fold_all(true);
        l_app.set_fold_all(true);
        assert_eq!(row(&s_app), 1);
        assert!(row(&l_app) > 1, "taller: {}", row(&l_app));
    }

    #[test]
    fn footer_offers_the_other_pivot() {
        let mut app = sample();
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[11].contains("p group by verb"), "{:?}", lines[11]);
        app.toggle_mode();
        let lines = render_to_lines(&mut app, 70, 12);
        assert!(lines[11].contains("p group by domain"), "{:?}", lines[11]);
    }
}
