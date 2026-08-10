package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func renderPreview(item *listItem, captured string, width, height int, tokenUsage *tmux.TokenUsage) string {
	innerWidth := width - 2
	innerHeight := height - 2

	if item == nil {
		lines := make([]string, innerHeight)
		mid := innerHeight / 2
		msg := "No session selected"
		for i := range lines {
			if i == mid {
				lines[i] = padOrTruncate(centerText(msg, innerWidth), innerWidth)
			} else {
				lines[i] = strings.Repeat(" ", innerWidth)
			}
		}
		content := strings.Join(lines, "\n")
		return drawBorder(content, width, innerHeight)
	}
	if item.session == nil {
		return renderActionPreview(item.kind, width, innerWidth, innerHeight)
	}

	session := item.session
	// Header: build text first, append styled suffixes after padding
	// to prevent ANSI codes and ambiguous-width icons from clipping.
	badge := aiLabelPlain(*session)

	branchInfo := ""
	branchStyled := ""
	if session.GitBranch != "" {
		prefix := "⌥"
		if session.IsWorktree {
			prefix = "⌥⌥"
		}
		branchText := prefix + " " + session.GitBranch
		branchInfo = "  " + branchText
		branchStyled = "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(branchText)
	}

	label := previewLabel(item)
	headerText := fmt.Sprintf("[ %s ]  %s", label, shortenPath(session.Directory))
	// Measure in cells, not bytes: every icon here is multi-byte, and byte
	// length would over-subtract and float the badge left of the edge.
	headerWidth := innerWidth - ansi.StringWidth(badge.text) - ansi.StringWidth(branchInfo)
	if headerWidth < 10 {
		headerWidth = 10
	}
	headerPadded := padOrTruncate(headerText, headerWidth)

	headerStyled := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(headerPadded) + badge.styled + branchStyled
	separator := lipgloss.NewStyle().Foreground(colorBorder).Render(
		strings.Repeat("─", innerWidth))

	// Status line (optional): Claude state on the left, token cost on the right
	statusLine := formatStatusLine(*session, tokenUsage, innerWidth)

	// Available lines for content (minus header + separator + optional status line)
	headerLines := 2
	if statusLine != "" {
		headerLines = 3
	}
	contentLines := innerHeight - headerLines
	if contentLines < 1 {
		contentLines = 1
	}

	capLines := strings.Split(captured, "\n")
	// Keep last N lines (most recent output)
	if len(capLines) > contentLines {
		capLines = capLines[len(capLines)-contentLines:]
	}

	// Build all lines: header, [token], separator, then content
	allLines := make([]string, innerHeight)
	lineIdx := 0
	allLines[lineIdx] = headerStyled
	lineIdx++
	if statusLine != "" {
		allLines[lineIdx] = statusLine
		lineIdx++
	}
	allLines[lineIdx] = separator
	lineIdx++
	for i := 0; i < contentLines; i++ {
		if i < len(capLines) {
			allLines[lineIdx+i] = padOrTruncate(capLines[i], innerWidth)
		} else {
			allLines[lineIdx+i] = strings.Repeat(" ", innerWidth)
		}
	}

	content := strings.Join(allLines, "\n")
	return drawBorder(content, width, innerHeight)
}

func renderActionPreview(kind itemKind, width, innerWidth, innerHeight int) string {
	title := "New shell"
	description := "Close mux and continue in the current SSH login shell."
	if os.Getenv("TMUX") != "" {
		description = "Detach this tmux client and continue in the outer login shell."
	}
	if kind == itemNewSession {
		title = "New tmux session"
		description = "Create a named tmux session, optionally choose its directory, and attach."
	}

	lines := make([]string, innerHeight)
	for i := range lines {
		lines[i] = strings.Repeat(" ", innerWidth)
	}
	if innerHeight > 1 {
		lines[0] = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(
			padOrTruncate("[ "+title+" ]", innerWidth))
		lines[1] = lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", innerWidth))
	}
	if innerHeight > 3 {
		lines[3] = padOrTruncate(description, innerWidth)
	}
	if innerHeight > 5 {
		lines[5] = helpStyle.Render(padOrTruncate("Press Enter to continue.", innerWidth))
	}
	return drawBorder(strings.Join(lines, "\n"), width, innerHeight)
}

// previewLabel returns the header label for the previewed target. For session
// rows it's just the session name; for windows/panes it appends the
// hierarchy and command/window name.
func previewLabel(it *listItem) string {
	switch it.kind {
	case itemWindow:
		return fmt.Sprintf("%s · %d:%s", it.session.Name, it.window.Index, it.window.Name)
	case itemPane:
		return fmt.Sprintf("%s · %d.%d %s", it.session.Name, it.window.Index, it.pane.Index, it.pane.Command)
	default:
		return it.session.Name
	}
}

// labelInfo holds both the styled and plain-text versions of a badge. The
// plain text exists so callers can measure the badge without counting ANSI.
type labelInfo struct {
	text   string // plain text for width calculation (e.g. "  ✦ claude")
	styled string // ANSI-styled version for display
}

// aiLabelPlain builds the preview header badge. It uses the same glyph rule as
// the list: the live-state glyph when the tool publishes one, the tool's own
// icon otherwise.
func aiLabelPlain(s tmux.Session) labelInfo {
	tool, ok := tmux.SessionAITool(s)
	if !ok {
		return labelInfo{}
	}
	glyph, _ := aiGlyph(s)
	text := "  " + glyph + " " + tool.Name
	// The preview panel is never a selected row, so the base tint always fits.
	style := lipgloss.NewStyle().Foreground(aiBadgeColor(s, false)).Bold(true)
	return labelInfo{text: text, styled: "  " + style.Render(glyph+" "+tool.Name)}
}

// formatStatusLine renders the row below the preview header: the AI CLI's live
// state and how long it has held it on the left, token usage and cost on the
// right. Returns "" when the session has neither, leaving the caller's line
// budget untouched.
//
// The state text is laid out first and the token cluster is dropped when they
// cannot both fit — on a narrow preview, why the tool is blocked matters more
// than what it has cost.
func formatStatusLine(s tmux.Session, u *tmux.TokenUsage, width int) string {
	left := aiStatusText(s, width)

	right := ""
	if u != nil {
		right = fmt.Sprintf("%s in / %s out  ~$%.2f  ",
			tmux.FormatTokens(u.InputTokens),
			tmux.FormatTokens(u.OutputTokens),
			u.TotalCost)
	}

	if left == "" && right == "" {
		return ""
	}

	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 2 {
		right = ""
		gap = width - ansi.StringWidth(left)
	}
	if gap < 0 {
		gap = 0
	}

	muted := lipgloss.NewStyle().Foreground(colorMuted)
	stateStyle := muted
	if c := aiStateColor(s.AIState, false); c != nil {
		stateStyle = lipgloss.NewStyle().Foreground(c)
	}

	return stateStyle.Render(left) + strings.Repeat(" ", gap) + muted.Render(right)
}

func shortenPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}
	}
	if len(path) > maxPathDisplay {
		path = "..." + path[len(path)-maxPathDisplay+3:]
	}
	return path
}
