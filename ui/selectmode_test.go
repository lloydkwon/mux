package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Select mode is two things at once, and the second is the one that is easy to
// drop: handing the mouse back to the terminal is useless while a tick two
// seconds later redraws over the selection being made.
func TestSelectModeStopsTheTick(t *testing.T) {
	m := menuTestModel()

	updated, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	on := updated.(Model)
	if !on.selectMode {
		t.Fatal("v did not enter select mode")
	}
	if cmd == nil {
		t.Fatal("entering select mode issued no command; mouse reporting is still on")
	}

	// A tick arriving in select mode must not schedule the next one. Returning
	// no command is what freezes the screen.
	ticked, tickCmd := on.Update(tickMsg{})
	if !ticked.(Model).selectMode {
		t.Error("the tick left select mode")
	}
	if tickCmd != nil {
		t.Error("the tick rescheduled itself in select mode; the redraw will clear the selection")
	}

	// Leaving has to restart it, because nothing else is holding a tick.
	off, resumeCmd := ticked.(Model).updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if off.(Model).selectMode {
		t.Fatal("the second v did not leave select mode")
	}
	if resumeCmd == nil {
		t.Fatal("leaving select mode issued no command; the list would stay frozen")
	}
}

// esc leaves too. It is the key a user reaches for to get out of any mode, and
// getting out of this one is more urgent than the filter it otherwise clears.
func TestSelectModeEscapes(t *testing.T) {
	m := menuTestModel()
	m.selectMode = true
	m.filterText = "keep me"

	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.selectMode {
		t.Error("esc did not leave select mode")
	}
	if got.filterText != "keep me" {
		t.Error("esc cleared the filter as well as leaving select mode")
	}
}

// A frozen list with nothing to explain it reads as mux having hung, so the bar
// has to say both that the refresh stopped and how to start it again.
func TestSelectModeAnnouncesItself(t *testing.T) {
	m := menuTestModel()
	m.selectMode = true
	bar := m.extraBar()
	if bar == "" {
		t.Fatal("select mode drew no bar")
	}
	for _, want := range []string{"copy", "paused", "esc"} {
		if !strings.Contains(bar, want) {
			t.Errorf("bar %q does not mention %q", bar, want)
		}
	}
}
