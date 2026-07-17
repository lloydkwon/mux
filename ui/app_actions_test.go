package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xguru/mux/tmux"
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
