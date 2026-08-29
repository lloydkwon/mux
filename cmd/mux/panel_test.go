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

// noSessionsRunner is a tmux with a server but nothing in it.
type noSessionsRunner struct{}

func (noSessionsRunner) Output(string, ...string) ([]byte, error) { return []byte("\n"), nil }
func (noSessionsRunner) Run(string, ...string) error              { return nil }

// The guard is what stops the popups: the popup runs mux, and a child that
// bootstrapped in turn would open them forever.
func TestShouldBootstrapNeverRecurses(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(tmux.BootstrapGuardEnv, "1")
	withRunner(t, deadPaneRunner{}) // would report sessions if it were asked

	if shouldBootstrap() {
		t.Error("a mux started by the bootstrap popup would bootstrap again")
	}
}

// Inside tmux, prefix + m is the popup and a bare `mux` in a pane is a choice.
func TestShouldBootstrapNotInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,4002,0")
	t.Setenv(tmux.BootstrapGuardEnv, "")
	withRunner(t, deadPaneRunner{})

	if shouldBootstrap() {
		t.Error("bootstrapped from inside tmux — that is what prefix + m is for")
	}
}

// Nothing to attach to. Creating a session to attach to would name it on the
// user's behalf, and the list mux draws instead already offers to ask.
func TestShouldBootstrapNotWithoutSessions(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(tmux.BootstrapGuardEnv, "")
	withRunner(t, noSessionsRunner{})

	if shouldBootstrap() {
		t.Error("bootstrapped with no session to attach to")
	}
}

// A tmux that cannot be asked is not a tmux to attach to.
func TestShouldBootstrapNotWhenTmuxFails(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(tmux.BootstrapGuardEnv, "")
	withRunner(t, failingRunner{err: errors.New("no server running")})

	if shouldBootstrap() {
		t.Error("bootstrapped with no reachable tmux server")
	}
}

// oneSessionRunner is a tmux with something to attach to.
type oneSessionRunner struct{}

func (oneSessionRunner) Output(name string, args ...string) ([]byte, error) {
	return []byte("mux|1|1786800000|0|/home/u|1786800000|zsh|123|0|0\n"), nil
}
func (oneSessionRunner) Run(string, ...string) error { return nil }

// The case the whole change exists for: a terminal that is not in tmux, with a
// session to attach to. prefix + m gets a popup; typing `mux` should too.
func TestShouldBootstrapOutsideTmuxWithSessions(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv(tmux.BootstrapGuardEnv, "")
	withRunner(t, oneSessionRunner{})

	if !shouldBootstrap() {
		t.Error("did not bootstrap outside tmux with a session to attach to")
	}
}

// Quitting the bootstrap's popup has to give the terminal back, and the mark is
// how runTUI can tell that popup from every other mux.
func TestBootstrappedPopupIsMarked(t *testing.T) {
	t.Setenv(tmux.BootstrapPopupEnv, "1")

	if !bootstrappedPopup() {
		t.Error("the popup a bootstrap opened did not recognise itself")
	}
}

// prefix + m, `mux popup`, and a bare `mux` in a pane all attached the terminal
// themselves or never attached it at all. Detaching on quit there would drop
// the user out of tmux for pressing q.
func TestBootstrappedPopupIsNotEveryMux(t *testing.T) {
	t.Setenv(tmux.BootstrapPopupEnv, "")

	if bootstrappedPopup() {
		t.Error("an ordinary mux claimed to be the bootstrap's popup")
	}
}

// The guard is a variable a user may export to opt out of bootstrapping. Read
// as the mark, it would make a plain `mux` in a terminal detach on quit.
func TestGuardAloneDoesNotMarkThePopup(t *testing.T) {
	t.Setenv(tmux.BootstrapPopupEnv, "")
	t.Setenv(tmux.BootstrapGuardEnv, "1")

	if bootstrappedPopup() {
		t.Error("MUX_NO_BOOTSTRAP alone was read as the popup mark")
	}
}
