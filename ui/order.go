package ui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type orderModel struct {
	input       textinput.Model
	sessionName string
	err         error
}

type sessionOrderMsg struct {
	sessionName string
	order       int
}

func newOrderModel(sessionName, firstDigit string) orderModel {
	input := textinput.New()
	input.Placeholder = "0 clears"
	input.SetValue(firstDigit)
	input.Focus()
	input.CharLimit = 6
	input.Width = 12

	return orderModel{input: input, sessionName: sessionName}
}

func (m orderModel) Update(msg tea.Msg) (orderModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		value := m.input.Value()
		order, err := strconv.Atoi(value)
		if err != nil || order < 0 {
			m.err = fmt.Errorf("enter a non-negative number")
			return m, nil
		}
		return m, func() tea.Msg {
			return sessionOrderMsg{sessionName: m.sessionName, order: order}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m orderModel) View() string {
	s := inputLabelStyle.Render("Set Session Order") + "\n\n"
	s += inputLabelStyle.Render("Session: ") + m.sessionName + "\n"
	s += inputLabelStyle.Render("Order:   ") + m.input.View() + "\n\n"
	s += helpStyle.Render("enter save • 0 clear • esc cancel")
	if m.err != nil {
		s += "\n" + errorStyle.Render(m.err.Error())
	}
	return s
}
