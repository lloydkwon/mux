package tmux

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// taskWorld 는 저장소 하나와 tmux 서버 하나를 상태로 들고 있는 가짜 러너다. 작업 시험은 «만들고 → 다시 부르고 →
// 닫는» 순서를 밟으므로, 답이 고정된 목으로는 멱등을 볼 수 없다. 모르는 명령은 실패한다.
type taskWorld struct {
	t    *testing.T
	home string
	main string // 원본 저장소
	// trees 는 linked worktree 다(경로 → 브랜치 ref, detached 면 빈 값).
	trees    map[string]string
	prunable map[string]bool
	sessions map[string]string // 이름 → 폴더
	refs     []string
	remotes  string
	dirty    map[string]string // worktree → status 출력
	unpushed string
	contains string // for-each-ref --contains HEAD 의 답
	options  map[string]string

	listErr  error // list-sessions 의 오류
	panesErr error // list-panes 만의 오류
	locked   string

	startTime string // 서버가 뜬 시각(초)
	killErr   error
	removed   error // worktree remove 의 오류
	onKill    func()
	onGit     func(k string)
	gitErr    error // worktree list 의 오류
	survives  bool  // 세션을 닫아도 Claude 가 끝나지 않는다
	noClaude  bool  // new-session 해도 Claude 가 뜨지 않는다
	state     string
	pane      *fakePane
	calls     []string

	seeds   []string // 만든 씨앗 «폴더|id|제목»
	seedErr error
}

// secondBoundary 는 초 경계를 기다린 자리다(calls 안에서).
const secondBoundary = "wait for the next second"

// afterBoundary 는 prefix 로 시작하는 첫 호출 바로 앞에서 초 경계를 기다렸는지 본다. 저장본 이름이 초 단위라, 기다리지
// 않으면 session-created 저장과 겹쳐 한 파일에 섞인다.
func (w *taskWorld) afterBoundary(prefix string) bool {
	for i, c := range w.calls {
		if strings.HasPrefix(c, prefix) {
			return i > 0 && w.calls[i-1] == secondBoundary
		}
	}
	return false
}

const (
	taskPane = "%5"
	taskPID  = 300
)

func (w *taskWorld) Output(name string, args ...string) ([]byte, error) {
	k := name + " " + strings.Join(args, " ")
	w.calls = append(w.calls, k)
	if name == "git" {
		return w.git(args)
	}
	switch {
	case k == "tmux -u list-sessions -F #{q:session_name} #{session_path}":
		if w.listErr != nil {
			return nil, w.listErr
		}
		var b strings.Builder
		for s, dir := range w.sessions {
			fmt.Fprintf(&b, "%s %s\n", strings.ReplaceAll(s, " ", `\ `), dir)
		}
		return []byte(b.String()), nil
	case strings.HasPrefix(k, "tmux list-panes"):
		if err := errors.Join(w.listErr, w.panesErr); err != nil {
			return nil, err
		}
		for s := range w.sessions {
			if strings.HasPrefix(s, "t-") {
				return []byte(taskPane + " 5001 " + s + "\n"), nil
			}
		}
		return []byte("%1 1 other\n"), nil
	case strings.HasPrefix(k, "tmux show-options -gqv "):
		if w.listErr != nil {
			return nil, w.listErr
		}
		return []byte(w.options[args[2]]), nil
	case k == "tmux display-message -p #{start_time}":
		return []byte(w.startTime), nil
	case strings.HasPrefix(k, "tmux display-message -p -t %9 "):
		return []byte("t-m1\n"), nil
	case strings.HasPrefix(k, "tmux display-message -p -t %8 "):
		return []byte("other\n"), nil
	}
	return w.pane.Output(name, args...)
}

func (w *taskWorld) git(args []string) ([]byte, error) {
	dir, rest := args[1], args[2:]
	k := strings.Join(rest, " ")
	if w.onGit != nil {
		w.onGit(k)
	}
	if w.gitErr != nil && k == "worktree list --porcelain -z" {
		return nil, w.gitErr
	}
	switch {
	case k == "worktree list --porcelain -z":
		if !strings.HasPrefix(dir, filepath.Dir(w.main)) {
			return nil, fmt.Errorf("%w: not a git repository", exitError())
		}
		list := [][]string{{"worktree " + w.main, "HEAD abc", "branch refs/heads/main"}}
		for _, p := range sortedKeys(w.trees) {
			e := []string{"worktree " + p, "HEAD abc"}
			if w.trees[p] != "" {
				e = append(e, "branch "+w.trees[p])
			}
			if w.locked == p {
				e = append(e, "locked claude session t-m1 (pid 1 start 2)")
			}
			if w.prunable[p] {
				e = append(e, "prunable gitdir file points to non-existent location")
			}
			list = append(list, e)
		}
		return wtList(list...), nil
	case strings.HasPrefix(k, "worktree add -b "):
		branch, path := rest[3], rest[4]
		w.addTree(path, "refs/heads/"+branch)
		w.refs = append(w.refs, "refs/heads/"+branch)
		return nil, nil
	case strings.HasPrefix(k, "-c status.showUntrackedFiles=normal worktree remove "):
		path := rest[len(rest)-1]
		if w.removed != nil {
			return nil, w.removed
		}
		delete(w.trees, path)
		return nil, os.RemoveAll(path)
	case strings.HasPrefix(k, "for-each-ref --count=1 --contains HEAD"):
		return []byte(w.contains), nil
	case strings.HasPrefix(k, "for-each-ref"):
		return []byte(strings.Join(w.refs, "\n")), nil
	case k == "remote":
		return []byte(w.remotes), nil
	case k == "rev-parse --short HEAD":
		return []byte("abc1234\n"), nil
	case k == "status --porcelain --untracked-files=normal":
		return []byte(w.dirty[dir]), nil
	case strings.HasPrefix(k, "rev-list --count HEAD --not "):
		return []byte(w.unpushed), nil
	}
	return nil, fmt.Errorf("taskWorld: unexpected git %v", args)
}

func (w *taskWorld) Run(name string, args ...string) error {
	k := name + " " + strings.Join(args, " ")
	w.calls = append(w.calls, k)
	switch args[0] {
	case "new-session":
		session, dir := args[3], args[5]
		if _, dup := w.sessions[session]; dup {
			return fmt.Errorf("exit status 1: duplicate session: %s", session)
		}
		w.sessions[session] = dir
		if !w.noClaude {
			id := strings.Fields(strings.SplitN(args[len(args)-1], "--resume ", 2)[1])[0]
			w.writeClaude(session, strings.TrimSuffix(id, ";"), dir, w.state)
		}
		return nil
	case "kill-session":
		if w.killErr != nil {
			return w.killErr
		}
		name, exact := strings.CutPrefix(args[2], "=")
		if !exact {
			return fmt.Errorf("taskWorld: kill-session 은 = 로 지목해야 한다: %v", args)
		}
		if w.onKill != nil {
			w.onKill()
		}
		delete(w.sessions, name)
		if !w.survives {
			w.stopClaude()
		}
		return nil
	case "run-shell":
		return nil
	}
	return w.pane.Run(name, args...)
}

// exitError 는 진짜 *exec.ExitError 다 — git 이 «저장소가 아니다» 라고 답한 것과 답하지 못한 것을 이 타입으로 가른다.
func exitError() error { return exec.Command("false").Run() }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (w *taskWorld) addTree(path, ref string) {
	w.t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	w.trees[path] = ref
}

func (w *taskWorld) worktree(id string) string { return w.main + "-t-" + id }

// writeClaude 는 그 세션의 Claude 상태 파일을 쓴다.
func (w *taskWorld) writeClaude(session, conversation, cwd, status string) {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{
		"pid": taskPID, "sessionId": conversation, "cwd": cwd, "startedAt": 1, "procStart": "1",
		"tmux": session + ":@1." + taskPane, "name": session, "nameSource": "user",
		"status": status, "statusUpdatedAt": 1793589532061, "waitingFor": "approve Bash",
	})
	if err := os.WriteFile(w.claudeFile(taskPID), b, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *taskWorld) claudeFile(pid int) string {
	return filepath.Join(w.home, claudeDir, sessionsDir, fmt.Sprintf("%d.json", pid))
}

func (w *taskWorld) stopClaude() { _ = os.Remove(w.claudeFile(taskPID)) }

// transcript 는 대화 기록 파일을 쓰고 경로를 돌려준다.
func (w *taskWorld) transcript(worktree, id string, lines ...string) string {
	w.t.Helper()
	dir := filepath.Join(w.home, claudeDir, "projects", encodePath(worktree))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *taskWorld) called(prefix string) int {
	n := 0
	for _, c := range w.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// mutated 는 저장소나 tmux 를 바꾼 호출들이다.
func (w *taskWorld) mutated() []string {
	var out []string
	for _, c := range w.calls {
		for _, verb := range []string{"worktree add", "worktree remove", "new-session", "kill-session", "send-keys", "run-shell"} {
			if strings.Contains(c, verb) {
				out = append(out, c)
			}
		}
	}
	return out
}

const (
	trCustomTitle = `{"type":"custom-title","customTitle":"로그인 오류 고치기","sessionId":"x"}`
	trHumanLine   = `{"type":"user","entrypoint":"cli","origin":{"kind":"human"},"message":{"role":"user","content":"go"}}`
	trSDKLine     = `{"type":"user","entrypoint":"sdk-cli","origin":{"kind":"human"},"message":{"role":"user","content":"go"}}`
)

func withTaskWorld(t *testing.T, fn func(w *taskWorld)) {
	t.Helper()
	var w *taskWorld
	// 살아 있다는 것은 상태 파일이 남아 있다는 것이다 — 닫힌 세션의 Claude 는 파일과 함께 끝난다.
	alive := func(pid int, _ string) bool {
		if w == nil {
			return false
		}
		_, err := os.Stat(w.claudeFile(pid))
		return err == nil
	}
	withClaudeSessions(t, nil, alive, func() {
		home, _ := homeDir()
		root := t.TempDir()
		w = &taskWorld{
			t: t, home: home, main: filepath.Join(root, "repo"),
			trees: map[string]string{}, prunable: map[string]bool{}, sessions: map[string]string{},
			dirty: map[string]string{}, options: map[string]string{}, unpushed: "0\n", contains: "refs/heads/main\n",
			remotes: "origin\n", refs: []string{"refs/heads/main", "refs/remotes/origin/main"},
			state: "idle", pane: &fakePane{pane: taskPane}, startTime: "1793580000\n",
		}
		w.options["@resurrect-dir"] = filepath.Join(root, "resurrect") + "\n"
		if err := os.Mkdir(w.main, 0o755); err != nil {
			t.Fatal(err)
		}
		SetRunner(w)

		now := time.UnixMilli(1793589600000)
		oldNow, oldSleep, oldLock := agentNow, agentSleep, agentLockDir
		oldSecond, oldSeed, oldID, oldRepos, oldExit := waitForNextSecond, seedTaskConversation, newConversationID, taskReposFile, taskExitTimeout
		agentNow = func() time.Time { return now }
		agentSleep = func(d time.Duration) { now = now.Add(d) }
		lockDir, confDir := t.TempDir(), t.TempDir()
		oldServer := serverStateFile
		serverStateFile = func() (string, error) { return filepath.Join(confDir, "server.json"), nil }
		agentLockDir = func() string { return lockDir }
		taskReposFile = func() (string, error) { return filepath.Join(confDir, "task-repos.txt"), nil }
		waitForNextSecond = func() { w.calls = append(w.calls, secondBoundary) }
		taskExitTimeout = 0
		n := 0
		newConversationID = func() (string, error) { n++; return fmt.Sprintf("conv-%d", n), nil }
		seedTaskConversation = func(dir, id, title string) error {
			w.seeds = append(w.seeds, dir+"|"+id+"|"+title)
			if w.seedErr != nil {
				return w.seedErr
			}
			w.transcript(dir, id, trCustomTitle)
			return nil
		}
		defer func() {
			serverStateFile = oldServer
			agentNow, agentSleep, agentLockDir = oldNow, oldSleep, oldLock
			waitForNextSecond, seedTaskConversation, newConversationID, taskReposFile, taskExitTimeout = oldSecond, oldSeed, oldID, oldRepos, oldExit
		}()
		fn(w)
	})
}

// watch 는 설정 파일에 저장소를 적는다.
func (w *taskWorld) watch(lines ...string) {
	w.t.Helper()
	path, _ := taskReposFile()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func TestTaskIDReadsOnlyOurNames(t *testing.T) {
	cases := map[string]string{
		"t-m123": "m123", "t-wms-45": "wms-45", "t-1": "1",
		"t-": "", "t--x": "", "t-M1": "", "t-a b": "", "t-a.b": "", "tm1": "", "mux": "", "my-t-m1": "", "t-한글": "",
		"t-" + strings.Repeat("a", 40): strings.Repeat("a", 40),
		"t-" + strings.Repeat("a", 41): "",
	}
	for session, want := range cases {
		got, ok := TaskID(session)
		if got != want || ok != (want != "") {
			t.Errorf("TaskID(%q) = %q, %v, want %q", session, got, ok, want)
		}
	}
}

// 작업은 폴더 자리로 알아본다 — 브랜치 이름이 아니라.
func TestTaskWorktreeIDGoesByTheFolder(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/p/repo-t-m1", "m1"},
		{"/p/repo-t-m1-2", "m1-2"},
		{"/p/repo-feat-x", ""},
		{"/p/other-t-m1", ""},
		{"/p/repo/.claude/worktrees/t-m1", ""},
		{"/q/repo-t-m1", ""},
	}
	for _, tc := range cases {
		if got, _ := taskWorktreeID("/p/repo", tc.path); got != tc.want {
			t.Errorf("taskWorktreeID(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTasksShowsEveryState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *taskWorld)
		want  TaskState
	}{
		{"worktree 만", func(w *taskWorld) { w.addTree(w.worktree("m1"), "refs/heads/t-m1") }, TaskNoSession},
		{"세션만", func(w *taskWorld) { w.sessions["t-m1"] = "/nowhere" }, TaskNoWorktree},
		{"Claude 가 꺼짐", func(w *taskWorld) {
			w.addTree(w.worktree("m1"), "refs/heads/t-m1")
			w.sessions["t-m1"] = w.worktree("m1")
		}, TaskNoClaude},
		{"일하는 중", func(w *taskWorld) {
			w.addTree(w.worktree("m1"), "refs/heads/t-m1")
			w.sessions["t-m1"] = w.worktree("m1")
			w.writeClaude("t-m1", "c", w.worktree("m1"), "busy")
		}, "working"},
		{"승인 대기", func(w *taskWorld) {
			w.addTree(w.worktree("m1"), "refs/heads/t-m1")
			w.sessions["t-m1"] = w.worktree("m1")
			w.writeClaude("t-m1", "c", w.worktree("m1"), "waiting")
		}, "blocked"},
		{"브랜치 이름을 바꿔도 같은 작업", func(w *taskWorld) { w.addTree(w.worktree("m1"), "refs/heads/feat/login") }, TaskNoSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				w.watch(w.main)
				tc.setup(w)
				tasks, warnings, err := Tasks()
				if err != nil || len(warnings) != 0 {
					t.Fatalf("Tasks: %v %v", err, warnings)
				}
				if len(tasks) != 1 || tasks[0].ID != "m1" || tasks[0].State != tc.want {
					t.Errorf("tasks = %+v, want m1 %s", tasks, tc.want)
				}
			})
		})
	}
}

// 한 세션에 Claude 가 여럿이면 급한 쪽이 작업의 상태다. 다른 세션의 것은 보지 않는다.
func TestMostUrgentAgent(t *testing.T) {
	agents := []Agent{
		{Session: "t-m1", PaneID: "%1", State: AgentIdle},
		{Session: "t-m1", PaneID: "%2", State: AgentWorking},
		{Session: "t-m1", PaneID: "%3", State: AgentShell},
		{Session: "t-m12", PaneID: "%4", State: AgentBlocked},
	}
	if a := mostUrgentAgent(agents, "t-m1"); a == nil || a.PaneID != "%2" {
		t.Errorf("t-m1 = %+v", a)
	}
	if a := mostUrgentAgent(agents, "t-m"); a != nil {
		t.Errorf("t-m = %+v, want none", a)
	}
}

// 세션은 worktree 나 그 아래에서 열린 것만 작업의 것이다. 심링크로 들어온 길은 풀어서 본다.
func TestSessionInWorktree(t *testing.T) {
	root := t.TempDir()
	wt, other, link := filepath.Join(root, "repo-t-m1"), filepath.Join(root, "repo"), filepath.Join(root, "link")
	for _, d := range []string{filepath.Join(wt, "sub"), other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(wt, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		wt: true, filepath.Join(wt, "sub"): true, link: true, "": true,
		other: false, root: false, wt + "2": false, "/not/there": false,
	} {
		if got := sessionInWorktree(path, wt); got != want {
			t.Errorf("sessionInWorktree(%q) = %v, want %v", path, got, want)
		}
	}
}

// 손으로 지운 폴더(prunable)와 다른 이름의 worktree 는 작업이 아니다.
func TestTasksSkipsWhatIsNotATask(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.main+"-feat-x", "refs/heads/feat/x")
		w.trees[w.worktree("gone")] = "refs/heads/t-gone"
		w.prunable[w.worktree("gone")] = true
		w.sessions["mux"] = w.main
		tasks, _, err := Tasks()
		if err != nil || len(tasks) != 0 {
			t.Errorf("tasks = %+v, %v, want none", tasks, err)
		}
	})
}

// 설정에 없는 저장소도 살아 있는 t-* 세션의 폴더로 찾는다.
func TestTasksFindsTheRepoOfALiveSession(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		w.sessions["t-m1"] = w.worktree("m1")
		tasks, _, err := Tasks()
		if err != nil || len(tasks) != 1 || tasks[0].Repo != w.main {
			t.Errorf("tasks = %+v, %v", tasks, err)
		}
	})
}

func TestTasksReportsAnUnreadableWatchedRepoAndKeepsTheRest(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch("# 주석", "", "/not/a/repo", w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		tasks, warnings, err := Tasks()
		if err != nil || len(tasks) != 1 {
			t.Fatalf("tasks = %+v, %v", tasks, err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "/not/a/repo") {
			t.Errorf("warnings = %v", warnings)
		}
	})
}

func TestWatchedTaskReposRefusesARelativePath(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch("dev/repo")
		if _, _, err := Tasks(); err == nil || !strings.Contains(err.Error(), ":1:") {
			t.Errorf("err = %v, want the line number", err)
		}
	})
}

// 서버가 없는 것은 «세션 없음» 이고, 답하지 못한 서버는 오류다. 뒤의 것을 세션 없음으로 읽으면 done 이 살아 있는
// 세션을 두고 worktree 를 지운다.
func TestTaskSessionsTellsNoServerFromABrokenOne(t *testing.T) {
	cases := map[string]bool{
		"exit status 1: no server running on /tmp/tmux-1000/default":                            true,
		"exit status 1: error connecting to /tmp/tmux-1000/default (No such file or directory)": true,
		"exit status 1: error connecting to /tmp/tmux-1000/default (Connection refused)":        true,
		"exit status 1: protocol version mismatch (client 8, server 7)":                         false,
		"exit status 1: error connecting to /tmp/tmux-1000/default (Permission denied)":         false,
		"exec: \"tmux\": executable file not found in $PATH":                                    false,
		"exit status 1: open terminal failed: not a terminal":                                   false,
	}
	for msg, none := range cases {
		withTaskWorld(t, func(w *taskWorld) {
			w.listErr = errors.New(msg)
			_, _, err := taskSessions()
			if (err == nil) != none {
				t.Errorf("taskSessions(%q) err = %v, want no-server=%v", msg, err, none)
			}
		})
	}
}

// 기억해 둔 서버가 살아 있으면 «서버 없음» 이 아니라 닿지 못한 것이다(TMUX_TMPDIR 없이 부른 cron, 지워진 소켓). 그때
// 세션 없음으로 읽으면 done 은 살아 있는 세션을 두고 worktree 를 지우고, start 는 둘째 서버를 띄운다.
func TestTaskCommandsStopWhenTheServerIsAliveElsewhere(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.listErr = errors.New("exit status 1: error connecting to /tmp/tmux-1000/default (No such file or directory)")
		oldIs := procIsTmuxServer
		procIsTmuxServer = func(int) bool { return true }
		defer func() { procIsTmuxServer = oldIs }()
		path, _ := serverStateFile()
		if err := saveServerStateTo(path, serverState{PID: 900, ProcStart: "1", SocketPath: "/run/real/default"}); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(w.claudeFile(900), []byte("{}"), 0o600) // 900 은 살아 있다
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		if _, err := FinishTask(TaskDoneRequest{ID: "m1", Force: true}); err == nil || !strings.Contains(err.Error(), "/run/real/default") {
			t.Errorf("done: %v", err)
		}
		if _, err := StartTask(TaskStartRequest{ID: "m2", Repo: w.main, Prompt: "go"}); err == nil {
			t.Error("start 가 둘째 서버를 띄웠다")
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
		os.Remove(w.claudeFile(900)) // 그 서버가 정말 죽었으면 없는 것이다
		if _, _, err := taskSessions(); err != nil {
			t.Errorf("죽은 서버: %v", err)
		}
	})
}

// 공백이 든 사람의 세션 이름이 작업으로 읽히지 않는다.
func TestTaskSessionsReadsNamesExactly(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.sessions["t-m1"] = "/p/with space/repo-t-m1"
		w.sessions["t-m2 /x"] = "/p"
		w.sessions["t-m3 y"] = "/p"
		got, _, err := taskSessions()
		if err != nil || len(got) != 1 || got["m1"].path != "/p/with space/repo-t-m1" {
			t.Errorf("sessions = %+v, %v", got, err)
		}
	})
}

func TestTaskTitleComesFromThePersonThenTheStoredTitle(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		w.transcript(wt, "conv", trCustomTitle, trHumanLine)
		title := func() string {
			tasks, _, err := Tasks()
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks = %+v, %v", tasks, err)
			}
			return tasks[0].Title
		}
		if got := title(); got != "로그인 오류 고치기" {
			t.Errorf("세션 없음: title = %q", got)
		}
		w.transcript(wt, "conv", `{"type":"custom-title","customTitle":"t-m1"}`, trHumanLine)
		if got := title(); got != "" {
			t.Errorf("이름이 세션 이름 그대로면 제목이 아니다: %q", got)
		}
	})
}

// 패널 제목은 작업 세션에서, 사람이 준 이름일 때만이다.
func TestTaskPanelTitle(t *testing.T) {
	cases := []struct{ session, name, want string }{
		{"t-m1", "로그인", "로그인"},
		{"t-m1", "t-m1", ""},
		{"t-m1", "", ""},
		{"mux", "로그인", ""},
	}
	for _, tc := range cases {
		if got := taskPanelTitle(tc.session, tc.name); got != tc.want {
			t.Errorf("taskPanelTitle(%q, %q) = %q, want %q", tc.session, tc.name, got, tc.want)
		}
	}
}

// 사람이 준 제목이 있는 작업 세션은 그 제목을 그리고 모델을 부르지 않는다. 작업이 아닌 세션은 그대로다.
func TestWithClaudeWorkUsesTheTaskTitle(t *testing.T) {
	files := map[string]string{
		"1.json": sessionFile(1, "s-1", "t-m1:@1.%1", "busy", "로그인 오류", 1),
		"2.json": sessionFile(2, "s-2", "mux:@2.%2", "busy", "옛 이름", 1),
		"3.json": sessionFile(3, "s-3", "t-m2:@3.%3", "busy", "t-m2", 1),
	}
	withClaudeSessions(t, files, nil, func() {
		for _, id := range []string{"s-1", "s-2", "s-3"} {
			home, _ := homeDir()
			dir := filepath.Join(home, claudeDir, "projects", encodePath("/home/u/proj"))
			os.MkdirAll(dir, 0o700)
			os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(trHuman(trBase, workPrompt)+"\n"), 0o600)
		}
		asked := 0
		oldAsk := askWorkTitle
		askWorkTitle = func(string) (string, error) { asked++; return "", errors.New("부르지 마라") }
		defer func() { askWorkTitle = oldAsk }()

		got := WithClaudeWork([]Session{{Name: "t-m1"}, {Name: "mux"}, {Name: "t-m2"}})
		if got[0].AIWork != "로그인 오류" || got[0].AIWorkUntitled {
			t.Errorf("작업: %q untitled=%v", got[0].AIWork, got[0].AIWorkUntitled)
		}
		for _, s := range got[1:] {
			if s.AIWork != workPrompt || !s.AIWorkUntitled {
				t.Errorf("%s: %q untitled=%v, want the prompt", s.Name, s.AIWork, s.AIWorkUntitled)
			}
		}
		TitleNextClaudeWork(got[:1])
		if asked != 0 {
			t.Errorf("제목이 있는 작업에 모델을 %d번 불렀다", asked)
		}
	})
}

func TestPromptFromFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got, err := PromptFromFile(write("one.md", "한 줄 지시\n")); err != nil || got != "한 줄 지시" {
		t.Errorf("한 줄: %q, %v", got, err)
	}
	for name, body := range map[string]string{"multi.md": "첫 줄\n둘째 줄\n", "long.md": strings.Repeat("가", 701)} {
		p := write(name, body)
		if got, err := PromptFromFile(p); err != nil || got != p+" 를 읽고 그대로 진행해" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if _, err := PromptFromFile(write("empty.md", " \n")); err == nil {
		t.Error("빈 파일을 받았다")
	}
	if _, err := PromptFromFile(filepath.Join(dir, "none.md")); err == nil {
		t.Error("없는 파일을 받았다")
	}
}
