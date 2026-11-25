package tmux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// 작업 — 작업 하나 = worktree 하나 = t-<id> 세션 하나 = Claude 하나(`mux task start/ls/done`).
// 설계와 실측은 docs/design/task.md.
//
// 상태 파일은 없다. 작업의 지금 모습은 부를 때마다 git worktree 목록, tmux 세션 목록, Claude 상태 파일로
// 다시 계산한다.

// taskPrefix 는 작업 세션과 worktree 이름의 앞머리다.
const taskPrefix = "t-"

// taskIDRe 는 작업 id 다. `-` 로 시작하면 tmux 와 git 의 옵션으로 읽힌다.
var taskIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// 오류 값 — cmd/mux 가 errors.Is 로 종료 코드를 고른다(task.md 의 표).
var (
	// ErrTaskRefused 는 --force 로도 하지 않는 것이다(작업 중, 커밋 안 된 변경, 떠 있는 커밋).
	ErrTaskRefused = errors.New("하지 않았다")
	// ErrTaskUnpushed 는 --force 면 하는 것이다. 위와 떼어 두어야 스크립트가 갈라 탄다.
	ErrTaskUnpushed = errors.New("푸시 안 된 커밋이 있다 (--force 면 진행한다)")
	// ErrNoTask 는 그 id 의 세션도 worktree 도 없다는 뜻이다.
	ErrNoTask = errors.New("그런 작업이 없다")
)

// TaskState 는 작업의 상태 단어다. Claude 가 있으면 agent API 의 단어를 그대로 쓴다.
type TaskState string

const (
	TaskNoClaude   TaskState = "no-claude"   // 세션과 worktree 는 있는데 Claude 가 꺼졌다
	TaskNoSession  TaskState = "no-session"  // worktree 만 있다 — start 로 다시 연다
	TaskNoWorktree TaskState = "no-worktree" // 세션만 있다 — 이상 상태
)

// Task 는 작업 하나의 지금 모습이다.
type Task struct {
	ID       string
	Repo     string // 원본 작업 트리. 세션만 있으면 빈 값
	Worktree string // 없으면 빈 값
	Branch   string // 지금 체크아웃된 브랜치. detached 면 빈 값
	Session  string // 없으면 빈 값
	State    TaskState
	Since    time.Time
	Title    string
	// Agent 는 그 세션의 Claude 중 가장 급한 것이다. 없으면 nil.
	Agent *Agent

	// sessionPath 는 그 세션을 연 폴더다.
	sessionPath string
	// foreign 은 이름은 이 작업의 것인데 다른 폴더에서 열린 세션의 폴더다 — 사람이 만든 세션이다. 그때 Session 은 비운다.
	foreign string
	locked  bool // git worktree lock
}

// ValidTaskID 는 작업 id 로 쓸 수 있는지 본다.
func ValidTaskID(id string) error {
	if !taskIDRe.MatchString(id) {
		return fmt.Errorf("작업 id 는 소문자·숫자·«-» 40자 이하이고 소문자나 숫자로 시작한다: %q", id)
	}
	return nil
}

// TaskSessionName 은 작업의 tmux 세션 이름이다.
func TaskSessionName(id string) string { return taskPrefix + id }

// TaskID 는 세션 이름이 작업의 것이면 그 id 를 돌려준다. 이름만 보는 순수 함수라 패널이 틱마다 불러도 된다.
func TaskID(session string) (string, bool) {
	id, ok := strings.CutPrefix(session, taskPrefix)
	if !ok || ValidTaskID(id) != nil {
		return "", false
	}
	return id, true
}

// taskWorktreeDir 은 작업의 worktree 자리다 — New worktree 행과 같은 규칙(원본 저장소 옆).
func taskWorktreeDir(main, id string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"-"+taskPrefix+id)
}

// taskWorktreeID 는 worktree 폴더가 작업의 자리면 그 id 다. 브랜치가 아니라 경로로 본다 — Claude 가 worktree
// 안에서 브랜치 이름을 바꿔도 같은 작업이다.
func taskWorktreeID(main, path string) (string, bool) {
	if filepath.Dir(path) != filepath.Dir(main) {
		return "", false
	}
	rest, ok := strings.CutPrefix(filepath.Base(path), filepath.Base(main)+"-")
	if !ok {
		return "", false
	}
	return TaskID(rest)
}

// taskReposFile 은 볼 저장소 목록이다. 상태가 아니라 설정이고, 테스트가 바꾼다.
var taskReposFile = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "task-repos.txt"), nil
}

// watchedTaskRepos 는 설정에 적힌 저장소들이다. 파일이 없으면 비어 있는 것이다.
func watchedTaskRepos() ([]string, error) {
	path, err := taskReposFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("작업 저장소 목록을 읽지 못했다: %w", err)
	}
	var repos []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dir, err := worktreeRepoDir(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		repos = append(repos, dir)
	}
	return repos, nil
}

// taskRepo 는 저장소 하나와 그 worktree 목록이다. 첫 항목이 원본이다.
type taskRepo struct {
	main  string
	trees []worktreeEntry
}

// readTaskRepo 는 dir 이 든 저장소를 읽는다. linked worktree 에서 물어도 원본이 첫 항목이다.
func readTaskRepo(dir string) (taskRepo, error) {
	out, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return taskRepo{}, fmt.Errorf("git 저장소를 읽지 못했다 (%s): %w", dir, err)
	}
	trees := parseWorktreeList(string(out))
	if len(trees) == 0 {
		return taskRepo{}, fmt.Errorf("git 저장소를 읽지 못했다: worktree 목록이 비었다 (%s)", dir)
	}
	if trees[0].bare {
		return taskRepo{}, fmt.Errorf("bare 저장소(%s)는 지원하지 않는다", trees[0].path)
	}
	return taskRepo{main: trees[0].path, trees: trees}, nil
}

// taskSession 은 살아 있는 t-* 세션 하나다. path 는 세션을 연 폴더다.
type taskSession struct{ name, path string }

// taskSessions 는 살아 있는 t-* 세션들이다(id 열쇠). 서버가 없으면 비어 있는 것이다.
//
// 서버가 없는 것과 답하지 못한 것을 가른다. 프로토콜이 어긋났거나 다른 소켓에 살아 있는 서버를 «세션 없음» 으로 읽으면
// done 이 살아 있는 세션을 닫지 않고 worktree 부터 지우고, start 는 둘째 서버를 띄운다.
//
// -u 와 q: 가 load-bearing 이다. UTF-8 로캘이 아닌 곳(cron, 원격 ssh 명령)에서 tmux 는 탭과 한글을 «_» 로 바꿔 내고,
// 그러면 모든 세션이 사라진 것으로 읽힌다(검토에서 재현). q: 는 이름의 공백을 «\ » 로 바꿔 첫 공백이 곧 경계다.
//
// running 은 서버가 떠 있는지다.
func taskSessions() (sessions map[string]taskSession, running bool, err error) {
	out, err := runner.Output("tmux", "-u", "list-sessions", "-F", "#{q:session_name} #{session_path}")
	if err != nil {
		if !noTmuxServer(err) {
			return nil, false, fmt.Errorf("tmux 세션 목록을 읽지 못했다: %w", err)
		}
		if sock := liveServerElsewhere(); sock != "" {
			return nil, false, fmt.Errorf("tmux 서버가 %s 에 살아 있는데 닿지 못했다 — TMUX_TMPDIR 을 확인하라: %w", sock, err)
		}
		return map[string]taskSession{}, false, nil
	}
	sessions = make(map[string]taskSession)
	for _, line := range strings.Split(string(out), "\n") {
		name, path, _ := strings.Cut(line, " ")
		if id, ok := TaskID(name); ok {
			sessions[id] = taskSession{name: name, path: path}
		}
	}
	return sessions, true, nil
}

// noTmuxServer 는 서버가 떠 있지 않다는 오류인지 본다. tmux 는 LC_MESSAGES 를 쓰지 않아 늘 영어다.
func noTmuxServer(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "(No such file or directory)") ||
		strings.Contains(msg, "(Connection refused)")
}

// taskScan 은 한 번 훑은 결과다.
type taskScan struct {
	repos    []taskRepo
	sessions map[string]taskSession
	agents   []Agent
	running  bool // tmux 서버가 떠 있다
	// warnings 는 읽지 못한 저장소다. 목록은 나머지로 만든다.
	warnings []error
	// watched 는 설정 파일에 있는 저장소의 원본 경로다.
	watched map[string]bool
}

// scanTasks 는 설정의 저장소, dirs, 살아 있는 t-* 세션의 저장소를 훑는다. dirs 는 --repo 와 지금 폴더다.
//
// 세션 폴더와 dirs 는 저장소가 아닐 수 있어 조용히 건너뛴다(복구가 홈에 연 세션, 저장소 밖에서 부른 ls).
// 설정에 적힌 것을 못 읽는 것만 경고다.
func scanTasks(dirs ...string) (taskScan, error) {
	scan := taskScan{watched: map[string]bool{}}
	sessions, running, err := taskSessions()
	if err != nil {
		return scan, err
	}
	scan.sessions, scan.running = sessions, running
	// 세션이 있는데 pane 을 못 읽었으면 Claude 가 없는 것이 아니라 모르는 것이다 — done 이 일하는 Claude 를 닫게 된다.
	agents, err := Agents()
	if err != nil && (len(sessions) > 0 || !errors.Is(err, ErrNoServer)) {
		return scan, err
	}
	scan.agents = agents

	seen := map[string]bool{}
	add := func(dir string) (string, error) {
		repo, err := readTaskRepo(dir)
		if err != nil {
			return "", err
		}
		if !seen[repo.main] {
			seen[repo.main] = true
			scan.repos = append(scan.repos, repo)
		}
		return repo.main, nil
	}

	watched, err := watchedTaskRepos()
	if err != nil {
		return scan, err
	}
	for _, dir := range watched {
		main, err := add(dir)
		if err != nil {
			scan.warnings = append(scan.warnings, err)
			continue
		}
		scan.watched[main] = true
	}
	for _, dir := range dirs {
		if dir != "" {
			_, _ = add(dir)
		}
	}
	for _, s := range sessions {
		if s.path != "" {
			_, _ = add(s.path)
		}
	}
	return scan, nil
}

// tasks 는 훑은 것을 작업 목록으로 만든다. 저장소 경로, id 순이다.
func (scan taskScan) tasks() []Task {
	var tasks []Task
	withWorktree := map[string]bool{}
	for _, repo := range scan.repos {
		for _, tree := range repo.trees[1:] {
			id, ok := taskWorktreeID(repo.main, tree.path)
			if !ok || tree.prunable || !isDir(tree.path) {
				continue
			}
			withWorktree[id] = true
			tasks = append(tasks, Task{
				ID:       id,
				Repo:     repo.main,
				Worktree: tree.path,
				Branch:   strings.TrimPrefix(tree.branch, "refs/heads/"),
				locked:   tree.locked,
			})
		}
	}
	for id := range scan.sessions {
		if !withWorktree[id] {
			tasks = append(tasks, Task{ID: id})
		}
	}
	for i := range tasks {
		tasks[i] = scan.fill(tasks[i])
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Repo != tasks[j].Repo {
			return tasks[i].Repo < tasks[j].Repo
		}
		return tasks[i].ID < tasks[j].ID
	})
	return tasks
}

// fill 은 세션·상태·제목을 채운다.
func (scan taskScan) fill(t Task) Task {
	if s, ok := scan.sessions[t.ID]; ok {
		if t.Worktree != "" && !sessionInWorktree(s.path, t.Worktree) {
			t.foreign = s.path
		} else {
			t.Session, t.sessionPath = s.name, s.path
			t.Agent = mostUrgentAgent(scan.agents, s.name)
		}
	}
	switch {
	case t.Session == "":
		t.State = TaskNoSession
	case t.Worktree == "":
		t.State = TaskNoWorktree
	case t.Agent == nil:
		t.State = TaskNoClaude
	default:
		t.State = TaskState(t.Agent.State)
	}
	if t.Agent != nil {
		t.Since = t.Agent.Since
	}
	t.Title = taskDisplayTitle(t)
	return t
}

// sessionInWorktree 는 세션이 그 worktree 에서 열렸는지 본다. 복구도 저장된 폴더에서 열므로 재부팅 뒤에도 맞다. 폴더를
// 모르면 이름을 믿는다.
func sessionInWorktree(path, worktree string) bool {
	if path == "" || underDir(path, worktree) {
		return true
	}
	real, err := filepath.EvalSymlinks(path)
	return err == nil && underDir(real, worktree)
}

// find 는 id 의 작업들이다. 둘 이상이면 저장소가 다른 같은 id 다.
func (scan taskScan) find(id string) []Task {
	return slices.DeleteFunc(scan.tasks(), func(t Task) bool { return t.ID != id })
}

// agentUrgency 는 한 세션에 Claude 가 여럿일 때 무엇을 보일지의 순서다. 모르는 상태는 일하는 것으로 친다.
var agentUrgency = map[AgentState]int{
	AgentBlocked: 4, AgentWorking: 3, AgentUnknown: 2, AgentShell: 1, AgentIdle: 0,
}

func mostUrgentAgent(agents []Agent, session string) *Agent {
	var best *Agent
	for _, a := range agents {
		if a.Session != session {
			continue
		}
		if best == nil || agentUrgency[a.State] > agentUrgency[best.State] {
			picked := a
			best = &picked
		}
	}
	return best
}

// Tasks 는 작업 목록이다. dirs 는 설정 밖에서 더 볼 저장소다(--repo, 지금 폴더). warnings 는 읽지 못한 저장소다.
func Tasks(dirs ...string) (tasks []Task, warnings []error, err error) {
	scan, err := scanTasks(dirs...)
	if err != nil {
		return nil, nil, err
	}
	return scan.tasks(), scan.warnings, nil
}

// taskPanelTitle 은 패널이 세션 아래 줄에 쓸 사람이 준 제목이다. 작업 세션이고, 사람이 이름을 정했고, 그 이름이
// 세션 이름이 아닐 때만이다 — 제목 없이 시작한 작업의 대화 이름은 t-<id> 다.
func taskPanelTitle(session, userName string) string {
	if _, ok := TaskID(session); !ok || userName == session {
		return ""
	}
	return userName
}

// taskDisplayTitle 은 ls 의 제목이다: 사람이 준 제목, 없으면 저장된 Haiku 제목. 모델은 부르지 않는다.
func taskDisplayTitle(t Task) string {
	session := TaskSessionName(t.ID)
	sessionID, cwd := "", t.Worktree
	if t.Agent != nil {
		if t.Agent.namedByUser {
			if title := taskPanelTitle(session, t.Agent.Name); title != "" {
				return title
			}
		}
		sessionID, cwd = t.Agent.SessionID, t.Agent.CWD
	} else if t.Worktree != "" {
		conv, ok, err := latestTaskConversation(t.Worktree)
		if err != nil || !ok {
			return ""
		}
		if title := taskPanelTitle(session, conv.title); title != "" {
			return title
		}
		sessionID = conv.id
	}
	if sessionID == "" {
		return ""
	}
	work := claudeWork(sessionID, cwd)
	if work == "" {
		return ""
	}
	title, _ := storedWorkTitle(sessionID, work)
	return title
}

// lockTask 는 작업 id 하나의 잠금을 잡는다. start 둘이 겹치면 뒤의 것이 «worktree 는 있고 세션은 없음» 으로 읽어
// 프롬프트 없이 세션을 열고, 앞의 것은 이름이 겹쳐 실패한다 — 프롬프트를 잃는다.
func lockTask(id string) (func(), error) {
	dir := agentLockDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("잠금 디렉터리: %w", err)
	}
	unlock, err := flockWait(filepath.Join(dir, "task-"+id+".lock"), 0)
	if errors.Is(err, errLockBusy) {
		return nil, fmt.Errorf("다른 mux task 가 %s 를 다루는 중이다", id)
	}
	return unlock, err
}

var errLockBusy = errors.New("다른 프로세스가 잠금을 쥐고 있다")

// flockWait 은 파일 잠금을 limit 까지 기다려 잡는다. flock(1) 과 같은 잠금이라 셸 훅과도 맞물린다. 프로세스가 죽으면
// 커널이 풀어 준다.
func flockWait(path string, limit time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("잠금 파일: %w", err)
	}
	deadline := agentNow().Add(limit)
	for syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if !agentNow().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w (%s)", errLockBusy, path)
		}
		agentSleep(taskReadyPoll)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// PromptFromFile 은 --prompt-file 의 글이다. 한 줄로 보낼 수 있으면 그 글을, 아니면 파일을 읽으라는 한 줄을
// 만든다 — 입력창에는 한 줄·700자까지만 칠 수 있다(agent-api.md).
func PromptFromFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("프롬프트 파일: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("프롬프트 파일: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("프롬프트 파일이 비었다: %s", path)
	}
	if ValidatePromptText(text) == nil {
		return text, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("프롬프트 파일: %w", err)
	}
	text = abs + " 를 읽고 그대로 진행해"
	if err := ValidatePromptText(text); err != nil {
		return "", fmt.Errorf("프롬프트 파일 경로를 보낼 수 없다: %w", err)
	}
	return text, nil
}
