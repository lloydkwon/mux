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

	for _, w := range []int{20, 40, 80} {
		for _, h := range []int{6, 12, 30} {
			m := watchTestModel(w, h)
			m.sessions = sessions

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

// Keys other than quit must be inert: this pane is usually not the focused one,
// and a stray key reaching it should not change what it shows.
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
func TestSessionAtRowWhenDegraded(t *testing.T) {
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

// Only a left press switches. Release repeats the button under SGR and drags
// arrive as motion — either one switching would fire on an accidental swipe.
func TestWatchOnlyLeftPressSwitches(t *testing.T) {
	m := watchTestModel(40, 20)
	m.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking)}

	// Row 0 is the heading and row 1 the blank under it; the first session is 2.
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd == nil {
		t.Error("left press on a session row did not switch")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}); cmd != nil {
		t.Error("release switched")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}); cmd != nil {
		t.Error("drag switched")
	}
	if _, cmd := m.Update(tea.MouseMsg{Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd != nil {
		t.Error("clicking the header switched")
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

	// Window unchanged, pane changed: a border drag. Adopt and remember it.
	m.winWidth, m.targetWidth = 150, 40
	dragged, cmd := m.applyResizeWith(60, 150)
	if cmd == nil {
		t.Error("a drag did not schedule a save")
	}
	if dragged.targetWidth != 60 {
		t.Errorf("target = %d after a drag, want 60", dragged.targetWidth)
	}

	// Window changed and the pane drifted: tmux re-laid it out, so undo it.
	relaid, cmd := dragged.applyResizeWith(66, 269)
	if cmd == nil {
		t.Error("a re-layout was not corrected")
	}
	if relaid.targetWidth != 60 {
		t.Errorf("target = %d after a re-layout, want it held at 60", relaid.targetWidth)
	}
	if relaid.winWidth != 269 {
		t.Errorf("winWidth = %d, want the new window size recorded", relaid.winWidth)
	}

	// The correction lands: same window, pane back on target, nothing to undo.
	settled, _ := relaid.applyResizeWith(60, 269)
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
