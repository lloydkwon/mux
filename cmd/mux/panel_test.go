package main

import (
	"errors"
	"testing"

	"github.com/lloydkwon/mux/tmux"
)

// failingRunner stands in for a tmux that cannot answer — the target pane has
// gone, which is the normal way the hook path fails.
type failingRunner struct{ err error }

func (f failingRunner) Output(string, ...string) ([]byte, error) { return nil, f.err }
func (f failingRunner) Run(string, ...string) error              { return f.err }

// deadPaneRunner reproduces the reported bug exactly: tmux answers a
// display-message about a dead pane with exit 0 and a single space, which
// panelWindow correctly refuses.
type deadPaneRunner struct{}

func (deadPaneRunner) Output(string, ...string) ([]byte, error) { return []byte(" \n"), nil }
func (deadPaneRunner) Run(string, ...string) error              { return nil }

func withRunner(t *testing.T, r tmux.CommandRunner) {
	t.Helper()
	tmux.SetRunner(r)
	// nil hands the real runner back — SetRunner documents that for exactly this.
	t.Cleanup(func() { tmux.SetRunner(nil) })
}

// The hook path never reports. Seven hooks fire it, client-resized among them,
// and a failing run-shell paints the status line of every attached client every
// time — so one failure becomes a permanent banner. This is the reported bug:
// 'mux panel --auto -t %53' returned 1, for a pane that had already closed.
func TestPanelAutoNeverReportsFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		runner tmux.CommandRunner
	}{
		{"the pane is gone", deadPaneRunner{}},
		{"tmux itself failed", failingRunner{err: errors.New("no server running")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRunner(t, tc.runner)
			if err := runPanelAuto("%53", true); err != nil {
				t.Errorf("the hook path reported %v — tmux would print it on the status line", err)
			}
		})
	}
}

// Pressing prefix + a still reports. It is an answer to a key the user just
// pressed, and it has somewhere to go.
func TestPanelByHandStillReportsFailure(t *testing.T) {
	withRunner(t, deadPaneRunner{})
	if err := runPanelAuto("%53", false); err == nil {
		t.Error("opening by hand swallowed the failure — the key press had no answer")
	}
}
