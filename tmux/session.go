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

	// One scan covers every session, so it is hoisted out of the loop.
	statuses := ClaudeStatuses()

	sessions := make([]Session, 0, len(lines))
	for _, line := range lines {
		s, err := parseLine(line, statuses)
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

// parseLine builds a Session from one list-sessions line. statuses maps tmux
// session names to live Claude state and may be nil.
func parseLine(line string, statuses map[string]ClaudeStatus) (Session, error) {
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

	if st, ok := statuses[s.Name]; ok {
		s.AIState = st.State
		s.AIWaitingFor = st.WaitingFor
		s.AISince = st.Since
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
