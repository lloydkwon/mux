package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

// worktreeTestModel 은 액션 행 셋 아래 세션 둘을 둔다 — alpha 는 태그 없이 폴더만, beta 는 @project_dir 가 있다.
func worktreeTestModel() Model {
	created := time.Now()
	m := Model{
		tree:   newTreeState(),
		prefs:  defaultPreferences(),
		width:  120,
		height: 30,
		sessions: []tmux.Session{
			{Name: "alpha", Directory: "/w/alpha/sub", Created: created},
			{Name: "beta", Directory: "/w/beta/deep", ProjectDir: "/w/beta", Created: created},
		},
	}
	m.applyFilter()
	return m
}

func worktreeRowIndex(t *testing.T, m Model) int {
	t.Helper()
	for i, it := range m.items {
		if it.kind == itemNewWorktree {
			return i
		}
	}
	t.Fatalf("no New worktree row in %v", kinds(m.items))
	return -1
}

func sessionRowIndex(t *testing.T, m Model, name string) int {
	t.Helper()
	for i, it := range m.items {
		if it.kind == itemSession && it.session.Name == name {
			return i
		}
	}
	t.Fatalf("no session row %q", name)
	return -1
}

// worktreeSeam 은 tmux 쪽 시임 둘을 바꿔 끼운다. 진짜 prepareWorktree 는 디스크에 worktree 를 만든다.
type worktreeSeam struct {
	calls        int
	repo, branch string
	ws           tmux.WorktreeSession
	err          error
	// origin 은 mux 를 띄운 세션이라고 답할 이름이다. asked 는 그것을 물은 횟수다.
	origin string
	asked  int
}

func stubWorktreeSeam(t *testing.T) *worktreeSeam {
	t.Helper()
	s := &worktreeSeam{ws: tmux.WorktreeSession{Name: "alpha-feat-x", Dir: "/w/alpha-feat-x"}, origin: "alpha"}
	oldPrepare, oldCurrent := prepareWorktree, currentSessionName
	prepareWorktree = func(repo, branch string) (tmux.WorktreeSession, error) {
		s.calls++
		s.repo, s.branch = repo, branch
		return s.ws, s.err
	}
	currentSessionName = func() string {
		s.asked++
		return s.origin
	}
	t.Cleanup(func() { prepareWorktree, currentSessionName = oldPrepare, oldCurrent })
	return s
}

// step 은 Update 를 한 번 부르고 Model 을 돌려준다.
func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// originFrom 은 cmd(배치일 수 있다) 가운데 worktreeOriginMsg 를 내는 것을 찾는다. 배치의 다른 Cmd(커서 깜박임)는
// 시간을 기다리므로 각자 고루틴에서 돌린다.
func originFrom(t *testing.T, cmd tea.Cmd) worktreeOriginMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("no Cmd to ask for the session")
	}
	msgs := make(chan tea.Msg, 16)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range batch {
					run(sub)
				}
				return
			}
			msgs <- msg
		}()
	}
	run(cmd)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-msgs:
			if o, ok := msg.(worktreeOriginMsg); ok {
				return o
			}
		case <-timeout:
			t.Fatal("the Cmd never asked which session mux was opened from")
		}
	}
}

// openPrompt 은 New worktree 행에서 enter 를 누르고, 돌려받은 Cmd 의 답(mux 를 띄운 세션)까지 넣는다.
func openPrompt(t *testing.T, m Model) Model {
	t.Helper()
	m.cursor = worktreeRowIndex(t, m)
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeWorktree {
		t.Fatalf("enter on New worktree: mode = %v, want the prompt", m.mode)
	}
	if m.projectDir != "" {
		return m
	}
	m, _ = step(t, m, originFrom(t, cmd))
	return m
}

func TestWorktreeRowSitsUnderNewSession(t *testing.T) {
	m := worktreeTestModel()
	i := worktreeRowIndex(t, m)
	if i != 2 || m.items[1].kind != itemNewSession || m.items[3].kind != itemSession {
		t.Fatalf("rows = %v, want New worktree right under New tmux session", kinds(m.items))
	}
	row := ansi.Strip(formatItemRow(m.items[i], false, false, 16, 80, &m.tree))
	if !strings.Contains(row, "⌥ New worktree") || !strings.Contains(row, "git worktree + shell") {
		t.Errorf("row = %q", row)
	}
}

// Repo 칸은 mux 를 띄운 세션에서 온다 — @project_dir 가 있으면 그것, 없으면 그 세션의 폴더.
// 커서가 머문 세션으로 채우던 첫 안은 이 시험에서 깨졌다: beta 에서 행까지 k 로 올라오면 alpha 를 지나 alpha 가 됐다.
func TestWorktreePromptStartsFromTheSessionMuxWasOpenedFrom(t *testing.T) {
	for _, tc := range []struct{ origin, want string }{
		{"alpha", "/w/alpha/sub"},
		{"beta", "/w/beta"},
	} {
		seam := stubWorktreeSeam(t)
		seam.origin = tc.origin
		m := worktreeTestModel()
		m.cursor = sessionRowIndex(t, m, "beta")
		for m.cursor > worktreeRowIndex(t, m) {
			m, _ = step(t, m, runeKey('k'))
		}
		m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		if seam.asked != 0 {
			t.Fatal("Update asked tmux itself instead of returning a Cmd")
		}
		if got := m.worktreeMod.repoInput.Value(); got != "" {
			t.Errorf("Repo = %q before the answer, want blank", got)
		}
		m, _ = step(t, m, originFrom(t, cmd))
		if got := m.worktreeMod.repoInput.Value(); got != tc.want {
			t.Errorf("opened from %s: Repo = %q, want %q", tc.origin, got, tc.want)
		}
		if m.worktreeMod.focused != 0 || !m.worktreeMod.branchInput.Focused() {
			t.Errorf("opened from %s: the branch field should have the focus", tc.origin)
		}
	}
}

// VS Code 에서 연 목록이면 그 프로젝트가 먼저다. 그때는 tmux 에 묻는 답이 와도 덮지 않는다.
func TestWorktreePromptPrefersTheSeededProject(t *testing.T) {
	stubWorktreeSeam(t)
	m := worktreeTestModel()
	m.projectDir = "/w/vscode"
	m.filterText = m.projectDir
	m.applyFilter()
	m = openPrompt(t, m)
	m, _ = step(t, m, worktreeOriginMsg{session: "alpha"})
	if got := m.worktreeMod.repoInput.Value(); got != "/w/vscode" {
		t.Errorf("Repo = %q, want the seeded project", got)
	}
}

// 고르지 못하면 빈 칸이다 — 다른 세션의 폴더로 바꿔 채우지 않는다. 사람이 그새 친 것도 덮지 않는다.
func TestWorktreePromptLeavesRepoAloneWhenItCannotTell(t *testing.T) {
	for _, origin := range []string{"", "gone"} {
		seam := stubWorktreeSeam(t)
		seam.origin = origin
		if got := openPrompt(t, worktreeTestModel()).worktreeMod.repoInput.Value(); got != "" {
			t.Errorf("opened from %q: Repo = %q, want blank", origin, got)
		}
	}

	stubWorktreeSeam(t)
	m := worktreeTestModel()
	m.cursor = worktreeRowIndex(t, m)
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.worktreeMod.repoInput.SetValue("/typed")
	m, _ = step(t, m, worktreeOriginMsg{session: "alpha"})
	if got := m.worktreeMod.repoInput.Value(); got != "/typed" {
		t.Errorf("Repo = %q, want what was typed kept", got)
	}

	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m, _ = step(t, m, worktreeOriginMsg{session: "alpha"}); m.mode != modeList {
		t.Errorf("a late answer reopened the prompt: mode = %v", m.mode)
	}
}

// 루프 규칙: Update 는 git 을 부르지 않고 Cmd 만 돌려준다. git 은 그 Cmd 안에서 돈다.
func TestWorktreeRunsOnlyInTheCmd(t *testing.T) {
	seam := stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m.worktreeMod.branchInput.SetValue(" feat/x ")

	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if seam.calls != 0 {
		t.Fatal("Update ran git itself instead of returning a Cmd")
	}
	if cmd == nil || !m.worktreeMod.busy {
		t.Fatalf("enter: cmd=%v busy=%v, want a Cmd and the prompt busy", cmd != nil, m.worktreeMod.busy)
	}
	if !strings.Contains(m.View(), "creating worktree") {
		t.Error("the prompt does not say it is working")
	}
	msg := cmd()
	if seam.calls != 1 || seam.repo != "/w/alpha/sub" || seam.branch != "feat/x" {
		t.Fatalf("Cmd called prepareWorktree %d times with (%q, %q)", seam.calls, seam.repo, seam.branch)
	}
	if _, ok := msg.(worktreeReadyMsg); !ok {
		t.Fatalf("Cmd returned %T, want worktreeReadyMsg", msg)
	}

	// 도는 동안 다시 누른 enter 는 두 번째 worktree 를 만들지 않는다.
	if _, again := step(t, m, tea.KeyMsg{Type: tea.KeyEnter}); again != nil {
		t.Error("a second enter while busy returned another Cmd")
	}
}

func TestWorktreeEmptyBranchDoesNothing(t *testing.T) {
	seam := stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.worktreeMod.busy || seam.calls != 0 {
		t.Errorf("enter with no branch: cmd=%v busy=%v calls=%d", cmd != nil, m.worktreeMod.busy, seam.calls)
	}
}

// 성공이면 대상을 적고 끝낸다 — 세션 생성과 attach 는 cmd/mux 가 한다.
func TestWorktreeReadyQuitsWithTheSessionToMake(t *testing.T) {
	stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m.worktreeMod.busy = true
	m, cmd := step(t, m, worktreeReadyMsg{id: m.worktreeMod.id, ws: tmux.WorktreeSession{Name: "alpha-feat-x", Dir: "/w/alpha-feat-x"}})
	if cmd == nil {
		t.Fatal("no Cmd after the worktree was ready")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Fatal("the TUI should quit so cmd/mux can make the session")
	}
	r := m.NewSessionRequest()
	if r == nil || r.Name != "alpha-feat-x" || r.Dir != "/w/alpha-feat-x" {
		t.Fatalf("NewSessionRequest = %+v", r)
	}
	if m.AttachName() != "" || m.ResumeRequest() != nil {
		t.Error("only the new-session request should be set")
	}
}

// git 이 거절하면 그 이유를 입력 화면에 보이고 다시 받는다. 긴 오류는 상자 안에서 접힌다.
func TestWorktreeErrorStaysOnThePrompt(t *testing.T) {
	stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m.worktreeMod.busy = true
	long := "git worktree add: exit status 128: fatal: '/home/u/dev/projects/some/very/long/path/alpha-feat-x' already exists"
	m, cmd := step(t, m, worktreeReadyMsg{id: m.worktreeMod.id, err: errors.New(long)})
	if cmd != nil || m.mode != modeWorktree || m.worktreeMod.busy || m.NewSessionRequest() != nil {
		t.Fatalf("after an error: mode=%v busy=%v cmd=%v request=%v", m.mode, m.worktreeMod.busy, cmd != nil, m.NewSessionRequest())
	}
	view := m.worktreeMod.View()
	if !strings.Contains(ansi.Strip(strings.ReplaceAll(view, "\n", "")), "already exists") {
		t.Errorf("the error is not on the prompt:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Errorf("line %d cells wide, would push the box off a popup: %q", w, ansi.Strip(line))
		}
	}
}

// 기다리다 esc 로 닫으면 늦게 온 결과를 상태 줄에 알린다 — 만든 worktree 를 말없이 두지 않는다.
func TestWorktreeLateResultAfterEscIsANotice(t *testing.T) {
	stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m.worktreeMod.busy = true
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("esc while busy: mode = %v, want the list", m.mode)
	}
	done, cmd := step(t, m, worktreeReadyMsg{id: m.worktreeMod.id, ws: tmux.WorktreeSession{Dir: "/w/alpha-feat-x"}})
	if cmd != nil || done.NewSessionRequest() != nil || !strings.Contains(done.notice, "/w/alpha-feat-x") {
		t.Errorf("late success: cmd=%v request=%v notice=%q", cmd != nil, done.NewSessionRequest(), done.notice)
	}
	failed, _ := step(t, m, worktreeReadyMsg{id: m.worktreeMod.id, err: errors.New("boom")})
	if failed.mode != modeList || !strings.Contains(failed.notice, "boom") {
		t.Errorf("late error: mode=%v notice=%q", failed.mode, failed.notice)
	}
}

// 기다리다 esc 로 닫고 다시 열면, 첫 화면이 시킨 결과는 둘째 화면의 것이 아니다 — 거기서 세션을 열거나
// 오류를 보이지 않고 상태 줄에만 알린다.
func TestWorktreeResultOfAClosedPromptDoesNotLandOnTheNextOne(t *testing.T) {
	stubWorktreeSeam(t)
	m := openPrompt(t, worktreeTestModel())
	m.worktreeMod.branchInput.SetValue("feat/x")
	m, first := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = openPrompt(t, m)

	m, cmd := step(t, m, first())
	if cmd != nil || m.NewSessionRequest() != nil {
		t.Fatalf("the first prompt's result opened a session from the second: request=%+v", m.NewSessionRequest())
	}
	if m.mode != modeWorktree || m.worktreeMod.busy || m.worktreeMod.err != nil {
		t.Errorf("the second prompt changed: mode=%v busy=%v err=%v", m.mode, m.worktreeMod.busy, m.worktreeMod.err)
	}
	if !strings.Contains(m.notice, "/w/alpha-feat-x") {
		t.Errorf("notice = %q, want the made worktree said", m.notice)
	}
}

// 더블클릭은 activateCurrent 를 거친다. 이 행이 그 switch 에 없으면 빈 attach 로 빠져 mux 가 닫힌다.
func TestDoubleClickOnTheWorktreeRowOpensThePrompt(t *testing.T) {
	stubWorktreeSeam(t)
	m := worktreeTestModel()
	y := m.rowY(worktreeRowIndex(t, m))
	first, _ := step(t, m, leftClick(4, y))
	second, _ := step(t, first, leftClick(4, y))
	if second.mode != modeWorktree || second.AttachName() != "" {
		t.Fatalf("double click: mode=%v attach=%q, want the prompt", second.mode, second.AttachName())
	}
}

// 오른쪽 칸은 행이 하는 일을 말한다.
func TestWorktreeRowPreviewSaysWhatItDoes(t *testing.T) {
	m := worktreeTestModel()
	m.cursor = worktreeRowIndex(t, m)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "[ New worktree ]") || !strings.Contains(view, "beside the repo") {
		t.Errorf("preview does not say what the row does:\n%s", view)
	}
}
