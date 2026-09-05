package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/ddromanidis/taskui/internal/pivot"
)

// A project's own config, and the one rule that makes it safe.
//
// `pin:` and `pivots:` are statements about a particular task list — which tasks are the
// ones you actually run, and how this repository's names group — and they have been living
// in a single global file, where they apply to every project you open. `.taskui-danger` and
// `.taskui-cover` already established that a repository may say things about itself.
//
// The rule is that a project may **add, never override**. Config in a repository is config
// you did not write and did not read: a theme from somebody else's checkout taking over
// your terminal, or their `keys:` block moving `q` while you are holding it, is a surprise
// and not a feature. Extra pivots and extra pins are additive, visible in the header the
// moment they take effect, and cost you nothing if you ignore them. Everything else is
// refused out loud — a key that silently did nothing would be worse than one that says why.

// ProjectFiles are the names a project's own config may be written under, in the order they
// are looked for. Two spellings because YAML in the wild uses both, and a file that is
// ignored for its extension is a bug report.
var ProjectFiles = []string{".taskui.yaml", ".taskui.yml"}

// projectKeys are the only top-level keys a project file may set.
var projectKeys = map[string]bool{"pivots": true, "pin": true}

// Project is what a repository says about how its own task list should be read.
type Project struct {
	Pivots []pivot.Spec
	Pins   []string
	// Path is the file it came from, empty when there was none.
	Path     string
	Problems []string
}

// LoadProject reads a project's own config, if it has one.
//
// A missing file is the ordinary case and not a problem. A malformed one is: it was written
// on purpose, and a repository whose grouping quietly did not appear looks exactly like a
// taskui that does not support it.
func LoadProject(root string) Project {
	path := ""
	for _, name := range ProjectFiles {
		candidate := filepath.Join(root, name)
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	if path == "" {
		return Project{}
	}

	project := Project{Path: path}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		project.Problems = append(project.Problems, fmt.Sprintf("%s: %v", filepath.Base(path), err))
		return project
	}

	pivots, problems := readPivots(v)
	project.Pivots = pivots
	project.Problems = append(project.Problems, prefixed(path, problems)...)

	order, problems := readOrder(v)
	project.Pins = order.Pins
	project.Problems = append(project.Problems, prefixed(path, problems)...)

	project.Problems = append(project.Problems, refusals(path, v)...)
	return project
}

// refusals names the keys a project file is not allowed to set.
//
// Reported per top-level block rather than per leaf: a `colors:` block with nine entries is
// one decision that was not this file's to make, and nine lines saying so would bury the
// one line that matters.
func refusals(path string, v *viper.Viper) []string {
	seen := map[string]bool{}
	var out []string
	for _, key := range v.AllKeys() {
		top := key
		if at := strings.Index(top, "."); at >= 0 {
			top = top[:at]
		}
		if projectKeys[top] || seen[top] {
			continue
		}
		seen[top] = true
		out = append(out, fmt.Sprintf("%s: `%s` is yours to set, not the project's — "+
			"a project file may only add `pivots` and `pin`", filepath.Base(path), top))
	}
	return out
}

func prefixed(path string, problems []string) []string {
	if len(problems) == 0 {
		return nil
	}
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, filepath.Base(path)+": "+p)
	}
	return out
}

// WithProject folds a project's additions into a config.
//
// Appended rather than merged: pivots land after yours, so `p` reaches the built-ins and
// your own first and the repository's last, and pins land after yours, so where the two
// disagree about what belongs at the top, yours is the one at the top.
func (c Config) WithProject(p Project) Config {
	have := map[string]bool{DomainPivot: true, VerbPivot: true, FilePivot: true}
	for _, spec := range c.Pivots {
		have[spec.Name] = true
	}
	for _, spec := range p.Pivots {
		if have[spec.Name] {
			c.Problems = append(c.Problems, fmt.Sprintf(
				"%s: `%s` is already a grouping of yours, so the project's is ignored",
				filepath.Base(p.Path), spec.Name))
			continue
		}
		have[spec.Name] = true
		c.Pivots = append(c.Pivots, spec)
	}
	c.Order.Pins = append(c.Order.Pins, p.Pins...)
	c.Problems = append(c.Problems, p.Problems...)
	return c
}
