package ui

import "github.com/charmbracelet/lipgloss"

// The terminal's own scheme is the palette.
//
// mux names roles; the scheme fills them in. A light scheme supplies colours its
// author picked for a light background and a dark one supplies colours picked
// for a dark background, so mux never has to guess which it is looking at — and
// there is nothing to configure.
//
// It used to hardcode hex, all of it chosen against a dark terminal. Measured on
// GitHub Light (#F6F8FA), the colour every list row's text was drawn in reached
// 2.4:1 and the accent 1.7:1, against a 4.5:1 floor for body text. The screen
// was not ugly so much as barely there.
//
// Deliberately *not* lipgloss.AdaptiveColor: that resolves by asking the
// terminal for its background over OSC 11, and `mux watch` runs inside a tmux
// pane where the answer may never come — which would leave the panel drawn in
// the opposite palette from the TUI beside it. An ANSI index has nothing to ask.
var (
	colorAccent  = lipgloss.ANSIColor(6) // cyan
	colorSuccess = lipgloss.ANSIColor(2) // green
	colorDanger  = lipgloss.ANSIColor(1) // red
	colorPrimary = lipgloss.ANSIColor(5) // magenta
	colorMuted   = lipgloss.ANSIColor(8) // bright black
	colorBorder  = lipgloss.ANSIColor(8)

	// AI live-state colours. No highlighted-row variants: a selected row is
	// drawn in reverse video, which has no background of its own to be legible
	// against — see rowBaseStyle.
	colorStateWorking  = colorAccent
	colorStateApproval = colorDanger
	colorStateReady    = colorSuccess

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	borderStyle = lipgloss.NewStyle().
			Foreground(colorBorder)

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
