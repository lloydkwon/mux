package ui

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// maxEvents caps the transition log. The number lives in tmux because that
	// is where the log itself lives now — the panel and the shared store must
	// agree on it, or a panel would draw rows the store has already dropped.
	maxEvents = tmux.MaxPanelEvents

	// notifyMinWidth is the narrowest pane the panel can say anything useful in.
	// Below it the columns collide, so the pane shows a notice instead.
	//
	// The number itself lives in tmux, which is the side that refuses to restore
	// a remembered width below it — the floor has to be the same on both ends of
	// that round trip, or a hand-edited panel.json opens a pane this renderer
	// immediately gives up on.
	notifyMinWidth = tmux.MinPanelWidth
)

// aiEvent is one observed AI state transition.
//
// since is the state's own start time, and it is what makes the shared log
// possible: at is this process's clock at the moment it noticed, which differs
// by up to a tick between panels, while since is a value they all read from the
// same file and therefore agree on exactly.
type aiEvent struct {
	at      time.Time
	session string
	text    string
	state   tmux.AIState
	since   time.Time
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
//   - Entering Working is logged too, which my-mux's history has always done and
//     this had not. It costs history depth — a turn is now two lines rather than
//     one, so the same fifty-entry log covers roughly half the time — and buys
//     the other end of every turn: without it the log says when work finished
//     and never when it started, so nothing in it has a duration. Unlike Ready
//     it is not gated on the previous state, because None → Working is a real
//     start: a tracked session that had no AI running now does.
//   - Every other transition is silent. Entering Shell is the one that matters
//     there: it is a detail of how a turn is being served, not a turn changing
//     hands, and it flaps.
func detectTransitions(prev map[string]aiSnapshot, sessions []tmux.Session, now time.Time) ([]aiEvent, map[string]aiSnapshot) {
	next := make(map[string]aiSnapshot, len(sessions))
	var events []aiEvent

	for _, s := range sessions {
		before, seen := prev[s.Name]
		next[s.Name] = aiSnapshot{state: s.AIState, since: s.AISince}
		if !seen {
			continue
		}

		switch {
		case s.AIState == tmux.AIStateApproval && (before.state != s.AIState || newPrompt(before, s)):
			text := s.AIState.Icon() + " " + aiStateLabel(s.AIState)
			if s.AIWaitingFor != "" {
				text += " · " + aiWaitingLabel(s.AIWaitingFor)
			}
			events = append(events, aiEvent{at: now, session: s.Name, text: text,
				state: s.AIState, since: s.AISince})
		case s.AIState == tmux.AIStateReady && before.state != s.AIState && before.state != tmux.AIStateNone:
			events = append(events, aiEvent{at: now, session: s.Name,
				text: transitionText(s), state: s.AIState, since: s.AISince})
		case s.AIState == tmux.AIStateWorking && before.state != s.AIState:
			events = append(events, aiEvent{at: now, session: s.Name,
				text: transitionText(s), state: s.AIState, since: s.AISince})
		}
	}

	// A session that vanished is reported once, then forgotten: it drops out of
	// next, so a returning name is treated as new rather than as a transition
	// from whatever it held before.
	for name, before := range prev {
		if _, alive := next[name]; alive || before.state == tmux.AIStateNone {
			continue
		}
		events = append(events, aiEvent{at: now, session: name,
			text: goneIcon + " " + goneLabel, state: tmux.AIStateNone})
	}

	return events, next
}

// transitionText is what one logged transition says.
//
// The glyph already carries the state, so the words are free to carry what the
// state cannot: the tool's own name for the work in hand. "작업 중" and
// "작업 완료" were the same two sentences on every row of the log — true, and
// saying nothing you could act on. `⏳ panel-session-last-notification` says
// which turn just started.
//
// The label stays where there is no task name: a tool that publishes none
// (anything but Claude today) still has a state worth reporting, and a bare
// glyph is not a sentence.
func transitionText(s tmux.Session) string {
	if s.AITask != "" {
		return s.AIState.Icon() + " " + s.AITask
	}
	return s.AIState.Icon() + " " + aiStateLabel(s.AIState)
}

// aiSnapshot is what the detector remembers about a session between ticks.
//
// The state alone was not enough. Claude stamps every status change, including
// one blocked prompt replacing another, and with only the state to compare
// those are indistinguishable from no change at all — the panel went quiet on
// exactly the transition it exists to report. since is the file's own timestamp,
// so both panels and the shared log agree on which prompt is which.
type aiSnapshot struct {
	state tmux.AIState
	since time.Time
}

// newPrompt reports a fresh Approval arriving while the session was already
// blocked — Claude answered one prompt and immediately asked another.
//
// A zero timestamp cannot distinguish them, and guessing would turn every tick
// of a screen-detected tool into an event, so an unstamped state stays silent
// until it actually changes.
func newPrompt(before aiSnapshot, s tmux.Session) bool {
	return !s.AISince.IsZero() && !before.since.IsZero() && !before.since.Equal(s.AISince)
}

// What a session's disappearance is drawn as. `○` rather than a new glyph
// because it is already pinned at one cell by TestGlyphWidthsAreStable, and a
// state row that measures wrong shifts every column after it.
//
// A vanished session is the one transition with no state to report: the badge
// that would have said so went with it. Without this line the row simply stops
// being drawn, which is indistinguishable from a session that went quiet.
const (
	goneIcon  = "○"
	goneLabel = "종료"
)

// aiWaitingLabel names what a blocked session is waiting for, in the panel's
// own language.
//
// Claude's `waitingFor` is a mixed field: a couple of fixed phrases, and
// otherwise the command it wants to run ("Bash: git push"). The fixed ones are
// the only part that can be translated, and anything unrecognised is passed
// through verbatim — a command is not prose and must not be rewritten.
//
// The panel had been printing the English through, one line under its own
// Korean, which is the drift aiStateLabel exists to prevent for states.
func aiWaitingLabel(raw string) string {
	switch raw {
	case "input needed":
		return "입력 대기"
	case "permission prompt":
		return "권한 승인"
	}
	return raw
}

// aiStateLabel names a live state in the panel's own words.
//
// The panel speaks Korean throughout — its heading, its separators, its
// degraded notices — while AIState.String() is the English the TUI and
// `mux status` print. The panel goes through here so its own wording cannot
// drift from the TUI's by accident — they differ on purpose.
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

// notifyLines builds the whole panel. Every line is exactly width cells. Returns
// nil when there is nothing to report, so callers can tell "empty" from "a box
// of blanks".
//
// Three parts, in the order they earn their rows: a row per session, one line
// under each saying what last happened there, and the chronological log of
// everything below that.
//
// The middle part is the one that had been missing. A badge says what a session
// is doing *now*; it does not say what it just finished, and reading that off a
// flat log meant scanning past whichever session was busiest — measured, forty
// rows of one session repeating itself.
//
// The session block is rendered twice because the per-session line has to be
// all-or-nothing, and deciding that means knowing how tall the list is without
// it. Computing that arithmetically would put the block's layout rules in two
// places; rendering costs string assembly and nothing else, and sessionLines()
// already redraws the whole panel on every click lookup.
func notifyLines(sessions []tmux.Session, events []aiEvent, width, height int, selected, own string, showHeader bool) []notifyLine {
	rows := notifySessionLines(sessions, nil, width, 0, selected, own)
	if len(rows) == 0 {
		return nil
	}

	header := 0
	if showHeader {
		header = len(panelHeaderLines(sessions, width, height, selected))
	}

	// A line per session is bought first, and only if every session can have
	// one: a list where some rows carry a line and others do not reads as
	// missing data rather than as a short pane.
	perSession := 0
	if height-header-len(rows) >= len(sessions) {
		perSession = 1
	}
	lines := notifySessionLines(sessions, events, width, perSession, selected, own)

	// A section break, not session spacing: this blank carries no session, so it
	// separates the two halves without stretching the last session's click block.
	lines = append(lines, notifyLine{text: blankRow(width)})

	// The log gets what is left and is the thing that yields — it is last, so
	// fixedBox clipping it costs the least. Everything above it is either a
	// session or the one line saying what last happened there.
	lines = append(lines, notifyEventLines(events, width, height-header-len(lines))...)
	if !showHeader {
		return lines
	}
	return append(panelHeaderLines(sessions, width, height, selected), lines...)
}

const (
	// panelHeaderFullHeight is the shortest pane that still gets the path row,
	// panelHeaderShortHeight the shortest that gets a header at all.
	//
	// The panel has no height budget: notifyLines generates as much as it likes
	// and fixedBox clips from the bottom, so every row the header takes is a row
	// off the end of the event log. These two numbers are the whole rationing
	// policy — below them the header is what yields, not the log, because a
	// twelve-row pane that is all header has stopped being a list.
	panelHeaderFullHeight  = 12
	panelHeaderShortHeight = 8
)

// panelHeaderLines describe the session under the cursor: its name and branch,
// what it is doing, and where it lives.
//
// This is half of the detail column 85049e8 removed. The half that went for good
// was the screen capture — fifty columns drawing a copy of the pane sitting
// right beside the panel. This half draws what no pane can: a *detached* session
// has no screen to look at, and without these rows nothing in the panel says
// where the selected one is or what branch it is on.
//
// Every line carries no session, which is load-bearing twice over: sessionAtRow
// indexes the same slice, so clicks keep landing on the rows they name, and
// sessionOrder skips empty-session lines, so the keyboard cursor cannot stop
// here.
//
// Off unless @mux_panel_header says otherwise, and the default is the honest
// one: `mux border` now puts these same facts on the top border of the pane you
// are in, so in the state the panel opens in — cursor on your own session, per
// autoSelect — the header is a second copy of the line right beside it. What it
// still answers is the case the border cannot reach: move the cursor with
// M-Up/M-Down and it describes a session you are *not* in, which nothing else
// shows. That is worth keeping and not worth showing by default.
func panelHeaderLines(sessions []tmux.Session, width, height int, selected string) []notifyLine {
	if width <= 0 || height < panelHeaderShortHeight || selected == "" {
		return nil
	}

	var s tmux.Session
	found := false
	for _, c := range sessions {
		if c.Name == selected {
			s, found = c, true
			break
		}
	}
	if !found {
		return nil
	}

	lines := []notifyLine{{text: panelHeaderName(s, width)}}
	if text := panelStateText(s, width-1); text != "" {
		lines = append(lines, notifyLine{text: renderRow([]rowSeg{
			{text: " " + text, color: aiStateColor(s.AIState)},
		}, width, false)})
	}
	if height >= panelHeaderFullHeight && s.Directory != "" {
		lines = append(lines, notifyLine{text: renderRow([]rowSeg{
			{text: fitCells(" "+panelPath(s.Directory), width-1), color: colorMuted},
		}, width, false)})
	}
	// A section break, carrying no session for the same reason the one between
	// the list and the log carries none.
	return append(lines, notifyLine{text: blankRow(width)})
}

// panelHeaderName is the session's name, with its branch flush right.
func panelHeaderName(s tmux.Session, width int) string {
	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}
	return renderRow(fitRight(
		rowSeg{text: s.Name, color: colorAccent},
		rowSeg{text: branch, color: colorMuted},
		width,
	), width, false)
}

// panelStateText drops detail until the cluster fits, in the order the TUI's
// preview drops it: the blocking reason outranks the elapsed time, which
// outranks the label.
//
// Built here rather than through aiStatusText because that one prints
// AIState.String(), the English the TUI and `mux status` use. These rows sit
// directly above the panel's own Korean, and the two naming the same state
// differently in one pane reads as a bug.
func panelStateText(s tmux.Session, width int) string {
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
		candidates = append(candidates, glyph+" "+label+" · "+aiWaitingLabel(s.AIWaitingFor)+elapsed)
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

// panelPath is the session's directory with $HOME collapsed to ~.
//
// Deliberately not shortenPath: that one truncates by byte length, which splits
// a multi-byte rune down the middle on a path with Hangul in it. Clamping to the
// column is fitCells' job here anyway, so this only does the substitution.
func panelPath(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(dir, home) {
		return "~" + dir[len(home):]
	}
	return dir
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
func notifySessionLines(sessions []tmux.Session, events []aiEvent, width, perSession int, selected, own string) []notifyLine {
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
	lines = append(lines, sessionBlocks(ai, events, width, perSession, selected, own, false)...)

	if len(other) > 0 {
		lines = append(lines, blank,
			notifyLine{text: sectionRule("세션", width)}, blank)
		lines = append(lines, sessionBlocks(other, events, width, perSession, selected, own, true)...)
	}
	return lines
}

// sessionEventLines renders one session's own history, indented under its row.
//
// The name is left out on purpose: inside that session's block it is the one
// thing every row would repeat. What is left — a time and what happened — is
// what the session row cannot say, since it only ever shows the state now.
//
// An empty history still draws a line. A cursor sitting on a session that
// renders nothing under it looks like a panel that failed rather than a session
// nothing has happened in.
func sessionEventLines(events []aiEvent, session string, width, budget int, sel bool) []notifyLine {
	if budget <= 0 {
		return nil
	}

	var lines []notifyLine
	for _, e := range events {
		if e.session != session {
			continue
		}
		lines = append(lines, notifyLine{
			text:    sessionEventLine(e, width),
			session: session,
		})
		if len(lines) == budget {
			return lines
		}
	}
	if len(lines) == 0 && sel {
		// Only under the cursor. A row that says nothing happened is worth one
		// line where the cursor is, and seven lines of it everywhere else.
		return []notifyLine{{
			text:    helpStyle.Render(padOrTruncate("    아직 없음", width)),
			session: session,
		}}
	}
	return lines
}

// sessionEventLine renders "    14:23:01 ✅ 작업 완료" for one logged event.
//
// Indented past the badge column so the block reads as belonging to the row
// above it, and given an explicit text width rather than letting renderRow
// truncate: that cuts without a marker, so a clipped reason would read as the
// whole reason.
func sessionEventLine(e aiEvent, width int) string {
	head := "    " + e.at.Format("15:04:05") + " "
	avail := width - ansi.StringWidth(head)
	if avail < 2 {
		return renderRow(nil, width, false)
	}
	return renderRow([]rowSeg{
		{text: head, color: colorMuted},
		{text: fitCells(e.text, avail), color: aiStateColor(e.state)},
	}, width, false)
}

// notifyEventLines builds the chronological block under the session list: what
// happened across every session, newest first.
//
// It is the counterpart to the one line each session carries. That line answers
// "what last happened *here*"; this answers "what has been happening", which is
// the question a sidebar glanced at between turns is really asking — and it is
// the only place a transition in a session you are not looking at can appear.
//
// budget is what the sessions left over. The block is last, so it is the thing
// that yields on a short pane: everything above it is either a session or the
// one line naming its latest state, and those are the panel.
func notifyEventLines(events []aiEvent, width, budget int) []notifyLine {
	if budget <= 1 {
		// Not even room for the rule and a row. Drawing a heading over nothing
		// reads as a section that failed to load.
		return nil
	}

	lines := []notifyLine{{text: sectionRule("최근 이벤트", width)}}
	if len(events) == 0 {
		return append(lines, notifyLine{text: helpStyle.Render(padOrTruncate(" 아직 없음", width))})
	}
	for _, e := range events {
		if len(lines) == budget {
			break
		}
		lines = append(lines, notifyLine{text: notifyEventLine(e, width)})
	}
	return lines
}

// notifyEventLine renders "14:23:01 name ✅ 작업 완료" for one logged event.
//
// The name is carried here, unlike sessionEventLine: down here a row has no
// session block above it to belong to, so without the name nothing says which
// session it is about.
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
		{text: " " + fitCells(e.text, textWidth), color: aiStateColor(e.state)},
	}
	return renderRow(segs, width, false)
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
// under a blocked one, and the line saying what last happened there.
//
// A blank sits between sessions. It was taken out once, on the reasoning that
// the line under each session already gave the click target its second row —
// true, and it cost the list its air: seven blocks of two lines each run
// together into a wall. The rows it buys back are worth more than the rows it
// costs.
//
// dim marks the second group, whose rows are context rather than the point.
func sessionBlocks(ss []tmux.Session, events []aiEvent, width, perSession int, selected, own string, dim bool) []notifyLine {
	var lines []notifyLine
	for i, s := range ss {
		sel := s.Name == selected
		lines = append(lines, notifyLine{
			text:    notifySessionLine(s, width, rowFlags{selected: sel, dim: dim, own: s.Name == own}),
			session: s.Name,
		})
		if s.AIState == tmux.AIStateApproval && s.AIWaitingFor != "" {
			// The reason is part of its session's block: anywhere in the block
			// clicks to the same session, which is simpler to predict than a
			// row that looks attached but does nothing.
			lines = append(lines, notifyLine{
				text: renderRow([]rowSeg{
					{text: fitCells("    "+aiWaitingLabel(s.AIWaitingFor), width), color: colorMuted},
				}, width, sel),
				session: s.Name,
			})
		}
		// One line per session saying what last happened to it. The row above
		// carries the state *now*; this adds a wall clock, the transition before
		// the current one when the log has moved past it, and the reason a
		// session was blocked on after it stops being blocked.
		//
		// The row clicks to the same session as the row above it, and
		// sessionOrder — which folds consecutive rows with the same owner —
		// gains no extra stop for the cursor.
		//
		// Drawn unhighlighted, unlike the reason row: what the highlight is for
		// is saying precisely which session the cursor is on, and a second
		// inverted line under every selected row blurs that.
		last := sessionEventLines(events, s.Name, width, perSession, sel)
		lines = append(lines, last...)

		// Between sessions only. The blank belongs to the session above it, so a
		// click anywhere in the block lands on the same place; skipping it after
		// the last one keeps the list from trailing into the next heading, at the
		// cost of the bottom session being a shorter target.
		//
		// It follows the highlight only when nothing else sits under the row: an
		// inverted bar below an unhighlighted event line reads as a stray row
		// rather than as the bottom of this block.
		if i < len(ss)-1 {
			lines = append(lines, notifyLine{
				text:    renderRow(nil, width, sel && len(last) == 0),
				session: s.Name,
			})
		}
	}
	return lines
}

// sectionRule renders " ── label ────…" across the pane.
//
// The label used to sit on its own with two dashes in front of it, which reads
// as a line of text rather than as a division — the panel stacks three of these
// blocks and needed the breaks between them to be visible at a glance. The rule
// stops one cell short so it lines up with the right margin every row keeps.
func sectionRule(label string, width int) string {
	head := " ── " + label + " "
	rest := width - ansi.StringWidth(head) - 1
	if rest < 1 {
		return helpStyle.Render(fitCells(head, width))
	}
	return helpStyle.Render(padOrTruncate(head+strings.Repeat("─", rest), width))
}

// blankRow is a full-width empty row in the list's base style.
func blankRow(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(" ", width)
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

// ownMarker flags the session this pane lives in. One cell, like ▶ and ▼, and
// pinned by TestGlyphWidthsAreStable.
//
// It earns its two cells by answering a question the row would otherwise leave
// open: why picking that one session shows a summary instead of its output.
const ownMarker = "◀"

// rowFlags are the per-row states notifySessionLine draws differently. Grouped
// because three trailing booleans at a call site stop saying which is which.
type rowFlags struct {
	selected bool
	dim      bool // second group: context rather than the point
	own      bool // the session this pane lives in
}

// notifySessionLine renders "⏳ name 3m            ⌥ branch" for one session.
//
// Name and age sit together on the left so the age reads as belonging to the
// name, and the branch is flush right so the column can be scanned vertically.
//
// A session running no AI CLI renders the same way with an empty badge — the
// padding is what keeps the branch in one column across both groups.
func notifySessionLine(s tmux.Session, width int, f rowFlags) string {
	selected, dim := f.selected, f.dim
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

	// Travels with the age, on the left, because it belongs to the session's
	// identity rather than to its context — and because the branch, which is
	// what yields under pressure, is on the right.
	mark := ""
	if f.own {
		mark = " " + ownMarker
	}

	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}

	// The branch yields first: which session it is and how long it has held its
	// state are the point of the row, the branch is context. The reserved cell
	// keeps at least one space between the age and the branch when the branch
	// has been cut down to exactly the room left.
	name := s.Name
	fixed := ansi.StringWidth(age) + ansi.StringWidth(mark)
	if room := avail - ansi.StringWidth(name) - fixed - 1; room < ansi.StringWidth(branch) {
		if room >= minBranchWidth {
			branch = fitCells(branch, room)
		} else {
			branch = ""
		}
	}
	// Only truncate the name when it genuinely overflows — fitCells always pads
	// to the width it is given, and padding here would reopen the gap between
	// the name and the age that this layout exists to close.
	if nameRoom := avail - fixed - ansi.StringWidth(branch); ansi.StringWidth(name) > nameRoom {
		name = fitCells(name, nameRoom)
	}

	// renderRow pads what is left over at the very end, so the gap has to be an
	// explicit segment — otherwise the padding lands after the branch and the
	// right edge stops lining up.
	gap := avail - ansi.StringWidth(name) - fixed - ansi.StringWidth(branch)
	if gap < 0 {
		gap = 0
	}

	// A row in the second group is context: it recedes unless it is the one
	// selected, where the highlight has to win over the dimming.
	faded := lipgloss.TerminalColor(nil)
	if dim {
		faded = colorMuted
	}
	ageColor := aiStateColor(s.AIState)
	if ageColor == nil {
		ageColor = faded
	}

	segs := []rowSeg{
		{text: head, color: aiBadgeColor(s)},
		{text: name, color: faded},
		{text: age, color: ageColor},
		{text: mark, color: colorAccent},
		{text: strings.Repeat(" ", gap)},
		{text: branch, color: faded},
	}
	return renderRow(segs, width, selected)
}
