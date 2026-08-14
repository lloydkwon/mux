package ui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// mouseTestModel is a model sized like a real terminal, holding count sessions
// under the two action rows.
func mouseTestModel(count int) Model {
	sessions := make([]tmux.Session, count)
	for i := range sessions {
		sessions[i] = tmux.Session{Name: fmt.Sprintf("session-%02d", i), Created: time.Now()}
	}
	m := Model{
		tree:     newTreeState(),
		prefs:    defaultPreferences(),
		width:    120,
		height:   30,
		sessions: sessions,
	}
	m.applyFilter()
	return m
}

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

// rowY is the terminal row a given item index is drawn on, unscrolled.
func (m Model) rowY(idx int) int {
	return idx + m.chromeTop() + 1
}

// The point of the feature: the pointer lands on the row the user is looking at.
func TestClickMovesTheCursorToThatRow(t *testing.T) {
	m := mouseTestModel(5)

	for _, idx := range []int{0, 1, 2, 5} {
		updated, _ := m.updateList(leftClick(4, m.rowY(idx)))
		if got := updated.(Model).cursor; got != idx {
			t.Errorf("click on row %d moved the cursor to %d", idx, got)
		}
	}
}

// A click asks for the clicked row's preview — otherwise the right column would
// keep showing whatever the cursor was on before.
func TestClickRefreshesThePreview(t *testing.T) {
	m := mouseTestModel(5)
	_, cmd := m.updateList(leftClick(4, m.rowY(3)))
	if cmd == nil {
		t.Fatal("a click on a new row should request its preview")
	}
	if got := m.AttachName(); got != "" {
		t.Fatalf("a single click attached to %q; it should only move the cursor", got)
	}
}

// The second press on the same row is the decision — the user has already seen
// the preview the first one asked for.
func TestDoubleClickAttaches(t *testing.T) {
	m := mouseTestModel(5)
	y := m.rowY(3) // items[3] is the second session (two action rows first)

	first, _ := m.updateList(leftClick(4, y))
	second, cmd := first.(Model).updateList(leftClick(4, y))
	got := second.(Model)

	if cmd == nil {
		t.Fatal("attaching should quit the TUI")
	}
	if got.AttachName() != "session-01" {
		t.Fatalf("attach target = %q, want session-01", got.AttachName())
	}
}

// Two presses far enough apart are two decisions to look, not one to go.
func TestSlowSecondClickDoesNotAttach(t *testing.T) {
	m := mouseTestModel(5)
	y := m.rowY(3)

	first, _ := m.updateList(leftClick(4, y))
	stale := first.(Model)
	stale.lastClick.at = time.Now().Add(-2 * doubleClickWindow)

	second, _ := stale.updateList(leftClick(4, y))
	if got := second.(Model).AttachName(); got != "" {
		t.Fatalf("a slow second click attached to %q", got)
	}
}

// Clicking one row and then another is never a double click, however fast.
func TestDoubleClickNeedsTheSameRow(t *testing.T) {
	m := mouseTestModel(5)
	first, _ := m.updateList(leftClick(4, m.rowY(2)))
	second, _ := first.(Model).updateList(leftClick(4, m.rowY(3)))
	if got := second.(Model).AttachName(); got != "" {
		t.Fatalf("clicks on different rows attached to %q", got)
	}
}

// The action rows are rows like any other, and a double click on one must do
// exactly what enter does — that is why both go through activateCurrent.
func TestDoubleClickOnActionRows(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-test,123,0")

	shell := mouseTestModel(3)
	y := shell.rowY(0)
	first, _ := shell.updateList(leftClick(4, y))
	second, cmd := first.(Model).updateList(leftClick(4, y))
	if got := second.(Model); !got.DetachRequested() || cmd == nil {
		t.Fatalf("double click on New shell: detach=%v cmd=%v", got.DetachRequested(), cmd != nil)
	}

	create := mouseTestModel(3)
	y = create.rowY(1)
	first, _ = create.updateList(leftClick(4, y))
	second, _ = first.(Model).updateList(leftClick(4, y))
	if got := second.(Model); got.mode != modeCreate || !got.createModel.attach {
		t.Fatalf("double click on New tmux session: mode=%v attach=%v", got.mode, got.createModel.attach)
	}
}

// A scrolled list must still map the pointer to the row the user sees. The
// renderer and the click map share listOffset precisely so this holds.
func TestClickAccountsForScrollOffset(t *testing.T) {
	m := mouseTestModel(60)
	innerHeight := m.panelHeight() - 2
	m.cursor = len(m.items) - 1 // scrolled to the bottom

	offset := listOffset(m.cursor, innerHeight)
	if offset == 0 {
		t.Fatalf("test needs a scrolled list; offset=%d items=%d inner=%d",
			offset, len(m.items), innerHeight)
	}

	// The top visible row is items[offset], not items[0].
	updated, _ := m.updateList(leftClick(4, m.chromeTop()+1))
	if got := updated.(Model).cursor; got != offset {
		t.Fatalf("click on the top visible row = %d, want %d", got, offset)
	}
}

// The filter bar pushes the columns down a row; the click map has to follow it.
func TestClickFollowsTheExtraBar(t *testing.T) {
	m := mouseTestModel(5)
	m.filterText = "session-0"
	m.applyFilter()
	if m.chromeTop() != 2 {
		t.Fatalf("chromeTop with a filter bar = %d, want 2", m.chromeTop())
	}

	updated, _ := m.updateList(leftClick(4, m.rowY(2)))
	if got := updated.(Model).cursor; got != 2 {
		t.Fatalf("cursor = %d, want 2", got)
	}
}

// Everything that is not a list row is inert: the chrome, the border, the
// preview column, rows past the end of the list, and a release.
func TestClicksOutsideTheListAreIgnored(t *testing.T) {
	m := mouseTestModel(3)
	m.cursor = 2
	before := m.cursor

	cases := []struct {
		name string
		msg  tea.MouseMsg
	}{
		{"title row", leftClick(4, 0)},
		{"top border", leftClick(4, m.chromeTop())},
		{"below the last item", leftClick(4, m.rowY(len(m.items)))},
		{"preview column", leftClick(m.listWidth()+4, m.rowY(0))},
		{"release, not press", tea.MouseMsg{X: 4, Y: m.rowY(0),
			Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease}},
		{"right button", tea.MouseMsg{X: 4, Y: m.rowY(0),
			Button: tea.MouseButtonRight, Action: tea.MouseActionPress}},
	}
	for _, tc := range cases {
		updated, _ := m.updateList(tc.msg)
		got := updated.(Model)
		if got.cursor != before {
			t.Errorf("%s: moved the cursor to %d", tc.name, got.cursor)
		}
		if got.AttachName() != "" {
			t.Errorf("%s: attached to %q", tc.name, got.AttachName())
		}
	}
}

func TestWheelMovesTheCursor(t *testing.T) {
	m := mouseTestModel(5)
	m.cursor = 2

	down, _ := m.updateList(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if got := down.(Model).cursor; got != 3 {
		t.Fatalf("wheel down = %d, want 3", got)
	}
	up, _ := down.(Model).updateList(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if got := up.(Model).cursor; got != 2 {
		t.Fatalf("wheel up = %d, want 2", got)
	}
}

// The cursor stops at both ends rather than wrapping, whether it is moved by a
// key or a wheel.
func TestCursorClampsAtBothEnds(t *testing.T) {
	m := mouseTestModel(3)

	top, _ := m.moveCursor(-1)
	if got := top.(Model).cursor; got != 0 {
		t.Errorf("up from the top = %d, want 0", got)
	}

	m.cursor = len(m.items) - 1
	bottom, _ := m.moveCursor(1)
	if got := bottom.(Model).cursor; got != len(m.items)-1 {
		t.Errorf("down from the bottom = %d, want %d", got, len(m.items)-1)
	}

	empty := Model{tree: newTreeState(), prefs: defaultPreferences()}
	if _, cmd := empty.moveCursor(1); cmd != nil {
		t.Error("moving in an empty list should do nothing")
	}
}
