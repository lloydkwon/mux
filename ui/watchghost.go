package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// ghostCheckInterval is how often the panel asks who is attached.
//
// Not every tick. The panel ticks every two seconds because a badge has to
// react to a click; a terminal someone walked away from yesterday does not, and
// there is one panel per window all asking the same server the same question.
const ghostCheckInterval = time.Minute

// ghostDetachedMsg reports a client this panel disconnected, so the pane can say
// so. Only successes arrive: with several panels racing, the losers get an error
// about a client that is already gone, which is not news.
type ghostDetachedMsg struct {
	tty     string
	session string
	size    string
}

// ghostCheckCmd finds forgotten clients and disconnects them.
//
// A command rather than work inside Update, for the reason mergeEventsCmd is:
// this shells out to tmux and `ui` has no mock runner, so doing it inline would
// make `go test ./ui` disconnect the developer's own terminal. A command is
// inert until something runs it, and nothing in the tests does.
func ghostCheckCmd(idle time.Duration) tea.Cmd {
	return func() tea.Msg {
		cs, err := tmux.ListClients()
		if err != nil {
			return nil
		}
		for _, c := range tmux.StaleClients(cs, idle, time.Now()) {
			if tmux.DetachTTY(c.TTY) == nil {
				// One per pass. The next check is a minute away, and a second
				// ghost is not urgent enough to justify reporting a batch.
				return ghostDetachedMsg{tty: c.TTY, session: c.Session, size: c.Size()}
			}
		}
		return nil
	}
}

// dueForGhostCheck reports whether enough time has passed, and stamps the model
// when it has. Split out so the cadence can be tested without a tmux server.
func (m watchModel) dueForGhostCheck(now time.Time) (watchModel, bool) {
	// Zero disables the whole thing. That is what every test gets — watchModel
	// values built by hand carry no threshold — and it is the same mechanism
	// exePath and editorBin use to keep `go test ./ui` off this machine.
	if m.ghostIdle <= 0 {
		return m, false
	}
	if !m.lastGhostCheck.IsZero() && now.Sub(m.lastGhostCheck) < ghostCheckInterval {
		return m, false
	}
	m.lastGhostCheck = now
	return m, true
}
