package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/xguru/mux/tmux"
)

const (
	// stateGlyphWidth is the cell width every state glyph must measure. The
	// glyphs below are Emoji_Presentation, which ansi.StringWidth reports as 2
	// and terminals draw as 2. Any replacement must measure the same, because
	// drawBorder re-pads every line and no width compensation survives it.
	stateGlyphWidth = 2

	// stateCellWidth is the fixed list column: glyph + space + 4-cell elapsed.
	stateCellWidth = stateGlyphWidth + 1 + 4
)

// claudeGlyph returns the marker for a Claude state, or blank padding when
// there is no live Claude session. Always exactly stateGlyphWidth cells.
func claudeGlyph(st tmux.ClaudeState) string {
	switch st {
	case tmux.ClaudeStateWorking:
		return "⏳"
	case tmux.ClaudeStateApproval:
		return "❗"
	case tmux.ClaudeStateReady:
		return "✅"
	default:
		return strings.Repeat(" ", stateGlyphWidth)
	}
}

// claudeColor returns the foreground for a state, using a lighter tint when the
// row is selected so it stays legible against colorSelected. Returns nil for
// sessions with no Claude state, meaning "use the row's base color".
func claudeColor(st tmux.ClaudeState, selected bool) lipgloss.TerminalColor {
	switch st {
	case tmux.ClaudeStateWorking:
		if selected {
			return colorStateWorkingSel
		}
		return colorStateWorking
	case tmux.ClaudeStateApproval:
		if selected {
			return colorStateApprovalSel
		}
		return colorStateApproval
	case tmux.ClaudeStateReady:
		if selected {
			return colorStateReadySel
		}
		return colorStateReady
	default:
		return nil
	}
}

// claudeStateCell renders the fixed-width list column holding the state glyph
// and how long the session has held that state. It always returns exactly
// stateCellWidth cells.
//
// For a Claude session the time is the state's age — "blocked for 3m" is the
// number worth acting on, where the session's creation age is inert. Other
// sessions keep showing their creation age, as they always have.
func claudeStateCell(s tmux.Session, selected bool) (string, lipgloss.TerminalColor) {
	glyph := claudeGlyph(s.ClaudeState)

	when := s.Created
	if s.ClaudeState != tmux.ClaudeStateNone && !s.ClaudeSince.IsZero() {
		when = s.ClaudeSince
	}

	return glyph + " " + timeAgo(when), claudeColor(s.ClaudeState, selected)
}

// claudeStatusText renders the preview's state cluster, dropping detail to fit
// maxWidth. Returns "" when the session has no live Claude state.
//
// The blocking reason outranks everything else it competes with: on a narrow
// preview the token counts go before the reason does.
func claudeStatusText(s tmux.Session, maxWidth int) string {
	if s.ClaudeState == tmux.ClaudeStateNone {
		return ""
	}

	glyph := claudeGlyph(s.ClaudeState)
	label := s.ClaudeState.String()

	elapsed := ""
	if !s.ClaudeSince.IsZero() {
		elapsed = "  " + compactAgo(s.ClaudeSince)
	}

	candidates := []string{}
	if s.ClaudeWaitingFor != "" {
		candidates = append(candidates,
			fmt.Sprintf("  %s %s · %s%s", glyph, label, s.ClaudeWaitingFor, elapsed))
	}
	candidates = append(candidates,
		fmt.Sprintf("  %s %s%s", glyph, label, elapsed),
		fmt.Sprintf("  %s%s", glyph, elapsed),
		"  "+glyph,
	)

	for _, c := range candidates {
		if ansi.StringWidth(c) <= maxWidth {
			return c
		}
	}
	return ""
}
