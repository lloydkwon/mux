package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The help page has no scrolling, and fixedBox clips whatever does not fit
// without saying so. The smallest screen it must survive is a tmux popup on an
// 80x24 terminal — 85%x80% of that is 68x19 — so the body is held to that
// budget here rather than losing its bottom rows on someone's laptop. Korean
// runes draw two cells, so a reworded line can blow this without looking longer.
func TestHelpBodyFitsPopup(t *testing.T) {
	lines := strings.Split(renderHelpBody(), "\n")

	if len(lines) > helpMaxLines {
		t.Errorf("help body has %d lines, want at most %d", len(lines), helpMaxLines)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(ansi.Strip(line)); w > helpMaxWidth {
			t.Errorf("line %d measures %d cells, want at most %d: %q",
				i+1, w, helpMaxWidth, ansi.Strip(line))
		}
	}
}

// viewHelp replaces the whole screen, so it owes the terminal exactly its
// dimensions — no short page leaving stale rows behind, no long one scrolling
// the top away.
func TestViewHelpDimensions(t *testing.T) {
	for _, w := range []int{68, 80, 120} {
		for _, h := range []int{19, 24, 40} {
			m := Model{width: w, height: h}
			lines := strings.Split(m.viewHelp(), "\n")
			if len(lines) != h {
				t.Errorf("%dx%d: got %d lines, want %d", w, h, len(lines), h)
				continue
			}
			for i, line := range lines {
				if got := ansi.StringWidth(ansi.Strip(line)); got != w {
					t.Errorf("%dx%d: line %d measures %d cells, want %d",
						w, h, i+1, got, w)
				}
			}
		}
	}
}

// `?` was falling through updateList's digit branch and being discarded.
func TestHelpKeyOpensAndAnyKeyCloses(t *testing.T) {
	m := menuTestModel()
	updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	got := updated.(Model)
	if got.mode != modeHelp {
		t.Fatalf("mode after ? = %v, want help", got.mode)
	}

	closed, _ := got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	if closed.(Model).mode != modeList {
		t.Fatalf("mode after keypress = %v, want list", closed.(Model).mode)
	}
}

// The tick keeps firing while help is open; it must not dismiss the page.
func TestHelpSurvivesTick(t *testing.T) {
	m := menuTestModel()
	m.mode = modeHelp
	updated, _ := m.Update(tickMsg{})
	if got := updated.(Model).mode; got != modeHelp {
		t.Fatalf("mode after tick = %v, want help", got)
	}
}
