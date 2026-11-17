package tmux

import (
	"errors"
	"testing"
	"time"
)

// ReadLastTurn 은 mux serve 의 대화 탭이다(docs/design/serve.md). 회신 규칙은 ReadAgentReply 와 같고,
// 그 턴을 연 «보낸 말» 을 함께 돌려준다 — 오류로 회신이 없을 때도.

func TestReadLastTurnPairsThePromptWithItsReply(t *testing.T) {
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		at := agentNow().Add(-2 * time.Minute)
		writeTranscript(t, jsonl(t,
			humanTurn(isoAt(at.Add(-time.Minute)), 1, "earlier"),
			assistantRow(isoAt(at.Add(-50*time.Second)), "r0", "end_turn", text("old reply")),
			humanTurn(isoAt(at), 2, "ours"),
			assistantRow(isoAt(at.Add(time.Second)), "r1", "tool_use", text("확인해 보겠다"), toolUse()),
			toolResult(isoAt(at.Add(2*time.Second))),
			assistantRow(isoAt(at.Add(3*time.Second)), "r2", "end_turn", text("new reply")),
		))
		turn, err := ReadLastTurn(a)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Prompt != "ours" || !turn.Human || turn.Reply != "new reply" {
			t.Errorf("turn = %+v", turn)
		}
		if turn.PromptAt.UnixMilli() != at.UnixMilli() || turn.ReplyAt.UnixMilli() != at.Add(3*time.Second).UnixMilli() {
			t.Errorf("시각 = %v / %v, want %v / %v", turn.PromptAt, turn.ReplyAt, at, at.Add(3*time.Second))
		}
	})
}

func TestReadLastTurnKeepsThePromptWhenThereIsNoReplyYet(t *testing.T) {
	tests := []struct {
		name   string
		status string
		last   map[string]any
		want   error
	}{
		{"작업 중", "busy", assistantRow("", "r1", "tool_use", text("돌려 본다"), toolUse()), ErrTurnRunning},
		{"승인 대기", "waiting", assistantRow("", "r1", "tool_use", toolUse()), ErrAgentBlocked},
		// 마지막 그룹에 글이 없다 — 앞 그룹의 중간 문장으로 물러서지 않는다(agent-api.md).
		{"회신 글 없음", "idle", assistantRow("", "r1", "end_turn", thinking()), ErrNoReply},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withPromptPane(t, tt.status, &fakePane{}, func(a Agent) {
				at := agentNow().Add(-2 * time.Minute)
				tt.last["timestamp"] = isoAt(at.Add(time.Second))
				writeTranscript(t, jsonl(t, humanTurn(isoAt(at), 1, "ours"), tt.last))
				turn, err := ReadLastTurn(a)
				if !errors.Is(err, tt.want) {
					t.Errorf("err = %v, want %v", err, tt.want)
				}
				if turn.Prompt != "ours" || turn.Reply != "" {
					t.Errorf("turn = %+v — 보낸 말만 있어야 한다", turn)
				}
			})
		})
	}
}

func TestReadLastTurnSaysWhenNoHumanOpenedIt(t *testing.T) {
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		at := agentNow().Add(-2 * time.Minute)
		writeTranscript(t, jsonl(t,
			humanTurn(isoAt(at.Add(-time.Minute)), 1, "ours"),
			assistantRow(isoAt(at.Add(-50*time.Second)), "r0", "end_turn", text("reply to ours")),
			metaTurn(isoAt(at), 2, "<task-notification>빌드가 끝났다</task-notification>"),
			assistantRow(isoAt(at.Add(time.Second)), "r1", "end_turn", text("빌드 결과를 확인했다")),
		))
		turn, err := ReadLastTurn(a)
		if err != nil {
			t.Fatal(err)
		}
		// 마지막 턴은 작업 알림이 열었다 — 사람의 앞 턴으로 물러서지 않고, 사람 턴이 아니라고 말한다.
		if turn.Human || turn.Reply != "빌드 결과를 확인했다" {
			t.Errorf("turn = %+v", turn)
		}
	})
}

func TestTurnPromptText(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{"그대로", "테스트 돌려 줘", "테스트 돌려 줘"},
		{"여러 줄은 남긴다", "첫 줄\n둘째 줄", "첫 줄\n둘째 줄"},
		{"인자 붙은 슬래시 명령",
			"<command-message>govgen-reply</command-message>\n<command-name>/govgen-reply</command-name>\n<command-args>~/r/G-3.md</command-args>",
			"/govgen-reply ~/r/G-3.md"},
		{"인자 없는 슬래시 명령", "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>", "/clear"},
		{"끼워 넣은 블록", "고쳐 줘<system-reminder>숨은 글</system-reminder>", "고쳐 줘"},
		{"붙여 넣은 글의 태그", "<pasted_content>로그</pasted_content> 봐 줘", "로그 봐 줘"},
		// 화면을 망가뜨리는 이스케이프와 줄 순서를 뒤집는 방향 문자는 걷는다.
		{"이스케이프와 방향 문자", "\x1b[31m빨강\x1b[0m‮뒤집기", "빨강뒤집기"},
		{"앞뒤 공백", "  \n 글 \n", "글"},
	}
	for _, tt := range tests {
		if got := turnPromptText(tt.raw); got != tt.want {
			t.Errorf("%s: turnPromptText(%q) = %q, want %q", tt.name, tt.raw, got, tt.want)
		}
	}
}
