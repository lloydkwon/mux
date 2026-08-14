package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// padOrTruncate ensures a string is exactly `width` visible characters
func padOrTruncate(s string, width int) string {
	w := ansi.StringWidth(s)
	if w > width {
		return ansi.Truncate(s, width, "")
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// fixedBox takes rendered content and forces it to exactly width x height visible area.
// It splits by newlines, truncates/pads each line to width, and truncates/pads to height lines.
func fixedBox(content string, width, height int) string {
	lines := strings.Split(content, "\n")

	result := make([]string, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			result[i] = padOrTruncate(lines[i], width)
		} else {
			result[i] = strings.Repeat(" ", width)
		}
	}
	return strings.Join(result, "\n")
}

// rowSeg is one span of a list row. Segments carry plain text plus an optional
// color, applied per-span.
//
// Spans exist because a nested lipgloss.Render emits its own ESC[0m, which
// resets the *background* too — so a colored segment in the middle of a row
// would strip the selection highlight from everything after it. Each span
// instead re-states the full style, background included.
type rowSeg struct {
	text  string
	color lipgloss.TerminalColor // nil uses the row's base color
	atom  bool                   // drop entirely rather than truncate
}

// rowBaseStyle returns the style every segment builds on.
//
// A selected row is reverse video and nothing else: the terminal swaps its own
// foreground and background, so the highlight is legible in any scheme without
// mux picking either colour. An unselected row sets no colour at all, leaving
// the terminal's foreground — which is the one colour guaranteed to be readable
// on its background.
func rowBaseStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().Reverse(true)
	}
	return lipgloss.NewStyle()
}

// renderRow concatenates segments into exactly width measured cells, padding
// the remainder so a selected row's highlight reaches the right edge.
// Segments marked atom are skipped whole rather than cut in half.
func renderRow(segs []rowSeg, width int, selected bool) string {
	base := rowBaseStyle(selected)

	var b strings.Builder
	remaining := width
	for _, sg := range segs {
		if remaining <= 0 {
			break
		}
		text := sg.text
		w := ansi.StringWidth(text)
		if w > remaining {
			if sg.atom {
				continue
			}
			text = ansi.Truncate(text, remaining, "")
			w = ansi.StringWidth(text)
		}
		style := base
		// Colours are dropped on a selected row, not merely overridden. Under
		// reverse video a foreground is painted as the *background*, so a
		// coloured span would come out as a green or red block sitting in the
		// middle of the highlight. The row inverts as one piece; the state
		// glyphs (⏳❗✅) still say what the colour would have.
		if sg.color != nil && !selected {
			style = base.Foreground(sg.color)
		}
		b.WriteString(style.Render(text))
		remaining -= w
	}
	if remaining > 0 {
		b.WriteString(base.Render(strings.Repeat(" ", remaining)))
	}
	return b.String()
}

// clampInt constrains v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// fitCells pads or truncates s to exactly width cells, marking truncation with
// "...". Unlike a byte slice this never cuts a multi-byte rune in half.
func fitCells(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return padOrTruncate(s, width)
	}
	if width <= 3 {
		return ansi.Truncate(s, width, "")
	}
	return padOrTruncate(ansi.Truncate(s, width-3, "")+"...", width)
}

// drawFrame wraps the two columns in a single frame with one divider between
// them.
//
// Two separate boxes put two border characters side by side in the middle of the
// screen, which reads as a seam rather than a division. One frame says the same
// thing in half the ink, and the divider lines up with the corners.
//
// Both blocks must already be exactly their width and their height. The frame
// does not pad or clip: a renderer returning the wrong size is a bug that should
// show as a broken frame rather than as content quietly cut off.
func drawFrame(left, right string, leftWidth, rightWidth, height int) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")

	rows := make([]string, 0, height+2)
	rows = append(rows, "╭"+strings.Repeat("─", leftWidth)+"┬"+strings.Repeat("─", rightWidth)+"╮")
	for i := 0; i < height; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		rows = append(rows, edge()+padOrTruncate(l, leftWidth)+edge()+padOrTruncate(r, rightWidth)+edge())
	}
	rows = append(rows, "╰"+strings.Repeat("─", leftWidth)+"┴"+strings.Repeat("─", rightWidth)+"╯")

	// Only the horizontal rules are rendered as one block; the verticals are
	// styled per character because the content between them carries its own
	// colours, and a style wrapping the whole row would reset them.
	rows[0] = borderStyle.Render(rows[0])
	rows[len(rows)-1] = borderStyle.Render(rows[len(rows)-1])
	return strings.Join(rows, "\n")
}

// edge is one vertical frame character, styled on its own so it cannot reset the
// colours of the row it sits beside.
func edge() string { return borderStyle.Render("│") }
