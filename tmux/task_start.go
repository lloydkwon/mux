package tmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// `mux task start` — worktree 를 만들고, 씨앗 대화를 만들고, 그 대화를 잇는 세션을 연다. 설계는 docs/design/task.md.

const (
	// taskReadyLimit 은 새 세션의 Claude 가 입력을 받을 때까지 기다리는 한도다.
	taskReadyLimit = 60 * time.Second
	// taskReadySettle 동안 idle 과 입력창이 이어져야 준비된 것으로 본다. 시작 훅이 도는 동안 친 글은 사라질 수 있다.
	taskReadySettle = time.Second
	taskReadyPoll   = 200 * time.Millisecond

	// taskOldAge 가 지난 worktree 에 기록이 없으면 안 보낸 것이 아니라 지워진 것일 수 있다 — 자동으로 보내지 않는다.
	taskOldAge = 24 * time.Hour

	// taskSlotLimit 은 다른 작업이 세션을 만드는 것을 기다리는 한도다.
	taskSlotLimit = 30 * time.Second

	// taskSeedLimit 은 씨앗 대화를 만드는 `claude -p` 의 한도다(실측 0.3초).
	taskSeedLimit = 30 * time.Second

	// 아무도 붙지 않은 세션의 크기다. 폭이 140 이상이면 after-new-session 훅이 패널을 붙인다.
	taskSessionWidth  = "120"
	taskSessionHeight = "50"
)

// seedTaskConversation 은 runTaskSeed 다. 러너를 거치지 않아 테스트가 바꾼다.
var seedTaskConversation = runTaskSeed

// newConversationID 는 새 대화 id 다. 테스트가 바꾼다.
var newConversationID = randomUUID

// TaskStartRequest 는 start 의 인자다.
type TaskStartRequest struct {
	ID string
	// Repo 는 --repo 다. 비면 CWD 의 저장소를 쓴다.
	Repo   string
	CWD    string
	Title  string
	Prompt string // 보낼 한 줄. 비면 PromptFile
	// PromptFile 은 보낼 때에만 읽는다. 이미 있는 작업에 다시 부를 때 파일이 사라졌어도 아무 변화가 없어야 한다.
	PromptFile string
}

// prompt 는 보낼 글이다. 없으면 빈 값.
func (req TaskStartRequest) prompt() (string, error) {
	if req.Prompt == "" && req.PromptFile != "" {
		return PromptFromFile(req.PromptFile)
	}
	return req.Prompt, nil
}

// TaskAction 은 start 가 한 일이다.
type TaskAction string

const (
	TaskCreated TaskAction = "created" // worktree 와 세션을 새로 만들었다
	TaskResumed TaskAction = "resumed" // 있는 worktree 에 세션을 다시 열었다
	TaskExists  TaskAction = "exists"  // 이미 있어 아무것도 하지 않았다
)

// TaskStarted 는 start 의 결과다. 오류가 있어도 어디까지 했는지 채워져 있다.
type TaskStarted struct {
	Action TaskAction
	Task   Task
	// Base 는 새 worktree 가 시작한 커밋이다.
	Base string
	// Token 은 프롬프트를 보냈을 때만 있다.
	Token *AgentToken
	// Notes 는 사람에게 알릴 말이다.
	Notes []string
}

func (r *TaskStarted) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// StartTask 는 작업을 시작하거나 다시 연다. 여러 번 불러도 프롬프트는 한 번만 들어간다.
//
// 중간에 실패하면 만든 것을 되돌리지 않는다. 다시 부르면 남은 데서 이어지고, 프롬프트가 나갔는지 모르는 세션을
// 지우는 쪽이 더 나쁘다.
func StartTask(req TaskStartRequest) (TaskStarted, error) {
	var res TaskStarted
	// 무엇이든 만들기 전에 검사한다 — 프롬프트가 잘못됐다는 것을 세션을 만든 뒤에 알면 다시 부를 수 없다.
	if err := ValidTaskID(req.ID); err != nil {
		return res, err
	}
	if cleanWorkText(req.Title) == ReviewSessionName(req.ID) {
		return res, fmt.Errorf("%s 는 이 작업의 리뷰어 이름이라 제목으로 쓸 수 없다", ReviewSessionName(req.ID))
	}
	if req.Prompt != "" {
		if err := ValidatePromptText(req.Prompt); err != nil {
			return res, err
		}
	}
	if req.Repo != "" {
		dir, err := worktreeRepoDir(req.Repo)
		if err != nil {
			return res, err
		}
		req.Repo = dir
	}
	unlock, err := lockTask(req.ID)
	if err != nil {
		return res, err
	}
	defer unlock()

	scan, err := scanTasks(req.Repo, req.CWD)
	if err != nil {
		return res, err
	}
	// 서버를 띄우지 않는다. 빈 서버가 뜨면 tmux 설정이 저장된 세션 복원을 시작하고, 다른 소켓을 보고 있는 것이면 둘째
	// 서버에 실제 세션과 Claude 가 한 벌 더 뜬다.
	if !scan.running {
		return res, errors.New("tmux 서버가 없다 — mux 나 tmux 를 먼저 열고 다시 하라")
	}
	for _, w := range scan.warnings {
		res.note("%v", w)
	}
	existing, err := existingTask(scan, req)
	if err != nil {
		return res, err
	}
	if existing != nil {
		res.Task = *existing
		return continueTask(res, req)
	}
	return createTask(res, scan, req)
}

// existingTask 는 이미 있는 작업을 찾는다. 없으면 nil 이다. 같은 id 가 다른 저장소에 있으면 오류다.
func existingTask(scan taskScan, req TaskStartRequest) (*Task, error) {
	found := scan.find(req.ID)
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) > 1 {
		return nil, fmt.Errorf("작업 %s 가 여러 저장소에 있다: %s", req.ID, taskPlaces(found))
	}
	t := found[0]
	if t.Worktree == "" {
		return nil, fmt.Errorf("세션 %s 는 있는데 worktree 가 없다 — 이상 상태라 아무것도 하지 않았다 (mux task done %s 로 닫을 수 있다)", t.Session, t.ID)
	}
	if err := checkStartRepo(t, req); err != nil {
		return nil, err
	}
	if t.foreign != "" {
		return nil, fmt.Errorf("세션 %s 는 이 작업의 것이 아니다 (%s 에서 열렸다) — 이름을 바꾸거나 닫은 뒤 다시 하라", TaskSessionName(t.ID), t.foreign)
	}
	return &t, nil
}

// checkStartRepo 는 부른 사람이 말하는 저장소가 그 작업의 저장소인지 본다. 다른 저장소의 작업을 이으면 엉뚱한 Claude 에게
// 프롬프트를 보낸다. --repo 가 없으면 지금 폴더의 저장소를 말하는 것이고, 저장소 밖에서 부른 것은 설정에서 찾은 하나다.
func checkStartRepo(t Task, req TaskStartRequest) error {
	if req.Repo != "" {
		repo, err := readTaskRepo(req.Repo)
		if err != nil {
			return err
		}
		if repo.main != t.Repo {
			return fmt.Errorf("작업 %s 는 다른 저장소에 이미 있다: %s", req.ID, t.Repo)
		}
		return nil
	}
	if req.CWD == "" {
		return nil
	}
	repo, err := readTaskRepo(req.CWD)
	var answered *exec.ExitError
	switch {
	case errors.As(err, &answered):
		return nil // git 이 «저장소가 아니다» 라고 답했다
	case err != nil:
		return err // 답을 듣지 못했다(시간 초과) — 저장소 밖이라는 뜻이 아니다
	case repo.main != t.Repo:
		return fmt.Errorf("작업 %s 는 다른 저장소에 이미 있다: %s (지금 폴더는 %s) — 그 작업을 말하는 것이면 --repo 로 지목한다", req.ID, t.Repo, repo.main)
	}
	return nil
}

func taskPlaces(tasks []Task) string {
	places := make([]string, 0, len(tasks))
	for _, t := range tasks {
		places = append(places, t.Repo)
	}
	return strings.Join(places, ", ")
}

// continueTask 는 worktree 가 있는 작업을 다룬다.
func continueTask(res TaskStarted, req TaskStartRequest) (TaskStarted, error) {
	t := res.Task
	if t.Session != "" {
		res.Action = TaskExists
		res.note("%s 는 이미 있다 (%s) — 아무것도 하지 않았다", t.Session, t.State)
		if t.State == TaskNoClaude {
			if conv, ok, err := latestTaskConversation(t); err == nil && ok {
				res.note("Claude 가 꺼져 있다. 그 세션의 셸에서: claude --resume %s", conv.id)
			}
		}
		return res, nil
	}

	res.Action = TaskResumed
	if t.locked {
		// 만들다 끊긴 worktree 는 git 이 잠근 채 남긴다(initializing). 반쯤 꺼내진 파일 위에서 일을 시작하지 않는다.
		return res, fmt.Errorf("worktree 가 잠겨 있다 (%s) — 만들다 끊겼을 수 있다. 확인하고 git -C %s worktree unlock 하거나 지운 뒤 다시 하라", t.Worktree, t.Repo)
	}
	conv, ok, err := latestTaskConversation(t)
	if err != nil {
		return res, err
	}
	prompt, old := "", !conv.prompted && olderThan(t.Worktree, taskOldAge)
	if !conv.prompted && !old {
		if prompt, err = req.prompt(); err != nil {
			return res, err
		}
	}
	if !ok {
		id, err := seedTask(t, req.Title)
		if err != nil {
			return res, err
		}
		conv = taskConversation{id: id}
	} else {
		if pid := conversationHolder(conv.id); pid != 0 {
			return res, fmt.Errorf("이 대화는 이미 열려 있다 (pid %d) — 둘이 한 기록에 쓰게 되므로 열지 않았다", pid)
		}
		if req.Title != "" {
			res.note("있는 대화를 이으므로 --title 은 쓰지 않았다")
		}
	}
	if err := openTaskSession(t, conv.id); err != nil {
		return res, err
	}
	res.Task.Session = TaskSessionName(t.ID)
	if conv.prompted {
		res.note("%s 를 다시 열고 대화를 이었다 — 첫 프롬프트는 다시 보내지 않았다", res.Task.Session)
		return res, nil
	}
	if old {
		res.note("프롬프트를 보낸 기록이 없지만 오래된 worktree 라 보내지 않았다(기록이 지워졌을 수 있다). 보내려면: mux agent prompt %s …", res.Task.Session)
		return res, nil
	}
	if prompt == "" {
		res.note("%s 를 다시 열었다. 이 작업에는 프롬프트를 보낸 적이 없다", res.Task.Session)
		return res, nil
	}
	res.note("프롬프트를 보낸 적이 없는 작업이라 이번에 보낸다")
	return sendFirstPrompt(res, prompt)
}

// olderThan 은 worktree 를 만든 지 age 가 넘었는지 본다.
func olderThan(worktree string, age time.Duration) bool {
	info, err := os.Stat(filepath.Join(worktree, ".git"))
	return err == nil && agentNow().Sub(info.ModTime()) > age
}

// conversationHolder 는 그 대화를 열고 있는 살아 있는 Claude 의 pid 다. 없으면 0. 방금 닫힌 세션의 Claude 는 끝나는
// 중일 수 있어 taskExitTimeout 까지 기다려 본다.
func conversationHolder(id string) int {
	deadline := agentNow().Add(taskExitTimeout)
	for {
		holder := 0
		for _, sf := range liveClaudeSessionFiles() {
			if sf.SessionID == id {
				holder = sf.PID
			}
		}
		if holder == 0 || !agentNow().Before(deadline) {
			return holder
		}
		agentSleep(taskReadyPoll)
	}
}

// createTask 는 worktree 부터 새로 만든다.
func createTask(res TaskStarted, scan taskScan, req TaskStartRequest) (TaskStarted, error) {
	prompt, err := req.prompt()
	if err != nil {
		return res, err
	}
	if prompt == "" {
		return res, errors.New("새 작업에는 --prompt 나 --prompt-file 이 있어야 한다")
	}
	from := req.Repo
	if from == "" {
		from = req.CWD
	}
	repo, err := readTaskRepo(from)
	if err != nil {
		return res, fmt.Errorf("%w — 저장소 안에서 부르거나 --repo 로 지목한다", err)
	}
	t := Task{
		ID:       req.ID,
		Repo:     repo.main,
		Worktree: taskWorktreeDir(repo.main, req.ID),
		Branch:   TaskSessionName(req.ID),
	}
	res.Action, res.Task = TaskCreated, t
	if res.Base, err = addTaskWorktree(from, repo, t); err != nil {
		return res, err
	}
	if !scan.watched[repo.main] {
		path, _ := taskReposFile()
		res.note("%s 는 %s 에 없다 — 세션이 끝나면 mux task ls 가 이 작업을 찾지 못한다", repo.main, path)
	}

	id, err := seedTask(t, req.Title)
	if err != nil {
		return res, err
	}
	if err := openTaskSession(t, id); err != nil {
		return res, err
	}
	res.Task.Session = TaskSessionName(t.ID)
	return sendFirstPrompt(res, prompt)
}

// addTaskWorktree 는 from 의 HEAD 에서 새 브랜치로 worktree 를 만들고 시작 커밋을 돌려준다.
//
// 브랜치가 이미 있으면 거절한다. done 이 브랜치를 남기므로, 조용히 다시 쓰면 이미 머지된 브랜치 위에 새 일이 쌓인다.
func addTaskWorktree(from string, repo taskRepo, t Task) (string, error) {
	for _, tree := range repo.trees[1:] {
		if tree.path == t.Worktree {
			return "", fmt.Errorf("worktree 폴더(%s)가 사라졌다. git worktree prune 뒤에 다시 하라", t.Worktree)
		}
	}
	if _, err := os.Lstat(t.Worktree); err == nil {
		return "", fmt.Errorf("이미 있는 폴더라 덮지 않는다: %s", t.Worktree)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("폴더를 확인하지 못했다 (%s): %w", t.Worktree, err)
	}
	ref, err := existingBranchRef(from, t.Branch)
	if err != nil {
		return "", err
	}
	if ref != "" {
		return "", fmt.Errorf("브랜치가 이미 있다 (%s) — 끝낸 작업일 수 있다. 이어서 하려면 git -C %s worktree add %s %s 를 한 뒤 다시 하라",
			ref, t.Repo, t.Worktree, t.Branch)
	}
	out, err := outputWithin(worktreeReadLimit, "git", "-C", from, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("시작할 커밋을 읽지 못했다: %w", err)
	}
	if _, err := outputWithin(worktreeAddLimit, "git", "-C", from, "worktree", "add", "-b", t.Branch, t.Worktree); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// existingBranchRef 는 그 이름의 로컬 브랜치나 원격 추적 브랜치다. 없으면 빈 값이다.
//
// 원격 이름은 `git remote` 로 받고 정확히 비교한다 — 이유는 worktreeAddArgs 와 같다(worktree.md).
func existingBranchRef(dir, branch string) (string, error) {
	local := "refs/heads/" + branch
	out, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "for-each-ref", "--format=%(refname)", local, "refs/remotes/")
	if err != nil {
		return "", fmt.Errorf("브랜치 목록을 읽지 못했다: %w", err)
	}
	remotesOut, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "remote")
	if err != nil {
		return "", fmt.Errorf("원격 목록을 읽지 못했다: %w", err)
	}
	taken := map[string]bool{local: true}
	for _, r := range strings.Fields(string(remotesOut)) {
		taken["refs/remotes/"+r+"/"+branch] = true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if ref := strings.TrimSpace(line); taken[ref] {
			return ref, nil
		}
	}
	return "", nil
}

// seedTask 는 모델을 부르지 않는 씨앗 대화를 만들고 그 id 를 돌려준다. 제목이 없으면 세션 이름이다.
//
// 씨앗이 있어야 세션의 명령줄이 처음부터 `claude --resume <id>` 가 된다 — resurrect 가 재부팅 뒤 그 줄을 다시 쳤을
// 때 같은 대화로 이어지는 모양은 이것뿐이다(task.md «실행 모양»).
func seedTask(t Task, title string) (string, error) {
	title = cleanWorkText(title)
	if title == "" {
		title = TaskSessionName(t.ID)
	}
	id, err := newConversationID()
	if err != nil {
		return "", err
	}
	if err := seedTaskConversation(t.Worktree, id, title); err != nil {
		return "", fmt.Errorf("대화를 만들지 못했다: %w", err)
	}
	// 다음 start 가 찾는 길로 확인한다. 거기서 못 찾는 기록이면 «안 보냄» 으로 읽혀 프롬프트가 다시 들어간다.
	conv, ok, err := latestTaskConversation(t)
	if err != nil {
		return "", err
	}
	if !ok || conv.id != id {
		return "", fmt.Errorf("만든 대화의 기록을 찾지 못했다 (%s) — Claude 의 기록 폴더 이름이 예상과 다르다", t.Worktree)
	}
	return id, nil
}

// runTaskSeed 는 `claude -p /rename` 을 한 번 돌린다. 러너를 거치지 않는 것은 폴더와 환경을 정해야 해서다
// (runWorkTitleModel 과 같다). 사용자 설정을 읽지 않아 훅과 CLAUDE.md 가 돌지 않고, 도구도 없다.
func runTaskSeed(dir, sessionID, title string) error {
	bin := findClaudeBinary()
	if bin == "" {
		return errors.New("claude 를 찾지 못했다")
	}
	ctx, cancel := context.WithTimeout(context.Background(), taskSeedLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, taskSeedArgs(sessionID, title)...)
	cmd.Dir = dir
	cmd.Env = workTitleEnv(os.Environ()) // TMUX 를 빼지 않으면 부른 pane 이 Claude 세션으로 보인다
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("claude 가 %v 안에 답하지 않았다", taskSeedLimit)
	}
	if err != nil {
		return withStderr(err, stderr.Bytes())
	}
	var reply struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return fmt.Errorf("claude 답을 읽지 못했다: %w", err)
	}
	if reply.IsError {
		return fmt.Errorf("claude: %s", reply.Result)
	}
	return nil
}

func taskSeedArgs(sessionID, title string) []string {
	return []string{
		"-p",
		"--output-format", "json",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--tools", "",
		"--session-id", sessionID,
		"/rename " + title,
	}
}

// randomUUID 는 v4 UUID 다.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("대화 id 를 만들지 못했다: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// openTaskSession 은 그 대화를 잇는 세션을 worktree 폴더에 연다.
//
// 이름이 겹치면 실패해야 한다 — CreateSessionWithCommand 처럼 -2 를 붙이면 작업을 못 찾는다. @project_dir 은 달지
// 않는다(worktree.md — 달면 패널 자동 삽입이 물러난다). 초 경계를 기다리는 것은 AdoptClaudeSessions 와 같은 이유다.
//
// 잠금을 쥔 채 기다리는 것은 작업 둘을 함께 시작할 때다. 잠금이 없으면 초 경계가 오히려 둘을 같은 초에 세운다.
func openTaskSession(t Task, conversation string) error {
	unlock, err := flockWait(filepath.Join(agentLockDir(), "task-session.lock"), taskSlotLimit)
	if err != nil {
		return fmt.Errorf("세션 %s: %w", TaskSessionName(t.ID), err)
	}
	defer unlock()
	waitForNextSecond()
	if err := runner.Run("tmux", "new-session", "-d", "-s", TaskSessionName(t.ID), "-c", tmuxDirArg(t.Worktree),
		"-x", taskSessionWidth, "-y", taskSessionHeight, taskSessionCommand(conversation)); err != nil {
		return fmt.Errorf("세션 %s: %w", TaskSessionName(t.ID), err)
	}
	return nil
}

// taskSessionCommand 는 세션의 명령이다. Claude 는 셸의 자식이어야 하고 명령줄에 사람이 쓴 글이 없어야 한다 —
// resurrect 가 pane 의 자식 명령을 저장했다가 인용 없이 다시 친다(task.md). 그래서 `"$SHELL"` 로 이어 받고, 글은
// 대화 id 하나뿐이다. Claude 를 끝내면 그 셸이 worktree 폴더에 남는다.
func taskSessionCommand(conversation string) string {
	claude := "claude --resume " + conversation
	if _, err := exec.LookPath("claude"); err != nil {
		// 이 PATH 에 없으면 설치 스크립트가 두는 곳을 앞에 붙인다. 이름은 claude 그대로여야 복구가 알아본다.
		if bin := findClaudeBinary(); bin != "" {
			claude = "PATH=" + shellQuote(filepath.Dir(bin)) + `:"$PATH" ` + claude
		}
	}
	return claude + `; exec "$SHELL"`
}

// sendFirstPrompt 는 준비를 기다려 프롬프트를 한 번 보낸다.
func sendFirstPrompt(res TaskStarted, prompt string) (TaskStarted, error) {
	a, err := waitTaskReady(res.Task.Session)
	if err != nil {
		return res, err
	}
	tok, err := PromptAgent(a, prompt, false)
	if err != nil {
		return res, err
	}
	res.Token = &tok
	return res, nil
}

// waitTaskReady 는 그 세션의 Claude 가 idle 이고 입력창이 보이는 상태가 taskReadySettle 동안 이어질 때까지 기다린다.
// 대화상자에서 멈췄으면 곧바로 ErrAgentBlocked 다 — 기다려도 사람이 답하기 전에는 바뀌지 않는다.
func waitTaskReady(session string) (Agent, error) {
	deadline := agentNow().Add(taskReadyLimit)
	var since time.Time
	for {
		a, ready, err := taskReadyOnce(session)
		if err != nil {
			return Agent{}, err
		}
		now := agentNow()
		switch {
		case !ready:
			since = time.Time{}
		case since.IsZero():
			since = now
		case now.Sub(since) >= taskReadySettle:
			return a, nil
		}
		if !now.Before(deadline) {
			return Agent{}, fmt.Errorf("%w: %s 의 Claude 가 %v 안에 입력을 받을 준비가 되지 않았다. 아무것도 보내지 않았고 세션은 남았다 — pane 을 확인한 뒤 mux agent prompt %s",
				ErrWaitTimeout, session, taskReadyLimit, session)
		}
		agentSleep(taskReadyPoll)
	}
}

func taskReadyOnce(session string) (Agent, bool, error) {
	agents, err := Agents()
	if err != nil {
		return Agent{}, false, err
	}
	a := mostUrgentAgent(agents, session)
	if a == nil {
		return Agent{}, false, nil
	}
	if a.State == AgentBlocked {
		return Agent{}, false, fmt.Errorf("%w: %s (%s). 아무것도 보내지 않았다", ErrAgentBlocked, session, a.WaitingFor)
	}
	if a.State != AgentIdle {
		return Agent{}, false, nil
	}
	_, ok := readBox(a.PaneID)
	return *a, ok, nil
}
