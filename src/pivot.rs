//! Grouping is a pivot, not a set of bespoke views: one flat list of tasks plus a key
//! function, rendered as a fold tree. Adding a grouping means adding a builder here.

use crate::task::Task;
use std::collections::BTreeMap;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    /// n-level split on `:` — `backend` > `migrate` > `down`.
    Domain,
    /// Two-level on the last segment — `lint` > {api:lint, app:lint, backend:lint, …}.
    /// The transpose of Domain: it collects the cross-cutting concerns that the domain
    /// tree scatters.
    Verb,
}

impl Mode {
    pub fn label(self) -> &'static str {
        match self {
            Mode::Domain => "domain",
            Mode::Verb => "verb",
        }
    }

    pub fn toggled(self) -> Mode {
        match self {
            Mode::Domain => Mode::Verb,
            Mode::Verb => Mode::Domain,
        }
    }
}

/// A node in the fold tree.
///
/// `task` and `children` are independent: a node can be both a runnable task and a group
/// header. `backend:migrate` is exactly that — it applies migrations *and* it is the
/// parent of `backend:migrate:down`, `:prod`, `:status`, `:schema`.
#[derive(Debug, Clone)]
pub struct Node {
    /// What renders on the row.
    pub label: String,
    /// Stable identity for fold state — survives rebuilds and filtering.
    pub key: String,
    pub task: Option<usize>,
    pub children: Vec<usize>,
    /// Tasks in this subtree, including `task` itself.
    pub count: usize,
}

impl Node {
    pub fn is_group(&self) -> bool {
        !self.children.is_empty()
    }
}

#[derive(Debug, Default)]
pub struct Tree {
    pub nodes: Vec<Node>,
    pub roots: Vec<usize>,
}

impl Tree {
    fn push(&mut self, label: String, key: String) -> usize {
        self.nodes.push(Node {
            label,
            key,
            task: None,
            children: Vec::new(),
            count: 0,
        });
        self.nodes.len() - 1
    }

    /// Depth-first walk, parents before children, honouring `expanded`.
    pub fn flatten(&self, expanded: &dyn Fn(&str) -> bool) -> Vec<Row> {
        let mut rows = Vec::new();
        for &r in &self.roots {
            self.walk(r, 0, expanded, &mut rows);
        }
        rows
    }

    fn walk(&self, idx: usize, depth: usize, expanded: &dyn Fn(&str) -> bool, out: &mut Vec<Row>) {
        let node = &self.nodes[idx];
        let open = node.is_group() && expanded(&node.key);
        out.push(Row {
            node: idx,
            depth,
            open,
        });
        if open {
            for &c in &node.children {
                self.walk(c, depth + 1, expanded, out);
            }
        }
    }

    /// Every ancestor key of the node holding `task_idx`, outermost first. Used to open
    /// the folds needed to reveal a selection after a pivot or a filter change.
    pub fn ancestors_of_task(&self, task_idx: usize) -> Option<Vec<String>> {
        for &r in &self.roots {
            let mut path = Vec::new();
            if self.find(r, task_idx, &mut path) {
                path.pop(); // the node itself is not its own ancestor
                return Some(path);
            }
        }
        None
    }

    fn find(&self, idx: usize, task_idx: usize, path: &mut Vec<String>) -> bool {
        let node = &self.nodes[idx];
        path.push(node.key.clone());
        if node.task == Some(task_idx) {
            return true;
        }
        for &c in &node.children {
            if self.find(c, task_idx, path) {
                return true;
            }
        }
        path.pop();
        false
    }
}

#[derive(Debug, Clone, Copy)]
pub struct Row {
    pub node: usize,
    pub depth: usize,
    /// Group rows only: whether this row's children are showing.
    pub open: bool,
}

/// Tasks with no namespace go here. Pinned to the top: in practice these are the daily
/// drivers (`all`, `build`, `test`, `dev`, …).
pub const ROOT_GROUP: &str = "(root)";
/// Verb mode bucket for verbs that only ever appear once. Grouping a singleton reads
/// worse than not grouping it at all, so they land here, flat.
pub const OTHER_GROUP: &str = "other";

pub fn build(mode: Mode, tasks: &[Task], visible: &[usize]) -> Tree {
    match mode {
        Mode::Domain => build_domain(tasks, visible),
        Mode::Verb => build_verb(tasks, visible),
    }
}

fn build_domain(tasks: &[Task], visible: &[usize]) -> Tree {
    let mut tree = Tree::default();
    // key -> node index, so repeated prefixes reuse the same node.
    let mut index: BTreeMap<String, usize> = BTreeMap::new();

    // Namespaced tasks first, so every group node exists before the unnamespaced ones are
    // placed. Otherwise a root-level `build` would land in (root) and `build:release`
    // would then create a *second*, unrelated `build` node beside it.
    for &ti in visible {
        let segs = tasks[ti].segments();
        if segs.len() == 1 {
            continue;
        }

        // Walk the prefix, creating nodes as needed, and attach the task to the node that
        // matches its full path — which may already exist as a group.
        let mut parent: Option<usize> = None;
        for depth in 0..segs.len() {
            let key = segs[..=depth].join(":");
            let existing = index.get(&key).copied();
            let node = match existing {
                Some(n) => n,
                None => {
                    let n = tree.push(segs[depth].to_string(), key.clone());
                    match parent {
                        Some(p) => tree.nodes[p].children.push(n),
                        None => tree.roots.push(n),
                    }
                    index.insert(key, n);
                    n
                }
            };
            if depth == segs.len() - 1 {
                tree.nodes[node].task = Some(ti);
            }
            parent = Some(node);
        }
    }

    for &ti in visible {
        let name = &tasks[ti].name;
        if name.contains(':') {
            continue;
        }

        // A root-level task whose name is also a namespace belongs *to* that namespace,
        // not beside it. `build` and `build:release` are one thing with a subtask, the
        // same shape as `backend:migrate` and `backend:migrate:down`.
        if let Some(&node) = index.get(name.as_str()) {
            tree.nodes[node].task = Some(ti);
            continue;
        }

        let root = *index.entry(ROOT_GROUP.to_string()).or_insert_with(|| {
            let n = tree.push(ROOT_GROUP.to_string(), ROOT_GROUP.to_string());
            tree.roots.push(n);
            n
        });
        let leaf = tree.push(name.clone(), name.clone());
        tree.nodes[leaf].task = Some(ti);
        tree.nodes[root].children.push(leaf);
    }

    sort_domain(&mut tree);
    recount(&mut tree);
    tree
}

/// Within a node: plain tasks first, then subgroups, each alphabetical. Keeps the leaf
/// verbs of a namespace (`backend:build`, `backend:fmt`, …) together at the top instead
/// of interleaving them with `backend:migrate`'s subtree.
fn sort_domain(tree: &mut Tree) {
    let order = |nodes: &Vec<Node>, a: &usize, b: &usize| {
        let (na, nb) = (&nodes[*a], &nodes[*b]);
        na.is_group()
            .cmp(&nb.is_group())
            .then_with(|| na.label.cmp(&nb.label))
    };

    for i in 0..tree.nodes.len() {
        let mut kids = std::mem::take(&mut tree.nodes[i].children);
        kids.sort_by(|a, b| order(&tree.nodes, a, b));
        tree.nodes[i].children = kids;
    }
    let mut roots = std::mem::take(&mut tree.roots);
    roots.sort_by(|a, b| {
        // (root) is pinned first; everything else alphabetical.
        let (ka, kb) = (&tree.nodes[*a].key, &tree.nodes[*b].key);
        (ka != ROOT_GROUP)
            .cmp(&(kb != ROOT_GROUP))
            .then_with(|| ka.cmp(kb))
    });
    tree.roots = roots;
}

fn build_verb(tasks: &[Task], visible: &[usize]) -> Tree {
    let mut by_verb: BTreeMap<&str, Vec<usize>> = BTreeMap::new();
    for &ti in visible {
        by_verb.entry(tasks[ti].verb()).or_default().push(ti);
    }

    // Singletons carry no grouping value — pool them.
    let mut groups: Vec<(&str, Vec<usize>)> = Vec::new();
    let mut singles: Vec<usize> = Vec::new();
    for (verb, members) in by_verb {
        if members.len() > 1 {
            groups.push((verb, members));
        } else {
            singles.extend(members);
        }
    }

    // Size descending, so the cross-cutting concerns you pivoted to find are on top.
    groups.sort_by(|a, b| b.1.len().cmp(&a.1.len()).then_with(|| a.0.cmp(b.0)));

    let mut tree = Tree::default();
    for (verb, mut members) in groups {
        // Root aggregate first, then the rest alphabetically. `lint` sits directly above
        // `app:lint`, `backend:lint`, `infra:lint` — which is exactly what `task lint`
        // will do, so the verb pivot doubles as a static preview of that run.
        members.sort_by(|a, b| {
            let (na, nb) = (&tasks[*a].name, &tasks[*b].name);
            na.contains(':')
                .cmp(&nb.contains(':'))
                .then_with(|| na.cmp(nb))
        });
        let g = tree.push(verb.to_string(), format!("verb:{verb}"));
        tree.roots.push(g);
        for ti in members {
            // Leaves show the full colon path: flattening the domain into the label is
            // the entire point of this pivot, so we must not re-nest it.
            let leaf = tree.push(
                tasks[ti].name.clone(),
                format!("verb:{verb}/{}", tasks[ti].name),
            );
            tree.nodes[leaf].task = Some(ti);
            tree.nodes[g].children.push(leaf);
        }
    }

    if !singles.is_empty() {
        singles.sort_by(|a, b| tasks[*a].name.cmp(&tasks[*b].name));
        let g = tree.push(OTHER_GROUP.to_string(), format!("verb:{OTHER_GROUP}"));
        tree.roots.push(g); // last
        for ti in singles {
            let leaf = tree.push(
                tasks[ti].name.clone(),
                format!("verb:{OTHER_GROUP}/{}", tasks[ti].name),
            );
            tree.nodes[leaf].task = Some(ti);
            tree.nodes[g].children.push(leaf);
        }
    }

    recount(&mut tree);
    tree
}

fn recount(tree: &mut Tree) {
    let roots = tree.roots.clone();
    for r in roots {
        count_subtree(tree, r);
    }
}

fn count_subtree(tree: &mut Tree, idx: usize) -> usize {
    let kids = tree.nodes[idx].children.clone();
    let mut n = if tree.nodes[idx].task.is_some() { 1 } else { 0 };
    for c in kids {
        n += count_subtree(tree, c);
    }
    tree.nodes[idx].count = n;
    n
}

#[cfg(test)]
pub(crate) fn fixture(names: &[&str]) -> Vec<Task> {
    names
        .iter()
        .map(|n| Task {
            name: n.to_string(),
            desc: String::new(),
            aliases: Vec::new(),
            dangerous: false,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Shape lifted from a real Taskfile: root aggregates, several namespaces, and
    /// namespaces that nest three deep.
    fn sample() -> Vec<Task> {
        fixture(&[
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
            "backend:migrate:prod",
            "infra:lint",
            "site:build",
            "wt:ls",
        ])
    }

    fn render(tree: &Tree) -> Vec<String> {
        tree.flatten(&|_| true)
            .iter()
            .map(|r| format!("{}{}", "  ".repeat(r.depth), tree.nodes[r.node].label))
            .collect()
    }

    #[test]
    fn domain_pins_root_group_first() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Domain, &tasks, &all);
        assert_eq!(tree.nodes[tree.roots[0]].label, ROOT_GROUP);
        assert_eq!(tree.nodes[tree.roots[0]].count, 4);
        // …and the rest are alphabetical.
        let rest: Vec<&str> = tree.roots[1..]
            .iter()
            .map(|&r| tree.nodes[r].label.as_str())
            .collect();
        assert_eq!(rest, ["app", "backend", "infra", "site", "wt"]);
    }

    /// The same must hold when the parent has no namespace of its own: taskui's own
    /// Taskfile has `build` and `build:release`, and `build` was being shown twice —
    /// once as a task under (root), once as an unrelated namespace beside it.
    #[test]
    fn a_root_task_that_names_a_namespace_owns_it() {
        let tasks = fixture(&["all", "build", "build:release", "test", "test:one"]);
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Domain, &tasks, &all);

        let roots: Vec<&str> = tree
            .roots
            .iter()
            .map(|&r| tree.nodes[r].label.as_str())
            .collect();
        assert_eq!(roots, [ROOT_GROUP, "build", "test"], "one `build`, not two");

        let build = tree.nodes.iter().find(|n| n.key == "build").unwrap();
        assert!(build.is_group(), "parents build:release");
        assert!(build.task.is_some(), "and is still runnable itself");
        assert_eq!(build.count, 2);

        // (root) keeps only what genuinely has no namespace.
        let root = &tree.nodes[tree.roots[0]];
        let kids: Vec<&str> = root
            .children
            .iter()
            .map(|&c| tree.nodes[c].label.as_str())
            .collect();
        assert_eq!(kids, ["all"]);
    }

    /// `backend:migrate` applies migrations *and* parents `backend:migrate:down`. The
    /// node has to be both, or one of the two disappears from the UI.
    #[test]
    fn domain_node_can_be_both_group_and_task() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Domain, &tasks, &all);
        let migrate = tree
            .nodes
            .iter()
            .find(|n| n.key == "backend:migrate")
            .expect("backend:migrate node");
        assert!(migrate.is_group(), "should parent its subtasks");
        assert!(migrate.task.is_some(), "should still be runnable itself");
        assert_eq!(migrate.count, 3, "itself plus down plus prod");
    }

    #[test]
    fn domain_puts_plain_tasks_before_subgroups() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Domain, &tasks, &all);
        let lines = render(&tree);
        let backend = lines.iter().position(|l| l == "backend").unwrap();
        // build, fmt, lint, then the migrate subtree.
        assert_eq!(
            &lines[backend + 1..backend + 5],
            ["  build", "  fmt", "  lint", "  migrate"]
        );
    }

    #[test]
    fn verb_groups_by_last_segment_size_descending() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Verb, &tasks, &all);
        let groups: Vec<(&str, usize)> = tree
            .roots
            .iter()
            .map(|&r| (tree.nodes[r].label.as_str(), tree.nodes[r].count))
            .collect();
        // build and lint tie at 4; ties break alphabetically.
        assert_eq!(groups[0], ("build", 4));
        assert_eq!(groups[1], ("lint", 4));
        assert_eq!(groups[2], ("fmt", 3));
    }

    /// Grouping a verb that only appears once reads worse than not grouping it, so the
    /// singletons pool into one flat bucket, pinned last.
    #[test]
    fn verb_pools_singletons_into_other_last() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Verb, &tasks, &all);
        let last = *tree.roots.last().unwrap();
        assert_eq!(tree.nodes[last].label, OTHER_GROUP);
        let members: Vec<&str> = tree.nodes[last]
            .children
            .iter()
            .map(|&c| tree.nodes[c].label.as_str())
            .collect();
        assert_eq!(
            members,
            [
                "all",
                "backend:migrate",
                "backend:migrate:down",
                "backend:migrate:prod",
                "wt:ls"
            ]
        );
    }

    /// The pivot must flatten the domain into the leaf label — re-nesting it would undo
    /// the point of the view.
    #[test]
    fn verb_leaves_carry_the_full_colon_path() {
        let tasks = sample();
        let all: Vec<usize> = (0..tasks.len()).collect();
        let tree = build(Mode::Verb, &tasks, &all);
        let lint = tree
            .roots
            .iter()
            .map(|&r| &tree.nodes[r])
            .find(|n| n.label == "lint")
            .unwrap();
        let members: Vec<&str> = lint
            .children
            .iter()
            .map(|&c| tree.nodes[c].label.as_str())
            .collect();
        assert_eq!(members, ["lint", "app:lint", "backend:lint", "infra:lint"]);
        assert!(lint
            .children
            .iter()
            .all(|&c| tree.nodes[c].children.is_empty()));
    }
}
