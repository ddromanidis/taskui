package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddromanidis/taskui/internal/pivot"
)

func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".taskui.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The two things a repository knows about its own task list: which tasks are the ones people
// actually run, and how its names group.
func TestAProjectMayAddPivotsAndPins(t *testing.T) {
	dir := writeProject(t, `
pin:
  - "dev:*"
  - build
pivots:
  - name: service
    regex: "^(?P<group>[^:]+):"
`)
	project := LoadProject(dir)
	if len(project.Problems) > 0 {
		t.Fatalf("problems: %v", project.Problems)
	}
	if len(project.Pins) != 2 || project.Pins[0] != "dev:*" {
		t.Errorf("pins = %v", project.Pins)
	}
	if len(project.Pivots) != 1 || project.Pivots[0].Name != "service" {
		t.Errorf("pivots = %+v", project.Pivots)
	}

	config := DefaultConfig().WithProject(project)
	if len(config.Pivots) != 1 || config.Order.Pins[0] != "dev:*" {
		t.Errorf("config did not take the project's additions: %+v", config.Order.Pins)
	}
}

// Config in a repository is config you did not write. A theme or a keymap arriving from
// someone else's checkout is a surprise, so it is refused — and said out loud, because a key
// that silently did nothing would be worse than one that explains itself.
func TestAProjectMayNotSetYourThemeOrYourKeys(t *testing.T) {
	dir := writeProject(t, `
theme: synthwave
colors:
  accent: "#ff0000"
keys:
  quit: z
pin:
  - build
`)
	project := LoadProject(dir)
	if len(project.Pins) != 1 {
		t.Errorf("the additive half was dropped along with the rest: %v", project.Pins)
	}
	joined := strings.Join(project.Problems, "\n")
	for _, refused := range []string{"theme", "colors", "keys"} {
		if !strings.Contains(joined, "`"+refused+"`") {
			t.Errorf("nothing said about `%s`: %v", refused, project.Problems)
		}
	}
	// One line per block, not one per colour.
	if len(project.Problems) != 3 {
		t.Errorf("got %d problems, want one per refused block: %v", len(project.Problems), project.Problems)
	}

	config := DefaultConfig().WithProject(project)
	if config.Theme.Name != DefaultTheme().Name {
		t.Error("a project changed the theme")
	}
}

// Yours wins where the two collide, and the collision is reported rather than resolved
// silently — two groupings answering to one name would make `p` depend on load order.
func TestYourOwnPivotBeatsTheProjects(t *testing.T) {
	dir := writeProject(t, `
pivots:
  - name: service
    regex: "^(?P<group>[^:]+):"
`)
	config := DefaultConfig()
	config.Pivots = []pivot.Spec{{Name: "service", Regex: "^(?P<group>x)"}}
	config = config.WithProject(LoadProject(dir))

	if len(config.Pivots) != 1 || config.Pivots[0].Regex != "^(?P<group>x)" {
		t.Errorf("pivots = %+v", config.Pivots)
	}
	if len(config.Problems) != 1 || !strings.Contains(config.Problems[0], "ignored") {
		t.Errorf("problems = %v", config.Problems)
	}
}

// A missing file is the ordinary case. A broken one was written on purpose and has to say so.
func TestAMissingProjectFileIsNotAProblemAndABrokenOneIs(t *testing.T) {
	if p := LoadProject(t.TempDir()); p.Path != "" || len(p.Problems) > 0 {
		t.Errorf("a directory with no project file reported %+v", p)
	}
	dir := writeProject(t, "pin: [unclosed\n")
	if p := LoadProject(dir); len(p.Problems) == 0 {
		t.Error("a file that does not parse said nothing")
	}
}
