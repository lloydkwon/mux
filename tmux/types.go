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

	// ProjectDir is the session's @project_dir tmux option — the directory the
	// session was opened for, set by the tmux-project VS Code profile. Empty
	// for sessions nobody tagged. Unlike Directory (which follows the active
	// pane, and is overwritten by the AI's own cwd) it never moves, which is
	// what makes it usable as project identity.
	ProjectDir string

	// Note is the one-line note the user attached to this session, from the
	// @mux_note tmux option. Everything else on this struct is something mux
	// worked out; this is the only field a person wrote. Empty is the normal
	// case and never an error — most sessions do not need one.
	Note string

	// Live AI CLI state. Two providers reach this: the tool's own state file
	// (today only Claude, via ~/.claude/sessions) and screen detection
	// (tmux/detect, twenty agents). The state file wins where both answer,
	// because a tool reporting on itself beats reading its screen.
	AIState AIState // AIStateNone when no live AI state
	// AITool names the tool the state belongs to, as an aiToolMap key. Empty
	// when no provider answered, in which case the tool is inferred from
	// ActiveCommand instead.
	AITool       string
	AIWaitingFor string // why the tool is blocked; only for AIStateApproval
	// AITask is the tool's own name for the work in hand, when it publishes one
	// (today only Claude, via the "name" field of its state file). Empty
	// otherwise — a session with no task name is not an error, it is a session
	// whose tool does not say.
	AITask  string
	AISince time.Time // when the current state began; zero if unknown
	AIPID   int       // pid of the process publishing the state; 0 if none

	// WindowIndex and PaneIndex locate a pane inside the session. They are
	// strings because nothing does arithmetic on them and tmux may render either
	// as empty when it has nothing to report.
	WindowIndex string
	PaneIndex   string
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
