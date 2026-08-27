package redact

import "testing"

const summary = `task: lint

Lint all source code

env:
  TELEGRAM_BOT_USERNAME: "Atlasiobot"
  CLOUDFLARE_EMAIL_API_TOKEN: "cfut_1Vz8mQQbIPz64ZlspkFhoNrWStb4Og"
  LOG_REDACT_PII: "false"
  PAGES_ENDPOINT: "http://localhost:4566"
  DATABASE_PASSWORD: "hunter2hunter2"

commands:
 - Task: api:check
`

func TestHarvestsCredentialsFromTheEnvDump(t *testing.T) {
	r := FromSummary(summary)
	if r.Len() != 2 {
		t.Fatalf("the token and the password, nothing else: got %d (%v)", r.Len(), r.secrets)
	}
	if got := r.Redact(
		"connecting with cfut_1Vz8mQQbIPz64ZlspkFhoNrWStb4Og now",
	); got != "connecting with [redacted] now" {
		t.Errorf("got %q", got)
	}
	if got := r.Redact("pw=hunter2hunter2"); got != "pw=[redacted]" {
		t.Errorf("got %q", got)
	}
}

// The disaster case: masking a value like `false` would replace the word everywhere it
// legitimately appears in the output.
func TestLeavesShortAndBooleanValuesAlone(t *testing.T) {
	r := FromSummary(summary)
	for _, line := range []string{
		"LOG_REDACT_PII was false all along",
		"posting to http://localhost:4566",
	} {
		if got := r.Redact(line); got != line {
			t.Errorf("Redact(%q) = %q", line, got)
		}
	}
}

// A non-secret key with a long value is not a credential.
func TestALongValueUnderAnInnocentKeyIsNotMasked(t *testing.T) {
	r := FromSummary(summary)
	if got := r.Redact("hello Atlasiobot"); got != "hello Atlasiobot" {
		t.Errorf("got %q", got)
	}
}

// Known credential shapes are masked whatever the variable is called.
func TestRecognisesCredentialsByTheirOwnShape(t *testing.T) {
	r := FromSummary("  HARMLESS_NAME: \"ghp_AAAABBBBCCCCDDDDEEEE\"\n")
	if got := r.Redact("token ghp_AAAABBBBCCCCDDDDEEEE"); got != "token [redacted]" {
		t.Errorf("got %q", got)
	}
}

// Masking a short secret first would leave the tail of a longer one exposed.
func TestMasksLongerSecretsFirst(t *testing.T) {
	r := New([]string{"abcdefgh", "abcdefghijklmnop"})
	if got := r.Redact("value abcdefghijklmnop"); got != "value [redacted]" {
		t.Errorf("got %q", got)
	}
}

func TestAnEmptyRedactorIsAPassthrough(t *testing.T) {
	if got := Empty().Redact("anything at all"); got != "anything at all" {
		t.Errorf("got %q", got)
	}
}
