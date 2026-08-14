package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/lloydkwon/mux/tmux"
)

func TestLayoutDimensions(t *testing.T) {
	sessions := []tmux.Session{
		{Name: "claude", WindowCount: 1, Created: time.Now().Add(-2 * time.Hour), Attached: true, Directory: "/Users/test/workspace/project1"},
		{Name: "dev-server", WindowCount: 2, Created: time.Now().Add(-24 * time.Hour), Attached: false, Directory: "/Users/test/workspace/project2"},
		{Name: "deploy", WindowCount: 1, Created: time.Now().Add(-48 * time.Hour), Attached: false, Directory: "/Users/test/workspace/project3"},
	}

	widths := []int{80, 120, 160, 200}
	heights := []int{20, 30, 40, 50}

	for _, w := range widths {
		for _, h := range heights {
			t.Run("", func(t *testing.T) {
				m := NewModel()
				m.width = w
				m.height = h
				m.sessions = sessions
				m.filtered = sessions
				m.cursor = 0

				output := m.viewMain()
				lines := strings.Split(output, "\n")

				t.Logf("w=%d h=%d => output lines=%d", w, h, len(lines))

				if len(lines) > h {
					t.Errorf("w=%d h=%d: output has %d lines, exceeds terminal height %d", w, h, len(lines), h)
					// Print first and last few lines for debugging
					for i, l := range lines {
						if i < 3 || i >= len(lines)-3 {
							t.Logf("  line %d (len=%d): %q", i, len(l), truncStr(l, 80))
						}
					}
				}
			})
		}
	}
}

func TestListPreviewSameHeight(t *testing.T) {
	sessions := []tmux.Session{
		{Name: "claude", WindowCount: 1, Created: time.Now(), Attached: true, Directory: "/Users/test/project"},
		{Name: "dev", WindowCount: 1, Created: time.Now(), Attached: false, Directory: "/Users/test/dev"},
	}

	width := 120
	height := 30

	listWidth := width * 2 / 5
	previewWidth := width - listWidth
	contentHeight := height - 3

	listOut := renderSessionList(sessions, 0, "", listWidth, contentHeight)
	item := &listItem{kind: itemSession, session: &sessions[0]}
	previewOut := renderPreview(item, "", previewWidth, contentHeight, nil)

	listLines := strings.Count(listOut, "\n") + 1
	previewLines := strings.Count(previewOut, "\n") + 1

	t.Logf("list lines=%d, preview lines=%d, contentHeight=%d", listLines, previewLines, contentHeight)

	if listLines != previewLines {
		t.Errorf("height mismatch: list=%d preview=%d", listLines, previewLines)
	}
}

func TestSessionListScrolling(t *testing.T) {
	// Create more sessions than can fit in a small viewport
	sessions := make([]tmux.Session, 20)
	for i := range sessions {
		sessions[i] = tmux.Session{
			Name:        fmt.Sprintf("session-%02d", i),
			WindowCount: 1,
			Created:     time.Now(),
		}
	}

	width := 60
	height := 10 // innerHeight = 8, so only 8 sessions visible

	// Cursor at 0: first session should be visible
	out := renderSessionList(sessions, 0, "", width, height)
	if !strings.Contains(out, "session-00") {
		t.Error("cursor=0: expected session-00 to be visible")
	}

	// Cursor at 15: should scroll so session-15 is visible
	out = renderSessionList(sessions, 15, "", width, height)
	if !strings.Contains(out, "session-15") {
		t.Error("cursor=15: expected session-15 to be visible")
	}
	// session-00 should be scrolled out
	if strings.Contains(out, "session-00") {
		t.Error("cursor=15: expected session-00 to be scrolled out")
	}

	// Cursor at last session
	out = renderSessionList(sessions, 19, "", width, height)
	if !strings.Contains(out, "session-19") {
		t.Error("cursor=19: expected session-19 to be visible")
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// One frame around both columns rather than two boxes side by side: the seam of
// two adjacent border characters is what this replaced, so the shape is worth
// pinning.
func TestDrawFrameShape(t *testing.T) {
	for _, tc := range []struct{ left, right, height int }{
		{10, 20, 3}, {1, 1, 1}, {40, 60, 12},
	} {
		leftBlock := blockOf(tc.left, tc.height, "L")
		rightBlock := blockOf(tc.right, tc.height, "R")

		out := ansi.Strip(drawFrame(leftBlock, rightBlock, tc.left, tc.right, tc.height))
		lines := strings.Split(out, "\n")

		if want := tc.height + 2; len(lines) != want {
			t.Fatalf("%+v: %d lines, want %d", tc, len(lines), want)
		}
		wantWidth := tc.left + tc.right + 3
		for i, l := range lines {
			if got := ansi.StringWidth(l); got != wantWidth {
				t.Errorf("%+v: line %d measures %d cells, want %d", tc, i, got, wantWidth)
			}
		}
		if got := lines[0]; !strings.HasPrefix(got, "╭") || !strings.HasSuffix(got, "╮") {
			t.Errorf("%+v: top rule = %q", tc, got)
		}
		// The divider sits between the columns, once, on every row.
		for i, l := range lines {
			divider := []rune(l)[tc.left+1]
			want := map[bool]rune{true: '┬', false: '│'}[i == 0]
			if i == len(lines)-1 {
				want = '┴'
			}
			if divider != want {
				t.Errorf("%+v: line %d divider = %q, want %q", tc, i, divider, want)
			}
		}
	}
}

// blockOf builds a w×h block of fill for the frame to wrap.
func blockOf(w, h int, fill string) string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = strings.Repeat(fill, w)
	}
	return strings.Join(lines, "\n")
}

// The palette is the terminal's, not mux's. A hex value creeping back in is the
// regression this guards: it would be picked against whichever background the
// author happened to be using, and be unreadable on the other one.
func TestPaletteIsTheTerminalScheme(t *testing.T) {
	for name, c := range map[string]lipgloss.TerminalColor{
		"colorAccent":   colorAccent,
		"colorSuccess":  colorSuccess,
		"colorDanger":   colorDanger,
		"colorPrimary":  colorPrimary,
		"colorMuted":    colorMuted,
		"colorBorder":   colorBorder,
		"stateWorking":  colorStateWorking,
		"stateReady":    colorStateReady,
		"stateApproval": colorStateApproval,
	} {
		if _, ok := c.(lipgloss.ANSIColor); !ok {
			t.Errorf("%s is %T, want an ANSI palette index", name, c)
		}
	}

	// The AI tools' colours go through the same rule, from the other package.
	for _, name := range []string{"claude", "codex", "aider", "gemini"} {
		tool, ok := tmux.LookupAITool(name)
		if !ok {
			t.Fatalf("%s is missing from the registry", name)
		}
		if strings.HasPrefix(tool.Color, "#") {
			t.Errorf("%s is %q, want an ANSI palette index", name, tool.Color)
		}
	}
}

// Reverse video swaps the terminal's own two colours, so a foreground set on top
// of it is painted as a *background* — a coloured span inside a selected row
// would come out as a block. renderRow drops segment colours there, and this is
// what stops someone from putting the state colours back.
func TestSelectedRowDropsSegmentColors(t *testing.T) {
	// Tests do not run on a terminal, so lipgloss would otherwise strip every
	// escape and the assertions below would pass on an empty string.
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })

	segs := []rowSeg{
		{text: "name"},
		{text: " state", color: colorDanger},
		{text: " branch", color: colorMuted},
	}

	selected := renderRow(segs, 30, true)
	if !strings.Contains(selected, "\x1b[7m") {
		t.Errorf("selected row is not reverse video: %q", selected)
	}
	// ANSI 1 as a foreground is "31"; the muted grey is "90". Neither may appear.
	for _, sgr := range []string{"31", "90"} {
		if strings.Contains(selected, "\x1b["+sgr+"m") {
			t.Errorf("selected row still sets colour %s: %q", sgr, selected)
		}
	}

	// Unselected, the same segments keep their colours — the drop is about the
	// highlight, not about giving up colour.
	if plain := renderRow(segs, 30, false); !strings.Contains(plain, "\x1b[31m") {
		t.Errorf("unselected row lost its colour: %q", plain)
	}
}
