package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lloydkwon/mux/tmux"
)

// 패널의 넓은 글은 밝기로 상태를 가르고, 색은 배지·나이 같은 짧은 표식에만 남는다(2026-11-09, 사용자 요청
// «구분은 편하게, 눈의 피로는 덜하게»). 완료가 가장 흔한 상태라 작업 줄과 최근 이벤트가 초록 글자 덩어리였다.
// 넓은 글에 aiStateColor 를 다시 쓰기 전에 docs/design/panel.md 를 읽는다.

// SGR 글자색 번호: ANSI 1·2·6 은 31·32·36, 흐린 회색(8)은 90. 색 없음은 "" 다.
const (
	sgrRed   = "31"
	sgrGreen = "32"
	sgrCyan  = "36"
	sgrMuted = "90"
)

func withANSIProfile(t *testing.T) {
	t.Helper()
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })
}

// fgOf 는 line 에서 text 가 그려진 글자색의 SGR 번호다. text 바로 앞에 색 시퀀스가 없으면(앞 구간의 리셋이거나
// 아무것도 없으면) 터미널의 글자색 그대로라는 뜻이라 "" 를 돌려준다.
func fgOf(t *testing.T, line, text string) string {
	t.Helper()
	i := strings.Index(line, text)
	if i < 0 {
		t.Fatalf("%q is not in %q", text, line)
	}
	prefix := line[:i]
	if !strings.HasSuffix(prefix, "m") {
		return ""
	}
	j := strings.LastIndex(prefix, "\x1b[")
	if j < 0 {
		return ""
	}
	if sgr := prefix[j+2 : len(prefix)-1]; sgr != "0" {
		return sgr
	}
	return ""
}

// 작업 줄은 세 단계다: 막힌 것은 빨강, 일하는 것(작업 중·셸)은 나이와 같은 청록으로 약간 눈에 띄게, 끝나고
// 쉬는 것은 흐리게. 일하는 줄은 터미널 글자색이었다가 이름·기본 글과 섞인다고 해서 청록이 됐다(2026-11-09).
func TestWorkLineIsToldApartByBrightness(t *testing.T) {
	withANSIProfile(t)
	for _, tc := range []struct {
		state tmux.AIState
		want  string
	}{
		{tmux.AIStateApproval, sgrRed},
		{tmux.AIStateWorking, sgrCyan},
		{tmux.AIStateShell, sgrCyan},
		{tmux.AIStateReady, sgrMuted},
	} {
		s := sess("api", tc.state)
		s.AIWork = "패널 글자색 정리"
		if got := fgOf(t, sessionWorkLine(s, 48), s.AIWork); got != tc.want {
			t.Errorf("%v: work line drawn in %q, want %q", tc.state, got, tc.want)
		}
	}
}

// 이벤트가 작업 줄 자리에 설 때(지금 하는 일을 모를 때)도 같은 규칙이다 — 같은 자리의 글이 상태마다 다른
// 규칙으로 칠해지면 어느 쪽이 그 세션의 지금인지 읽히지 않는다.
func TestSessionEventLineFollowsTheWorkLine(t *testing.T) {
	withANSIProfile(t)
	for _, tc := range []struct {
		state tmux.AIState
		text  string
		want  string
	}{
		{tmux.AIStateApproval, "❗ 승인 대기", sgrRed},
		{tmux.AIStateWorking, "⏳ 작업 중", sgrCyan},
		{tmux.AIStateReady, "✅ 작업 완료", sgrMuted},
	} {
		e := aiEvent{at: time.Now(), session: "api", text: tc.text, state: tc.state}
		if got := fgOf(t, sessionEventLine(e, 48), tc.text); got != tc.want {
			t.Errorf("%v: event line drawn in %q, want %q", tc.state, got, tc.want)
		}
	}
}

// 최근 이벤트는 지나간 일이라 모두 흐리고, 사람을 기다린 일만 빨강으로 남는다. 상태는 글 머리의 글리프가 말한다.
func TestEventLogRecedes(t *testing.T) {
	withANSIProfile(t)
	for _, tc := range []struct {
		state tmux.AIState
		text  string
		want  string
	}{
		{tmux.AIStateApproval, "❗ 승인 대기", sgrRed},
		{tmux.AIStateWorking, "⏳ 작업 중", sgrMuted},
		{tmux.AIStateReady, "✅ 작업 완료", sgrMuted},
		{tmux.AIStateNone, goneIcon + " " + goneLabel, sgrMuted},
	} {
		e := aiEvent{at: time.Now(), session: "api", text: tc.text, state: tc.state}
		// 글 구간은 이름과 띄우는 한 칸으로 시작한다.
		if got := fgOf(t, notifyEventLine(e, 48), " "+tc.text); got != tc.want {
			t.Errorf("%v: log line drawn in %q, want %q", tc.state, got, tc.want)
		}
	}
}

// 색이 물러난 것은 넓은 글뿐이다. 세션 행의 나이는 짧은 표식이라 상태색을 그대로 지니고, 그것이 한눈에
// 보이는 구분의 나머지 반이다.
func TestSessionRowAgeKeepsTheStateColour(t *testing.T) {
	withANSIProfile(t)
	now := time.Now()
	for _, tc := range []struct {
		state tmux.AIState
		want  string
	}{
		{tmux.AIStateApproval, sgrRed},
		{tmux.AIStateWorking, sgrCyan},
		{tmux.AIStateReady, sgrGreen},
	} {
		s := sess("api", tc.state)
		s.AISince = now.Add(-3 * time.Minute)
		age := " " + compactAgo(sessionAge(s))
		if got := fgOf(t, notifySessionLine(s, 48, rowFlags{}), age); got != tc.want {
			t.Errorf("%v: age drawn in %q, want %q", tc.state, got, tc.want)
		}
	}
}

// 배지도 나이와 같은 짧은 표식이라 상태색 그대로다(aiBadgeColor → aiStateColor). 넓은 글의 규칙(aiTextColor)을
// 배지에 잘못 이으면 가장 흔한 완료의 ✅ 가 흐려지고, 한눈에 보이는 구분이 사라진다.
func TestSessionRowBadgeKeepsTheStateColour(t *testing.T) {
	withANSIProfile(t)
	for _, tc := range []struct {
		state tmux.AIState
		want  string
	}{
		{tmux.AIStateApproval, sgrRed},
		{tmux.AIStateWorking, sgrCyan},
		{tmux.AIStateReady, sgrGreen},
	} {
		s := sess("api", tc.state)
		glyph, ok := aiGlyph(s)
		if !ok {
			t.Fatalf("%v: the session has no badge glyph", tc.state)
		}
		// 배지 구간은 한 칸을 앞에 두고 시작한다(notifySessionLine 의 head) — 글리프만 찾으면 색 시퀀스 바로
		// 뒤가 아니라 "" 가 나온다.
		if got := fgOf(t, notifySessionLine(s, 48, rowFlags{}), " "+glyph); got != tc.want {
			t.Errorf("%v: badge %q drawn in %q, want %q", tc.state, glyph, got, tc.want)
		}
	}
}
