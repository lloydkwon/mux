package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// claudeStatusTTL bounds how stale a displayed state may be. Elapsed time is
// recomputed from ClaudeStatus.Since at render time, so this caps only how
// long a *transition* takes to appear, not the smoothness of the clock.
const claudeStatusTTL = 1 * time.Second

// ClaudeStatus is the live state of one Claude Code session bound to a tmux
// session. Claude is the one AI CLI that publishes its own state, so this file
// is the only producer of a non-zero AIState.
type ClaudeStatus struct {
	State      AIState
	RawStatus  string    // verbatim "status", for diagnostics
	WaitingFor string    // verbatim "waitingFor"; set only for AIStateApproval
	Since      time.Time // when the current state began; zero when unknown
	SessionID  string
	PID        int
}

// Elapsed reports how long the session has been in its current state. It
// returns zero when the start time is unknown, and clamps negative values so
// clock skew can never render as a time in the future.
func (s ClaudeStatus) Elapsed(now time.Time) time.Duration {
	if s.Since.IsZero() {
		return 0
	}
	if d := now.Sub(s.Since); d > 0 {
		return d
	}
	return 0
}

type cachedClaudeStatuses struct {
	byTmuxSession map[string]ClaudeStatus
	expiresAt     time.Time
}

var (
	claudeStatusCache   cachedClaudeStatuses
	claudeStatusCacheMu sync.Mutex
)

// ClaudeStatuses returns the live state of every Claude Code session, keyed by
// the tmux session name it runs in. Sessions not running under tmux, and
// sessions whose process is gone, are omitted. The returned map is shared and
// must not be mutated. It is never nil.
func ClaudeStatuses() map[string]ClaudeStatus {
	claudeStatusCacheMu.Lock()
	if c := claudeStatusCache; c.byTmuxSession != nil && time.Now().Before(c.expiresAt) {
		claudeStatusCacheMu.Unlock()
		return c.byTmuxSession
	}
	claudeStatusCacheMu.Unlock()

	statuses := scanClaudeStatuses()

	claudeStatusCacheMu.Lock()
	claudeStatusCache = cachedClaudeStatuses{
		byTmuxSession: statuses,
		expiresAt:     time.Now().Add(claudeStatusTTL),
	}
	claudeStatusCacheMu.Unlock()

	return statuses
}

// scanClaudeStatuses reads every session file once and indexes the live ones
// by tmux session name.
func scanClaudeStatuses() map[string]ClaudeStatus {
	result := make(map[string]ClaudeStatus)

	dir, err := claudeSessionsDir()
	if err != nil {
		return result
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result // no Claude install, or no sessions yet — feature stays off
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // raced with the session exiting
		}
		status, session, ok := parseClaudeStatus(data)
		if !ok {
			continue
		}
		if prev, dup := result[session]; dup && !moreUrgent(status, prev) {
			continue
		}
		result[session] = status
	}

	return result
}

// parseClaudeStatus decodes one session file and reports the tmux session it
// belongs to. ok is false for anything we cannot or should not display:
// a partially written file, a session outside tmux, a dead process, or a
// status we don't model.
func parseClaudeStatus(data []byte) (status ClaudeStatus, tmuxSession string, ok bool) {
	var sf claudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return ClaudeStatus{}, "", false // torn write — skip this file, not the scan
	}

	tmuxSession = tmuxSessionName(sf.Tmux)
	if tmuxSession == "" {
		return ClaudeStatus{}, "", false
	}
	if !isAlive(sf.PID, sf.ProcStart) {
		return ClaudeStatus{}, "", false // stale file from a crashed session
	}

	state := mapClaudeState(sf.Status)
	if state == AIStateNone {
		return ClaudeStatus{}, "", false
	}
	if state == AIStateShell {
		state = demoteServerShell(sf.PID)
	}

	status = ClaudeStatus{
		State:     state,
		RawStatus: sf.Status,
		SessionID: sf.SessionID,
		PID:       sf.PID,
	}
	if state == AIStateApproval {
		status.WaitingFor = sf.WaitingFor
	}
	if sf.StatusUpdatedAt > 0 {
		status.Since = time.UnixMilli(sf.StatusUpdatedAt)
	}
	return status, tmuxSession, true
}

// mapClaudeState reduces Claude's status enum to the AIState we display.
//
// "waiting" always means Claude is blocked on the user — it is set only while
// some prompt or dialog is open, whatever the reason. A finished turn sitting
// at the prompt reports "idle" instead.
func mapClaudeState(status string) AIState {
	switch status {
	case "busy":
		return AIStateWorking
	case "waiting":
		return AIStateApproval
	case "idle":
		return AIStateReady
	case "shell":
		return AIStateShell
	default:
		// Anything a future version adds.
		return AIStateNone
	}
}

// serverCmdPattern matches commands whose whole job is to keep running — dev
// servers and watchers. Mirrors my-mux's SERVER_CMD list.
//
// ponytail: a plain pattern list; add new launchers here when they turn up.
var serverCmdPattern = regexp.MustCompile(
	`(yarn|npm|pnpm|bun)(\s+run)?\s+(dev|start|serve|watch)|bootRun|vite|next dev|nuxt dev|nodemon|runserver|uvicorn`)

// shellJobs is the /proc lookup, replaceable in tests.
var shellJobs = shellJobCmdlines

// demoteServerShell decides what to display for a session Claude reports as
// "shell".
//
// Claude sets "shell" whenever a background shell is alive, and in its own
// precedence that outranks both "idle" and "busy". So a session that left a dev
// server running never reaches "idle" — its turns end invisibly, and a panel
// watching for the turn to finish waits forever. Observed directly: a session
// actively generating output reported "shell" for 13 minutes because one
// background server was up.
//
// When every shell job it owns is a long-running server, the turn really is
// over and the session is waiting on the user. Anything else — a build, a
// download, a test run — means it is still busy, so the shell state stands.
//
// Nothing to inspect (no /proc, no identifiable jobs) also leaves it alone:
// absence of evidence is not evidence the turn ended.
func demoteServerShell(pid int) AIState {
	jobs := shellJobs(pid)
	if len(jobs) == 0 {
		return AIStateShell
	}
	for _, cmd := range jobs {
		if !serverCmdPattern.MatchString(cmd) {
			return AIStateShell
		}
	}
	return AIStateReady
}

// tmuxSessionName extracts the session name from Claude's tmux reference,
// formatted "<session>:@<window_id>.%<pane_id>". tmux forbids ':' and '.' in
// session names, so the first ':' is an unambiguous boundary. Returns "" when
// the reference is absent or malformed.
func tmuxSessionName(ref string) string {
	name, _, found := strings.Cut(ref, ":")
	if !found {
		return ""
	}
	return name
}

// moreUrgent reports whether a should displace b when two Claude sessions share
// one tmux session (for example, one per window). Ordering is deterministic so
// the result does not depend on directory iteration order.
func moreUrgent(a, b ClaudeStatus) bool {
	ra, rb := urgencyRank(a.State), urgencyRank(b.State)
	if ra != rb {
		return ra > rb
	}
	if !a.Since.Equal(b.Since) {
		return a.Since.After(b.Since)
	}
	return a.PID > b.PID
}

func urgencyRank(s AIState) int {
	switch s {
	case AIStateApproval:
		return 4
	case AIStateWorking:
		return 3
	case AIStateReady:
		return 2
	case AIStateShell:
		return 1
	default:
		return 0
	}
}
