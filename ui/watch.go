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
	err           error

	// events is what the panel draws: the shared log and this pane's own
	// failures, interleaved by time.
	events []aiEvent

	// shared is the log every panel on the server reads and writes, held here as
	// it was last merged. A panel that has just started gets the whole history on
	// its first merge, which is the point — it observes nothing on its first tick
	// (every session is "seen for the first time") and would otherwise open empty
	// next to panels that have been up for hours.
	shared []aiEvent

	// local is the events that are true of this pane only — a click of ours that
	// failed to switch. Sharing those would put a line nobody can act on in every
	// other window on the server.
	local []aiEvent

	// selected is the cursor `mux nav` moves and enter commits, held by name
	// rather than by row index: rows are ordered by how long a state has held,
	// so an index points at a different session two seconds later.
	selected string

	// ownSession is the session this pane lives in — the one on screen in the
	// pane beside the panel. Its row is marked ◀ and is where the cursor opens,
	// so the panel says where you are before it says anything else.
	//
	// Empty means "could not tell", and the panel then falls back to the top row.
	// Resolved once at startup — a pane can be moved between sessions, but being
	// wrong costs a stale marker, not correctness.
	ownSession string

	// Held so a re-layout can be told from a drag. winWidth is the window size
	// this pane was last seen in; targetWidth is the width to hold it at.
	winWidth    int
	targetWidth int

	// showHeader turns the session header on. Off unless @mux_panel_header says
	// otherwise, and resolved once at startup for the same reason
	// minWindowWidth is: a preference does not change between ticks, and the
	// panel should not spend a show-options every two seconds asking.
	showHeader bool

	// minWindowWidth is the window width below which this pane leaves, resolved
	// once at startup so applyResizeWith stays pure and a resize costs no extra
	// tmux call. Zero means "not resolved" and reads as the default.
	minWindowWidth int
}

// minWidth is the bar this pane leaves below. The same number decides whether a
// panel is opened at all (tmux.MinWindowWidth): a window mux would refuse to
// open one in is a window an open one should not stay in.
func (m watchModel) minWidth() int {
	if m.minWindowWidth > 0 {
		return m.minWindowWidth
	}
	return tmux.DefaultMinWindowWidth
}

// RunWatch renders the panel until the user quits. Used by `mux watch`.
//
// Mouse reporting is on so session rows can be clicked. The cost is that tmux
// hands this pane every mouse event instead of handling it itself, so its
// wheel-scroll into copy-mode and drag-to-select stop working here; holding
// Shift still gets the terminal's own selection.
func RunWatch() error {
	// Resolved here rather than in a tea.Cmd: it is wanted by the very first
	// auto-selection, and asking for it asynchronously would let the panel pick
	// its own session once and then correct itself a frame later.
	own, err := tmux.SessionForPane(selfPane())
	if err != nil {
		own = "" // not knowing is a state; see watchModel.ownSession
	}

	// Name the pane, so that whatever restores this window later can tell that
	// the pane it hands back used to be the panel. Done here rather than where
	// the pane is created, so a panel someone started by hand with `mux watch`
	// is marked too — the same reason panelCommand matches that pane.
	//
	// A title mux could not set is not a reason to refuse to draw.
	_ = tmux.MarkPanelPane(selfPane())

	_, err = tea.NewProgram(
		watchModel{
			ownSession:     own,
			minWindowWidth: tmux.MinWindowWidth(),
			showHeader:     tmux.PanelHeaderEnabled(),
		},
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
		return m.handleKey(msg)

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
		// The panel shows no output to read first, so a click is the decision.
		// The keyboard keeps the two steps it needs: `mux nav` moves the cursor
		// and enter commits.
		return m, switchToSession(name)

	case switchFailedMsg:
		// Surface it in the log rather than failing silently: the session can
		// vanish between the render and the click. Local, not shared — it says
		// this pane's click failed, which is not news in anyone else's window.
		m.local = pushEvents(m.local, []aiEvent{{
			at: time.Now(), session: msg.session, text: "⚠ 전환 실패: " + msg.err.Error(),
		}})
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case eventsMergedMsg:
		m.shared = msg.events
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case watchTickMsg:
		return m, tea.Batch(loadSessions, watchTick())

	case sessionsLoadedMsg:
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		// Same order as the TUI: diff before the slice is replaced. A transition
		// can only be seen by diffing, so this stays per-process — but what it
		// observes goes to the log every panel shares, which is why a panel
		// created ten minutes from now still opens with this in it.
		fresh, states := detectTransitions(m.prevAIStates, msg.sessions, time.Now())
		m.prevAIStates = states
		m.sessions = msg.sessions
		// Show our own observation now rather than waiting for it to come back
		// from the store — this pane saw it, and a two-second lag on the one
		// event you were watching for is exactly the lag that matters. The merge
		// then replaces this wholesale, so an optimistic row cannot survive as a
		// duplicate of the shared one.
		m.shared = pushEvents(m.shared, fresh)
		m.events = combineEvents(m.shared, m.local)
		// Merging is a command, not work done here: the store shells out to tmux,
		// and Update is called directly by tests that must not touch the
		// developer's own server. It runs even with nothing fresh — reading is
		// how this panel learns what the others have seen.
		return m.reselect(), mergeEventsCmd(fresh)
	}
	return m, nil
}

// handleKey drives the selection. Keys reach this pane by `send-keys` from a
// tmux binding rather than by it being focused — that is the whole point, since
// focusing the panel would take the keyboard away from the pane you are typing
// in. See `mux nav`.
func (m watchModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		// Leave without choosing. Only reachable when the focus key put the
		// cursor here, which is also when restoreFocus's guard lets it act.
		return m, leaveFocus()
	case "up", "k":
		return m.moveSelection(-1)
	case "down", "j":
		return m.moveSelection(1)
	case "home", "g":
		return m.moveSelection(-len(m.sessionOrder()))
	case "end", "G":
		return m.moveSelection(len(m.sessionOrder()))
	case "enter":
		if m.selected == "" {
			return m, nil
		}
		return m, switchToSession(m.selected)
	}
	return m, nil
}

// sessionOrder lists the sessions the panel shows, top to bottom. Derived from
// the rendered rows rather than from the session slice, so it cannot disagree
// with what a click on the same row would pick.
func (m watchModel) sessionOrder() []string {
	var order []string
	var last string
	for _, l := range m.sessionLines() {
		if l.session != "" && l.session != last {
			order = append(order, l.session)
		}
		last = l.session
	}
	return order
}

// moveSelection steps delta rows through the list, stopping at either end.
// Wrapping would make a held key cycle forever with nothing to say it had.
func (m watchModel) moveSelection(delta int) (tea.Model, tea.Cmd) {
	order := m.sessionOrder()
	if len(order) == 0 {
		return m, nil
	}
	at := 0
	for i, name := range order {
		if name == m.selected {
			at = clampInt(i+delta, 0, len(order)-1)
			break
		}
	}
	return m.selectSession(order[at]), nil
}

// selectSession moves the cursor to name.
func (m watchModel) selectSession(name string) watchModel {
	if name != "" {
		m.selected = name
	}
	return m
}

// reselect keeps the cursor pointing at a session that still exists, and picks
// a row when there is nothing selected yet.
func (m watchModel) reselect() watchModel {
	for _, s := range m.sessions {
		if s.Name == m.selected {
			return m
		}
	}
	m.selected = m.autoSelect()
	return m
}

// autoSelect picks the row the cursor lands on when nothing is selected: the
// session this pane lives in, so the highlight and the ◀ mark agree on where you
// are.
//
// It used to skip that session, on the reasoning that pressing enter there would
// go nowhere. Wrong on both counts. The list is one panel per window, so
// switching sessions lands you in front of a *different* panel — one that had
// never been told anything — and skipping meant it opened with some unrelated
// session highlighted, which is a sidebar failing at the one thing a sidebar is
// for. And enter there does do something: switchToSession restores the focus
// before it switches, so on the session you are already in it hands you back to
// the pane you were working in.
//
// Falls back to the top row when ownSession is empty ("could not tell") or has
// gone from the list.
func (m watchModel) autoSelect() string {
	order := m.sessionOrder()
	for _, name := range order {
		if name == m.ownSession {
			return name
		}
	}
	if len(order) > 0 {
		return order[0]
	}
	return ""
}

// restoreFocus hands the active pane back, but only when this pane has it.
//
// tmux makes a pane active before forwarding a click, so after a click this is
// exactly what needs undoing — left alone, the window keeps reporting the
// panel's directory as its session's own.
//
// The guard is what makes it safe on the key path. Keys arrive by send-keys
// without the panel ever becoming active, and `select-pane -l` there selects
// whatever the window visited before the pane the user is typing in. Measured:
// pressing enter from a three-pane window moved the focus to the third pane.
func restoreFocus() {
	self := selfPane()
	if tmux.PaneActive(self) {
		_ = tmux.RestoreLastPane(self)
	}
}

// leaveFocus hands the focus back to the pane the user came from, without
// choosing anything. The counterpart to the focus key, for the times you looked
// and did not want to go anywhere.
func leaveFocus() tea.Cmd {
	return func() tea.Msg {
		restoreFocus()
		return nil
	}
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
		switch {
		case paneWidth > maxPanelWidth(winWidth):
			// Opened wider than the ceiling: a width remembered from a roomier
			// window, or one saved before the ceiling existed. Correct it now
			// rather than leaving the work pane squeezed until something else
			// happens to trigger a re-layout.
			m.targetWidth = maxPanelWidth(winWidth)
			return m, tea.Batch(
				restorePanelWidth(m.targetWidth),
				rememberPanelWidth(m.targetWidth),
			)
		case paneWidth >= notifyMinWidth:
			m.targetWidth = paneWidth
		}
		return m, nil

	case winWidth < m.minWidth():
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
		if paneWidth > maxPanelWidth(winWidth) {
			// Not a drag anyone meant. "Same window, different pane" is also
			// what a pane appearing or dying beside the panel looks like, and
			// adopting that once is enough to make it permanent — the width is
			// saved to the session and then actively held. Measured: the panel
			// reached 188 of 237 columns and stayed there, leaving 48 to work
			// in. Put it back rather than merely declining to remember it, or
			// the squeeze lasts until the next re-layout.
			if m.targetWidth > 0 {
				return m, restorePanelWidth(m.targetWidth)
			}
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

// maxPanelWidth is the widest the panel may be held at: half its window.
//
// The ceiling is the counterpart to notifyMinWidth, and it exists for the same
// reason — a width the panel arrives at by accident must not become the width it
// enforces. The lower clamp stops a transient squeeze from sticking; this one
// stops the opposite, which is worse: the panel is a sidebar, and past half the
// window the pane you actually work in is the one being squeezed.
//
// Half rather than a fixed number of columns because the only thing that matters
// is what is left over, and that scales with the window.
func maxPanelWidth(winWidth int) int { return winWidth / 2 }

// rememberPanelWidth records the pane's width, so reopening the panel brings
// back the size the user dragged it to.
//
// Both halves, because they answer different questions. The tmux option is the
// live one and is per session, so two sessions on one server can differ — but it
// dies with the server, and a width the user dragged once should not have to be
// dragged again after a reboot. The disk copy is that: one width, the last one
// chosen, seeding every panel that has nothing more specific to go on.
//
// Failure is deliberately silent on both: this is a convenience, and a session
// that cannot be resolved (the pane is going away, tmux is shutting down) is not
// worth a line in the event log.
func rememberPanelWidth(width int) tea.Cmd {
	return func() tea.Msg {
		if session, err := tmux.SessionForPane(selfPane()); err == nil {
			_ = tmux.SetPanelWidth(session, width)
		}
		_ = tmux.SavePanelWidth(width)
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
		// Hand focus back before leaving, when there is any to hand back. The
		// window we are about to leave would otherwise keep reporting the
		// panel's command and directory as the session's own. Best-effort:
		// there may be no previous pane, and that must not block the switch the
		// user actually asked for.
		restoreFocus()

		if err := tmux.SwitchClient(name); err != nil {
			return switchFailedMsg{session: name, err: err}
		}
		return nil
	}
}

// sessionAtRow maps a pane row to the session on it, empty when that row is not
// a session. The watch pane draws no border and starts at the top of the alt
// screen, so a row index is a line index.
func (m watchModel) sessionAtRow(y int) string {
	if y < 0 || m.width < notifyMinWidth || m.err != nil {
		return ""
	}
	lines := m.sessionLines()
	if y >= len(lines) {
		return ""
	}
	return lines[y].session
}

// sessionLines is the panel's rows: the selected session's header, the session
// list, then the event log.
//
// The height goes in because the header rations itself against it — the rows it
// takes come off the bottom of the event log, and fixedBox does that clipping
// silently.
func (m watchModel) sessionLines() []notifyLine {
	return notifyLines(m.sessions, m.events, m.width, m.height, m.selected, m.ownSession, m.showHeader)
}

func (m watchModel) View() string {
	if m.width == 0 {
		return ""
	}
	return fixedBox(m.body(), m.width, m.height)
}

// body returns the panel's content before it is clipped to size. No
// border: the tmux pane already draws one.
func (m watchModel) body() string {
	if m.width < notifyMinWidth {
		// Too narrow for the columns to mean anything. Say so rather than
		// rendering a shredded panel.
		return helpStyle.Render(fitCells("좁음", m.width))
	}
	if m.err != nil {
		return errorStyle.Render(fitCells(m.err.Error(), m.width))
	}

	lines := m.sessionLines()
	if len(lines) == 0 {
		return helpStyle.Render(fitCells(" AI 세션 없음", m.width))
	}
	return strings.Join(notifyTexts(lines), "\n")
}
