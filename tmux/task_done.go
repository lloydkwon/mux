package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// `mux task done` — 세션을 닫고 worktree 를 지운다. 브랜치는 지우지 않는다. 설계는 docs/design/task.md.
//
// 여기서 git 에 --force 를 주는 곳은 없다. --force 플래그가 넘기는 것은 «푸시 안 된 커밋» 하나뿐이다.

// taskExitTimeout 은 세션을 닫은 뒤 Claude 가 끝나기를 기다리는 한도다(끝낼 때 도는 훅이 있다). 테스트는 0 으로 둔다.
var taskExitTimeout = 10 * time.Second

const (
	// taskSaveLockLimit 은 다른 저장(사용자의 session-created 훅)이 끝나기를 기다리는 한도다.
	taskSaveLockLimit = 10 * time.Second
	// taskSaveHold 는 저장한 뒤 잠금을 더 쥐는 시간이다. 저장본 이름이 초 단위라 같은 초의 둘째 저장이 첫째의 파일을 지운다.
	taskSaveHold = time.Second
	// taskRestoreGrace 안에 뜬 서버에서는 저장하지 않는다. 복원 중인 일부 목록으로 저장본을 덮는다.
	taskRestoreGrace = 30 * time.Second
)

// TaskDoneRequest 는 done 의 인자다.
type TaskDoneRequest struct {
	ID    string
	Repo  string // --repo. 비면 설정·지금 폴더에서 찾는다
	CWD   string
	Pane  string // 이 명령을 부른 pane ($TMUX_PANE). tmux 밖이면 빈 값
	Force bool
}

// TaskDone 은 done 의 결과다.
type TaskDone struct {
	Task  Task
	Notes []string
}

func (r *TaskDone) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// FinishTask 는 작업을 정리한다. 확인이 다 끝나기 전에는 아무것도 닫거나 지우지 않는다.
func FinishTask(req TaskDoneRequest) (TaskDone, error) {
	var res TaskDone
	if err := ValidTaskID(req.ID); err != nil {
		return res, err
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

	scan, t, err := scanTaskToFinish(req)
	if err != nil {
		return res, err
	}
	for _, w := range scan.warnings {
		res.note("%v", w)
	}
	res.Task = t

	if _, err := checkTaskIdle(scan, t, req.Pane); err != nil {
		return res, err
	}
	if t.Worktree != "" {
		if err := checkTaskWorktree(t, req.Force); err != nil {
			return res, err
		}
	}
	// git 을 읽는 동안 Claude 가 일을 시작했을 수 있다. 닫기 바로 앞에서 새로 본다.
	scan, t, err = scanTaskToFinish(req)
	if err != nil {
		return res, err
	}
	if t.Worktree != res.Task.Worktree {
		return res, fmt.Errorf("확인하는 사이 작업 %s 가 바뀌었다 (%q → %q) — 다시 하라", t.ID, res.Task.Worktree, t.Worktree)
	}
	pids, err := checkTaskIdle(scan, t, req.Pane)
	if err != nil {
		return res, err
	}
	if t.Session != "" {
		if err := runner.Run("tmux", "kill-session", "-t", "="+t.Session); err != nil {
			return res, fmt.Errorf("세션 %s 를 닫지 못했다: %w", t.Session, err)
		}
		if pid := waitPIDsExit(pids, taskExitTimeout); pid != 0 && t.Worktree != "" {
			return res, fmt.Errorf("%w: 세션은 닫았고 worktree 는 남겼다 — Claude(pid %d)가 아직 끝나지 않았다", ErrTaskRefused, pid)
		}
	}
	if t.Worktree != "" {
		if err := removeTaskWorktree(t); err != nil {
			return res, err
		}
	}
	if err := saveSessionsNow(); err != nil {
		res.note("세션 저장을 하지 못했다 — 다음 자동 저장 전에 재부팅하면 이 작업이 되살아날 수 있다: %v", err)
	}
	return res, nil
}

// taskRescans 는 세션은 있는데 Claude 가 안 보일 때 다시 읽는 횟수다.
const taskRescans = 3

// scanTaskToFinish 는 닫을 작업을 찾는다. 세션은 있는데 Claude 가 안 보이면 다시 읽는다 — Claude 는 상태 파일을
// 제자리에 덮어써서 반쯤 쓰인 순간에는 읽히지 않고, 상태가 바뀌는 때가 바로 일을 시작하는 때다.
func scanTaskToFinish(req TaskDoneRequest) (taskScan, Task, error) {
	for try := 0; ; try++ {
		scan, err := scanTasks(req.Repo, req.CWD)
		if err != nil {
			return scan, Task{}, err
		}
		t, err := taskToFinish(scan, req)
		if err != nil {
			return scan, Task{}, err
		}
		if t.Session == "" || t.Agent != nil || try >= taskRescans {
			return scan, t, nil
		}
		agentSleep(taskReadyPoll)
	}
}

// taskToFinish 는 닫을 작업 하나를 고른다.
func taskToFinish(scan taskScan, req TaskDoneRequest) (Task, error) {
	found := scan.find(req.ID)
	if req.Repo != "" {
		repo, err := readTaskRepo(req.Repo)
		if err != nil {
			return Task{}, err
		}
		kept := found[:0:0]
		for _, t := range found {
			if t.Repo == repo.main || t.Repo == "" {
				kept = append(kept, t)
			}
		}
		found = kept
	}
	switch len(found) {
	case 0:
		return Task{}, fmt.Errorf("%w: %s (mux task ls)", ErrNoTask, req.ID)
	case 1:
		return found[0], nil
	}
	return Task{}, fmt.Errorf("작업 %s 가 여러 저장소에 있다 — --repo 로 고른다: %s", req.ID, taskPlaces(found))
}

// checkTaskIdle 은 지금 닫아도 되는지 본다. 그 세션에서 도는 Claude 의 pid 들을 돌려준다.
//
// 세션 밖의 Claude 도 본다 — 같은 폴더를 herdr 나 다른 세션에서 열어 두었을 수 있고, worktree 를 지우면 그쪽이
// 일하던 폴더가 사라진다.
func checkTaskIdle(scan taskScan, t Task, pane string) ([]int, error) {
	if t.foreign != "" {
		return nil, fmt.Errorf("%w: 세션 %s 는 이 작업의 것이 아니다 (%s 에서 열렸다)", ErrTaskRefused, TaskSessionName(t.ID), t.foreign)
	}
	if t.Session != "" && pane != "" {
		name, err := paneSessionName(pane)
		if err != nil {
			return nil, err
		}
		if name == t.Session {
			return nil, fmt.Errorf("%w: %s 안에서는 닫을 수 없다 — 세션을 닫으면 이 명령도 끝나 worktree 가 남는다", ErrTaskRefused, t.Session)
		}
	}
	if t.Worktree == "" {
		if err := checkLoneSession(scan, t); err != nil {
			return nil, err
		}
	}
	var pids []int
	for _, a := range scan.agents {
		if t.Session == "" || a.Session != t.Session {
			continue
		}
		if err := idleOrRefuse(a.State, a.PaneID, a.WaitingFor); err != nil {
			return nil, err
		}
		pids = append(pids, a.PID)
	}
	if t.Worktree == "" {
		return pids, nil
	}
	for _, sf := range liveClaudeSessionFiles() {
		if !underDir(sf.CWD, t.Worktree) || slices.Contains(pids, sf.PID) {
			continue
		}
		if err := idleOrRefuse(agentStateOf(sf.Status, sf.PID), "pid "+strconv.Itoa(sf.PID), sf.WaitingFor); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: 세션 밖의 Claude(pid %d)가 이 worktree 를 열고 있다 — 다른 tmux 서버나 herdr 일 수 있다. 끝낸 뒤 다시 하라", ErrTaskRefused, sf.PID)
	}
	return pids, nil
}

// checkLoneSession 은 worktree 가 안 보이는 세션을 닫아도 되는지 본다. 닫아도 되는 것은 이미 지운 작업이 복구로 되살아난
// 것뿐이다 — 작업의 자리에서 열렸고 그 폴더가 없다. 저장소를 못 읽었을 뿐이면 커밋 안 된 변경을 보지 않은 채 닫게 되고,
// 작업의 자리가 아니면 사람이 만든 세션이다.
func checkLoneSession(scan taskScan, t Task) error {
	place := taskPlaceOf(t.sessionPath, t.ID)
	if place == "" {
		return fmt.Errorf("%w: 세션 %s 는 mux task 가 만든 것이 아니다 (%s 에서 열렸다)", ErrTaskRefused, t.Session, t.sessionPath)
	}
	if _, err := os.Lstat(place); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s 가 있는데 git worktree 로 읽히지 않는다 — 저장소를 확인한 뒤 다시 하라", place)
	}
	if len(scan.warnings) > 0 {
		return fmt.Errorf("작업의 worktree 를 찾지 못했는데 읽지 못한 저장소가 있다: %w", errors.Join(scan.warnings...))
	}
	return nil
}

// taskPlaceOf 는 path 이거나 그 위에 있는 작업의 자리(«…-t-<id>»)다. 없으면 빈 값. 복구는 세션을 하위 폴더에서 열 수도 있다.
func taskPlaceOf(path, id string) string {
	for filepath.IsAbs(path) && path != "/" {
		if strings.HasSuffix(filepath.Base(path), "-"+TaskSessionName(id)) {
			return path
		}
		path = filepath.Dir(path)
	}
	return ""
}

func idleOrRefuse(state AgentState, where, waitingFor string) error {
	switch state {
	case AgentIdle:
		return nil
	case AgentBlocked:
		return fmt.Errorf("%w: %s (%s)", ErrAgentBlocked, where, waitingFor)
	default:
		return fmt.Errorf("%w: Claude 가 일하고 있다 (%s, %s)", ErrTaskRefused, where, state)
	}
}

// paneSessionName 은 pane 을 가진 세션이다.
func paneSessionName(pane string) (string, error) {
	out, err := runner.Output("tmux", "display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return "", fmt.Errorf("이 pane(%s)의 세션을 알 수 없다: %w", pane, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// underDir 은 path 가 dir 이거나 그 아래인지 본다. 이름이 앞부분만 같은 이웃(t-m1 과 t-m12)을 가른다.
func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsAbs(path) && rel != ".." && !strings.HasPrefix(rel, "../")
}

// checkTaskWorktree 는 지우면 잃을 것이 있는지 본다.
func checkTaskWorktree(t Task, force bool) error {
	if t.locked {
		return fmt.Errorf("%w: worktree 가 잠겨 있다 — git -C %s worktree unlock %s 뒤에 다시 하라", ErrTaskRefused, t.Repo, t.Worktree)
	}
	if err := checkTaskLoss(t); err != nil {
		return err
	}
	if force {
		return nil
	}
	n, err := unpushedCommits(t)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d개 (%s)", ErrTaskUnpushed, n, t.Worktree)
	}
	return nil
}

// checkTaskLoss 는 지우면 어디에도 남지 않는 것이 있는지 본다: 커밋 안 된 변경과, 아무 ref 도 가리키지 않는 커밋.
//
// 뒤의 것은 detached HEAD 의 커밋이고 git worktree remove 는 막지 않는다. 브랜치 위면 그 브랜치가 가리키므로 늘 통과한다 —
// 스캔 때의 브랜치 이름을 믿지 않고 HEAD 를 그때그때 읽는다.
func checkTaskLoss(t Task) error {
	if err := checkTaskClean(t); err != nil {
		return err
	}
	out, err := outputWithin(worktreeReadLimit, "git", "-C", t.Worktree, "for-each-ref", "--count=1", "--contains", "HEAD",
		"--format=%(refname)", "refs/heads/", "refs/remotes/", "refs/tags/")
	if err != nil {
		return fmt.Errorf("커밋을 확인하지 못했다: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("%w: detached HEAD 의 커밋을 가리키는 브랜치가 없다 — 지우면 잃는다 (%s)", ErrTaskRefused, t.Worktree)
	}
	return nil
}

// checkTaskClean 은 커밋 안 된 변경이 없는지 본다. 추적 안 된 파일을 숨기는 저장소 설정(status.showUntrackedFiles
// no)이 있어도 보이게 명시한다.
func checkTaskClean(t Task) error {
	out, err := outputWithin(worktreeReadLimit, "git", "-C", t.Worktree, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("변경을 확인하지 못했다: %w", err)
	}
	if dirty := strings.TrimRight(string(out), "\n"); dirty != "" {
		return fmt.Errorf("%w: 커밋 안 된 변경이 있다 (%s)\n%s", ErrTaskRefused, t.Worktree, firstLines(dirty, maxDirtyLines))
	}
	return nil
}

// maxDirtyLines 는 오류에 보일 변경의 수다.
const maxDirtyLines = 10

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… 외 %d개", len(lines)-n)
}

// unpushedCommits 는 푸시 안 된 커밋의 수다. 네트워크를 쓰지 않는다.
//
// 원격이 없는 저장소에서는 이력 전체가 «푸시 안 됨» 이 되므로, 다른 로컬 브랜치에 없는 커밋으로 센다.
func unpushedCommits(t Task) (int, error) {
	remotes, err := outputWithin(worktreeReadLimit, "git", "-C", t.Worktree, "remote")
	if err != nil {
		return 0, fmt.Errorf("원격 목록을 읽지 못했다: %w", err)
	}
	args := []string{"-C", t.Worktree, "rev-list", "--count", "HEAD", "--not"}
	switch {
	case strings.TrimSpace(string(remotes)) != "":
		args = append(args, "--remotes")
	case t.Branch != "":
		args = append(args, "--exclude="+t.Branch, "--branches")
	default:
		args = append(args, "--branches")
	}
	out, err := outputWithin(worktreeReadLimit, "git", args...)
	if err != nil {
		return 0, fmt.Errorf("푸시 안 된 커밋을 세지 못했다: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("푸시 안 된 커밋을 세지 못했다: %w", err)
	}
	return n, nil
}

// removeTaskWorktree 는 세션을 닫은 뒤에 worktree 를 지운다. 닫는 사이 파일이나 커밋이 생겼을 수 있어 한 번 더 본다.
//
// 설정을 명시하는 것이 load-bearing 이다 — showUntrackedFiles 가 no 인 저장소에서는 git worktree remove 도 추적 안
// 된 파일을 못 보고 함께 지운다(e2e 가 진짜 git 으로 잰다).
func removeTaskWorktree(t Task) error {
	if err := checkTaskLoss(t); err != nil {
		return fmt.Errorf("세션은 닫았고 worktree 는 남겼다: %w", err)
	}
	if _, err := outputWithin(worktreeAddLimit, "git", "-C", t.Repo, "-c", "status.showUntrackedFiles=normal",
		"worktree", "remove", t.Worktree); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}

// waitPIDsExit 는 프로세스들이 끝나기를 limit 까지 기다린다. 그래도 남은 것이 있으면 그 pid 하나, 없으면 0.
func waitPIDsExit(pids []int, limit time.Duration) int {
	deadline := agentNow().Add(limit)
	for _, pid := range pids {
		for pid > 0 && isAlive(pid, "") {
			if !agentNow().Before(deadline) {
				return pid
			}
			agentSleep(taskReadyPoll)
		}
	}
	return 0
}

// saveSessionsNow 는 resurrect 저장을 한 번 부른다. 플러그인을 쓰지 않으면 아무것도 하지 않는다.
//
// 닫은 세션은 다음 자동 저장(15분)까지 저장본에 남는다. 그 사이 재부팅하면 지운 작업이 되살아나 홈 폴더에서
// Claude 를 연다. tmux.conf 가 session-closed 에 저장을 걸지 않는 이유(종료 때 세션이 하나씩 죽으며 빈 저장본을
// 남긴다)는 사람이 부른 done 에는 해당하지 않는다.
//
// 사용자의 session-created 훅과 같은 규칙으로 부른다: 같은 잠금 파일, 저장 뒤 1초 더, 서버가 뜬 지 30초 뒤부터. 셋 다
// 저장본(last)이 깨진 사고에서 나왔다(~/.tmux.conf 의 주석).
func saveSessionsNow() error {
	out, err := runner.Output("tmux", "show-options", "-gqv", "@resurrect-save-script-path")
	if err != nil {
		return fmt.Errorf("tmux 에 묻지 못했다: %w", err)
	}
	script := strings.TrimSpace(string(out))
	if script == "" {
		return nil
	}
	if _, err := os.Stat(script); err != nil {
		return errors.New("저장 스크립트가 없다: " + script)
	}
	if err := checkServerAge(); err != nil {
		return err
	}
	dir, err := resurrectDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("저장 폴더: %w", err)
	}
	unlock, err := flockWait(filepath.Join(dir, ".save.lock"), taskSaveLockLimit)
	if err != nil {
		return err
	}
	defer unlock()
	// run-shell 은 끝을 기다려 종료 코드를 돌려주고, 인자를 포맷으로 펼친다(tmux-assumptions 21절) — # 를 막는다.
	err = runner.Run("tmux", "run-shell", strings.ReplaceAll(shellQuote(script)+" quiet", "#", "##"))
	agentSleep(taskSaveHold)
	return err
}

// checkServerAge 는 서버가 복원을 마쳤을 만큼 오래됐는지 본다. 모르면 저장하지 않는 쪽이다.
func checkServerAge() error {
	out, err := runner.Output("tmux", "display-message", "-p", "#{start_time}")
	if err != nil {
		return fmt.Errorf("서버가 뜬 시각을 읽지 못했다: %w", err)
	}
	start, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return fmt.Errorf("서버가 뜬 시각을 읽지 못했다: %w", err)
	}
	if agentNow().Sub(time.Unix(start, 0)) < taskRestoreGrace {
		return fmt.Errorf("서버가 뜬 지 %v 가 지나지 않았다 — 복원 중일 수 있다", taskRestoreGrace)
	}
	return nil
}
