package tmux

import (
	"strings"
	"testing"

	"github.com/lloydkwon/mux/tmux/detect"
)

// A capture of Claude Code sitting on a permission prompt, taken verbatim from
// a live pane. The blank tail is what capture-pane really returns — it pads to
// the pane height — and trimming it is what makes bottom-anchored regions line
// up with what herdr's engine expects.
const fixtureClaudeBlocked = `● I'll update the config for you.

╭──────────────────────────────────────────────╮
│ Edit file                                    │
│ config.toml                                  │
│                                              │
│ Do you want to make this edit?               │
│ ❯ 1. Yes                                     │
│   2. No, tell Claude what to do differently  │
╰──────────────────────────────────────────────╯
  esc to cancel · enter to confirm



`

// The same session a moment later, back at its prompt.
const fixtureClaudeIdle = `● Done — config.toml now sets the timeout to 30s.

╭──────────────────────────────────────────────╮
│ >                                            │
╰──────────────────────────────────────────────╯
  ? for shortcuts



`

func TestSplitCapturesCutsOnTheSeparator(t *testing.T) {
	batched := "first pane\n" + captureSeparator + "\nsecond pane\n" + captureSeparator + "\nthird pane\n"

	screens := splitCaptures(batched, 3)
	if len(screens) != 3 {
		t.Fatalf("got %d screens, want 3", len(screens))
	}
	for index, want := range []string{"first pane\n", "second pane\n", "third pane\n"} {
		if screens[index] != want {
			t.Errorf("screen %d = %q, want %q", index, screens[index], want)
		}
	}
}

// A batch that comes back short — a pane died between listing and capturing —
// must leave the remaining sessions blank rather than shifting every screen
// onto the wrong session.
func TestSplitCapturesPadsAShortBatch(t *testing.T) {
	screens := splitCaptures("only one\n", 3)
	if len(screens) != 3 {
		t.Fatalf("got %d screens, want 3", len(screens))
	}
	if screens[0] != "only one\n" {
		t.Errorf("screen 0 = %q", screens[0])
	}
	if screens[1] != "" || screens[2] != "" {
		t.Errorf("screens 1,2 = %q,%q, want empty", screens[1], screens[2])
	}
}

// capture-pane pads to the pane height. herdr's engine reads regions anchored
// at the last non-blank line, so leaving the padding on would push
// bottom_non_empty_lines(N) past everything that matters.
func TestTrimTrailingBlankLines(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"padded", "a\nb\n\n\n\n", "a\nb\n"},
		{"already tight", "a\nb\n", "a\nb\n"},
		{"whitespace only lines count as blank", "a\n   \n\t\n", "a\n"},
		{"all blank", "\n\n\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trimTrailingBlankLines(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScreenStateMapsOntoBadgeStates(t *testing.T) {
	// herdr's vocabulary is idle/working/blocked; mux's badge speaks
	// waiting/working/approval. A mismatch here shows the wrong glyph.
	cases := []struct {
		from detect.State
		want AIState
	}{
		{detect.StateWorking, AIStateWorking},
		{detect.StateBlocked, AIStateApproval},
		{detect.StateIdle, AIStateReady},
		{detect.StateUnknown, AIStateNone},
	}
	for _, tc := range cases {
		t.Run(tc.from.String(), func(t *testing.T) {
			if got := screenState(tc.from); got != tc.want {
				t.Errorf("%v -> %v, want %v", tc.from, got, tc.want)
			}
		})
	}
}

// The whole point of the feature: a real Claude screen, through the real
// engine, lands on the badge mux draws.
func TestDetectionReachesTheBadge(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   AIState
	}{
		{"permission prompt is an approval", fixtureClaudeBlocked, AIStateApproval},
		{"back at the prompt is waiting", fixtureClaudeIdle, AIStateReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detection := detect.Detect("claude", detect.Input{
				Screen: trimTrailingBlankLines(tc.screen),
			})
			if got := screenState(detection.State); got != tc.want {
				t.Errorf("got %v (rule %q), want %v", got, detection.RuleID, tc.want)
			}
		})
	}
}

// ScreenStates must survive a tmux that is not running at all — mux is often
// started before any session exists.
func TestScreenStatesWithoutAServer(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		// No mock registered for list-panes, so the runner errors.
		states := ScreenStates()
		if len(states) != 0 {
			t.Errorf("got %d states, want none", len(states))
		}
	})
}

// A session whose pane runs a plain shell must not appear at all, so callers
// can tell "no agent here" from "agent, state unknown".
func TestActiveAgentPanesSkipsNonAgents(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("work\t%0\tzsh\t0\thostname\n"), nil,
			"tmux", "list-panes", "-a", "-F", screenPaneFormat)

		panes := activeAgentPanes()
		if len(panes) != 0 {
			t.Errorf("got %d panes, want none for a plain shell", len(panes))
		}
	})
}

func TestActiveAgentPanesTakesOnePanePerSession(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte(strings.Join([]string{
			"work\t%0\tclaude\t0\t⠋ Claude",
			"work\t%1\tclaude\t0\tsecond pane in the same session",
			"notes\t%2\tcodex\t0\tCodex",
		}, "\n")+"\n"), nil, "tmux", "list-panes", "-a", "-F", screenPaneFormat)

		panes := activeAgentPanes()
		if len(panes) != 2 {
			t.Fatalf("got %d panes, want one per session", len(panes))
		}
		if panes[0].session != "work" || panes[0].paneID != "%0" {
			t.Errorf("first pane = %+v", panes[0])
		}
		if panes[0].title != "⠋ Claude" {
			t.Errorf("title = %q, want the pane title carried through", panes[0].title)
		}
		if panes[1].session != "notes" || panes[1].tool != "codex" {
			t.Errorf("second pane = %+v", panes[1])
		}
	})
}
