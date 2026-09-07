package task

import (
	"fmt"
	"os"
	"path/filepath"
)

// Starter is the Taskfile written for a directory that has none.
//
// It is a hello-world in that everything in it runs and prints, and a template in that the
// three tasks are each demonstrating something taskui reads: a `desc:`, which is the line
// under the name in the picker; a colon in a name, which is what folds into a namespace;
// and the `(NAME=Ada)` convention in a description, which is where the args prompt gets its
// hint from. Somebody who never edits this file has a working Taskfile; somebody who opens
// it has an example of each thing they are about to write.
//
// Deliberately not `task --init`, which writes a single `default:` task — the one name
// taskui does not list, because a default that runs the listing would appear in its own
// list. Following that suggestion would leave you looking at an empty picker.
const Starter = `# https://taskfile.dev
#
# Written by taskui because this directory had no Taskfile. Edit it into your own — the
# three below are here to show what taskui reads:
#
#   desc:       the line under the name in the picker. A task without one still runs.
#   a colon     in a name makes a namespace — greet:world and greet:twice fold under greet.
#   (NAME=Ada)  in a description becomes the hint beside the args prompt.

version: '3'

tasks:
  hello:
    desc: "Say hello"
    cmds:
      - echo "Hello, world!"

  greet:world:
    desc: "Greet somebody by name (NAME=Ada)"
    vars:
      NAME: '{{.NAME | default "world"}}'
    cmds:
      - echo "Hello, {{.NAME}}!"

  greet:twice:
    desc: "Two commands, so there is something to fold open"
    cmds:
      - echo "once"
      - echo "twice"
`

// StarterName is what the starter is written as. The first of Filenames, so that the file
// this writes is the file go-task then picks.
const StarterName = "Taskfile.yml"

// WriteStarter writes the starter Taskfile into dir and returns its path.
//
// Exclusive create: the only caller has already established there is no Taskfile here, so
// anything in the way is a surprise, and a surprise is not something to overwrite.
func WriteStarter(dir string) (string, error) {
	path := filepath.Join(dir, StarterName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(Starter); err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, nil
}
