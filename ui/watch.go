package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// narrowSamplesBeforeLeaving is how many consecutive readings below the minimum
// window width it takes for the panel to quit.
//
// Two, not one. A session switch re-lays-out every window tmux touches and a
// window can report a width it holds for an instant; one reading was enough to
// close panels during an ordinary switch. The cost is asymmetric — staying one
// tick too long in a narrow window is a cramped sidebar, while leaving wrongly
// takes the sidebar *and* the keyboard until the user finds prefix + a.
const narrowSamplesBeforeLeaving = 2

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
	prevAIStates  map[string]aiSnapshot
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
	//
	// Resolved at startup and **re-resolved when it stops naming a live session**
	// — a rename does exactly that, and the cost of being wrong is not the stale
	// marker this comment used to claim. A name nothing answers to takes the
	// cursor with it: ownSessionClients never matches, so ownClients sticks at
	// false and reanchorOnArrival can never fire again, and the moment the
	// selected session goes away autoSelect falls to the top row. Measured — a
	// session renamed after its panel started drew no ◀ at all and sat on someone
	// else's row until the process was restarted.
	ownSession string

	// resolveOwn asks tmux which session this pane is in now. nil disables the
	// re-resolve entirely, which is what every hand-built watchModel in the tests
	// gets — the same zero-value gate exePath, editorBin and ghostIdle use, and
	// what keeps `go test ./ui` off the developer's own server.
	resolveOwn func() (string, error)

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

	// narrowSamples counts consecutive readings below minWindowWidth. Leaving
	// costs the user their sidebar and their keyboard until they press
	// prefix + a, so it takes two readings; any wide one resets the count.
	narrowSamples int

	// winPanes is how many panes the window held at the last reading. A pane
	// appearing or dying beside this one changes the panel's width without the
	// window changing size — which is indistinguishable from a border drag if
	// the width is all you look at. Measured: closing a pane next to the panel
	// in a 237-column window handed it the freed columns, landing on exactly
	// 118 — half, which the ceiling below lets through — and that was then
	// saved to disk and actively held.
	winPanes int

	// exePath is the binary this process was started from, resolved once at
	// startup. Empty disables the whole check — which is what every test gets,
	// and what keeps `go test ./ui` off the filesystem and off tmux here.
	exePath string

	// exeStamp is what that path looked like when this process started, and
	// pendingExe is a reading that disagrees with it and is waiting to be seen
	// twice. A single disagreeing reading is not enough: `install` cannot write
	// over a running binary (measured: ETXTBSY, which is why `cp` fails there
	// and `install` unlinks first), so the path briefly names a file that is
	// still being written. Restarting into that is restarting into a truncated
	// binary.
	exeStamp   binaryStamp
	pendingExe binaryStamp

	// restartRequested is read by RunWatch after the program exits, the same way
	// the TUI hands back attachTarget: exec cannot run under Bubble Tea, which
	// holds the terminal in raw mode on the alternate screen.
	restartRequested bool

	// editorBin is the editor a right click opens a session's folder in, resolved
	// once at startup. Empty means there is none to run, which is the whole
	// switch — there is no option, because the gesture is the opt-in: a right
	// click is not something anyone presses by accident, which is exactly what an
	// option would have been protecting them from. (@mux_editor 는 그 스위치가 아니다 —
	// 무엇으로 열지만 고른다. ideaBin, editorChoice.)
	//
	// The zero value being off is what keeps every test that builds a watchModel
	// by hand from launching anything — the same reason exePath works that way.
	editorBin string

	// ideaBin 은 IntelliJ 의 런처로, 시작할 때 findIntelliJ 로 한 번 채운다. "" 이면
	// @mux_editor=idea 여도 띄울 것이 없다 — editorBin 과 같은 제로값 관문이다.
	ideaBin string

	// editorChoice 는 @mux_editor 를 읽는다(tmux.EditorChoice). 오른쪽 클릭마다 tea.Cmd
	// 안에서 부른다. nil 이면 묻지 않고 VS Code — 손으로 만든 watchModel 은 전부 nil
	// 이므로 테스트가 개발자의 서버에 묻지 않는다.
	editorChoice func() string

	// focusWindow raises the VS Code window already open on a folder and says
	// whether it did (focusShellWindow). nil is off — every hand-built
	// watchModel, so no test reaches the desktop — and with it off a raise
	// falls back to `code <folder>`, which is what WSL needs anyway.
	focusWindow func(dir string) focusResult

	// focusIdea raises the IntelliJ window open on a project folder
	// (focusShellIntelliJ), used when @mux_editor is idea. nil is off, like
	// focusWindow — and with it off nothing is raised, since IntelliJ has no
	// launcher that only raises.
	focusIdea func(dir string) focusResult

	// ghostIdle is how long a differently-sized client must sit idle before this
	// panel disconnects it, resolved once at startup like every other
	// preference here. Zero disables the check — which is what every test gets,
	// the same mechanism exePath and editorBin use.
	ghostIdle time.Duration

	// lastGhostCheck is when the question was last asked. The panel ticks every
	// two seconds; a terminal abandoned yesterday does not need answering that
	// often, and there is one panel per window asking.
	lastGhostCheck time.Time

	// probeMouse asks tmux whether its `mouse` option is on (watchmouse.go).
	// nil is off — every hand-built watchModel — so no test asks the
	// developer's server, the same zero-value gate resolveOwn uses.
	probeMouse func() bool

	// mouseOff is the last answer, and what puts the hint row above the list.
	// lastMouseCheck is when it was asked, on the ghost check's minute cadence.
	mouseOff       bool
	lastMouseCheck time.Time

	// ownClients is how many clients were in this pane's session as of the last
	// refresh. Held so an *arrival* can be seen: a level would undo the user's
	// own cursor two seconds after they moved it, an edge only fires when
	// someone comes. A count rather than a bool, because with a VS Code
	// terminal parked on the session the bool is true the whole time and a
	// Windows Terminal landing beside it was invisible — the cursor then stayed
	// wherever it was last left, pointing at some other session.
	ownClients int

	// fitted is whether mux fixed this window's size to hide the panel from an
	// editor's terminal (VS Code, IntelliJ), as of the last resize event — or
	// the last time adoptSavedWidth had something to change. The panel follows
	// the one width there too; what changes is the hand that corrects it: the
	// fit (refitCmd) rather than resize-pane, because a VS Code-only window's
	// width is derived from the panel's. See applyFittedResize. Read beside the
	// other I/O so the pure paths need no server.
	fitted bool

	// probeFitted asks tmux whether this pane's window is fitted, for the tick
	// that has a width to adopt. nil never asks — every hand-built watchModel —
	// the same zero-value gate resolveOwn uses, so `go test ./ui` stays off the
	// developer's server. RunWatch sets it.
	probeFitted func() bool

	// refitPending is a drag in a fitted window not yet followed by a refit.
	// The refit waits for the next tick rather than riding with the drag: a
	// mouse drag arrives as a run of resize events, and pinning the border on
	// each one pulled it back under the pointer (review, 2026-10-22).
	refitPending bool
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

	// Read once, outside the loop: these are startup facts, and the loop below
	// only turns again when an exec failed rather than because anything about
	// the pane changed.
	minWidth := tmux.MinWindowWidth()
	header := tmux.PanelHeaderEnabled()
	// Looked for unconditionally: not finding one stands the right click down,
	// which is the same answer an "off" option would have given and costs no
	// configuration to reach.
	editorBin := findEditor()
	// 같은 이유로 무조건 찾는다. 찾아도 @mux_editor=idea 가 아니면 쓰이지 않는다.
	ideaBin := findIntelliJ()
	// A preference, read once for the same reason minWidth is: it does not
	// change between ticks, and asking every two seconds costs a show-options
	// per panel per tick to be told the same number.
	ghostIdle := tmux.GhostClientIdle()
	exe, exeErr := os.Executable()
	if exeErr != nil {
		// Not knowing what we are running disables the restart check and
		// nothing else. See watchModel.exePath.
		exe = ""
	}

	for {
		final, err := tea.NewProgram(
			watchModel{
				ownSession: own,
				// Only the real panel gets a resolver; a watchModel built by
				// hand has none and never asks tmux anything.
				resolveOwn:     resolveOwnSession,
				probeFitted:    func() bool { return tmux.WindowFitted(selfPane()) },
				minWindowWidth: minWidth,
				showHeader:     header,
				editorBin:      editorBin,
				ideaBin:        ideaBin,
				editorChoice:   tmux.EditorChoice,
				focusWindow:    focusShellWindow,
				focusIdea:      focusShellIntelliJ,
				ghostIdle:      ghostIdle,
				probeMouse:     tmux.MouseEnabled,
				exePath:        exe,
				// Taken here rather than on the first tick so the baseline is
				// the file we actually started from. A tick later it may already
				// be someone else's.
				exeStamp: statBinary(exe),
			},
			tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
		if err != nil {
			return err
		}

		wm, ok := final.(watchModel)
		if !ok || !wm.restartRequested {
			return nil
		}

		// exec returns only when it failed. Returning here would end the process
		// and take the pane with it, so the panel goes back up on the code we
		// already have — and the new model stamps the file that would not run,
		// which is what stops this from looping on a binary that never will.
		if relaunch(exe) != nil {
			continue
		}
	}
}

func (m watchModel) Init() tea.Cmd {
	// The mouse is asked once at startup rather than waiting for the first
	// minute: a panel that cannot be clicked should say so while the user is
	// still looking at it for the first time. nil when probeMouse is.
	return tea.Batch(loadSessions, watchTick(), mouseCheckCmd(m.probeMouse))
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
		// arrives as motion — neither is a click.
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		name := m.sessionAtRow(msg.Y)
		if name == "" {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonLeft:
			// The panel shows no output to read first, so a click is the
			// decision. The keyboard keeps the two steps it needs: `mux nav`
			// moves the cursor and enter commits.
			//
			// The cursor is deliberately not moved. This panel is the one being
			// *left*; the panel that matters next is the target session's own,
			// a different process, which re-anchors itself when the client
			// lands there (reanchorOnArrival). Moving this cursor only left it
			// pointing at the session the user went to when they came back.
			return m, m.switchCmd(name)
		case tea.MouseButtonRight:
			// The other half of the move, on the button nobody presses while
			// skimming. It does not switch: opening a project's editor without
			// leaving the terminal you are working in is the case this exists
			// for, and a left click is right there for the other one.
			//
			// Measured against a real server: tmux's default MouseDown3Pane
			// forwards the press to the pane when the application has mouse
			// reporting on (`mouse_any_flag` is 1 here), so this needs nothing
			// in anyone's tmux.conf.
			return m, openSessionEditor(name, m.editorDirFor(name), m.editorSetup())
		}
		return m, nil

	case switchFailedMsg:
		// Surface it in the log rather than failing silently: the session can
		// vanish between the render and the click. Local, not shared — it says
		// this pane's click failed, which is not news in anyone else's window.
		m.local = pushEvents(m.local, []aiEvent{{
			at: time.Now(), session: msg.session, text: "⚠ 전환 실패: " + msg.err.Error(),
		}})
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case editorFailedMsg:
		// Same channel as a failed switch, and local for the same reason: this is
		// what happened in this pane, not news for anyone else's window. The
		// switch itself succeeded, so the row is already where the user asked to
		// be — this only says the editor did not follow.
		m.local = pushEvents(m.local, []aiEvent{{
			at: time.Now(), session: msg.session, text: "⚠ " + msg.editor + " 실행 실패: " + msg.err.Error(),
		}})
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case ghostDetachedMsg:
		// Local, like a failed switch: several panels see the same ghost and one
		// of them wins the race, so putting this in the shared log would draw a
		// line in every window for something one pane did. The session named is
		// the one the ghost was sitting in, which is the row a reader would go
		// looking at.
		m.local = pushEvents(m.local, []aiEvent{{
			at: time.Now(), session: msg.session,
			text: "⌁ 유령 클라이언트 분리: " + msg.tty + " (" + msg.size + ")",
		}})
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case eventsMergedMsg:
		m.shared = msg.events
		m.events = combineEvents(m.shared, m.local)
		return m, nil

	case watchTickMsg:
		m, adopt := m.adoptSavedWidth()
		// Before anything else: a panel running code the user has already
		// replaced is a panel reporting on a version of itself that no longer
		// exists, and there is no window event after an install for the hooks to
		// notice. `mux watch` is one process per pane and lives for days.
		m, stale := m.checkBinary(statBinary(m.exePath))
		if stale {
			return m.requestRestart()
		}
		// Who is attached, at its own slower cadence. Last in the batch and
		// last in the tick for a reason: it is the only thing here that acts on
		// something outside this pane, so everything the user is looking at is
		// already on its way before it runs.
		var ghost tea.Cmd
		if due, ok := m.dueForGhostCheck(time.Now()); ok {
			m, ghost = due, ghostCheckCmd(m.ghostIdle)
		} else {
			m = due
		}
		var mouse tea.Cmd
		if due, ok := m.dueForMouseCheck(time.Now()); ok {
			m, mouse = due, mouseCheckCmd(m.probeMouse)
		} else {
			m = due
		}
		return m, tea.Batch(loadSessions, watchTick(), adopt, ghost, mouse)

	case mouseStateMsg:
		m.mouseOff = msg.off
		return m, nil

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
		m = m.reanchorOnArrival()
		cmds := []tea.Cmd{mergeEventsCmd(fresh, sessionNames(m.sessions))}
		// A VS Code terminal looking at a window taller than itself sees a
		// viewport, and tmux sends that viewport to the top-left whenever the
		// active pane hides its cursor — which Claude does while it streams.
		// Kept on the bottom from here, the one process per session that is
		// already awake every two seconds. Only while someone is attached: an
		// empty session has no viewport to keep, which is what makes the
		// common case cost nothing.
		if m.ownSession != "" && m.ownClients > 0 {
			cmds = append(cmds, keepBottomVisibleCmd(m.ownSession))
		}
		// A name nothing answers to is not "could not tell" — it is a question
		// worth asking again. Costs one display-message, and only after a
		// rename; the check itself reads the list already in hand.
		if m.ownSessionLost() {
			cmds = append(cmds, resolveOwnCmd(m.resolveOwn))
		}
		return m.reselect(), tea.Batch(cmds...)

	case ownResolvedMsg:
		return m.adoptOwnSession(msg), nil
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
		return m, m.switchCmd(m.selected)
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

// reanchorOnArrival puts the cursor back on this pane's own session when a
// client arrives in it.
//
// Without this the cursor is wherever it was last left, which is precisely the
// wrong place: you move it to another session in order to *leave*, and the panel
// you come back to is still pointing at the session you went to. The ◀ mark and
// the highlight then name different rows — ◀ says "you are here" while the
// highlight says "herdr" — and nothing else in the panel reconciles them.
//
// It is deliberately the arrival and not the state. "My session is attached" is
// true the whole time you are sitting in it, so acting on the level would drag
// the cursor back two seconds after every M-Up. Acting on the false → true edge
// leaves a cursor you moved while you are here alone, and only resets it once
// you have been away.
//
// Session.Attached costs nothing — ListSessions already carries it every tick.
// What it cannot tell is *which* client arrived: with two clients on one server
// a second one landing in this session also re-anchors this cursor. Rare, and
// not wrong in direction — someone did just arrive here.
func (m watchModel) reanchorOnArrival() watchModel {
	was := m.ownClients
	m.ownClients = m.ownSessionClients()

	// Only a rise. A drop is someone leaving, which says nothing about where
	// the cursor of whoever stayed should be.
	if m.ownSession == "" || m.ownClients <= was {
		return m
	}
	return m.selectSession(m.ownSession)
}

// ownSessionClients reports how many clients are in this pane's session, as
// of the sessions just loaded.
func (m watchModel) ownSessionClients() int {
	for _, s := range m.sessions {
		if s.Name == m.ownSession {
			return s.AttachedClients
		}
	}
	return 0
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
	winWidth, winPanes, livePane, err := tmux.PaneShape(selfPane())
	if err != nil {
		return m, nil // nothing to compare against; leave the width alone
	}
	// The pane width from the same instant as the window, not the message's:
	// see tmux.PaneShape for the stale-message drag this prevents. The
	// message's width only stands in when tmux answered with nothing.
	if livePane > 0 {
		paneWidth = livePane
	}
	// Read here, beside the other I/O, and handed in as values: the pure
	// half below is what the tests drive, and it must not touch a server.
	m.fitted = tmux.WindowFitted(selfPane())
	saved := 0
	if m.fitted {
		// fit 이 고정하는 것과 같은 값 — 파일이 없으면 기본 폭이다(SavedPanelWidth 의 0 이 아니라).
		saved = tmux.DesiredPanelWidth()
	}
	m, cmd := m.applyResizeWith(paneWidth, winWidth, winPanes, m.fitted, saved)
	return m, cmd
}

// applyResizeWith is applyResize with every reading supplied. fitted says mux
// fixed this window's size (viewport.go), and saved is the one remembered width
// — read only for a fitted window, where applyFittedResize needs it.
func (m watchModel) applyResizeWith(paneWidth, winWidth, winPanes int, fitted bool, saved int) (watchModel, tea.Cmd) {
	// A window wide enough clears the tally: only *consecutive* narrow readings
	// mean the window really shrank.
	if winWidth >= m.minWidth() {
		m.narrowSamples = 0
	}

	if fitted {
		return m.applyFittedResize(paneWidth, winWidth, winPanes, saved)
	}

	switch {
	case m.winWidth == 0: // first size seen — adopt it
		m.winWidth = winWidth
		m.winPanes = winPanes
		switch {
		case paneWidth > maxPanelWidth(winWidth):
			// Opened wider than the ceiling: a width remembered from a roomier
			// window, or one saved before the ceiling existed. Correct it now
			// rather than leaving the work pane squeezed until something else
			// happens to trigger a re-layout.
			// Corrected, but *not* remembered. "Adoption clamps and never writes
			// back" is the rule that keeps one narrow window from dragging every
			// other panel down to its size, and this branch used to break it:
			// a panel opening in a 149-column window wrote its 74 to the shared
			// panel.json, and every other panel adopted 74 within two ticks.
			// Observed in the field while reproducing an unrelated bug — a
			// throwaway 172-column window took the saved width from 52 to 43.
			m.targetWidth = maxPanelWidth(winWidth)
			return m, restorePanelWidth(m.targetWidth)
		case paneWidth >= notifyMinWidth:
			m.targetWidth = paneWidth
		}
		return m, nil

	case winWidth <= 0:
		// Not a width. tmux reports one mid-relayout, and quitting on it would
		// close the panel because the server was busy for a moment.
		return m, nil

	case winWidth < m.minWidth():
		// The window shrank past what it can spare — a phone attached, most
		// likely. Leave, so the work pane gets its columns back; `prefix+a`
		// brings the panel back on a wide screen.
		//
		// Only on the *transition*: a panel opened by hand in an already narrow
		// window stays, for the same reason the key overrides the hook.
		//
		// And only on the *second* narrow sample in a row. Switching sessions
		// makes tmux re-lay-out every window it touches, and a window can report
		// a width it will not keep for more than an instant; quitting on one
		// reading closed panels during an ordinary session switch and left the
		// user with no sidebar and, worse, no way to steer — `mux nav` sends
		// keys to a pane that is no longer there and exits 0, so the panel
		// stopped answering with nothing to say why. Leaving is not undoable
		// from the keyboard, so it should take more evidence than staying.
		m.narrowSamples++
		if m.narrowSamples < narrowSamplesBeforeLeaving {
			return m, nil
		}
		return m, tea.Quit

	case winWidth == m.winWidth && winPanes != m.winPanes:
		// A pane appeared or died beside this one. tmux hands a closing pane's
		// columns to a neighbour, and that neighbour may be the panel — which
		// looks exactly like a drag if the window width is all you compare.
		// The count is what tells them apart, and it has to be checked before
		// the drag branch: the ceiling below cannot, because a two-pane window
		// splits into exactly half and half is not *over* half.
		m.winPanes = winPanes
		if m.targetWidth > 0 && paneWidth != m.targetWidth {
			return m, restorePanelWidth(m.targetWidth)
		}
		return m, nil

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
		if paneWidth == m.targetWidth {
			// 우리가 맞춰 둔 폭으로 돌아온 것뿐이다 — 새로 기억할 것이 없다.
			// 이게 adoptSavedWidth 의 안전장치이기도 하다: 좁은 창에서 천장에
			// 걸려 깎인 폭이 전역 저장본을 끌어내리는 경로가 여기 하나뿐이라,
			// 여기서 막으면 한 창의 사정이 나머지 패널을 좁히지 못한다.
			return m, nil
		}
		m.targetWidth = paneWidth
		return m, rememberPanelWidth(paneWidth)

	default: // tmux re-laid the window out
		m.winWidth = winWidth
		m.winPanes = winPanes
		if m.targetWidth > 0 && paneWidth != m.targetWidth {
			return m, restorePanelWidth(m.targetWidth)
		}
		return m, nil
	}
}

// applyFittedResize is applyResizeWith for a window mux fixed the size of — the
// one a VS Code or IntelliJ terminal is attached to (tmux/viewport.go).
//
// 맞춤 창도 «패널은 어디서나 한 폭»이다. 다른 점은 고치는 손 하나다: VS Code 만 붙은 창은
// 창 폭이 곧 VS Code + 1 + 패널이라, 패널 폭을 resize-pane 으로만 바꾸면 작업 pane 이
// VS Code 보다 좁아진다. 그래서 여기서는 폭을 직접 고치지 않고 fit 을 다시 돌린다(refitCmd)
// — 창 크기와 패널 고정을 fit 이 한 번에 한다.
//
// 예전에는 맞춤 창이면 패널이 폭에서 완전히 손을 뗐다(b54d4c3, 09-23). 그때 fit 은 패널을
// WT 안에 맞추려고 따로 줄였고(fitPanelWidth), 두 주인의 목표가 달라 99 ↔ 107 로 번갈았다.
// 10-19(aee4d94)에 fitPanelWidth 가 사라져 fit 도 «저장 폭, 창의 반이 상한»을 쓰게 됐는데 —
// 패널의 adoptSavedWidthWith 와 같은 공식 — 예외만 남았다. 그 결과가 2026-10-21 의 버그다:
// IntelliJ 터미널이 붙은 my-mux 에서 드래그한 폭이 저장되지 않았고, 다른 세션은 따라오지
// 않았고, my-mux 자신도 다음 훅에 72 로 돌아갔다. e2e TestDragInFittedWindowReachesEveryPanel.
//
// 드래그를 가리는 규칙은 보통 창과 같고, 가드가 하나 더 있다: fit 이 고정한 값(expected)도
// 드래그가 아니다. fit 은 훅에서 이 패널 모르게 resize-pane 을 하고, 창의 반에 걸려 깎인
// 값을 드래그로 저장하면 창 하나의 사정이 모든 패널을 좁힌다.
func (m watchModel) applyFittedResize(paneWidth, winWidth, winPanes, saved int) (watchModel, tea.Cmd) {
	switch {
	case winWidth <= 0:
		return m, nil
	case m.winWidth == 0, winWidth != m.winWidth:
		// 첫 읽기, fit 의 resize-window. 사람의 뜻이 아니고, 고정은 그 resize 가 부른 훅에서
		// fit 이 한다. 60→107 오염(창이 커지며 비례 배분된 폭을 드래그로 읽음)이 이 경로였으므로
		// 여기서는 기록만 한다.
		m.winWidth, m.winPanes = winWidth, winPanes
		if m.targetWidth == 0 && paneWidth >= notifyMinWidth && paneWidth <= maxPanelWidth(winWidth) {
			m.targetWidth = paneWidth
		}
		return m, nil
	case winPanes != m.winPanes:
		// 옆에 pane 이 생기거나 죽었다 — tmux 가 그 칸을 패널에 줬을 수 있다. 보통 창처럼 되돌리되
		// 손은 fit 이다. pane 이 생기고 죽는 것에는 훅이 없어서, 여기서 안 하면 다른 클라이언트가
		// 움직일 때까지 그대로다.
		m.winPanes = winPanes
		if m.targetWidth > 0 && paneWidth != m.targetWidth {
			return m, refitCmd(saved)
		}
		return m, nil
	case paneWidth < notifyMinWidth, paneWidth > maxPanelWidth(winWidth):
		// 같은 창에서 패널만 렌더할 수 없을 만큼 좁아졌거나 창의 반을 넘었다 — 의도가 아니다
		// (실측: 239칸 창이 74/164 로 갈라진 채 머묾). fit 으로 되돌린다.
		return m, refitCmd(saved)
	}
	expected := saved
	if expected > maxPanelWidth(winWidth) {
		expected = maxPanelWidth(winWidth)
	}
	if paneWidth == m.targetWidth || paneWidth == expected {
		// 우리나 fit 이 맞춘 폭이다.
		m.targetWidth = paneWidth
		return m, nil
	}
	// 사람이 테두리를 끌었다: 저장해 모든 패널이 따라오게 한다. 이 창을 새 폭으로 다시 맞추는
	// 것(VS Code 만 붙은 창이면 창 크기까지)은 다음 틱에 한다(refitPending) — 드래그 이벤트마다
	// 고정하면 끌고 있는 테두리를 지난 폭으로 당긴다. 저장이 끝난 뒤에 맞추게 되는 것도 덤이다:
	// 맞춤이 부른 훅은 저장 폭을 디스크에서 읽는다.
	m.targetWidth = paneWidth
	m.refitPending = true
	return m, rememberPanelWidth(paneWidth)
}

// refitOwnWindow re-runs the fit on this panel's own window at a panel width —
// only its own: every fitted window has a panel that follows the one width by
// itself, and sweeping them all reached into the window being dragged. A var
// for the reason openEditor is one: the real call reaches the developer's tmux.
var refitOwnWindow = func(width int) { tmux.SyncFittedWindowOf(selfPane(), width) }

// refitCmd runs refitOwnWindow as a command — it shells out, and Update is
// driven directly by tests.
func refitCmd(width int) tea.Cmd {
	if width <= 0 {
		return nil
	}
	return func() tea.Msg {
		refitOwnWindow(width)
		return nil
	}
}

// adoptSavedWidth follows the width last dragged in *any* panel, so one manual
// decision reaches every open one instead of only the session it was made in.
//
// Read straight off disk on the tick rather than deferred to a tea.Cmd: it is a
// two-line JSON file every two seconds, and the value is wanted by the very
// decision being made. applyResize reads the window shape synchronously for a
// harder-won reason, and this is the cheap end of the same trade.
//
// The window's own ceiling still applies — a panel cannot take more than half
// the window it is in — but the clamped value is never written back. That is
// what stops one narrow window from dragging every other panel down to its size,
// and the guard doing it is in applyResizeWith: a resize landing on the width we
// already wanted is not a drag and remembers nothing.
func (m watchModel) adoptSavedWidth() (watchModel, tea.Cmd) {
	saved := tmux.SavedPanelWidth()
	winWidth := m.winWidth

	// m.winWidth is the window size the last resize *event* reported, and there
	// are four ways it goes stale: a window that changes size without changing
	// this pane's (no WindowSizeMsg at all), a mid-relayout reading of zero, a
	// narrow reading, and a border drag — none of them update it. Clamping
	// against a stale number is what turns one re-layout into two visible
	// corrections two seconds apart, which is exactly the wobble this is meant
	// to prevent.
	//
	// So re-read, but only when the ceiling would actually bite. In the ordinary
	// case — a saved width comfortably under half the window — this costs no
	// tmux call at all, which is what keeps a two-second tick cheap.
	if saved > 0 && winWidth > 0 && saved > maxPanelWidth(winWidth) {
		if w, _, err := tmux.WindowShape(selfPane()); err == nil && w > 0 {
			winWidth = w
			m.winWidth = w
		}
	}
	next, cmd := m.adoptSavedWidthWith(saved, winWidth)
	if cmd == nil {
		return next, nil
	}
	// 맞춤 창도 따라간다 — 다만 fit 을 통해서다(adoptSavedWidthWith). 맞춤 여부는 바꿀 것이
	// 생겼을 때만 다시 묻는다: 크기가 이미 맞는 창은 resize 이벤트 없이 맞춤 상태가 되므로
	// m.fitted 가 낡을 수 있고, 평소 틱에 tmux 를 부르지 않으려면 이때뿐이다.
	if m.probeFitted != nil {
		m.fitted = m.probeFitted()
	}
	return m.adoptSavedWidthWith(saved, winWidth)
}

// adoptSavedWidthWith is the decision itself, split out so it can be tested
// without a tmux server — the same shape as applyResize/applyResizeWith, and for
// the same reason: `ui` has no mock runner, so a pure function is the only way
// to test this without resizing the developer's own panel.
func (m watchModel) adoptSavedWidthWith(saved, winWidth int) (watchModel, tea.Cmd) {
	if m.refitPending {
		// 지난 틱 사이에 이 맞춤 창에서 드래그가 있었다 — 이제 창을 그 폭으로 다시 맞춘다.
		// 저장 폭도 그 값이라 아래의 따라가기는 할 일이 없다.
		m.refitPending = false
		if m.fitted {
			return m, refitCmd(m.targetWidth)
		}
	}
	if saved <= 0 {
		return m, nil
	}
	want := saved
	if winWidth > 0 && want > maxPanelWidth(winWidth) {
		want = maxPanelWidth(winWidth)
	}
	if want < notifyMinWidth || want == m.targetWidth {
		return m, nil
	}
	m.targetWidth = want
	if m.fitted {
		// 맞춤 창의 폭은 fit 이 고친다 — applyFittedResize 의 이유. 창의 반 상한도 fit 이
		// 같은 공식으로 건다(panelWidthFor).
		return m, refitCmd(saved)
	}
	return m, restorePanelWidth(want)
}

// maxPanelWidth is an alias of tmux.MaxPanelWidth, the way notifyMinWidth is one
// of tmux.MinPanelWidth. The number has to be the same on both ends of the round
// trip: TogglePanel clamps the width it opens a panel at, and this clamps what
// the panel will hold. Two copies would let a panel open at a width it rejects
// on its first frame.
func maxPanelWidth(winWidth int) int { return tmux.MaxPanelWidth(winWidth) }

// rememberPanelWidth records the pane's width, so every panel — this one after
// a reopen, and the others on their next tick — comes back at the size the user
// dragged it to.
//
// One width, not one per session. It used to be both, with a per-session tmux
// option consulted first, and that made a drag in one session invisible to the
// rest. Someone dragging a border is saying how wide the panel is, not how wide
// it is here.
//
// Failure is deliberately silent: this is a convenience, and a panel whose pane
// is going away is not worth a line in the event log.
func rememberPanelWidth(width int) tea.Cmd {
	return func() tea.Msg {
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

type editorFailedMsg struct {
	session string
	editor  string // 로그에 쓰는 이름: "VS Code", "IntelliJ", 모르는 값이면 "편집기"
	err     error
}

// switchToSession moves the current tmux client. It stays a tea.Cmd rather than
// quitting first, the way the TUI's attach must: switch-client returns normally
// instead of replacing this process, so the panel survives the switch.
func switchToSession(name, own, targetDir string, ed editorSetup) tea.Cmd {
	return func() tea.Msg {
		// Hand focus back before leaving, when there is any to hand back. The
		// window we are about to leave would otherwise keep reporting the
		// panel's command and directory as the session's own. Best-effort:
		// there may be no previous pane, and that must not block the switch the
		// user actually asked for.
		restoreFocus()

		// Which client moves is not left to tmux. switch-client with no -c
		// moves "the current client", which for a command run from a pane is
		// whichever client is watching that pane's session — and with both
		// terminals attached that may be the VS Code one, which is supposed to
		// watch a single project. Name Windows Terminal explicitly instead.
		var err error
		if tmux.SessionHasVSCodeClient(own) {
			err = tmux.SwitchWTClient(name)
		} else {
			err = tmux.SwitchClient(name)
		}

		// Then bring the target's own VS Code window forward too. The terminal
		// and the editor are two views of one session, and moving only the
		// terminal leaves the user reading one project while typing in another.
		// Only a window that is already open on it — a click must not launch
		// an editor for a session nobody has open. Best effort: it rides along
		// with the switch and must not report the switch as broken.
		//
		// This used to *replace* the switch when such a window existed, to
		// keep Windows Terminal out of a session whose window was sized to the
		// editor. The window is sized to the larger client now (viewport.go),
		// so there is nothing left to avoid, and the skip only meant clicking
		// those rows moved nothing.
		//
		// Which editor's window is @mux_editor's (raiseChosenEditor) — the same
		// choice the right click launches with.
		raiseChosenEditor(name, targetDir, ed)

		if err != nil {
			return switchFailedMsg{session: name, err: err}
		}
		return nil
	}
}

// vscodeWindowFor is the folder of the VS Code window to raise for a session,
// or "" when none is open — in which case nothing is launched.
//
// Two ways a window is known to be open: its integrated terminal is attached
// to this very session, or VS Code's own record of its windows lists the
// folder (tmux.OpenVSCodeFolders). The first is the surer signal, so it goes
// first; the second is what makes the click work when no terminal is open.
//
// No fallback to the project folder. A left click moves the user to a session;
// opening an editor they did not have is the right click's job, and with
// terminals closed the fallback opened a new window on every click.
func vscodeWindowFor(name, editorBin, targetDir string) string {
	if editorBin == "" || targetDir == "" {
		return ""
	}
	if dir := tmux.VSCodeDirFor(targetDir, tmux.VSCodeClientDirs()[name]); dir != "" {
		return dir
	}
	return tmux.VSCodeWindowFor(targetDir)
}

// editorOpenOn reports whether VS Code has a window open on exactly dir — the
// right click's precondition for asking the shell. Exactly, not containing: a
// right click on a subfolder of an open project opens that subfolder, as
// `code <subfolder>` always did.
func editorOpenOn(name, editorBin, dir string) bool {
	return dir != "" && vscodeWindowFor(name, editorBin, dir) == strings.TrimRight(dir, "/")
}

// raiseOpenEditor brings forward the VS Code window already open on a session,
// and does nothing when there is none. It is the one place a *switch* touches
// the editor — the panel's left click, enter, and the TUI's pick all come
// through here — so none of them can launch a window. Launching is the right
// click's job (openSessionEditor); the TUI once launched on every pick because
// it had its own copy of this without the "already open" half. Best effort: it
// rides along with a switch and must not report the switch as broken.
//
// The window is raised through the shell first (focus), because on native
// Wayland `code <folder>` leaves an open window where it is; `code` is the
// fallback, and the only path on WSL. focus may be nil.
func raiseOpenEditor(name, editorBin, targetDir string, focus func(string) focusResult) {
	dir := vscodeWindowFor(name, editorBin, targetDir)
	if dir == "" {
		return
	}
	r := focusUnavailable
	if focus != nil {
		r = focus(dir)
	}
	if r == focusUnavailable {
		_ = openEditor(editorBin, dir)
	}
}

// keepBottomVisibleCmd runs the viewport rule for this pane's session. A
// command rather than a call: it shells out, and Update is driven directly by
// tests that must not touch the developer's own server.
func keepBottomVisibleCmd(session string) tea.Cmd {
	return func() tea.Msg {
		tmux.KeepBottomVisible(session)
		return nil
	}
}

// switchCmd builds the move for one session, so a left click and enter cannot
// drift apart on which client they are allowed to move.
func (m watchModel) switchCmd(name string) tea.Cmd {
	return switchToSession(name, m.ownSession, m.sessionDir(name), m.editorSetup())
}

// sessionDir is the directory a session is *about*, which is what decides
// which VS Code window owns it: the project tag when the session carries one,
// else its current directory. The tag wins because the directory follows the
// active pane and wanders — a session tagged for projects/api whose shell is
// sitting in ~/wandered still belongs to the api window.
func (m watchModel) sessionDir(name string) string {
	s, ok := sessionByName(m.sessions, name)
	if !ok {
		return ""
	}
	if s.ProjectDir != "" {
		return s.ProjectDir
	}
	return s.Directory
}

// openSessionEditor opens a session's project folder without moving the client.
//
// The focus has to be handed back for the same reason a switch hands it back,
// and it is easier to miss here: tmux's own MouseDown3Pane runs `select-pane`
// before forwarding the press, so after a right click the panel *is* the active
// pane — and unlike a switch, nothing else afterwards would take the user out
// of it.
//
// A window already open on exactly this folder is raised through the shell
// first, for the reason raiseOpenEditor gives; launching is what happens
// otherwise. The shell is only asked when the folder is known to be open
// (editorOpenOn): it matches windows by base name, so asking for a folder with
// no window could raise a same-named other project and launch nothing. That
// check reads tmux and VS Code's record, which is why it runs here in the
// command and not in Update. focus may be nil.
//
// 어느 편집기인지는 여기서 @mux_editor 를 읽어 정한다(ed.choose). tmux 를 묻는 일이라
// Update 가 아니라 이 커맨드 안이다. IntelliJ 를 고르면 VS Code 창 올리기는 묻지 않고
// 이미 열린 프로젝트도 IntelliJ 에 맡긴다. 그 창이 앞으로 나오는지는 재 보지 않았다 —
// 처음 여는 폴더의 신뢰 대화상자는 GNOME 에서 현재 창 뒤에 떴다(2026-10-21).
//
// 편집기가 하나도 없어도 여기까지 온다. VS Code 경로는 bin 이 없으면 조용히 끝나고(예전
// 그대로), IntelliJ 를 골랐는데 없거나 모르는 값이면 보고한다 — 관문을 editorDirFor 에
// 두면 그 보고가 사라진다.
func openSessionEditor(name, dir string, ed editorSetup) tea.Cmd {
	return func() tea.Msg {
		restoreFocus()

		if dir == "" {
			return nil
		}
		choice := ""
		if ed.choose != nil {
			choice = ed.choose()
		}
		switch choice {
		case "", choiceVSCode:
			return openInVSCode(name, ed.code, dir, ed.focus)
		case choiceIntelliJ:
			// 그 폴더에 «정확히» 열린 프로젝트가 있으면 창을 올리고 끝낸다 — VS Code 의
			// editorOpenOn 과 같은 규칙. 하위 폴더는 띄운다(IntelliJ 가 그 폴더를 연다).
			if ed.focusIdea != nil && intellijWindowFor(name, dir) == strings.TrimRight(dir, "/") &&
				ed.focusIdea(dir) == focusRaised {
				return nil
			}
			if ed.idea == "" {
				return editorFailedMsg{session: name, editor: "IntelliJ", err: errIntelliJMissing}
			}
			if err := openDetached(ed.idea, dir); err != nil {
				return editorFailedMsg{session: name, editor: "IntelliJ", err: err}
			}
			return nil
		default:
			return editorFailedMsg{session: name, editor: "편집기",
				err: fmt.Errorf("알 수 없는 @mux_editor 값 %q (code 또는 idea)", choice)}
		}
	}
}

// errIntelliJMissing 은 @mux_editor=idea 인데 런처를 못 찾았을 때다. 조용히 VS Code 로
// 물러서지 않는다 — 그러면 설정이 먹었는지 알 길이 없다.
var errIntelliJMissing = errors.New("idea 를 찾지 못함 (PATH, JetBrains Toolbox, snap)")

// openInVSCode 는 오른쪽 클릭의 VS Code 경로다. bin 이 "" 이면 아무것도 하지 않는다.
func openInVSCode(name, bin, dir string, focus func(string) focusResult) tea.Msg {
	if bin == "" {
		return nil
	}
	if focus != nil && editorOpenOn(name, bin, dir) && focus(dir) == focusRaised {
		return nil
	}
	if err := openEditor(bin, dir); err != nil {
		return editorFailedMsg{session: name, editor: "VS Code", err: err}
	}
	return nil
}

// editorSetup 은 오른쪽 클릭이 편집기를 고르고 띄우는 데 필요한 것을 묶는다.
type editorSetup struct {
	code, idea string                   // 런처 경로, "" 이면 없음
	choose     func() string            // @mux_editor, nil 이면 VS Code
	focus      func(string) focusResult // VS Code 창 올리기, nil 이면 끔
	focusIdea  func(string) focusResult // IntelliJ 창 올리기, nil 이면 끔
}

func (m watchModel) editorSetup() editorSetup {
	return editorSetup{code: m.editorBin, idea: m.ideaBin, choose: m.editorChoice,
		focus: m.focusWindow, focusIdea: m.focusIdea}
}

// chosen is @mux_editor as ed reads it; "" when there is nothing to ask.
func (ed editorSetup) chosen() string {
	if ed.choose == nil {
		return ""
	}
	return ed.choose()
}

// raiseChosenEditor is the switch's half of the editor: it brings forward the
// window @mux_editor's editor already has open on the session, and launches
// nothing. idea raises an IntelliJ project window; anything else is VS Code as
// it always was — a value the right click would report as unknown still gets
// the old behaviour here, where nothing is reported.
func raiseChosenEditor(name, targetDir string, ed editorSetup) {
	if ed.chosen() == choiceIntelliJ {
		raiseOpenIntelliJ(name, targetDir, ed.focusIdea)
		return
	}
	raiseOpenEditor(name, ed.code, targetDir, ed.focus)
}

// raiseOpenIntelliJ brings forward the IntelliJ window open on a session's
// project, and does nothing when there is none. No fallback: IntelliJ's
// launcher opens a project rather than merely raising one, and opening is the
// right click's job. focus may be nil.
func raiseOpenIntelliJ(name, targetDir string, focus func(string) focusResult) {
	if focus == nil {
		return
	}
	if dir := intellijWindowFor(name, targetDir); dir != "" {
		focus(dir)
	}
}

// intellijWindowFor is the folder of the IntelliJ project to raise for a
// session, or "" when none is open — vscodeWindowFor's IntelliJ half. An
// IntelliJ terminal attached to the session names its project surely; IntelliJ's
// record of open projects covers a session with no such terminal. The window is
// chosen by that folder's base name, which is why the folder must be one that is
// known to be open.
func intellijWindowFor(name, targetDir string) string {
	if targetDir == "" {
		return ""
	}
	if dir := tmux.VSCodeDirFor(targetDir, tmux.IntelliJClientDirs()[name]); dir != "" {
		return dir
	}
	return tmux.IntelliJWindowFor(targetDir)
}

// editorDirFor is the folder to open for a session, or "" when the panel does
// not know that session. 편집기가 있는지는 여기서 보지 않는다 — openSessionEditor 가
// 고른 편집기마다 따로 본다.
//
// ProjectDir first: types.go says why, and it is the whole reason that field
// exists. Directory follows the active pane and is overwritten by the AI's own
// cwd, so on a session whose shell has wandered it names somewhere that is not
// the project. @project_dir does not move.
//
// Costs no tmux call: listFormat already carries both, so m.sessions has them.
func (m watchModel) editorDirFor(name string) string {
	s, ok := sessionByName(m.sessions, name)
	if !ok {
		return ""
	}
	if s.ProjectDir != "" {
		return s.ProjectDir
	}
	return s.Directory
}

// sessionByName finds a session in the slice the panel last loaded.
func sessionByName(sessions []tmux.Session, name string) (tmux.Session, bool) {
	for _, s := range sessions {
		if s.Name == name {
			return s, true
		}
	}
	return tmux.Session{}, false
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
	lines := notifyLines(m.sessions, m.events, m.width, m.height, m.selected, m.ownSession, m.showHeader)
	if !m.mouseOff || len(lines) == 0 {
		return lines
	}
	// On top, where it is read before anything is clicked. It carries no
	// session, so the rows under it still click to the session they name —
	// sessionAtRow indexes this same slice — and the cursor cannot stop on it.
	return append([]notifyLine{mouseHintLine(m.width)}, lines...)
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

// checkBinary compares a fresh reading of the panel's own path against the one
// taken at startup, and says whether this process is now running code the user
// has replaced.
//
// It is pure so that the decision can be tested without installing anything, and
// it stands down entirely when exePath is empty — a model built without a
// baseline never asks the filesystem a question.
//
// A disagreeing reading has to arrive twice before it counts, and the two have
// to agree with *each other*. An install that is still copying moves the size
// under us, so "different from the baseline" is true well before the file is a
// binary; "different, and unchanged since I last looked" is the thing worth
// acting on. An unreadable path — briefly, between the unlink and the create —
// clears the count rather than deciding anything.
func (m watchModel) checkBinary(now binaryStamp) (watchModel, bool) {
	if m.exePath == "" || !m.exeStamp.ok() || !now.ok() || now.same(m.exeStamp) {
		m.pendingExe = binaryStamp{}
		return m, false
	}
	if !now.same(m.pendingExe) {
		m.pendingExe = now
		return m, false
	}
	return m, true
}

// requestRestart quits so RunWatch can exec — unless the user is standing in
// this pane, in which case it waits.
//
// The wait is not about safety; exec keeps the pane and therefore the focus. It
// is that `m.selected` does not survive, so restarting under someone who is
// steering with prefix+Tab and j/k moves their cursor back to their own session
// mid-gesture. The install is not going anywhere, and neither is the next tick.
//
// Reached only when checkBinary said so, which needs a baseline no test sets —
// that is what keeps this tmux call out of `go test ./ui`.
func (m watchModel) requestRestart() (tea.Model, tea.Cmd) {
	if tmux.PaneActive(selfPane()) {
		return m, tea.Batch(loadSessions, watchTick())
	}
	m.restartRequested = true
	return m, tea.Quit
}
