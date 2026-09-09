package herdr

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// This is the only file in the package that imports tmux. Everything above it
// speaks herdr's vocabulary; everything below the ui seam speaks mux's, and the
// translation happens here so there is one place to look when a field is wrong.

// isWorktree answers whether a directory is a linked git worktree, for the
// workspaces herdr reports no worktree block for — which, measured, is all of
// them. tmux.LookupGitInfo is filesystem-only behind a 5s cache, so it costs no
// round trip and gives the same answer the tmux path shows.
//
// It is a var so the mapping stays testable against the captured fixtures:
// those carry real paths on the machine they were captured from, and a test
// that reached the disk would answer differently on another one.
var isWorktree = func(dir string) bool { return tmux.LookupGitInfo(dir).IsWorktree }

// mapState turns herdr's agent lifecycle into the badge mux already draws.
//
// The one judgement call is idle. herdr documents idle and done as both meaning
// "ready for input", differing only in whether the completion has been seen —
// so done is a finished turn (which is what AIStateReady says, word for word)
// and idle is a session sitting at its prompt with nothing to report. Mapping
// idle to Ready as well was the obvious alternative and is wrong: measured,
// five of nine live agents were idle, so every row would carry ✅ and the badge
// would stop distinguishing anything. That is the same failure CLAUDE.md
// records for Claude's sticky "shell" status.
//
// AIStateNone does not leave the row bare. SessionAITool answers from AITool,
// and ui/status.go's aiGlyph falls back to the tool's own icon when there is no
// state glyph — which is exactly how an idle Claude renders on the tmux path.
func mapState(status string) tmux.AIState {
	switch status {
	case "working":
		return tmux.AIStateWorking
	case "blocked":
		return tmux.AIStateApproval
	case "done":
		return tmux.AIStateReady
	default: // "idle", "unknown", anything herdr adds later
		return tmux.AIStateNone
	}
}

// statusRank orders agents for picking the one that speaks for a workspace.
// Higher wins; a blocked agent outranks a busy one because it is the row the
// user has to act on.
func statusRank(status string) int {
	switch status {
	case "blocked":
		return 4
	case "working":
		return 3
	case "done":
		return 2
	case "idle":
		return 1
	default:
		return 0
	}
}

// paneNumber pulls the n out of a pane id ("w8:p1" -> 1). Zero when herdr ever
// changes the shape — a window index of 0 is harmless, it only orders rows.
func paneNumber(paneID string) int {
	i := strings.LastIndex(paneID, ":p")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(paneID[i+2:])
	if err != nil {
		return 0
	}
	return n
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// sessionNames assigns each workspace the name its row is keyed by.
//
// Uniqueness is not cosmetic: pruneCaches, preferences.Orders and focusSession
// all key on Session.Name, so two workspaces sharing a label would share an
// expansion state and a sort order. herdr's own label is used where it is free;
// a collision takes "label#number", and number is herdr's, so the same
// workspace keeps the same name across ticks.
func sessionNames(ws []workspaceInfo) map[string]string {
	taken := make(map[string]bool, len(ws))
	names := make(map[string]string, len(ws))
	for _, w := range ws {
		name := w.Label
		if name == "" {
			name = w.WorkspaceID
		}
		if taken[name] {
			name = name + "#" + strconv.Itoa(w.Number)
		}
		// Still taken (two workspaces with the same label and number is not
		// possible today, but the id always is unique).
		if taken[name] {
			name = w.WorkspaceID
		}
		taken[name] = true
		names[w.WorkspaceID] = name
	}
	return names
}

// panesByWorkspace groups the snapshot's panes, newest-numbered last.
func panesByWorkspace(snap *snapshot) map[string][]paneInfo {
	by := make(map[string][]paneInfo, len(snap.workspaces))
	for _, p := range snap.panes {
		by[p.WorkspaceID] = append(by[p.WorkspaceID], p)
	}
	for id := range by {
		ps := by[id]
		sort.SliceStable(ps, func(i, j int) bool {
			return paneNumber(ps[i].PaneID) < paneNumber(ps[j].PaneID)
		})
		by[id] = ps
	}
	return by
}

// leadPane picks the pane that speaks for its workspace: the one whose state
// the user would act on first, and among equals the one that changed most
// recently. It supplies the session's directory, task, tool and age.
func leadPane(ps []paneInfo) (paneInfo, bool) {
	best := -1
	for i, p := range ps {
		if best < 0 {
			best = i
			continue
		}
		br, pr := statusRank(ps[best].AgentStatus), statusRank(p.AgentStatus)
		if pr > br || (pr == br && p.StateChangedAtUnixMs > ps[best].StateChangedAtUnixMs) {
			best = i
		}
	}
	if best < 0 {
		return paneInfo{}, false
	}
	return ps[best], true
}

// sessionsFrom is the whole mapping, kept pure so it can be tested against the
// captured fixtures without a herdr anywhere near it.
func sessionsFrom(snap *snapshot, now time.Time) []tmux.Session {
	names := sessionNames(snap.workspaces)
	byWS := panesByWorkspace(snap)

	sessions := make([]tmux.Session, 0, len(snap.workspaces))
	for _, w := range snap.workspaces {
		ps := byWS[w.WorkspaceID]
		s := tmux.Session{
			Name:        names[w.WorkspaceID],
			WindowCount: w.PaneCount,
			Attached:    w.Focused,
			GitBranch:   w.GitBranch,
			AIState:     mapState(w.AgentStatus),
			Windows:     windowsFrom(ps, snap.agentNames),
		}

		if lead, ok := leadPane(ps); ok {
			s.Directory = lead.CWD
			s.ActiveCommand = lead.Agent
			s.AITool = lead.Agent
			s.AITask = lead.TerminalTitleStripped
			s.AISince = msToTime(lead.StateChangedAtUnixMs)
		}

		// A workspace opened on a linked worktree knows its own paths; every
		// live one measured here carried no worktree block at all, so the pane's
		// cwd is the normal answer and the block is the exception.
		if w.Worktree != nil {
			if w.Worktree.CheckoutPath != "" {
				s.Directory = w.Worktree.CheckoutPath
			}
			s.ProjectDir = w.Worktree.RepoRoot
			s.IsWorktree = w.Worktree.IsLinkedWorktree
		}
		if s.ProjectDir == "" {
			s.ProjectDir = s.Directory
		}
		if !s.IsWorktree && s.Directory != "" {
			s.IsWorktree = isWorktree(s.Directory)
		}

		s.Created = createdFor(ps, now)
		sessions = append(sessions, s)
	}

	// Focused first, then most recently active — the order tmux.ListSessions
	// returns, so the list opens the same way whichever source filled it.
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Attached != sessions[j].Attached {
			return sessions[i].Attached
		}
		return sessions[i].Created.After(sessions[j].Created)
	})
	return sessions
}

// createdFor is what the elapsed column counts from, and it must never be zero.
//
// herdr has no workspace creation time — not in the response and not in the
// schema. A zero Created reaches ui/list.go's compactAgo as time.Since(year 1),
// which renders "740049d": seven cells in a column budgeted for four, and
// because timeAgo's %4s pads without truncating, every column after it shifts.
//
// The newest state change in the workspace is the honest substitute and reads
// better than a creation time would: it answers "how long since anything
// happened here". With no panes at all there is nothing to measure and the
// snapshot's own clock stands in, which renders a stable "0s".
func createdFor(ps []paneInfo, now time.Time) time.Time {
	var newest int64
	for _, p := range ps {
		if p.StateChangedAtUnixMs > newest {
			newest = p.StateChangedAtUnixMs
		}
	}
	if newest > 0 {
		return time.UnixMilli(newest)
	}
	return now
}

// windowsFrom turns a workspace's panes into the rows nested under it.
//
// herdr is workspace > tab > pane, but every live workspace measured had
// exactly one tab labelled "1", so keeping the tab layer would cost a permanent
// empty row per workspace and a second keypress to reach an agent. Pane ids are
// workspace-scoped ("w8:p1", "w8:p2" in one tab), so dropping the tab layer
// loses no way to address a pane.
func windowsFrom(ps []paneInfo, agentNames map[string]string) []tmux.Window {
	if len(ps) == 0 {
		return nil
	}
	ws := make([]tmux.Window, 0, len(ps))
	for _, p := range ps {
		ws = append(ws, tmux.Window{
			Index:   paneNumber(p.PaneID),
			Name:    windowName(p, agentNames[p.PaneID]),
			Active:  p.Focused,
			AIState: mapState(p.AgentStatus),
			AITool:  p.Agent,
			AITask:  p.TerminalTitleStripped,
			AISince: msToTime(p.StateChangedAtUnixMs),
		})
	}
	return ws
}

// windowName names an agent row. The fallback chain is not defensive padding:
// measured, this repository's own pane had no herdr name, and a pane hosting no
// agent has neither name nor title.
func windowName(p paneInfo, agentName string) string {
	if agentName != "" {
		return agentName
	}
	if p.TerminalTitleStripped != "" {
		return p.TerminalTitleStripped
	}
	if p.Agent != "" {
		return p.Agent
	}
	return "p" + strconv.Itoa(paneNumber(p.PaneID))
}

// Sessions returns herdr's workspaces as mux sessions, each carrying its agents
// as windows.
func Sessions() ([]tmux.Session, error) {
	snap, err := Snapshot()
	if err != nil {
		return nil, err
	}
	return sessionsFrom(snap, time.Now()), nil
}

// Windows returns the agent rows for one session. They arrived with the session
// list, so this reads the same cached snapshot rather than asking again.
func Windows(session string) ([]tmux.Window, error) {
	ss, err := Sessions()
	if err != nil {
		return nil, err
	}
	for _, s := range ss {
		if s.Name == session {
			return s.Windows, nil
		}
	}
	return nil, nil
}

// PaneID resolves a (session, window index) pair back to the herdr pane it came
// from, which is what the preview has to be asked for.
func PaneID(session string, window int) (string, error) {
	snap, err := Snapshot()
	if err != nil {
		return "", err
	}
	names := sessionNames(snap.workspaces)
	byWS := panesByWorkspace(snap)
	for _, w := range snap.workspaces {
		if names[w.WorkspaceID] != session {
			continue
		}
		ps := byWS[w.WorkspaceID]
		if window < 0 {
			// "the active one", the same meaning previewKey gives -1.
			if lead, ok := leadPane(ps); ok {
				return lead.PaneID, nil
			}
			return "", nil
		}
		for _, p := range ps {
			if paneNumber(p.PaneID) == window {
				return p.PaneID, nil
			}
		}
	}
	return "", nil
}
