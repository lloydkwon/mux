package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xguru/mux/tmux"
)

type createModel struct {
	nameInput textinput.Model
	dirInput  textinput.Model
	focused   int // 0=name, 1=dir
	attach    bool
	err       error
}

func newCreateModel(attach bool) createModel {
	name := textinput.New()
	name.Placeholder = "session-name"
	name.Focus()
	name.CharLimit = 50
	name.Width = 40

	dir := textinput.New()
	dir.Placeholder = "~/workspace (optional)"
	dir.CharLimit = 200
	dir.Width = 40

	return createModel{
		nameInput: name,
		dirInput:  dir,
		focused:   0,
		attach:    attach,
	}
}

type sessionCreatedMsg struct {
	name   string
	attach bool
}

func (m createModel) Update(msg tea.Msg) (createModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			if m.focused == 0 {
				m.focused = 1
				m.nameInput.Blur()
				m.dirInput.Focus()
			} else {
				m.focused = 0
				m.dirInput.Blur()
				m.nameInput.Focus()
			}
			return m, nil

		case "enter":
			name := m.nameInput.Value()
			if name == "" {
				return m, nil
			}
			dir := m.dirInput.Value()

			var err error
			if dir != "" {
				err = tmux.CreateSessionWithDir(name, dir)
			} else {
				err = tmux.CreateSession(name)
			}
			if err != nil {
				m.err = err
				return m, nil
			}
			return m, func() tea.Msg {
				return sessionCreatedMsg{name: name, attach: m.attach}
			}
		}
	}

	var cmd tea.Cmd
	if m.focused == 0 {
		m.nameInput, cmd = m.nameInput.Update(msg)
	} else {
		m.dirInput, cmd = m.dirInput.Update(msg)
	}
	return m, cmd
}

func (m createModel) View() string {
	s := inputLabelStyle.Render("New Session") + "\n\n"
	s += inputLabelStyle.Render("Name: ") + m.nameInput.View() + "\n"
	s += inputLabelStyle.Render("Dir:  ") + m.dirInput.View() + "\n\n"
	s += helpStyle.Render("tab switch • enter create • esc cancel")

	if m.err != nil {
		s += "\n" + errorStyle.Render(m.err.Error())
	}

	return s
}
