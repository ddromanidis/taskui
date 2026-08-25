//! The execution graph: which tasks a task invokes.
//!
//! `task --list-all --json` does not carry dependencies, and parsing the Taskfiles
//! ourselves would mean reimplementing `includes:` resolution and then tracking go-task's
//! semantics forever. `task --summary <name>` already reports a task's direct edges using
//! go-task's own resolver, so recursing that from whatever was invoked reconstructs the
//! graph for the cost of a few process spawns.
//!
//! Note that `--summary` also prints the resolved environment, which for a Taskfile with
//! `dotenv:` means real credentials. That output is parsed in memory and dropped; it must
//! never be persisted or displayed.

use anyhow::{Context, Result};
use std::collections::{BTreeMap, HashSet};
use std::path::Path;
use std::process::Command;

#[derive(Debug, Default, Clone)]
pub struct Graph {
    /// task name -> the tasks it invokes, in the order it invokes them.
    pub edges: BTreeMap<String, Vec<String>>,
}

impl Graph {
    pub fn children(&self, task: &str) -> &[String] {
        self.edges.get(task).map(|v| v.as_slice()).unwrap_or(&[])
    }

    /// Every task reachable from `root`, including `root`.
    pub fn reachable(&self, root: &str) -> Vec<String> {
        let mut out = Vec::new();
        let mut seen = HashSet::new();
        let mut stack = vec![root.to_string()];
        while let Some(t) = stack.pop() {
            if !seen.insert(t.clone()) {
                continue;
            }
            for c in self.children(&t).iter().rev() {
                stack.push(c.clone());
            }
            out.push(t);
        }
        out
    }
}

/// A task's direct edges, dependencies first (go-task runs those before the commands).
fn parse_summary(text: &str) -> Vec<String> {
    #[derive(PartialEq)]
    enum Section {
        None,
        Deps,
        Cmds,
    }

    let mut section = Section::None;
    let mut deps = Vec::new();
    let mut cmds = Vec::new();

    for line in text.lines() {
        let trimmed = line.trim_end();
        match trimmed.trim() {
            "dependencies:" => {
                section = Section::Deps;
                continue;
            }
            "commands:" => {
                section = Section::Cmds;
                continue;
            }
            _ => {}
        }

        // Items are ` - <thing>`. Anything else — the env dump, the description, blank
        // lines — ends the section we were in.
        let Some(item) = trimmed.strip_prefix(" - ") else {
            if !trimmed.is_empty() {
                section = Section::None;
            }
            continue;
        };

        match section {
            Section::Deps => deps.push(item.trim().to_string()),
            // Under `commands:` only `Task: x` entries are edges; the rest are shell.
            Section::Cmds => {
                if let Some(name) = item.trim().strip_prefix("Task: ") {
                    cmds.push(name.trim().to_string());
                }
            }
            Section::None => {}
        }
    }

    deps.extend(cmds);
    deps
}

/// Variables a task declares with `requires: { vars: [NAME] }`.
///
/// This is a *fact* rather than a guess mined from prose, so the args prompt can pre-fill
/// `NAME=` and know it is asking for something real. One `--summary` call, ~40ms, made
/// only when the prompt opens.
pub fn required_vars(dir: &Path, task: &str) -> Vec<String> {
    let Ok(text) = summary_of(dir, task) else {
        return Vec::new();
    };
    parse_requires(&text)
}

fn parse_requires(text: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut inside = false;
    for line in text.lines() {
        let trimmed = line.trim();
        if trimmed == "requires:" {
            inside = true;
            continue;
        }
        if !inside {
            continue;
        }
        match trimmed.strip_prefix("- ") {
            Some(name) => out.push(name.trim().to_string()),
            None => {
                // The block nests — the names sit under a `vars:` sub-heading — so that
                // one line is stepped over rather than treated as the end of the section.
                if trimmed == "vars:" || trimmed.is_empty() {
                    continue;
                }
                break;
            }
        }
    }
    out
}

/// What a task actually is: what it says it does, what it needs, and what it will run.
#[derive(Debug, Default, Clone)]
pub struct Detail {
    pub summary: Vec<String>,
    pub requires: Vec<String>,
    pub dependencies: Vec<String>,
    pub commands: Vec<String>,
}

/// Parse `task --summary` into something showable.
///
/// The `vars:` and `env:` blocks are dropped on the floor. That is not tidiness: `env:`
/// is the resolved environment, so for a Taskfile with `dotenv:` it contains live
/// credentials, and this output goes on screen.
fn parse_detail(text: &str) -> Detail {
    #[derive(PartialEq, Clone, Copy)]
    enum Section {
        Description,
        Secrets,
        Requires,
        Dependencies,
        Commands,
    }

    let mut detail = Detail::default();
    let mut section = Section::Description;
    let mut lines = text.lines();
    // The first line is `task: <name>`, which the UI already knows.
    lines.next();

    for line in lines {
        let trimmed = line.trim();
        // Inside `requires:` the names nest under their own `vars:` sub-heading, which
        // must not be mistaken for the top-level one.
        if section == Section::Requires && trimmed == "vars:" {
            continue;
        }
        match trimmed {
            "vars:" | "env:" => {
                section = Section::Secrets;
                continue;
            }
            "requires:" => {
                section = Section::Requires;
                continue;
            }
            "dependencies:" => {
                section = Section::Dependencies;
                continue;
            }
            "commands:" => {
                section = Section::Commands;
                continue;
            }
            _ => {}
        }

        match section {
            Section::Secrets => {}
            Section::Description => {
                // go-task's stand-in for "no description" is noise here.
                if !trimmed.is_empty() && !trimmed.starts_with("(task does not have") {
                    detail.summary.push(trimmed.to_string());
                }
            }
            // `requires:` nests: the items sit under a `vars:` sub-heading, which is
            // skipped rather than treated as the end of the section.
            Section::Requires => {
                if let Some(name) = trimmed.strip_prefix("- ") {
                    detail.requires.push(name.trim().to_string());
                }
            }
            Section::Dependencies => {
                if let Some(name) = trimmed.strip_prefix("- ") {
                    detail.dependencies.push(name.trim().to_string());
                }
            }
            // Commands keep their shape: a multi-line shell block is one command, and
            // reflowing it would misrepresent what runs.
            Section::Commands => {
                if !line.trim_end().is_empty() {
                    detail
                        .commands
                        .push(line.trim_end().trim_start_matches(" - ").to_string());
                }
            }
        }
    }
    detail
}

/// One `--summary` call, made when the detail panel opens.
pub fn detail(dir: &Path, task: &str) -> Detail {
    summary_of(dir, task)
        .map(|t| parse_detail(&t))
        .unwrap_or_default()
}

fn summary_of(dir: &Path, task: &str) -> Result<String> {
    let out = Command::new("task")
        .arg("--summary")
        .arg(task)
        .current_dir(dir)
        .output()
        .with_context(|| format!("running `task --summary {task}`"))?;
    // A task that cannot be summarised is not fatal — it just has no known children.
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

/// Walk outward from `root`, one `--summary` per distinct task.
///
/// Tasks are memoised and revisits short-circuit, so a diamond (`all` reaching `lint` and
/// `check`, both reaching `backend:*`) costs one call per node, and a cycle terminates
/// instead of spinning.
pub fn resolve(dir: &Path, root: &str) -> Result<Graph> {
    Ok(resolve_detailed(dir, root)?.0)
}

/// As [`resolve`], but also hands back the root task's raw `--summary` text.
///
/// That text contains the resolved environment — which is exactly what the redactor needs
/// in order to know what to mask. It is returned rather than stored so the caller is
/// forced to decide what happens to it; it must not be persisted or displayed.
pub fn resolve_detailed(dir: &Path, root: &str) -> Result<(Graph, String)> {
    // The root's own summary is wanted verbatim, so it is fetched here; everything below
    // it is resolved concurrently.
    let root_summary = summary_of(dir, root)?;
    let graph = resolve_parallel(root, dir)?;
    Ok((graph, root_summary))
}

/// Resolve one frontier at a time, fetching each frontier's tasks concurrently.
///
/// One `--summary` is a process spawn — around 40ms — and a big aggregate has dozens of
/// them. Serially that was 1.3s of dead time before atlas's `all` produced a single line.
/// The graph is a level-order walk anyway, so each level's calls are independent.
fn resolve_parallel(root: &str, dir: &Path) -> Result<Graph> {
    // Enough to hide the latency without spawning a process per task in a wide graph.
    const LANES: usize = 8;

    let mut graph = Graph::default();
    let mut frontier = vec![root.to_string()];

    while !frontier.is_empty() {
        let mut next = Vec::new();

        for batch in frontier.chunks(LANES) {
            let mut handles = Vec::new();
            for task in batch {
                let (dir, task) = (dir.to_path_buf(), task.clone());
                handles.push(std::thread::spawn(move || {
                    let text = summary_of(&dir, &task).unwrap_or_default();
                    (task, parse_summary(&text))
                }));
            }
            for handle in handles {
                let Ok((task, children)) = handle.join() else {
                    continue;
                };
                for c in &children {
                    if !graph.edges.contains_key(c) && !next.contains(c) {
                        next.push(c.clone());
                    }
                }
                graph.edges.insert(task, children);
            }
        }

        // Anything already resolved by an earlier level is not revisited, so a diamond
        // costs one call and a cycle terminates.
        frontier = next
            .into_iter()
            .filter(|t| !graph.edges.contains_key(t))
            .collect();
    }

    Ok(graph)
}

#[cfg(test)]
fn resolve_with(root: &str, mut fetch: impl FnMut(&str) -> Result<String>) -> Result<Graph> {
    let mut graph = Graph::default();
    let mut queue = vec![root.to_string()];

    while let Some(task) = queue.pop() {
        if graph.edges.contains_key(&task) {
            continue;
        }
        let children = parse_summary(&fetch(&task)?);
        for c in &children {
            if !graph.edges.contains_key(c) {
                queue.push(c.clone());
            }
        }
        graph.edges.insert(task, children);
    }

    Ok(graph)
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Real `task --summary` output: the env dump comes first and must not be mistaken
    /// for edges.
    const LINT: &str = r#"task: lint

Lint all source code

env:
  TELEGRAM_BOT_USERNAME: "Atlasiobot"
  LOG_REDACT_PII: "false"

commands:
 - Task: api:check
 - Task: backend:lint
 - Task: app:lint
 - Task: infra:lint
"#;

    const MID: &str = r#"task: mid

(task does not have description or summary)

dependencies:
 - a

commands:
 - Task: b
"#;

    const SHELL_ONLY: &str = r#"task: fmt

commands:
 - cargo fmt --all
 - terraform fmt -recursive
"#;

    #[test]
    fn parses_task_edges_from_commands() {
        assert_eq!(
            parse_summary(LINT),
            ["api:check", "backend:lint", "app:lint", "infra:lint"]
        );
    }

    /// Dependencies run before commands, so they come first in the child order.
    #[test]
    fn dependencies_precede_commands() {
        assert_eq!(parse_summary(MID), ["a", "b"]);
    }

    /// Shell commands under `commands:` are not edges.
    #[test]
    fn shell_commands_are_not_edges() {
        assert!(parse_summary(SHELL_ONLY).is_empty());
    }

    /// The env dump is the dangerous case: `KEY: "value"` lines look nothing like ` - x`
    /// items, but a sloppy parser that just scanned for colons would swallow them.
    #[test]
    fn env_dump_yields_no_edges() {
        let env_only = "task: x\n\nenv:\n  API_TOKEN: \"cfut_secret\"\n  OTHER: \"Task: nope\"\n";
        assert!(parse_summary(env_only).is_empty());
    }

    /// Real `--summary` output for a task declaring `requires: { vars: [NAME] }`.
    const FULL: &str = r#"task: wt:new

New worktree + branch for an agent: task wt:new NAME=backend

vars:

env:
  CLOUDFLARE_API_TOKEN: "cfut_realtoken"
  S3_REGION: "us-east-1"

requires:
  vars:
    - NAME

dependencies:
 - setup

commands:
 - Task: build
 - set -eu
dir=".worktrees/"
git worktree add "$dir"
"#;

    /// The env block is the resolved environment — live credentials for a Taskfile with
    /// `dotenv:` — and this output goes on screen.
    #[test]
    fn the_detail_never_carries_the_environment() {
        let d = parse_detail(FULL);
        let all = format!("{d:?}");
        assert!(
            !all.contains("cfut_realtoken"),
            "a secret leaked into the panel: {all}"
        );
        assert!(
            !all.contains("S3_REGION"),
            "and neither should the harmless ones"
        );
    }

    #[test]
    fn parses_what_a_task_is_and_does() {
        let d = parse_detail(FULL);
        assert_eq!(
            d.summary,
            ["New worktree + branch for an agent: task wt:new NAME=backend"]
        );
        assert_eq!(d.requires, ["NAME"]);
        assert_eq!(d.dependencies, ["setup"]);
        assert_eq!(d.commands[0], "Task: build");
        assert_eq!(d.commands[1], "set -eu");
        // Multi-line shell keeps its shape rather than being reflowed.
        assert!(d.commands.iter().any(|c| c.contains("git worktree add")));
    }

    /// "(task does not have description or summary)" is go-task's stand-in, not content.
    #[test]
    fn the_no_description_placeholder_is_dropped() {
        let d = parse_detail(MID);
        assert!(d.summary.is_empty(), "{:?}", d.summary);
        assert_eq!(d.dependencies, ["a"]);
    }

    #[test]
    fn parses_required_vars() {
        let text = "task: wt:new\n\nNew worktree\n\nvars:\n\nenv:\n  S3_REGION: \"us-east-1\"\n\nrequires:\n    - NAME\n\ncommands:\n - set -eu\n";
        assert_eq!(parse_requires(text), ["NAME"]);
    }

    #[test]
    fn parses_several_required_vars() {
        assert_eq!(
            parse_requires("requires:\n    - NAME\n    - WORD\n\ncommands:\n"),
            ["NAME", "WORD"]
        );
    }

    /// A task with no `requires:` block asks for nothing.
    #[test]
    fn no_requires_block_yields_nothing() {
        assert!(parse_requires(LINT).is_empty());
    }

    /// The dependency list must not be mistaken for required variables.
    #[test]
    fn dependencies_are_not_required_vars() {
        assert!(parse_requires(MID).is_empty());
    }

    #[test]
    fn resolve_walks_the_whole_graph_once_per_task() {
        let mut calls: Vec<String> = Vec::new();
        let graph = resolve_with("all", |t| {
            calls.push(t.to_string());
            Ok(match t {
                "all" => "commands:\n - Task: lint\n - Task: test\n".into(),
                // A diamond: both sides reach backend:lint.
                "lint" => "commands:\n - Task: backend:lint\n".into(),
                "test" => "commands:\n - Task: backend:lint\n".into(),
                _ => String::new(),
            })
        })
        .unwrap();

        assert_eq!(graph.children("all"), ["lint", "test"]);
        assert_eq!(graph.children("backend:lint"), [] as [String; 0]);
        calls.sort();
        assert_eq!(
            calls,
            ["all", "backend:lint", "lint", "test"],
            "one call each"
        );
    }

    #[test]
    fn resolve_terminates_on_a_cycle() {
        let graph = resolve_with("a", |t| {
            Ok(match t {
                "a" => "commands:\n - Task: b\n".into(),
                "b" => "commands:\n - Task: a\n".into(),
                _ => String::new(),
            })
        })
        .unwrap();
        assert_eq!(graph.children("a"), ["b"]);
        assert_eq!(graph.children("b"), ["a"]);
    }

    #[test]
    fn reachable_lists_the_subtree_depth_first() {
        let graph = resolve_with("all", |t| {
            Ok(match t {
                "all" => "commands:\n - Task: lint\n - Task: test\n".into(),
                "lint" => "commands:\n - Task: backend:lint\n".into(),
                _ => String::new(),
            })
        })
        .unwrap();
        assert_eq!(
            graph.reachable("all"),
            ["all", "lint", "backend:lint", "test"]
        );
    }
}
