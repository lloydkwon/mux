package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// badgeWidth is the fixed cell width of the AI column. State glyphs are
	// Emoji_Presentation and measure 2; tool icons measure 1 and get padded to
	// match, so the branch column lines up across every row.
	badgeWidth = 2

	// elapsedWidth is the fixed gutter holding how long the row has held its
	// state.
	elapsedWidth = 4
)

// aiGlyph returns the session's AI marker: the live-state glyph when the tool
// publishes one, otherwise the tool's own icon. ok is false when the session
// runs no known AI CLI.
//
// One badge carries both facts. A separate state column would repeat what the
// tool icon already says, since only a detected AI CLI can have a state.
//
// The result is unpadded — the list pads it to badgeWidth, the preview header
// wants it tight against the tool name.
func aiGlyph(s tmux.Session) (string, bool) {
	tool, ok := tmux.SessionAITool(s)
	if !ok {
		return "", false
	}
	if g := s.AIState.Icon(); g != "" {
		return g, true
	}
	return tool.Icon, true
}

// branchGlyph returns the marker that precedes a session's git branch: doubled
// for a linked worktree, matching the legend the help page prints.
//
// One decider, for the same reason aiGlyph is one: the preview, the panel and
// the help legend all have to agree, and this had already drifted — the panel
// showed a plain ⌥ for worktrees the preview marked ⌥⌥.
//
// Both forms measure exactly what they draw (⌥ is pinned at one cell by
// TestGlyphWidthsAreStable), so a caller's width arithmetic holds either way.
func branchGlyph(s tmux.Session) string {
	if s.IsWorktree {
		return "⌥⌥"
	}
	return "⌥"
}

// aiStateColor returns the foreground for a live state, using a lighter tint
// when the row is selected so it stays legible against colorSelected. Returns
// nil for a session with no live state, meaning "use the row's base color".
func aiStateColor(st tmux.AIState, selected bool) lipgloss.TerminalColor {
	switch st {
	case tmux.AIStateWorking:
		if selected {
			return colorStateWorkingSel
		}
		return colorStateWorking
	case tmux.AIStateApproval:
		if selected {
			return colorStateApprovalSel
		}
		return colorStateApproval
	case tmux.AIStateReady:
		if selected {
			return colorStateReadySel
		}
		return colorStateReady
	default:
		return nil
	}
}

// aiBadgeColor colors the badge by state when there is one, falling back to the
// tool's own hex so ✦/◈/⬡/✧ keep the colors they have always had.
func aiBadgeColor(s tmux.Session, selected bool) lipgloss.TerminalColor {
	if c := aiStateColor(s.AIState, selected); c != nil {
		return c
	}
	if tool, ok := tmux.SessionAITool(s); ok {
		return lipgloss.Color(tool.Color)
	}
	return nil
}

// sessionAge returns the timestamp the list's elapsed column counts from.
//
// For a session with live AI state that is the state's age — "blocked for 3m"
// is the number worth acting on, where the session's creation age is inert.
// Other sessions keep showing their creation age, as they always have.
func sessionAge(s tmux.Session) time.Time {
	if s.AIState != tmux.AIStateNone && !s.AISince.IsZero() {
		return s.AISince
	}
	return s.Created
}

// aiStatusText renders the preview's state cluster, dropping detail to fit
// maxWidth. Returns "" when the session has no live AI state — the preview
// header already names the tool.
//
// The blocking reason outranks everything else it competes with: on a narrow
// preview the token counts go before the reason does.
func aiStatusText(s tmux.Session, maxWidth int) string {
	if s.AIState == tmux.AIStateNone {
		return ""
	}

	glyph := s.AIState.Icon()
	label := s.AIState.String()

	elapsed := ""
	if !s.AISince.IsZero() {
		elapsed = "  " + compactAgo(s.AISince)
	}

	candidates := []string{}
	if s.AIWaitingFor != "" {
		candidates = append(candidates,
			fmt.Sprintf("  %s %s · %s%s", glyph, label, s.AIWaitingFor, elapsed))
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
