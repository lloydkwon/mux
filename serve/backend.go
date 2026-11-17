package serve

import "github.com/lloydkwon/mux/tmux"

// backend 는 핸들러가 tmux 에서 읽고 쓰는 것 전부다. 테스트는 가짜를 넣고, 진짜 배선은 e2e 가 본다 —
// ListSessions 하나가 tmux·pgrep·ps·git 을 여러 번 불러, 러너 목으로는 핸들러의 뜻보다 목 키가 커진다.
type backend interface {
	Sessions() ([]tmux.Session, error)
	Agents() ([]tmux.Agent, error)
	Prompt(a tmux.Agent, text string, force bool) error
	LastTurn(a tmux.Agent) (tmux.LastTurn, error)
	Capture(target string) (string, error)
	// 대화상자에 답하기 — 누르기 직전에 화면을 다시 읽어, 폰이 본 대화상자(key)와 선택지일 때만 누른다
	// (tmux/agent_dialog.go).
	Dialog(a tmux.Agent) (tmux.Dialog, error)
	Answer(a tmux.Agent, key string, n int, label, text string) error
	AnswerMulti(a tmux.Agent, key string, want []tmux.MultiChoice, text string) error
	Cancel(a tmux.Agent, key string) error
	// 보관함(tmux/park.go). 폰은 되살리기만 한다 — 보관(세션을 닫는다)과 지우기(대화로 가는 길을 지운다)는 TUI 의 일이다.
	Parked() ([]tmux.ParkedSession, error)
	Unpark(p tmux.ParkedSession) (string, error)
}

// tmuxBackend 는 tmux 패키지를 그대로 부른다. 여기서 아무것도 다시 구현하지 않는다.
type tmuxBackend struct{}

// Sessions 는 패널의 loadWatchSessions 와 같다. WithClaudeWork 는 저장된 제목만 읽는다 — 폰을 열어 둔 것만으로
// 제목 모델(Haiku)이 불리면 안 된다(TitleNextClaudeWork 는 패널만 부른다).
func (tmuxBackend) Sessions() ([]tmux.Session, error) {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return nil, err
	}
	return tmux.WithClaudeWork(sessions), nil
}

func (tmuxBackend) Agents() ([]tmux.Agent, error) { return tmux.Agents() }

func (tmuxBackend) Prompt(a tmux.Agent, text string, force bool) error {
	_, err := tmux.PromptAgent(a, text, force)
	return err
}

func (tmuxBackend) LastTurn(a tmux.Agent) (tmux.LastTurn, error) { return tmux.ReadLastTurn(a) }

func (tmuxBackend) Capture(target string) (string, error) { return tmux.CapturePaneTarget(target) }

func (tmuxBackend) Dialog(a tmux.Agent) (tmux.Dialog, error) { return tmux.ReadDialog(a) }

func (tmuxBackend) Answer(a tmux.Agent, key string, n int, label, text string) error {
	return tmux.AnswerDialog(a, key, n, label, text)
}

func (tmuxBackend) AnswerMulti(a tmux.Agent, key string, want []tmux.MultiChoice, text string) error {
	return tmux.AnswerMulti(a, key, want, text)
}

func (tmuxBackend) Cancel(a tmux.Agent, key string) error { return tmux.CancelDialog(a, key) }

func (tmuxBackend) Parked() ([]tmux.ParkedSession, error) { return tmux.LoadParked() }

// Unpark 는 TUI 의 보관함 enter 와 같은 RestoreParked 다. attach 만 없다 — 폰은 세션에 붙지 않는다.
func (tmuxBackend) Unpark(p tmux.ParkedSession) (string, error) { return tmux.RestoreParked(p) }
