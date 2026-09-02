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

// firstSessionRow is the pane row the first clickable session lands on. Tests
// that care about clicking a session ask for it rather than counting the chrome
// above it, which changes.
func firstSessionRow(m watchModel) int {
	for i, l := range m.sessionLines() {
		if l.session != "" {
			return i
		}
	}
	return -1
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
		{0, ""},    // 🔔 header
		{1, ""},    // the blank under the header
		{2, "web"}, // web has no event of its own
		{3, "web"}, // spacer, still the same block
		{4, "api"},
		{5, "api"}, // the indented waitingFor line
		{6, "api"}, // spacer
		{7, "mux"},
		{8, "mux"}, // mux's own last event; last session, so no spacer follows
		// The section break below the list belongs to nobody, so the last
		// session does not silently gain a row it does not own.
		{9, ""},
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
	// Left and right are the two answers this panel has. Anything else — the
	// middle button, a wheel tilt — must fall through rather than pick one.
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonMiddle, Action: tea.MouseActionPress}); cmd != nil {
		t.Error("the middle button acted")
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

	// Computed rather than hardcoded: reselect() gives this model a selection, so
	// the header block sits above the heading and the first session row moves
	// with it. A literal here would pin the header's height into a test about
	// clicking.
	row := firstSessionRow(m)
	updated, cmd := m.Update(tea.MouseMsg{Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Errorf("a click on session row %d did not switch", row)
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
	m.winWidth, m.targetWidth, m.winPanes = 250, 40, 2
	dragged, cmd := m.applyResizeWith(60, 250, 2)
	if cmd == nil {
		t.Error("a drag did not schedule a save")
	}
	if dragged.targetWidth != 60 {
		t.Errorf("target = %d after a drag, want 60", dragged.targetWidth)
	}

	// Window changed and the pane drifted: tmux re-laid it out, so undo it.
	relaid, cmd := dragged.applyResizeWith(66, 300, 2)
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
	settled, _ := relaid.applyResizeWith(60, 300, 2)
	if settled.targetWidth != 60 {
		t.Errorf("target = %d once settled, want 60", settled.targetWidth)
	}
}

// A pane squeezed below what the panel can render is not intent. Adopting one
// is what collapsed the panel to a single column and then held it there.
func TestWatchWidthIgnoresSqueeze(t *testing.T) {
	m := watchTestModel(40, 20)
	m.winWidth, m.targetWidth, m.winPanes = 150, 60, 2

	squeezed, _ := m.applyResizeWith(1, 150, 2) // same window: would read as a drag
	if squeezed.targetWidth != 60 {
		t.Errorf("target = %d, want the squeeze rejected and 60 kept", squeezed.targetWidth)
	}
}

// quits reports whether a command is the panel leaving its window.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// Attaching from a phone shrinks the window and the panel would hold its 48
// columns, leaving the work pane almost none. It leaves instead. The first size
// is exempt: a panel opened by hand in an already narrow window stays, the same
// way the key overrides the hook.
func TestWatchQuitsWhenWindowGetsNarrow(t *testing.T) {
	// Opened by hand in a 54-column window: stays.
	m := watchTestModel(40, 20)
	narrowFirst, cmd := m.applyResizeWith(40, 54, 2)
	if quits(cmd) {
		t.Error("quit on the first size — a hand-opened panel should stay")
	}
	if narrowFirst.winWidth != 54 {
		t.Errorf("winWidth = %d, want the narrow window adopted", narrowFirst.winWidth)
	}

	// A phone attaches to a window that was wide: leave — but only once the
	// narrow window has been seen twice. One reading is what a session switch
	// produces mid-relayout, and quitting on it closed panels during an ordinary
	// switch.
	wide := watchTestModel(48, 20)
	wide.winWidth, wide.targetWidth, wide.winPanes = 269, 48, 2
	once, cmd := wide.applyResizeWith(48, 54, 2)
	if quits(cmd) {
		t.Error("quit on a single narrow reading")
	}
	if _, cmd := once.applyResizeWith(48, 54, 2); !quits(cmd) {
		t.Error("did not quit after the window stayed narrow")
	}

	// Still wide enough: hold the width as before, do not leave. Expressed
	// against the bar itself so it follows when the threshold moves.
	if _, cmd := wide.applyResizeWith(52, wide.minWidth(), 2); quits(cmd) {
		t.Error("quit on a window that is exactly the minimum")
	}
}

// The bar the panel leaves below is resolved once at startup, so a model that
// never got the chance has to behave as it always did rather than as if every
// window were wide enough.
func TestWatchMinWidthFallsBackToTheDefault(t *testing.T) {
	if got := (watchModel{}).minWidth(); got != tmux.DefaultMinWindowWidth {
		t.Errorf("unresolved minWidth = %d, want %d", got, tmux.DefaultMinWindowWidth)
	}
	if got := (watchModel{minWindowWidth: 96}).minWidth(); got != 96 {
		t.Errorf("resolved minWidth = %d, want 96", got)
	}

	// And the resolved bar is what a resize is judged against, not the default.
	m := watchTestModel(48, 20)
	m.minWindowWidth = 96
	m.winWidth, m.targetWidth, m.winPanes = 269, 48, 2
	if _, cmd := m.applyResizeWith(48, 100, 2); quits(cmd) {
		t.Error("quit at 100 columns with the bar set to 96")
	}
	narrow, cmd := m.applyResizeWith(48, 90, 2)
	if quits(cmd) {
		t.Error("quit on a single reading at 90 columns")
	}
	if _, cmd := narrow.applyResizeWith(48, 90, 2); !quits(cmd) {
		t.Error("did not quit at 90 columns with the bar set to 96")
	}
}

// One panel per window means switching sessions puts you in front of a panel
// that has never been told anything, and what it opens on is the only thing it
// says about where you are. So it opens on the session it lives in — the same
// row it marks ◀.
func TestWatchAutoSelectPrefersOwnSession(t *testing.T) {
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
			name:     "this pane's own session is also the top row",
			own:      "project",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "project",
		},
		{
			// The row order follows elapsed time, so "where you are" is regularly
			// not the top row — and it is still what the cursor opens on.
			name:     "own session is further down the list",
			own:      "api",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "api",
		},
		{
			name:     "only this session exists",
			own:      "project",
			sessions: []tmux.Session{mk("project", time.Second)},
			want:     "project",
		},
		{
			// Not knowing must not be guessed at: the top row is the same thing
			// the panel picked before it could tell.
			name:     "own session unknown",
			own:      "",
			sessions: []tmux.Session{mk("project", time.Second), mk("api", time.Hour)},
			want:     "project",
		},
		{
			// The pane outlived its session, or was moved. Nothing to anchor on,
			// so the fallback has to hold rather than leave the cursor nowhere.
			name:     "own session is gone from the list",
			own:      "vanished",
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

// Auto-selection is what happens when nobody chose. A deliberate choice — here
// the session the panel lives in, but any of them — must survive the refresh
// two seconds later.
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

// The panel reached 188 of 237 columns and stayed there, leaving 48 to work in.
// "Same window, different pane width" reads as a drag, but it is also what a
// pane appearing or dying beside the panel looks like — and adopting that once
// is enough to make it permanent, since the width is then saved and enforced.
func TestWatchWidthRejectsRunawayGrowth(t *testing.T) {
	m := watchTestModel(48, 20)
	m.winWidth, m.targetWidth, m.winPanes = 237, 48, 2

	grown, cmd := m.applyResizeWith(188, 237, 2)
	if grown.targetWidth != 48 {
		t.Errorf("target = %d, want the runaway rejected and 48 kept", grown.targetWidth)
	}
	if cmd == nil {
		t.Error("the panel was left squeezing the work pane instead of being put back")
	}

	// Exactly half is still the user's to take — the ceiling is a limit, not a
	// margin below it.
	half, _ := m.applyResizeWith(118, 237, 2)
	if half.targetWidth != 118 {
		t.Errorf("target = %d, want half the window adopted", half.targetWidth)
	}

	// One column over is not.
	over, _ := m.applyResizeWith(119, 237, 2)
	if over.targetWidth != 48 {
		t.Errorf("target = %d, want a width past half rejected", over.targetWidth)
	}
}

// A width remembered in a roomier window, or saved before the ceiling existed,
// must not survive being reopened somewhere it no longer fits.
func TestWatchWidthCorrectsAnOversizeFirstSize(t *testing.T) {
	m := watchTestModel(188, 20)

	corrected, cmd := m.applyResizeWith(188, 237, 2)
	if corrected.targetWidth != 118 {
		t.Errorf("target = %d, want it clamped to half the window", corrected.targetWidth)
	}
	if cmd == nil {
		t.Error("an oversize panel was accepted on sight")
	}

	// An ordinary first size is still adopted as-is.
	normal, cmd := watchTestModel(48, 20).applyResizeWith(48, 237, 2)
	if normal.targetWidth != 48 {
		t.Errorf("target = %d, want the opening width adopted", normal.targetWidth)
	}
	if cmd != nil {
		t.Error("an in-band first size scheduled a correction")
	}
}

// The ceiling must not swallow the case the lower clamp already covers: a pane
// squeezed below what the panel can render is left alone, not enforced.
func TestWatchWidthStillIgnoresSqueezeWithCeiling(t *testing.T) {
	m := watchTestModel(48, 20)
	m.winWidth, m.targetWidth, m.winPanes = 237, 48, 2

	squeezed, cmd := m.applyResizeWith(1, 237, 2)
	if squeezed.targetWidth != 48 {
		t.Errorf("target = %d, want the squeeze rejected", squeezed.targetWidth)
	}
	if cmd != nil {
		t.Error("a transient squeeze scheduled a resize")
	}
}

// Leaving takes two narrow readings in a row, and it is the *in a row* that
// matters: a session switch re-lays-out every window tmux touches, so a window
// can report a width it does not keep. Quitting on one closed panels during an
// ordinary switch and left the user without a sidebar and without a keyboard —
// `mux nav` sends keys to a pane that is gone and exits 0, so nothing said why.
func TestWatchNeedsTwoNarrowReadingsInARow(t *testing.T) {
	m := watchTestModel(48, 20)
	m.winWidth, m.targetWidth, m.winPanes = 269, 48, 2

	// Narrow, then wide again: the tally resets and the panel stays.
	narrow, cmd := m.applyResizeWith(48, 54, 2)
	if quits(cmd) {
		t.Fatal("quit on the first narrow reading")
	}
	recovered, cmd := narrow.applyResizeWith(48, 269, 2)
	if quits(cmd) {
		t.Fatal("quit on a wide reading")
	}
	if _, cmd := recovered.applyResizeWith(48, 54, 2); quits(cmd) {
		t.Error("a wide reading did not clear the count — one narrow reading quit again")
	}
}

// A width of zero is not a narrow window, it is tmux mid-relayout. Counting it
// would let two busy moments close the panel.
func TestWatchIgnoresANonWidth(t *testing.T) {
	m := watchTestModel(48, 20)
	m.winWidth, m.targetWidth, m.winPanes = 269, 48, 2

	zeroed, cmd := m.applyResizeWith(48, 0, 2)
	if quits(cmd) {
		t.Fatal("quit on a zero width")
	}
	if _, cmd := zeroed.applyResizeWith(48, 0, 2); quits(cmd) {
		t.Error("two zero widths quit — a non-width must not count toward leaving")
	}
}

// attachedSess is a session with a client sitting in it.
func attachedSess(name string, st tmux.AIState, attached bool) tmux.Session {
	s := sess(name, st)
	s.Attached = attached
	return s
}

// load drives the real per-tick path, which is where the cursor is decided.
func load(m watchModel, sessions []tmux.Session) watchModel {
	updated, _ := m.Update(sessionsLoadedMsg{sessions: sessions})
	return updated.(watchModel)
}

// You move the cursor to another session in order to *leave*. Coming back, the
// panel used to still be pointing at the session you went to — ◀ saying "you are
// here" while the highlight said somewhere else. Arriving puts it back.
func TestWatchReanchorsOnArrival(t *testing.T) {
	m := watchTestModel(48, 20)
	m.ownSession = "mux"

	// Sitting in mux: the cursor opens on it.
	m = load(m, []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, true),
		attachedSess("herdr", tmux.AIStateReady, false),
	})
	if m.selected != "mux" {
		t.Fatalf("selected = %q on arrival, want mux", m.selected)
	}

	// Pick herdr and leave: the client is now elsewhere.
	m = m.selectSession("herdr")
	m = load(m, []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, false),
		attachedSess("herdr", tmux.AIStateReady, true),
	})
	if m.selected != "herdr" {
		t.Errorf("selected = %q while away, want the cursor left alone", m.selected)
	}

	// Come back.
	m = load(m, []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, true),
		attachedSess("herdr", tmux.AIStateReady, false),
	})
	if m.selected != "mux" {
		t.Errorf("selected = %q after coming back, want mux — ◀ and the highlight disagree", m.selected)
	}
}

// The arrival, not the state. "My session is attached" stays true the whole time
// you sit in it, so acting on the level would drag the cursor back two seconds
// after every M-Up.
func TestWatchKeepsTheCursorYouMovedWhileHere(t *testing.T) {
	here := []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, true),
		attachedSess("herdr", tmux.AIStateReady, false),
	}

	m := watchTestModel(48, 20)
	m.ownSession = "mux"
	m = load(m, here)

	m = m.selectSession("herdr")
	for i := 0; i < 3; i++ {
		m = load(m, here)
		if m.selected != "herdr" {
			t.Fatalf("tick %d: selected = %q — the cursor was dragged back while the user was still here", i, m.selected)
		}
	}
}

// A panel in a session nobody is in has no arrival to react to.
func TestWatchLeavesTheCursorAloneWhileDetached(t *testing.T) {
	away := []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, false),
		attachedSess("herdr", tmux.AIStateReady, true),
	}

	m := watchTestModel(48, 20)
	m.ownSession = "mux"
	m = load(m, away)
	m = m.selectSession("herdr")
	m = load(m, away)

	if m.selected != "herdr" {
		t.Errorf("selected = %q, want the cursor untouched while no client is here", m.selected)
	}
}

// "Could not tell" must not be guessed at, the same way autoSelect refuses to.
func TestWatchDoesNotReanchorWithoutAnOwnSession(t *testing.T) {
	m := watchTestModel(48, 20)
	m.ownSession = ""

	m = load(m, []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, true),
		attachedSess("herdr", tmux.AIStateReady, false),
	})
	m = m.selectSession("herdr")
	m = load(m, []tmux.Session{
		attachedSess("mux", tmux.AIStateWorking, true),
		attachedSess("herdr", tmux.AIStateReady, false),
	})

	if m.selected != "herdr" {
		t.Errorf("selected = %q, want the choice kept when the panel cannot tell where it lives", m.selected)
	}
}

// The bug the pane count exists for. A pane closing beside the panel hands it
// the freed columns without the window changing size, which is indistinguishable
// from a drag if the width is all you look at. In a 237-column window with two
// panes that lands on exactly 118 — half — and half is not *over* half, so the
// ceiling let it through, the width was saved to disk and then actively held.
// Observed in the field: panel.json read {"width": 118}.
func TestWatchWidthRejectsAWidthHandedOverByAClosingPane(t *testing.T) {
	m := watchTestModel(36, 20)
	m.winWidth, m.targetWidth, m.winPanes = 237, 36, 3

	grown, cmd := m.applyResizeWith(118, 237, 2) // a pane went away
	if grown.targetWidth != 36 {
		t.Errorf("target = %d, want 36 kept — the columns were tmux's doing", grown.targetWidth)
	}
	if cmd == nil {
		t.Error("the panel kept half the window instead of being put back")
	}
	if grown.winPanes != 2 {
		t.Errorf("winPanes = %d, want the new count recorded", grown.winPanes)
	}
}

// The other half of the same rule: a pane *appearing* is not a drag either.
func TestWatchWidthIgnoresAResizeFromASplit(t *testing.T) {
	m := watchTestModel(36, 20)
	m.winWidth, m.targetWidth, m.winPanes = 237, 36, 2

	split, cmd := m.applyResizeWith(60, 237, 3)
	if split.targetWidth != 36 {
		t.Errorf("target = %d, want the split ignored", split.targetWidth)
	}
	if cmd == nil {
		t.Error("a width tmux chose during a split was left in place")
	}
}

// And the rule must not swallow real drags: same window, same panes, a width
// the user chose — including exactly half, which the ceiling deliberately
// allows.
func TestWatchWidthStillAcceptsADragAtTheSamePaneCount(t *testing.T) {
	m := watchTestModel(36, 20)
	m.winWidth, m.targetWidth, m.winPanes = 237, 36, 2

	dragged, cmd := m.applyResizeWith(118, 237, 2)
	if dragged.targetWidth != 118 {
		t.Errorf("target = %d, want the drag adopted", dragged.targetWidth)
	}
	if cmd == nil {
		t.Error("a real drag was not remembered")
	}
}

// 한 곳에서 드래그한 폭을 다른 패널들도 따라가야 한다. 세션마다 제각각이던
// 동작을 바꾼 것이라, 따라가는지와 "따라간 것이 드래그로 오해되지 않는지"를
// 같이 고정한다.
func TestAdoptSavedWidthFollowsTheLastDrag(t *testing.T) {
	// 실제 ~/.config/mux/panel.json 을 건드리지 않도록 격리한다.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := tmux.SavePanelWidth(60); err != nil {
		t.Fatalf("SavePanelWidth: %v", err)
	}

	m := watchModel{winWidth: 250, winPanes: 2, targetWidth: 36}
	adopted, cmd := m.adoptSavedWidth()
	if adopted.targetWidth != 60 {
		t.Errorf("target = %d, want the saved 60", adopted.targetWidth)
	}
	if cmd == nil {
		t.Error("adopting did not schedule a resize")
	}

	// 이미 그 폭이면 아무것도 하지 않는다 — 틱마다 pane 을 흔들면 안 된다.
	if _, cmd := adopted.adoptSavedWidth(); cmd != nil {
		t.Error("a panel already at the saved width scheduled a resize anyway")
	}
}

// 좁은 창은 천장에 걸려 깎인 폭을 쓰되, 그 값을 전역 저장본에 되쓰지 않아야
// 한다. 되쓰면 창 하나의 사정이 나머지 패널을 전부 좁힌다.
func TestAdoptSavedWidthClampsWithoutWritingBack(t *testing.T) {
	// 실제 ~/.config/mux/panel.json 을 건드리지 않도록 격리한다.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := tmux.SavePanelWidth(60); err != nil {
		t.Fatalf("SavePanelWidth: %v", err)
	}

	// 창이 90칸이면 천장은 45.
	m := watchModel{winWidth: 90, winPanes: 2, targetWidth: 36}
	clamped, cmd := m.adoptSavedWidth()
	if clamped.targetWidth != 45 {
		t.Errorf("target = %d, want the ceiling 45", clamped.targetWidth)
	}
	if cmd == nil {
		t.Error("clamped adoption did not schedule a resize")
	}

	// 그 리사이즈가 돌아오면 "같은 창, 다른 pane" 이라 드래그 분기로 들어간다.
	// 우리가 맞춘 폭 그대로이므로 기억할 것이 없어야 한다.
	settled, cmd := clamped.applyResizeWith(45, 90, 2)
	if cmd != nil {
		t.Error("landing on the width we asked for was remembered as a drag")
	}
	if settled.targetWidth != 45 {
		t.Errorf("target = %d, want it left at 45", settled.targetWidth)
	}
	if w := tmux.SavedPanelWidth(); w != 60 {
		t.Errorf("saved width is now %d — the clamp leaked into the global value", w)
	}
}

// The line saying what last happened sits inside its session's block, so it
// clicks to the same place as the row above it. Anything else would be a row
// that looks attached and goes somewhere else.
func TestSessionAtRowOnTheLastEventLine(t *testing.T) {
	now := time.Now()
	blocked := sess("api", tmux.AIStateApproval)
	blocked.AISince = now.Add(-time.Hour)
	blocked.AIWaitingFor = "Bash: git push"
	old := sess("mux", tmux.AIStateWorking)
	old.AISince = now.Add(-24 * time.Hour)

	m := watchTestModel(40, 30)
	m.sessions = []tmux.Session{old, blocked}
	m.selected = "api"
	m.events = []aiEvent{
		{at: now, session: "api", text: "❗ 승인 대기", state: tmux.AIStateApproval},
		{at: now.Add(-time.Minute), session: "mux", text: "✅ 작업 완료", state: tmux.AIStateReady},
	}

	for _, tc := range []struct {
		row  int
		want string
	}{
		{2, "api"}, // the session row
		{3, "api"}, // its blocked-on reason
		{4, "api"}, // its last event
		{5, "api"}, // spacer, still the same block
		{6, "mux"},
		{7, "mux"}, // mux's own last event
	} {
		if got := m.sessionAtRow(tc.row); got != tc.want {
			t.Errorf("sessionAtRow(%d) = %q, want %q", tc.row, got, tc.want)
		}
	}

	// mux's event belongs under mux, never inside api's block.
	for _, l := range m.sessionLines() {
		if l.session == "api" && strings.Contains(ansi.Strip(l.text), "✅ 작업 완료") {
			t.Errorf("mux's event was drawn inside api's block: %q", ansi.Strip(l.text))
		}
	}
}

// The cursor steps between sessions. Folding the history into its session's
// block is what keeps a dozen history rows from becoming a dozen extra stops.
func TestOpenHistoryAddsNoCursorStops(t *testing.T) {
	now := time.Now()
	m := watchTestModel(40, 30)
	m.sessions = []tmux.Session{sess("api", tmux.AIStateApproval), sess("mux", tmux.AIStateWorking)}
	m.events = []aiEvent{
		{at: now, session: "api", text: "❗ 승인 대기", state: tmux.AIStateApproval},
		{at: now.Add(-time.Minute), session: "api", text: "⏳ 작업 중", state: tmux.AIStateWorking},
	}

	closed := len(m.sessionOrder())
	m.selected = "api"
	if open := len(m.sessionOrder()); open != closed {
		t.Errorf("cursor stops = %d with a history open, %d without — want them equal", open, closed)
	}
	// Every session carries an event line now, not just the selected one, so the
	// folding has to hold for all of them.
	if got, want := len(m.sessionOrder()), len(m.sessions); got != want {
		t.Errorf("cursor stops = %d for %d sessions — event rows became stops", got, want)
	}
}
