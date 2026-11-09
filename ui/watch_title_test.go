package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

func untitledSessions() []tmux.Session {
	return []tmux.Session{{Name: "a", AIWork: "패널에 세션별 작업을 적어 주세요", AIWorkUntitled: true}}
}

// 손으로 만든 watchModel 은 claude 를 부르지 않는다 — go test ./ui 가 사용량을 쓰면 안 된다.
func TestWatchWithoutTheSwitchNeverTitles(t *testing.T) {
	m := watchTestModel(60, 30)
	got, _ := m.Update(sessionsLoadedMsg{sessions: untitledSessions()})
	if got.(watchModel).titling {
		t.Error("스위치 없이 제목을 지으러 나갔다")
	}
}

func TestWatchTitlesOneAtATime(t *testing.T) {
	var called [][]tmux.Session
	m := watchTestModel(60, 30)
	m.titleWork = func(s []tmux.Session) { called = append(called, s) }
	m.sessions = untitledSessions()

	first, cmd := m.nextTitleCmd()
	if cmd == nil || !first.titling {
		t.Fatalf("첫 호출: cmd=%v titling=%v", cmd != nil, first.titling)
	}
	if len(called) != 0 {
		t.Fatal("Update 안에서 불렀다 — 수 초 걸리는 일이라 Cmd 여야 한다")
	}

	// 짓는 동안 다음 틱이 와도 또 나가지 않는다.
	if _, again := first.nextTitleCmd(); again != nil {
		t.Error("짓는 중에 또 나갔다")
	}

	msg := cmd()
	if len(called) != 1 || len(called[0]) != 1 || called[0][0].Name != "a" {
		t.Fatalf("받은 세션 %v", called)
	}

	got, _ := first.Update(msg)
	done := got.(watchModel)
	if done.titling {
		t.Error("끝난 뒤에도 titling 이다 — 다음 세션이 영영 안 지어진다")
	}
	if _, next := done.nextTitleCmd(); next == nil {
		t.Error("끝난 뒤 다음 호출이 나가지 않는다")
	}
}

func TestWatchWithNothingUntitledStaysIdle(t *testing.T) {
	m := watchTestModel(60, 30)
	m.titleWork = func([]tmux.Session) { t.Error("부를 일이 없는데 불렀다") }
	m.sessions = []tmux.Session{
		{Name: "a", AIWork: "패널 작업 표시"},
		{Name: "b"},
	}
	got, cmd := m.nextTitleCmd()
	if cmd != nil || got.titling {
		t.Errorf("cmd=%v titling=%v", cmd != nil, got.titling)
	}
}

// 틱이 세션을 받으면 제목을 지으러 나간다.
func TestSessionsLoadedStartsTitling(t *testing.T) {
	m := watchTestModel(60, 30)
	m.titleWork = func([]tmux.Session) {}
	var got tea.Model
	got, _ = m.Update(sessionsLoadedMsg{sessions: untitledSessions()})
	if !got.(watchModel).titling {
		t.Error("세션을 받고도 제목을 지으러 나가지 않았다")
	}
}
