package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// ls --json 의 스키마 핀: 스크립트(회차 루프)가 이 필드 이름으로 읽는다.
func TestAgentsJSON(t *testing.T) {
	b, err := agentsJSON(nil, "")
	if err != nil || string(b) != "[]" {
		t.Errorf("빈 목록 = %s, %v; want []", b, err)
	}

	b, err = agentsJSON([]tmux.Agent{
		{PaneID: "%12", Session: "pair", Tool: "claude", State: tmux.AgentBlocked, Since: time.UnixMilli(1793589532061),
			WaitingFor: "approve Bash", CWD: "/home/u/proj", Name: "mbl-plan", SessionID: "s-1", PID: 10499},
		{PaneID: "%6", Session: "my-mux", Tool: "claude", State: tmux.AgentIdle, CWD: "/x", Name: "me", SessionID: "s-2", PID: 7},
	}, "%6")
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"pane":"%12","session":"pair","tool":"claude","state":"blocked","since":1793589532061,"waitingFor":"approve Bash","cwd":"/home/u/proj","name":"mbl-plan","sessionId":"s-1","pid":10499},` +
		`{"pane":"%6","session":"my-mux","tool":"claude","state":"idle","cwd":"/x","name":"me","sessionId":"s-2","pid":7,"self":true}]`
	if string(b) != want {
		t.Errorf("agentsJSON =\n%s\nwant\n%s", b, want)
	}
}

func TestAgentTableAlignsWideNames(t *testing.T) {
	var out bytes.Buffer
	now := time.UnixMilli(1793589600000)
	writeAgentTable(&out, []tmux.Agent{
		{PaneID: "%2", Session: "kream", State: tmux.AgentWorking, Since: now.Add(-90 * time.Second), Name: "실행_크림", CWD: "/a"},
		{PaneID: "%6", Session: "my-mux", State: tmux.AgentIdle, Name: "mux-api", CWD: "/b"},
	}, "%6", now)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "%6*") {
		t.Fatalf("표:\n%s", out.String())
	}
	// 한글 이름 뒤의 CWD 열이 같은 화면 칸에서 시작해야 한다.
	col := func(line, cell string) int { return displayWidth(line[:strings.Index(line, cell)]) }
	if a, b := col(lines[1], "/a"), col(lines[2], "/b"); a != b {
		t.Errorf("CWD 열이 어긋났다 (%d vs %d):\n%s", a, b, out.String())
	}
	if !strings.Contains(lines[1], "1m") {
		t.Errorf("경과 시간이 없다: %q", lines[1])
	}
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 {
			w += 2 // 이 테스트의 한글
		} else {
			w++
		}
	}
	return w
}

func TestAgentExitCodes(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("usage"), 1},
		{tmux.ErrAmbiguous, 1},
		{tmux.ErrPromptUnknown, 1},
		{tmux.ErrTokenMismatch, 1},
		{tmux.ErrNoReply, 2},
		{tmux.ErrAgentBlocked, 3},
		{tmux.ErrWaitTimeout, 4},
		{tmux.ErrAgentNotReady, 5},
		{tmux.ErrTurnRunning, 5},
		{tmux.ErrNotSubmitted, 6},
		{tmux.ErrPromptNotSeen, 7}, // 제출은 됐을 수 있다 — 6(다시 보내도 됨)과 섞으면 두 번 제출된다
		// tmux 가 감싸서 돌려줘도 같은 코드여야 한다.
		{fmt.Errorf("%w: %%5 (approve)", tmux.ErrAgentBlocked), 3},
	}
	for _, tt := range tests {
		if got := exitCode(tt.err); got != tt.want {
			t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
