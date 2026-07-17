package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOrderModelAcceptsMultipleDigits(t *testing.T) {
	m := newOrderModel("news", "1")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected order command")
	}
	msg, ok := cmd().(sessionOrderMsg)
	if !ok {
		t.Fatalf("message type = %T", cmd())
	}
	if msg.sessionName != "news" || msg.order != 12 {
		t.Fatalf("order message = %#v", msg)
	}
}

func TestOrderZeroClears(t *testing.T) {
	m := newOrderModel("news", "0")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd().(sessionOrderMsg)
	if msg.order != 0 {
		t.Fatalf("order = %d, want 0", msg.order)
	}
}
