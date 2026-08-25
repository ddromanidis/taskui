//! Masking credentials out of captured output.
//!
//! A tool whose point is keeping run output around is a tool that writes whatever scrolled
//! past into a file you will later grep. For a Taskfile with `dotenv:`, that includes API
//! tokens — any command echoing its environment prints them, and `task --summary` prints
//! them unprompted.
//!
//! Redaction happens in the capture thread, before a line is ever handed to the UI, so
//! nothing unmasked reaches the screen, the buffers, or the disk. The secrets themselves
//! come from the `--summary` env dump we already run to build the graph: the leak is also
//! the best available list of what to plug.

use std::borrow::Cow;

pub const MARKER: &str = "[redacted]";

/// Substrings never to let through.
#[derive(Debug, Default, Clone)]
pub struct Redactor {
    /// Longest first, so masking a value never leaves a fragment of a longer one behind.
    secrets: Vec<String>,
}

/// Key fragments that mark a value as a credential. Matched case-insensitively against
/// the variable name.
const SECRET_KEYS: [&str; 12] = [
    "token",
    "secret",
    "password",
    "passwd",
    "credential",
    "private",
    "apikey",
    "api_key",
    "access_key",
    "auth",
    "session",
    "dsn",
];

/// Value prefixes that are credentials regardless of what the variable is called.
const SECRET_VALUES: [&str; 6] = ["cfut_", "sk-", "ghp_", "github_pat_", "AKIA", "-----BEGIN"];

/// Values this short, or this boolean, are not worth masking — and masking them would
/// wreck the output, since `false` and `1` appear everywhere.
fn worth_masking(value: &str) -> bool {
    if value.len() < 8 {
        return false;
    }
    if value.chars().all(|c| c.is_ascii_digit()) {
        return false;
    }
    !matches!(
        value.to_ascii_lowercase().as_str(),
        "true" | "false" | "localhost" | "development" | "production"
    )
}

fn is_secret(key: &str, value: &str) -> bool {
    if SECRET_VALUES.iter().any(|p| value.starts_with(p)) {
        return true;
    }
    let k = key.to_ascii_lowercase();
    // `KEY` on its own is too broad — it matches `TELEGRAM_MINI_APP_NAME`-style keys far
    // less often than it matches genuine ones, but `MONKEY`/`KEYBOARD` would slip in — so
    // it is only honoured at a word boundary.
    let keyish = SECRET_KEYS.iter().any(|s| k.contains(s))
        || k.split(|c: char| !c.is_alphanumeric()).any(|w| w == "key");
    keyish && worth_masking(value)
}

impl Redactor {
    pub fn empty() -> Self {
        Redactor::default()
    }

    /// Harvest from the `KEY: "value"` block that `task --summary` prints.
    pub fn from_summary(text: &str) -> Self {
        let mut secrets = Vec::new();
        for line in text.lines() {
            // Env entries are indented `  KEY: "value"`; the ` - x` items and the section
            // headers are not.
            let trimmed = line.trim();
            if trimmed.starts_with('-') || !line.starts_with("  ") {
                continue;
            }
            // Split on the first colon only: values routinely contain more of them.
            let Some((key, value)) = trimmed.split_once(':') else {
                continue;
            };
            let value = value.trim().trim_matches('"');
            if is_secret(key.trim(), value) {
                secrets.push(value.to_string());
            }
        }
        Redactor::new(secrets)
    }

    pub fn new(mut secrets: Vec<String>) -> Self {
        secrets.retain(|s| !s.is_empty());
        secrets.sort_by_key(|s| std::cmp::Reverse(s.len()));
        secrets.dedup();
        Redactor { secrets }
    }

    pub fn len(&self) -> usize {
        self.secrets.len()
    }

    /// Replace every known secret with [`MARKER`]. Borrows when there is nothing to do,
    /// which is the overwhelmingly common case.
    pub fn redact<'a>(&self, line: &'a str) -> Cow<'a, str> {
        if self.secrets.is_empty() || !self.secrets.iter().any(|s| line.contains(s.as_str())) {
            return Cow::Borrowed(line);
        }
        let mut out = line.to_string();
        for secret in &self.secrets {
            if out.contains(secret.as_str()) {
                out = out.replace(secret.as_str(), MARKER);
            }
        }
        Cow::Owned(out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SUMMARY: &str = r#"task: lint

Lint all source code

env:
  TELEGRAM_BOT_USERNAME: "Atlasiobot"
  CLOUDFLARE_EMAIL_API_TOKEN: "cfut_1Vz8mQQbIPz64ZlspkFhoNrWStb4Og"
  LOG_REDACT_PII: "false"
  PAGES_ENDPOINT: "http://localhost:4566"
  DATABASE_PASSWORD: "hunter2hunter2"

commands:
 - Task: api:check
"#;

    #[test]
    fn harvests_credentials_from_the_env_dump() {
        let r = Redactor::from_summary(SUMMARY);
        assert_eq!(r.len(), 2, "the token and the password, nothing else");
        assert_eq!(
            r.redact("connecting with cfut_1Vz8mQQbIPz64ZlspkFhoNrWStb4Og now"),
            "connecting with [redacted] now"
        );
        assert_eq!(r.redact("pw=hunter2hunter2"), "pw=[redacted]");
    }

    /// The disaster case: masking a value like `false` would replace the word everywhere
    /// it legitimately appears in the output.
    #[test]
    fn leaves_short_and_boolean_values_alone() {
        let r = Redactor::from_summary(SUMMARY);
        assert_eq!(
            r.redact("LOG_REDACT_PII was false all along"),
            "LOG_REDACT_PII was false all along"
        );
        assert_eq!(
            r.redact("posting to http://localhost:4566"),
            "posting to http://localhost:4566"
        );
    }

    /// A non-secret key with a long value is not a credential.
    #[test]
    fn a_long_value_under_an_innocent_key_is_not_masked() {
        let r = Redactor::from_summary(SUMMARY);
        assert_eq!(r.redact("hello Atlasiobot"), "hello Atlasiobot");
    }

    /// Known credential shapes are masked whatever the variable is called.
    #[test]
    fn recognises_credentials_by_their_own_shape() {
        let r = Redactor::from_summary("  HARMLESS_NAME: \"ghp_AAAABBBBCCCCDDDDEEEE\"\n");
        assert_eq!(
            r.redact("token ghp_AAAABBBBCCCCDDDDEEEE"),
            "token [redacted]"
        );
    }

    /// Masking a short secret first would leave the tail of a longer one exposed.
    #[test]
    fn masks_longer_secrets_first() {
        let r = Redactor::new(vec!["abcdefgh".into(), "abcdefghijklmnop".into()]);
        assert_eq!(r.redact("value abcdefghijklmnop"), "value [redacted]");
    }

    #[test]
    fn borrows_when_there_is_nothing_to_mask() {
        let r = Redactor::new(vec!["sekrit-value".into()]);
        assert!(matches!(r.redact("ordinary output"), Cow::Borrowed(_)));
    }

    #[test]
    fn an_empty_redactor_is_a_passthrough() {
        let r = Redactor::empty();
        assert_eq!(r.redact("anything at all"), "anything at all");
    }
}
