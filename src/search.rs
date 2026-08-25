//! Searching captured output, live and archived.
//!
//! This uses ripgrep's own engine as a library — `grep-regex` and `grep-searcher` — rather
//! than shelling out to `rg`. The point is not raw speed; it is that one matcher covers
//! both corpora. The live run is in memory and the archive is on disk, and if those two
//! were searched by different code they would drift apart in case handling, regex dialect
//! and match semantics — the sort of difference a user notices and cannot articulate.

use crate::run::Run;
use crate::store::{self, Manifest};
use anyhow::Result;
use grep_matcher::Matcher;
use grep_regex::{RegexMatcher, RegexMatcherBuilder};
use grep_searcher::sinks::UTF8;
use grep_searcher::Searcher;
use std::path::Path;

pub struct Query {
    matcher: RegexMatcher,
    pub pattern: String,
}

impl Query {
    /// Smart case: a lowercase pattern is case-insensitive, a pattern with any uppercase
    /// is not. Same rule ripgrep uses, and the reason `fail` finds `FAIL` but `FAIL` does
    /// not drag in `fail`.
    pub fn new(pattern: &str) -> Result<Query> {
        let matcher = RegexMatcherBuilder::new()
            .case_smart(true)
            .line_terminator(Some(b'\n'))
            .build(pattern)?;
        Ok(Query {
            matcher,
            pattern: pattern.to_string(),
        })
    }

    pub fn matches(&self, text: &str) -> bool {
        self.matcher.is_match(text.as_bytes()).unwrap_or(false)
    }

    /// Byte range of the first match, for highlighting.
    pub fn first_match(&self, text: &str) -> Option<(usize, usize)> {
        self.matcher
            .find(text.as_bytes())
            .ok()
            .flatten()
            .map(|m| (m.start(), m.end()))
    }
}

/// A hit in the live run: which task, and which line of its buffer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LiveHit {
    pub task: String,
    pub index: usize,
}

/// Search the run in memory, in execution order so `n` walks the run the way it happened
/// rather than alphabetically.
pub fn search_run(run: &Run, q: &Query) -> Vec<LiveHit> {
    let mut order = run.order.clone();
    // Anything that produced output but never got into `order` still deserves searching.
    for name in run.tasks.keys() {
        if !order.contains(name) {
            order.push(name.clone());
        }
    }

    let mut hits = Vec::new();
    for task in order {
        let Some(t) = run.tasks.get(&task) else {
            continue;
        };
        for (index, line) in t.lines.iter().enumerate() {
            // Match the stripped text: a colour change landing mid-word would hide the
            // match from a search over the raw bytes.
            if q.matches(&line.plain) {
                hits.push(LiveHit {
                    task: task.clone(),
                    index,
                });
            }
        }
    }
    hits
}

#[derive(Debug, Clone)]
pub struct StoredHit {
    pub task: String,
    pub line_no: u64,
    pub text: String,
}

#[derive(Debug, Clone)]
pub struct RunHits {
    pub manifest: Manifest,
    pub hits: Vec<StoredHit>,
}

/// Search every stored run, newest first.
///
/// `max_per_run` caps how much of a single noisy run can crowd out the others; the count
/// of what was dropped is reported so a truncated result never reads as a complete one.
pub fn search_store(base: &Path, q: &Query, max_per_run: usize) -> (Vec<RunHits>, usize) {
    let mut out = Vec::new();
    let mut dropped = 0;

    for manifest in store::list(base) {
        let dir = store::run_dir(base, &manifest.id);
        let mut hits = Vec::new();

        for entry in &manifest.tasks {
            if entry.lines == 0 {
                continue;
            }
            // The `.txt` sidecar, not `.ansi`: searching escape sequences is how you miss
            // a match that happens to straddle a colour change.
            let path = dir.join(format!("{}.txt", entry.file));
            let task = entry.name.clone();
            let mut found: Vec<StoredHit> = Vec::new();
            let _ = Searcher::new().search_path(
                &q.matcher,
                &path,
                UTF8(|line_no, text| {
                    found.push(StoredHit {
                        task: task.clone(),
                        line_no,
                        text: text.trim_end().to_string(),
                    });
                    Ok(true)
                }),
            );
            hits.extend(found);
        }

        if hits.len() > max_per_run {
            dropped += hits.len() - max_per_run;
            hits.truncate(max_per_run);
        }
        if !hits.is_empty() {
            out.push(RunHits { manifest, hits });
        }
    }

    (out, dropped)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::run::Run;
    use std::fs;
    use std::path::PathBuf;

    fn temp(tag: &str) -> PathBuf {
        let p = std::env::temp_dir().join(format!("taskui-search-{}-{tag}", std::process::id()));
        let _ = fs::remove_dir_all(&p);
        fs::create_dir_all(&p).unwrap();
        p
    }

    fn run_with(lines: &[(&str, &str)]) -> Run {
        let mut r = Run::detached("all", Run::graph_from(&[("all", &["a", "b"])]));
        for (task, text) in lines {
            r.feed(task, text);
        }
        r
    }

    #[test]
    fn finds_matches_across_tasks() {
        let run = run_with(&[
            ("a", "compiling"),
            ("a", "error: boom"),
            ("b", "warning: unused"),
            ("b", "error: also boom"),
        ]);
        let hits = search_run(&run, &Query::new("error").unwrap());
        assert_eq!(
            hits,
            [
                LiveHit {
                    task: "a".into(),
                    index: 1
                },
                LiveHit {
                    task: "b".into(),
                    index: 1
                },
            ]
        );
    }

    /// Hits come back in execution order, so stepping through them walks the run the way
    /// it happened rather than alphabetically.
    #[test]
    fn hits_follow_execution_order_not_task_name_order() {
        let mut r = Run::detached("all", Run::graph_from(&[("all", &["z", "a"])]));
        r.feed("z", "error: first");
        r.feed("a", "error: second");
        let hits = search_run(&r, &Query::new("error").unwrap());
        assert_eq!(hits[0].task, "z", "z ran first, so its hit comes first");
        assert_eq!(hits[1].task, "a");
    }

    /// Lowercase searches loosely, any uppercase searches exactly — ripgrep's rule.
    #[test]
    fn smart_case() {
        let run = run_with(&[
            ("a", "--- FAIL: TestOrderTotal"),
            ("a", "failed to connect"),
        ]);
        assert_eq!(search_run(&run, &Query::new("fail").unwrap()).len(), 2);
        assert_eq!(search_run(&run, &Query::new("FAIL").unwrap()).len(), 1);
    }

    #[test]
    fn regex_not_just_literals() {
        let run = run_with(&[("a", "3 migrations pending"), ("a", "0 migrations pending")]);
        let hits = search_run(&run, &Query::new(r"[1-9]\d* migrations pending").unwrap());
        assert_eq!(hits.len(), 1);
        assert_eq!(hits[0].index, 0);
    }

    /// Searching the raw bytes would miss this: the escape sequence splits the word.
    #[test]
    fn matches_through_colour_codes() {
        let mut r = Run::detached("a", Run::graph_from(&[("a", &[])]));
        r.feed("a", "\x1b[31merr\x1b[0mor: boom");
        let hits = search_run(&r, &Query::new("error").unwrap());
        assert_eq!(hits.len(), 1, "the match spans a colour change");
    }

    #[test]
    fn first_match_gives_a_highlight_range() {
        let q = Query::new("boom").unwrap();
        assert_eq!(q.first_match("error: boom"), Some((7, 11)));
        assert_eq!(q.first_match("nothing here"), None);
    }

    /// The same query, run over the archive, must find the same thing it found live.
    #[test]
    fn the_same_query_works_on_stored_runs() {
        let base = temp("stored");
        let mut r = run_with(&[("a", "error: boom"), ("b", "all clear")]);
        r.finish(1);
        store::save(&base, Path::new("/proj"), &r).unwrap();

        let (results, dropped) = search_store(&base, &Query::new("error").unwrap(), 100);
        assert_eq!(dropped, 0);
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].manifest.root, "all");
        assert_eq!(results[0].hits.len(), 1);
        assert_eq!(results[0].hits[0].task, "a");
        assert_eq!(results[0].hits[0].text, "error: boom");
        assert_eq!(results[0].hits[0].line_no, 1);
    }

    /// A run with no hits is absent rather than present-and-empty.
    #[test]
    fn runs_without_hits_are_omitted() {
        let base = temp("nohits");
        let mut r = run_with(&[("a", "all clear")]);
        r.finish(0);
        store::save(&base, Path::new("/proj"), &r).unwrap();

        let (results, _) = search_store(&base, &Query::new("error").unwrap(), 100);
        assert!(results.is_empty());
    }

    /// Truncation has to be reported, or a capped result reads as a complete one.
    #[test]
    fn per_run_truncation_is_counted() {
        let base = temp("trunc");
        let mut r = Run::detached("all", Run::graph_from(&[("all", &[])]));
        for i in 0..10 {
            r.feed("all", &format!("error {i}"));
        }
        r.finish(1);
        store::save(&base, Path::new("/proj"), &r).unwrap();

        let (results, dropped) = search_store(&base, &Query::new("error").unwrap(), 4);
        assert_eq!(results[0].hits.len(), 4);
        assert_eq!(dropped, 6);
    }

    #[test]
    fn an_invalid_pattern_is_an_error_not_a_panic() {
        assert!(Query::new("(unclosed").is_err());
    }
}
