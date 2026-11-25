//go:build e2e

// 재부팅 복구는 지금 쓰는 tmux-resurrect 가 맡는다. 작업 세션이 그 방식으로 worktree 폴더에서 같은 대화로 돌아오고,
// `mux task done` 으로 지운 작업은 돌아오지 않는지 진짜 플러그인 스크립트로 왕복한다(docs/design/task.md).
// 서버를 내렸다 올리므로 다른 시험과 서버를 나눠 쓰지 않는다.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// resurrectPlugin 은 사용자의 홈에 깔린 플러그인이다. 읽기만 한다. 없으면 건너뛴다(CI).
func resurrectPlugin(t *testing.T) string {
	t.Helper()
	u, err := user.Current() // HOME 은 TestMain 이 임시 폴더로 바꿨다
	if err != nil {
		t.Skipf("사용자 홈을 모른다: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(u.HomeDir, ".tmux", "plugins", "tmux-resurrect"),
		filepath.Join(u.HomeDir, ".config", "tmux", "plugins", "tmux-resurrect"),
	} {
		if _, err := os.Stat(filepath.Join(dir, "resurrect.tmux")); err == nil {
			return dir
		}
	}
	t.Skip("tmux-resurrect 가 깔려 있지 않다 — 왕복은 이 플러그인이 있는 기계에서만 잰다")
	return ""
}

// privateServer 는 이 시험만의 tmux 서버다.
type privateServer struct {
	t          *testing.T
	sock, conf string
}

func (s privateServer) run(args ...string) string {
	s.t.Helper()
	cmd := exec.Command("tmux", append([]string{"-S", s.sock, "-f", s.conf}, args...)...)
	cmd.Env = append(os.Environ(), "TMUX=", "TMUX_PANE=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Logf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (s privateServer) boot() {
	s.t.Helper()
	// 씨앗 이름은 어떤 작업 이름의 앞부분도 아니어야 한다 — resurrect 는 has-session 을 = 없이 묻는다(resurrect.md).
	s.run("new-session", "-d", "-s", "zz-boot", "-x", "120", "-y", "40")
}

func (s privateServer) kill() { s.run("kill-server") }

func (s privateServer) sessions() string { return s.run("list-sessions", "-F", "#{session_name}") }

func (s privateServer) env() []string { return []string{"TMUX=" + s.sock + ",0,0"} }

// assertSaved 는 저장이 멎은 뒤 last 가 있는 파일을 가리키고 그 작업들이 다 들어 있는지 본다. 깨진 last 로 재부팅하면
// 세션이 하나도 돌아오지 않는다.
func (s privateServer) assertSaved(saves string, ids ...string) {
	s.t.Helper()
	last := filepath.Join(saves, "last")
	var problem string
	for deadline := time.Now().Add(8 * time.Second); ; time.Sleep(300 * time.Millisecond) {
		problem = ""
		data, err := os.ReadFile(last)
		if err != nil {
			target, _ := os.Readlink(last)
			problem = fmt.Sprintf("last 가 깨졌다(%s): %v", target, err)
		}
		for _, id := range ids {
			if n := strings.Count(string(data), "pane\tt-"+id+"\t"); err == nil && n != 1 {
				problem = fmt.Sprintf("t-%s 가 저장본에 %d줄이다", id, n)
			}
		}
		if problem == "" || time.Now().After(deadline) {
			break
		}
	}
	if problem != "" {
		s.t.Fatalf("저장본: %s", problem)
	}
}

// watchLast 는 돌려준 함수를 부를 때까지 last 가 늘 있는 파일을 가리키는지 지켜본다. 깨진 last 는 뒤의 저장이 고쳐 버리므로
// 끝난 뒤에만 보면 놓친다.
func (s privateServer) watchLast(saves string) (stop func()) {
	s.t.Helper()
	last := filepath.Join(saves, "last")
	quit, done := make(chan struct{}), make(chan struct{})
	looked, broken, first := 0, 0, ""
	go func() {
		defer close(done)
		for {
			if _, err := os.Stat(last); err != nil {
				if broken++; first == "" {
					target, _ := os.Readlink(last)
					first = fmt.Sprintf("%s → %s", time.Now().Format("05.000"), target)
				}
			}
			looked++
			select {
			case <-quit:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return func() {
		s.t.Helper()
		close(quit)
		<-done
		if broken > 0 || looked < 10 {
			s.t.Errorf("last 가 깨져 있었다: %d번 봐서 %d번 (처음 %s)", looked, broken, first)
		}
	}
}

// waitServerAge 는 서버가 뜬 지 age 가 지나기를 기다린다.
func waitServerAge(t *testing.T, s privateServer, age time.Duration) {
	t.Helper()
	start := time.Unix(int64(atoi(s.run("display-message", "-p", "#{start_time}"))), 0)
	time.Sleep(time.Until(start.Add(age)))
}

func TestTaskSurvivesARebootAndADoneTaskDoesNot(t *testing.T) {
	plugin := resurrectPlugin(t)
	repo := taskRepo(t)
	dir, err := os.MkdirTemp("/tmp", "mxr") // 소켓 경로는 108바이트 안이어야 한다
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	// 디렉터리 이름이 resurrect 로 끝나야 복원 표식이 기본값과 같은 모양이 된다(resurrect.md).
	saves := filepath.Join(dir, "resurrect")
	s := privateServer{t: t, sock: filepath.Join(dir, "s"), conf: filepath.Join(dir, "tmux.conf")}
	// 임시 HOME 의 zsh 는 첫 실행 마법사를 띄워 복원이 친 글을 삼키고, 로그인 bash 는 /etc/profile 이 PATH 를 바꿀 수
	// 있다. 재는 것은 셸 설정이 아니므로 맨 bash 로 고정한다.
	conf := fmt.Sprintf(`set -g default-shell /bin/bash
set -g default-command '/bin/bash --noprofile --norc'
set -g @resurrect-dir '%s'
set -g @resurrect-capture-pane-contents 'on'
set -g @resurrect-processes 'ssh claude "opencode->opencode -c"'
run-shell '%s/resurrect.tmux'
`, saves, plugin)
	if err := os.WriteFile(s.conf, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	// 명령줄이 «claude …» 로 보이는 것까지 재려고 래퍼 대신 이름이 claude 인 실행 파일을 앞에 둔다. 래퍼는
	// exec 하며 이름을 바꾼다.
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	if err := os.Symlink(fakeClaude(t), filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s.boot()
	t.Cleanup(s.kill)

	// 이름이 서로 앞부분인 작업 둘과, 끝낼 작업 둘.
	conversations := map[string]string{}
	var mu sync.Mutex
	for _, id := range []string{"r1", "r12", "r3", "r4"} {
		out, errOut, code := muxTaskEnv(t, repo, s.env(), "start", id, "--title", "작업 "+id+" (공백; 메타)", "--prompt", "복구 시험 "+id)
		if code != 0 {
			t.Fatalf("start %s: exit %d\n%s", id, code, errOut)
		}
		conversations[id], _, _ = strings.Cut(out, "@")
	}
	row := func(id string) string {
		out, _, _ := muxTaskEnv(t, repo, s.env(), "ls", "--json")
		for _, part := range strings.Split(out, "},") {
			if strings.Contains(part, `"id":"`+id+`"`) {
				return part
			}
		}
		return ""
	}
	idle := func(id string) bool {
		r := row(id)
		return strings.Contains(r, `"state":"idle"`) && strings.Contains(r, `"sessionId":"`+conversations[id]+`"`)
	}
	for id := range conversations {
		eventually(t, id+" 의 턴이 끝난다", 15*time.Second, func() bool { return idle(id) })
	}

	// 함께 부른 start 들이 저장본을 깨뜨리지 않는다. 저장본 이름이 초 단위라, 같은 초의 저장 둘이면 last 가 없는 파일을
	// 가리킨다(OpenRig 시험: 잠금 없이 10번 중 10번). 첫 번째는 훅에 잠금이 없어 mux 가 1초씩 띄우는 것만으로 버틴다.
	s.run("set-hook", "-g", "session-created", `run-shell -b "`+plugin+`/scripts/save.sh quiet"`)
	startAll := func(ids ...string) {
		t.Helper()
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if out, errOut, code := muxTaskEnv(t, repo, s.env(), "start", id, "--prompt", "동시 시험 "+id); code != 0 {
					t.Errorf("start %s: exit %d\n%s", id, code, errOut)
				} else {
					mu.Lock()
					conversations[id], _, _ = strings.Cut(out, "@")
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
	}
	startAll("p1", "p2", "p3")
	s.assertSaved(saves, "p1", "p2", "p3")

	// 저장한 뒤 하나를 끝낸다. done 이 저장을 한 번 더 불러 저장본에서 빠져야 한다.
	s.run("run-shell", plugin+"/scripts/save.sh quiet")
	saved, _ := os.ReadFile(filepath.Join(saves, "last"))
	for id, conv := range conversations {
		if !strings.Contains(string(saved), ":claude --resume "+conv+"\n") {
			t.Errorf("%s 의 명령줄이 저장되지 않았다:\n%s", id, saved)
		}
	}
	if strings.Contains(string(saved), "공백; 메타") {
		t.Errorf("사람이 쓴 글이 저장본에 있다 — 복원하면 셸에 그대로 쳐진다:\n%s", saved)
	}
	// 서버가 뜬 지 30초 안에는 done 이 저장을 부르지 않는다(복원 중인 목록으로 덮지 않게).
	waitServerAge(t, s, 31*time.Second)
	// 사용자의 훅(잠금 + 1초)이 걸린 서버에서 done 넷을 함께, 이어서 start 둘을 함께 부른다. done 의 저장들은 잠금이 없으면
	// 겹치고, 잠금만 있고 1초를 더 쥐지 않으면 이어서 돌아 같은 초에 선다 — 넷이면 어느 둘은 같은 초다.
	s.run("set-hook", "-g", "session-created", `run-shell -b "if [ $(( $(date +%s) - #{start_time} )) -gt 30 ]; then { flock 9; `+
		plugin+`/scripts/save.sh quiet; sleep 1; } 9>>`+saves+`/.save.lock; fi"`)
	intact := s.watchLast(saves)
	var wg sync.WaitGroup
	for _, id := range []string{"r3", "r4", "p2", "p3"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, errOut, code := muxTaskEnv(t, repo, s.env(), "done", id); code != 0 || strings.Contains(errOut, "저장") {
				t.Errorf("done %s: exit %d\n%s", id, code, errOut)
			}
		}()
	}
	wg.Wait()
	intact()
	s.assertSaved(saves, "r1", "r12", "p1")
	startAll("p4", "p5")
	s.assertSaved(saves, "r1", "r12", "p1", "p4", "p5")

	// «재부팅» — 서버를 내리고 새로 띄워 복원한다.
	s.kill()
	time.Sleep(500 * time.Millisecond)
	s.boot()
	s.run("run-shell", plugin+"/scripts/restore.sh")

	for _, id := range []string{"r3", "r4", "p2", "p3"} {
		if got := s.sessions(); strings.Contains(got+"\n", "t-"+id+"\n") {
			t.Errorf("끝낸 작업 %s 가 되살아났다:\n%s", id, got)
		}
	}
	for _, id := range []string{"r1", "r12", "p1", "p4"} {
		for deadline := time.Now().Add(20 * time.Second); !idle(id); time.Sleep(200 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s 가 같은 대화로 돌아오지 않았다: %s\n화면:\n%s", id, row(id), s.run("capture-pane", "-p", "-t", "=t-"+id+":"))
			}
		}
		if got := s.run("display-message", "-p", "-t", "=t-"+id+":", "#{pane_current_path}"); got != repo+"-t-"+id {
			t.Errorf("%s 의 폴더 = %q", id, got)
		}
		if strings.HasPrefix(id, "p") {
			continue // 제목 없이 시작한 작업
		}
		if !strings.Contains(row(id), `"title":"작업 `+id) {
			t.Errorf("%s 의 제목이 돌아오지 않았다: %s", id, row(id))
		}
		if got := fakeLog("작업 " + id + " (공백; 메타)"); got != "복구 시험 "+id+"\n" {
			t.Errorf("%s 에 들어간 글 = %q — 프롬프트는 한 번이어야 한다", id, got)
		}
	}
}
