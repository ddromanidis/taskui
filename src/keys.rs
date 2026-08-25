//! The keymap, as data.
//!
//! Both the `?` screen and the one-line footer hints are generated from this table, so
//! they cannot disagree with each other. They could still disagree with the *handlers* in
//! `main.rs` — nothing here dispatches anything — but a single table that both surfaces
//! read from is the difference between one place to update and three.

/// Everything a single keypress can ask for.
///
/// Dispatch goes through this rather than matching characters directly, which is what
/// makes rebinding possible: the config maps a character to an action, and the handlers
/// only ever see actions.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Action {
    Pivot,
    Args,
    Detail,
    Jump,
    Filter,
    History,
    ResumeRun,
    Interactive,
    Force,
    Help,
    Quit,
    Search,
    NextMatch,
    PrevMatch,
    FilterMatches,
    ContextMore,
    ContextLess,
    Rerun,
    Stop,
    Input,
    InteractiveRerun,
    Follow,
    Watch,
    Yank,
    YankAll,
    AllProjects,
    Top,
    Bottom,
}

/// Action, its default key, and the config name used to rebind it.
const DEFAULTS: &[(Action, char, &str)] = &[
    (Action::Pivot, 'p', "pivot"),
    (Action::Args, 'a', "args"),
    (Action::Detail, 's', "detail"),
    (Action::Jump, 't', "jump"),
    (Action::Filter, '/', "filter"),
    (Action::History, 'h', "history"),
    (Action::ResumeRun, 'v', "watch-run"),
    (Action::Interactive, 'i', "interactive"),
    (Action::Force, 'F', "force"),
    (Action::Help, '?', "help"),
    (Action::Quit, 'q', "quit"),
    (Action::Search, '/', "search"),
    (Action::NextMatch, 'n', "next-match"),
    (Action::PrevMatch, 'N', "prev-match"),
    (Action::FilterMatches, 'f', "filter-matches"),
    (Action::ContextMore, ']', "context-more"),
    (Action::ContextLess, '[', "context-less"),
    (Action::Rerun, 'r', "rerun"),
    (Action::Stop, 'x', "stop"),
    (Action::Input, 'i', "input"),
    (Action::InteractiveRerun, 'I', "interactive-rerun"),
    (Action::Follow, 'w', "follow"),
    (Action::Watch, 'W', "watch"),
    (Action::Yank, 'y', "yank"),
    (Action::YankAll, 'Y', "yank-all"),
    (Action::AllProjects, 'a', "all-projects"),
    (Action::Top, 'g', "top"),
    (Action::Bottom, 'G', "bottom"),
];

/// Which character triggers which action.
///
/// Screens are separate maps because the same key means different things depending on
/// where you are — `a` is "run with arguments" in the picker and "all projects" in the
/// history list, and `i` arms interactive mode in one and types at the task in the other.
#[derive(Debug, Clone)]
pub struct Keymap {
    picker: Vec<(char, Action)>,
    run: Vec<(char, Action)>,
    history: Vec<(char, Action)>,
}

const PICKER_ACTIONS: &[Action] = &[
    Action::Pivot,
    Action::Args,
    Action::Detail,
    Action::Jump,
    Action::Filter,
    Action::History,
    Action::ResumeRun,
    Action::Interactive,
    Action::Force,
    Action::Help,
    Action::Quit,
];

const RUN_ACTIONS: &[Action] = &[
    Action::Search,
    Action::NextMatch,
    Action::PrevMatch,
    Action::FilterMatches,
    Action::ContextMore,
    Action::ContextLess,
    Action::Rerun,
    Action::Args,
    Action::Stop,
    Action::Input,
    Action::InteractiveRerun,
    Action::Follow,
    Action::Watch,
    Action::Yank,
    Action::YankAll,
    Action::History,
    Action::Help,
    Action::Quit,
];

const HISTORY_ACTIONS: &[Action] = &[
    Action::Search,
    Action::AllProjects,
    Action::Help,
    Action::Quit,
];

fn default_key(action: Action) -> char {
    DEFAULTS
        .iter()
        .find(|(a, _, _)| *a == action)
        .map(|(_, k, _)| *k)
        .unwrap_or('\0')
}

/// The config name for an action, e.g. `filter-matches`.
pub fn action_name(action: Action) -> &'static str {
    DEFAULTS
        .iter()
        .find(|(a, _, _)| *a == action)
        .map(|(_, _, n)| *n)
        .unwrap_or("")
}

pub fn action_by_name(name: &str) -> Option<Action> {
    DEFAULTS
        .iter()
        .find(|(_, _, n)| *n == name)
        .map(|(a, _, _)| *a)
}

pub fn all_actions() -> impl Iterator<Item = (Action, char, &'static str)> + 'static {
    DEFAULTS.iter().copied()
}

impl Default for Keymap {
    fn default() -> Self {
        let build = |actions: &[Action]| {
            actions
                .iter()
                .map(|a| (default_key(*a), *a))
                .collect::<Vec<_>>()
        };
        Keymap {
            picker: build(PICKER_ACTIONS),
            run: build(RUN_ACTIONS),
            history: build(HISTORY_ACTIONS),
        }
    }
}

impl Keymap {
    /// Point an action at a different key, wherever that action is available.
    pub fn rebind(&mut self, action: Action, key: char) {
        for map in [&mut self.picker, &mut self.run, &mut self.history] {
            for slot in map.iter_mut() {
                if slot.1 == action {
                    slot.0 = key;
                }
            }
        }
    }

    pub fn picker(&self, key: char) -> Option<Action> {
        Self::look(&self.picker, key)
    }

    pub fn run(&self, key: char) -> Option<Action> {
        Self::look(&self.run, key)
    }

    pub fn history(&self, key: char) -> Option<Action> {
        Self::look(&self.history, key)
    }

    /// First match wins, so a rebinding that collides with another action shadows it
    /// rather than doing both.
    fn look(map: &[(char, Action)], key: char) -> Option<Action> {
        map.iter().find(|(k, _)| *k == key).map(|(_, a)| *a)
    }

    /// Keys bound to more than one action in the same screen. Reported rather than
    /// silently resolved — a shadowed key looks like a broken one.
    pub fn conflicts(&self) -> Vec<String> {
        let mut out = Vec::new();
        for (screen, map) in [
            ("picker", &self.picker),
            ("run", &self.run),
            ("history", &self.history),
        ] {
            for (i, (key, action)) in map.iter().enumerate() {
                if let Some((_, other)) = map[..i].iter().find(|(k, _)| k == key) {
                    out.push(format!(
                        "{screen}: `{key}` is both {} and {}",
                        action_name(*other),
                        action_name(*action)
                    ));
                }
            }
        }
        out
    }
}

pub struct Binding {
    pub keys: &'static str,
    /// The full explanation, for the `?` screen.
    pub what: &'static str,
    /// A two-or-three word label for the footer, or `None` to keep this binding in the
    /// full help only. Written out rather than derived from `what`: shortening prose
    /// mechanically produced a 170-character line and labels like "fold or unfold a".
    pub footer: Option<&'static str>,
}

pub struct Section {
    pub title: &'static str,
    pub note: &'static str,
    pub bindings: &'static [Binding],
}

const fn b(keys: &'static str, what: &'static str) -> Binding {
    Binding {
        keys,
        what,
        footer: None,
    }
}

const fn f(keys: &'static str, what: &'static str, label: &'static str) -> Binding {
    Binding {
        keys,
        what,
        footer: Some(label),
    }
}

pub const PICKER: Section = Section {
    title: "Picker",
    note: "browsing the Taskfile",
    bindings: &[
        b("j k ↑ ↓", "move"),
        b("gg G", "first / last row"),
        b("^d ^u", "half a screen down / up"),
        b("PgUp PgDn Home End", "move faster"),
        // No footer label: the picker's footer names the pivot you would switch *to*,
        // which is more use than the word "pivot" and would otherwise be printed twice.
        // No footer label: the picker's footer names the pivot you would switch *to*,
        // which is more use than the word "pivot" and would otherwise be printed twice.
        b("p", "toggle the pivot: by domain / by verb"),
        f("space", "fold or unfold a group", "fold"),
        b("⇥", "fold or unfold everything"),
        f("⏎", "run the task", "run"),
        f("a", "run it with arguments", "args"),
        f("i", "arm interactive mode for the next run", "interactive"),
        b("⇧F", "arm --force: ignore go-task's up-to-date checks"),
        f("/", "filter the list down to matching tasks", "filter"),
        f("t", "jump to a task, leaving the list intact", "jump"),
        f("s", "what this task is, and what it will run", "detail"),
        f("v", "go back to the run already in progress", "watch"),
        f("h", "past runs", "history"),
        b("?", "this screen"),
        b("q esc", "quit"),
    ],
};

pub const RUN: Section = Section {
    title: "Run",
    note: "watching, or reading back, one run",
    bindings: &[
        b("j k ↑ ↓", "move"),
        b("gg G", "first / last row"),
        b("^d ^u", "half a screen down / up"),
        f("space", "fold or unfold a task's output", "fold"),
        f("/", "search the output", "search"),
        f("n N", "next / previous match", "next"),
        f("f", "filter to matching lines only", "filter"),
        f("[ ]", "less / more context around each hit", "context"),
        f("r", "re-run this task, same arguments", "rerun"),
        f("a", "re-run it with different arguments", "args"),
        f(
            "i",
            "type at the running task — works even when you cannot see the prompt",
            "input",
        ),
        b(
            "⇧I",
            "re-run this task interactively, so prompts are visible",
        ),
        f("x", "stop the run", "stop"),
        b("y", "copy the line under the cursor"),
        b("⇧Y", "copy everything this task printed"),
        b("w", "resume following the running task"),
        b("⇧W", "watch: re-run this task whenever the source changes"),
        b("h", "past runs"),
        b("?", "this screen"),
        f("esc", "back to the picker — the run keeps going", "back"),
        b("q", "quit, stopping the run"),
    ],
};

pub const HISTORY: Section = Section {
    title: "History",
    note: "runs already finished, scoped to this project",
    bindings: &[
        b("j k ↑ ↓", "move"),
        b("gg G", "first / last row"),
        f("⏎", "reopen the run", "open"),
        f("/", "search across every stored run", "search runs"),
        f("a", "widen to all projects", "all projects"),
        b("?", "this screen"),
        f("esc", "back to the picker", "back"),
        b("q", "quit"),
    ],
};

pub const DETAIL: Section = Section {
    title: "Detail",
    note: "what a task is, before you run it",
    bindings: &[
        b("j k", "scroll"),
        b("⏎", "run it"),
        b("a", "run it with arguments"),
        b("s esc", "back"),
    ],
};

pub const PROMPTS: Section = Section {
    title: "Prompts",
    note: "while a prompt is open, these take over",
    bindings: &[
        b("arguments", "← → Home End Delete edit · ⏎ run · esc cancel"),
        b(
            "search / filter",
            "⏎ keep the query · esc clear · ↑ ↓ step through matches",
        ),
        b("input", "every key goes to the task · esc stop typing"),
        b("confirmation", "y runs it · anything else cancels"),
    ],
};

pub const SECTIONS: &[&Section] = &[&PICKER, &RUN, &HISTORY, &DETAIL, &PROMPTS];

/// The footer line for a section: the bindings worth the space, in table order.
pub fn footer(section: &Section) -> String {
    section
        .bindings
        .iter()
        .filter_map(|b| b.footer.map(|label| format!("{} {label}", b.keys)))
        .collect::<Vec<_>>()
        .join("   ")
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The footer is generated, so it cannot drift from the help screen.
    #[test]
    fn footers_are_built_from_the_same_table() {
        let picker = footer(&PICKER);
        assert!(picker.contains("⏎ run"), "{picker}");
        // The picker's footer names the target pivot itself, so `p` is not in the table.
        assert!(!picker.contains("pivot"), "{picker}");
        assert!(picker.contains("h history"), "{picker}");
        // Not marked for the footer, so it stays in the full help only.
        assert!(!picker.contains("everything"), "{picker}");
    }

    /// A binding shown in the footer still carries its long form for the `?` screen.
    #[test]
    fn footer_bindings_keep_their_full_explanation() {
        let i = RUN.bindings.iter().find(|b| b.keys == "i").unwrap();
        assert_eq!(i.footer, Some("input"));
        assert!(i.what.contains("cannot see the prompt"), "{}", i.what);
    }

    #[test]
    fn every_section_documents_the_help_key_or_is_a_prompt() {
        for section in SECTIONS {
            if section.title == "Prompts" || section.title == "Detail" {
                continue;
            }
            assert!(
                section.bindings.iter().any(|b| b.keys.contains('?')),
                "{} does not mention ?",
                section.title
            );
        }
    }

    /// Footers get one line, so keep them plausibly short.
    #[test]
    fn footers_fit_a_reasonable_terminal() {
        for section in SECTIONS {
            let line = footer(section);
            assert!(line.chars().count() < 110, "{}: {line:?}", section.title);
        }
    }
}
