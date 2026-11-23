package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

const (
	// badgeWidth is the fixed cell width of the AI column. State glyphs are
	// Emoji_Presentation and measure 2; tool icons measure 1 and get padded to
	// match, so the branch column lines up across every row.
	badgeWidth = 2

	// elapsedWidth is the fixed gutter holding how long the row has held its
	// state.
	elapsedWidth = 4
)

// aiGlyph returns the session's AI marker: the live-state glyph when the tool
// publishes one, otherwise the tool's own icon. ok is false when the session
// runs no known AI CLI.
//
// One badge carries both facts. A separate state column would repeat what the
// tool icon already says, since only a detected AI CLI can have a state.
//
// The result is unpadded — the list pads it to badgeWidth, the preview header
// wants it tight against the tool name.
func aiGlyph(s tmux.Session) (string, bool) {
	tool, ok := tmux.SessionAITool(s)
	if !ok {
		return "", false
	}
	if g := s.AIState.Icon(); g != "" {
		return g, true
	}
	return tool.Icon, true
}

// branchGlyph returns the marker that precedes a session's git branch: doubled
// for a linked worktree, matching the legend the help page prints.
//
// One decider, for the same reason aiGlyph is one: the preview, the panel and
// the help legend all have to agree, and this had already drifted — the panel
// showed a plain ⌥ for worktrees the preview marked ⌥⌥.
//
// Both forms measure exactly what they draw (⌥ is pinned at one cell by
// TestGlyphWidthsAreStable), so a caller's width arithmetic holds either way.
func branchGlyph(s tmux.Session) string {
	if s.IsWorktree {
		return "⌥⌥"
	}
	return "⌥"
}

// aiStateColor returns the foreground for a live state, or nil for a session
// with no live state — meaning "use the row's own colour".
//
// It took a `selected` argument once, to return a lighter tint that survived the
// highlight's dark background. A reverse-video highlight has no background of
// its own, and renderRow drops segment colours there anyway, so whether a row is
// selected is no longer this function's business.
func aiStateColor(st tmux.AIState) lipgloss.TerminalColor {
	switch st {
	case tmux.AIStateWorking:
		return colorStateWorking
	case tmux.AIStateApproval:
		return colorStateApproval
	case tmux.AIStateReady:
		return colorStateReady
	default:
		return nil
	}
}

// aiTextColor 는 상태를 따르는 «넓은 글» — 패널의 세션 아래 줄 — 의 색이다. nil 은 터미널의 글자색이다.
//
// aiStateColor 를 그대로 쓰지 않는다(2026-11-09, 사용자 요청 «구분은 편하게, 눈의 피로는 덜하게»). 가장
// 흔한 상태인 완료가 가장 넓게 초록으로 칠해져 패널이 초록 글자 덩어리였다. 그래서 완료는 흐리게 물러나고,
// 색은 드문 상태에만 남는다 — 막힌 것은 빨강, 일하는 것(작업 중·셸)은 나이와 같은 청록. 일하는 줄은 한때
// 터미널 글자색이었는데 이름과 섞여 «약간 눈에 띄게» 해 달라고 해서 청록이 됐다(같은 날).
func aiTextColor(st tmux.AIState) lipgloss.TerminalColor {
	switch st {
	case tmux.AIStateApproval:
		return colorStateApproval
	case tmux.AIStateWorking, tmux.AIStateShell:
		return colorStateWorking
	case tmux.AIStateReady:
		return colorMuted
	default:
		return nil
	}
}

// aiLogColor 는 최근 이벤트 글의 색이다. 지나간 일이라 모두 흐리고, 사람을 기다린 일만 빨강으로 남긴다 —
// 상태는 글 머리의 글리프(⏳❗✅)가 이미 말한다.
func aiLogColor(st tmux.AIState) lipgloss.TerminalColor {
	if st == tmux.AIStateApproval {
		return colorStateApproval
	}
	return colorMuted
}

// aiBadgeColor colors the badge by state when there is one, falling back to the
// tool's own colour so ✦/◈/⬡/✧ stay told apart.
func aiBadgeColor(s tmux.Session) lipgloss.TerminalColor {
	if c := aiStateColor(s.AIState); c != nil {
		return c
	}
	if tool, ok := tmux.SessionAITool(s); ok {
		return lipgloss.Color(tool.Color)
	}
	return nil
}

// sessionAge returns the timestamp the list's elapsed column counts from.
//
// For a session with live AI state that is the state's age — "blocked for 3m"
// is the number worth acting on, where the session's creation age is inert.
// Other sessions keep showing their creation age, as they always have.
func sessionAge(s tmux.Session) time.Time {
	if t := stateSince(s); !t.IsZero() {
		return t
	}
	return s.Created
}

// stateSince 는 화면이 «이 상태로 얼마나 됐나» 를 셀 시각이다. 목록·미리보기·패널·pane 테두리가 모두
// 이것을 쓴다 — 하나라도 다른 시계를 쓰면 정렬과 찍힌 숫자가 어긋난다. 0 이면 모른다.
//
// idle 인 Claude 는 상태 파일의 시각(AISince)이 아니라 대화 기록의 마지막 작업(AILastActive)이다.
// 프롬프트에서 /theme 같은 대화상자를 열고 닫아도 AISince 는 새로 쓰인다 — 2026-11-03 에 일괄로
// 보낸 /theme 하나가 전날 끝난 세션들을 «9분 전에 끝남» 으로 올렸다. 이벤트 기록은 그대로 AISince 다.
func stateSince(s tmux.Session) time.Time {
	if s.AIState == tmux.AIStateNone {
		return time.Time{}
	}
	if !s.AILastActive.IsZero() {
		return s.AILastActive
	}
	return s.AISince
}

// aiStatusText renders the preview's state cluster, dropping detail to fit
// maxWidth. Returns "" when the session has no live AI state — the preview
// header already names the tool.
//
// The blocking reason outranks everything else it competes with: on a narrow
// preview the token counts go before the reason does.
func aiStatusText(s tmux.Session, maxWidth int) string {
	if s.AIState == tmux.AIStateNone {
		return ""
	}

	glyph := s.AIState.Icon()
	label := s.AIState.String()

	elapsed := ""
	if t := stateSince(s); !t.IsZero() {
		elapsed = "  " + compactAgo(t)
	}

	candidates := []string{}
	if s.AIWaitingFor != "" {
		candidates = append(candidates,
			fmt.Sprintf("  %s %s · %s%s", glyph, label, s.AIWaitingFor, elapsed))
	}
	candidates = append(candidates,
		fmt.Sprintf("  %s %s%s", glyph, label, elapsed),
		fmt.Sprintf("  %s%s", glyph, elapsed),
		"  "+glyph,
	)

	for _, c := range candidates {
		if ansi.StringWidth(c) <= maxWidth {
			return c
		}
	}
	return ""
}
