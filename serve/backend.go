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
