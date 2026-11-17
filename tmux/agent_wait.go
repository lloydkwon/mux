package tmux

import (
	"fmt"
	"time"
)

const (
	// waitPoll 은 wait 가 상태 파일을 읽는 간격이다. 파일 하나라 tmux 호출이 없다.
	waitPoll = 300 * time.Millisecond
	// waitTranscriptEvery 는 대화 기록을 다시 읽는 최소 간격이다(수 MB 파일이 있다).
	waitTranscriptEvery = 1 * time.Second
	// promptLandGrace 는 idle 인데 프롬프트가 기록에 없을 때 기다려 주는 시간이다.
	// 넘으면 프롬프트가 닿지 않은 것으로 본다.
	promptLandGrace = 15 * time.Second
	// readLag* — 대화 기록은 턴이 끝난 뒤 조금 늦게 쓰인다. 회차 루프가 2초 간격 8번으로 잡았다.
	readLagWindow  = 30 * time.Second
	readLagRetries = 8
	readLagPoll    = 2 * time.Second
)

// waitInput 은 wait 가 한 번 판단하는 데 쓰는 사실들이다 — waitStep 을 순수 함수로 두어
// 경계를 표로 시험한다.
type waitInput struct {
	State AgentState
	Since time.Time
	Now   time.Time

	HaveAfter       bool
	After           time.Time
	TranscriptFound bool
	PromptSeen      bool
	PromptAt        time.Time // 기록에 프롬프트 턴이 시작된 시각
	TurnEnded       bool
}

type waitDecision int

const (
	waitContinue waitDecision = iota
	waitDone
	waitBlocked
	waitNotSeen
)

// waitStep 은 지금 끝났는지 정한다.
//
// --after 가 있으면 «그 프롬프트의 턴» 이 끝나야 한다. 기준 시각은 보낸 시각이 아니라 기록에
// 프롬프트 턴이 시작된 시각이다: --force 로 작업 중에 보낸 글은 앞 턴이 끝난 뒤에야 턴이
// 되는데(실측), 그 사이 잠깐의 idle 도 보낸 시각보다는 늦기 때문이다. 상태가 굳어
// statusUpdatedAt 이 움직이지 않는 경우(shell 이 idle 로 강등)를 위해 기록상의 턴 끝도 받는다.
func waitStep(in waitInput) waitDecision {
	switch in.State {
	case AgentBlocked:
		return waitBlocked
	case AgentIdle:
	default:
		return waitContinue
	}
	if !in.HaveAfter {
		return waitDone
	}
	if !in.TranscriptFound {
		// 기록이 없으면 상태 파일만으로: 보낸 뒤에 시작된 idle 이면 끝이다.
		if !in.Since.Before(in.After) {
			return waitDone
		}
		return waitContinue
	}
	if in.PromptSeen {
		if in.Since.After(in.PromptAt) || in.TurnEnded {
			return waitDone
		}
		return waitContinue
	}
	ref := in.After
	if in.Since.After(ref) {
		ref = in.Since
	}
	if in.Now.Sub(ref) > promptLandGrace {
		return waitNotSeen
	}
	return waitContinue
}

// WaitAgent 는 대상이 끝나기를 기다린다. after 가 nil 이면 «작업 중이 아님(idle)» 까지,
// 있으면 그 프롬프트의 턴이 끝날 때까지. timeout 0 은 끝없이.
func WaitAgent(a Agent, after *AgentToken, timeout time.Duration) (Agent, error) {
	start := agentNow()
	var (
		lastRead time.Time
		in       waitInput
	)
	for {
		fresh, err := refreshAgent(a)
		if err != nil {
			return a, err
		}
		a = fresh
		now := agentNow()
		in.State, in.Since, in.Now = a.State, a.Since, now
		if after != nil && a.State == AgentIdle && (lastRead.IsZero() || now.Sub(lastRead) >= waitTranscriptEvery) {
			lastRead = now
			in.HaveAfter, in.After = true, after.SentAt
			rows, ids, terr := transcriptChain(after.SessionID, a.CWD)
			in.TranscriptFound = terr == nil
			in.PromptSeen, in.TurnEnded = false, false
			if terr == nil {
				if err := checkTokenChain(a, *after, ids); err != nil {
					return a, err
				}
				if span, ok := findTurn(rows, &after.SentAt, after.TextHash); ok {
					in.PromptSeen = true
					in.PromptAt = rows[span.start].at
					in.TurnEnded = turnEnded(rows, span)
				}
			}
		}
		switch waitStep(in) {
		case waitDone:
			return a, nil
		case waitBlocked:
			return a, fmt.Errorf("%w: %s (%s)", ErrAgentBlocked, a.PaneID, a.WaitingFor)
		case waitNotSeen:
			return a, fmt.Errorf("%w: %s", ErrPromptNotSeen, a.PaneID)
		}
		if timeout > 0 && now.Sub(start) >= timeout {
			return a, fmt.Errorf("%w: %s 는 %s 상태다", ErrWaitTimeout, a.PaneID, a.State)
		}
		agentSleep(waitPoll)
	}
}

// ReadAgentReply 는 대상의 회신 본문을 읽는다. after 가 있으면 그 프롬프트의 턴, 없으면
// 마지막 턴이다. 회신은 그 턴의 마지막 응답 그룹이고, 글이 없으면 ErrNoReply 다.
//
// 대화 기록은 턴이 끝난 뒤 조금 늦게 쓰인다. idle 이 된 지 얼마 안 됐으면 몇 번 다시 읽는다.
func ReadAgentReply(a Agent, after *AgentToken) (string, error) {
	turn, err := readTurn(a, after)
	return turn.Reply, err
}

// LastTurn 은 한 턴의 «보낸 말» 과 «회신» 이다 — mux serve 의 대화 탭이 쓴다(docs/design/serve.md).
type LastTurn struct {
	// Prompt 는 턴을 연 줄의 글이다. 슬래시 명령은 «/이름 인자» 로 되돌리고, 끼워 넣은 블록과
	// 이스케이프는 걷는다(turnPromptText).
	Prompt   string
	PromptAt time.Time
	// Human 은 사람이(mux agent prompt 도 같다) 친 턴인가다. 아니면 작업 알림·peer·system 이 연 턴이다.
	Human   bool
	Reply   string
	ReplyAt time.Time
}

// ReadLastTurn 은 마지막 턴의 보낸 말과 회신을 함께 읽는다. 회신 규칙은 ReadAgentReply 와 같다 — 진행
// 중이면 ErrTurnRunning, 막혔으면 ErrAgentBlocked, 마지막 그룹에 글이 없으면 ErrNoReply 이고, 앞 턴이나
// 앞 그룹으로 물러서지 않는다. 그런 오류에서도 턴을 찾았으면 보낸 말은 채워서 돌려준다.
func ReadLastTurn(a Agent) (LastTurn, error) {
	return readTurn(a, nil)
}

// readTurn 은 ReadAgentReply 와 ReadLastTurn 이 같이 쓰는 읽기다 — 늦게 쓰이는 기록을 기다리는 규칙이 둘에서
// 갈라지지 않게.
func readTurn(a Agent, after *AgentToken) (LastTurn, error) {
	for try := 0; ; try++ {
		fresh, err := refreshAgent(a)
		if err != nil {
			return LastTurn{}, err
		}
		a = fresh
		turn, lagged, err := readTurnOnce(a, after)
		recent := a.State == AgentIdle && agentNow().Sub(a.Since) < readLagWindow
		if err != nil && lagged && recent && try < readLagRetries {
			agentSleep(readLagPoll)
			continue
		}
		return turn, err
	}
}

// readTurnOnce 는 기록을 한 번 읽어 턴을 꺼낸다. lagged 는 «기록이 아직 따라오지 않았을
// 수 있다» 는 뜻이다 — 프롬프트 턴이 아직 없거나, 턴 뒤에 다른 턴이 없는데 회신 글이 없다.
func readTurnOnce(a Agent, after *AgentToken) (turn LastTurn, lagged bool, err error) {
	sid, at, hash := a.SessionID, (*time.Time)(nil), uint32(0)
	if after != nil {
		sid, at, hash = after.SessionID, &after.SentAt, after.TextHash
	}
	rows, ids, err := transcriptChain(sid, a.CWD)
	if err != nil {
		return LastTurn{}, false, fmt.Errorf("%s 의 대화 기록: %w", a.PaneID, err)
	}
	if after != nil {
		if err := checkTokenChain(a, *after, ids); err != nil {
			return LastTurn{}, false, err
		}
	}
	span, ok := findTurn(rows, at, hash)
	if !ok {
		if after != nil {
			return LastTurn{}, true, fmt.Errorf("%w: %s", ErrPromptNotSeen, a.PaneID)
		}
		return LastTurn{}, false, fmt.Errorf("%w: %s 의 기록에 턴이 없다", ErrNoReply, a.PaneID)
	}
	opener := rows[span.start]
	turn = LastTurn{
		Prompt:   turnPromptText(userText(opener.Message.Content)),
		PromptAt: opener.at,
		Human:    isHumanTurn(opener),
	}
	if span.open {
		switch a.State {
		case AgentBlocked:
			return turn, false, fmt.Errorf("%w: %s (%s)", ErrAgentBlocked, a.PaneID, a.WaitingFor)
		case AgentWorking, AgentShell, AgentUnknown:
			return turn, false, fmt.Errorf("%w: %s", ErrTurnRunning, a.PaneID)
		}
	}
	if text, final := turnReply(rows, span); final {
		turn.Reply, turn.ReplyAt = text, turnReplyAt(rows, span)
		return turn, false, nil
	}
	return turn, span.open, fmt.Errorf("%w: %s", ErrNoReply, a.PaneID)
}
