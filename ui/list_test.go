package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

var allAIStates = []tmux.AIState{
	tmux.AIStateNone,
	tmux.AIStateWorking,
	tmux.AIStateApproval,
	tmux.AIStateReady,
	tmux.AIStateShell,
}

// Every decorative rune the list emits must measure what the terminal draws.
// The frame re-pads each line to the column width, so any compensation a row
// tries to apply is undone — the only workable rule is that measured width
// equals drawn width.
func TestGlyphWidthsAreStable(t *testing.T) {
	oneCell := []string{"▶", "▼", "◀", "○", "*", "✦", "◈", "⬡", "✧", "⌥", "─", noteGlyph}
	// Screen detection brought sixteen more tools, and every one of their icons
	// shares the same badge slot. Taken from the map rather than copied here, so
	// a tool added later cannot skip this check.
	for _, tool := range tmux.AITools() {
		oneCell = append(oneCell, tool.Icon)
	}
	for _, g := range oneCell {
		if w := ansi.StringWidth(g); w != 1 {
			t.Errorf("glyph %q measures %d cells, want 1", g, w)
		}
	}
	for _, st := range allAIStates {
		g := st.Icon()
		if st == tmux.AIStateNone {
			if g != "" {
				t.Errorf("AIStateNone.Icon() = %q, want empty so the tool icon shows", g)
			}
			continue
		}
		if w := ansi.StringWidth(g); w != badgeWidth {
			t.Errorf("%v.Icon() = %q measures %d cells, want %d", st, g, w, badgeWidth)
		}
	}
}

// The badge is a fixed-width slot whatever it holds — a 2-cell state glyph, a
// 1-cell tool icon, or nothing at all. If it ever varies, the branch column
// shifts on that row alone.
func TestAIBadgeCellIsFixedWidth(t *testing.T) {
	for _, st := range allAIStates {
		for _, cmd := range []string{"", "bash", "claude", "codex", "aider", "gemini"} {
			s := tmux.Session{ActiveCommand: cmd, AIState: st}
			glyph, _ := aiGlyph(s)
			if w := ansi.StringWidth(padOrTruncate(glyph, badgeWidth)); w != badgeWidth {
				t.Errorf("state=%v cmd=%q: badge %q measures %d, want %d",
					st, cmd, glyph, w, badgeWidth)
			}
		}
	}
}

// The whole point of the merge: one badge says both which tool and what state.
// A live state replaces the tool icon rather than sitting beside it.
func TestAIBadgeMergesStateAndTool(t *testing.T) {
	stateful := tmux.Session{ActiveCommand: "claude", AIState: tmux.AIStateWorking}
	if got, _ := aiGlyph(stateful); got != tmux.AIStateWorking.Icon() {
		t.Errorf("working claude badge = %q, want the state glyph %q",
			got, tmux.AIStateWorking.Icon())
	}

	// No state file (Claude not running under a tmux-aware session, or a tool
	// that publishes nothing): the tool icon still identifies the row.
	for cmd, want := range map[string]string{
		"claude": "✦", "codex": "◈", "aider": "⬡", "gemini": "✧",
	} {
		got, ok := aiGlyph(tmux.Session{ActiveCommand: cmd})
		if !ok || got != want {
			t.Errorf("aiGlyph(%q) = %q, %v; want %q, true", cmd, got, ok, want)
		}
	}

	if _, ok := aiGlyph(tmux.Session{ActiveCommand: "bash"}); ok {
		t.Error("aiGlyph reported a badge for a plain shell session")
	}
}

// The exhaustive drift catcher: whatever the combination, a row occupies
// exactly the width it was given.
func TestFormatSessionRowWidthInvariant(t *testing.T) {
	now := time.Now()
	branches := []string{"", "main", "feature/a-very-long-branch-name-here"}
	names := []string{"a", "mux", "a-fairly-long-session-name-indeed", "한글세션이름"}
	notes := []string{"", "라벨링", "라벨링 작업이 끝나야 다음 단계로 넘어갈 수 있다"}
	widths := []int{10, 16, 22, 30, 46, 62, 78}

	for _, st := range allAIStates {
		for _, name := range names {
			for _, branch := range branches {
				for _, note := range notes {
					for _, cmd := range []string{"bash", "claude"} {
						for _, order := range []int{0, 1, 42} {
							for _, attached := range []bool{false, true} {
								for _, expanded := range []bool{false, true} {
									for _, selected := range []bool{false, true} {
										s := tmux.Session{
											Name:          name,
											Created:       now.Add(-90 * time.Second),
											Attached:      attached,
											ActiveCommand: cmd,
											GitBranch:     branch,
											Note:          note,
											AIState:       st,
											AISince:       now.Add(-3 * time.Minute),
										}
										for _, w := range widths {
											for _, showOrder := range []bool{false, true} {
												for _, nameWidth := range []int{sessionNameMin, 20, sessionNameMax} {
													row := formatSessionRow(s, order, expanded, selected, showOrder, nameWidth, w)
													if got := ansi.StringWidth(ansi.Strip(row)); got != w {
														t.Fatalf("width=%d name column=%d state=%v name=%q branch=%q note=%q cmd=%s order=%d: got %d cells (%q)",
															w, nameWidth, st, name, branch, note, cmd, order, got, ansi.Strip(row))
													}
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// At the narrowest supported panel the badge and elapsed time must survive —
// they are the reason the layout was rearranged.
func TestFormatSessionRowKeepsStateWhenNarrow(t *testing.T) {
	s := tmux.Session{
		Name:    "some-project",
		Created: time.Now().Add(-time.Hour),
		AIState: tmux.AIStateApproval,
		AISince: time.Now().Add(-3 * time.Minute),
	}
	row := ansi.Strip(formatSessionRow(s, 0, false, false, false, sessionNameMin, 30))

	if !strings.Contains(row, tmux.AIStateApproval.Icon()) {
		t.Errorf("approval glyph missing at width 30: %q", row)
	}
	if strings.Contains(row, "✦") {
		t.Errorf("row shows both the state glyph and the tool icon: %q", row)
	}
	if !strings.Contains(row, "3m") {
		t.Errorf("elapsed time missing at width 30: %q", row)
	}
}

// Sessions with no AI CLI must look exactly as they did: no badge, and the
// time column still showing the session's own age.
func TestFormatSessionRowLeavesNonAIAlone(t *testing.T) {
	s := tmux.Session{
		Name:          "plain",
		Created:       time.Now().Add(-2 * time.Hour),
		ActiveCommand: "bash",
	}
	row := ansi.Strip(formatSessionRow(s, 0, false, false, false, 20, 46))

	for _, st := range []tmux.AIState{
		tmux.AIStateWorking, tmux.AIStateApproval, tmux.AIStateReady,
	} {
		if strings.Contains(row, st.Icon()) {
			t.Errorf("non-AI row shows the %v glyph: %q", st, row)
		}
	}
	if !strings.Contains(row, "2h") {
		t.Errorf("non-AI row lost its creation age: %q", row)
	}
}

// Columns must line up between rows that differ in what they carry. The badge
// is padded to a fixed width precisely so a 2-cell state glyph, a 1-cell tool
// icon, and no badge at all leave the branch in the same column.
func TestFormatSessionRowColumnsAlign(t *testing.T) {
	now := time.Now()
	sessions := []tmux.Session{
		{Name: "alpha", Created: now, AIState: tmux.AIStateWorking, AISince: now, GitBranch: "main"},
		{Name: "beta", Created: now, ActiveCommand: "bash", GitBranch: "main"},
		{Name: "gamma", Created: now, ActiveCommand: "codex", GitBranch: "main"},
	}

	// Byte offsets differ between rows because the glyphs are multi-byte; the
	// column that matters is the cell count.
	cellOffset := func(row, sub string) int {
		idx := strings.Index(row, sub)
		if idx < 0 {
			return -1
		}
		return ansi.StringWidth(row[:idx])
	}

	wantName, wantBranch := -1, -1
	for _, s := range sessions {
		row := ansi.Strip(formatSessionRow(s, 0, false, false, false, 20, 46))

		got := cellOffset(row, s.Name)
		if got < 0 {
			t.Fatalf("name %q not found in %q", s.Name, row)
		}
		if wantName < 0 {
			wantName = got
		} else if got != wantName {
			t.Errorf("name column for %q starts at cell %d, want %d: %q", s.Name, got, wantName, row)
		}

		got = cellOffset(row, s.GitBranch)
		if got < 0 {
			t.Fatalf("branch %q not found in %q", s.GitBranch, row)
		}
		if wantBranch < 0 {
			wantBranch = got
		} else if got != wantBranch {
			t.Errorf("branch column for %q starts at cell %d, want %d: %q", s.Name, got, wantBranch, row)
		}
	}
}

func TestCompactAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		age  time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
		{-10 * time.Second, "0s"}, // future timestamps must not count down
	}
	for _, tt := range tests {
		if got := compactAgo(now.Add(-tt.age)); got != tt.want {
			t.Errorf("compactAgo(-%v) = %q, want %q", tt.age, got, tt.want)
		}
	}
}

// timeAgo keeps its 4-cell column contract now that it delegates.
func TestTimeAgoWidth(t *testing.T) {
	now := time.Now()
	for _, age := range []time.Duration{
		time.Second, time.Minute, time.Hour, 100 * 24 * time.Hour,
	} {
		if got := ansi.StringWidth(timeAgo(now.Add(-age))); got != 4 {
			t.Errorf("timeAgo(-%v) measures %d cells, want 4", age, got)
		}
	}
}

// The order column was four blank cells on every row of every list, spent so
// that the handful of people pinning an order could see it. It now appears only
// when the list has one to show.
func TestOrderColumnAppearsOnlyWhenUsed(t *testing.T) {
	s := tmux.Session{Name: "mux", Created: time.Now()}

	off := ansi.Strip(formatSessionRow(s, 0, false, false, false, 20, 60))
	on := ansi.Strip(formatSessionRow(s, 3, false, false, true, 20, 60))

	if want := "▶ mux"; !strings.HasPrefix(off, want) {
		t.Errorf("without an order the row starts %q, want it to start %q", off[:12], want)
	}
	if want := "▶ #3 mux"; !strings.HasPrefix(on, want) {
		t.Errorf("with an order the row starts %q, want it to start %q", on[:12], want)
	}

	// The column is the only difference, so the name moves by exactly its width.
	if got := strings.Index(on, "mux") - strings.Index(off, "mux"); got != orderWidth {
		t.Errorf("the name moved %d cells, want %d", got, orderWidth)
	}
}

// One column for every row in the list, decided once: a per-row width would let
// the age wander as names came and went.
func TestSessionNameWidthIsSharedAndBounded(t *testing.T) {
	now := time.Now()
	items := []listItem{
		{kind: itemNewShell},
		{kind: itemNewSession},
		{kind: itemSession, session: &tmux.Session{Name: "a", Created: now}},
		{kind: itemSession, session: &tmux.Session{Name: "a-longer-session-name", Created: now}},
	}

	// Wide enough for everything: the column is the longest label, and the
	// action rows count — they share it.
	if got, want := sessionNameWidth(items, 80, false, false), len("a-longer-session-name"); got != want {
		t.Errorf("width = %d, want the longest name %d", got, want)
	}

	// Narrow: bounded by what is left after the gutter, the tail and a branch.
	narrow := sessionNameWidth(items, 40, false, false)
	if narrow >= 40-rowGutter-sessionTailReserve {
		t.Errorf("width %d leaves nothing for the tail at 40 cells", narrow)
	}
	if narrow < sessionNameMin {
		t.Errorf("width %d is below the floor %d", narrow, sessionNameMin)
	}

	// Short names do not leave a hole: the column shrinks to fit them.
	short := []listItem{{kind: itemSession, session: &tmux.Session{Name: "a", Created: now}}}
	if got := sessionNameWidth(short, 80, false, false); got != sessionNameMin {
		t.Errorf("width for one short name = %d, want the floor %d", got, sessionNameMin)
	}
}

// TestSessionRowNoteYieldsAfterBranch pins the priority, which is the whole
// point of the note column. The branch is repeated by the preview header and by
// `mux border`; the note is written by hand and appears nowhere else, so the
// branch has to be the one that goes first.
func TestSessionRowNoteYieldsAfterBranch(t *testing.T) {
	s := tmux.Session{
		Name:      "matchingByLocal",
		Created:   time.Now().Add(-3 * time.Minute),
		GitBranch: "main",
		Note:      "라벨링 끝나야 진행",
	}
	row := func(w int) string {
		return ansi.Strip(formatSessionRow(s, 0, false, false, false, 16, w))
	}

	// Wide: both.
	if got := row(70); !strings.Contains(got, "라벨링 끝나야 진행") || !strings.Contains(got, "main") {
		t.Errorf("at 70 want note and branch, got %q", got)
	}
	// Squeezed: the branch is gone while the note is still whole.
	if got := row(56); strings.Contains(got, "main") || !strings.Contains(got, "라벨링 끝나야 진행") {
		t.Errorf("at 56 want the note whole and no branch, got %q", got)
	}
	// Tighter: the note itself is cut, and says so.
	if got := row(46); !strings.Contains(got, noteGlyph) || !strings.Contains(got, "...") {
		t.Errorf("at 46 want a truncated note, got %q", got)
	}
	// Narrower than minNoteWidth: dropped whole rather than left as a bare
	// glyph and an ellipsis, which says nothing the empty space does not.
	if got := row(30); strings.Contains(got, noteGlyph) {
		t.Errorf("at 30 want no note at all, got %q", got)
	}
}

// A note must not cost the branch on a row wide enough for both, which is what
// the reserve in sessionNameWidth is for.
func TestSessionNameWidthReservesRoomForNotes(t *testing.T) {
	noted := tmux.Session{Name: "a-longer-session-name", Note: "메모"}
	plain := tmux.Session{Name: "a-longer-session-name"}
	items := []listItem{{kind: itemSession, session: &noted}}
	bare := []listItem{{kind: itemSession, session: &plain}}

	if !anyNoted(items) {
		t.Fatal("anyNoted false for a session with a note")
	}
	if anyNoted(bare) {
		t.Fatal("anyNoted true for a session with none")
	}

	// At a width where the name is bounded by the room left rather than by its
	// own length, the reserve has to show up as a narrower name column.
	withNote := sessionNameWidth(items, 46, false, true)
	without := sessionNameWidth(bare, 46, false, false)
	if withNote >= without {
		t.Errorf("name column %d with notes, %d without — the reserve did nothing", withNote, without)
	}
}
