package herdr

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

// withStderr puts what the command said into the error, for the reason
// tmux/runner.go's twin does: without it every failure reaches the user as
// "exit status 1", and here that string is drawn in the list's error bar where
// it is the only thing the user gets to read. herdr explains itself on stderr —
// a missing socket says so by name.
func withStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// timedRunner 는 제한 시간을 받는 러너다(tmux/runner.go 의 쌍둥이). 진짜 러너만 갖는다 — 목은 Output 으로
// 물러서므로 시험은 시간을 쓰지 않는다.
type timedRunner interface {
	OutputWithin(limit time.Duration, name string, args ...string) ([]byte, error)
}

func (execRunner) OutputWithin(limit time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
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

// runner is this package's own command runner, deliberately separate from the
// tmux package's.
//
// The two could not share one: the mock in either package is keyed by the exact
// "name arg1 arg2" string, so a shared instance would make a herdr test's
// expected keys collide with a tmux test's in the same map. Same shape, two
// instances — which is also what keeps `go test ./herdr` from needing anything
// tmux knows about.
var runner CommandRunner = execRunner{}

// SetRunner replaces the command runner (for testing). Passing nil restores the
// real one, so a test in another package can hand it back in a defer without a
// getter, and cannot leave a nil behind for the next test to hit.
func SetRunner(r CommandRunner) {
	if r == nil {
		runner = execRunner{}
		return
	}
	runner = r
}
