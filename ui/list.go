package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	indentWindow = 2
	indentPane   = 4

	// rowGutter is what every row spends before its name: the chevron and one
	// space. It used to be twelve cells — chevron, an attached marker, a
	// four-wide order label nobody was using, and the elapsed time — all of it
	// in front of the one thing you actually read. The marker became the name's
	// colour, the order label now appears only when a session has one, and the
	// elapsed time moved behind the name where it reads as belonging to it.
	rowGutter = 2

	// orderWidth is the "#3 " label's cell count. The column is only spent when
	// some session in view carries an order.
	orderWidth = 3

	// The name column's bounds. It takes what is left after the gutter and the
	// tail, so on a wide list the name gets the room and on a narrow one the
	// branch yields first.
	sessionNameMin = 12
	sessionNameMax = 28

	// sessionTailReserve is what the age, the badge and their separators hold
	// back for themselves: "  12m  ✅".
	sessionTailReserve = 2 + elapsedWidth + 2 + badgeWidth
)

// The action rows' labels. Constants because the column sizes itself around
// them: a second copy of either string would let the width and the row drift,
// and the row would be the one that got cut.
const (
	actionNewShellLabel   = "New shell"
	actionNewSessionLabel = "New tmux session"
)

// listOffset is the index of the first item row drawn: the list scrolls only as
// far as it must to keep the cursor on screen.
//
// A function rather than two lines inside the renderer because the click map
// needs the same number — a row the user can see has to be a row they can hit,
// and a second copy of this arithmetic is how the two start disagreeing.
func listOffset(cursor, innerHeight int) int {
	if innerHeight > 0 && cursor >= innerHeight {
		return cursor - innerHeight + 1
	}
	return 0
}

// renderListView renders the flattened tree (sessions + expanded windows + panes)
// as exactly innerHeight lines of exactly innerWidth cells.
//
// Unframed: the caller draws one frame around both columns, so this returns the
// inside of the left one.
func renderListView(items []listItem, cursor int, filter string, t *treeState, innerWidth, innerHeight int) string {
	if len(items) == 0 {
		msg := "No tmux sessions found"
		if filter != "" {
			msg = fmt.Sprintf("No match: \"%s\"", filter)
		}
		lines := make([]string, innerHeight)
		mid := innerHeight / 2
		for i := range lines {
			if i == mid {
				lines[i] = helpStyle.Render(padOrTruncate(centerText(fitCells(msg, innerWidth), innerWidth), innerWidth))
			} else {
				lines[i] = strings.Repeat(" ", innerWidth)
			}
		}
		return strings.Join(lines, "\n")
	}

	offset := listOffset(cursor, innerHeight)
	// Both decided across the whole list rather than the visible slice: a column
	// that appeared and vanished as you scrolled would move every name with it.
	showOrder := anyOrdered(items)
	nameWidth := sessionNameWidth(items, innerWidth, showOrder)

	lines := make([]string, innerHeight)
	for i := 0; i < innerHeight; i++ {
		idx := i + offset
		if idx < len(items) {
			lines[i] = formatItemRow(items[idx], idx == cursor, showOrder, nameWidth, innerWidth, t)
		} else {
			lines[i] = strings.Repeat(" ", innerWidth)
		}
	}
	return strings.Join(lines, "\n")
}

// renderSessionList preserves the legacy session-only renderer for tests and
// callers that don't need tree expansion. It wraps each session in a listItem
// and delegates to renderListView with an empty tree state.
func renderSessionList(sessions []tmux.Session, cursor int, filter string, innerWidth, innerHeight int) string {
	items := make([]listItem, len(sessions))
	for i := range sessions {
		items[i] = listItem{kind: itemSession, session: &sessions[i]}
	}
	state := newTreeState()
	return renderListView(items, cursor, filter, &state, innerWidth, innerHeight)
}

// sessionNameWidth is the column every name shares: as wide as the longest one,
// no wider than what is left after the gutter, the age, the badge and a branch
// worth reading.
//
// Sized to the content rather than fixed because a fixed column leaves a hole
// between a short name and its age — and the hole is where the eye has to jump.
func sessionNameWidth(items []listItem, innerWidth int, showOrder bool) int {
	longest := 0
	for _, it := range items {
		var label string
		switch it.kind {
		case itemSession:
			label = it.session.Name
		case itemNewShell:
			label = actionNewShellLabel
		case itemNewSession:
			label = actionNewSessionLabel
		default:
			continue
		}
		if w := ansi.StringWidth(label); w > longest {
			longest = w
		}
	}
	order := 0
	if showOrder {
		order = orderWidth
	}
	room := innerWidth - rowGutter - order - sessionTailReserve - minBranchWidth
	return clampInt(min(longest, room), sessionNameMin, sessionNameMax)
}

// anyOrdered reports whether any session in the list carries a pinned order.
func anyOrdered(items []listItem) bool {
	for _, it := range items {
		if it.kind == itemSession && it.order > 0 {
			return true
		}
	}
	return false
}

func formatItemRow(it listItem, selected, showOrder bool, nameWidth, width int, t *treeState) string {
	switch it.kind {
	case itemNewShell:
		return formatActionRow("○", actionNewShellLabel, "leave tmux", selected, nameWidth, width)
	case itemNewSession:
		return formatActionRow("+", actionNewSessionLabel, "create and attach", selected, nameWidth, width)
	case itemWindow:
		expanded := t.isWindowExpanded(it.session.Name, it.window.Index)
		return formatWindowRow(it.window, expanded, selected, width)
	case itemPane:
		return formatPaneRow(it.pane, selected, width)
	default:
		expanded := t.isSessionExpanded(it.session.Name)
		return formatSessionRow(*it.session, it.order, expanded, selected, showOrder, nameWidth, width)
	}
}

// formatActionRow renders the two rows above the sessions. The icon sits in the
// chevron's column and the name starts where every session name starts, so the
// top of the list reads as one column rather than two layouts stacked.
func formatActionRow(icon, name, description string, selected bool, nameWidth, width int) string {
	// The name shares the sessions' column, so the description lands where a
	// session's age does and the top of the list reads as one table.
	segs := []rowSeg{
		{text: icon + " ", color: colorAccent},
		{text: fitCells(name, nameWidth), color: colorAccent},
		{text: "  " + description, color: colorMuted, atom: true},
	}
	return renderRow(segs, width, selected)
}

// formatSessionRow renders "▶ name        12m  ✅        ⌥ branch".
//
// The name leads, because it is what the eye looks for. Everything after it is
// context in a fixed column so the list can be scanned down rather than read
// across, and the branch is flush right for the same reason.
//
// showOrder keeps the "#3" column out of the way of lists that never use it —
// the four blank cells it used to spend on every row were the single biggest
// piece of the old gutter.
func formatSessionRow(s tmux.Session, order int, expanded, selected, showOrder bool, nameWidth, width int) string {
	chevron := "▶"
	if expanded {
		chevron = "▼"
	}

	orderLabel := ""
	if showOrder {
		orderLabel = strings.Repeat(" ", orderWidth)
		if order > 0 {
			orderLabel = fmt.Sprintf("#%-*d", orderWidth-1, order)
		}
	}

	name := fitCells(s.Name, nameWidth)

	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}
	// One cell short of the width: renderRow pads the leftover at the end, and
	// that leftover is the right margin.
	used := rowGutter + ansi.StringWidth(orderLabel) + nameWidth + sessionTailReserve
	if room := width - used - 1; room < ansi.StringWidth(branch) {
		if room >= minBranchWidth {
			branch = fitCells(branch, room)
		} else {
			branch = ""
		}
	}
	gap := width - used - ansi.StringWidth(branch)
	if gap < 0 {
		gap = 0
	}

	glyph, _ := aiGlyph(s)
	segs := []rowSeg{
		{text: chevron + " ", color: colorMuted},
		{text: orderLabel, color: colorMuted},
		// Attached is the name's colour rather than a marker column: one row in
		// the list is the one you are in, and a whole cell of every other row
		// was being spent to say so.
		{text: name, color: nameColor(s)},
		{text: "  " + timeAgo(sessionAge(s)), color: aiStateColor(s.AIState)},
		// Always emitted, blank when there is no AI CLI, so the columns after it
		// line up whether or not a row has a badge.
		{text: "  " + padOrTruncate(glyph, badgeWidth), color: aiBadgeColor(s)},
		{text: strings.Repeat(" ", gap)},
		{text: branch, color: colorMuted},
	}
	return renderRow(segs, width, selected)
}

// nameColor marks the session this client is attached to. Nil means the
// terminal's own foreground, which is what every other name gets.
func nameColor(s tmux.Session) lipgloss.TerminalColor {
	if !s.Attached {
		return nil
	}
	return colorAccent
}

// formatWindowRow renders a window under its session: indented to the session's
// name column, and dimmer, because it is detail rather than the list.
//
// The active one is marked by colour like an attached session, for the same
// reason — a marker column costs every row a cell to say something about one.
func formatWindowRow(w *tmux.Window, expanded, selected bool, width int) string {
	chevron := "▶"
	if expanded {
		chevron = "▼"
	}

	label := fmt.Sprintf("%d:%s", w.Index, fitName(w.Name))
	segs := []rowSeg{
		{text: strings.Repeat(" ", indentWindow) + chevron + " ", color: colorMuted},
		{text: label, color: activeColor(w.Active, nil)},
	}
	return renderRow(segs, width, selected)
}

func formatPaneRow(p *tmux.Pane, selected bool, width int) string {
	label := fmt.Sprintf("%d %s", p.Index, fitName(p.Command))
	segs := []rowSeg{
		{text: strings.Repeat(" ", indentPane), color: colorMuted},
		{text: label, color: activeColor(p.Active, colorMuted)},
	}
	return renderRow(segs, width, selected)
}

// fitName trims a window or pane label to the display cap.
func fitName(name string) string {
	if ansi.StringWidth(name) > maxSessionNameDisplay {
		return fitCells(name, maxSessionNameDisplay)
	}
	return name
}

// activeColor tints the active window or pane. base is what an inactive one
// gets, and nil there means the terminal's own foreground.
func activeColor(active bool, base lipgloss.TerminalColor) lipgloss.TerminalColor {
	if active {
		return colorAccent
	}
	return base
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
