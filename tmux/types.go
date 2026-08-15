// Package tmux provides functions for managing tmux sessions,
// capturing pane output, and detecting running processes.
package tmux

import "time"

// Session represents a tmux session with its metadata and state.
type Session struct {
	Name          string
	WindowCount   int      // total window count reported by list-sessions
	Windows       []Window // nil until enumerated via ListWindows
	Created       time.Time
	Activity      time.Time
	Attached      bool
	Directory     string
	ActiveCommand string
	PanePID       int
	GitBranch     string // current git branch, empty if not a git repo
	IsWorktree    bool   // true if Directory is a linked git worktree

	// Live AI CLI state. Two providers reach this: the tool's own state file
	// (today only Claude, via ~/.claude/sessions) and screen detection
	// (tmux/detect, twenty agents). The state file wins where both answer,
	// because a tool reporting on itself beats reading its screen.
	AIState AIState // AIStateNone when no live AI state
	// AITool names the tool the state belongs to, as an aiToolMap key. Empty
	// when no provider answered, in which case the tool is inferred from
	// ActiveCommand instead.
	AITool       string
	AIWaitingFor string    // why the tool is blocked; only for AIStateApproval
	AISince      time.Time // when the current state began; zero if unknown
	AIPID        int       // pid of the process publishing the state; 0 if none
}

// Window represents a single tmux window inside a session.
type Window struct {
	Index  int
	Name   string
	Active bool
	Panes  []Pane // nil until enumerated via ListPanes
}

// Pane represents a single tmux pane inside a window.
type Pane struct {
	Index   int
	Command string
	Active  bool
	Width   int
	Height  int
}
