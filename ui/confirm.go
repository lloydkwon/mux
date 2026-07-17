package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xguru/mux/tmux"
)

type confirmKillModel struct {
	sessionName string
}

type sessionKilledMsg struct {
	name string
	err  error
}

func newConfirmKillModel(sessionName string) confirmKillModel {
	return confirmKillModel{sessionName: sessionName}
}

func (m confirmKillModel) Update(msg tea.Msg) (confirmKillModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			name := m.sessionName
			err := tmux.KillSession(name)
			return m, func() tea.Msg {
				return sessionKilledMsg{name: name, err: err}
			}
		default:
			// Any other key cancels
			return m, func() tea.Msg {
				return sessionKilledMsg{name: "", err: nil}
			}
		}
	}
	return m, nil
}

func (m confirmKillModel) View() string {
	return errorStyle.Render(
		fmt.Sprintf("Kill \"%s\"? (y/N)", m.sessionName))
}
