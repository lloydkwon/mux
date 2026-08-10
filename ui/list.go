package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lloydkwon/mux/tmux"
)

const (
	indentWindow = 2
	indentPane   = 4

	// Session row layout. The prefix is chevron + attached marker + order
	// label + the elapsed cell, plus their separators — a width-invariant
	// gutter, so the elapsed time survives at every panel width.
	sessionPrefixWidth = 1 + 1 + 1 + 1 + 4 + 1 + elapsedWidth + 1
	// floor: prefix + this + the badge cell exactly fills an 80-col panel, so
	// the badge is the last thing standing rather than the first thing cut.
	sessionNameMin     = 13
	sessionNameMax     = 24
	sessionTailReserve = 12 // room the AI icon and branch would like
)

// renderListView renders the flattened tree (sessions + expanded windows + panes).
// Items must already be flattened by the caller via flatten().
func renderListView(items []listItem, cursor int, filter string, t *treeState, width, height int) string {
	innerWidth := width - 2 // border chars
	innerHeight := height - 2

	if len(items) == 0 {
		msg := "No tmux sessions found"
		if filter != "" {
			msg = fmt.Sprintf("No match: \"%s\"", filter)
		}
		lines := make([]string, innerHeight)
		mid := innerHeight / 2
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

	offset := 0
	if cursor >= innerHeight {
		offset = cursor - innerHeight + 1
	}

	lines := make([]string, innerHeight)
	for i := 0; i < innerHeight; i++ {
		idx := i + offset
		if idx < len(items) {
			lines[i] = formatItemRow(items[idx], idx == cursor, innerWidth, t)
		} else {
			lines[i] = strings.Repeat(" ", innerWidth)
		}
	}

	content := strings.Join(lines, "\n")
	return drawBorder(content, width, innerHeight)
}

// renderSessionList preserves the legacy session-only renderer for tests and
// callers that don't need tree expansion. It wraps each session in a listItem
// and delegates to renderListView with an empty tree state.
func renderSessionList(sessions []tmux.Session, cursor int, filter string, width, height int) string {
	items := make([]listItem, len(sessions))
	for i := range sessions {
		items[i] = listItem{kind: itemSession, session: &sessions[i]}
	}
	state := newTreeState()
	return renderListView(items, cursor, filter, &state, width, height)
}

func formatItemRow(it listItem, selected bool, width int, t *treeState) string {
	switch it.kind {
	case itemNewShell:
		return formatActionRow("○", "New shell", "leave tmux", selected, width)
	case itemNewSession:
		return formatActionRow("+", "New tmux session", "create and attach", selected, width)
	case itemWindow:
		expanded := t.isWindowExpanded(it.session.Name, it.window.Index)
		return formatWindowRow(it.window, expanded, selected, width)
	case itemPane:
		return formatPaneRow(it.pane, selected, width)
	default:
		expanded := t.isSessionExpanded(it.session.Name)
		return formatSessionRow(*it.session, it.order, expanded, selected, width)
	}
}

func formatActionRow(icon, name, description string, selected bool, width int) string {
	text := fmt.Sprintf("  %s %-18s %s", icon, name, description)
	row := padOrTruncate(text, width)
	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCursor).
			Background(colorSelected).
			Render(row)
	}
	return lipgloss.NewStyle().Foreground(colorAccent).Render(row)
}

func formatSessionRow(s tmux.Session, order int, expanded, selected bool, width int) string {
	chevron := "▶"
	if expanded {
		chevron = "▼"
	}

	status := "○"
	if s.Attached {
		status = "*"
	}

	orderLabel := "    "
	if order > 0 {
		orderLabel = fmt.Sprintf("#%-3d", order)
	}

	nameWidth := clampInt(width-sessionPrefixWidth-sessionTailReserve,
		sessionNameMin, sessionNameMax)

	// The elapsed time takes the state's color: with the state glyph folded
	// into the AI badge, the color is what still says "this row is blocked".
	segs := []rowSeg{
		{text: fmt.Sprintf("%s %s %s ", chevron, status, orderLabel)},
		{text: timeAgo(sessionAge(s)), color: aiStateColor(s.AIState, selected)},
		{text: " " + fitCells(s.Name, nameWidth)},
	}
	// Always emit the badge cell, blank when there is no AI CLI, so the branch
	// column lines up whether or not a row has a badge.
	glyph, _ := aiGlyph(s)
	segs = append(segs, rowSeg{
		text:  " " + padOrTruncate(glyph, badgeWidth),
		color: aiBadgeColor(s, selected),
		atom:  true,
	})
	if s.GitBranch != "" {
		segs = append(segs, rowSeg{text: " " + s.GitBranch})
	}

	return renderRow(segs, width, selected)
}

func formatWindowRow(w *tmux.Window, expanded, selected bool, width int) string {
	chevron := "▶"
	if expanded {
		chevron = "▼"
	}
	marker := " "
	if w.Active {
		marker = "*"
	}

	name := w.Name
	if len(name) > maxSessionNameDisplay {
		name = name[:maxSessionNameDisplay-3] + "..."
	}

	text := fmt.Sprintf("%s%s %s %d:%s", strings.Repeat(" ", indentWindow), chevron, marker, w.Index, name)
	row := padOrTruncate(text, width)

	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCursor).
			Background(colorSelected).
			Render(row)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9CA3AF")).
		Render(row)
}

func formatPaneRow(p *tmux.Pane, selected bool, width int) string {
	marker := " "
	if p.Active {
		marker = "*"
	}
	cmd := p.Command
	if len(cmd) > maxSessionNameDisplay {
		cmd = cmd[:maxSessionNameDisplay-3] + "..."
	}

	text := fmt.Sprintf("%s%s %d %s", strings.Repeat(" ", indentPane), marker, p.Index, cmd)
	row := padOrTruncate(text, width)

	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCursor).
			Background(colorSelected).
			Render(row)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6B7280")).
		Render(row)
}

func centerText(s string, width int) string {
	pad := (width - len(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// timeAgo formats the age of t right-aligned in a 4-cell column.
func timeAgo(t time.Time) string {
	return fmt.Sprintf("%4s", compactAgo(t))
}

// compactAgo formats the age of t as "5s"/"12m"/"3h"/"2d", unpadded. Times in
// the future clamp to zero rather than rendering as a countdown.
func compactAgo(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
