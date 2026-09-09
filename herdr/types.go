package herdr

// The wire types for herdr's CLI, which prints the socket API's JSON to stdout.
//
// **Every field is optional and every decode must degrade rather than fail.**
// The format is herdr's own, private and unversioned — the same rule
// tmux/claude_status.go states for Claude's state file. A renamed field must
// cost a badge, never the list. That is why nothing here is a pointer and
// nothing is required: a missing key leaves a zero value, and the mapping layer
// decides what a zero value means.

// envelope is what every herdr command prints: an id plus exactly one of result
// or error.
type envelope struct {
	ID     string         `json:"id"`
	Result envelopeResult `json:"result"`
	Error  *envelopeError `json:"error"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// envelopeResult holds every list shape we ask for. One struct rather than
// three because the three calls are only ever made together, by Snapshot.
type envelopeResult struct {
	Type       string          `json:"type"`
	Workspaces []workspaceInfo `json:"workspaces"`
	Panes      []paneInfo      `json:"panes"`
	Agents     []paneInfo      `json:"agents"`
}

// workspaceInfo is one herdr workspace — what becomes a session row.
//
// Note what is *not* here, measured against a live server rather than assumed:
// no cwd, no created, no activity. The schema has no such fields either, so the
// session's directory and age both have to be worked out from its panes.
type workspaceInfo struct {
	WorkspaceID string        `json:"workspace_id"`
	Number      int           `json:"number"`
	Label       string        `json:"label"`
	Focused     bool          `json:"focused"`
	PaneCount   int           `json:"pane_count"`
	TabCount    int           `json:"tab_count"`
	ActiveTabID string        `json:"active_tab_id"`
	AgentStatus string        `json:"agent_status"`
	GitBranch   string        `json:"git_branch"`
	GitAhead    int           `json:"git_ahead"`
	GitBehind   int           `json:"git_behind"`
	Worktree    *worktreeInfo `json:"worktree"`
}

// worktreeInfo is present only for a workspace herdr opened on a git worktree.
// Every live workspace observed here had none, so the mapping must have an
// answer for its absence rather than treating it as the normal case.
type worktreeInfo struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// paneInfo covers both `pane list` and `agent list`: the agent listing is the
// pane listing narrowed to panes that host an agent, with `name` added. They
// are joined on PaneID — measured, `pane list` returned 10 and `agent list` 9,
// the odd one being a plain shell.
type paneInfo struct {
	PaneID        string            `json:"pane_id"`
	TabID         string            `json:"tab_id"`
	WorkspaceID   string            `json:"workspace_id"`
	TerminalID    string            `json:"terminal_id"`
	Agent         string            `json:"agent"`
	AgentSession  *agentSessionInfo `json:"agent_session"`
	AgentStatus   string            `json:"agent_status"`
	CWD           string            `json:"cwd"`
	ForegroundCWD string            `json:"foreground_cwd"`
	Focused       bool              `json:"focused"`
	// Name is herdr's own name for the agent ("govgen-plan"). It is absent more
	// often than one would guess — this repo's own session had none — so the
	// mapping needs a fallback chain rather than a default.
	Name                  string `json:"name"`
	TerminalTitle         string `json:"terminal_title"`
	TerminalTitleStripped string `json:"terminal_title_stripped"`
	StateChangedAtUnixMs  int64  `json:"state_changed_at_unix_ms"`
	InteractiveReady      bool   `json:"interactive_ready"`
}

// agentSessionInfo points at the tool's own session. Kind is "id" or "path";
// only "id" carries a session identifier (Claude's UUID) — "path" is a
// transcript path, and reading one as the other would silently look for a file
// that is not there.
type agentSessionInfo struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

// sessionID returns the tool's session identifier, empty unless herdr said it
// is one.
func (a *agentSessionInfo) sessionID() string {
	if a == nil || a.Kind != "id" {
		return ""
	}
	return a.Value
}
