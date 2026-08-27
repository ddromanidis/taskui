// Package redact masks credentials out of captured output.
//
// A tool whose point is keeping run output around is a tool that writes whatever scrolled
// past into a file you will later grep. For a Taskfile with `dotenv:`, that includes API
// tokens — any command echoing its environment prints them, and `task --summary` prints
// them unprompted.
//
// Redaction happens in the capture goroutine, before a line is ever handed to the UI, so
// nothing unmasked reaches the screen, the buffers, or the disk. The secrets themselves
// come from the `--summary` env dump we already run to build the graph: the leak is also
// the best available list of what to plug.
package redact

import (
	"slices"
	"sort"
	"strings"
)

// Marker is what a masked value is replaced with.
const Marker = "[redacted]"

// Redactor holds substrings never to let through.
type Redactor struct {
	// Longest first, so masking a value never leaves a fragment of a longer one behind.
	secrets []string
}

// secretKeys are key fragments that mark a value as a credential. Matched
// case-insensitively against the variable name.
var secretKeys = [...]string{
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
}

// secretValues are value prefixes that are credentials regardless of what the variable is
// called.
var secretValues = [...]string{"cfut_", "sk-", "ghp_", "github_pat_", "AKIA", "-----BEGIN"}

// worthMasking rejects values this short, or this boolean: masking them would wreck the
// output, since `false` and `1` appear everywhere.
func worthMasking(value string) bool {
	if len(value) < 8 {
		return false
	}
	digits := true
	for _, c := range value {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		return false
	}
	switch strings.ToLower(value) {
	case "true", "false", "localhost", "development", "production":
		return false
	}
	return true
}

func isSecret(key, value string) bool {
	for _, p := range secretValues {
		if strings.HasPrefix(value, p) {
			return true
		}
	}
	k := strings.ToLower(key)
	keyish := false
	for _, s := range secretKeys {
		if strings.Contains(k, s) {
			keyish = true
			break
		}
	}
	if !keyish {
		// `KEY` on its own is too broad — `MONKEY`/`KEYBOARD` would slip in — so it is
		// only honoured at a word boundary.
		if slices.Contains(strings.FieldsFunc(k, func(c rune) bool {
			return (c < 'a' || c > 'z') && (c < '0' || c > '9')
		}), "key") {
			keyish = true
		}
	}
	return keyish && worthMasking(value)
}

// Empty is a passthrough redactor.
func Empty() *Redactor { return &Redactor{} }

// FromSummary harvests from the `KEY: "value"` block that `task --summary` prints.
func FromSummary(text string) *Redactor {
	var secrets []string
	for line := range strings.SplitSeq(text, "\n") {
		// Env entries are indented `  KEY: "value"`; the ` - x` items and the section
		// headers are not.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-") || !strings.HasPrefix(line, "  ") {
			continue
		}
		// Split on the first colon only: values routinely contain more of them.
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if isSecret(strings.TrimSpace(key), value) {
			secrets = append(secrets, value)
		}
	}
	return New(secrets)
}

func New(secrets []string) *Redactor {
	kept := make([]string, 0, len(secrets))
	seen := map[string]bool{}
	for _, s := range secrets {
		if s != "" && !seen[s] {
			seen[s] = true
			kept = append(kept, s)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	return &Redactor{secrets: kept}
}

// Len is how many distinct secrets are being masked.
func (r *Redactor) Len() int { return len(r.secrets) }

// Redact replaces every known secret with Marker.
func (r *Redactor) Redact(line string) string {
	if len(r.secrets) == 0 {
		return line
	}
	out := line
	for _, secret := range r.secrets {
		if strings.Contains(out, secret) {
			out = strings.ReplaceAll(out, secret, Marker)
		}
	}
	return out
}
