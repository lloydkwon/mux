package tmux

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockRunner records calls and returns pre-configured responses.
type mockRunner struct {
	outputs map[string]mockResult
	runs    []string
}

type mockResult struct {
	out []byte
	err error
}

func newMockRunner() *mockRunner {
	return &mockRunner{outputs: make(map[string]mockResult)}
}

func (m *mockRunner) key(name string, args ...string) string {
	return name + " " + strings.Join(args, " ")
}

func (m *mockRunner) OnOutput(out []byte, err error, name string, args ...string) {
	m.outputs[m.key(name, args...)] = mockResult{out: out, err: err}
}

func (m *mockRunner) Output(name string, args ...string) ([]byte, error) {
	k := m.key(name, args...)
	if r, ok := m.outputs[k]; ok {
		return r.out, r.err
	}
	return nil, fmt.Errorf("mock: unexpected call: %s", k)
}

func (m *mockRunner) Run(name string, args ...string) error {
	m.runs = append(m.runs, m.key(name, args...))
	return nil
}

func withMock(t *testing.T, fn func(m *mockRunner)) {
	t.Helper()
	m := newMockRunner()
	old := runner
	SetRunner(m)
	// Clear command cache to avoid cross-test interference
	cmdCacheMu.Lock()
	cmdCache = make(map[int]cachedCommand)
	cmdCacheMu.Unlock()
	// Point the Claude state lookup at an empty home so tests never read the
	// developer's real ~/.claude/sessions, and drop anything it already cached.
	claudeStatusCacheMu.Lock()
	claudeStatusCache = cachedClaudeStatuses{}
	claudeStatusCacheMu.Unlock()
	oldHome := homeDir
	homeDir = func() (string, error) { return t.TempDir(), nil }
	// Same reason, for the panel's remembered width: TogglePanel consults it to
	// pick a split width, so left alone every panel test would assert against
	// whatever the developer last dragged their own panel to.
	oldPanelState := panelStateFile
	stateDir := t.TempDir()
	panelStateFile = func() (string, error) { return filepath.Join(stateDir, "panel.json"), nil }
	defer func() {
		runner = old
		homeDir = oldHome
		panelStateFile = oldPanelState
		claudeStatusCacheMu.Lock()
		claudeStatusCache = cachedClaudeStatuses{}
		claudeStatusCacheMu.Unlock()
	}()
	fn(m)
}

func TestListSessionsWithMock(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		now := time.Now().Unix()
		line1 := fmt.Sprintf("dev|2|%d|1|/home/user/dev|%d|bash|100", now-3600, now-60)
		line2 := fmt.Sprintf("ai|1|%d|0|/home/user/ai|%d|claude|200", now-7200, now-120)
		out := line1 + "\n" + line2

		// Mock the list-sessions call
		m.OnOutput([]byte(out), nil, "tmux", "list-sessions", "-F", listFormat)
		// Mock resolveCommand calls — pgrep returns nothing (so rawCmd is used)
		m.OnOutput(nil, fmt.Errorf("no children"), "pgrep", "-P", "100")
		m.OnOutput(nil, fmt.Errorf("no children"), "pgrep", "-P", "200")

		sessions, err := ListSessions()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sessions) != 2 {
			t.Fatalf("expected 2 sessions, got %d", len(sessions))
		}
		// Attached sessions first
		if sessions[0].Name != "dev" {
			t.Errorf("expected first session 'dev', got %q", sessions[0].Name)
		}
	})
}

func TestResolveCommandWithMock(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		// pgrep returns child PIDs
		m.OnOutput([]byte("42\n43\n"), nil, "pgrep", "-P", "100")
		// ps for PID 42 returns bash
		m.OnOutput([]byte("/bin/bash\n"), nil, "ps", "-o", "args=", "-p", "42")
		// ps for PID 43 returns claude
		m.OnOutput([]byte("/usr/local/bin/claude --help\n"), nil, "ps", "-o", "args=", "-p", "43")

		result := resolveCommand(100, "bash")
		if result != "claude" {
			t.Errorf("expected 'claude', got %q", result)
		}
	})
}

func TestCapturePaneWithMock(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("hello world\n\n"), nil, "tmux", "capture-pane", "-t", "test-session", "-p", "-e")

		content, err := CapturePane("test-session")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "hello world" {
			t.Errorf("expected 'hello world', got %q", content)
		}
	})
}

func TestCreateSessionWithMock(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := CreateSession("new-sess"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m.runs) != 1 {
			t.Fatalf("expected 1 run call, got %d", len(m.runs))
		}
		expected := "tmux new-session -d -s new-sess"
		if m.runs[0] != expected {
			t.Errorf("expected %q, got %q", expected, m.runs[0])
		}
	})
}

func TestKillSessionWithMock(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := KillSession("old-sess"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "tmux kill-session -t old-sess"
		if m.runs[0] != expected {
			t.Errorf("expected %q, got %q", expected, m.runs[0])
		}
	})
}

// "exit status 1" is the one message that explains nothing, and it is what a
// user saw when tmux had in fact said exactly what was wrong.
func TestWithStderrCarriesTheReason(t *testing.T) {
	base := errors.New("exit status 1")

	got := withStderr(base, []byte("duplicate session: mux\n"))
	if want := "exit status 1: duplicate session: mux"; got.Error() != want {
		t.Errorf("error = %q, want %q", got.Error(), want)
	}
	// Wrapped, not replaced: callers may still want the ExitError.
	if !errors.Is(got, base) {
		t.Error("the original error was lost")
	}

	if got := withStderr(base, []byte("  \n")); got != base {
		t.Errorf("empty stderr changed the error to %v", got)
	}
	if got := withStderr(nil, []byte("noise")); got != nil {
		t.Errorf("success turned into an error: %v", got)
	}
}

// ListSessions tells "no server running" apart by looking for "exit status" in
// the message, so the reason has to be appended rather than substituted.
func TestWithStderrKeepsTheNoServerCheckWorking(t *testing.T) {
	err := withStderr(errors.New("exit status 1"),
		[]byte("no server running on /tmp/tmux-1000/default"))
	if !strings.Contains(err.Error(), "exit status") {
		t.Errorf("error %q no longer reads as an exit status", err)
	}
}
