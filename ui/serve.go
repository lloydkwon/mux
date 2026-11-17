package ui

import (
	"github.com/lloydkwon/mux/tmux"
)

// PhoneRow 는 mux serve 의 세션 카드 한 장이다(docs/design/serve.md).
//
// 값은 패널과 같은 판정 함수가 정한다 — 순서(sortByDisplayedAge), 글리프(aiGlyph), 나이(sessionAge 와
// compactAgo), 승인 사유(aiWaitingLabel). 두 번째 판정을 두면 폰을 패널 옆에 놓았을 때 어긋난다. 그리는
// 모양은 폰이 정한다: 패널의 줄 렌더러(notifyLines)는 13px 고정 폭 줄이라 폰에서 세션이 서로 구분되지
// 않았다(사용자, 2026-11-17).
type PhoneRow struct {
	Session  string `json:"session"`
	State    string `json:"state"` // working | approval | ready | shell | none
	Glyph    string `json:"glyph"`
	Age      string `json:"age"`
	Branch   string `json:"branch,omitempty"`
	Worktree bool   `json:"worktree,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Note     string `json:"note,omitempty"`
	Work     string `json:"work,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// PhoneRows 는 세션을 패널의 두 묶음으로 나눠 카드로 만든다: AI 도구가 도는 세션, 그리고 나머지. 각 묶음은
// 패널과 같은 순서다. 받은 슬라이스는 건드리지 않는다. 빈 묶음도 nil 이 아니라 빈 슬라이스다 — JSON 에서
// null 이 아니라 [] 가 되어야 폰이 «없음» 을 그린다.
//
// tmux 를 부르지 않는 순수 함수다. 세션을 읽는 것은 serve 패키지다 — «ui 에는 목 러너가 없다».
func PhoneRows(sessions []tmux.Session) (ai, other []PhoneRow) {
	var aiSessions, otherSessions []tmux.Session
	for _, s := range sessions {
		if _, ok := tmux.SessionAITool(s); ok {
			aiSessions = append(aiSessions, s)
		} else {
			otherSessions = append(otherSessions, s)
		}
	}
	sortByDisplayedAge(aiSessions)
	sortByDisplayedAge(otherSessions)
	return phoneRowsOf(aiSessions), phoneRowsOf(otherSessions)
}

func phoneRowsOf(sessions []tmux.Session) []PhoneRow {
	rows := make([]PhoneRow, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, phoneRow(s))
	}
	return rows
}

func phoneRow(s tmux.Session) PhoneRow {
	glyph, _ := aiGlyph(s)
	r := PhoneRow{
		Session:  s.Name,
		State:    phoneState(s.AIState),
		Glyph:    glyph,
		Age:      compactAgo(sessionAge(s)),
		Branch:   s.GitBranch,
		Worktree: s.IsWorktree,
		Note:     s.Note,
		Work:     s.AIWork,
	}
	if s.Directory != "" {
		r.Dir = panelPath(s.Directory)
	}
	if s.AIState == tmux.AIStateApproval && s.AIWaitingFor != "" {
		r.Reason = aiWaitingLabel(s.AIWaitingFor)
	}
	return r
}

// phoneState 는 카드의 상태 단어다. AIState.String() 을 쓰지 않는다 — 그것은 Ready 를 "waiting" 으로
// 내서(status --json 의 오래된 약속) «완료» 와 «승인 대기» 가 섞인다(agent-api.md 의 상태 단어와 같은 이유).
func phoneState(st tmux.AIState) string {
	switch st {
	case tmux.AIStateWorking:
		return "working"
	case tmux.AIStateApproval:
		return "approval"
	case tmux.AIStateReady:
		return "ready"
	case tmux.AIStateShell:
		return "shell"
	default:
		return "none"
	}
}
