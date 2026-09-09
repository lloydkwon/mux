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

// resumeRequest is a row that cannot be attached to because nothing in tmux is
// holding it — the conversation has to be started somewhere before there is
// anywhere to go. cmd/mux acts on it after the TUI exits, the same way it acts
// on attachTarget, and for the same reason: creating a session and attaching to
// it are both things that must not happen inside the Bubble Tea loop.
type resumeRequest struct {
	// Name is the tmux session to create, already sanitised.
	Name string
	Dir  string
	// Args is the command to run in it, argv style.
	Args []string
	// Forked records that something else still holds the conversation, so the
	// resume was given its own id rather than sharing a transcript.
	Forked bool
}

// sourceActivator is the optional half of a source: what committing a row
// means.
//
// It is separate from sessionSource because the two answers are unrelated. A
// source that only lists implements neither and its rows do nothing when
// committed (herdr); a source whose rows live somewhere mux can reach says
// where (tmux); a source whose rows may live nowhere yet says how to start one
// (Claude). Folding this into sessionSource would make every source answer a
// question most of them have no business answering.
type sourceActivator interface {
	// Activate reports what to do with the named row: attach to a tmux target,
	// or start one. ok is false when the row cannot be committed at all.
	Activate(session string) (attach previewKey, resume *resumeRequest, ok bool)
}

// sourceChrome lets a source name itself on screen.
//
// The title and the standing bar say what the rows *are* and what is off, and
// only the source knows both. Keeping the strings beside the source that means
// them is what stops the list claiming to be listing tmux sessions while it
// draws something else — which it did, until it said "herdr" in one place and
// "tmux sessions" in the title right above it.
//
// tmuxSource implements neither: its title is the default and it has nothing to
// warn about.
type sourceChrome interface {
	Title(rows int) string
	Bar() string
}

// chrome returns the source's own title and bar, and whether it has any.
func (m Model) chrome() (sourceChrome, bool) {
	c, ok := m.src.(sourceChrome)
	return c, ok
}

// source returns the source to read from, resolving nil to tmux.
func (m Model) source() sessionSource {
	if m.src == nil {
		return tmuxSource{}
	}
	return m.src
}
