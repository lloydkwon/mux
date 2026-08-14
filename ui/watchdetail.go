package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// detailGutterWidth is the vertical rule plus the space after it. The pane
	// draws no border of its own, so this line is the only thing separating the
	// two columns — drawBorder would cost two cells and two rows to say the same.
	detailGutterWidth = 2

	// detailEventBudget caps how many transitions the column shows. The log is
	// context here; the live output above it is the reason to look.
	detailEventBudget = 5

	// detailMinPreviewLines is how much output has to survive before the event
	// log is worth any rows at all. Below it the log yields entirely.
	detailMinPreviewLines = 3
)

// detailView is everything the panel's right column draws from.
//
// Bundled rather than passed as arguments because the column keeps growing
// facts — output, ownership, cost — and a seven-argument render call stops
// saying which value is which at the call site.
type detailView struct {
	session  *tmux.Session
	captured string
	events   []aiEvent

	// own marks the session this pane lives in. Its output is already on screen
	// in the pane beside the panel, so the column summarises it instead of
	// drawing a second copy.
	own   bool
	usage *tmux.TokenUsage
}

// render draws the column: which session is selected, what state it is in, and
// either the tail of what it is printing or — for this pane's own session — a
// summary of it.
//
// Returns exactly height lines of exactly width cells, because
// joinHorizontalFixed concatenates the two columns line by line and any
// disagreement shears the panel.
//
// This does not reuse renderPreview: that one takes a *listItem, draws its own
// border, and budgets width-2/height-2 for it. Here the tmux pane is the border.
func (d detailView) render(width, height int) string {
	inner := width - detailGutterWidth
	if inner < 1 || height < 1 {
		return fixedBox("", width, height)
	}

	if d.session == nil {
		return detailBox(detailCentered("세션을 고르세요", inner, height), width, height)
	}

	s := *d.session
	lines := []string{detailHeader(s, inner), detailStatus(s, inner), detailSeparator(inner)}

	// What is left after the header goes to the body, minus whatever the event
	// log takes. The log is the first thing to give up its rows.
	body := height - len(lines)
	eventRows := detailEventRows(d.events, body)
	bodyRows := body - eventRows
	if bodyRows < 0 {
		bodyRows = 0
	}

	if d.own {
		lines = append(lines, detailSelfCard(s, d.usage, inner, bodyRows)...)
	} else {
		lines = append(lines, detailCapture(d.captured, inner, bodyRows)...)
	}

	if eventRows > 0 {
		lines = append(lines, detailSeparator(inner))
		for _, e := range d.events[:eventRows-1] {
			lines = append(lines, notifyEventLine(e, inner))
		}
	}

	return detailBox(lines, width, height)
}

// detailSelfCard describes this pane's own session instead of mirroring it.
//
// Capturing it would draw a copy of the pane sitting right beside the panel —
// the same screen twice, for fifty columns. What is worth the space is what the
// pane itself does not say: where it is, how long it has been up, what it has
// cost.
//
// The notice goes first and is the last thing dropped: it is the line that
// explains why this column looks different from every other session's.
func detailSelfCard(s tmux.Session, usage *tmux.TokenUsage, inner, rows int) []string {
	if rows <= 0 {
		return nil
	}

	facts := []string{"", " " + shortenPath(s.Directory)}

	windows := fmt.Sprintf("창 %d", s.WindowCount)
	if !s.Created.IsZero() {
		windows += " · 가동 " + compactAgo(s.Created)
	}
	facts = append(facts, " "+windows)

	if usage != nil {
		facts = append(facts, fmt.Sprintf(" %s in / %s out  ~$%.2f",
			tmux.FormatTokens(usage.InputTokens),
			tmux.FormatTokens(usage.OutputTokens),
			usage.TotalCost))
	}

	// Everything after the notice yields from the bottom up, so a short column
	// keeps the path over the cost rather than the other way round.
	if len(facts) > rows-1 {
		facts = facts[:max(0, rows-1)]
	}

	out := make([]string, 0, rows)
	out = append(out, renderRow([]rowSeg{
		{text: fitCells(" 지금 이 창입니다 — 오른쪽이 그 화면입니다", inner), color: colorAccent},
	}, inner, false))
	for _, f := range facts {
		out = append(out, renderRow([]rowSeg{{text: f, color: colorMuted}}, inner, false))
	}
	for len(out) < rows {
		out = append(out, strings.Repeat(" ", inner))
	}
	return out[:rows]
}

// detailEventRows reports how many rows of the column the event log may take,
// counting its own separator. Zero means the log is dropped.
func detailEventRows(events []aiEvent, body int) int {
	if len(events) == 0 {
		return 0
	}
	want := len(events)
	if want > detailEventBudget {
		want = detailEventBudget
	}
	// +1 for the separator above the log.
	for want > 0 {
		if body-(want+1) >= detailMinPreviewLines {
			return want + 1
		}
		want--
	}
	return 0
}

// detailHeader is the session's name, with its branch flush right.
func detailHeader(s tmux.Session, inner int) string {
	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}
	return renderRow(fitRight(
		rowSeg{text: s.Name, color: colorAccent},
		rowSeg{text: branch, color: colorMuted},
		inner,
	), inner, false)
}

// detailStatus is the live-state cluster, or the session's directory when there
// is no state to report — something has to identify what is being previewed.
//
// Built here rather than through aiStatusText because that one prints
// AIState.String(), the English the TUI and `mux status` use. This column sits
// directly above the event log, which speaks the panel's Korean; the two
// naming the same state differently in one pane reads as a bug.
func detailStatus(s tmux.Session, inner int) string {
	if text := detailStateText(s, inner-1); text != "" {
		return renderRow([]rowSeg{
			{text: " " + text, color: aiStateColor(s.AIState, false)},
		}, inner, false)
	}
	return renderRow([]rowSeg{
		{text: " " + shortenPath(s.Directory), color: colorMuted},
	}, inner, false)
}

// detailStateText drops detail until the cluster fits, in the order the TUI's
// preview drops it: the blocking reason outranks the elapsed time, which
// outranks the label.
func detailStateText(s tmux.Session, width int) string {
	if s.AIState == tmux.AIStateNone {
		return ""
	}
	glyph, label := s.AIState.Icon(), aiStateLabel(s.AIState)

	elapsed := ""
	if !s.AISince.IsZero() {
		elapsed = "  " + compactAgo(s.AISince)
	}

	candidates := []string{}
	if s.AIWaitingFor != "" {
		candidates = append(candidates, glyph+" "+label+" · "+s.AIWaitingFor+elapsed)
	}
	candidates = append(candidates,
		glyph+" "+label+elapsed,
		glyph+elapsed,
		glyph,
	)
	for _, c := range candidates {
		if ansi.StringWidth(c) <= width {
			return c
		}
	}
	return ""
}

func detailSeparator(inner int) string {
	return lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", inner))
}

// detailCapture returns the last rows lines of captured output, padded out when
// there is less of it than there is room.
//
// The captured text carries raw ANSI from capture-pane -e, so it is clipped with
// the cell-aware padOrTruncate and never wrapped in a style — an outer style
// would reset the colors the pane drew.
func detailCapture(captured string, inner, rows int) []string {
	if rows <= 0 {
		return nil
	}
	var capLines []string
	// Trailing newlines are what capture-pane ends with, not output. Keeping
	// them spends the freshest rows — the ones the eye goes to — on blanks.
	if trimmed := strings.TrimRight(captured, "\n"); trimmed != "" {
		capLines = strings.Split(trimmed, "\n")
	}
	if len(capLines) > rows {
		capLines = capLines[len(capLines)-rows:]
	}

	out := make([]string, rows)
	for i := range out {
		if i < len(capLines) {
			out[i] = padOrTruncate(capLines[i], inner)
		} else {
			out[i] = strings.Repeat(" ", inner)
		}
	}
	return out
}

// detailCentered is the placeholder body, message on the middle row.
func detailCentered(msg string, inner, height int) []string {
	lines := make([]string, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", inner)
	}
	if height > 0 {
		lines[height/2] = helpStyle.Render(padOrTruncate(centerText(msg, inner), inner))
	}
	return lines
}

// detailBox prefixes every row with the column rule and forces the block to
// exactly width by height.
func detailBox(lines []string, width, height int) string {
	rule := lipgloss.NewStyle().Foreground(colorBorder).Render("│") + " "

	out := make([]string, height)
	inner := width - detailGutterWidth
	for i := range out {
		body := strings.Repeat(" ", inner)
		if i < len(lines) {
			body = padOrTruncate(lines[i], inner)
		}
		out[i] = rule + body
	}
	return strings.Join(out, "\n")
}

// fitRight lays a left and a right segment out across width cells, dropping the
// right one whole rather than showing a stub of it.
func fitRight(left, right rowSeg, width int) []rowSeg {
	leftText := " " + left.text
	rightText := right.text
	if rightText != "" {
		rightText += " "
	}

	gap := width - ansi.StringWidth(leftText) - ansi.StringWidth(rightText)
	if gap < 1 && rightText != "" {
		rightText = ""
		gap = width - ansi.StringWidth(leftText)
	}
	if gap < 0 {
		leftText = fitCells(leftText, width)
		gap = 0
	}

	return []rowSeg{
		{text: leftText, color: left.color},
		{text: strings.Repeat(" ", gap)},
		{text: rightText, color: right.color},
	}
}
