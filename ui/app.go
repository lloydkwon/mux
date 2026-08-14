// Package ui implements the Bubble Tea TUI for browsing and managing tmux sessions.
package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// Layout
	listWidthPercent = 2 // numerator of 5 (40%)
	listWidthDenom   = 5 // denominator
	minPanelHeight   = 5

	// Timing
	refreshInterval = 500 * time.Millisecond

	// doubleClickWindow is how long a second press on the same row still counts
	// as one gesture. A single press only moves the cursor, so this is the gap in
	// which the user can commit without reaching for the keyboard.
	doubleClickWindow = 400 * time.Millisecond

	// Display limits
	maxSessionNameDisplay = 18
	maxPathDisplay        = 35
	filterCharLimit       = 50
	filterInputWidth      = 30
)

type mode int

const (
	modeList mode = iota
	modeCreate
	modeRename
	modeFilter
	modeConfirmKill
	modeOrder
	modeHelp
)

// clickMark remembers the last left press, so the next one can be told apart
// from a double click.
type clickMark struct {
	item int
	at   time.Time
}

// Model is the top-level Bubble Tea model for the session manager TUI.
type Model struct {
	sessions        []tmux.Session
	filtered        []tmux.Session
	items           []listItem // flattened tree of (sessions, windows, panes)
	tree            treeState
	cursor          int
	mode            mode
	width           int
	height          int
	err             error
	createModel     createModel
	renameModel     renameModel
	filterMod       filterModel
	confirmKillMod  confirmKillModel
	orderModel      orderModel
	filterText      string
	prefs           preferences
	attachTarget    previewKey       // set when we want to attach after quitting (zero value = no attach)
	detachRequested bool             // set when New shell is selected from inside tmux
	lastClick       clickMark        // for telling a double click from two clicks
	focusSession    string           // session name to focus cursor on after next load
	previewContent  string           // cached capture-pane output
	previewKey      previewKey       // (session, window, pane) the cache belongs to
	tokenUsage      *tmux.TokenUsage // cached token usage for current AI session
	tokenSession    string           // session name the token cache belongs to
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type sessionsLoadedMsg struct {
	sessions []tmux.Session
	err      error
}

func loadSessions() tea.Msg {
	sessions, err := tmux.ListSessions()
	return sessionsLoadedMsg{sessions: sessions, err: err}
}

type previewLoadedMsg struct {
	key     previewKey
	content string
}

type tokenUsageLoadedMsg struct {
	sessionName string
	usage       *tmux.TokenUsage
}

type windowsLoadedMsg struct {
	sessionName string
	windows     []tmux.Window
}

type panesLoadedMsg struct {
	sessionName string
	windowIndex int
	panes       []tmux.Pane
}

func loadWindows(sessionName string) tea.Cmd {
	return func() tea.Msg {
		windows, _ := tmux.ListWindows(sessionName)
		return windowsLoadedMsg{sessionName: sessionName, windows: windows}
	}
}

func loadPanes(sessionName string, windowIndex int) tea.Cmd {
	return func() tea.Msg {
		panes, _ := tmux.ListPanes(sessionName, windowIndex)
		return panesLoadedMsg{sessionName: sessionName, windowIndex: windowIndex, panes: panes}
	}
}

func refreshPreview(key previewKey) tea.Cmd {
	return func() tea.Msg {
		content, err := tmux.CapturePaneTarget(key.target())
		if err != nil {
			content = "Error: " + err.Error()
		}
		return previewLoadedMsg{key: key, content: content}
	}
}

func loadTokenUsage(sessionName string, panePID int) tea.Cmd {
	return func() tea.Msg {
		sessionID, cwd, err := tmux.FindClaudeSession(panePID)
		if err != nil {
			return tokenUsageLoadedMsg{sessionName: sessionName}
		}
		usage, _ := tmux.LoadTokenUsage(sessionID, cwd)
		return tokenUsageLoadedMsg{sessionName: sessionName, usage: usage}
	}
}

// NewModel returns a new Model with default settings.
func NewModel() Model {
	prefs, err := loadPreferences()
	return Model{tree: newTreeState(), prefs: prefs, err: err}
}

// NewSessionModel returns a Model that opens straight on the create prompt and
// attaches to what it makes.
//
// The same screen `n` opens from the list, reached without going through the
// list first — which is what lets the panel offer "새 세션" without building a
// second create flow of its own. `esc` still drops to the list, exactly as
// cancelling `n` does.
func NewSessionModel() Model {
	m := NewModel()
	m.mode = modeCreate
	m.createModel = newCreateModel(true)
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadSessions, tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{loadSessions, tick()}
		if it := m.currentItem(); it != nil && it.session != nil {
			cmds = append(cmds, refreshPreview(previewKeyForItem(*it)))
			// Cost tracking reads Claude's own JSONL logs, so it only pays off
			// for Claude — any other tool would scan for a file that is not there.
			if tool, ok := tmux.SessionAITool(*it.session); ok && tool.Name == "claude" {
				cmds = append(cmds, loadTokenUsage(it.session.Name, it.session.PanePID))
			}
		}
		// Refresh windows/panes for expanded subtrees
		for name := range m.tree.expandedSession {
			cmds = append(cmds, loadWindows(name))
		}
		for sessionName, windows := range m.tree.expandedWindow {
			for windowIdx := range windows {
				cmds = append(cmds, loadPanes(sessionName, windowIdx))
			}
		}
		return m, tea.Batch(cmds...)

	case sessionsLoadedMsg:
		m.err = msg.err
		if msg.err == nil {
			m.sessions = msg.sessions
			m.tree.pruneCaches(m.sessions)
			m.applyFilter()
			if m.focusSession != "" {
				for i, it := range m.items {
					if it.kind == itemSession && it.session.Name == m.focusSession {
						m.cursor = i
						break
					}
				}
				m.focusSession = ""
			}
		}
		return m, nil

	case windowsLoadedMsg:
		m.tree.windowsCache[msg.sessionName] = msg.windows
		m.rebuildItems()
		return m, nil

	case panesLoadedMsg:
		m.tree.panesCache[paneCacheKey{session: msg.sessionName, window: msg.windowIndex}] = msg.panes
		m.rebuildItems()
		return m, nil

	case previewLoadedMsg:
		m.previewKey = msg.key
		m.previewContent = msg.content
		return m, nil

	case tokenUsageLoadedMsg:
		m.tokenSession = msg.sessionName
		m.tokenUsage = msg.usage
		return m, nil

	case sessionCreatedMsg:
		m.mode = modeList
		if msg.attach {
			m.attachTarget = previewKey{session: msg.name, window: -1, pane: -1}
			return m, tea.Quit
		}
		m.focusSession = msg.name
		return m, loadSessions

	case sessionRenamedMsg:
		m.mode = modeList
		if order, ok := m.prefs.Orders[msg.oldName]; ok {
			delete(m.prefs.Orders, msg.oldName)
			m.prefs.Orders[msg.newName] = order
			if err := savePreferences(m.prefs); err != nil {
				m.err = err
			}
		}
		m.focusSession = msg.newName
		return m, loadSessions

	case filterAppliedMsg:
		m.mode = modeList
		m.filterText = msg.text
		m.applyFilter()
		return m, nil

	case sessionKilledMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.mode = modeList
		if msg.name != "" {
			if _, ok := m.prefs.Orders[msg.name]; ok {
				delete(m.prefs.Orders, msg.name)
				if err := savePreferences(m.prefs); err != nil {
					m.err = err
				}
			}
			return m, loadSessions
		}
		return m, nil

	case sessionOrderMsg:
		m.mode = modeList
		if msg.order == 0 {
			delete(m.prefs.Orders, msg.sessionName)
		} else {
			m.prefs.Orders[msg.sessionName] = msg.order
		}
		if err := savePreferences(m.prefs); err != nil {
			m.err = err
		}
		m.applyFilter()
		m.focusItemSession(msg.sessionName)
		return m, nil
	}

	switch m.mode {
	case modeCreate:
		return m.updateCreate(msg)
	case modeRename:
		return m.updateRename(msg)
	case modeFilter:
		return m.updateFilter(msg)
	case modeConfirmKill:
		return m.updateConfirmKill(msg)
	case modeOrder:
		return m.updateOrder(msg)
	case modeHelp:
		// Any key closes, like confirmKillModel's default cancel. Guarding on
		// KeyMsg keeps the 500ms tick from dismissing the page on its own.
		if _, ok := msg.(tea.KeyMsg); ok {
			m.mode = modeList
		}
		return m, nil
	default:
		return m.updateList(msg)
	}
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			return m.moveCursor(-1)
		case "down", "j":
			return m.moveCursor(1)
		case "g":
			m.cursor = 0
			return m, m.refreshCurrentPreview()
		case "G":
			if len(m.items) > 0 {
				m.cursor = len(m.items) - 1
				return m, m.refreshCurrentPreview()
			}

		case "tab", "right", "l":
			return m.expandCurrent()

		case "shift+tab", "left", "h":
			return m.collapseCurrent()

		case "enter":
			return m.activateCurrent()

		case "n":
			m.mode = modeCreate
			m.createModel = newCreateModel(false)
			return m, m.createModel.nameInput.Focus()

		case "o":
			selected := m.currentSessionName()
			m.prefs = m.prefs.nextSort()
			if err := savePreferences(m.prefs); err != nil {
				m.err = err
			}
			m.applyFilter()
			m.focusItemSession(selected)
			return m, m.refreshCurrentPreview()

		case "x":
			if it := m.currentItem(); it != nil && it.kind == itemSession {
				m.mode = modeConfirmKill
				m.confirmKillMod = newConfirmKillModel(it.session.Name)
			}

		case "r":
			if it := m.currentItem(); it != nil && it.kind == itemSession {
				m.mode = modeRename
				m.renameModel = newRenameModel(it.session.Name)
				return m, m.renameModel.input.Focus()
			}

		case "/":
			m.mode = modeFilter
			m.filterMod = newFilterModel(m.filterText)
			return m, nil

		case "?":
			m.mode = modeHelp
			return m, nil

		case "esc":
			if m.filterText != "" {
				m.filterText = ""
				m.applyFilter()
			}

		default:
			if len(msg.String()) == 1 && msg.String()[0] >= '0' && msg.String()[0] <= '9' {
				if it := m.currentItem(); it != nil && it.kind == itemSession {
					m.mode = modeOrder
					m.orderModel = newOrderModel(it.session.Name, msg.String())
					return m, m.orderModel.input.Focus()
				}
			}
		}
	}
	return m, nil
}

// activateCurrent commits the row under the cursor: attach for a session,
// window or pane, and the two action rows do what they say.
//
// Shared by `enter` and the double click so the two cannot drift — a click that
// attached to a different target than the key would be a bug nobody would think
// to look for.
func (m Model) activateCurrent() (tea.Model, tea.Cmd) {
	it := m.currentItem()
	if it == nil {
		return m, nil
	}
	switch it.kind {
	case itemNewShell:
		m.detachRequested = os.Getenv("TMUX") != ""
		return m, tea.Quit
	case itemNewSession:
		m.mode = modeCreate
		m.createModel = newCreateModel(true)
		return m, m.createModel.nameInput.Focus()
	default:
		m.attachTarget = previewKeyForItem(*it)
		return m, tea.Quit
	}
}

// handleMouse turns a press into a cursor move, or into an activation when it is
// the second press on the same row.
//
// A single click only moves the cursor, unlike the watch panel where a click
// switches outright: that panel shows nothing to read first, while here the
// right column is exactly that. Clicking to read and clicking to leave must not
// be the same gesture.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.moveCursor(-1)
	case tea.MouseButtonWheelDown:
		return m.moveCursor(1)
	case tea.MouseButtonLeft:
		// Press only. A release carries the same button under SGR, and a drag
		// arrives as motion — neither is a click.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
	default:
		return m, nil
	}

	// Correct for the chrome above the columns and the column's own border, then
	// for however far the list has scrolled.
	row := msg.Y - m.chromeTop() - 1
	idx := row + listOffset(m.cursor, m.panelHeight()-2)
	// x==0 is the frame and x==listInnerWidth+1 the divider; neither is a row.
	if msg.X < 1 || msg.X > m.listInnerWidth() || row < 0 || idx < 0 || idx >= len(m.items) {
		return m, nil
	}

	if m.lastClick.item == idx && time.Since(m.lastClick.at) <= doubleClickWindow {
		m.cursor = idx
		return m.activateCurrent()
	}
	m.lastClick = clickMark{item: idx, at: time.Now()}
	if idx == m.cursor {
		return m, nil
	}
	m.cursor = idx
	return m, m.refreshCurrentPreview()
}

// moveCursor steps the cursor delta rows, stopping at either end.
func (m Model) moveCursor(delta int) (tea.Model, tea.Cmd) {
	if len(m.items) == 0 {
		return m, nil
	}
	next := clampInt(m.cursor+delta, 0, len(m.items)-1)
	if next == m.cursor {
		return m, nil
	}
	m.cursor = next
	return m, m.refreshCurrentPreview()
}

// expandCurrent expands the row under the cursor and dispatches the loader.
// On a pane (leaf) it does nothing.
func (m Model) expandCurrent() (tea.Model, tea.Cmd) {
	it := m.currentItem()
	if it == nil || !it.canExpand() {
		return m, nil
	}
	switch it.kind {
	case itemSession:
		if m.tree.isSessionExpanded(it.session.Name) {
			return m, nil
		}
		m.tree.setSessionExpanded(it.session.Name, true)
		m.rebuildItems()
		return m, loadWindows(it.session.Name)
	case itemWindow:
		if m.tree.isWindowExpanded(it.session.Name, it.window.Index) {
			return m, nil
		}
		m.tree.setWindowExpanded(it.session.Name, it.window.Index, true)
		m.rebuildItems()
		return m, loadPanes(it.session.Name, it.window.Index)
	}
	return m, nil
}

// collapseCurrent collapses the row under the cursor. On a child row whose own
// kind cannot collapse further, it walks up to the parent and collapses that.
func (m Model) collapseCurrent() (tea.Model, tea.Cmd) {
	it := m.currentItem()
	if it == nil {
		return m, nil
	}
	switch it.kind {
	case itemSession:
		if !m.tree.isSessionExpanded(it.session.Name) {
			return m, nil
		}
		m.tree.setSessionExpanded(it.session.Name, false)
	case itemWindow:
		if m.tree.isWindowExpanded(it.session.Name, it.window.Index) {
			m.tree.setWindowExpanded(it.session.Name, it.window.Index, false)
		} else {
			// Already-collapsed window: jump up to the parent session
			m.cursor = m.findItemIndex(itemSession, it.session.Name, 0, 0)
			m.tree.setSessionExpanded(it.session.Name, false)
		}
	case itemPane:
		// Collapse the parent window and move cursor up to it
		m.cursor = m.findItemIndex(itemWindow, it.session.Name, it.window.Index, 0)
		m.tree.setWindowExpanded(it.session.Name, it.window.Index, false)
	}
	m.rebuildItems()
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
	return m, m.refreshCurrentPreview()
}

// refreshCurrentPreview returns a tea.Cmd to capture the pane targeted by the
// current cursor position. Returns nil when there is no current item.
func (m *Model) refreshCurrentPreview() tea.Cmd {
	if it := m.currentItem(); it != nil && it.session != nil {
		return refreshPreview(previewKeyForItem(*it))
	}
	return nil
}

// findItemIndex returns the index of the matching listItem, or -1 if not found.
func (m *Model) findItemIndex(kind itemKind, sessionName string, windowIdx, paneIdx int) int {
	for i, it := range m.items {
		if it.kind != kind || it.session == nil || it.session.Name != sessionName {
			continue
		}
		switch kind {
		case itemSession:
			return i
		case itemWindow:
			if it.window.Index == windowIdx {
				return i
			}
		case itemPane:
			if it.window.Index == windowIdx && it.pane.Index == paneIdx {
				return i
			}
		}
	}
	return -1
}

func (m Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.mode = modeList
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.createModel, cmd = m.createModel.Update(msg)
	return m, cmd
}

func (m Model) updateRename(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			m.mode = modeList
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.renameModel, cmd = m.renameModel.Update(msg)
	return m, cmd
}

func (m Model) updateFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.filterMod, cmd = m.filterMod.Update(msg)
	// Live filter as you type
	m.filterText = m.filterMod.LiveText()
	m.applyFilter()
	return m, cmd
}

func (m Model) updateConfirmKill(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.confirmKillMod, cmd = m.confirmKillMod.Update(msg)
	return m, cmd
}

func (m Model) updateOrder(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.orderModel, cmd = m.orderModel.Update(msg)
	return m, cmd
}

func (m *Model) currentItem() *listItem {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		return &m.items[m.cursor]
	}
	return nil
}

// currentSession returns the parent session of the current row (the row itself
// for session rows). Returns nil if no row is selected.
func (m *Model) currentSession() *tmux.Session {
	if it := m.currentItem(); it != nil && it.session != nil {
		return it.session
	}
	return nil
}

func (m *Model) currentSessionName() string {
	if s := m.currentSession(); s != nil {
		return s.Name
	}
	return ""
}

// rebuildItems recomputes the flattened tree view from the filtered session
// list and current expansion state. Call after sessions, filter, or expansion
// state changes.
func (m *Model) rebuildItems() {
	m.items = flattenMenu(m.filtered, &m.tree, m.prefs.Orders, m.filterText == "")
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
}

func (m *Model) applyFilter() {
	sorted := sortedSessions(m.sessions, m.prefs)
	if m.filterText == "" {
		m.filtered = sorted
	} else {
		lower := strings.ToLower(m.filterText)
		m.filtered = nil
		for _, s := range sorted {
			if strings.Contains(strings.ToLower(s.Name), lower) ||
				strings.Contains(strings.ToLower(s.Directory), lower) {
				m.filtered = append(m.filtered, s)
			}
		}
	}
	m.rebuildItems()
}

func (m *Model) focusItemSession(name string) {
	if name == "" {
		return
	}
	for i, it := range m.items {
		if it.kind == itemSession && it.session != nil && it.session.Name == name {
			m.cursor = i
			return
		}
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	switch m.mode {
	case modeCreate:
		return m.viewWithOverlay(m.createModel.View())
	case modeRename:
		return m.viewWithOverlay(m.renameModel.View())
	case modeOrder:
		return m.viewWithOverlay(m.orderModel.View())
	case modeHelp:
		return m.viewHelp()
	default:
		return m.viewMain()
	}
}

// extraBar is the line between the title and the columns: the filter prompt, the
// kill confirmation, the active filter, or an error. Empty when there is none.
func (m Model) extraBar() string {
	switch {
	case m.mode == modeFilter:
		return m.filterMod.View()
	case m.mode == modeConfirmKill:
		return m.confirmKillMod.View()
	case m.filterText != "":
		return helpStyle.Render(fmt.Sprintf("filter: %s (esc clear)", m.filterText))
	case m.err != nil:
		return errorStyle.Render(m.err.Error())
	}
	return ""
}

// chromeTop is how many rows sit above the columns — the offset a mouse row has
// to be corrected by.
func (m Model) chromeTop() int {
	if m.extraBar() != "" {
		return 2
	}
	return 1
}

// panelHeight is how many rows each column gets, borders included.
func (m Model) panelHeight() int {
	// title(1+margin1) + help(1), plus the filter/error bar when there is one.
	chrome := 3
	if m.extraBar() != "" {
		chrome++
	}
	if h := m.height - chrome; h > minPanelHeight {
		return h
	}
	return minPanelHeight
}

// listWidth is the left column including the frame characters on either side of
// it — what a mouse column has to be compared against.
func (m Model) listWidth() int {
	return m.width * listWidthPercent / listWidthDenom
}

// listInnerWidth is the drawable width of the left column, inside the frame.
func (m Model) listInnerWidth() int {
	return m.listWidth() - 2
}

// titleBar is the heading: what you are looking at on the left, how it is
// ordered on the right.
//
// The sort was in brackets beside the count, where it competed with the title
// for the same glance. Flush right and dim, it is there when looked for and out
// of the way when not.
func (m Model) titleBar() string {
	// Sessions only — windows and panes are not what the number is about.
	left := fmt.Sprintf("⚡ tmux sessions %d", len(m.filtered))
	right := fmt.Sprintf("sort: %s", m.prefs.Sort)

	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right) - 1
	if gap < 1 {
		return titleStyle.Render(fitCells(left, m.width))
	}
	return titleStyle.Render(left) + strings.Repeat(" ", gap) + helpStyle.Render(right)
}

func (m Model) viewMain() string {
	title := m.titleBar()

	// Help bar
	help := renderHelp()

	extraBar := m.extraBar()

	// One frame around both columns: three vertical characters in total, so the
	// two inner widths and those three are the whole terminal.
	innerHeight := m.panelHeight() - 2
	leftWidth := m.listInnerWidth()
	rightWidth := m.width - leftWidth - 3

	list := renderListView(m.items, m.cursor, m.filterText, &m.tree, leftWidth, innerHeight)

	currentItem := m.currentItem()
	currentSession := m.currentSession()
	cachedContent := ""
	if currentItem != nil && m.previewKey == previewKeyForItem(*currentItem) {
		cachedContent = m.previewContent
	}
	var tokenUsage *tmux.TokenUsage
	if currentSession != nil && m.tokenSession == currentSession.Name {
		tokenUsage = m.tokenUsage
	}
	preview := renderPreview(currentItem, cachedContent, rightWidth, innerHeight, tokenUsage)

	content := drawFrame(list, preview, leftWidth, rightWidth, innerHeight)

	// Assemble
	var b strings.Builder
	b.WriteString(title)
	b.WriteByte('\n')
	if extraBar != "" {
		b.WriteString(extraBar)
		b.WriteByte('\n')
	}
	b.WriteString(content)
	b.WriteByte('\n')
	b.WriteString(help)

	return b.String()
}

func (m Model) viewWithOverlay(overlay string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2).
		Render(overlay)

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		box)
}

func renderHelp() string {
	keys := []struct{ key, desc string }{
		{"↑↓/jk", "navigate"},
		{"click", "select"},
		{"tab", "expand"},
		{"⇧tab", "collapse"},
		{"enter/2click", "attach"},
		{"n", "new"},
		{"x", "kill"},
		{"r", "rename"},
		{"0-9", "order"},
		{"o", "sort"},
		{"/", "filter"},
		{"?", "help"},
		{"q", "quit"},
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts,
			helpKeyStyle.Render(k.key)+" "+helpStyle.Render(k.desc))
	}
	// A tight separator, not the roomier "  •  ": the bar is clipped at the
	// terminal's width, and at 12 entries the wider one pushed `q quit` off a
	// 150-column screen.
	return strings.Join(parts, helpStyle.Render(" • "))
}

// AttachName returns the session name to attach to (if any) after the TUI
// exits. Returns empty when no attach was requested.
func (m Model) AttachName() string {
	return m.attachTarget.session
}

// AttachWindowIndex returns the window index selected for attachment, or -1
// if the user selected a session row.
func (m Model) AttachWindowIndex() int {
	return m.attachTarget.window
}

// AttachPaneIndex returns the pane index selected for attachment, or -1 if
// the user did not drill down to a pane row.
func (m Model) AttachPaneIndex() int {
	return m.attachTarget.pane
}

// DetachRequested reports whether New shell was selected while mux was
// running inside tmux. The caller should detach the current tmux client after
// the TUI restores the terminal; outside tmux, quitting mux already reveals
// the login shell and this remains false.
func (m Model) DetachRequested() bool {
	return m.detachRequested
}

// DetachClient detaches the current tmux client without killing its session.
func DetachClient() error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}
	return runTmux(tmuxPath, "detach-client")
}

// runTmux runs a tmux command and carries what it said into any error.
//
// These calls bypass the tmux package's runner on purpose — they must reach the
// real process — so they need their own copy of what runner.go does with
// stderr. Without it an attach that tmux refused for a reason it was happy to
// state arrives as "failed to attach: exit status 1".
func runTmux(tmuxPath string, args ...string) error {
	cmd := exec.Command(tmuxPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// AttachToSession switches to the target session, optionally focusing a
// specific window and pane first. Pass windowIdx == -1 to keep the active
// window; pass paneIdx == -1 to keep the active pane within that window.
//
// If already inside tmux, uses switch-client. Otherwise, uses attach-session.
func AttachToSession(name string, windowIdx, paneIdx int) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	// Focus the requested window/pane *before* attaching, since attach-session
	// replaces our process and we can't run anything afterwards.
	if windowIdx >= 0 {
		windowTarget := fmt.Sprintf("%s:%d", name, windowIdx)
		if err := runTmux(tmuxPath, "select-window", "-t", windowTarget); err != nil {
			return fmt.Errorf("select-window %s: %w", windowTarget, err)
		}
		if paneIdx >= 0 {
			paneTarget := fmt.Sprintf("%s.%d", windowTarget, paneIdx)
			if err := runTmux(tmuxPath, "select-pane", "-t", paneTarget); err != nil {
				return fmt.Errorf("select-pane %s: %w", paneTarget, err)
			}
		}
	}

	// "=" forces an exact match. Plain tmux target matching falls back to
	// prefix and then glob, so "mux" would be ambiguous next to "mux-old".
	if os.Getenv("TMUX") != "" {
		return runTmux(tmuxPath, "switch-client", "-t", "="+name)
	}
	return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", "=" + name}, os.Environ())
}
