package tmux

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const listFormat = "#{session_name}|#{session_windows}|#{session_created}|#{session_attached}|#{pane_current_path}|#{session_activity}|#{pane_current_command}|#{pane_pid}"

// ListSessions returns all tmux sessions sorted by attached status and recent activity.
func ListSessions() ([]Session, error) {
	out, err := runner.Output("tmux", "list-sessions", "-F", listFormat)
	if err != nil {
		// tmux returns error when no server is running
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	// One scan covers every session, so it is hoisted out of the loop. Screen
	// detection is hoisted for the same reason and costs one more round trip:
	// it batches every pane's capture into a single tmux invocation.
	statuses := ClaudeStatuses()
	screens := ScreenStates()

	sessions := make([]Session, 0, len(lines))
	for _, line := range lines {
		s, err := parseLine(line, statuses, screens)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Attached != sessions[j].Attached {
			return sessions[i].Attached
		}
		return sessions[i].Activity.After(sessions[j].Activity)
	})

	return sessions, nil
}

// SessionForTarget builds the Session owning target, in one round trip. Pass ""
// for the current pane.
//
// Every field of listFormat resolves in a pane's context, so this is
// list-sessions' format read through display-message and handed to the same
// parser — no second format string to drift, and no second place deciding what a
// Session is.
//
// It exists for `mux border`, which runs once per pane per refresh as a fresh
// process. ListSessions there would walk every session and pay for every one of
// their git and Claude lookups to render a line about one of them.
//
// The pane fields (path, command, pid) describe *target* rather than its
// session's active pane, which is what a line drawn above that pane should say.
func SessionForTarget(target string) (Session, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, listFormat)

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	// No screen states, deliberately. ScreenStates costs two tmux spawns and
	// captures every pane on the server; tmux runs this once per pane and
	// re-runs them on redraw, so paying that here is quadratic in the number of
	// panes on screen. Claude's statuses are file reads and cost nothing.
	//
	// The price is that an agent only screen detection recognises reaches the
	// border with no live state — its tool icon, not a glyph. That is the
	// budget for a renderer with no process to cache in, not an oversight.
	return parseLine(strings.TrimSpace(string(out)), ClaudeStatuses(), nil)
}

// parseLine builds a Session from one list-sessions line. statuses maps tmux
// session names to live Claude state and screens to screen-detected state;
// either may be nil.
func parseLine(line string, statuses map[string]ClaudeStatus, screens map[string]ScreenState) (Session, error) {
	parts := strings.SplitN(line, "|", 8)
	if len(parts) < 8 {
		return Session{}, fmt.Errorf("unexpected format: %s", line)
	}

	windows, _ := strconv.Atoi(parts[1])
	createdUnix, _ := strconv.ParseInt(parts[2], 10, 64)
	attached, _ := strconv.Atoi(parts[3])
	activityUnix, _ := strconv.ParseInt(parts[5], 10, 64)
	panePID, _ := strconv.Atoi(parts[7])

	activeCommand := resolveCommand(panePID, parts[6])
	gitInfo := LookupGitInfo(parts[4])

	s := Session{
		Name:          parts[0],
		WindowCount:   windows,
		Created:       time.Unix(createdUnix, 0),
		Activity:      time.Unix(activityUnix, 0),
		Attached:      attached > 0,
		Directory:     parts[4],
		ActiveCommand: activeCommand,
		PanePID:       panePID,
		GitBranch:     gitInfo.Branch,
		IsWorktree:    gitInfo.IsWorktree,
	}

	// Screen detection first, then the state file over the top of it. A tool
	// reporting its own state is better evidence than reading its screen: it
	// knows the difference between thinking and waiting for a tool call, and it
	// does not go blind when the user opens a transcript. Screen detection is
	// what covers the other nineteen agents, and Claude in the moments before
	// its state file exists.
	if sc, ok := screens[s.Name]; ok && sc.State != AIStateNone {
		s.AIState = sc.State
		s.AITool = sc.Tool
	}

	if st, ok := statuses[s.Name]; ok {
		s.AIState = st.State
		s.AITool = "claude"
		s.AIWaitingFor = st.WaitingFor
		s.AISince = st.Since
		s.AIPID = st.PID
	}

	return s, nil
}

// CreateSession creates a new detached tmux session with the given name.
func CreateSession(name string) error {
	return runner.Run("tmux", "new-session", "-d", "-s", name)
}

// CreateSessionWithDir creates a new detached tmux session starting in the given directory.
func CreateSessionWithDir(name, dir string) error {
	return runner.Run("tmux", "new-session", "-d", "-s", name, "-c", dir)
}

// KillSession destroys the tmux session with the given name.
func KillSession(name string) error {
	return runner.Run("tmux", "kill-session", "-t", name)
}

// RenameSession renames a tmux session from oldName to newName.
func RenameSession(oldName, newName string) error {
	return runner.Run("tmux", "rename-session", "-t", oldName, newName)
}

// SwitchClient points the current tmux client at the named session. Only valid
// from inside tmux, where the client is implied by $TMUX.
//
// The "=" prefix forces an exact match. Without it tmux falls back to prefix
// and then glob matching, so "mux" would be ambiguous next to "mux-old" and a
// name containing * or [ would match something else entirely.
//
// Unlike AttachToSession this returns normally instead of replacing the
// process, so it has no reason to bypass the runner.
func SwitchClient(name string) error {
	return runner.Run("tmux", "switch-client", "-t", "="+name)
}
