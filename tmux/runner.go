package tmux

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
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

// timedRunner 는 제한 시간을 받는 러너다. 진짜 러너만 갖는다 — 목 러너는 Output 으로 물러서므로 테스트는
// 시간을 쓰지 않는다.
type timedRunner interface {
	OutputWithin(limit time.Duration, name string, args ...string) ([]byte, error)
}

func (execRunner) OutputWithin(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// 자식이 출력 파이프를 쥔 채 남아도 기다림이 끝나게 — claude_work_title.go 와 같은 이유.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s did not answer within %v", name, limit)
	}
	return out, withStderr(err, stderr.Bytes())
}

// outputWithin 은 runner.Output 에 제한 시간을 준다. 러너가 제한 시간을 모르면(목) 그냥 Output 이다.
func outputWithin(limit time.Duration, name string, args ...string) ([]byte, error) {
	if tr, ok := runner.(timedRunner); ok {
		return tr.OutputWithin(limit, name, args...)
	}
	return runner.Output(name, args...)
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
