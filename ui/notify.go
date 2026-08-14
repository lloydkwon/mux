package ui

import (
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// maxEvents caps the transition log. Mirrors my-mux's MAX_EVENTS.
	maxEvents = 20

	// notifyMinWidth is the narrowest pane the panel can say anything useful in.
	// Below it the columns collide, so the pane shows a notice instead.
	notifyMinWidth = 24
)

// aiEvent is one observed AI state transition.
type aiEvent struct {
	at      time.Time
	session string
	text    string
	state   tmux.AIState
}

// detectTransitions compares this refresh's sessions against the previous
// states and returns the events worth reporting, plus the state map to keep.
//
// It is pure so the rules can be tested without a Model. The rules mirror
// my-mux's detector, which is the behavior the user already reads:
//
//   - A session seen for the first time only gets recorded. Otherwise starting
//     mux would dump one event per running session.
//   - Entering Approval is always news: that is the state that blocks on you.
//   - Reaching Ready from any other live state is "turn finished". Only None is
//     excluded — that is a session appearing mid-turn, not a turn ending. The
//     test is deliberately "not None" rather than a list of the states a turn
//     can pass through, so a state added to AIState later still ends a turn
//     instead of silently dropping the completion event.
//   - Every other transition is silent. The list badge already says a session
//     is busy, and a line per state change would drown the two that matter.
func detectTransitions(prev map[string]tmux.AIState, sessions []tmux.Session, now time.Time) ([]aiEvent, map[string]tmux.AIState) {
	next := make(map[string]tmux.AIState, len(sessions))
	var events []aiEvent

	for _, s := range sessions {
		before, seen := prev[s.Name]
		next[s.Name] = s.AIState
		if !seen || before == s.AIState {
			continue
		}

		switch {
		case s.AIState == tmux.AIStateApproval:
			text := s.AIState.Icon() + " " + aiStateLabel(s.AIState)
			if s.AIWaitingFor != "" {
				text += " · " + s.AIWaitingFor
			}
			events = append(events, aiEvent{at: now, session: s.Name, text: text, state: s.AIState})
		case s.AIState == tmux.AIStateReady && before != tmux.AIStateNone:
			events = append(events, aiEvent{at: now, session: s.Name,
				text: s.AIState.Icon() + " " + aiStateLabel(s.AIState), state: s.AIState})
		}
	}

	// Sessions that vanished drop out of next, so a returning name is treated as
	// new rather than as a transition from whatever it held before.
	return events, next
}

// aiStateLabel names a live state in the panel's own words.
//
// The panel speaks Korean throughout — its heading, its separators, its
// degraded notices — while AIState.String() is the English the TUI and
// `mux status` print. Both halves of the panel go through here so the detail
// column and the event log two rows below it cannot disagree about what to call
// the same state.
func aiStateLabel(st tmux.AIState) string {
	switch st {
	case tmux.AIStateWorking:
		return "작업 중"
	case tmux.AIStateApproval:
		return "승인 대기"
	case tmux.AIStateReady:
		return "작업 완료"
	case tmux.AIStateShell:
		return "셸 실행 중"
	default:
		return ""
	}
}

// pushEvents prepends new events and trims to maxEvents, newest first.
func pushEvents(log []aiEvent, fresh []aiEvent) []aiEvent {
	if len(fresh) == 0 {
		return log
	}
	out := make([]aiEvent, 0, len(log)+len(fresh))
	for i := len(fresh) - 1; i >= 0; i-- { // 같은 tick 안에서도 나중 것이 위로
		out = append(out, fresh[i])
	}
	out = append(out, log...)
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}
	return out
}

// notifyLine is one rendered row. session names the tmux session a click on
// this row should switch to, and is empty for rows that are not a session.
//
// The owner travels with the line rather than being recomputed from a copy of
// the layout loop: an approval row is followed by an extra reason line, so any
// second walk of that loop desyncs the moment another conditional row is added.
type notifyLine struct {
	text    string
	session string
}

// notifyLines builds the single-column panel: sessions on top, recent
// transitions below. Every line is exactly width cells. Returns nil when there
// is nothing to report, so callers can tell "empty" from "a box of blanks".
//
// The two-column panel calls notifySessionLines and notifyEventLines directly,
// because the halves live in different columns there.
func notifyLines(sessions []tmux.Session, events []aiEvent, width int, selected string) []notifyLine {
	lines := notifySessionLines(sessions, width, selected)
	if len(lines) == 0 && len(events) == 0 {
		return nil
	}
	// A section break, not session spacing: this blank carries no session, so it
	// separates the two halves without stretching the last session's click block.
	// Skipped when there are no sessions, or it would double up with the blank
	// already sitting under the heading.
	if len(lines) > 0 {
		lines = append(lines, notifyLine{text: blankRow(width)})
	}
	return append(lines, notifyEventLines(events, width)...)
}

// notifySessionLines builds the session half: sessions running an AI CLI first,
// then everything else. Returns nil when there are no sessions at all.
//
// selected names the session whose block is drawn highlighted; "" highlights
// nothing.
//
// No border: the tmux pane it fills already draws one.
//
// Blank lines are layout, not decoration. A session's trailing blank carries
// that session, which is what makes a click target two rows tall.
//
// Glyphs and colors come from aiGlyph/aiStateColor, the same deciders the list
// uses — a second mapping here would drift from the rows it sits next to.
func notifySessionLines(sessions []tmux.Session, width int, selected string) []notifyLine {
	if width <= 0 {
		return nil
	}

	// Two groups, because they answer different questions. The top group is what
	// the panel is for — who is working, who is blocked. The rest is there so
	// every session is reachable without opening the TUI, and it stays below the
	// fold.
	var ai, other []tmux.Session
	for _, s := range sessions {
		if _, ok := tmux.SessionAITool(s); ok {
			ai = append(ai, s)
		} else {
			other = append(other, s)
		}
	}
	if len(ai) == 0 && len(other) == 0 {
		return nil
	}
	sortByDisplayedAge(ai)
	sortByDisplayedAge(other)

	blank := notifyLine{text: blankRow(width)}
	lines := []notifyLine{
		{text: helpKeyStyle.Render(padOrTruncate(" 🔔 AI 세션", width))},
		// Air under the title so it reads as a heading, not the first row.
		blank,
	}
	if len(ai) == 0 {
		// Say it rather than letting the next heading imply it: a heading with
		// nothing under it reads as a panel that failed to load.
		lines = append(lines, notifyLine{text: helpStyle.Render(padOrTruncate("  없음", width))})
	}
	lines = append(lines, sessionBlocks(ai, width, selected, false)...)

	if len(other) > 0 {
		lines = append(lines, blank,
			notifyLine{text: helpStyle.Render(padOrTruncate(" ── 세션", width))}, blank)
		lines = append(lines, sessionBlocks(other, width, selected, true)...)
	}
	return lines
}

// notifyEventLines builds the transition log half.
func notifyEventLines(events []aiEvent, width int) []notifyLine {
	lines := []notifyLine{{text: helpStyle.Render(padOrTruncate(" ── 최근 이벤트", width))}}
	if len(events) == 0 {
		return append(lines, notifyLine{text: helpStyle.Render(padOrTruncate(" 아직 없음", width))})
	}
	for _, e := range events {
		lines = append(lines, notifyLine{text: notifyEventLine(e, width)})
	}
	return lines
}

// sortByDisplayedAge orders sessions by the same value their rows print, most
// recent first.
//
// Sorting by anything else makes the elapsed column non-monotonic — a session at
// 41m listed below two at 3h reads as broken, which is exactly what sorting by
// creation time produced. Rows do move when a state changes; the block-sized
// click target is what keeps that from misfiring.
func sortByDisplayedAge(ss []tmux.Session) {
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := sessionAge(ss[i]), sessionAge(ss[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		return ss[i].Name < ss[j].Name
	})
}

// sessionBlocks renders one group: a row per session, an indented reason line
// under a blocked one, and a spacer between blocks.
//
// dim marks the second group, whose rows are context rather than the point.
func sessionBlocks(ss []tmux.Session, width int, selected string, dim bool) []notifyLine {
	var lines []notifyLine
	for i, s := range ss {
		sel := s.Name == selected
		lines = append(lines, notifyLine{
			text:    notifySessionLine(s, width, sel, dim),
			session: s.Name,
		})
		if s.AIState == tmux.AIStateApproval && s.AIWaitingFor != "" {
			// The reason is part of its session's block: anywhere in the block
			// clicks to the same session, which is simpler to predict than a
			// row that looks attached but does nothing.
			lines = append(lines, notifyLine{
				text: renderRow([]rowSeg{
					{text: fitCells("    "+s.AIWaitingFor, width), color: mutedUnless(sel)},
				}, width, sel),
				session: s.Name,
			})
		}
		// Between sessions only. The blank belongs to the session above it, which
		// is what makes a click target two rows tall; skipping it after the last
		// one keeps the list from trailing off into the next heading, at the cost
		// of the bottom session being a one-row target.
		if i < len(ss)-1 {
			lines = append(lines, notifyLine{text: renderRow(nil, width, sel), session: s.Name})
		}
	}
	return lines
}

// blankRow is a full-width empty row in the list's base style.
func blankRow(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(" ", width)
}

// mutedUnless returns the dim foreground, or nil once the row is selected —
// dimming a highlighted row fights the highlight it is drawn on.
func mutedUnless(selected bool) lipgloss.TerminalColor {
	if selected {
		return nil
	}
	return colorMuted
}

// notifyTexts flattens rendered rows for callers that do not care about clicks.
func notifyTexts(lines []notifyLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// minBranchWidth is the narrowest branch worth keeping on a session row: the
// glyph, a space, and enough letters to recognise. Below it the branch is
// dropped whole rather than shown as an ellipsis.
const minBranchWidth = 6

// notifySessionLine renders "⏳ name 3m            ⌥ branch" for one session.
//
// Name and age sit together on the left so the age reads as belonging to the
// name, and the branch is flush right so the column can be scanned vertically.
//
// A session running no AI CLI renders the same way with an empty badge — the
// padding is what keeps the branch in one column across both groups.
func notifySessionLine(s tmux.Session, width int, selected, dim bool) string {
	glyph, _ := aiGlyph(s)

	// The badge is padded to badgeWidth and framed by single spaces, because a
	// 2-cell state glyph would otherwise sit flush against the name where a
	// 1-cell tool icon gets a pad space — the two kinds of row must not
	// disagree.
	head := " " + padOrTruncate(glyph, badgeWidth) + " "
	// One cell short of the width: renderRow pads the leftover at the end, which
	// becomes the right margin, so a truncated branch never touches the edge.
	avail := width - ansi.StringWidth(head) - 1
	if avail < 1 {
		return renderRow(nil, width, selected)
	}

	age := " " + compactAgo(sessionAge(s))

	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}

	// The branch yields first: which session it is and how long it has held its
	// state are the point of the row, the branch is context. The reserved cell
	// keeps at least one space between the age and the branch when the branch
	// has been cut down to exactly the room left.
	name := s.Name
	if room := avail - ansi.StringWidth(name) - ansi.StringWidth(age) - 1; room < ansi.StringWidth(branch) {
		if room >= minBranchWidth {
			branch = fitCells(branch, room)
		} else {
			branch = ""
		}
	}
	// Only truncate the name when it genuinely overflows — fitCells always pads
	// to the width it is given, and padding here would reopen the gap between
	// the name and the age that this layout exists to close.
	if nameRoom := avail - ansi.StringWidth(age) - ansi.StringWidth(branch); ansi.StringWidth(name) > nameRoom {
		name = fitCells(name, nameRoom)
	}

	// renderRow pads what is left over at the very end, so the gap has to be an
	// explicit segment — otherwise the padding lands after the branch and the
	// right edge stops lining up.
	gap := avail - ansi.StringWidth(name) - ansi.StringWidth(age) - ansi.StringWidth(branch)
	if gap < 0 {
		gap = 0
	}

	// A row in the second group is context: it recedes unless it is the one
	// selected, where the highlight has to win over the dimming.
	faded := lipgloss.TerminalColor(nil)
	if dim {
		faded = mutedUnless(selected)
	}
	ageColor := aiStateColor(s.AIState, selected)
	if ageColor == nil {
		ageColor = faded
	}

	segs := []rowSeg{
		{text: head, color: aiBadgeColor(s, selected)},
		{text: name, color: faded},
		{text: age, color: ageColor},
		{text: strings.Repeat(" ", gap)},
		{text: branch, color: faded},
	}
	return renderRow(segs, width, selected)
}

// notifyEventLine renders "14:23:01 name ✅ 작업 완료" for one logged event.
//
// The name is not padded to a column: what happened should read as a sentence
// continuing from the session it happened to, and a fixed column strands the
// text far to the right of every short name.
func notifyEventLine(e aiEvent, width int) string {
	stamp := e.at.Format("15:04:05")
	head := " " + stamp + " "
	avail := width - ansi.StringWidth(head)
	if avail < 2 {
		return renderRow(nil, width, false)
	}

	// A very long name still has to leave the text room to say something, so it
	// gives up half the line — but only then.
	name := e.session
	if cap := avail / 2; ansi.StringWidth(name) > cap {
		name = fitCells(name, cap)
	}

	// Explicit width rather than letting renderRow truncate: it cuts without a
	// marker, so a clipped reason would read as the whole reason.
	textWidth := avail - ansi.StringWidth(name) - 1

	segs := []rowSeg{
		{text: head},
		{text: name},
		{text: " " + fitCells(e.text, textWidth), color: aiStateColor(e.state, false)},
	}
	return renderRow(segs, width, false)
}
