package tmux

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// sessionFile 은 Claude 2.1.312 의 상태 파일 모양을 따른다.
func sessionFile(pid int, sid, tmuxRef, status, name string, startedAt int64) string {
	return fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/home/u/proj","startedAt":%d,"procStart":"1","version":"2.1.312","tmux":%q,"name":%q,"nameSource":"user","status":%q,"statusUpdatedAt":1793589532061,"waitingFor":"approve Bash"}`,
		pid, sid, startedAt, tmuxRef, name, status)
}

// withAgentServer 는 상태 파일들과 tmux 의 pane 목록을 함께 세운다.
func withAgentServer(t *testing.T, files map[string]string, panes string, fn func(m *mockRunner)) {
	t.Helper()
	withClaudeSessions(t, files, nil, func() {
		m := newMockRunner()
		if panes != "" {
			m.OnOutput([]byte(panes), nil, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}")
		}
		SetRunner(m) // withClaudeSessions 가 끝날 때 원래 러너로 돌려놓는다
		fn(m)
	})
}

func TestAgentsUsesTheLiveSessionAndDropsReusedPanes(t *testing.T) {
	files := map[string]string{
		// Claude 는 세션 이름을 "old" 로 적었지만 지금 그 pane 은 "renamed" 의 것이다.
		"101.json": sessionFile(101, "s-101", "old:@1.%1", "busy", "plan", 1),
		// 다른 서버의 %2 를 가리키는 살아 있는 파일 — 이 서버의 %2 프로세스 트리에 없다.
		"102.json": sessionFile(102, "s-102", "elsewhere:@4.%2", "idle", "ghost", 1),
		// pane 이 이 서버에 없다.
		"103.json": sessionFile(103, "s-103", "gone:@9.%9", "idle", "gone", 1),
		// tmux 밖의 Claude.
		"104.json": sessionFile(104, "s-104", "", "idle", "herdr", 1),
		"105.json": sessionFile(105, "s-105", "work:@2.%3", "waiting", "exec", 1),
	}
	panes := "%1 5001 renamed\n%2 5002 shells\n%3 5003 work\n"
	withAgentServer(t, files, panes, func(*mockRunner) {
		pidInPane = func(pid, panePID int) (bool, bool) {
			if pid == 102 {
				return false, true // 확인했는데 그 pane 아래에 없다
			}
			return true, true
		}
		agents, err := Agents()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, a := range agents {
			got = append(got, a.PaneID+"="+a.Session+":"+a.Name+":"+string(a.State))
		}
		want := "%1=renamed:plan:working %3=work:exec:blocked"
		if strings.Join(got, " ") != want {
			t.Errorf("Agents = %v, want %s", got, want)
		}
		if agents[1].WaitingFor != "approve Bash" {
			t.Errorf("blocked 의 WaitingFor = %q", agents[1].WaitingFor)
		}
		if agents[0].WaitingFor != "" {
			t.Error("blocked 가 아닌데 WaitingFor 가 있다")
		}
	})
}

func TestAgentsWithoutServer(t *testing.T) {
	withAgentServer(t, map[string]string{"1.json": sessionFile(1, "s", "a:@1.%1", "idle", "n", 1)}, "", func(*mockRunner) {
		if _, err := Agents(); !errors.Is(err, ErrNoServer) {
			t.Errorf("서버가 없을 때 err = %v", err)
		}
	})
}

func TestAgentsPrefersTheProcessUnderThePane(t *testing.T) {
	files := map[string]string{
		"201.json": sessionFile(201, "s-201", "a:@1.%1", "idle", "older-but-real", 100),
		"202.json": sessionFile(202, "s-202", "a:@1.%1", "idle", "newer-unknown", 200),
	}
	withAgentServer(t, files, "%1 5001 a\n", func(*mockRunner) {
		pidInPane = func(pid, panePID int) (bool, bool) {
			if pid == 201 {
				return true, true
			}
			return false, false // 모른다 — 걸러지지는 않는다
		}
		agents, _ := Agents()
		if len(agents) != 1 || agents[0].Name != "older-but-real" {
			t.Errorf("Agents = %+v — pane 아래에 있다고 확인된 쪽이 이겨야 한다", agents)
		}
	})
}

func TestAgentStateWords(t *testing.T) {
	old := shellJobs
	defer func() { shellJobs = old }()
	shellJobs = func(int) []string { return nil } // 백그라운드 셸에 아무것도 없다 → shell 그대로
	tests := map[string]AgentState{
		"busy": AgentWorking, "waiting": AgentBlocked, "idle": AgentIdle, "shell": AgentShell, "compacting": AgentUnknown,
	}
	for status, want := range tests {
		if got := agentStateOf(status, 1); got != want {
			t.Errorf("agentStateOf(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestResolveAgent(t *testing.T) {
	agents := []Agent{
		{PaneID: "%1", Session: "solo", Name: "alpha", State: AgentIdle},
		{PaneID: "%2", Session: "pair", Name: "mbl-plan", State: AgentIdle},
		{PaneID: "%3", Session: "pair", Name: "mbl-exec", State: AgentWorking},
		{PaneID: "%4", Session: "other", Name: "mbl-plan", State: AgentIdle},
		{PaneID: "%5", Session: "beta", Name: "x", State: AgentIdle},
		{PaneID: "%6", Session: "y", Name: "beta", State: AgentIdle},
	}
	tests := []struct {
		target, want string
		err          error
	}{
		{"%1", "%1", nil},
		{"%9", "", ErrNoAgent},
		{"solo", "%1", nil},     // Claude 가 하나인 세션
		{"alpha", "%1", nil},    // 이름
		{"mbl-exec", "%3", nil}, // 짝 합친 창의 이름
		{"pair", "", ErrAmbiguous},
		{"mbl-plan", "", ErrAmbiguous}, // 같은 이름이 두 프로젝트에
		{"beta", "", ErrAmbiguous},     // 한쪽은 세션 이름, 한쪽은 Claude 이름 — 우선순위 없음
		{"nobody", "", ErrNoAgent},
	}
	for _, tt := range tests {
		a, err := ResolveAgent(agents, tt.target)
		if tt.err != nil {
			if !errors.Is(err, tt.err) {
				t.Errorf("%s: err = %v, want %v", tt.target, err, tt.err)
			}
			continue
		}
		if err != nil || a.PaneID != tt.want {
			t.Errorf("%s: = %s, %v; want %s", tt.target, a.PaneID, err, tt.want)
		}
	}
	_, err := ResolveAgent(agents, "mbl-plan")
	for _, want := range []string{"%2", "%4", "session=pair", "session=other", "(name)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("모호함 오류에 %q 가 없다:\n%v", want, err)
		}
	}
}

func TestAgentTokenRoundTrip(t *testing.T) {
	tok := AgentToken{SessionID: "c0333750-bb03", SentAt: time.UnixMilli(1793589532061)}
	if tok.String() != "c0333750-bb03@1793589532061" {
		t.Errorf("String = %q", tok.String())
	}
	back, err := ParseAgentToken(" " + tok.String() + "\n")
	if err != nil || back != tok {
		t.Errorf("ParseAgentToken = %+v, %v", back, err)
	}
	for _, bad := range []string{"", "nosep", "@123", "sid@", "sid@abc", "sid@-5"} {
		if _, err := ParseAgentToken(bad); err == nil {
			t.Errorf("ParseAgentToken(%q) 가 통과했다", bad)
		}
	}
}

func TestCheckTokenChain(t *testing.T) {
	a := Agent{SessionID: "B"}
	tok := AgentToken{SessionID: "A"}
	if err := checkTokenChain(a, tok, []string{"A", "B"}); err != nil {
		t.Errorf("compact 로 이어진 대화를 거부했다: %v", err)
	}
	if err := checkTokenChain(a, tok, []string{"A"}); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("다른 대화를 받아들였다: %v", err)
	}
}

func TestAgentTokenCarriesTheTextHash(t *testing.T) {
	tok := AgentToken{SessionID: "s", SentAt: time.UnixMilli(1793589532061), TextHash: promptHash("hello")}
	back, err := ParseAgentToken(tok.String())
	if err != nil || back != tok {
		t.Errorf("ParseAgentToken(%q) = %+v, %v", tok.String(), back, err)
	}
	if _, err := ParseAgentToken("s@1793589532061#zz"); err == nil {
		t.Error("깨진 해시가 통과했다")
	}
	if back, err := ParseAgentToken("s@1793589532061"); err != nil || back.TextHash != 0 {
		t.Errorf("해시 없는 토큰 = %+v, %v", back, err)
	}
}

func TestRefreshAgentSurvivesATornRead(t *testing.T) {
	// Claude 는 상태 파일을 제자리에 덮어써서 반쯤 쓰인 내용이 읽힌다. 살아 있으면 끝난 것이 아니다.
	withClaudeSessions(t, map[string]string{"300.json": `{"pid":300,"sessionId":"s-3`}, nil, func() {
		a := Agent{PaneID: "%5", PID: 300, State: AgentWorking, SessionID: "s-300"}
		got, err := refreshAgent(a)
		if err != nil || got.State != AgentWorking {
			t.Errorf("refreshAgent = %+v, %v — 이전 상태를 그대로 둬야 한다", got, err)
		}
	})
	withClaudeSessions(t, map[string]string{"300.json": `{"pid":300,"sessionId":"s-3`}, func(int, string) bool { return false }, func() {
		if _, err := refreshAgent(Agent{PaneID: "%5", PID: 300}); !errors.Is(err, ErrAgentGone) {
			t.Errorf("죽은 프로세스: err = %v", err)
		}
	})
}
