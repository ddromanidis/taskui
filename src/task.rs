//! Task discovery: shell out to `task --list-all` and parse the result.
//!
//! The obvious call is `--list-all --json`, which additionally carries aliases as an
//! array and each task's source location. It is also, measured on a repo with seven
//! `includes:` and twenty tasks declaring `sources:`, **fifty-six times slower** — 4.28s
//! against 0.076s. The `--json` form computes `up_to_date` for every task, which means
//! fingerprinting every `sources:` glob, which on a Rust workspace means walking
//! `target/`. Almost all of that 4.28s was system time, not parsing.
//!
//! Four seconds before the first frame is not a price worth paying for a source location
//! nothing reads yet, so this parses the text form instead. If the file pivot or
//! jump-to-definition ever land, they should fetch the JSON on a background thread and
//! fill it in — not block startup on it.
//!
//! Neither form carries dependencies, so this is only the static half of the picture; the
//! execution graph comes from [`crate::graph`].

use anyhow::{bail, Context, Result};
use std::path::Path;
use std::process::Command;

#[derive(Debug, Clone)]
pub struct Task {
    /// Full colon path, e.g. `backend:migrate:down`.
    pub name: String,
    pub desc: String,
    pub aliases: Vec<String>,
    /// Heuristic: does the description suggest this touches production or destroys data?
    pub dangerous: bool,
}

impl Task {
    /// Path segments of the name: `backend:migrate:down` -> ["backend", "migrate", "down"].
    pub fn segments(&self) -> Vec<&str> {
        self.name.split(':').collect()
    }

    /// The last segment — the "verb" the task pivot groups on.
    pub fn verb(&self) -> &str {
        self.name.rsplit(':').next().unwrap_or(&self.name)
    }

    /// Usage hint mined from the description, which by convention spells out an example:
    /// "Scaffold a migration and register it (NAME=add_x)" or "task backend:test -- -p
    /// ingest for one crate".
    ///
    /// Shown beside the args prompt rather than pre-filled into it. The descriptions trail
    /// off into prose often enough — "…-- -p ingest for one crate" — that pre-filling
    /// would hand you a command that is wrong in a way you might not notice before
    /// pressing enter.
    pub fn args_hint(&self) -> Option<String> {
        // Descriptions are written from inside the file that defines the task, so an
        // included one often spells its example with the bare name: `site/Taskfile.yml`
        // says "task new -- …" for what the root calls `site:new`. Try both.
        let mut needles = vec![format!("task {} ", self.name)];
        if self.verb() != self.name {
            needles.push(format!("task {} ", self.verb()));
        }
        for needle in &needles {
            let Some(at) = self.desc.find(needle.as_str()) else {
                continue;
            };
            let hint = trim_prose(&self.desc[at + needle.len()..]);
            if !hint.is_empty() {
                return Some(hint);
            }
        }
        // Bare `NAME=value` conventions, e.g. "(NAME=add_x)" or "(WORD=адрес)".
        let open = self.desc.find('(')?;
        let close = self.desc[open..].find(')')? + open;
        let inner = self.desc[open + 1..close].trim();
        let looks_like_assignment = inner.split_whitespace().next().is_some_and(|w| {
            w.contains('=') && w.chars().next().is_some_and(|c| c.is_ascii_uppercase())
        });
        looks_like_assignment.then(|| inner.to_string())
    }
}

/// The `KEY=` prefixes implied by a hint like `NAME=backend` or `WORD=адрес`.
///
/// A fallback for tasks that take a variable but do not declare `requires:`. Only the key
/// is kept — pre-filling the example *value* would be handing you someone else's argument.
pub fn keys_in_hint(hint: &str) -> Vec<String> {
    hint.split_whitespace()
        .filter_map(|w| w.split_once('='))
        .map(|(k, _)| k)
        .filter(|k| {
            !k.is_empty()
                && k.chars()
                    .all(|c| c.is_ascii_uppercase() || c == '_' || c.is_ascii_digit())
        })
        .map(|k| k.to_string())
        .collect()
}

/// Cut a mined hint back to the part that is plausibly an argument.
///
/// Descriptions run on: "-- infra:deploy:plan`.", "NAME=backend (the BRANCH survives)".
/// The parenthetical and the trailing punctuation are commentary, not arguments.
fn trim_prose(rest: &str) -> String {
    let mut end = rest.len();
    for stop in [")", " (", ";", ", "] {
        if let Some(at) = rest.find(stop) {
            end = end.min(at);
        }
    }
    rest[..end]
        .trim()
        .trim_end_matches(['.', ',', '`', ' '])
        .to_string()
}

/// Split an args line the way a shell would, so quoted arguments survive.
///
/// `site:new -- "My Post Title"` has to reach go-task as one argument, not three.
pub fn split_args(input: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut current = String::new();
    let mut quote: Option<char> = None;
    let mut started = false;
    let mut chars = input.chars().peekable();

    while let Some(c) = chars.next() {
        match c {
            '\\' => {
                if let Some(next) = chars.next() {
                    current.push(next);
                    started = true;
                }
            }
            '\'' | '"' => match quote {
                Some(q) if q == c => quote = None,
                Some(_) => {
                    current.push(c);
                    started = true;
                }
                // An empty quoted string is still an argument.
                None => {
                    quote = Some(c);
                    started = true;
                }
            },
            c if c.is_whitespace() && quote.is_none() => {
                if started {
                    out.push(std::mem::take(&mut current));
                    started = false;
                }
            }
            c => {
                current.push(c);
                started = true;
            }
        }
    }
    if started {
        out.push(current);
    }
    out
}

/// Name of the opt-in file listing tasks that must not be run by accident.
pub const DANGER_FILE: &str = ".taskui-danger";

/// Match a task name against a pattern supporting `*`.
///
/// Deliberately not a full glob: task names are colon paths, and `deploy:*` plus exact
/// names covers every real case without pulling in a dependency whose semantics would
/// then need explaining.
pub fn glob_match(pattern: &str, name: &str) -> bool {
    let mut parts = pattern.split('*');
    let Some(first) = parts.next() else {
        return false;
    };
    if !name.starts_with(first) {
        return false;
    }
    let mut rest = &name[first.len()..];
    let mut last: Option<&str> = None;
    for part in parts {
        last = Some(part);
        if part.is_empty() {
            continue;
        }
        match rest.find(part) {
            Some(at) => rest = &rest[at + part.len()..],
            None => return false,
        }
    }
    // A trailing `*` swallows whatever is left; otherwise the pattern must reach the end.
    match last {
        None => rest.is_empty(),
        Some("") => true,
        Some(tail) => name.ends_with(tail),
    }
}

/// Read `.taskui-danger`: one pattern per line, `#` comments, blanks ignored.
///
/// Its presence switches off the description heuristic entirely. A guess and a
/// declaration disagreeing about which tasks are dangerous is worse than either alone —
/// once you have written the list down, that list is the answer.
pub fn danger_patterns(dir: &Path) -> Vec<String> {
    let Ok(text) = std::fs::read_to_string(dir.join(DANGER_FILE)) else {
        return Vec::new();
    };
    text.lines()
        .map(|l| l.split('#').next().unwrap_or("").trim())
        .filter(|l| !l.is_empty())
        .map(|l| l.to_string())
        .collect()
}

/// Words that mark a task as one you should not fire off from a fuzzy filter by accident.
///
/// This is a heuristic over descriptions and it will both over- and under-match. It is a
/// stopgap: the real fix is an explicit marker in the Taskfile, which we can honour later
/// without changing anything else here.
const DANGER_WORDS: [&str; 6] = [
    "production",
    "prod database",
    "applies!",
    "wipe",
    "destroy",
    "claims ",
];

fn looks_dangerous(name: &str, desc: &str) -> bool {
    let d = desc.to_lowercase();
    if DANGER_WORDS.iter().any(|w| d.contains(w)) {
        return true;
    }
    // `backend:migrate:prod`, `backend:promo:prod`, `deploy:*`
    name.ends_with(":prod") || name.starts_with("deploy:") || name == "deploy"
}

/// One line of `task --list-all`:
///
/// ```text
/// * build:      Build all components                    (aliases: b)
/// ```
fn parse_entry(line: &str) -> Option<Task> {
    let rest = line.strip_prefix("* ")?;
    // Names contain colons, so split at the first whitespace rather than the first colon
    // — `backend:migrate:down:` is one name, not three.
    let (name, tail) = match rest.find(char::is_whitespace) {
        Some(at) => (&rest[..at], &rest[at..]),
        None => (rest, ""),
    };
    let name = name.trim_end_matches(':');
    if name.is_empty() {
        return None;
    }

    let mut desc = tail.trim().to_string();
    let mut aliases = Vec::new();
    // Parsed off the end rather than searched for, so a description that happens to
    // mention the word does not get eaten.
    if desc.ends_with(')') {
        if let Some(at) = desc.rfind("(aliases: ") {
            aliases = desc[at + "(aliases: ".len()..desc.len() - 1]
                .split(',')
                .map(|a| a.trim().to_string())
                .filter(|a| !a.is_empty())
                .collect();
            desc = desc[..at].trim().to_string();
        }
    }

    Some(Task {
        dangerous: looks_dangerous(name, &desc),
        name: name.to_string(),
        desc,
        aliases,
    })
}

/// Run `task --list-all` in `dir` and return the tasks, minus the `*:default` entries —
/// in a UI where a namespace is itself a selectable row, a task whose only job is "show
/// available tasks" is noise.
pub fn discover(dir: &Path) -> Result<Vec<Task>> {
    let declared = danger_patterns(dir);
    let out = Command::new("task")
        .arg("--list-all")
        .current_dir(dir)
        .output()
        .context("failed to run `task` — is go-task installed and on PATH?")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        bail!(
            "`task --list-all` failed in {}: {}",
            dir.display(),
            stderr.trim()
        );
    }

    let text = String::from_utf8_lossy(&out.stdout);
    let mut tasks: Vec<Task> = text
        .lines()
        .filter_map(parse_entry)
        .filter(|t| t.name != "default" && !t.name.ends_with(":default"))
        .map(|mut t| {
            if !declared.is_empty() {
                t.dangerous = declared.iter().any(|p| glob_match(p, &t.name));
            }
            t
        })
        .collect();

    tasks.sort_by(|a, b| a.name.cmp(&b.name));
    Ok(tasks)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_a_plain_entry() {
        let t =
            parse_entry("* all:                           Everything: format, lint, test, build")
                .unwrap();
        assert_eq!(t.name, "all");
        assert_eq!(t.desc, "Everything: format, lint, test, build");
        assert!(t.aliases.is_empty());
    }

    #[test]
    fn parses_aliases_off_the_end() {
        let t = parse_entry("* build:      Build all components        (aliases: b)").unwrap();
        assert_eq!(t.name, "build");
        assert_eq!(t.desc, "Build all components");
        assert_eq!(t.aliases, ["b"]);
    }

    #[test]
    fn parses_namespaced_names() {
        let t =
            parse_entry("* backend:migrate:down:   Roll back the most recent migration").unwrap();
        assert_eq!(t.name, "backend:migrate:down");
        assert_eq!(t.desc, "Roll back the most recent migration");
    }

    /// A description ending in a parenthesis is not an alias list.
    #[test]
    fn a_parenthesised_description_is_not_eaten() {
        let t = parse_entry("* setup:   Install tools and dependencies (safe to re-run)").unwrap();
        assert_eq!(t.desc, "Install tools and dependencies (safe to re-run)");
        assert!(t.aliases.is_empty());
    }

    /// `--list-all` includes tasks with no description at all.
    #[test]
    fn handles_a_missing_description() {
        let t = parse_entry("* sec:secrets:dir:").unwrap();
        assert_eq!(t.name, "sec:secrets:dir");
        assert_eq!(t.desc, "");
    }

    #[test]
    fn ignores_the_header_and_blank_lines() {
        assert!(parse_entry("task: Available tasks for this project:").is_none());
        assert!(parse_entry("").is_none());
    }

    fn task_with(name: &str, desc: &str) -> Task {
        Task {
            name: name.into(),
            desc: desc.into(),
            aliases: Vec::new(),
            dangerous: false,
        }
    }

    #[test]
    fn mines_a_usage_hint_from_the_description() {
        let t = task_with(
            "backend:test",
            "Run Rust workspace tests (task backend:test -- -p ingest for one crate)",
        );
        assert_eq!(t.args_hint().as_deref(), Some("-- -p ingest for one crate"));
    }

    /// Included tasks describe themselves by their local name.
    #[test]
    fn mines_a_hint_written_with_the_bare_name() {
        let t = task_with(
            "site:new",
            r#"New post — usage: task new -- "My Post Title" (title is slugified)"#,
        );
        assert_eq!(t.args_hint().as_deref(), Some(r#"-- "My Post Title""#));
    }

    /// The commentary after the arguments is not part of them.
    #[test]
    fn trims_trailing_prose_from_a_hint() {
        let t = task_with(
            "wt:rm",
            "Remove an agent's worktree: task wt:rm NAME=backend (the BRANCH survives)",
        );
        assert_eq!(t.args_hint().as_deref(), Some("NAME=backend"));

        let t = task_with(
            "deploy:local",
            "Full deploy. Pass a target with `task deploy:local -- infra:deploy:plan`.",
        );
        assert_eq!(t.args_hint().as_deref(), Some("-- infra:deploy:plan"));

        let t = task_with(
            "dev:run",
            "Run the backend once: task dev:run -- <args>, default `serve`",
        );
        assert_eq!(t.args_hint().as_deref(), Some("-- <args>"));
    }

    #[test]
    fn mines_bare_assignment_conventions() {
        let t = task_with(
            "backend:gen:migration",
            "Scaffold a migration and register it (NAME=add_x)",
        );
        assert_eq!(t.args_hint().as_deref(), Some("NAME=add_x"));
    }

    /// A parenthetical that is just prose is not a usage hint.
    #[test]
    fn does_not_invent_a_hint_from_ordinary_prose() {
        let t = task_with("setup", "Install tools and dependencies (safe to re-run)");
        assert_eq!(t.args_hint(), None);
    }

    /// A hint of the `KEY=value` shape tells us the key even when the task does not
    /// declare `requires:`.
    #[test]
    fn extracts_keys_from_an_assignment_hint() {
        assert_eq!(keys_in_hint("NAME=backend"), ["NAME"]);
        assert_eq!(keys_in_hint("WORD=адрес"), ["WORD"]);
        assert_eq!(keys_in_hint("A=1 B=2"), ["A", "B"]);
    }

    /// `--` style arguments carry no key to pre-fill.
    #[test]
    fn extracts_no_keys_from_a_dash_dash_hint() {
        assert!(keys_in_hint(r#"-- "My Post Title""#).is_empty());
        assert!(keys_in_hint("-- -p ingest").is_empty());
    }

    /// Quoted arguments have to reach go-task intact.
    #[test]
    fn splits_args_like_a_shell() {
        assert_eq!(
            split_args("-- convert report.pdf"),
            ["--", "convert", "report.pdf"]
        );
        assert_eq!(split_args(r#"-- "My Post Title""#), ["--", "My Post Title"]);
        assert_eq!(split_args("NAME=backend"), ["NAME=backend"]);
        assert_eq!(split_args("  spaced   out  "), ["spaced", "out"]);
        assert_eq!(split_args(""), [] as [String; 0]);
    }

    #[test]
    fn quotes_inside_words_and_escapes_survive() {
        assert_eq!(split_args(r#"MSG="hello world""#), ["MSG=hello world"]);
        assert_eq!(split_args(r"path\ with\ spaces"), ["path with spaces"]);
        // An empty quoted string is a real argument, and `--` is one too.
        assert_eq!(split_args(r#"-- '' empty"#), ["--", "", "empty"]);
    }

    #[test]
    fn globs_match_colon_paths() {
        assert!(glob_match("deploy:*", "deploy:backend"));
        assert!(glob_match("deploy:*", "deploy:logs:archive"));
        assert!(!glob_match("deploy:*", "dev:up"));
        assert!(glob_match("backend:migrate:prod", "backend:migrate:prod"));
        assert!(!glob_match("backend:migrate:prod", "backend:migrate:down"));
        assert!(glob_match("*:prod", "backend:promo:prod"));
        assert!(glob_match("*", "anything"));
    }

    /// Production-touching tasks are flagged so they are not one fuzzy keypress away.
    #[test]
    fn flags_production_tasks() {
        assert!(
            parse_entry("* deploy:backend:   Deploy the backend")
                .unwrap()
                .dangerous
        );
        assert!(
            parse_entry("* backend:migrate:prod:   Apply migrations")
                .unwrap()
                .dangerous
        );
        assert!(
            !parse_entry("* backend:lint:   Rust lints")
                .unwrap()
                .dangerous
        );
    }
}
