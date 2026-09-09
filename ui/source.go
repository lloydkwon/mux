package ui

import "github.com/lloydkwon/mux/tmux"

// sessionSource is where the list, the tree and the preview come from.
//
// It is declared here rather than in tmux or herdr because this is where it is
// used, and because Capture takes a previewKey — a ui concept. The two
// implementations sit beside it: tmuxSource does what the loaders always did,
// herdrSource wraps the herdr package.
//
// **A nil source means tmux.** That is the whole compatibility story: every
// existing caller, and every test that builds a Model{...} by hand, keeps the
// behaviour it had without naming a source. It is the same rule exePath uses to
// disable the panel's self-restart and editorBin uses to disable its right
// click — a zero value that means "as before" is what keeps `go test ./ui` off
// the developer's own machine.
//
// One interface rather than four function fields: with four, the nil check
// lives in four places, and forgetting one would send herdr mode to tmux, which
// on a machine with no tmux server is a silently empty list — the failure that
// is hardest to trace back to its cause.
type sessionSource interface {
	Sessions() ([]tmux.Session, error)
	Windows(session string) ([]tmux.Window, error)
	Panes(session string, window int) ([]tmux.Pane, error)
	Capture(key previewKey) (string, error)
}

// tmuxSource is the default, and every method is the call the package-level
// loader made before the seam existed.
type tmuxSource struct{}

func (tmuxSource) Sessions() ([]tmux.Session, error) { return tmux.ListSessions() }

func (tmuxSource) Windows(session string) ([]tmux.Window, error) {
	return tmux.ListWindows(session)
}

func (tmuxSource) Panes(session string, window int) ([]tmux.Pane, error) {
	return tmux.ListPanes(session, window)
}

func (tmuxSource) Capture(key previewKey) (string, error) {
	return tmux.CapturePaneTarget(key.target())
}

// source returns the source to read from, resolving nil to tmux.
func (m Model) source() sessionSource {
	if m.src == nil {
		return tmuxSource{}
	}
	return m.src
}
