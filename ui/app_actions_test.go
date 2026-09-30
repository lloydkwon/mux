package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

func menuTestModel() Model {
	m := Model{
		tree:  newTreeState(),
		prefs: defaultPreferences(),
		sessions: []tmux.Session{
			{Name: "news"},
		},
	}
	m.applyFilter()
	return m
}

func TestNewShellActionQuitsWithoutAttach(t *testing.T) {
	t.Setenv("TMUX", "")
	m := menuTestModel()
	if m.items[0].kind != itemNewShell {
		t.Fatalf("first item = %v, want new shell", m.items[0].kind)
	}
	updated, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("new shell should quit the TUI")
	}
	if got.AttachName() != "" {
		t.Fatalf("attach target = %q, want empty", got.AttachName())
	}
	if got.DetachRequested() {
		t.Fatal("outside tmux should not request detach")
	}
}

func TestNewShellActionRequestsDetachInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,123,0")
	m := menuTestModel()
	updated, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("new shell should quit the TUI")
	}
	if !got.DetachRequested() {
		t.Fatal("inside tmux should request client detach")
	}
}

func TestNewSessionActionCreatesInAttachMode(t *testing.T) {
	m := menuTestModel()
	m.cursor = 1
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.mode != modeCreate || !got.createModel.attach {
		t.Fatalf("mode = %v attach = %v", got.mode, got.createModel.attach)
	}
}

func TestCreatedSessionInAttachModeQuitsWithTarget(t *testing.T) {
	m := menuTestModel()
	updated, cmd := m.Update(sessionCreatedMsg{name: "fresh", attach: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("created session should quit for attachment")
	}
	if got.AttachName() != "fresh" || got.AttachWindowIndex() != -1 || got.AttachPaneIndex() != -1 {
		t.Fatalf("attach target = %#v", got.attachTarget)
	}
}

// `mux new` and the panel's "새 세션" row open the same screen `n` opens in the
// list — the point of the subcommand is that there is no second create flow.
func TestNewSessionModelOpensOnTheCreatePrompt(t *testing.T) {
	m := NewSessionModel()
	if m.mode != modeCreate {
		t.Errorf("mode = %v, want create", m.mode)
	}
	if !m.createModel.attach {
		t.Error("a session created this way should be attached to")
	}

	// And cancelling leaves the ordinary list behind, exactly as cancelling `n`
	// does — nothing about the entry point changes what esc means.
	cancelled, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := cancelled.(Model).mode; got != modeList {
		t.Errorf("mode after esc = %v, want list", got)
	}
}

func TestDigitStartsOrderInputForSession(t *testing.T) {
	m := menuTestModel()
	m.cursor = 2
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	got := updated.(Model)
	if got.mode != modeOrder {
		t.Fatalf("mode = %v, want order", got.mode)
	}
	if got.orderModel.sessionName != "news" || got.orderModel.input.Value() != "1" {
		t.Fatalf("order model = %#v", got.orderModel)
	}
}

// 첫 목록이 오면 커서는 첫 세션 행에 선다 — "New shell"(행 0)이 아니라. 부트스트랩
// 팝업에서 그 행의 enter 는 detach 라, 목록이 뜨자마자 enter 를 치면 tmux 밖으로 떨어졌다.
func TestFirstLoadLandsOnFirstSession(t *testing.T) {
	m := Model{tree: newTreeState(), prefs: defaultPreferences(), width: 120, height: 30}
	updated, _ := m.Update(sessionsLoadedMsg{sessions: []tmux.Session{
		{Name: "alpha"}, {Name: "beta"},
	}})
	got := updated.(Model)
	if it := got.currentItem(); it == nil || it.kind != itemSession || it.session.Name != "alpha" {
		t.Fatalf("cursor on %+v, want the first session row", it)
	}
	// 두 번째 로드는 커서를 건드리지 않는다 — 사용자가 옮긴 자리를 뺏으면 안 된다.
	got.cursor = firstSessionIndex(got.items) + 1
	updated, _ = got.Update(sessionsLoadedMsg{sessions: []tmux.Session{{Name: "alpha"}, {Name: "beta"}}})
	again := updated.(Model)
	if it := again.currentItem(); it == nil || it.session == nil || it.session.Name != "beta" {
		t.Fatalf("second load moved the cursor to %+v", it)
	}
}
