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

// joinHorizontalFixed joins two blocks of text side-by-side, line by line
func joinHorizontalFixed(left, right string) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")

	maxLen := len(leftLines)
	if len(rightLines) > maxLen {
		maxLen = len(rightLines)
	}

	result := make([]string, maxLen)
	for i := 0; i < maxLen; i++ {
		l := ""
		r := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		result[i] = l + r
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
func rowBaseStyle(selected bool) lipgloss.Style {
	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCursor).
			Background(colorSelected)
	}
	return lipgloss.NewStyle().Foreground(colorListRow)
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
		if sg.color != nil {
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

// drawBorder wraps content lines with a rounded border
func drawBorder(content string, width, height int) string {
	innerWidth := width - 2
	lines := strings.Split(content, "\n")

	// Build bordered output
	result := make([]string, 0, height+2)

	// Top border
	result = append(result, "╭"+strings.Repeat("─", innerWidth)+"╮")

	// Content lines (pad/truncate to exactly height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		line = padOrTruncate(line, innerWidth)
		result = append(result, "│"+line+"│")
	}

	// Bottom border
	result = append(result, "╰"+strings.Repeat("─", innerWidth)+"╯")

	return lipgloss.NewStyle().
		Foreground(colorBorder).
		Render(strings.Join(result, "\n"))
}
