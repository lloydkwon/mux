//go:build e2e

// `mux task start/ls/done` 을 격리 tmux + 진짜 git + 가짜 Claude 로 끝까지 돌린다(docs/design/task.md).
// git 의 «조용히 잃는» 동작 둘과 무시된 파일, resurrect 왕복도 여기서 진짜로 고정한다.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

type taskRow struct {
	ID, Repo, Worktree, Branch, Session, Pane, SessionID, State, Title string
	Since                                                              int64
	Commits                                                            int
}

// muxTaskEnv 는 `mux task …` 를 dir 에서 돌려 stdout·stderr·종료 코드를 돌려준다. env 의 «-이름» 은 그 변수를 지운다 —
// 빈 값(TMUX=)은 지운 것이 아니다. tmux 는 TMUX 가 있기만 하면 클라이언트를 UTF-8 로 본다.
func muxTaskEnv(t *testing.T, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	fakeClaude(t) // 씨앗과 세션이 PATH 의 claude 를 부른다
	cmd := exec.Command(muxBin, append([]string{"task"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMUX_PANE=")
	for _, kv := range env {
		if name, unset := strings.CutPrefix(kv, "-"); unset {
			cmd.Env = slices.DeleteFunc(cmd.Env, func(have string) bool { return strings.HasPrefix(have, name+"=") })
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mux task %v: %v", args, err)
	}
	return strings.TrimSpace(stdout.String()), stderr.String(), code
}

func muxTask(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	return muxTaskEnv(t, dir, nil, args...)
}

func taskByID(t *testing.T, dir, id string) *taskRow {
	t.Helper()
	out, errOut, code := muxTask(t, dir, "ls", "--json")
	if code != 0 {
		t.Fatalf("ls: exit %d\n%s", code, errOut)
	}
	var rows []taskRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	for _, r := range rows {
		if r.ID == id {
			return &r
		}
	}
	return nil
}

// taskRepo 는 커밋 하나와 로컬 bare 원격이 있는 저장소다. 설정 파일에 등록해 둔다.
func taskRepo(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	repo := gitRepo(t, parent, "taskrepo")
	git(t, repo, "config", "user.name", "e2e")
	git(t, repo, "config", "user.email", "e2e@example.com")
	git(t, parent, "init", "-q", "--bare", "remote.git")
	git(t, repo, "remote", "add", "origin", filepath.Join(parent, "remote.git"))
	git(t, repo, "push", "-q", "origin", "main")
	conf := filepath.Join(home, ".config", "mux", "task-repos.txt")
	if err := os.WriteFile(conf, []byte("# e2e\n"+repo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(conf) })
	return repo
}

// waitTaskIdle 은 첫 프롬프트의 턴이 끝나기를 기다린다 — start 는 제출만 확인하고 돌아온다.
func waitTaskIdle(t *testing.T, repo, id string) {
	t.Helper()
	eventually(t, id+" 의 턴이 끝난다", 10*time.Second, func() bool {
		row := taskByID(t, repo, id)
		return row != nil && row.State == "idle"
	})
}

func killTask(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() { tmq("kill-session", "-t", "=t-"+id) })
}

func hasSession(name string) bool {
	_, err, _ := runTmux("has-session", "-t", "="+name)
	return err == nil
}

// claudeIn 은 dir 에서 도는 Claude 의 pid 다.
func claudeIn(t *testing.T, dir string) int {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	for _, f := range files {
		var sf struct {
			PID int    `json:"pid"`
			CWD string `json:"cwd"`
		}
		if data, err := os.ReadFile(f); err == nil && json.Unmarshal(data, &sf) == nil && sf.CWD == dir && running(sf.PID) {
			return sf.PID
		}
	}
	t.Fatalf("%s 에서 도는 Claude 가 없다", dir)
	return 0
}

// running 은 프로세스가 돌고 있는지 본다. 끝났는데 거두어지지 않은 것(좀비)은 도는 것이 아니다 — init 없는 컨테이너에서는
// 그대로 남는다.
func running(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err == nil && !strings.Contains(string(stat), ") Z ")
}

func commitFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", "add "+name)
}

// 요청서의 여섯 순서.
func TestTaskLifecycle(t *testing.T) {
	repo := taskRepo(t)
	wt := repo + "-t-m1"
	const title = "로그인 오류"
	killTask(t, "m1")

	// 1. 시작 — ls 에 보이고 프롬프트가 한 번 들어간다.
	start := []string{"start", "m1", "--repo", repo, "--title", title, "--prompt", "Mantis #1 을 읽고 진행"}
	token, errOut, code := muxTask(t, repo, start...)
	if code != 0 || token == "" {
		t.Fatalf("start: exit %d, token %q\n%s", code, token, errOut)
	}
	if _, errOut, code := muxAgent(t, "wait", "t-m1", "--after", token, "--timeout", "30s"); code != 0 {
		t.Fatalf("wait: exit %d\n%s", code, errOut)
	}
	row := taskByID(t, "/", "m1")
	if row == nil || row.Repo != repo || row.Worktree != wt || row.Branch != "t-m1" || row.Session != "t-m1" ||
		row.State != "idle" || row.Title != title || row.Pane == "" || row.SessionID == "" || row.Since == 0 || row.Commits != 0 {
		t.Fatalf("ls = %+v", row)
	}
	conversation := row.SessionID
	if got := fakeLog(title); got != "Mantis #1 을 읽고 진행\n" {
		t.Fatalf("가짜 Claude 가 받은 것 = %q", got)
	}
	if got := tm(t, "display-message", "-p", "-t", "=t-m1:", "#{session_path}|#{pane_current_path}"); got != wt+"|"+wt {
		t.Errorf("세션 폴더 = %q, want %q", got, wt)
	}
	if got := tmq("show-options", "-qv", "-t", "=t-m1:", "@project_dir"); got != "" {
		t.Errorf("@project_dir = %q — 달면 패널이 물러난다", got)
	}

	// 2. 같은 명령 — 아무 변화 없음.
	before := tm(t, "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}")
	out, errOut, code := muxTask(t, repo, start...)
	if code != 0 || out != "" || !strings.Contains(errOut, "이미 있다") {
		t.Errorf("두 번째 start: exit %d, %q, %q", code, out, errOut)
	}
	if after := tm(t, "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}"); after != before {
		t.Errorf("pane 이 바뀌었다:\n%s\n→\n%s", before, after)
	}

	// 3. 세션을 직접 종료 — «세션 없음». 제목은 기록에서 온다.
	tm(t, "kill-session", "-t", "=t-m1")
	if row := taskByID(t, "/", "m1"); row == nil || row.State != "no-session" || row.Session != "" || row.Title != title {
		t.Fatalf("ls = %+v", row)
	}

	// 4. 다시 열기 — 같은 대화, 프롬프트는 다시 보내지 않는다. 저장소 밖에서 불러도 설정으로 찾는다.
	out, errOut, code = muxTask(t, "/", "start", "m1")
	if code != 0 || out != "" {
		t.Fatalf("다시 열기: exit %d, %q\n%s", code, out, errOut)
	}
	eventually(t, "같은 대화로 이어진다", 15*time.Second, func() bool {
		row := taskByID(t, "/", "m1")
		return row != nil && row.State == "idle" && row.SessionID == conversation
	})
	time.Sleep(1500 * time.Millisecond)
	if got := fakeLog(title); got != "Mantis #1 을 읽고 진행\n" {
		t.Errorf("프롬프트가 다시 들어갔다: %q", got)
	}

	// 5. 커밋 안 된 파일 — --force 여도 거절.
	stray := filepath.Join(wt, "a.txt")
	if err := os.WriteFile(stray, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := muxTask(t, "/", "done", "m1", "--force"); code != 5 || !strings.Contains(errOut, "a.txt") {
		t.Errorf("done --force: exit %d, %q", code, errOut)
	}
	if _, err := os.Stat(stray); err != nil || !hasSession("t-m1") {
		t.Fatalf("거절했는데 손댔다: %v, session %v", err, hasSession("t-m1"))
	}

	// 6. 커밋만으로는 «푸시 안 됨»(8), 푸시하면 정리된다. 브랜치는 남는다.
	git(t, wt, "add", "a.txt")
	git(t, wt, "commit", "-q", "-m", "a")
	if row := taskByID(t, "/", "m1"); row == nil || row.Commits != 1 {
		t.Errorf("커밋 하나 뒤의 ls = %+v", row)
	}
	if _, errOut, code := muxTask(t, "/", "done", "m1"); code != 8 || !hasSession("t-m1") {
		t.Errorf("푸시 전 done: exit %d, %q", code, errOut)
	}
	git(t, wt, "push", "-q", "origin", "t-m1")
	if _, errOut, code := muxTask(t, "/", "done", "m1"); code != 0 {
		t.Fatalf("done: exit %d\n%s", code, errOut)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) || hasSession("t-m1") {
		t.Errorf("worktree %v, session %v", err, hasSession("t-m1"))
	}
	if got := git(t, repo, "branch", "--list", "t-m1"); got == "" {
		t.Error("브랜치가 사라졌다")
	}
	if row := taskByID(t, "/", "m1"); row != nil {
		t.Errorf("끝낸 작업이 ls 에 남았다: %+v", row)
	}
	// 남은 브랜치 위에 조용히 다시 시작하지 않는다.
	if _, errOut, code := muxTask(t, repo, start...); code != 1 || !strings.Contains(errOut, "브랜치가 이미 있다") {
		t.Errorf("같은 id 다시: exit %d, %q", code, errOut)
	}
	if _, errOut, code := muxTask(t, "/", "done", "m1"); code != 1 {
		t.Errorf("없는 작업 done: exit %d, %q", code, errOut)
	}
}

// 이름이 앞부분만 같은 이웃 작업을 건드리지 않는다 — `=` 없는 kill-session 은 t-m1 이 없으면 t-m12 를 죽인다.
func TestTaskDoneLeavesTheNeighbourAlone(t *testing.T) {
	repo := taskRepo(t)
	for _, id := range []string{"n1", "n12"} {
		killTask(t, id)
		if _, errOut, code := muxTask(t, repo, "start", id, "--prompt", "이웃 시험"); code != 0 {
			t.Fatalf("start %s: exit %d\n%s", id, code, errOut)
		}
		waitTaskIdle(t, repo, id)
	}
	// 닫힌 세션의 Claude 가 아직 살아 있는 동안의 done 은 다음 시험이 본다. 여기서 재는 것은 이웃이다.
	pid := claudeIn(t, repo+"-t-n1")
	tm(t, "kill-session", "-t", "=t-n1")
	eventually(t, "닫힌 세션의 Claude 가 끝난다", 10*time.Second, func() bool { return !running(pid) })
	if _, errOut, code := muxTask(t, repo, "done", "n1"); code != 0 {
		t.Fatalf("done n1: exit %d\n%s", code, errOut)
	}
	if !hasSession("t-n12") {
		t.Error("이웃 작업의 세션을 죽였다")
	}
	if row := taskByID(t, repo, "n12"); row == nil || row.Session == "" {
		t.Errorf("n12 = %+v", row)
	}
}

// 다른 git 이 같은 저장소에 worktree 를 만드는 순간에는 worktree 명령이 128 로 끝난다(git 이 이웃의 빈 commondir 을 읽는다). 그
// 순간을 훅으로 만든다: 작업의 브랜치가 생기면 이웃의 commondir 을 잠깐 비운다.
func TestTaskStartGetsPastAHalfMadeWorktree(t *testing.T) {
	repo := taskRepo(t)
	git(t, repo, "worktree", "add", "-q", "-b", "other", repo+"-other")
	commondir := filepath.Join(repo, ".git", "worktrees", filepath.Base(repo)+"-other", "commondir")
	mark := filepath.Join(t.TempDir(), "fired")
	hook := "#!/bin/sh\n[ \"$1\" = committed ] && grep -q refs/heads/t-race1 || exit 0\n[ -e " + mark + " ] && exit 0\n" +
		"cp " + commondir + " " + mark + " && : > " + commondir + "\n" +
		"(sleep 0.3; cp " + mark + " " + commondir + ") </dev/null >/dev/null 2>&1 &\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", "reference-transaction"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	killTask(t, "race1")
	out, errOut, code := muxTask(t, repo, "start", "race1", "--prompt", "겹친 시작")
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("훅이 돌지 않았다 — 이 시험은 아무것도 재지 않는다: %v", err)
	}
	if code != 0 || out == "" {
		t.Fatalf("start: exit %d, 토큰 %q\n%s", code, out, errOut)
	}
	waitTaskIdle(t, repo, "race1")
}

// 세션을 닫자마자 부른 done 은 그 Claude 가 끝나기를 기다렸다가 지운다. 진짜 Claude 는 끝낼 때 훅이 돌아 몇 초 더 산다.
func TestTaskDoneWaitsForAClosingClaude(t *testing.T) {
	repo := taskRepo(t)
	commitFile(t, repo, ".fake-slow-exit") // 가짜 Claude 는 이 파일이 있는 폴더에서 천천히 끝난다
	git(t, repo, "push", "-q", "origin", "main")
	killTask(t, "slow1")
	if _, errOut, code := muxTask(t, repo, "start", "slow1", "--prompt", "천천히 끝나는 시험"); code != 0 {
		t.Fatalf("start: exit %d\n%s", code, errOut)
	}
	waitTaskIdle(t, repo, "slow1")
	wt := repo + "-t-slow1"
	pid := claudeIn(t, wt)
	tm(t, "kill-session", "-t", "=t-slow1")
	if !running(pid) {
		t.Fatal("가짜 Claude 가 바로 끝났다 — 이 시험은 아무것도 재지 않는다")
	}
	if _, errOut, code := muxTask(t, repo, "done", "slow1"); code != 0 {
		t.Fatalf("done: exit %d\n%s", code, errOut)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) || running(pid) {
		t.Errorf("worktree %v, Claude 살아 있음 %v", err, running(pid))
	}
}

// Claude 를 끝내면 worktree 폴더의 셸이 남는다. start 는 새로 열지 않고 알린다.
func TestTaskKeepsAShellWhenClaudeExits(t *testing.T) {
	repo := taskRepo(t)
	killTask(t, "sh1")
	if _, errOut, code := muxTask(t, repo, "start", "sh1", "--prompt", "셸 시험"); code != 0 {
		t.Fatalf("start: exit %d\n%s", code, errOut)
	}
	waitTaskIdle(t, repo, "sh1")
	tm(t, "send-keys", "-t", "=t-sh1:", "C-c")
	eventually(t, "Claude 없음", 10*time.Second, func() bool {
		row := taskByID(t, repo, "sh1")
		return row != nil && row.State == "no-claude" && row.Session == "t-sh1"
	})
	if got := tm(t, "display-message", "-p", "-t", "=t-sh1:", "#{pane_current_path}"); got != repo+"-t-sh1" {
		t.Errorf("셸의 폴더 = %q", got)
	}
	out, errOut, code := muxTask(t, repo, "start", "sh1", "--prompt", "셸 시험")
	if code != 0 || out != "" || !strings.Contains(errOut, "claude --resume ") {
		t.Errorf("start: exit %d, %q, %q", code, out, errOut)
	}
	// 셸만 남은 세션에는 프롬프트를 보낼 곳이 없다.
	if _, _, code := muxAgent(t, "prompt", "t-sh1", "echo hi"); code != 1 {
		t.Errorf("셸 pane 에 prompt: exit %d, want 1", code)
	}
}

// 로캘이 UTF-8 이 아니거나(cron, 원격 ssh 명령) 다른 소켓을 보는 곳에서 부른 done 이 살아 있는 세션을 «없음» 으로 읽고
// worktree 부터 지우지 않는다. 둘 다 검토에서 실제 서버로 재현됐다.
func TestTaskDoneFromAForeignEnvironment(t *testing.T) {
	repo := taskRepo(t)
	killTask(t, "env1")
	if _, errOut, code := muxTask(t, repo, "start", "env1", "--prompt", "환경 시험"); code != 0 {
		t.Fatalf("start: exit %d\n%s", code, errOut)
	}
	waitTaskIdle(t, repo, "env1")
	wt := repo + "-t-env1"
	intact := func(what string) {
		t.Helper()
		if _, err := os.Stat(wt); err != nil || !hasSession("t-env1") {
			t.Fatalf("%s: worktree %v, session %v", what, err, hasSession("t-env1"))
		}
	}

	// 다른 소켓: 서버가 없는 것처럼 보인다. 기억은 패널·TUI 가 해 두는 것이다.
	tmux.RememberServer()
	emptyDir := t.TempDir()
	elsewhere := []string{"-TMUX", "TMUX_TMPDIR=" + emptyDir}
	if _, errOut, code := muxTaskEnv(t, repo, elsewhere, "done", "env1", "--force"); code != 1 || !strings.Contains(errOut, sock) {
		t.Errorf("다른 소켓에서 done: exit %d, %q", code, errOut)
	}
	intact("다른 소켓에서 done")
	if _, _, code := muxTaskEnv(t, repo, elsewhere, "start", "env2", "--prompt", "x"); code != 1 {
		t.Errorf("다른 소켓에서 start: exit %d — 둘째 서버를 띄우면 안 된다", code)
	}
	// 기억이 없어도 start 는 서버를 띄우지 않는다 — 빈 서버가 뜨면 복원이 시작된다.
	noMemory := append(slices.Clone(elsewhere), "XDG_CONFIG_HOME="+t.TempDir())
	if _, errOut, code := muxTaskEnv(t, repo, noMemory, "start", "env2", "--prompt", "x"); code != 1 || !strings.Contains(errOut, "서버가 없다") {
		t.Errorf("서버가 없을 때 start: exit %d, %q", code, errOut)
	}
	if _, err := os.Stat(repo + "-t-env2"); err == nil {
		t.Error("서버가 없는데 start 가 worktree 를 만들었다")
	}
	if socks, _ := filepath.Glob(filepath.Join(emptyDir, "*", "*")); len(socks) != 0 {
		t.Errorf("둘째 서버를 띄웠다: %v", socks)
	}

	// UTF-8 이 아닌 로캘: 같은 세션을 정확히 보고 정리까지 한다. $TMUX 가 있으면 tmux 가 UTF-8 로 간주해 시험이 되지
	// 않으므로, TMUX 없이 기본 이름의 소켓을 심링크로 닿게 한다.
	tmuxDir, err := os.MkdirTemp("/tmp", "mxe") // 소켓 경로 길이 한도
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	inner := filepath.Join(tmuxDir, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sock, filepath.Join(inner, "default")); err != nil {
		t.Fatal(err)
	}
	latin := []string{"-TMUX", "-LC_ALL", "-LC_CTYPE", "LANG=C", "TMUX_TMPDIR=" + tmuxDir}
	out, errOut, code := muxTaskEnv(t, repo, latin, "ls", "--json")
	if code != 0 || !strings.Contains(out, `"session":"t-env1"`) {
		t.Fatalf("LANG=C ls: exit %d, %s %s", code, out, errOut)
	}
	if _, errOut, code := muxTaskEnv(t, repo, latin, "done", "env1"); code != 0 {
		t.Errorf("LANG=C done: exit %d, %q", code, errOut)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) || hasSession("t-env1") {
		t.Errorf("LANG=C done 뒤: worktree %v, session %v", err, hasSession("t-env1"))
	}
}

// 대화상자에서 사람을 기다리는 Claude 에게는 첫 프롬프트를 보내지 않는다(3). 보내면 대화상자에 답하는 셈이다.
func TestTaskStartDoesNotTypeIntoADialog(t *testing.T) {
	repo := taskRepo(t)
	commitFile(t, repo, ".fake-dialog") // 가짜 Claude 는 이 파일이 있는 폴더에서 대화상자를 띄운 채 시작한다
	killTask(t, "dlg1")
	start := []string{"start", "dlg1", "--repo", repo, "--prompt", "대화상자에 들어가면 안 되는 글"}
	out, errOut, code := muxTask(t, repo, start...)
	if code != 3 || out != "" {
		t.Fatalf("start: exit %d, %q\n%s", code, out, errOut)
	}
	row := taskByID(t, repo, "dlg1")
	if row == nil || row.State != "blocked" || row.Session != "t-dlg1" || row.Commits != 0 {
		t.Fatalf("ls = %+v", row)
	}
	// 대화상자에 글자가 한 자도 들어가지 않았다.
	if s := screen(t, row.Pane); !strings.Contains(s, "과일을 골라 주세요") || strings.Contains(s, "대화상자에 들어가면") {
		t.Errorf("화면:\n%s", s)
	}
	// 다시 불러도 보내지 않고, 막혀 있는 동안은 done 도 거절한다.
	if out, _, code := muxTask(t, repo, start...); code != 0 || out != "" {
		t.Errorf("두 번째 start: exit %d, %q", code, out)
	}
	if _, _, code := muxTask(t, repo, "done", "dlg1", "--force"); code != 3 || !hasSession("t-dlg1") {
		t.Errorf("막힌 채 done: exit %d, session %v", code, hasSession("t-dlg1"))
	}
	if got := fakeLog("t-dlg1"); got != "" {
		t.Errorf("가짜 Claude 가 받은 것 = %q", got)
	}
	// 사람이 대화상자를 치우면 그 뒤에는 보낼 수 있다.
	tm(t, "send-keys", "-t", row.Pane, "Escape")
	waitTaskIdle(t, repo, "dlg1")
	if _, errOut, code := muxAgent(t, "prompt", "t-dlg1", "이제 보낸다"); code != 0 {
		t.Errorf("치운 뒤 prompt: exit %d\n%s", code, errOut)
	}
	eventually(t, "치운 뒤의 글만 들어간다", 5*time.Second, func() bool { return fakeLog("t-dlg1") == "이제 보낸다\n" })
}

// git 이 조용히 잃는 것들을 진짜 git 으로 고정한다. git 이 바뀌어 막아 주기 시작하면 여기가 알려 준다.
func TestTaskDoneAgainstRealGit(t *testing.T) {
	wtOf := func(t *testing.T, repo, id string) string {
		wt := repo + "-t-" + id
		git(t, repo, "worktree", "add", "-q", "-b", "t-"+id, wt)
		return wt
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	t.Run("추적 안 된 파일을 숨기는 설정", func(t *testing.T) {
		repo := taskRepo(t)
		git(t, repo, "config", "status.showUntrackedFiles", "no")
		wt := wtOf(t, repo, "g1")
		os.WriteFile(filepath.Join(wt, "new.txt"), []byte("x"), 0o644)
		if got := git(t, wt, "status", "--porcelain"); got != "" {
			t.Fatalf("전제가 바뀌었다 — status 가 보여 준다: %q", got)
		}
		if _, errOut, code := muxTask(t, repo, "done", "g1", "--force"); code != 5 || !exists(filepath.Join(wt, "new.txt")) {
			t.Errorf("exit %d, %q, 남음=%v", code, errOut, exists(wt))
		}
		// 우리가 막지 않으면 git 은 파일째 지운다 — 그래서 설정을 명시한다.
		git(t, repo, "worktree", "remove", wt)
		if exists(wt) {
			t.Error("전제가 바뀌었다 — git 이 이제 막는다")
		}
	})

	t.Run("아무도 가리키지 않는 커밋", func(t *testing.T) {
		repo := taskRepo(t)
		wt := wtOf(t, repo, "g2")
		git(t, wt, "checkout", "-q", "--detach")
		commitFile(t, wt, "d.txt")
		if _, errOut, code := muxTask(t, repo, "done", "g2", "--force"); code != 5 || !exists(wt) {
			t.Errorf("exit %d, %q", code, errOut)
		}
		git(t, wt, "branch", "keep")
		if _, errOut, code := muxTask(t, repo, "done", "g2", "--force"); code != 0 || exists(wt) {
			t.Errorf("가리키는 브랜치가 생긴 뒤: exit %d, %q", code, errOut)
		}
	})

	t.Run("무시된 파일은 함께 사라진다", func(t *testing.T) {
		repo := taskRepo(t)
		wt := wtOf(t, repo, "g3")
		os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("secret.env\n"), 0o644)
		os.WriteFile(filepath.Join(wt, "secret.env"), []byte("x"), 0o644)
		if _, errOut, code := muxTask(t, repo, "done", "g3"); code != 0 || exists(wt) {
			t.Errorf("exit %d, %q", code, errOut)
		}
	})

	t.Run("원격이 없는 저장소", func(t *testing.T) {
		repo := taskRepo(t)
		git(t, repo, "remote", "remove", "origin")
		wt := wtOf(t, repo, "g4")
		commitFile(t, wt, "c.txt")
		if _, errOut, code := muxTask(t, repo, "done", "g4"); code != 8 {
			t.Errorf("다른 브랜치에 없는 커밋: exit %d, %q", code, errOut)
		}
		git(t, repo, "merge", "-q", "t-g4")
		if _, errOut, code := muxTask(t, repo, "done", "g4"); code != 0 || exists(wt) {
			t.Errorf("머지한 뒤: exit %d, %q", code, errOut)
		}
	})

	t.Run("잠긴 worktree", func(t *testing.T) {
		repo := taskRepo(t)
		wt := wtOf(t, repo, "g6")
		git(t, repo, "worktree", "lock", wt)
		tm(t, "new-session", "-d", "-s", "t-g6", "-c", wt)
		killTask(t, "g6")
		if _, errOut, code := muxTask(t, repo, "done", "g6", "--force"); code != 5 || !hasSession("t-g6") || !exists(wt) {
			t.Errorf("exit %d, %q, session %v", code, errOut, hasSession("t-g6"))
		}
		git(t, repo, "worktree", "unlock", wt)
		if _, errOut, code := muxTask(t, repo, "done", "g6"); code != 0 || hasSession("t-g6") || exists(wt) {
			t.Errorf("푼 뒤: exit %d, %q", code, errOut)
		}
	})

	t.Run("이름만 같은 사람의 세션", func(t *testing.T) {
		repo := taskRepo(t)
		wt := wtOf(t, repo, "g7")
		tm(t, "new-session", "-d", "-s", "t-g7", "-c", repo)
		killTask(t, "g7")
		if _, errOut, code := muxTask(t, repo, "done", "g7", "--force"); code != 5 || !hasSession("t-g7") || !exists(wt) {
			t.Errorf("exit %d, %q, session %v", code, errOut, hasSession("t-g7"))
		}
		if _, errOut, code := muxTask(t, repo, "start", "g7", "--prompt", "x"); code != 1 || !strings.Contains(errOut, "이 작업의 것이 아니다") {
			t.Errorf("start: exit %d, %q", code, errOut)
		}
	})

	t.Run("손댄 추적 파일", func(t *testing.T) {
		repo := taskRepo(t)
		commitFile(t, repo, "t.txt")
		wt := wtOf(t, repo, "g5")
		os.WriteFile(filepath.Join(wt, "t.txt"), []byte("changed"), 0o644)
		if _, _, code := muxTask(t, repo, "done", "g5", "--force"); code != 5 || !exists(wt) {
			t.Errorf("exit %d", code)
		}
	})
}

// 짧은 세션 여럿이 생겼다 사라져도 패널·이벤트 기록이 정상이다. 같은 이름이 다시 생겨도 옛 «종료» 가 보이지 않는다.
func TestShortTasksLeaveThePanelAndEventsSane(t *testing.T) {
	repo := taskRepo(t)
	const base = "e2e-task-panel"
	newSession(t, base)
	attach(t, base, 200, 40, false)
	w := eventuallyWindow(t, base, "패널이 열린다", 8*time.Second, func(w window) bool { return w.panel > 0 })
	panel := panelPane(t, w.id)

	ids := []string{"p1", "p2", "p3"}
	for _, id := range ids {
		killTask(t, id)
		if _, errOut, code := muxTask(t, repo, "start", id, "--title", "짧은 작업 "+id, "--prompt", "패널 시험 프롬프트를 보낸다"); code != 0 {
			t.Fatalf("start %s: exit %d\n%s", id, code, errOut)
		}
	}
	for _, id := range ids {
		eventually(t, "패널에 작업 표시와 제목이 보인다: "+id, 10*time.Second, func() bool {
			s := screen(t, panel)
			return strings.Contains(s, "#t-"+id) && strings.Contains(underSession(t, panel, "#t-"+id), "짧은 작업 "+id)
		})
	}
	if !strings.Contains(screen(t, panel), base) || strings.Contains(screen(t, panel), "#"+base) {
		t.Errorf("보통 세션에 표시가 붙었거나 사라졌다:\n%s", screen(t, panel))
	}
	// 제목이 있는 작업에는 제목 모델을 부르지 않는다.
	if data, _ := os.ReadFile(filepath.Join(home, "fake-titles.log")); strings.Contains(string(data), "패널 시험 프롬프트") {
		t.Errorf("제목이 있는데 모델을 불렀다:\n%s", data)
	}

	for _, id := range ids {
		waitTaskIdle(t, repo, id)
		if _, errOut, code := muxTask(t, repo, "done", id); code != 0 {
			t.Fatalf("done %s: exit %d\n%s", id, code, errOut)
		}
	}
	eventually(t, "패널에서 사라진다", 10*time.Second, func() bool { return !strings.Contains(screen(t, panel), "t-p") })
	eventually(t, "이벤트 옵션에 죽은 작업이 남지 않는다", 10*time.Second, func() bool {
		return !strings.Contains(tmq("show-options", "-gqv", "@mux_events"), `"t-p`)
	})
	if !hasSession(base) || !strings.Contains(screen(t, panel), base) {
		t.Errorf("오래 가는 세션이 패널에서 사라졌다:\n%s", screen(t, panel))
	}

	// 같은 이름의 세션이 다시 생긴다(사람이 만든 빈 세션). 옛 «종료» 를 물려받지 않아야 한다.
	tm(t, "new-session", "-d", "-s", "t-p1", "-c", repo)
	eventually(t, "다시 생긴 세션이 패널에 보인다", 10*time.Second, func() bool { return strings.Contains(screen(t, panel), "#t-p1") })
	time.Sleep(5 * time.Second) // 패널 틱 둘
	if s := tmq("show-options", "-gqv", "@mux_events"); strings.Contains(s, `"s":"t-p1","st":0`) {
		t.Errorf("옛 종료 이벤트를 되돌렸다: %s", s)
	}
	if got := underSession(t, panel, "#t-p1"); strings.Contains(got, "종료") {
		t.Errorf("살아 있는 세션 아래에 종료가 보인다: %q", got)
	}
}

// 작업 세션을 TUI 에서 보관(p)하면 worktree 는 남아 «세션 없음» 이 되고, start 는 보관된 그 대화를 잇는다.
func TestParkedTaskIsReopenedByStart(t *testing.T) {
	repo := taskRepo(t)
	killTask(t, "pk1")
	if _, errOut, code := muxTask(t, repo, "start", "pk1", "--prompt", "보관 시험"); code != 0 {
		t.Fatalf("start: exit %d\n%s", code, errOut)
	}
	sid := ""
	eventually(t, "턴이 끝난다", 10*time.Second, func() bool {
		row := taskByID(t, repo, "pk1")
		if row == nil || row.State != "idle" {
			return false
		}
		sid = row.SessionID
		return strings.Contains(transcriptOf(sid), "echo: 보관 시험")
	})
	if _, err := tmux.ParkSession(tmux.Session{Name: "t-pk1"}); err != nil {
		t.Fatalf("보관: %v", err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join(home, ".config", "mux", "parked.json")) })
	if row := taskByID(t, repo, "pk1"); row == nil || row.State != "no-session" {
		t.Fatalf("보관 뒤 ls = %+v", row)
	}
	if _, errOut, code := muxTask(t, repo, "start", "pk1"); code != 0 {
		t.Fatalf("다시 열기: exit %d\n%s", code, errOut)
	}
	eventually(t, "보관된 대화를 잇는다", 15*time.Second, func() bool {
		row := taskByID(t, repo, "pk1")
		return row != nil && row.SessionID == sid
	})
	// 보관함에 남은 항목을 되살려도 대화가 둘이 되지 않는다 — 살아 있는 세션으로 간다.
	list, err := tmux.LoadParked()
	if err != nil || len(list) != 1 {
		t.Fatalf("보관함 = %+v, %v", list, err)
	}
	name, err := tmux.RestoreParked(list[0])
	if err != nil || name != "t-pk1" || hasSession("t-pk1-2") {
		t.Errorf("복원 = %q, %v, 둘째 세션 %v", name, err, hasSession("t-pk1-2"))
	}
}
