package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// 2026-11-03 의 사고를 줄인 것: 전날 끝난 세션에 /theme 이 들어가 상태 파일의 시각(AISince)이 9분 전이
// 됐다. 대화 기록의 마지막 작업(AILastActive)은 19시간 전이다.
func themedSession(name string, now time.Time) tmux.Session {
	s := sess(name, tmux.AIStateReady)
	s.AISince = now.Add(-9 * time.Minute)
	s.AILastActive = now.Add(-19 * time.Hour)
	return s
}

// 정렬은 행이 찍는 값으로 한다 — 그 값이 이제 마지막 작업 시각이다.
func TestRecentSortUsesTheLastWork(t *testing.T) {
	now := time.Now()
	worked := sess("worked", tmux.AIStateReady)
	worked.AISince = now.Add(-time.Hour)
	sessions := []tmux.Session{themedSession("themed", now), worked}

	got := sortedSessions(sessions, preferences{Sort: sortRecent})
	if got[0].Name != "worked" {
		t.Errorf("recent order = %s, %s; want the session that actually worked first", got[0].Name, got[1].Name)
	}

	panel := append([]tmux.Session(nil), sessions...)
	sortByDisplayedAge(panel)
	if panel[0].Name != "worked" {
		t.Errorf("panel order = %s, %s; want the session that actually worked first", panel[0].Name, panel[1].Name)
	}
}

// 목록·미리보기·패널·pane 테두리가 같은 나이를 말한다. 하나라도 AISince 를 쓰면 정렬과 숫자가 다시 어긋난다.
func TestEveryElapsedShowsTheLastWork(t *testing.T) {
	now := time.Now()
	s := themedSession("themed", now)
	s.Directory = "/home/u/src"

	if got := sessionAge(s); !got.Equal(s.AILastActive) {
		t.Errorf("sessionAge = %v, want AILastActive %v", got, s.AILastActive)
	}
	shown := map[string]string{
		"list":    formatSessionRow(s, 0, false, false, false, 12, 80),
		"preview": aiStatusText(s, 70),
		"panel":   panelStateText(s, 40),
		"border":  BorderLine(s, "", 120),
	}
	for where, text := range shown {
		if !strings.Contains(text, "19h") || strings.Contains(text, "9m") {
			t.Errorf("%s shows %q, want the last work's 19h and not the status file's 9m", where, text)
		}
	}
}

// 기록을 못 읽은 세션(AILastActive 0)은 예전처럼 상태 파일의 시각을 쓴다.
func TestElapsedFallsBackToTheStatusFile(t *testing.T) {
	s := sess("plain", tmux.AIStateReady)
	s.AISince = time.Now().Add(-9 * time.Minute)
	if got := sessionAge(s); !got.Equal(s.AISince) {
		t.Errorf("sessionAge = %v, want AISince %v", got, s.AISince)
	}
	if got := aiStatusText(s, 70); !strings.Contains(got, "9m") {
		t.Errorf("preview %q does not show the status file's age", got)
	}
}
