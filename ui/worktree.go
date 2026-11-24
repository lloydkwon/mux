package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// «New worktree» 행 — 브랜치 이름을 받아 원본 저장소 옆에 worktree 를 만들고, 그 폴더의 셸 세션으로 간다.
// git 은 tmux/worktree.go, 설계 이유는 docs/design/worktree.md.

// tmux 쪽 시임이다. ui 에는 목 러너가 없어서, 테스트는 이것을 바꿔 끼워 «Update 안에서는 부르지 않고 돌려준
// Cmd 에서만 부른다» 를 확인한다(park 와 같다).
var (
	prepareWorktree    = tmux.PrepareWorktree
	currentSessionName = tmux.CurrentSessionName
)

// worktreeErrWidth 는 입력 화면의 오류를 접는 폭이다. 오버레이는 줄을 접지 않아, git 의 긴 경로가 상자를
// 화면 밖으로 민다 — mux popup 의 가장 작은 안쪽 폭(68칸 − 테두리·여백 6) 안에 든다.
const worktreeErrWidth = 56

// newSessionRequest 는 TUI 가 끝난 뒤 cmd/mux 가 만들고 붙을 셸 세션이다. resumeRequest 와 같은 이유로
// 세션 생성과 attach 는 Bubble Tea 루프 밖에서 한다. @project_dir 태그는 달지 않는다 — 태그가 있으면
// 패널 자동 삽입이 물러난다(tmux-config.md).
type newSessionRequest struct {
	// Name 은 만들 세션 이름이다. 겹치면 만드는 쪽이 -2 를 붙인다.
	Name string
	Dir  string
}

// worktreeOriginMsg 는 mux 를 띄운 세션의 이름이다. Repo 칸을 채운다.
type worktreeOriginMsg struct{ session string }

// worktreeReadyMsg 는 git 쪽 일이 끝났다는 알림이다. id 는 그 일을 시킨 입력 화면이다.
type worktreeReadyMsg struct {
	id  int
	ws  tmux.WorktreeSession
	err error
}

type worktreeModel struct {
	branchInput textinput.Model
	repoInput   textinput.Model
	focused     int // 0=branch, 1=repo
	// busy 는 git 이 도는 동안이다. 그동안 enter 는 무시한다 — 두 번 누르면 두 번째가 «이미 있다» 로 거꾸로 보고한다.
	busy bool
	err  error
	// id 는 이 입력 화면을 연 차례다(Model.worktreeOpened). 기다리다 esc 로 닫고 다시 열면, 첫 화면이 시킨
	// 결과가 둘째 화면에 와서 거기서 세션을 열거나 오류를 보일 뻔했다 — 같은 id 의 결과만 이 화면의 것이다.
	id int
}

func newWorktreeModel(repo string) worktreeModel {
	branch := textinput.New()
	branch.Placeholder = "feat/name"
	branch.Focus()
	branch.CharLimit = 100
	branch.Width = 40

	r := textinput.New()
	r.Placeholder = "~/path/to/repo"
	r.CharLimit = 300
	r.Width = 40
	r.SetValue(repo)

	return worktreeModel{branchInput: branch, repoInput: r}
}

func (m worktreeModel) Update(msg tea.Msg) (worktreeModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.busy {
			return m, nil
		}
		switch key.String() {
		case "tab", "shift+tab":
			if m.focused == 0 {
				m.focused = 1
				m.branchInput.Blur()
				m.repoInput.Focus()
			} else {
				m.focused = 0
				m.repoInput.Blur()
				m.branchInput.Focus()
			}
			return m, nil
		case "enter":
			branch := strings.TrimSpace(m.branchInput.Value())
			if branch == "" {
				return m, nil
			}
			repo := strings.TrimSpace(m.repoInput.Value())
			m.busy = true
			m.err = nil
			// git 은 여기가 아니라 이 Cmd 안에서 돈다.
			id := m.id
			return m, func() tea.Msg {
				ws, err := prepareWorktree(repo, branch)
				return worktreeReadyMsg{id: id, ws: ws, err: err}
			}
		}
	}

	var cmd tea.Cmd
	if m.focused == 0 {
		m.branchInput, cmd = m.branchInput.Update(msg)
	} else {
		m.repoInput, cmd = m.repoInput.Update(msg)
	}
	return m, cmd
}

func (m worktreeModel) View() string {
	s := inputLabelStyle.Render("New Worktree") + "\n\n"
	s += inputLabelStyle.Render("Branch: ") + m.branchInput.View() + "\n"
	s += inputLabelStyle.Render("Repo:   ") + m.repoInput.View() + "\n\n"
	s += helpStyle.Render("made beside the repo as <repo>-<branch>, opened in a shell") + "\n"
	s += helpStyle.Render("tab switch • enter create • esc cancel")
	switch {
	case m.busy:
		s += "\n" + helpStyle.Render("creating worktree...")
	case m.err != nil:
		s += "\n" + errorStyle.Width(worktreeErrWidth).Render(m.err.Error())
	}
	return s
}

// sessionRepo 는 세션 name 의 저장소 후보다 — @project_dir 가 있으면 그것, 없으면 그 세션의 폴더.
// 목록에 없으면 빈 값이다.
func (m Model) sessionRepo(name string) string {
	for i := range m.sessions {
		if s := m.sessions[i]; name != "" && s.Name == name {
			if s.ProjectDir != "" {
				return s.ProjectDir
			}
			return s.Directory
		}
	}
	return ""
}

// openWorktree 는 입력 화면을 연다. Repo 칸은 VS Code 에서 연 목록이면 그 프로젝트이고, 아니면 mux 를 띄운
// 세션에서 온다 — 그 이름은 tmux 에 물어야 해서 Cmd 로 받는다(fillWorktreeRepo).
//
// 커서가 머문 세션으로 채우던 첫 안은 버렸다: 키보드로 이 행까지 올라오면 그 위의 세션을 모두 지나, 언제나
// 맨 위 세션이 되었다(TestWorktreePromptStartsFromTheSessionMuxWasOpenedFrom).
func (m Model) openWorktree() (tea.Model, tea.Cmd) {
	m.mode = modeWorktree
	m.worktreeOpened++
	m.worktreeMod = newWorktreeModel(m.projectDir)
	m.worktreeMod.id = m.worktreeOpened
	focus := m.worktreeMod.branchInput.Focus()
	if m.projectDir != "" {
		return m, focus
	}
	return m, tea.Batch(focus, func() tea.Msg {
		return worktreeOriginMsg{session: currentSessionName()}
	})
}

// fillWorktreeRepo 는 mux 를 띄운 세션의 저장소로 Repo 칸을 채운다. 그사이 사람이 친 것이 있으면 두고,
// 화면을 이미 닫았으면 아무것도 하지 않는다.
func (m Model) fillWorktreeRepo(msg worktreeOriginMsg) Model {
	if m.mode != modeWorktree || m.worktreeMod.repoInput.Value() != "" {
		return m
	}
	if repo := m.sessionRepo(msg.session); repo != "" {
		m.worktreeMod.repoInput.SetValue(repo)
	}
	return m
}

func (m Model) updateWorktree(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		// git 이 도는 중이어도 닫는다 — 늦게 온 결과는 finishWorktree 가 상태 줄에 알린다.
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.worktreeMod, cmd = m.worktreeMod.Update(msg)
	return m, cmd
}

// finishWorktree 는 git 쪽 결과를 받는다. 성공이면 대상을 적고 끝낸다 — 세션 생성과 attach 는 cmd/mux 가 한다.
func (m Model) finishWorktree(msg worktreeReadyMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeWorktree || msg.id != m.worktreeMod.id {
		// 기다리다 esc 로 닫았다(그 뒤 다시 열었을 수도 있다). 만든 것이 있으면 말없이 두지 않는다.
		if msg.err != nil {
			m.notice = "worktree: " + msg.err.Error()
		} else {
			m.notice = fmt.Sprintf("worktree ready at %s (not opened)", msg.ws.Dir)
		}
		return m, nil
	}
	if msg.err != nil {
		m.worktreeMod.busy = false
		m.worktreeMod.err = msg.err
		return m, nil
	}
	m.mode = modeList
	m.newSessionTarget = &newSessionRequest{Name: msg.ws.Name, Dir: msg.ws.Dir}
	return m, tea.Quit
}

// NewSessionRequest 는 TUI 가 끝난 뒤 만들고 붙을 셸 세션이다. 없으면 nil.
func (m Model) NewSessionRequest() *newSessionRequest { return m.newSessionTarget }
