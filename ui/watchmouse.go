package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// With tmux's `mouse` off, a click on this panel arrives only by accident.
//
// tmux then turns the outer terminal's mouse reporting on and off to match the
// *active* pane's own mouse mode, and hands whatever arrives to the pane under
// the pointer. In a window whose active pane is claude — which turns mouse
// reporting on — the terminal sends clicks and the panel gets them. In a window
// whose active pane is a shell, the terminal sends nothing, the panel receives
// nothing, and a click on a session row only starts the terminal's own text
// selection. Measured, and pinned in scripts/tmux-assumptions.sh section 13.
//
// mux does not turn the option on (tmux.MouseEnabled says why), so the panel
// says it is off instead: a panel that works in some windows and not in others
// looks broken, not reduced.

// mouseStateMsg carries the answer to "is tmux's mouse option off".
type mouseStateMsg struct{ off bool }

// mouseCheckCmd asks probe in a command rather than in Update, for the reason
// ghostCheckCmd does: `ui` has no mock runner. nil probe, nil command.
func mouseCheckCmd(probe func() bool) tea.Cmd {
	if probe == nil {
		return nil
	}
	return func() tea.Msg { return mouseStateMsg{off: !probe()} }
}

// dueForMouseCheck reports whether to ask again, and stamps the model when it
// is. On the ghost check's minute: the option changes when someone edits their
// config, not between ticks, and one panel per window asking every two seconds
// would be a show-options per panel per tick for the same answer. A minute is
// also how long the hint outlives `set -g mouse on`.
func (m watchModel) dueForMouseCheck(now time.Time) (watchModel, bool) {
	if m.probeMouse == nil {
		return m, false
	}
	if !m.lastMouseCheck.IsZero() && now.Sub(m.lastMouseCheck) < ghostCheckInterval {
		return m, false
	}
	m.lastMouseCheck = now
	return m, true
}

// mouseHintLine is the row that says so, with the fix in it. No glyph: the
// width of the ambiguous warning signs differs between terminals (rendering.md).
func mouseHintLine(width int) notifyLine {
	return notifyLine{text: helpStyle.Render(padOrTruncate(" 마우스 꺼짐 · set -g mouse on", width))}
}
