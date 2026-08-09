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

	// Claude state colors. The -Sel variants are lighter tints: the base red
	// only reaches ~3:1 contrast against colorSelected, which is too low to
	// read on a highlighted row.
	colorStateWorking     = lipgloss.Color("#22D3EE")
	colorStateApproval    = lipgloss.Color("#EF4444")
	colorStateReady       = lipgloss.Color("#22C55E")
	colorStateWorkingSel  = lipgloss.Color("#67E8F9")
	colorStateApprovalSel = lipgloss.Color("#FCA5A5")
	colorStateReadySel    = lipgloss.Color("#86EFAC")

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
