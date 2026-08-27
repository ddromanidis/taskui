// Package search searches captured output, live and archived.
//
// This uses Go's own `regexp` and a line loop over `bufio` rather than shelling out to a
// grep. The point is not raw speed; it is that one matcher covers both corpora. The live
// run is in memory and the archive is on disk, and if those two were searched by different
// code they would drift apart in case handling, regex dialect and match semantics — the
// sort of difference a user notices and cannot articulate.
package search

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ddromanidis/taskui/internal/run"
	"github.com/ddromanidis/taskui/internal/store"
)

type Query struct {
	re *regexp.Regexp
	// Pattern is what the user typed, for the header.
	Pattern string
}

// NewQuery compiles a pattern with smart case: a lowercase pattern is case-insensitive, a
// pattern with any uppercase is not. Same rule ripgrep uses, and the reason `fail` finds
// `FAIL` but `FAIL` does not drag in `fail`.
func NewQuery(pattern string) (*Query, error) {
	source := pattern
	if !hasUpper(pattern) {
		source = "(?i)" + pattern
	}
	re, err := regexp.Compile(source)
	if err != nil {
		return nil, err
	}
	return &Query{re: re, Pattern: pattern}, nil
}

// hasUpper reports whether the pattern contains an uppercase literal.
//
// Escapes are skipped rather than inspected: `\W` and `\S` are classes, not capitals, and
// counting them would silently make an all-lowercase pattern case-sensitive.
func hasUpper(pattern string) bool {
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' {
			i++
			continue
		}
		if runes[i] >= 'A' && runes[i] <= 'Z' {
			return true
		}
	}
	return false
}

func (q *Query) Matches(text string) bool { return q.re.MatchString(text) }

// FirstMatch is the byte range of the first match, for highlighting.
func (q *Query) FirstMatch(text string) (int, int, bool) {
	loc := q.re.FindStringIndex(text)
	if loc == nil {
		return 0, 0, false
	}
	return loc[0], loc[1], true
}

// LiveHit is a hit in the live run: which task, and which line of its buffer.
type LiveHit struct {
	Task  string
	Index int
}

// InRun searches the run in memory, in execution order so `n` walks the run the way it
// happened rather than alphabetically.
func InRun(r *run.Run, q *Query) []LiveHit {
	order := append([]string(nil), r.Order...)
	// Anything that produced output but never got into Order still deserves searching.
	seen := map[string]bool{}
	for _, n := range order {
		seen[n] = true
	}
	for _, name := range r.TaskNames() {
		if !seen[name] {
			order = append(order, name)
		}
	}

	var hits []LiveHit
	for _, task := range order {
		t, ok := r.Tasks[task]
		if !ok {
			continue
		}
		for index, line := range t.Lines {
			// Match the stripped text: a colour change landing mid-word would hide the
			// match from a search over the raw bytes.
			if q.Matches(line.Plain) {
				hits = append(hits, LiveHit{Task: task, Index: index})
			}
		}
	}
	return hits
}

type StoredHit struct {
	Task   string
	LineNo uint64
	Text   string
}

type RunHits struct {
	Manifest store.Manifest
	Hits     []StoredHit
}

// InStore searches every stored run, newest first.
//
// maxPerRun caps how much of a single noisy run can crowd out the others; the count of
// what was dropped is reported so a truncated result never reads as a complete one.
func InStore(base string, q *Query, maxPerRun int) ([]RunHits, int) {
	var out []RunHits
	dropped := 0

	for _, manifest := range store.List(base) {
		dir := store.RunDir(base, manifest.ID)
		var hits []StoredHit

		for _, entry := range manifest.Tasks {
			if entry.Lines == 0 {
				continue
			}
			// The `.txt` sidecar, not `.ansi`: searching escape sequences is how you miss
			// a match that happens to straddle a colour change.
			path := filepath.Join(dir, entry.File+".txt")
			hits = append(hits, searchFile(path, entry.Name, q)...)
		}

		if len(hits) > maxPerRun {
			dropped += len(hits) - maxPerRun
			hits = hits[:maxPerRun]
		}
		if len(hits) > 0 {
			out = append(out, RunHits{Manifest: manifest, Hits: hits})
		}
	}

	return out, dropped
}

// searchFile walks one archived task's output.
//
// ReadString rather than [bufio.Scanner]: Scanner caps a token at 64KB and stops the file's
// search there, and a run that dumped a minified bundle or a base64 blob on one line would
// silently lose everything after it.
func searchFile(path, task string, q *Query) []StoredHit {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var hits []StoredHit
	reader := bufio.NewReader(f)
	var lineNo uint64
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lineNo++
			text := strings.TrimRight(line, "\r\n")
			if q.Matches(text) {
				hits = append(hits, StoredHit{Task: task, LineNo: lineNo, Text: strings.TrimRight(text, " \t")})
			}
		}
		if err != nil {
			// EOF or a read error: either way there is nothing left to search.
			return hits
		}
	}
}
