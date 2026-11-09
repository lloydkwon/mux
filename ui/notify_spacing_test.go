package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/lloydkwon/mux/tmux"
)

// 세션 블록은 빈 줄 없이 붙고, 블록 사이에 보이는 선도 없다(2026-11-09, 사용자 결정). 세션이 늘어 줄이
// 모자랐고, 검은 이중 밑줄을 거쳐 마지막 요청은 «선을 바탕과 같게 해서 안 보이게» 였다 — 바탕색 선은 긋지
// 않은 것과 같은 화면이다. 빈 줄을 되살리거나 밑줄을 다시 긋기 전에 docs/design/panel.md 를 읽는다.
func TestSessionBlocksSitFlush(t *testing.T) {
	// 밑줄이 몰래 돌아오면 잡히게 이스케이프를 낸다 — 테스트는 터미널이 아니라 그냥 두면 다 걷힌다.
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })

	now := time.Now()
	working := sess("mux", tmux.AIStateWorking)
	working.AISince = now.Add(-time.Minute)
	blocked := sess("api", tmux.AIStateApproval)
	blocked.AISince = now.Add(-10 * time.Minute)
	blocked.AIWaitingFor = "Bash: git push"
	ready := sess("radar", tmux.AIStateReady)
	ready.AISince = now.Add(-time.Hour)
	sessions := []tmux.Session{
		working, blocked, ready,
		{Name: "notes", ActiveCommand: "zsh", Created: now.Add(-time.Minute)},
		{Name: "zz", ActiveCommand: "zsh", Created: now.Add(-time.Hour)},
	}

	// mux 의 블록이 두 줄이 되게(세션 행 + 이벤트 줄) 이벤트를 하나 준다.
	events := []aiEvent{{at: now, session: "mux", text: "⏳ 작업 중", state: tmux.AIStateWorking}}

	rows := notifyLines(sessions, events, 40, 40, "notes", "", false)
	var order []string
	for i, l := range rows {
		if l.session == "" {
			continue
		}
		if strings.TrimSpace(ansi.Strip(l.text)) == "" {
			t.Errorf("row %d of %s is a blank spacer", i, l.session)
		}
		if strings.Contains(l.text, "\x1b[4m") || strings.Contains(l.text, "\x1b[4:") {
			t.Errorf("row %d of %s draws an underline: %q", i, l.session, l.text)
		}
		if len(order) == 0 || order[len(order)-1] != l.session {
			order = append(order, l.session)
		}
	}
	// 다섯 세션이 다 그려졌는지 — 빈 결과에서 위 단언이 헛도는 것을 막는다.
	if got := strings.Join(order, " "); got != "mux api radar notes zz" {
		t.Errorf("sessions drawn = %q", got)
	}
}
