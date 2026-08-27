package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/ddromanidis/taskui/internal/theme"
)

// inTempConfig points the config path somewhere disposable and returns it. Every test here
// writes files, and none of them may touch the real one.
func inTempConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	return filepath.Join(home, "taskui", "config.yaml")
}

// execute drives a command the way cobra would, capturing what it printed.
//
// The viper instance is replaced first. It is package-level and filled by cobra's
// OnInitialize, so in a test binary — where several commands run in one process — whatever
// the previous test's config said survives into the next one. A real invocation is one
// command in a fresh process, and this makes the tests agree with that: without it, a test
// that wrote `theme: synthwave` made a later test render in synthwave.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	v = viper.New()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		configForce = false
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestConfigPathIsUnderXDGConfigHome(t *testing.T) {
	want := inTempConfig(t)
	out, err := execute(t, "config", "path")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != want {
		t.Errorf("got %q, want %q", strings.TrimSpace(out), want)
	}
}

// The default without XDG_CONFIG_HOME is the one every other tool uses.
func TestWithoutXDGItIsDotConfigUnderHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/somebody")
	if got, want := theme.ConfigPath(), "/home/somebody/.config/taskui/config.yaml"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigSaysWhenThereIsNoFileYet(t *testing.T) {
	inTempConfig(t)
	out, err := execute(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not there yet") {
		t.Errorf("got:\n%s", out)
	}
	// …and where the themes go, since that is the other half of the same question.
	if !strings.Contains(out, "themes") {
		t.Errorf("got:\n%s", out)
	}
}

func TestInitWritesAConfigYouCanActuallyLoad(t *testing.T) {
	path := inTempConfig(t)
	if _, err := execute(t, "config", "init"); err != nil {
		t.Fatal(err)
	}

	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// It has to be the real thing, not a stub: the point of creating it is that you then
	// have every setting in front of you.
	for _, want := range []string{"theme:", "colors:", "glyphs:", "keys:"} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("the written config has no %q block", want)
		}
	}
	// And it must load without complaint, or `config init` has handed you a broken file.
	config := theme.Load(path)
	if len(config.Problems) > 0 {
		t.Errorf("the config it wrote does not load cleanly: %v", config.Problems)
	}
}

// The config file is the one file here holding decisions made by hand. Overwriting it
// because a command was ambiguous would be unforgivable.
func TestInitNeverOverwritesByAccident(t *testing.T) {
	path := inTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "theme: synthwave\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, "config", "init")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("got:\n%s", out)
	}
	blob, _ := os.ReadFile(path)
	if string(blob) != mine {
		t.Error("it overwrote a config that was already there")
	}
}

func TestInitForceReplacesIt(t *testing.T) {
	path := inTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("theme: synthwave\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(t, "config", "init", "--force"); err != nil {
		t.Fatal(err)
	}
	blob, _ := os.ReadFile(path)
	if !strings.Contains(string(blob), "colors:") {
		t.Errorf("--force did not replace it: %q", blob)
	}
}

// An editor opened on an empty buffer is a worse starting point than one showing every
// setting there is, so editing creates the file first.
func TestEditCreatesTheFileBeforeOpeningIt(t *testing.T) {
	path := inTempConfig(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true") // a program that opens nothing and succeeds

	if _, err := execute(t, "config", "edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("edit did not create the file: %v", err)
	}
}

func TestEditWithNoEditorPrintsThePathInstead(t *testing.T) {
	path := inTempConfig(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	out, err := execute(t, "config", "edit")
	if err == nil {
		t.Fatal("should have reported that there is no editor")
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Errorf("error = %q", err)
	}
	// The path is the useful half of the answer, so it goes to stdout regardless.
	if !strings.Contains(out, path) {
		t.Errorf("got:\n%s", out)
	}
}

func TestEditReportsAnEditorThatWillNotRun(t *testing.T) {
	inTempConfig(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "definitely-not-a-real-editor-9f3a")

	_, err := execute(t, "config", "edit")
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "could not run") {
		t.Errorf("error = %q", err)
	}
}

// --since takes the units people actually use for an archive; [time.ParseDuration] stops at
// hours, and "two days ago" is the natural way to ask.
func TestParseSince(t *testing.T) {
	for _, c := range []struct {
		text string
		want time.Duration
	}{
		{"90m", 90 * time.Minute},
		{"2h", 2 * time.Hour},
		{"2d", 48 * time.Hour},
		{"3w", 21 * 24 * time.Hour},
		{" 1d ", 24 * time.Hour},
		{"", 0},
	} {
		got, err := parseSince(c.text)
		if err != nil {
			t.Errorf("%q: %v", c.text, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestParseSinceRejectsNonsense(t *testing.T) {
	for _, text := range []string{"banana", "-2d", "d", "2y", "-3h"} {
		if _, err := parseSince(text); err == nil {
			t.Errorf("%q was accepted", text)
		}
	}
}

// --- the exit-code contract ---------------------------------------------------------------

// The codes are an interface. Before this, `--run` on a failing pipeline came back 0 — the
// manual promised the task's status and the program returned success — and `--flaky` shared
// its code with "there is no Taskfile here", so a script could not tell them apart.

func TestAQuietExitCarriesItsCode(t *testing.T) {
	err := exitWith(201)
	var status exitError
	if !errors.As(err, &status) {
		t.Fatal("not an exitError")
	}
	if status.code != 201 {
		t.Errorf("code = %d", status.code)
	}
	if status.message != nil {
		t.Error("a quiet exit should say nothing — the command already did")
	}
}

func TestALoudExitCarriesBoth(t *testing.T) {
	err := exitBecause(ExitFound, "%d tasks went both ways", 3)
	var status exitError
	if !errors.As(err, &status) {
		t.Fatal("not an exitError")
	}
	if status.code != ExitFound {
		t.Errorf("code = %d, want %d", status.code, ExitFound)
	}
	if !strings.Contains(err.Error(), "3 tasks") {
		t.Errorf("message = %q", err)
	}
}

// The three codes have to stay distinct, because telling them apart is their whole job.
func TestTheExitCodesAreDistinct(t *testing.T) {
	seen := map[int]string{}
	for name, code := range map[string]int{
		"ExitOK":     ExitOK,
		"ExitFailed": ExitFailed,
		"ExitFound":  ExitFound,
	} {
		if other, clash := seen[code]; clash {
			t.Errorf("%s and %s are both %d", name, other, code)
		}
		seen[code] = name
	}
	if ExitOK != 0 {
		t.Error("success has to be 0")
	}
}

// A run that failed is reported by `--flaky` as a finding, not as taskui breaking.
func TestFlakyReportsAFindingNotAFailure(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)

	var out strings.Builder
	err := printFlaky(&out, "/nowhere")
	if err != nil {
		t.Fatalf("an empty archive is not a finding: %v", err)
	}
	if !strings.Contains(out.String(), "no task") {
		t.Errorf("got %q", out.String())
	}
}
