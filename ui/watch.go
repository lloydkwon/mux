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

	// selected is the session the detail column is showing, held by name rather
	// than by row index: rows are ordered by how long a state has held, so an
	// index points at a different session two seconds later.
	selected string

	// ownSession is the session this pane lives in — the one already on screen
	// beside the panel. Previewing it would draw a copy of the pane next to it,
	// so the detail column shows a summary instead and the auto-selection looks
	// past it.
	//
	// Empty means "could not tell", and everything below then behaves as if the
	// panel had no idea, which is what it used to do. Resolved once at startup:
	// a pane can be moved between sessions, but being wrong there costs one
	// mirrored frame, not correctness.
	ownSession string

	// preview is the captured output for previewKey's session, kept apart so a
	// capture that arrives after the selection moved can be discarded rather
	// than shown under the wrong name.
	preview    string
	previewKey previewKey

	// usage is the token cost of tokenSession, on the same discard rule. Only
	// loaded for the summary card, and only for a Claude session.
	usage        *tmux.TokenUsage
	tokenSession string

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
	// Resolved here rather than in a tea.Cmd: it is wanted by the very first
	// auto-selection, and asking for it asynchronously would let the panel pick
	// its own session once and then correct itself a frame later.
	own, err := tmux.SessionForPane(selfPane())
	if err != nil {
		own = "" // not knowing is a state; see watchModel.ownSession
	}

	_, err = tea.NewProgram(watchModel{ownSession: own},
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
		// arrives as motion — neither should move the selection.
		if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
			return m, nil
		}
		name := m.sessionAt(msg.X, msg.Y)
		if name == "" {
			return m, nil
		}
		// One click to look, a second to go. Without a detail column there is
		// nothing to look at, so the first click is the decision.
		if m.listColumnWidth() == 0 || name == m.selected {
			return m, switchToSession(name)
		}
		next, cmd := m.selectSession(name)
		// tmux made this pane active to deliver the click. Hand focus back even
		// though we are not leaving: while the panel is the active pane, its
		// window reports the panel's directory as the session's own.
		return next, tea.Batch(restoreFocusCmd(), cmd)

	case previewLoadedMsg:
		// Same guard the TUI uses: a capture that lands after the selection
		// moved would be drawn under the wrong session's name.
		if msg.key.session == m.selected {
			m.preview, m.previewKey = msg.content, msg.key
		}
		return m, nil

	case tokenUsageLoadedMsg:
		if msg.sessionName == m.selected {
			m.usage, m.tokenSession = msg.usage, msg.sessionName
		}
		return m, nil

	case switchFailedMsg:
		// Surface it in the log rather than failing silently: the session can
		// vanish between the render and the click.
		m.events = pushEvents(m.events, []aiEvent{{
			at: time.Now(), session: msg.session, text: "⚠ 전환 실패: " + msg.err.Error(),
		}})
		return m, nil

	case watchTickMsg:
		return m, tea.Batch(loadSessions, watchTick(), m.contentCmd())

	case sessionsLoadedMsg:
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		// Same order as the TUI: diff before the slice is replaced. This process
		// keeps its own history — it cannot share the TUI's, and a panel that
		// only logs while mux is open would defeat the point.
		fresh, states := detectTransitions(m.prevAIStates, msg.sessions, time.Now())
		m.prevAIStates = states
		m.events = pushEvents(m.events, fresh)
		m.sessions = msg.sessions
		return m.reselect()
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
	next, cmd := m.selectSession(order[at])
	return next, cmd
}

// selectSession points the detail column at name and asks for its content.
func (m watchModel) selectSession(name string) (watchModel, tea.Cmd) {
	if name == "" || name == m.selected {
		return m, nil
	}
	m.selected = name
	// Drop the outgoing session's content rather than leaving it under the new
	// name until the next load lands.
	m.preview, m.previewKey = "", previewKey{}
	m.usage, m.tokenSession = nil, ""
	return m, m.contentCmd()
}

// reselect keeps the selection pointing at a session that still exists, and
// picks a row when there is nothing selected yet.
func (m watchModel) reselect() (watchModel, tea.Cmd) {
	for _, s := range m.sessions {
		if s.Name == m.selected {
			return m, nil
		}
	}
	m.selected = m.autoSelect()
	m.preview, m.previewKey = "", previewKey{}
	m.usage, m.tokenSession = nil, ""
	if m.selected == "" {
		return m, nil
	}
	// Ask for the content now rather than waiting out a whole tick: the first
	// frame after startup would otherwise show a named session with a blank
	// column beside it.
	return m, m.contentCmd()
}

// autoSelect picks the row to land on when nothing is selected: the top of the
// list, but looking past this pane's own session.
//
// Left alone it would land there nearly every time — the list is ordered by how
// recently a state changed, and the session you are working in is the one whose
// state keeps changing. Opening the panel would then spend the detail column
// mirroring the pane beside it. Choosing a session is still allowed to land on
// it; this is only what happens when nobody chose.
func (m watchModel) autoSelect() string {
	order := m.sessionOrder()
	for _, name := range order {
		if name != m.ownSession {
			return name
		}
	}
	// Nothing else to show — one session, and it is this one. The summary card
	// is what keeps that from being a mirror.
	if len(order) > 0 {
		return order[0]
	}
	return ""
}

// showsSelfCard reports whether the detail column will summarise the selection
// instead of mirroring it.
//
// One decider for both halves: the renderer asks it to choose the card, and the
// loader asks it to choose what to fetch. Two copies of this test would drift
// into capturing output nothing draws, or drawing a card with no cost on it.
func (m watchModel) showsSelfCard() bool {
	return m.selected != "" && m.selected == m.ownSession
}

// contentCmd loads whatever the detail column needs for the selected session:
// its output, or its token cost when the column is showing the summary card.
func (m watchModel) contentCmd() tea.Cmd {
	if m.selected == "" || m.listColumnWidth() == 0 {
		return nil
	}
	if m.showsSelfCard() {
		return m.tokenCmd()
	}
	return refreshPreview(previewKey{session: m.selected, window: -1, pane: -1})
}

// tokenCmd loads the cost line of the summary card. Gated on Claude the same
// way the TUI gates it: any other tool would scan for a file that is not there.
func (m watchModel) tokenCmd() tea.Cmd {
	for _, s := range m.sessions {
		if s.Name != m.selected {
			continue
		}
		if tool, ok := tmux.SessionAITool(s); !ok || tool.Name != "claude" {
			return nil
		}
		return loadTokenUsage(s.Name, s.PanePID)
	}
	return nil
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

// restoreFocusCmd is restoreFocus as a tea.Cmd, for a click that selects rather
// than switches — switchToSession does it on its own way out.
func restoreFocusCmd() tea.Cmd {
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

// watchTwoColumnMinWidth is the narrowest pane worth splitting. Below it the
// list gets the whole pane, which is what the panel has always looked like.
//
// The split gives the list a readable 30 at minimum and leaves the detail column
// enough to show a prompt and its options without wrapping every line.
const watchTwoColumnMinWidth = 76

// listColumnWidth is the session column's width, or 0 when the pane is too
// narrow to split.
//
// Derived from the pane width rather than stored, so a click and the frame it
// was aimed at can never disagree about where the columns are.
func (m watchModel) listColumnWidth() int {
	if m.width < watchTwoColumnMinWidth {
		return 0
	}
	return clampInt(m.width*2/5, 30, 40)
}

// sessionAt maps a click to the session under it, empty when that point is not
// on a session row. The watch pane draws no border and starts at the top of the
// alt screen, so a row index is a line index.
//
// A click in the detail column selects nothing: it is a readout, and there is
// nothing there to aim at.
func (m watchModel) sessionAt(x, y int) string {
	if y < 0 || m.width < notifyMinWidth || m.err != nil {
		return ""
	}
	if listW := m.listColumnWidth(); listW > 0 && x >= listW {
		return ""
	}
	lines := m.sessionLines()
	if y >= len(lines) {
		return ""
	}
	return lines[y].session
}

// sessionLines is the session column's rows, in the layout the current pane
// width produces.
func (m watchModel) sessionLines() []notifyLine {
	if listW := m.listColumnWidth(); listW > 0 {
		return notifySessionLines(m.sessions, listW, m.selected, m.ownSession)
	}
	return notifyLines(m.sessions, m.events, m.width, m.selected, m.ownSession)
}

func (m watchModel) View() string {
	if m.width == 0 {
		return ""
	}
	if listW := m.listColumnWidth(); listW > 0 && m.err == nil {
		return m.twoColumnView(listW)
	}
	return fixedBox(m.body(), m.width, m.height)
}

// twoColumnView puts the session list beside the selected session's detail.
//
// joinHorizontalFixed concatenates line by line, so both sides have to be
// exactly their own width and exactly m.height tall or the columns shear.
func (m watchModel) twoColumnView(listW int) string {
	lines := notifySessionLines(m.sessions, listW, m.selected, m.ownSession)
	left := ""
	if len(lines) == 0 {
		left = helpStyle.Render(fitCells(" 세션 없음", listW))
	} else {
		left = strings.Join(notifyTexts(lines), "\n")
	}

	right := detailView{
		session:  m.selectedSession(),
		captured: m.previewFor(m.selected),
		events:   m.events,
		own:      m.showsSelfCard(),
		usage:    m.usageFor(m.selected),
	}.render(m.width-listW, m.height)

	return joinHorizontalFixed(fixedBox(left, listW, m.height), right)
}

// selectedSession returns the selected session, or nil when nothing is selected
// or the selection has since vanished.
func (m watchModel) selectedSession() *tmux.Session {
	for i := range m.sessions {
		if m.sessions[i].Name == m.selected {
			return &m.sessions[i]
		}
	}
	return nil
}

// previewFor returns the captured output only when it belongs to the session
// asked about. A capture that arrives after the selection moved would otherwise
// be drawn under the new session's name.
func (m watchModel) previewFor(session string) string {
	if session == "" || m.previewKey.session != session {
		return ""
	}
	return m.preview
}

// usageFor is previewFor's rule for the token cost.
func (m watchModel) usageFor(session string) *tmux.TokenUsage {
	if session == "" || m.tokenSession != session {
		return nil
	}
	return m.usage
}

// body returns the single-column content before it is clipped to size. No
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

	lines := notifyLines(m.sessions, m.events, m.width, m.selected, m.ownSession)
	if len(lines) == 0 {
		return helpStyle.Render(fitCells(" AI 세션 없음", m.width))
	}
	return strings.Join(notifyTexts(lines), "\n")
}
