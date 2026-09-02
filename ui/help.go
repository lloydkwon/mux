package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lloydkwon/mux/tmux"
)

// The help page's smallest supported screen. `mux popup` opens at 85%x80% of
// the terminal (tmux/popup.go), so an 80x24 terminal leaves it 68x19 — the
// tightest case anyone actually runs. fixedBox clips silently past that, so the
// budget is pinned by TestHelpBodyFitsPopup rather than discovered in the wild.
const (
	helpMaxWidth = 68
	helpMaxLines = 19
)

// Column widths of the help page's two tables, in cells. Padding is applied to
// the plain text *before* styling: a rendered string carries escape codes that
// measure zero, so padding after Render would land outside the style and drift
// the next column.
const (
	helpGlyphCol  = 6
	helpLegendCol = 20
	// The key column holds "enter 더블클릭", which is what set its width: Korean
	// runes draw two cells, and at 13 it was cutting that label mid-glyph and
	// running the next column into it.
	helpKeyCol  = 15
	helpDescCol = 12
	helpKey2Col = 7
)

// aiLegendOrder fixes the order tools appear in the legend. Ranging over
// tmux's map directly would shuffle the row between renders.
var aiLegendOrder = []string{"claude", "codex", "aider", "gemini"}

// helpLegendRow renders one row of the marker legend: two (glyph, meaning)
// pairs on fixed cell columns.
func helpLegendRow(g1, t1, g2, t2 string) string {
	return " " +
		helpKeyStyle.Render(padOrTruncate(g1, helpGlyphCol)) +
		helpStyle.Render(padOrTruncate(t1, helpLegendCol)) +
		helpKeyStyle.Render(padOrTruncate(g2, helpGlyphCol)) +
		helpStyle.Render(t2)
}

// helpKeyRow renders one row of the shortcut table: two (key, action) pairs on
// fixed cell columns.
func helpKeyRow(k1, d1, k2, d2 string) string {
	return " " +
		helpKeyStyle.Render(padOrTruncate(k1, helpKeyCol)) +
		helpStyle.Render(padOrTruncate(d1, helpDescCol)) +
		helpKeyStyle.Render(padOrTruncate(k2, helpKey2Col)) +
		helpStyle.Render(d2)
}

// aiToolLegend renders the tool icons in their own colors, read from tmux's
// registry so the legend cannot drift from what the list draws.
func aiToolLegend() string {
	var parts []string
	for _, name := range aiLegendOrder {
		tool, ok := tmux.LookupAITool(name)
		if !ok {
			continue
		}
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color(tool.Color)).Render(tool.Icon)
		parts = append(parts, icon+" "+helpStyle.Render(tool.Name))
	}
	return " " + strings.Join(parts, "   ")
}

// aiStateLegend renders the live-state glyphs in the same colors the list gives
// them, so a row's color is readable here too.
func aiStateLegend() string {
	states := []struct {
		state tmux.AIState
		label string
	}{
		{tmux.AIStateWorking, "작업 중"},
		{tmux.AIStateApproval, "입력 대기"},
		{tmux.AIStateReady, "턴 종료"},
	}

	var parts []string
	for _, s := range states {
		glyph := lipgloss.NewStyle().Foreground(aiStateColor(s.state)).Render(s.state.Icon())
		parts = append(parts, glyph+" "+helpStyle.Render(s.label))
	}
	return " " + strings.Join(parts, "   ")
}

// renderHelpBody builds the help page. The marker legend comes first and the
// shortcut table second: fixedBox clips from the bottom, so the part that can
// only be learned here outranks the part the footer already hints at.
func renderHelpBody() string {
	lines := []string{
		titleStyle.Render(" mux 도움말") + helpStyle.Render(" · 아무 키나 누르면 닫힙니다"),
		"",
		helpLegendRow("▶ ▼", "접힘 / 펼침", "이름", "밝은 이름 = attach 중"),
		helpLegendRow("#3", "고정 정렬 순서", "12m", "세션 나이 (AI 상태면 유지 시간)"),
		helpLegendRow("⌥ ⌥⌥", "브랜치 / worktree", noteGlyph, "세션 메모"),
		"",
		titleStyle.Render(" AI 배지") + helpStyle.Render(" · 상태 글리프가 도구 아이콘을 대체한다"),
		aiToolLegend(),
		aiStateLegend(),
		"",
		titleStyle.Render(" 단축키"),
		helpKeyRow("↑↓ jk 클릭", "이동", "n", "새 세션"),
		helpKeyRow("tab l", "펼침", "r", "이름 변경"),
		helpKeyRow("shift+tab h", "접기", "x", "세션 종료"),
		helpKeyRow("g G", "처음 / 끝", "0-9", "순서 지정 (0 해제)"),
		helpKeyRow("enter 더블클릭", "attach", "o", "정렬 순환"),
		helpKeyRow("/", "검색", "esc", "검색 해제"),
		helpKeyRow("?", "도움말", "q", "종료"),
		helpKeyRow("m", "메모 편집", "v", "선택 모드 (복사)"),
	}
	return strings.Join(lines, "\n")
}

// viewHelp renders the help page as a full screen.
//
// It deliberately skips viewWithOverlay: that centers a box without bounding
// it, which is fine for the 3-line create/rename prompts but not for a page
// this size — in a tmux popup on an 80x24 terminal there are only 19 rows to
// work with. fixedBox instead guarantees exactly m.height lines of exactly
// m.width cells at any size.
func (m Model) viewHelp() string {
	return fixedBox(renderHelpBody(), m.width, m.height)
}
