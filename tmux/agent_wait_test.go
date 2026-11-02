package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitStep(t *testing.T) {
	base := time.UnixMilli(1793589600000)
	at := func(sec float64) time.Time { return base.Add(time.Duration(sec * float64(time.Second))) }
	tests := []struct {
		name string
		in   waitInput
		want waitDecision
	}{
		{"working", waitInput{State: AgentWorking}, waitContinue},
		{"shell keeps waiting", waitInput{State: AgentShell}, waitContinue},
		{"blocked", waitInput{State: AgentBlocked}, waitBlocked},
		{"idle without --after", waitInput{State: AgentIdle}, waitDone},
		{"no transcript, idle began after sending", waitInput{State: AgentIdle, HaveAfter: true, After: at(0), Since: at(3)}, waitDone},
		{"no transcript, stale idle", waitInput{State: AgentIdle, HaveAfter: true, After: at(0), Since: at(-5)}, waitContinue},
		{"prompt seen, idle after its turn began", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0),
			PromptSeen: true, PromptAt: at(0.01), Since: at(4)}, waitDone},
		// --force 로 작업 중에 보낸 글: 앞 턴이 끝난 idle 은 보낸 시각보다 늦지만 프롬프트 턴보다는 이르다.
		{"queued prompt, idle of the previous turn", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0),
			PromptSeen: true, PromptAt: at(9), Since: at(9)}, waitContinue},
		// 상태가 굳어 statusUpdatedAt 이 움직이지 않아도, 기록상 턴이 끝났으면 끝이다.
		{"stuck status but the turn ended", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0),
			PromptSeen: true, PromptAt: at(0.01), Since: at(-60), TurnEnded: true}, waitDone},
		{"prompt not seen yet", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0), Since: at(-5), Now: at(10)}, waitContinue},
		{"prompt never landed", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0), Since: at(-5), Now: at(16)}, waitNotSeen},
		// 앞 턴이 길었어도 idle 이 막 시작됐으면 기록이 따라올 시간을 준다.
		{"long previous turn, idle just began", waitInput{State: AgentIdle, HaveAfter: true, TranscriptFound: true, After: at(0), Since: at(60), Now: at(65)}, waitContinue},
	}
	for _, tt := range tests {
		if got := waitStep(tt.in); got != tt.want {
			t.Errorf("%s: waitStep = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// writeTranscript 는 임시 HOME 에 대상(s-300, /home/u/proj)의 대화 기록을 쓴다.
func writeTranscript(t *testing.T, content string) {
	t.Helper()
	home, _ := homeDir()
	p := filepath.Join(home, claudeDir, "projects", encodePath("/home/u/proj"), "s-300.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// scriptSleep 은 가짜 시계의 n 번째 잠마다 할 일을 건다.
func scriptSleep(steps map[int]func()) {
	inner := agentSleep
	n := 0
	agentSleep = func(d time.Duration) {
		inner(d)
		n++
		if f, ok := steps[n]; ok {
			f()
		}
	}
}

func isoAt(tm time.Time) string { return tm.UTC().Format("2006-01-02T15:04:05.000Z") }

func TestWaitIgnoresTheIdleLeftFromBeforeThePrompt(t *testing.T) {
	// 보내자마자는 아직 지난 턴의 idle 이다(회차 루프가 0.07초 만에 끝나 버린 그 경우).
	// 프롬프트가 기록에 오르고, busy 를 거쳐 다시 idle 이 될 때까지 기다려야 한다.
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		sent := agentNow()
		tok := AgentToken{SessionID: "s-300", SentAt: sent}
		writeTranscript(t, "")
		scriptSleep(map[int]func(){
			2: func() {
				writeTranscript(t, jsonl(t, humanTurn(isoAt(sent.Add(20*time.Millisecond)), 1, "ours")))
				rewriteState(t, "busy", agentNow().UnixMilli())
			},
			10: func() {
				writeTranscript(t, jsonl(t,
					humanTurn(isoAt(sent.Add(20*time.Millisecond)), 1, "ours"),
					assistantRow(isoAt(agentNow()), "r1", "end_turn", text("done")),
				))
				rewriteState(t, "idle", agentNow().UnixMilli())
			},
		})
		if _, err := WaitAgent(a, &tok, time.Minute); err != nil {
			t.Fatal(err)
		}
		if waited := agentNow().Sub(sent); waited < 10*waitPoll {
			t.Errorf("%v 만에 끝났다 — 지난 턴의 idle 에서 멈췄다", waited)
		}
	})
}

func TestWaitStopsOnBlockedAndTimesOut(t *testing.T) {
	withPromptPane(t, "waiting", &fakePane{}, func(a Agent) {
		if _, err := WaitAgent(a, nil, time.Minute); !errors.Is(err, ErrAgentBlocked) {
			t.Errorf("blocked: err = %v", err)
		}
	})
	withPromptPane(t, "busy", &fakePane{}, func(a Agent) {
		start := agentNow()
		if _, err := WaitAgent(a, nil, 2*time.Second); !errors.Is(err, ErrWaitTimeout) {
			t.Errorf("timeout: err = %v", err)
		}
		if d := agentNow().Sub(start); d < 2*time.Second || d > 3*time.Second {
			t.Errorf("timeout 2s 에 %v 기다렸다", d)
		}
	})
}

func TestWaitRefusesAnotherConversation(t *testing.T) {
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		writeTranscript(t, "")
		tok := AgentToken{SessionID: "s-300", SentAt: agentNow()}
		a.SessionID = "s-300"
		// 대상이 /clear 로 다른 대화가 됐다: 상태 파일의 대화 id 가 바뀌었다.
		home, _ := homeDir()
		content := sessionFile(300, "s-new", "w:@1.%5", "idle", "target", 1)
		if err := os.WriteFile(filepath.Join(home, claudeDir, sessionsDir, "300.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WaitAgent(a, &tok, time.Minute); !errors.Is(err, ErrTokenMismatch) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestReadReplyAfterToken(t *testing.T) {
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		sent := agentNow()
		writeTranscript(t, jsonl(t,
			humanTurn(isoAt(sent.Add(-time.Minute)), 1, "earlier"),
			assistantRow(isoAt(sent.Add(-50*time.Second)), "r0", "end_turn", text("old reply")),
			humanTurn(isoAt(sent.Add(10*time.Millisecond)), 2, "ours"),
			assistantRow(isoAt(sent.Add(time.Second)), "r1", "end_turn", text("new reply")),
		))
		tok := AgentToken{SessionID: "s-300", SentAt: sent}
		got, err := ReadAgentReply(a, &tok)
		if err != nil || got != "new reply" {
			t.Errorf("read --after = %q, %v", got, err)
		}
		got, err = ReadAgentReply(a, nil)
		if err != nil || got != "new reply" {
			t.Errorf("read (마지막 턴) = %q, %v", got, err)
		}
	})
}

func TestReadWhileTheTurnRuns(t *testing.T) {
	withPromptPane(t, "busy", &fakePane{}, func(a Agent) {
		sent := agentNow()
		writeTranscript(t, jsonl(t,
			humanTurn(isoAt(sent.Add(10*time.Millisecond)), 1, "ours"),
			assistantRow(isoAt(sent.Add(time.Second)), "r1", "tool_use", text("돌려 본다"), toolUse()),
		))
		tok := AgentToken{SessionID: "s-300", SentAt: sent}
		if _, err := ReadAgentReply(a, &tok); !errors.Is(err, ErrTurnRunning) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestReadNoReplyDoesNotWaitOnAnOldIdle(t *testing.T) {
	// 상태 파일의 idle 이 오래됐으면(픽스처는 보낸 시각보다 1분 앞) 기록이 늦을 일이 없다 — 바로 2.
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		start := agentNow()
		writeTranscript(t, jsonl(t,
			humanTurn(isoAt(start.Add(-2*time.Minute)), 1, "ours"),
			assistantRow(isoAt(start.Add(-110*time.Second)), "r1", "end_turn", thinking()),
		))
		if _, err := ReadAgentReply(a, nil); !errors.Is(err, ErrNoReply) {
			t.Errorf("err = %v", err)
		}
		if agentNow() != start {
			t.Error("오래된 idle 인데 기록을 기다렸다")
		}
	})
}

func TestReadWaitsForALaggingTranscript(t *testing.T) {
	// 막 idle 이 됐는데 회신 줄이 아직 안 쓰였다 — 몇 번 다시 읽는다.
	withPromptPane(t, "idle", &fakePane{}, func(a Agent) {
		sent := agentNow()
		rewriteState(t, "idle", sent.Add(time.Second).UnixMilli())
		prompt := humanTurn(isoAt(sent.Add(10*time.Millisecond)), 1, "ours")
		writeTranscript(t, jsonl(t, prompt))
		scriptSleep(map[int]func(){
			2: func() {
				writeTranscript(t, jsonl(t, prompt, assistantRow(isoAt(agentNow()), "r1", "end_turn", text("late reply"))))
			},
		})
		tok := AgentToken{SessionID: "s-300", SentAt: sent}
		got, err := ReadAgentReply(a, &tok)
		if err != nil || got != "late reply" {
			t.Errorf("read = %q, %v", got, err)
		}
	})
}
