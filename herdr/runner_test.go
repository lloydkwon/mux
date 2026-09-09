package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockRunner records calls and returns pre-configured responses. Keyed by the
// exact "name arg1 arg2" string, the same way tmux's is — adding a flag to a
// command breaks its test until the key is updated, which is the point.
type mockRunner struct {
	outputs map[string]mockResult
	gets    []string
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
	m.gets = append(m.gets, k)
	if r, ok := m.outputs[k]; ok {
		return r.out, r.err
	}
	return nil, fmt.Errorf("mock: unexpected call: %s", k)
}

func (m *mockRunner) Run(name string, args ...string) error {
	m.runs = append(m.runs, m.key(name, args...))
	return nil
}

// mockBin is what binPath resolves to under withMock, so the expected keys do
// not depend on where herdr is installed on the machine running the test.
const mockBin = "/mock/herdr"

func withMock(t *testing.T, fn func(m *mockRunner)) {
	t.Helper()
	m := newMockRunner()
	old := runner
	SetRunner(m)
	t.Setenv("MUX_HERDR_BIN", mockBin)
	resetSnapshotCache()
	defer func() {
		runner = old
		resetSnapshotCache()
	}()
	fn(m)
}

// fixture reads a captured herdr response from testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// seedSnapshot wires the three calls Snapshot makes to the live captures.
func seedSnapshot(t *testing.T, m *mockRunner) {
	t.Helper()
	m.OnOutput(fixture(t, "workspace_list.json"), nil, mockBin, "workspace", "list")
	m.OnOutput(fixture(t, "pane_list.json"), nil, mockBin, "pane", "list")
	m.OnOutput(fixture(t, "agent_list.json"), nil, mockBin, "agent", "list")
}

func TestSnapshotReadsAllThreeListsTogether(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		seedSnapshot(t, m)

		snap, err := Snapshot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(snap.workspaces) != 6 {
			t.Errorf("workspaces = %d, want 6", len(snap.workspaces))
		}
		// Measured: pane list carries one more entry than agent list — wC:p1 is
		// a plain shell. A mapping that read only the agent list would drop it.
		if len(snap.panes) != 10 {
			t.Errorf("panes = %d, want 10", len(snap.panes))
		}
		if len(snap.agentNames) != 8 {
			t.Errorf("named agents = %d, want 8", len(snap.agentNames))
		}
		if got := snap.agentNames["wE:p2"]; got != "govgen-plan" {
			t.Errorf("name for wE:p2 = %q, want %q", got, "govgen-plan")
		}
		// This repo's own pane has no name — the fallback chain exists for it.
		if _, ok := snap.agentNames["wA:p1"]; ok {
			t.Error("wA:p1 has a name; the fixture no longer covers the null-name case")
		}
	})
}

// The tick fans out a windows load per expanded session. Without the TTL that
// fan-out would multiply the round trips; with it, a whole tick costs three.
func TestSnapshotWithinTTLDoesNotCallAgain(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		seedSnapshot(t, m)

		for i := 0; i < 5; i++ {
			if _, err := Snapshot(); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if len(m.gets) != 3 {
			t.Errorf("exec calls = %d, want 3 — the TTL is not absorbing the fan-out:\n%v",
				len(m.gets), m.gets)
		}
	})
}

// --herdr is something the user asked for by name, so failing to reach it must
// be said out loud rather than swallowed into an empty list the way
// tmux.ListSessions swallows "no server running".
func TestSnapshotReportsAnErrorEnvelope(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte(`{"id":"","error":{"code":"unavailable","message":"no server"}}`),
			nil, mockBin, "workspace", "list")

		_, err := Snapshot()
		if err == nil {
			t.Fatal("an error envelope decoded as success")
		}
		if !strings.Contains(err.Error(), "no server") {
			t.Errorf("error = %q, want herdr's own message in it", err)
		}
	})
}

func TestSnapshotReportsAFailedCommand(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput(nil, withStderr(fmt.Errorf("exit status 1"),
			[]byte("failed to connect to herdr socket")), mockBin, "workspace", "list")

		_, err := Snapshot()
		if err == nil {
			t.Fatal("a failed command decoded as success")
		}
		// withStderr is what puts herdr's sentence in front of the user; without
		// it the error bar reads "exit status 1" and explains nothing.
		if !strings.Contains(err.Error(), "socket") {
			t.Errorf("error = %q, want the stderr text carried through", err)
		}
	})
}
