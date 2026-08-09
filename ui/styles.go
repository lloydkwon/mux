package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorPrimary  = lipgloss.Color("#7C3AED")
	colorAccent   = lipgloss.Color("#22D3EE")
	colorSuccess  = lipgloss.Color("#22C55E")
	colorDanger   = lipgloss.Color("#EF4444")
	colorMuted    = lipgloss.Color("#6B7280")
	colorBorder   = lipgloss.Color("#374151")
	colorSelected = lipgloss.Color("#312E81")
	colorCursor   = lipgloss.Color("#A78BFA")
	colorListRow  = lipgloss.Color("#9CA3AF")

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	inputLabelStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)
)
