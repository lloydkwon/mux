package tmux

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Output(name string, args ...string) ([]byte, error)
	Run(name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Output(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return out, withStderr(err, stderr.Bytes())
}

func (execRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return withStderr(cmd.Run(), stderr.Bytes())
}

// withStderr puts what the command said into the error.
//
// Without it every tmux failure reaches the user as "exit status 1" — the one
// string that explains nothing, and the reason a create that tmux had refused
// with "duplicate session: mux" looked like a bug in mux. tmux is good about
// explaining itself, and that sentence is what the create prompt and the
// panel's event log have room to show.
//
// Wrapped rather than replaced: ListSessions tells "no server running" apart by
// looking for "exit status" in the message, and callers may still want the
// ExitError.
func withStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// runner is the package-level command runner, replaceable in tests.
var runner CommandRunner = execRunner{}

// SetRunner replaces the command runner (for testing). Passing nil restores the
// real one, so a test in another package can hand it back in a defer without
// needing a getter — and cannot leave a nil behind for the next test to hit.
func SetRunner(r CommandRunner) {
	if r == nil {
		runner = execRunner{}
		return
	}
	runner = r
}
