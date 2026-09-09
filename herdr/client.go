package herdr

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// snapshotTTL is how long one reading of herdr is reused.
//
// It is shorter than the TUI's 500ms tick so every tick still gets a fresh
// answer, and long enough that everything *within* one tick shares it. That
// second half is what matters: the tick fans out a windows load per expanded
// session (ui/app.go), and without the cache a list of twenty workspaces would
// pay twenty round trips for data that arrived with the session list.
const snapshotTTL = 400 * time.Millisecond

// snapshot is one consistent reading of herdr: every workspace, every pane, and
// the agent names for the panes that host one.
//
// The three are fetched together and cached together because they are only ever
// wanted together. A workspace's directory and age come from its panes, so a
// workspace list read against a pane list from a different moment would
// describe a layout that never existed.
type snapshot struct {
	workspaces []workspaceInfo
	panes      []paneInfo
	// agentNames maps pane id to herdr's name for the agent in it. Kept apart
	// from panes because `pane list` does not carry it.
	agentNames map[string]string
	at         time.Time
}

var (
	snapMu   sync.Mutex
	snapLast *snapshot
)

// binPath resolves the herdr binary.
//
// MUX_HERDR_BIN first, matching HERDR_BIN in tmux/detect's differential test
// and TMUX_BIN in scripts/tmux-assumptions.sh: pointing this at a candidate is
// how a herdr upgrade gets checked before it is the one on PATH.
func binPath() (string, error) {
	if bin := os.Getenv("MUX_HERDR_BIN"); bin != "" {
		return bin, nil
	}
	if bin, err := exec.LookPath("herdr"); err == nil {
		return bin, nil
	}
	// herdr installs itself here and does not always put it on the PATH of a
	// shell that did not source a profile.
	if home, err := os.UserHomeDir(); err == nil {
		fallback := filepath.Join(home, ".local", "bin", "herdr")
		if st, err := os.Stat(fallback); err == nil && !st.IsDir() {
			return fallback, nil
		}
	}
	return "", fmt.Errorf("herdr not found: not on PATH and no ~/.local/bin/herdr")
}

// call runs one herdr command and decodes its envelope.
//
// An error envelope is turned into a Go error carrying herdr's own message.
// Unlike tmux.ListSessions, a failure is never swallowed into an empty list:
// "no tmux server" is a normal state for a session manager, but --herdr is
// something the user asked for on the command line, so not reaching it has to
// be said out loud.
func call(args ...string) (envelopeResult, error) {
	bin, err := binPath()
	if err != nil {
		return envelopeResult{}, err
	}
	out, err := runner.Output(bin, args...)
	if err != nil {
		return envelopeResult{}, fmt.Errorf("herdr %s: %w", args[0], err)
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		return envelopeResult{}, fmt.Errorf("herdr %s: %w", args[0], err)
	}
	if env.Error != nil {
		return envelopeResult{}, fmt.Errorf("herdr %s: %s", args[0], env.Error.Message)
	}
	return env.Result, nil
}

// Snapshot returns the current reading of herdr, refetching only when the
// cached one has aged out.
func Snapshot() (*snapshot, error) {
	snapMu.Lock()
	defer snapMu.Unlock()
	if snapLast != nil && time.Since(snapLast.at) < snapshotTTL {
		return snapLast, nil
	}

	ws, err := call("workspace", "list")
	if err != nil {
		return nil, err
	}
	pn, err := call("pane", "list")
	if err != nil {
		return nil, err
	}
	ag, err := call("agent", "list")
	if err != nil {
		return nil, err
	}

	names := make(map[string]string, len(ag.Agents))
	for _, a := range ag.Agents {
		if a.Name != "" {
			names[a.PaneID] = a.Name
		}
	}

	snapLast = &snapshot{
		workspaces: ws.Workspaces,
		panes:      pn.Panes,
		agentNames: names,
		at:         time.Now(),
	}
	return snapLast, nil
}

// resetSnapshotCache drops the cached reading. Tests use it; nothing in the
// program does, because a stale reading is only ever 400ms stale.
func resetSnapshotCache() {
	snapMu.Lock()
	snapLast = nil
	snapMu.Unlock()
}
