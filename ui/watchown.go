package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lloydkwon/mux/tmux"
)

// ownResolvedMsg carries a fresh answer to "which session is this pane in".
type ownResolvedMsg struct {
	name string
	err  error
}

// resolveOwnCmd asks tmux again which session this pane lives in.
//
// A command rather than work inside Update, for the reason mergeEventsCmd and
// ghostCheckCmd are: this shells out to tmux and `ui` has no mock runner, so
// doing it inline would make `go test ./ui` query the developer's own server
// on every tick a test drives.
func resolveOwnCmd(resolve func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		name, err := resolve()
		return ownResolvedMsg{name: name, err: err}
	}
}

// resolveOwnSession is the real resolver, wired in by RunWatch.
//
// It asks about this pane rather than about a name, which is the whole point:
// $TMUX_PANE survives a rename, and the name is what went stale.
func resolveOwnSession() (string, error) { return tmux.SessionForPane(selfPane()) }

// ownSessionLost reports that ownSession no longer names a session that exists.
//
// Free to ask: the tick already holds the list. Nothing is spent until a rename
// actually happens, which is the same rule adoptSavedWidth follows when it
// re-reads the window only where the ceiling would bite.
func (m watchModel) ownSessionLost() bool {
	if m.ownSession == "" || m.resolveOwn == nil {
		return false
	}
	for _, s := range m.sessions {
		if s.Name == m.ownSession {
			return false
		}
	}
	return true
}

// adoptOwnSession takes the re-resolved name.
//
// It does not move the cursor. Clearing ownClients hands that to
// reanchorOnArrival, which fires on the next tick if a client is actually in
// this session — one mechanism for "put the cursor where you are", and it still
// leaves a cursor alone while the user is away.
//
// A failure empties ownSession, which is the documented "could not tell" state
// *and* closes ownSessionLost's gate, so a pane that can never answer does not
// ask every two seconds. The panel recovers the mark on its next restart.
func (m watchModel) adoptOwnSession(msg ownResolvedMsg) watchModel {
	if msg.err != nil || msg.name == "" {
		m.ownSession = ""
		return m
	}
	m.ownSession = msg.name
	m.ownClients = 0
	return m
}
