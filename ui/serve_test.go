package ui

import (
	"reflect"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// PhoneRows 는 mux serve 의 카드다(docs/design/serve.md). 값은 패널과 같은 판정 함수가 정해야 한다 —
// 폰과 패널을 나란히 놓고 볼 때 순서·글리프·나이가 어긋나면 둘 중 하나가 틀린 것으로 읽힌다.

func phoneFixture(now time.Time) []tmux.Session {
	return []tmux.Session{
		{Name: "docs", ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateReady,
			AISince: now.Add(-20 * time.Minute), GitBranch: "main", Directory: "/srv/docs", AIWork: "링크를 고쳤다"},
		{Name: "api", ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateApproval,
			AISince: now.Add(-2 * time.Minute), AIWaitingFor: "permission prompt", AIWork: "푸시하려는 중"},
		{Name: "mux", ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateWorking,
			AISince: now.Add(-5 * time.Minute), GitBranch: "feat", IsWorktree: true},
		// 상태 파일이 없는 AI 도구 — 화면 감지도 답하지 않은 codex. 글리프는 도구 아이콘이다.
		{Name: "cx", ActiveCommand: "codex", Created: now.Add(-3 * time.Hour)},
		{Name: "notes", Note: "회의록", Created: now.Add(-90 * time.Minute)},
		{Name: "dev", Created: now.Add(-7 * time.Hour)},
	}
}

func rowNames(rows []PhoneRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Session
	}
	return out
}

func TestPhoneRowsFollowThePanelsOrderAndGroups(t *testing.T) {
	sessions := phoneFixture(time.Now())
	ai, other := PhoneRows(sessions)
	if got, want := rowNames(ai), []string{"api", "mux", "docs", "cx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AI 세션 = %v, want %v", got, want)
	}
	if got, want := rowNames(other), []string{"notes", "dev"}; !reflect.DeepEqual(got, want) {
		t.Errorf("다른 세션 = %v, want %v", got, want)
	}
	if sessions[0].Name != "docs" {
		t.Error("PhoneRows 가 받은 슬라이스를 정렬했다 — 부르는 쪽의 순서를 바꾸면 안 된다")
	}
}

func TestPhoneRowsUseThePanelsDeciders(t *testing.T) {
	sessions := phoneFixture(time.Now())
	ai, other := PhoneRows(sessions)
	byName := map[string]PhoneRow{}
	for _, r := range append(append([]PhoneRow{}, ai...), other...) {
		byName[r.Session] = r
	}
	for _, s := range sessions {
		r := byName[s.Name]
		glyph, _ := aiGlyph(s)
		if r.Glyph != glyph {
			t.Errorf("%s: glyph = %q, 패널은 %q", s.Name, r.Glyph, glyph)
		}
		if want := compactAgo(sessionAge(s)); r.Age != want {
			t.Errorf("%s: age = %q, 패널은 %q", s.Name, r.Age, want)
		}
	}

	want := map[string]PhoneRow{
		"api":   {Session: "api", State: "approval", Glyph: "❗", Age: "2m", Reason: "권한 승인", Work: "푸시하려는 중"},
		"mux":   {Session: "mux", State: "working", Glyph: "⏳", Age: "5m", Branch: "feat", Worktree: true},
		"docs":  {Session: "docs", State: "ready", Glyph: "✅", Age: "20m", Branch: "main", Dir: "/srv/docs", Work: "링크를 고쳤다"},
		"cx":    {Session: "cx", State: "none", Glyph: "◈", Age: "3h"},
		"notes": {Session: "notes", State: "none", Age: "1h", Note: "회의록"},
		"dev":   {Session: "dev", State: "none", Age: "7h"},
	}
	for name, w := range want {
		if got := byName[name]; got != w {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, w)
		}
	}
}

func TestPhoneRowsEmpty(t *testing.T) {
	ai, other := PhoneRows(nil)
	if ai == nil || other == nil || len(ai)+len(other) != 0 {
		t.Errorf("빈 목록은 빈 슬라이스여야 한다(JSON 의 null 이 아니라 []): %v %v", ai, other)
	}
}
