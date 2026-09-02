package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

type noteModel struct {
	input       textinput.Model
	sessionName string
	err         error
}

type sessionNoteMsg struct {
	sessionName string
	note        string
}

// newNoteModel opens the editor on the note the session already has.
//
// Pre-filling is not just a convenience: the list row cuts a long note to
// whatever the column has left, so this prompt is the only place the whole
// thing can be read.
func newNoteModel(sessionName, current string) noteModel {
	input := textinput.New()
	input.Placeholder = "empty clears"
	input.SetValue(current)
	input.CursorEnd()
	input.Focus()
	input.CharLimit = tmux.MaxNoteLen
	input.Width = 40

	return noteModel{input: input, sessionName: sessionName}
}

// Update writes on enter. The write happens here rather than in the completion
// handler for the reason renameModel calls tmux.RenameSession here: a tmux
// failure has to be shown on the prompt that caused it, with what the user typed
// still in front of them.
func (m noteModel) Update(msg tea.Msg) (noteModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		note := m.input.Value()
		if err := tmux.SetSessionNote(m.sessionName, note); err != nil {
			m.err = err
			return m, nil
		}
		return m, func() tea.Msg {
			return sessionNoteMsg{sessionName: m.sessionName, note: note}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m noteModel) View() string {
	s := inputLabelStyle.Render("Session Note") + "\n\n"
	s += inputLabelStyle.Render("Session: ") + m.sessionName + "\n"
	s += inputLabelStyle.Render("Note:    ") + m.input.View() + "\n\n"
	s += helpStyle.Render("enter save • empty clears • esc cancel")
	if m.err != nil {
		s += "\n" + errorStyle.Render(m.err.Error())
	}
	return s
}
