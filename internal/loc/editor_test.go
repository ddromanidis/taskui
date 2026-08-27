package loc

import (
	"strings"
	"testing"
)

func TestEachEditorGetsTheSpellingItUnderstands(t *testing.T) {
	l := Loc{Path: "view.go", Line: 212, Col: 5}
	for _, c := range []struct {
		editor string
		want   string
	}{
		{"vim", "vim +212 /p/view.go"},
		{"nvim", "nvim +212 /p/view.go"},
		{"nano", "nano +212,5 /p/view.go"},
		{"emacs", "emacs +212:5 /p/view.go"},
		{"hx", "hx /p/view.go:212:5"},
		{"code", "code --goto /p/view.go:212:5"},
		{"subl", "subl /p/view.go:212:5"},
		{"goland", "goland --line 212 --column 5 /p/view.go"},
		{"micro", "micro +212 /p/view.go"},
		{"/usr/local/bin/nvim", "/usr/local/bin/nvim +212 /p/view.go"},
	} {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", c.editor)
		e, ok := EditorFor(l, "/p/view.go")
		if !ok {
			t.Fatalf("%s: no editor", c.editor)
		}
		got := e.Name + " " + strings.Join(e.Args, " ")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.editor, got, c.want)
		}
	}
}

// `EDITOR="code -w"` is a real thing people have set, and dropping the flag silently
// changes what the command does.
func TestFlagsAlreadyInTheVariableSurvive(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code -w -n")
	e, ok := EditorFor(Loc{Line: 9, Col: 2}, "/p/x.go")
	if !ok {
		t.Fatal("no editor")
	}
	want := []string{"-w", "-n", "--goto", "/p/x.go:9:2"}
	if strings.Join(e.Args, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", e.Args, want)
	}
}

// An unknown editor still opens the file. Guessing `+N` at it risks the editor reading that
// as a second filename and creating it, which is a worse failure than landing at line 1.
func TestAnUnknownEditorJustGetsTheFile(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor")
	e, ok := EditorFor(Loc{Line: 40}, "/p/x.go")
	if !ok {
		t.Fatal("no editor")
	}
	if len(e.Args) != 1 || e.Args[0] != "/p/x.go" {
		t.Errorf("got %v", e.Args)
	}
	if !e.Terminal {
		t.Error("an unknown editor should be assumed to want the terminal")
	}
}

func TestVisualBeatsEditor(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "code")
	e, _ := EditorFor(Loc{Line: 1}, "/p/x.go")
	if e.Name != "code" {
		t.Errorf("got %s, want code", e.Name)
	}
}

// An empty VISUAL must fall through rather than winning with nothing, or exporting it as a
// blank in a shell profile breaks the key entirely.
func TestAnEmptyVisualFallsThroughToEditor(t *testing.T) {
	t.Setenv("VISUAL", "  ")
	t.Setenv("EDITOR", "vim")
	e, ok := EditorFor(Loc{Line: 1}, "/p/x.go")
	if !ok || e.Name != "vim" {
		t.Errorf("ok=%v name=%s", ok, e.Name)
	}
}

func TestNoEditorConfiguredIsReported(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if _, ok := EditorFor(Loc{Line: 1}, "/p/x.go"); ok {
		t.Error("should have reported that there is no editor")
	}
}

// Whether the editor takes the terminal decides whether the UI is torn down for it. Getting
// it wrong for a windowed editor blacks out the session for a command that already returned.
func TestWindowedEditorsDoNotTakeTheTerminal(t *testing.T) {
	for _, name := range []string{"code", "subl", "goland", "zed", "cursor"} {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", name)
		e, _ := EditorFor(Loc{Line: 1}, "/p/x.go")
		if e.Terminal {
			t.Errorf("%s should not take the terminal", name)
		}
	}
	for _, name := range []string{"vim", "nvim", "nano", "emacs", "hx"} {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", name)
		e, _ := EditorFor(Loc{Line: 1}, "/p/x.go")
		if !e.Terminal {
			t.Errorf("%s should take the terminal", name)
		}
	}
}

// A tool that gave no column still has to produce a valid command; `nano +12,0` is not one.
func TestAMissingColumnBecomesOne(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	e, _ := EditorFor(Loc{Line: 12, Col: 0}, "/p/x.go")
	if e.Args[0] != "+12,1" {
		t.Errorf("got %q", e.Args[0])
	}
}
