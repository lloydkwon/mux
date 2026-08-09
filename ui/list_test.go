package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/xguru/mux/tmux"
)

var allClaudeStates = []tmux.ClaudeState{
	tmux.ClaudeStateNone,
	tmux.ClaudeStateWorking,
	tmux.ClaudeStateApproval,
	tmux.ClaudeStateReady,
}

// Every decorative rune the list emits must measure what the terminal draws.
// drawBorder re-pads each line to the panel width, so any compensation a row
// tries to apply is undone — the only workable rule is that measured width
// equals drawn width.
func TestGlyphWidthsAreStable(t *testing.T) {
	oneCell := []string{"▶", "▼", "○", "*", "✦", "◈", "⬡", "✧", "⌥"}
	for _, g := range oneCell {
		if w := ansi.StringWidth(g); w != 1 {
			t.Errorf("glyph %q measures %d cells, want 1", g, w)
		}
	}
	for _, st := range allClaudeStates {
		if w := ansi.StringWidth(claudeGlyph(st)); w != stateGlyphWidth {
			t.Errorf("claudeGlyph(%v) measures %d cells, want %d", st, w, stateGlyphWidth)
		}
	}
}

// The state column is a fixed-width slot; if it ever varies, every column to
// its right shifts on that row alone.
func TestClaudeStateCellIsFixedWidth(t *testing.T) {
	now := time.Now()
	ages := []time.Duration{
		0,
		5 * time.Second,
		12 * time.Minute,
		3 * time.Hour,
		2 * 24 * time.Hour,
		365 * 24 * time.Hour,
		-5 * time.Second, // clock skew
	}

	for _, st := range allClaudeStates {
		for _, age := range ages {
			s := tmux.Session{
				Created:     now.Add(-age),
				ClaudeState: st,
				ClaudeSince: now.Add(-age),
			}
			for _, selected := range []bool{false, true} {
				text, _ := claudeStateCell(s, selected)
				if w := ansi.StringWidth(text); w != stateCellWidth {
					t.Errorf("state=%v age=%v selected=%v: cell %q measures %d, want %d",
						st, age, selected, text, w, stateCellWidth)
				}
			}
		}
	}

	// An unknown state start must still fill the slot.
	s := tmux.Session{Created: now, ClaudeState: tmux.ClaudeStateWorking}
	text, _ := claudeStateCell(s, false)
	if w := ansi.StringWidth(text); w != stateCellWidth {
		t.Errorf("zero ClaudeSince: cell %q measures %d, want %d", text, w, stateCellWidth)
	}
}

// The exhaustive drift catcher: whatever the combination, a row occupies
// exactly the width it was given.
func TestFormatSessionRowWidthInvariant(t *testing.T) {
	now := time.Now()
	branches := []string{"", "main", "feature/a-very-long-branch-name-here"}
	names := []string{"a", "mux", "a-fairly-long-session-name-indeed", "한글세션이름"}
	widths := []int{10, 16, 22, 30, 46, 62, 78}

	for _, st := range allClaudeStates {
		for _, name := range names {
			for _, branch := range branches {
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
										ClaudeState:   st,
										ClaudeSince:   now.Add(-3 * time.Minute),
									}
									for _, w := range widths {
										row := formatSessionRow(s, order, expanded, selected, w)
										if got := ansi.StringWidth(ansi.Strip(row)); got != w {
											t.Fatalf("width=%d state=%v name=%q branch=%q cmd=%s order=%d: got %d cells (%q)",
												w, st, name, branch, cmd, order, got, ansi.Strip(row))
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

// At the narrowest supported panel the state and elapsed time must survive —
// they are the reason the layout was rearranged.
func TestFormatSessionRowKeepsStateWhenNarrow(t *testing.T) {
	s := tmux.Session{
		Name:        "some-project",
		Created:     time.Now().Add(-time.Hour),
		ClaudeState: tmux.ClaudeStateApproval,
		ClaudeSince: time.Now().Add(-3 * time.Minute),
	}
	row := ansi.Strip(formatSessionRow(s, 0, false, false, 30))

	if !strings.Contains(row, claudeGlyph(tmux.ClaudeStateApproval)) {
		t.Errorf("approval glyph missing at width 30: %q", row)
	}
	if !strings.Contains(row, "3m") {
		t.Errorf("elapsed time missing at width 30: %q", row)
	}
}

// Sessions without Claude must look exactly as they did: no state glyph, and
// the time column still showing the session's own age.
func TestFormatSessionRowLeavesNonClaudeAlone(t *testing.T) {
	s := tmux.Session{
		Name:          "plain",
		Created:       time.Now().Add(-2 * time.Hour),
		ActiveCommand: "bash",
	}
	row := ansi.Strip(formatSessionRow(s, 0, false, false, 46))

	for _, st := range []tmux.ClaudeState{
		tmux.ClaudeStateWorking, tmux.ClaudeStateApproval, tmux.ClaudeStateReady,
	} {
		if strings.Contains(row, claudeGlyph(st)) {
			t.Errorf("non-Claude row shows the %v glyph: %q", st, row)
		}
	}
	if !strings.Contains(row, "2h") {
		t.Errorf("non-Claude row lost its creation age: %q", row)
	}
}

// Columns must line up between rows that differ in what they carry.
func TestFormatSessionRowColumnsAlign(t *testing.T) {
	now := time.Now()
	sessions := []tmux.Session{
		{Name: "alpha", Created: now, ClaudeState: tmux.ClaudeStateWorking, ClaudeSince: now},
		{Name: "beta", Created: now, ActiveCommand: "bash"},
		{Name: "gamma", Created: now, ActiveCommand: "codex", GitBranch: "main"},
	}

	want := -1
	for _, s := range sessions {
		row := ansi.Strip(formatSessionRow(s, 0, false, false, 46))
		idx := strings.Index(row, s.Name)
		if idx < 0 {
			t.Fatalf("name %q not found in %q", s.Name, row)
		}
		// Byte offsets differ between rows because the state glyphs are
		// multi-byte; the column that matters is the cell count.
		got := ansi.StringWidth(row[:idx])
		if want < 0 {
			want = got
		} else if got != want {
			t.Errorf("name column for %q starts at cell %d, want %d: %q", s.Name, got, want, row)
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
