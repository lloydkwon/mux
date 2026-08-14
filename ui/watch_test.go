package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func watchTestModel(w, h int) watchModel {
	return watchModel{width: w, height: h}
}

var errRefreshTest = errTest("refresh failed")

type errTest string

func (e errTest) Error() string { return string(e) }

// The pane is not a panel floating over anything — it *is* the pane. A view
// short of the pane height leaves the previous frame's rows behind, and one
// over it scrolls the top away.
func TestWatchViewFillsPane(t *testing.T) {
	sessions := []tmux.Session{sess("mux", tmux.AIStateWorking), sess("api", tmux.AIStateApproval)}
	sessions[1].AIWaitingFor = "Bash: git push"

	for _, w := range []int{20, 40, 48, 84, 120} {
		for _, h := range []int{3, 4, 6, 12, 30} {
			m := watchTestModel(w, h)
			m.sessions = sessions
			// A highlighted row and a marked one both re-render the row, so the
			// shape has to hold with them on.
			m.selected = "api"
			m.ownSession = "mux"
			m.events = []aiEvent{{at: time.Now(), session: "api", text: "❗ 승인 대기"}}

			lines := strings.Split(m.View(), "\n")
			if len(lines) != h {
				t.Errorf("%dx%d: %d lines, want %d", w, h, len(lines), h)
				continue
			}
			for i, l := range lines {
				if got := ansi.StringWidth(ansi.Strip(l)); got != w {
					t.Errorf("%dx%d: line %d measures %d cells, want %d", w, h, i, got, w)
				}
			}
		}
	}
}

// A pane showing nothing reads as a crashed pane. Every degraded case has to
// say something.
func TestWatchViewDegrades(t *testing.T) {
	tests := []struct {
		name  string
		model watchModel
		want  string
	}{
		{
			name:  "no AI sessions",
			model: watchModel{width: 40, height: 8},
			want:  "AI 세션 없음",
		},
		{
			name:  "too narrow for the columns",
			model: watchModel{width: 10, height: 8},
			want:  "좁",
		},
		{
			name:  "refresh failed",
			model: watchModel{width: 40, height: 8, err: errRefreshTest},
			want:  "refresh failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ansi.Strip(tc.model.View())
			if !strings.Contains(got, tc.want) {
				t.Errorf("view = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// Before the first WindowSizeMsg there is no size to render into; a guess would
// be drawn once and then corrected, which flashes.
func TestWatchViewBeforeSize(t *testing.T) {
	if got := (watchModel{}).View(); got != "" {
		t.Errorf("view without a size = %q, want empty", got)
	}
}

// The panel must log transitions on its own — it runs as a separate process, so
// it cannot inherit the TUI's history. First refresh seeds, second reports.
func TestWatchLogsTransitions(t *testing.T) {
	m := watchTestModel(40, 12)

	first, _ := m.Update(sessionsLoadedMsg{sessions: []tmux.Session{sess("a", tmux.AIStateWorking)}})
	seeded := first.(watchModel)
	if len(seeded.events) != 0 {
		t.Fatalf("first refresh logged %d events, want none", len(seeded.events))
	}

	second, _ := seeded.Update(sessionsLoadedMsg{sessions: []tmux.Session{sess("a", tmux.AIStateApproval)}})
	got := second.(watchModel)
	if len(got.events) != 1 || got.events[0].session != "a" {
		t.Fatalf("events = %v, want one for session a", got.events)
	}
	if !strings.Contains(ansi.Strip(got.View()), "승인 대기") {
		t.Error("the logged transition does not reach the view")
	}
}

// Keys the panel does not use must stay inert: the bindings send only the ones
// below, and anything else arriving here is a stray that should change nothing.
func TestWatchKeysAreInert(t *testing.T) {
	m := watchTestModel(40, 12)

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}); cmd != nil {
		t.Error("a stray key produced a command")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Error("q did not quit")
	}
}

// The tick keeps the pane live without any input.
func TestWatchTickReloads(t *testing.T) {
	m := watchTestModel(40, 12)
	if _, cmd := m.Update(watchTickMsg(time.Now())); cmd == nil {
		t.Error("tick did not schedule a reload")
	}
}

// A click has to land on the session it looks like it landed on, and every row
// of a session's block counts — the row itself, an approval's reason line, and
// the spacer that makes the target easier to hit.
func TestSessionAtRow(t *testing.T) {
	now := time.Now()
	web := sess("web", tmux.AIStateReady)
	web.AISince = now.Add(-time.Minute) // freshest state
	blocked := sess("api", tmux.AIStateApproval)
	blocked.AISince = now.Add(-time.Hour)
	blocked.AIWaitingFor = "Bash: git push"
	old := sess("mux", tmux.AIStateWorking)
	old.AISince = now.Add(-24 * time.Hour)

	m := watchTestModel(40, 30)
	// Deliberately not in display order — the panel sorts by elapsed itself.
	m.sessions = []tmux.Session{old, blocked, web}
	m.events = []aiEvent{{at: now, session: "mux", text: "✅ 작업 완료"}}

	tests := []struct {
		row  int
		want string
	}{
		{0, ""}, // 🔔 header
		{1, ""}, // the blank under the header
		{2, "web"},
		{3, "web"}, // spacer, still the same block
		{4, "api"},
		{5, "api"}, // the indented waitingFor line
		{6, "api"}, // spacer
		{7, "mux"}, // last session: no spacer follows, so a one-row target
		// The section break belongs to no session — if it did, the last session
		// would silently get the two-row target the layout just took away.
		{8, ""},
		{9, ""},  // ── 최근 이벤트
		{10, ""}, // the event row
		{99, ""}, // past the end
		{-1, ""},
	}
	for _, tc := range tests {
		if got := m.sessionAtRow(tc.row); got != tc.want {
			t.Errorf("sessionAtRow(%d) = %q, want %q", tc.row, got, tc.want)
		}
	}
}

// Degraded panes have no rows to click, and clicking must not switch anywhere.
func TestSessionAtWhenDegraded(t *testing.T) {
	narrow := watchTestModel(10, 20)
	narrow.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking)}
	if got := narrow.sessionAtRow(1); got != "" {
		t.Errorf("narrow pane returned %q", got)
	}

	broken := watchTestModel(40, 20)
	broken.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking)}
	broken.err = errRefreshTest
	if got := broken.sessionAtRow(1); got != "" {
		t.Errorf("errored pane returned %q", got)
	}
}

// Only a left press acts. Release repeats the button under SGR and drags arrive
// as motion — either one acting would fire on an accidental swipe.
func TestWatchOnlyLeftPressActs(t *testing.T) {
	m := watchTestModel(40, 20)
	m.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking)}

	// Row 0 is the heading and row 1 the blank under it; the first session is 2.
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd == nil {
		t.Error("left press on a session row did nothing")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}); cmd != nil {
		t.Error("release acted")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}); cmd != nil {
		t.Error("drag acted")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd != nil {
		t.Error("clicking the header acted")
	}
}

// The panel shows no output to read first, so a click is the decision — it does
// not move a cursor and wait for a second one. The keyboard keeps the two steps
// it needs, because `mux nav` has no way to point at a row and commit at once.
func TestWatchClickSwitchesImmediately(t *testing.T) {
	m := watchTestModel(48, 20)
	m.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking), sess("api", tmux.AIStateApproval)}
	m = m.reselect()
	before := m.selected

	// Row 0 is the heading and row 1 the blank under it; the first session is 2.
	updated, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Error("a click on a session row did not switch")
	}
	if got := updated.(watchModel).selected; got != before {
		t.Errorf("the click moved the cursor to %q — it should switch, not select", got)
	}
}

// The selection is held by name because the rows reorder underneath it. A
// session that goes away has to release it rather than leave the cursor on a
// name that no longer exists.
func TestWatchSelectionSurvivesReorderAndDrops(t *testing.T) {
	now := time.Now()
	mk := func(name string, since time.Duration, st tmux.AIState) tmux.Session {
		s := sess(name, st)
		s.AISince = now.Add(-since)
		return s
	}

	m := watchTestModel(100, 30)
	loaded, _ := m.Update(sessionsLoadedMsg{sessions: []tmux.Session{
		mk("mux", time.Minute, tmux.AIStateWorking),
		mk("api", time.Hour, tmux.AIStateWorking),
	}})
	m = loaded.(watchModel)
	if m.selected != "mux" {
		t.Fatalf("selected %q, want the top row", m.selected)
	}

	// Point at the second row, then make it sort to the top.
	m = m.selectSession("api")
	reordered, _ := m.Update(sessionsLoadedMsg{sessions: []tmux.Session{
		mk("mux", time.Hour, tmux.AIStateWorking),
		mk("api", time.Second, tmux.AIStateWorking),
	}})
	if got := reordered.(watchModel).selected; got != "api" {
		t.Errorf("selected %q after a reorder, want api", got)
	}

	gone, _ := reordered.(watchModel).Update(sessionsLoadedMsg{sessions: []tmux.Session{
		mk("mux", time.Hour, tmux.AIStateWorking),
	}})
	if got := gone.(watchModel).selected; got != "mux" {
		t.Errorf("selected %q after api vanished, want it to fall back to mux", got)
	}
}

// Keys arrive by send-keys from a tmux binding, so the panel acts on them
// without ever being the focused pane.
func TestWatchKeysMoveSelection(t *testing.T) {
	m := watchTestModel(100, 30)
	m.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking), sess("api", tmux.AIStateApproval)}
	m = m.reselect()

	order := m.sessionOrder()
	if len(order) != 2 {
		t.Fatalf("order = %v, want two sessions", order)
	}

	down, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := down.(watchModel).selected; got != order[1] {
		t.Errorf("down selected %q, want %q", got, order[1])
	}
	// The end is a stop, not a wrap: a held key would otherwise cycle forever.
	stop, _ := down.(watchModel).Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := stop.(watchModel).selected; got != order[1] {
		t.Errorf("down at the bottom selected %q, want it held at %q", got, order[1])
	}
	up, _ := stop.(watchModel).Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := up.(watchModel).selected; got != order[0] {
		t.Errorf("up selected %q, want %q", got, order[0])
	}
	if _, cmd := up.(watchModel).Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("enter did not switch")
	}
}

// A failed switch must reach the log — the session can die between the render
// and the click, and a silent no-op looks like a broken panel.
func TestWatchSwitchFailureIsLogged(t *testing.T) {
	m := watchTestModel(40, 20)
	updated, _ := m.Update(switchFailedMsg{session: "gone", err: errRefreshTest})
	got := updated.(watchModel)

	if len(got.events) != 1 || !strings.Contains(got.events[0].text, "전환 실패") {
		t.Fatalf("events = %v, want a switch failure", got.events)
	}
}

// The panel drifted 40 → 46 and differed between sessions because every resize
// was taken as intent. With `aggressive-resize on`, switching sessions resizes
// windows and tmux redistributes panes, so each switch nudged the panel.
func TestWatchHoldsWidthAgainstRelayout(t *testing.T) {
	m := watchTestModel(40, 20)

	// Every window here is comfortably over MinWindowWidth: this test is about
	// holding a width, and a narrow window would take the quit branch first.
	//
	// Window unchanged, pane changed: a border drag. Adopt and remember it.
	m.winWidth, m.targetWidth = 250, 40
	dragged, cmd := m.applyResizeWith(60, 250)
	if cmd == nil {
		t.Error("a drag did not schedule a save")
	}
	if dragged.targetWidth != 60 {
		t.Errorf("target = %d after a drag, want 60", dragged.targetWidth)
	}

	// Window changed and the pane drifted: tmux re-laid it out, so undo it.
	relaid, cmd := dragged.applyResizeWith(66, 300)
	if cmd == nil {
		t.Error("a re-layout was not corrected")
	}
	if relaid.targetWidth != 60 {
		t.Errorf("target = %d after a re-layout, want it held at 60", relaid.targetWidth)
	}
	if relaid.winWidth != 300 {
		t.Errorf("winWidth = %d, want the new window size recorded", relaid.winWidth)
	}

	// The correction lands: same window, pane back on target, nothing to undo.
	settled, _ := relaid.applyResizeWith(60, 300)
	if settled.targetWidth != 60 {
		t.Errorf("target = %d once settled, want 60", settled.targetWidth)
	}
}

// A pane squeezed below what the panel can render is not intent. Adopting one
// is what collapsed the panel to a single column and then held it there.
func TestWatchWidthIgnoresSqueeze(t *testing.T) {
	m := watchTestModel(40, 20)
	m.winWidth, m.targetWidth = 150, 60

	squeezed, _ := m.applyResizeWith(1, 150) // same window: would read as a drag
	if squeezed.targetWidth != 60 {
		t.Errorf("target = %d, want the squeeze rejected and 60 kept", squeezed.targetWidth)
	}
}

// Attaching from a phone shrinks the window and the panel would hold its 48
// columns, leaving the work pane almost none. It leaves instead. The first size
// is exempt: a panel opened by hand in an already narrow window stays, the same
// way the key overrides the hook.
func TestWatchQuitsWhenWindowGetsNarrow(t *testing.T) {
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}

	// Opened by hand in a 54-column window: stays.
	m := watchTestModel(40, 20)
	narrowFirst, cmd := m.applyResizeWith(40, 54)
	if quits(cmd) {
		t.Error("quit on the first size — a hand-opened panel should stay")
	}
	if narrowFirst.winWidth != 54 {
		t.Errorf("winWidth = %d, want the narrow window adopted", narrowFirst.winWidth)
	}

	// A phone attaches to a window that was wide: leave.
	wide := watchTestModel(48, 20)
	wide.winWidth, wide.targetWidth = 269, 48
	if _, cmd := wide.applyResizeWith(48, 54); !quits(cmd) {
		t.Error("did not quit when the window shrank past the minimum")
	}

	// Still wide enough: hold the width as before, do not leave. Expressed
	// against the constant so it follows when the threshold moves.
	if _, cmd := wide.applyResizeWith(52, tmux.MinWindowWidth); quits(cmd) {
		t.Error("quit on a window that is exactly the minimum")
	}
}

// The session this pane lives in is already on screen beside the panel, so
// starting the cursor there means the first enter goes nowhere. It is also
// almost always the top row — the list is ordered by how recently a state
// changed, and the session you are working in is the one whose state keeps
// changing — so this is the default case, not an edge one.
func TestWatchAutoSelectSkipsOwnSession(t *testing.T) {
	now := time.Now()
	mk := func(name string, since time.Duration) tmux.Session {
		s := sess(name, tmux.AIStateWorking)
		s.AISince = now.Add(-since)
		return s
	}

	tests := []struct {
		name     string
		own      string
		sessions []tmux.Session
		want     string
	}{
		{
			name:     "the freshest row is this pane's own session",
			own:      "project",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "api",
		},
		{
			name:     "own session is not the top row and nothing changes",
			own:      "api",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "project",
		},
		{
			// One session, and it is this one. Nothing better to point at.
			name:     "only this session exists",
			own:      "project",
			sessions: []tmux.Session{mk("project", time.Second)},
			want:     "project",
		},
		{
			// Not knowing must not be guessed either way — this is exactly how
			// the panel behaved before it could tell.
			name:     "own session unknown",
			own:      "",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := watchTestModel(100, 30)
			m.ownSession = tt.own
			loaded, _ := m.Update(sessionsLoadedMsg{sessions: tt.sessions})
			if got := loaded.(watchModel).selected; got != tt.want {
				t.Errorf("selected %q, want %q", got, tt.want)
			}
		})
	}
}

// Skipping is what happens when nobody chose. Choosing this pane's own session
// is allowed, and a refresh two seconds later must not take it back.
func TestWatchOwnSessionCanBeChosen(t *testing.T) {
	now := time.Now()
	mk := func(name string, since time.Duration) tmux.Session {
		s := sess(name, tmux.AIStateWorking)
		s.AISince = now.Add(-since)
		return s
	}
	sessions := []tmux.Session{mk("project", time.Second), mk("api", time.Hour)}

	m := watchTestModel(100, 30)
	m.ownSession = "project"
	loaded, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	m = loaded.(watchModel)

	m = m.selectSession("project")
	refreshed, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	if got := refreshed.(watchModel).selected; got != "project" {
		t.Errorf("selected %q after a refresh, want the deliberate choice kept", got)
	}
}
