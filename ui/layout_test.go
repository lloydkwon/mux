package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xguru/mux/tmux"
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
