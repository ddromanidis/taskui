//! Colours, loaded from `config.yaml`.
//!
//! Every colour the UI draws comes from here rather than a literal at the call site, so
//! "make it configurable" is a matter of adding a field rather than hunting through the
//! rendering code. Anything the file does not mention keeps its default, which means a
//! two-line config is valid and an empty one is a no-op.

use ratatui::style::Color;
use serde::Deserialize;
use std::path::{Path, PathBuf};

/// `$XDG_CONFIG_HOME/taskui/config.yaml`, else `~/.config/taskui/config.yaml`.
pub fn config_path() -> PathBuf {
    if let Ok(x) = std::env::var("XDG_CONFIG_HOME") {
        if !x.is_empty() {
            return PathBuf::from(x).join("taskui/config.yaml");
        }
    }
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".into());
    PathBuf::from(home).join(".config/taskui/config.yaml")
}

/// A colour written as a name, a `#rrggbb`, or a 0–255 terminal palette index.
///
/// Names are the sixteen ANSI colours, so a value like `blue` follows whatever the
/// terminal's own scheme says blue is — which is usually what someone means when they set
/// a colour in a terminal program. `#rrggbb` opts out of that and pins it exactly.
fn parse_color(text: &str) -> Option<Color> {
    let t = text.trim().to_ascii_lowercase();

    if let Some(hex) = t.strip_prefix('#') {
        if hex.len() != 6 {
            return None;
        }
        let r = u8::from_str_radix(&hex[0..2], 16).ok()?;
        let g = u8::from_str_radix(&hex[2..4], 16).ok()?;
        let b = u8::from_str_radix(&hex[4..6], 16).ok()?;
        return Some(Color::Rgb(r, g, b));
    }

    if let Ok(n) = t.parse::<u8>() {
        return Some(Color::Indexed(n));
    }

    Some(match t.replace(['-', '_'], "").as_str() {
        "reset" | "default" => Color::Reset,
        "black" => Color::Black,
        "red" => Color::Red,
        "green" => Color::Green,
        "yellow" => Color::Yellow,
        "blue" => Color::Blue,
        "magenta" | "purple" => Color::Magenta,
        "cyan" => Color::Cyan,
        "gray" | "grey" | "white" => Color::Gray,
        "darkgray" | "darkgrey" => Color::DarkGray,
        "lightred" | "brightred" => Color::LightRed,
        "lightgreen" | "brightgreen" => Color::LightGreen,
        "lightyellow" | "brightyellow" => Color::LightYellow,
        "lightblue" | "brightblue" => Color::LightBlue,
        "lightmagenta" | "brightmagenta" => Color::LightMagenta,
        "lightcyan" | "brightcyan" => Color::LightCyan,
        "lightwhite" | "brightwhite" => Color::White,
        _ => return None,
    })
}

/// Render a colour back to something [`parse_color`] accepts.
fn color_name(c: Color) -> String {
    match c {
        Color::Reset => "default".into(),
        Color::Black => "black".into(),
        Color::Red => "red".into(),
        Color::Green => "green".into(),
        Color::Yellow => "yellow".into(),
        Color::Blue => "blue".into(),
        Color::Magenta => "magenta".into(),
        Color::Cyan => "cyan".into(),
        Color::Gray => "gray".into(),
        Color::DarkGray => "dark-gray".into(),
        Color::LightRed => "bright-red".into(),
        Color::LightGreen => "bright-green".into(),
        Color::LightYellow => "bright-yellow".into(),
        Color::LightBlue => "bright-blue".into(),
        Color::LightMagenta => "bright-magenta".into(),
        Color::LightCyan => "bright-cyan".into(),
        Color::White => "bright-white".into(),
        Color::Rgb(r, g, b) => format!("\"#{r:02x}{g:02x}{b:02x}\""),
        Color::Indexed(n) => n.to_string(),
    }
}

macro_rules! theme {
    ($($field:ident => $default:expr, $doc:literal;)*) => {
        /// Every colour the UI uses.
        #[derive(Debug, Clone, PartialEq)]
        pub struct Theme {
            $(
                #[doc = $doc]
                pub $field: Color,
            )*
        }

        impl Default for Theme {
            fn default() -> Self {
                Theme { $($field: $default,)* }
            }
        }

        /// The `colors:` block. Every key optional; anything absent keeps its default.
        #[derive(Debug, Default, Deserialize)]
        #[serde(deny_unknown_fields, rename_all = "kebab-case")]
        struct ColorsFile {
            $(pub $field: Option<String>,)*
        }

        impl Theme {
            fn apply(&mut self, file: &ColorsFile) -> Vec<String> {
                let mut bad = Vec::new();
                $(
                    if let Some(text) = &file.$field {
                        match parse_color(text) {
                            Some(c) => self.$field = c,
                            None => bad.push(format!(
                                "{}: {:?} is not a colour",
                                stringify!($field).replace('_', "-"),
                                text
                            )),
                        }
                    }
                )*
                bad
            }

            /// An annotated `config.yaml` holding this theme's current values.
            ///
            /// Emitted with real values rather than blanks so `--dump-config >
            /// config.yaml` gives you a file you can edit, not a form to fill in.
            pub fn to_yaml(&self) -> String {
                let mut out = String::from("colors:\n");
                $(
                    out.push_str(&format!(
                        "  # {}\n  {}: {}\n",
                        $doc,
                        stringify!($field).replace('_', "-"),
                        color_name(self.$field),
                    ));
                )*
                out
            }
        }
    };
}

theme! {
    accent => Color::Cyan, "The `taskui` word mark.";
    dim => Color::Gray, "Secondary text: counts, descriptions, key hints.";
    text => Color::Reset, "Ordinary output and task names.";
    selection => Color::Reset, "Highlighted row. `default` means reverse video, which is legible in every terminal theme; any other colour is used as a background instead.";

    mode => Color::Yellow, "The active pivot in the picker header.";
    alias => Color::Blue, "Task aliases.";
    danger => Color::Red, "The ⚠ marker on tasks that need a confirmation.";

    status_ok => Color::Green, "A task that succeeded.";
    status_running => Color::Yellow, "A task still going.";
    status_failed => Color::Red, "A task that failed.";
    status_skipped => Color::Gray, "A task never reached.";
    status_pending => Color::Gray, "A task not started yet.";

    command => Color::Blue, "go-task's own `task: [name] <cmd>` echo.";
    gutter => Color::Gray, "Line numbers and fold glyphs.";

    search => Color::Magenta, "The search and filter prompts.";
    match_fg => Color::Black, "Foreground of a highlighted match.";
    match_bg => Color::Magenta, "Background of a highlighted match.";

    notice => Color::Yellow, "Status messages along the bottom.";
    stored => Color::Blue, "Marks a run loaded from history.";
    interactive => Color::Green, "The interactive-mode marker and input bar.";
    warning_fg => Color::Black, "Foreground of the waiting-for-input bar.";
    warning_bg => Color::Yellow, "Background of the waiting-for-input bar.";
    confirm_fg => Color::Black, "Foreground of the production confirmation bar.";
    confirm_bg => Color::Red, "Background of the production confirmation bar.";
}

#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfigFile {
    /// Action name -> the key that triggers it.
    #[serde(default)]
    keys: std::collections::BTreeMap<String, String>,
    #[serde(default)]
    colors: ColorsFile,
    /// Accepted as a synonym so neither spelling is wrong.
    #[serde(default)]
    colours: ColorsFile,
}

#[derive(Debug, Clone, Default)]
pub struct Config {
    pub theme: Theme,
    pub keymap: crate::keys::Keymap,
    /// Anything wrong with the file, reported in the UI rather than swallowed — a colour
    /// that silently does nothing is worse than one that says why.
    pub problems: Vec<String>,
}

/// Point actions at different keys. Unknown names and multi-character values are
/// reported rather than ignored — a rebinding that silently does nothing is worse than
/// one that says why.
fn apply_keys(
    keymap: &mut crate::keys::Keymap,
    keys: &std::collections::BTreeMap<String, String>,
) -> Vec<String> {
    let mut problems = Vec::new();
    for (name, value) in keys {
        let Some(action) = crate::keys::action_by_name(name) else {
            problems.push(format!("keys: `{name}` is not an action"));
            continue;
        };
        let mut chars = value.chars();
        match (chars.next(), chars.next()) {
            (Some(c), None) => keymap.rebind(action, c),
            _ => problems.push(format!(
                "keys: {name} must be a single character, not {value:?}"
            )),
        }
    }
    problems
}

impl Config {
    /// Read `path`. A missing file is not an error; it is the normal case.
    pub fn load(path: &Path) -> Config {
        let mut config = Config::default();
        let Ok(text) = std::fs::read_to_string(path) else {
            return config;
        };
        match serde_yaml_ng::from_str::<ConfigFile>(&text) {
            Ok(file) => {
                config.problems.extend(config.theme.apply(&file.colors));
                config.problems.extend(config.theme.apply(&file.colours));
                config
                    .problems
                    .extend(apply_keys(&mut config.keymap, &file.keys));
                config.problems.extend(config.keymap.conflicts());
            }
            Err(e) => config.problems.push(format!("{}: {e}", path.display())),
        }
        config
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// One file per call: the suite runs in parallel, and a shared path means the tests
    /// overwrite each other's config.
    fn load_str(tag: &str, yaml: &str) -> Config {
        let dir = std::env::temp_dir().join(format!("taskui-theme-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join(format!("{tag}.yaml"));
        std::fs::write(&path, yaml).unwrap();
        let c = Config::load(&path);
        let _ = std::fs::remove_file(&path);
        c
    }

    #[test]
    fn colour_names_hex_and_indices_all_parse() {
        assert_eq!(parse_color("red"), Some(Color::Red));
        assert_eq!(parse_color("  Cyan "), Some(Color::Cyan));
        assert_eq!(parse_color("bright-blue"), Some(Color::LightBlue));
        assert_eq!(parse_color("purple"), Some(Color::Magenta));
        assert_eq!(parse_color("#1e2030"), Some(Color::Rgb(0x1e, 0x20, 0x30)));
        assert_eq!(parse_color("240"), Some(Color::Indexed(240)));
        assert_eq!(parse_color("chartreuse"), None);
        assert_eq!(parse_color("#abc"), None);
    }

    /// A missing file is the normal case, not an error.
    #[test]
    fn no_config_means_defaults() {
        let c = Config::load(Path::new("/definitely/not/here/config.yaml"));
        assert_eq!(c.theme, Theme::default());
        assert!(c.problems.is_empty());
    }

    #[test]
    fn only_the_named_colours_change() {
        let c = load_str(
            "named",
            "colors:\n  accent: magenta\n  status-failed: '#ff5555'\n",
        );
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.theme.accent, Color::Magenta);
        assert_eq!(c.theme.status_failed, Color::Rgb(0xff, 0x55, 0x55));
        // Untouched keys keep their defaults.
        assert_eq!(c.theme.status_ok, Theme::default().status_ok);
        assert_eq!(c.theme.dim, Theme::default().dim);
    }

    /// Both spellings work, because arguing about it would be sillier than supporting it.
    #[test]
    fn colours_is_accepted_too() {
        let c = load_str("colours", "colours:\n  accent: green\n");
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.theme.accent, Color::Green);
    }

    /// A colour that silently does nothing is worse than one that says why.
    #[test]
    fn a_bad_colour_is_reported_and_the_rest_still_applies() {
        let c = load_str("bad", "colors:\n  accent: chartreuse\n  mode: red\n");
        assert_eq!(c.theme.mode, Color::Red, "the good one still took");
        assert_eq!(c.theme.accent, Theme::default().accent);
        assert_eq!(c.problems.len(), 1);
        assert!(c.problems[0].contains("chartreuse"), "{:?}", c.problems);
    }

    /// A typo'd key is a typo, not a silently ignored line.
    #[test]
    fn an_unknown_key_is_reported() {
        let c = load_str("unknown", "colors:\n  acccent: red\n");
        assert_eq!(c.problems.len(), 1);
        assert!(c.problems[0].contains("acccent"), "{:?}", c.problems);
    }

    /// The defaults have to be legible on a terminal whose theme we know nothing about.
    /// `DarkGray` is ANSI *bright black* — a hair off the background in most dark
    /// colourschemes — and it paints nearly all the secondary text.
    #[test]
    fn no_default_is_bright_black() {
        let d = Theme::default();
        for (name, colour) in [
            ("dim", d.dim),
            ("gutter", d.gutter),
            ("status_skipped", d.status_skipped),
            ("status_pending", d.status_pending),
            ("text", d.text),
        ] {
            assert_ne!(
                colour,
                Color::DarkGray,
                "{name} is invisible on a dark theme"
            );
        }
    }

    /// A fixed background colour looks deliberate on the theme it was chosen against and
    /// vanishes on every other one, so the default is reverse video.
    #[test]
    fn the_selection_defaults_to_reverse_video() {
        assert_eq!(Theme::default().selection, Color::Reset);
        let c = load_str("sel", "colors:\n  selection: '#1e2030'\n");
        assert_eq!(
            c.theme.selection,
            Color::Rgb(0x1e, 0x20, 0x30),
            "still overridable"
        );
    }

    #[test]
    fn keys_can_be_rebound() {
        let c = load_str("keys", "keys:\n  pivot: P\n  filter-matches: z\n");
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.keymap.picker('P'), Some(crate::keys::Action::Pivot));
        assert_eq!(c.keymap.picker('p'), None, "the old key is free");
        assert_eq!(c.keymap.run('z'), Some(crate::keys::Action::FilterMatches));
    }

    /// A rebinding that silently does nothing is worse than one that says why.
    #[test]
    fn bad_key_settings_are_reported() {
        let c = load_str("badkeys", "keys:\n  nonsense: x\n  pivot: pp\n");
        assert_eq!(c.problems.len(), 2, "{:?}", c.problems);
        assert!(c.problems.iter().any(|p| p.contains("nonsense")));
        assert!(c.problems.iter().any(|p| p.contains("single character")));
    }

    /// A key bound twice shadows rather than doing both, so say so.
    #[test]
    fn colliding_keys_are_reported() {
        let c = load_str("collide", "keys:\n  pivot: a\n");
        assert!(
            c.problems.iter().any(|p| p.contains("both")),
            "{:?}",
            c.problems
        );
    }

    /// `--dump-config` must produce a file taskui can read back unchanged, or it is a
    /// template that lies about the defaults.
    #[test]
    fn the_dumped_config_round_trips() {
        let dumped = Theme::default().to_yaml();
        let c = load_str("roundtrip", &dumped);
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.theme, Theme::default());
    }

    /// Including the non-name colours.
    #[test]
    fn rgb_and_indexed_values_round_trip() {
        let theme = Theme {
            accent: Color::Rgb(0x1e, 0x20, 0x30),
            dim: Color::Indexed(240),
            ..Theme::default()
        };
        let c = load_str("roundtrip2", &theme.to_yaml());
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.theme.accent, Color::Rgb(0x1e, 0x20, 0x30));
        assert_eq!(c.theme.dim, Color::Indexed(240));
    }

    #[test]
    fn an_empty_config_is_valid() {
        let c = load_str("empty", "colors: {}\n");
        assert!(c.problems.is_empty());
        assert_eq!(c.theme, Theme::default());
    }

    /// Every field is settable — the macro is the whole point, so nothing gets forgotten.
    #[test]
    fn every_key_is_configurable() {
        // Derived from the dump, so a field added to the table is covered automatically.
        let keys: Vec<String> = Theme::default()
            .to_yaml()
            .lines()
            .filter_map(|l| {
                l.trim()
                    .strip_suffix(|_: char| true)
                    .and_then(|_| l.trim().split_once(':'))
            })
            .map(|(k, _)| k.to_string())
            .filter(|k| k != "colors")
            .collect();
        assert!(keys.len() > 20, "found {} keys", keys.len());
        let yaml: String = std::iter::once("colors:".to_string())
            .chain(keys.iter().map(|k| format!("  {k}: red")))
            .collect::<Vec<_>>()
            .join("\n");
        let c = load_str("every", &yaml);
        assert!(c.problems.is_empty(), "{:?}", c.problems);
        assert_eq!(c.theme.accent, Color::Red);
        assert_eq!(c.theme.confirm_bg, Color::Red);
        assert_eq!(c.theme.selection, Color::Red);
    }
}
