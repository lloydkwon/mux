package ui

import (
	"fmt"

	"github.com/lloydkwon/mux/herdr"
	"github.com/lloydkwon/mux/tmux"
)

// herdrSource reads the list from herdr instead of tmux.
//
// It is read-only by construction: there is no method here that changes
// anything, and Model.readOnly is what stops the keys that would.
type herdrSource struct{}

func (herdrSource) Sessions() ([]tmux.Session, error) { return herdr.Sessions() }

func (herdrSource) Windows(session string) ([]tmux.Window, error) {
	return herdr.Windows(session)
}

// Panes is always empty. The tree is workspace > agent: herdr's panes are
// already the window rows, so there is no third level to enumerate.
func (herdrSource) Panes(string, int) ([]tmux.Pane, error) { return nil, nil }

func (herdrSource) Capture(key previewKey) (string, error) {
	id, err := herdr.PaneID(key.session, key.window)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", nil
	}
	return herdr.CapturePane(id)
}

func (herdrSource) Title(rows int) string {
	return fmt.Sprintf("⚡ herdr workspaces %d", rows)
}

// Bar stands for the whole time herdr mode is on. A key that silently does
// nothing is indistinguishable from a broken program, and saying why once at
// the bottom answers all of them; a bar that came and went would move
// chromeTop between 1 and 2 and shift every click target by a row.
func (herdrSource) Bar() string {
	return "herdr (read-only) — attach, new, kill, rename and memo are off"
}

// NewHerdrModel returns a Model that reads herdr and changes nothing.
//
// It does not go through NewModel, and that is deliberate rather than a
// shortcut. NewModel seeds filterText from tmux's @project_dir (see its
// comment), which here would be both a wasted tmux round trip and, on a machine
// that does have a tmux server, a filter matching no herdr workspace — a list
// that opens empty and reads as "herdr never connected".
func NewHerdrModel() Model {
	prefs, err := loadPreferences()
	return Model{
		tree:     newTreeState(),
		prefs:    prefs,
		err:      err,
		src:      herdrSource{},
		readOnly: true,
	}
}
