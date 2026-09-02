package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// noteTestModel puts the cursor on a session row that already carries a note.
func noteTestModel(note string) Model {
	m := Model{
		tree:     newTreeState(),
		prefs:    defaultPreferences(),
		sessions: []tmux.Session{{Name: "news", Note: note}},
	}
	m.applyFilter()
	for i, it := range m.items {
		if it.kind == itemSession {
			m.cursor = i
			break
		}
	}
	return m
}

func TestNoteKeyOpensPrefilled(t *testing.T) {
	m := noteTestModel("라벨링 후 진행")
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got := updated.(Model)
	if got.mode != modeNote {
		t.Fatalf("mode after m = %v, want note", got.mode)
	}
	// Pre-filled because the list row cuts a long note to whatever the column
	// has left — this prompt is the only place the whole thing can be read.
	if v := got.noteModel.input.Value(); v != "라벨링 후 진행" {
		t.Errorf("input = %q, want the session's current note", v)
	}
	if got.noteModel.sessionName != "news" {
		t.Errorf("sessionName = %q, want news", got.noteModel.sessionName)
	}
}

// The key is a no-op on the two action rows and on window/pane rows, the same
// way r and x are: there is no session there to attach a note to.
func TestNoteKeyIgnoresNonSessionRows(t *testing.T) {
	m := noteTestModel("")
	m.cursor = 0
	if m.items[0].kind != itemNewShell {
		t.Fatalf("row 0 is %v, want the New shell action", m.items[0].kind)
	}
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if got := updated.(Model).mode; got != modeList {
		t.Errorf("mode = %v on an action row, want list", got)
	}
}

func TestNoteEscapeLeavesWithoutWriting(t *testing.T) {
	m := noteTestModel("before")
	opened, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	closed, cmd := opened.(Model).updateNote(tea.KeyMsg{Type: tea.KeyEsc})
	if got := closed.(Model).mode; got != modeList {
		t.Errorf("mode after esc = %v, want list", got)
	}
	// esc is the wrapper's job and emits no completion message, so nothing is
	// written and nothing reloads.
	if cmd != nil {
		t.Error("esc issued a command; it must not write or reload")
	}
}

// The note lives on the tmux session, so the completion handler has no
// preference to write and no Orders-style map to fix up — it reloads and gets
// out of the way.
func TestSessionNoteMsgReloadsAndFocuses(t *testing.T) {
	m := noteTestModel("")
	m.mode = modeNote
	updated, cmd := m.Update(sessionNoteMsg{sessionName: "news", note: "메모"})
	got := updated.(Model)
	if got.mode != modeList {
		t.Errorf("mode = %v, want list", got.mode)
	}
	if got.focusSession != "news" {
		t.Errorf("focusSession = %q, want news", got.focusSession)
	}
	if cmd == nil {
		t.Error("no reload was issued; the row would keep the old note until the next tick")
	}
}

func TestNoteViewMentionsClearing(t *testing.T) {
	view := newNoteModel("news", "").View()
	if !strings.Contains(view, "clear") {
		t.Errorf("view does not say how to remove a note: %q", view)
	}
}
