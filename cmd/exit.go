package cmd

import (
	"errors"
	"fmt"
	"os"
)

// What taskui exits with.
//
// Three cases and a passthrough. The passthrough is the one that matters: `--run` means "run
// this and be it", so its status is the task's, which is what the manual has always claimed
// and what the program did not do — a failing pipeline came back 0, and a CI step calling it
// went green. There is no worse shape for a bug than that.
//
// `--flaky` and `--lint` get a code of their own rather than sharing the failure one. Sharing
// it means a script cannot tell "this task is flaky" from "there is no Taskfile here", which
// is the distinction the exit code exists to draw. `diff` and `grep` both separate "found
// something" from "went wrong" for the same reason.
const (
	// ExitOK is nothing wrong.
	ExitOK = 0
	// ExitFailed is taskui could not do what was asked: a bad flag, no Taskfile, no go-task,
	// an unreadable config.
	ExitFailed = 1
	// ExitFound is the check found what it was looking for: `--flaky` and `--lint`.
	ExitFound = 2
)

// exitError ends the program with a particular code.
//
// A nil message means the command has already printed everything worth printing — which is
// the case for `--run`, whose captured tree ends in the exit line and needs no second word
// from the wrapper.
type exitError struct {
	code    int
	message error
}

func (e exitError) Error() string {
	if e.message == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.message.Error()
}

func (e exitError) Unwrap() error { return e.message }

// exitWith is the quiet form: this code, nothing said.
func exitWith(code int) error { return exitError{code: code} }

// exitBecause is the loud form: this code, and here is why.
func exitBecause(code int, format string, args ...any) error {
	return exitError{code: code, message: fmt.Errorf(format, args...)}
}

// Execute runs the command and exits with whatever it decided.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}

	if status, ok := errors.AsType[exitError](err); ok {
		if status.message != nil {
			fmt.Fprintln(os.Stderr, "taskui:", status.message)
		}
		os.Exit(status.code)
	}

	fmt.Fprintln(os.Stderr, "taskui:", err)
	os.Exit(ExitFailed)
}
