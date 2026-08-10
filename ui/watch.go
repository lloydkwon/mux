package ui

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// watchInterval is how often the panel pane re-reads sessions.
//
// Slower than the TUI's 500ms on purpose. That rate exists to keep the cursor
// row's preview live; a display-only pane has no cursor, and this runs as a
// second long-lived process alongside the shell you are actually working in.
// Claude's status cache is 1s, so 2s loses nothing it could have shown.
const watchInterval = 2 * time.Second

type watchTickMsg time.Time

func watchTick() tea.Cmd {
	return tea.Tick(watchInterval, func(t time.Time) tea.Msg {
		return watchTickMsg(t)
	})
}

// watchModel renders the notification panel to fill a dedicated tmux pane.
//
// It exists because tmux has no floating window: `display-popup` freezes the
// panes behind it and takes the keyboard ("Panes are not updated while a popup
// is present"), so the only always-visible surface that still lets you type is
// a real pane.
type watchModel struct {
	width, height int
	sessions      []tmux.Session
	prevAIStates  map[string]tmux.AIState
	events        []aiEvent
	err           error

	// Held so a re-layout can be told from a drag. winWidth is the window size
	// this pane was last seen in; targetWidth is the width to hold it at.
	winWidth    int
	targetWidth int
}

// RunWatch renders the panel until the user quits. Used by `mux watch`.
//
// Mouse reporting is on so session rows can be clicked. The cost is that tmux
// hands this pane every mouse event instead of handling it itself, so its
// wheel-scroll into copy-mode and drag-to-select stop working here; holding
// Shift still gets the terminal's own selection.
func RunWatch() error {
	_, err := tea.NewProgram(watchModel{},
		tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m watchModel) Init() tea.Cmd {
	return tea.Batch(loadSessions, watchTick())
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		prev := m.width
		m.width = msg.Width
		m.height = msg.Height
		// A resize only says this pane changed, not why. Ask tmux how wide the
		// window is now — that is what tells a drag apart from a re-layout.
		if msg.Width != prev {
			return m.applyResize(msg.Width)
		}
		return m, nil

	case tea.KeyMsg:
		// Display-only: only quitting is worth a key. Anything else would make
		// a pane you are not focused on look like it swallowed a keystroke.
		if k := msg.String(); k == "q" || k == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil

	case tea.MouseMsg:
		// Press only. A release carries the same button under SGR, and a drag
		// arrives as motion — neither should switch sessions.
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
			return m, nil
		}
		name := m.sessionAtRow(msg.Y)
		if name == "" {
			return m, nil
		}
		return m, switchToSession(name)

	case switchFailedMsg:
		// Surface it in the log rather than failing silently: the session can
		// vanish between the render and the click.
		m.events = pushEvents(m.events, []aiEvent{{
			at: time.Now(), session: msg.session, text: "⚠ 전환 실패: " + msg.err.Error(),
		}})
		return m, nil

	case watchTickMsg:
		return m, tea.Batch(loadSessions, watchTick())

	case sessionsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			// Same order as the TUI: diff before the slice is replaced. This
			// process keeps its own history — it cannot share the TUI's, and a
			// panel that only logs while mux is open would defeat the point.
			fresh, next := detectTransitions(m.prevAIStates, msg.sessions, time.Now())
			m.prevAIStates = next
			m.events = pushEvents(m.events, fresh)
			m.sessions = msg.sessions
		}
		return m, nil
	}
	return m, nil
}

// selfPane is this process's own pane, from the environment tmux sets for it.
//
// Every tmux call from here must name it. Left to infer, tmux resolves an
// omitted target to the *active* pane — which is the pane the user is working
// in, not this one, since the panel is created detached. A width correction
// sent that way shrank the wrong pane and the panel grew to fill what it left.
func selfPane() string { return os.Getenv("TMUX_PANE") }

// applyResize decides whether a resize was the user or tmux, and undoes it when
// it was tmux.
//
// The window staying the same size while this pane changes can only be a border
// drag — take it as the new target and remember it. The window changing size
// means tmux redistributed its panes proportionally, which is what made the
// panel drift 40 → 46 and differ between sessions: with `aggressive-resize on`,
// switching sessions resizes windows, so every switch nudged the panel.
//
// The window width is read *synchronously*, which is the one place this file
// blocks. Asking for it in a tea.Cmd made the two facts arrive separately: with
// resizes landing back to back, a stale window width matched the current one,
// the change read as a drag, and a momentarily squeezed pane became the width
// being enforced — observed collapsing the panel to a single column.
//
// The lower clamp is the second guard: a width the panel could not render in is
// never adopted as intent, so a transient squeeze cannot become permanent.
func (m watchModel) applyResize(paneWidth int) (tea.Model, tea.Cmd) {
	winWidth, err := tmux.WindowWidth(selfPane())
	if err != nil {
		return m, nil // nothing to compare against; leave the width alone
	}
	m, cmd := m.applyResizeWith(paneWidth, winWidth)
	return m, cmd
}

// applyResizeWith is the decision itself, split out so it can be tested without
// a tmux server.
func (m watchModel) applyResizeWith(paneWidth, winWidth int) (watchModel, tea.Cmd) {
	switch {
	case m.winWidth == 0: // first size seen — adopt it
		m.winWidth = winWidth
		if paneWidth >= notifyMinWidth {
			m.targetWidth = paneWidth
		}
		return m, nil

	case winWidth < tmux.MinWindowWidth:
		// The window shrank past what it can spare — a phone attached, most
		// likely. Leave, so the work pane gets its columns back; `prefix+a`
		// brings the panel back on a wide screen.
		//
		// Only on the *transition*: a panel opened by hand in an already narrow
		// window stays, for the same reason the key overrides the hook.
		return m, tea.Quit

	case winWidth == m.winWidth: // the user dragged the border
		if paneWidth < notifyMinWidth {
			return m, nil
		}
		m.targetWidth = paneWidth
		return m, rememberPanelWidth(paneWidth)

	default: // tmux re-laid the window out
		m.winWidth = winWidth
		if m.targetWidth > 0 && paneWidth != m.targetWidth {
			return m, restorePanelWidth(m.targetWidth)
		}
		return m, nil
	}
}

// rememberPanelWidth records the pane's width for its session, so reopening the
// panel there brings back the size the user dragged it to.
//
// Failure is deliberately silent: this is a convenience, and a session that
// cannot be resolved (the pane is going away, tmux is shutting down) is not
// worth a line in the event log.
func rememberPanelWidth(width int) tea.Cmd {
	return func() tea.Msg {
		if session, err := tmux.SessionForPane(selfPane()); err == nil {
			_ = tmux.SetPanelWidth(session, width)
		}
		return nil
	}
}

func restorePanelWidth(width int) tea.Cmd {
	return func() tea.Msg {
		_ = tmux.ResizePaneWidth(selfPane(), width)
		return nil
	}
}

type switchFailedMsg struct {
	session string
	err     error
}

// switchToSession moves the current tmux client. It stays a tea.Cmd rather than
// quitting first, the way the TUI's attach must: switch-client returns normally
// instead of replacing this process, so the panel survives the switch.
func switchToSession(name string) tea.Cmd {
	return func() tea.Msg {
		// Hand focus back before leaving. tmux made this pane active to deliver
		// the click, and the window we are about to leave would otherwise keep
		// reporting the panel's command and directory as the session's own.
		// Best-effort: there may be no previous pane, and that must not block
		// the switch the user actually asked for.
		_ = tmux.RestoreLastPane(selfPane())

		if err := tmux.SwitchClient(name); err != nil {
			return switchFailedMsg{session: name, err: err}
		}
		return nil
	}
}

// sessionAtRow maps a pane row to the session on it, empty when that row is not
// a session. The watch pane draws no border and starts at the top of the alt
// screen, so a row index is a line index — the TUI overlay's copy of this panel
// is offset and must not use this.
func (m watchModel) sessionAtRow(y int) string {
	if y < 0 || m.width < notifyMinWidth || m.err != nil {
		return ""
	}
	lines := notifyLines(m.sessions, m.events, m.width)
	if y >= len(lines) {
		return ""
	}
	return lines[y].session
}

func (m watchModel) View() string {
	if m.width == 0 {
		return ""
	}
	return fixedBox(m.body(), m.width, m.height)
}

// body returns the pane's content before it is clipped to size. No border: the
// tmux pane already draws one.
func (m watchModel) body() string {
	if m.width < notifyMinWidth {
		// Too narrow for the columns to mean anything. Say so rather than
		// rendering a shredded panel.
		return helpStyle.Render(fitCells("좁음", m.width))
	}
	if m.err != nil {
		return errorStyle.Render(fitCells(m.err.Error(), m.width))
	}

	lines := notifyLines(m.sessions, m.events, m.width)
	if len(lines) == 0 {
		return helpStyle.Render(fitCells(" AI 세션 없음", m.width))
	}
	return strings.Join(notifyTexts(lines), "\n")
}
