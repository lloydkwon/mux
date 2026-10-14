//go:build e2e

// 격리된 진짜 tmux 서버 위에서 사용자가 보는 동작을 검사한다.
//
// 단위 테스트는 tmux 를 목으로 바꾸므로 "창이 WT 보다 넓어졌다", "VS Code 클라이언트가
// 끌려갔다", "패널 폭 저장값이 오염됐다", "훅이 자기 자신을 다시 불러 끝나지 않는다" 같은
// 회귀를 하나도 잡지 못했다 — 전부 2026-09-22 하루에 실제로 났던 것들이다. 여기 각각을
// 시나리오 하나로 못 박는다.
//
// 격리는 편의가 아니라 요구사항이다(scripts/tmux-assumptions.sh 와 같은 원칙): 전용
// 소켓(-L), 임시 HOME/XDG(panel.json·.tmux.conf), 그리고 $TMUX 로 모든 tmux 호출을 그
// 서버로 보낸다. 훅과 패널이 띄우는 mux 는 tmux 가 넘겨주는 TMUX/TMUX_PANE 로 같은 서버를
// 본다(실측). 사용자의 서버는 건드리지 않는다.
//
//	make e2e            # go test -tags e2e ./e2e/
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
	"github.com/lloydkwon/mux/ui"
)

var (
	muxBin    string // 이 테스트가 빌드한 mux
	sock      string // 전용 서버 소켓 경로
	serverPID int    // 전용 서버 pid — kill-server 에 답하지 않을 때 SIGKILL 하려고
	home      string // 임시 HOME (panel.json, .tmux.conf)
)

func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Println("tmux 없음 — e2e 건너뜀")
		os.Exit(0)
	}
	if _, err := exec.LookPath("script"); err != nil {
		fmt.Println("script(1) 없음 — e2e 건너뜀")
		os.Exit(0)
	}
	os.Exit(run(m))
}

func run(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "mux-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	home = filepath.Join(tmp, "home")
	os.MkdirAll(filepath.Join(home, ".config", "mux"), 0o755)
	os.Setenv("HOME", home)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// mux 의 TUI 소켓은 os.TempDir() 아래 uid 별 하나뿐이다 — 격리하지 않으면
	// `mux switch` 의 폴백이 사용자의 진짜 TUI 에 attach 를 요청한다.
	os.MkdirAll(filepath.Join(tmp, "tmp"), 0o755)
	os.Setenv("TMPDIR", filepath.Join(tmp, "tmp"))

	// mux 를 빌드한다 — 설치본이 아니라 지금 작업 트리.
	muxBin = filepath.Join(tmp, "mux")
	build := exec.Command("go", "build", "-o", muxBin, "./cmd/mux")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("mux 빌드 실패: %v\n%s", err, out)
		return 1
	}

	// 진짜 훅 설정: 사용자가 `mux setup-panel` 로 얻는 그대로.
	os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g mouse on\n"), 0o644)
	if out, err := exec.Command(muxBin, "setup-panel").CombinedOutput(); err != nil {
		fmt.Printf("setup-panel 실패: %v\n%s", err, out)
		return 1
	}

	name := fmt.Sprintf("mux-e2e-%d", os.Getpid())
	os.Unsetenv("TMUX") // 테스트를 tmux 안에서 돌려도 사용자 서버로 새지 않게
	bootArgs := []string{"-L", name, "-f", filepath.Join(home, ".tmux.conf")}
	if os.Getenv("MUX_E2E_TMUX_VERBOSE") != "" {
		// 서버 로그(tmux-server-<pid>.log)를 임시 디렉터리에 남긴다 — 어떤 클라이언트가 어떤 명령을
		// 어떤 순서로 보냈는지는 이 로그에만 있다. 재현이 어려운 멈춤을 쫓을 때 켠다.
		bootArgs = append(bootArgs, "-vv")
		fmt.Println("e2e tmux server log dir:", tmp)
	}
	boot := exec.Command("tmux", append(bootArgs, "new-session", "-d", "-s", "_boot", "-x", "80", "-y", "24")...)
	boot.Dir = tmp
	if out, err := boot.CombinedOutput(); err != nil {
		fmt.Printf("서버 기동 실패: %v\n%s", err, out)
		return 1
	}
	out, err := exec.Command("tmux", "-L", name, "display-message", "-p", "#{socket_path}").Output()
	if err != nil {
		fmt.Printf("소켓 경로 실패: %v\n", err)
		return 1
	}
	sock = strings.TrimSpace(string(out))
	// 밖에서 들여다볼 수 있게 — 실패를 재현할 때 이 소켓에 -S 로 붙어 창 상태를 본다.
	fmt.Println("e2e tmux socket:", sock)
	// 이제부터 이 프로세스(와 여기서 띄우는 mux)의 모든 `tmux …` 는 이 서버로 간다.
	os.Setenv("TMUX", sock+",0,0")
	if out, err := exec.Command("tmux", "-S", sock, "display-message", "-p", "#{pid}").Output(); err == nil {
		serverPID = atoi(string(out))
	}
	defer stopServer()

	return m.Run()
}

func repoRoot() string {
	wd, _ := os.Getwd()
	return filepath.Dir(wd)
}

// tmuxTimeout 은 서버가 이 시간 안에 답하지 않으면 멈춘 것으로 보는 기준이다. 정상 명령은
// 수 ms 에 돌아온다. 서버가 CPU 를 다 쓰며 도는 경우 — 3.7c 는 패널처럼 작은 클라이언트의
// 오른쪽 가장자리에 걸친 pane 의 border-status 폭이 언더플로해 그렇게 된다(tmux #5664) —
// 그냥 기다리면 5분 뒤 go test 가 죽고 서버와 `mux watch` 는 고아로 남는다. 여기서 끊고
// 원인을 적는다.
const tmuxTimeout = 15 * time.Second

// hungCmd 는 처음 제시간에 답하지 않은 tmux 명령이다. 서버가 멈추면 그 뒤의 모든 관측이
// 비는데, 그걸 "안 됐다" 로만 적으면 원인이 가려진다 — 기다림이 실패할 때마다 덧붙인다.
var hungCmd string

// hangNote 는 서버 멈춤이 있었으면 그 사실을, 없었으면 빈 문자열을 돌려준다.
func hangNote() string {
	if hungCmd == "" {
		return ""
	}
	return fmt.Sprintf("\n서버 멈춤: `tmux %s` 가 %v 안에 답하지 않았다 (%s). 3.7c 라면 tmux #5664 — 작은 클라이언트의 오른쪽 가장자리에 걸친 pane 의 border-status 폭 언더플로",
		hungCmd, tmuxTimeout, tmuxVersion())
}

// runTmux 는 전용 서버에 명령을 보내고 stdout 을 돌려준다. hung 은 서버가 제시간에 답하지
// 않았다는 뜻이다; 그때 err 는 "signal: killed" 라 그것만으로는 구분이 안 된다.
func runTmux(args ...string) (out []byte, err error, hung bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	out, err = exec.CommandContext(ctx, "tmux", append([]string{"-S", sock}, args...)...).Output()
	hung = ctx.Err() == context.DeadlineExceeded
	if hung && hungCmd == "" {
		hungCmd = strings.Join(args, " ")
	}
	return out, err, hung
}

// tm 은 전용 서버에 tmux 명령을 보낸다. 실패는 테스트 실패다.
func tm(t *testing.T, args ...string) string {
	t.Helper()
	out, err, hung := runTmux(args...)
	if hung {
		t.Fatalf("tmux %s:%s", strings.Join(args, " "), hangNote())
	}
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("tmux %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr)
	}
	return strings.TrimSpace(string(out))
}

// tmq 는 tm 과 같되 실패를 빈 문자열로 돌려준다 — 없는 옵션을 읽을 때. 서버가 멈춘 경우도
// 빈 문자열이다; 바로 다음 tm 이 그 사실을 적는다.
func tmq(args ...string) string {
	out, _, _ := runTmux(args...)
	return strings.TrimSpace(string(out))
}

// tmuxVersion 은 소켓을 거치지 않으므로 서버가 멈춰 있어도 답한다.
func tmuxVersion() string {
	out, _ := exec.Command("tmux", "-V").Output()
	return strings.TrimSpace(string(out))
}

// stopServer 는 전용 서버를 내린다. kill-server 가 3초 안에 돌아오지 않으면 서버가 돌고 있는
// 것이므로 SIGKILL 한다 — 멈춘 서버를 남겨 두면 `mux watch` 고아와 소켓 파일이 쌓인다
// (한 번에 25개까지 쌓인 적이 있다).
func stopServer() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "tmux", "-S", sock, "kill-server").Run()
	if err == nil && ctx.Err() == nil {
		return
	}
	if serverPID > 0 && syscall.Kill(serverPID, 0) == nil {
		fmt.Printf("e2e: tmux 서버(pid %d)가 kill-server 에 답하지 않아 SIGKILL — %s\n", serverPID, tmuxVersion())
		_ = syscall.Kill(serverPID, syscall.SIGKILL)
		_ = os.Remove(sock)
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

// eventually 는 cond 가 참이 될 때까지 기다린다. 훅·패널 틱은 비동기라 관측은 늘 기다림이다.
func eventually(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && hungCmd == "" {
		if cond() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s: %v 안에 되지 않았다%s", what, timeout, hangNote())
}

// eventuallyWindow 는 세션 첫 창이 cond 를 만족할 때까지 기다리고, 못 하면 마지막으로 본
// 창 상태를 함께 적는다 — "안 됐다"만으로는 어느 축이 어긋났는지 알 수 없다.
func eventuallyWindow(t *testing.T, session, what string, timeout time.Duration, cond func(window) bool) window {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var w window
	for time.Now().Before(deadline) && hungCmd == "" {
		w = readWindow(t, session)
		if cond(w) {
			return w
		}
		time.Sleep(150 * time.Millisecond)
	}
	// 서버가 본 마지막 명령들 — 패널이 안 열렸는지, 열렸다 닫혔는지는 여기서만 보인다.
	var trail []string
	for _, l := range strings.Split(tmq("show-messages"), "\n") {
		if strings.Contains(l, "split-window") || strings.Contains(l, "kill-pane") ||
			strings.Contains(l, "resize-") || strings.Contains(l, "error") || strings.Contains(l, "message:") {
			trail = append(trail, l)
		}
	}
	if len(trail) > 14 {
		trail = trail[len(trail)-14:]
	}
	t.Fatalf("%s: %v 안에 되지 않았다 — 마지막 창: %dx%d fit=%q size=%q work=%d panel=%d@%d clients=[%s] saved=%d\n서버 로그:\n  %s%s",
		what, timeout, w.width, w.height, w.fit, w.sizeOpt, w.work, w.panel, w.panelLeft,
		strings.ReplaceAll(tmq("list-clients", "-t", "="+session, "-F", "#{client_width}x#{client_height}"), "\n", " "),
		savedPanelWidth(t), strings.Join(trail, "\n  "), hangNote())
	return w
}

// newSession 은 테스트 전용 세션을 만들고 끝나면 지운다.
func newSession(t *testing.T, name string) {
	t.Helper()
	tm(t, "new-session", "-d", "-s", name, "-x", "160", "-y", "40", "-c", os.TempDir())
	t.Cleanup(func() { tmq("kill-session", "-t", "="+name) })
}

// client 는 크기가 정해진 가짜 터미널 하나다. script(1) 이 pty 를 만들고, 그 안에서
// stty 로 크기를 못 박은 뒤 tmux attach 를 exec 한다 — 라이브러리 없이 되는 방법.
type client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	size  string // "WxH" — 이 클라이언트를 list-clients 에서 알아보는 열쇠
}

func attach(t *testing.T, session string, cols, rows int, vscode bool) *client {
	t.Helper()
	size := fmt.Sprintf("%dx%d", cols, rows)
	// script(1) runs the command through $SHELL -c. Under zsh a bare `=name`
	// is the equals expansion and "name not found" goes to the pty, never to
	// stderr — measured as a client that silently never appeared. Pin the
	// shell to sh and quote the target anyway.
	inner := fmt.Sprintf(`stty cols %d rows %d; exec tmux -S %s attach-session -t "=%s"`, cols, rows, sock, session)
	cmd := exec.Command("script", "-q", "-e", "-c", inner, "/dev/null")
	env := []string{"TERM=xterm-256color", "SHELL=/bin/sh"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TERM_PROGRAM=") || strings.HasPrefix(kv, "WT_SESSION=") ||
			strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "SHELL=") {
			continue
		}
		env = append(env, kv)
	}
	if vscode {
		env = append(env, "TERM_PROGRAM=vscode")
	} else {
		env = append(env, "WT_SESSION=e2e-fake-wt")
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("script: %v", err)
	}
	c := &client{cmd: cmd, stdin: stdin, size: size}
	t.Cleanup(c.close)
	eventually(t, "클라이언트 "+size+" attach", 5*time.Second, func() bool { return c.tty() != "" })
	return c
}

// field reads one list-clients column for this client. list-clients is the
// source of truth for "which client is where"; `display-message -c <tty>` was
// measured answering for a different client than the one named.
func (c *client) field(format string) string {
	for _, line := range strings.Split(tmq("list-clients", "-F", "#{client_width}x#{client_height} "+format), "\n") {
		size, rest, ok := strings.Cut(line, " ")
		if ok && size == c.size {
			return rest
		}
	}
	return ""
}

func (c *client) tty() string     { return c.field("#{client_tty}") }
func (c *client) session() string { return c.field("#{client_session}") }

func (c *client) close() {
	c.stdin.Close()
	syscall.Kill(-c.cmd.Process.Pid, syscall.SIGTERM)
	c.cmd.Wait()
}

// window 는 세션 첫 창의 크기·고정 상태·pane 배치를 한 번에 읽는다.
type window struct {
	id            string
	width, height int
	fit, sizeOpt  string
	work, panel   int // 작업 pane 폭, 패널 pane 폭(없으면 0)
	panelLeft     int
}

func readWindow(t *testing.T, session string) window {
	t.Helper()
	var w window
	f := strings.Fields(tm(t, "display-message", "-p", "-t", session, "#{window_id} #{window_width} #{window_height}"))
	if len(f) != 3 {
		t.Fatalf("창 정보: %q", f)
	}
	w.id, w.width, w.height = f[0], atoi(f[1]), atoi(f[2])
	w.fit = tmq("show-options", "-wqv", "-t", w.id, "@mux_vsfit")
	w.sizeOpt = tmq("show-options", "-wqv", "-t", w.id, "window-size")
	for _, line := range strings.Split(tm(t, "list-panes", "-t", w.id, "-F", "#{pane_width} #{pane_left} #{pane_start_command}"), "\n") {
		p := strings.Fields(line)
		if len(p) < 2 {
			continue
		}
		if strings.Contains(line, "mux watch") {
			w.panel, w.panelLeft = atoi(p[0]), atoi(p[1])
		} else if atoi(p[1]) == 0 {
			w.work = atoi(p[0])
		}
	}
	return w
}

func savePanelWidth(t *testing.T, width int) {
	t.Helper()
	b, _ := json.Marshal(map[string]int{"width": width})
	if err := os.WriteFile(filepath.Join(home, ".config", "mux", "panel.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func savedPanelWidth(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".config", "mux", "panel.json"))
	if err != nil {
		return 0
	}
	var v struct{ Width int }
	json.Unmarshal(b, &v)
	return v.Width
}

// 훅은 클라이언트가 붙을 때 돈다. 테스트가 직접 부르는 것은 훅이 부르는 것과 같은 명령이다.
func panelAuto(t *testing.T, session string) time.Duration {
	t.Helper()
	pane := tm(t, "display-message", "-p", "-t", session, "#{pane_id}")
	start := time.Now()
	if out, err := exec.Command(muxBin, "panel", "--auto", "-t", pane).CombinedOutput(); err != nil {
		t.Fatalf("mux panel --auto: %v\n%s", err, out)
	}
	return time.Since(start)
}

// ---------------------------------------------------------------------------

// 1. VS Code 클라이언트는 패널을 보지 못한다: 작업 pane 이 VS Code 폭 이상이고 패널은 그
// 오른쪽 바깥에 있다. 뷰포트는 활성 pane 의 커서를 따르므로 이 배치면 패널까지 가지 않는다.
func TestVSCodeClientNeverSeesPanel(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-see")
	attach(t, "e2e-see", 220, 50, false)
	attach(t, "e2e-see", 120, 24, true)

	w := eventuallyWindow(t, "e2e-see", "창 고정", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.panel > 0
	})
	if w.work < 120 {
		t.Errorf("작업 pane %d칸 < VS Code 120칸 — 패널이 VS Code 뷰포트에 들어온다", w.work)
	}
	if w.panelLeft < 121 {
		t.Errorf("패널이 %d열에서 시작 — VS Code(120칸) 화면 안이다", w.panelLeft)
	}
}

// 2. 창은 Windows Terminal 보다 커지지 않는다. 커지면 WT 도 뷰포트를 얻고, Claude 가 커서를
// 숨기는 순간 왼쪽 위로 튀어 입력창이 사라진다(실측: 패널 107 + VS Code 174 = 282 > WT 269).
func TestWindowNeverOutgrowsWT(t *testing.T) {
	savePanelWidth(t, 107) // 오염됐던 값 그대로 — 그래도 창은 WT 안에 있어야 한다
	newSession(t, "e2e-cap")
	attach(t, "e2e-cap", 220, 50, false)
	attach(t, "e2e-cap", 120, 24, true)

	// 정착까지 기다린다 — 붙은 직후엔 패널이 먼저 열리고 fit 이 뒤따른다.
	w := eventuallyWindow(t, "e2e-cap", "창이 WT 크기로 고정·정착", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.width == 220 && w.height == 49 && w.work >= 120
	})
	if w.panel != 99 {
		t.Errorf("패널 %d칸, want 99 (= 220 − 120 − 1: WT 안에 들어가는 최대)", w.panel)
	}
	// 그리고 그대로 머문다 — 패널 프로세스가 자기 폭으로 되돌리려 하면 여기서 어긋난다.
	time.Sleep(4 * time.Second)
	if w2 := readWindow(t, "e2e-cap"); w2.width != 220 || w2.work < 120 || w2.panel != 99 {
		t.Errorf("4초 뒤 창 %dx%d 작업 %d 패널 %d — 정착 상태가 흔들린다", w2.width, w2.height, w2.work, w2.panel)
	}
}

// 3. VS Code 만 붙어 있으면 창은 딱 그 크기 + 패널: 패닝조차 없다.
func TestVSCodeOnlyFitsExactly(t *testing.T) {
	savePanelWidth(t, 60) // 세션이 생기는 순간 훅이 이 값으로 패널을 연다
	newSession(t, "e2e-only")
	attach(t, "e2e-only", 120, 24, true)

	// 붙은 직후 한두 틱은 과도 상태다(패널이 먼저 열리고 fit 이 뒤따른다). 정착을 기다린다.
	w := eventuallyWindow(t, "e2e-only", "창 고정·정착", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.panel == 60 && w.work == 120
	})
	if w.width != 120+1+w.panel || w.height != 23 {
		t.Errorf("창 %dx%d, want %dx23 (VS Code + 구분선 + 패널 %d)", w.width, w.height, 120+1+w.panel, w.panel)
	}
	if w.work != 120 {
		t.Errorf("작업 pane %d, want 120", w.work)
	}
	if t.Failed() {
		t.Logf("panes: %s", tm(t, "list-panes", "-t", w.id, "-F", "#{pane_id} #{pane_width}@#{pane_left} #{pane_start_command}"))
		t.Logf("saved width: %d, window-size=%q", savedPanelWidth(t), w.sizeOpt)
		for i := 0; i < 4; i++ {
			time.Sleep(700 * time.Millisecond)
			w2 := readWindow(t, "e2e-only")
			t.Logf("+%dms: win %dx%d work=%d panel=%d@%d", (i+1)*700, w2.width, w2.height, w2.work, w2.panel, w2.panelLeft)
		}
	}
}

// 4. VS Code 가 떠나면 고정을 푼다 — 우리가 건 것만.
func TestFitReleasesWhenVSCodeLeaves(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-rel")
	attach(t, "e2e-rel", 220, 50, false)
	vs := attach(t, "e2e-rel", 120, 24, true)
	eventually(t, "창 고정", 8*time.Second, func() bool { return readWindow(t, "e2e-rel").fit == "1" })

	vs.close()
	eventually(t, "VS Code detach", 5*time.Second, func() bool { return vs.tty() == "" })
	panelAuto(t, "e2e-rel")
	w := readWindow(t, "e2e-rel")
	if w.fit != "" || w.sizeOpt != "" {
		t.Errorf("fit=%q window-size=%q, want 둘 다 해제", w.fit, w.sizeOpt)
	}
}

// 4b. 숨은 VS Code 터미널 탭이 하나 더 붙어 있어도, 큰 터미널이 다른 세션으로 떠난 뒤
// 창은 보고 있는 VS Code 에 맞춰 고정된 채로 남는다. 둘이면 물러나던 시절에는 창이
// VS Code 폭으로 줄어 패널이 편집기 화면에 들어왔다(2026-10-14 실측: my-mux 에 183x55 와
// 몇 시간째 유휴인 80x30 이 붙어 있었다).
func TestFitHoldsWithHiddenVSCodeTabWhenTerminalLeaves(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-hid-a")
	newSession(t, "e2e-hid-b")
	big := attach(t, "e2e-hid-a", 220, 50, false)
	attach(t, "e2e-hid-a", 80, 20, true) // 숨은 탭 — 먼저 붙어 활동 시각이 더 오래됐다
	// client_activity 는 초 단위라, 같은 초에 붙으면 크기로 가린다. 순서를 확실히 한다.
	time.Sleep(1100 * time.Millisecond)
	attach(t, "e2e-hid-a", 120, 24, true) // 보고 있는 VS Code

	eventually(t, "창 고정", 8*time.Second, func() bool { return readWindow(t, "e2e-hid-a").fit == "1" })

	if out, err := exec.Command(muxBin, "switch", "e2e-hid-b").CombinedOutput(); err != nil {
		t.Fatalf("mux switch: %v\n%s", err, out)
	}
	eventually(t, "큰 터미널 전환", 5*time.Second, func() bool { return big.session() == "e2e-hid-b" })

	w := eventuallyWindow(t, "e2e-hid-a", "VS Code 에 맞춰 재고정", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.width == 120+1+w.panel && w.work >= 120
	})
	if w.panelLeft < 121 {
		t.Errorf("패널이 %d열에서 시작 — 보고 있는 VS Code(120칸) 화면 안이다", w.panelLeft)
	}
}

// 4c. 고정된 창 안에서 패널이 틀어지면 다음 훅이 되돌린다. 창 크기가 이미 맞으면 아무것도
// 안 하던 가드 때문에, 실측(2026-10-14) 239칸 창이 작업 74 / 패널 164 로 갈라진 채
// 머물렀다 — 패널 프로세스는 고정된 창의 폭을 fit 에 맡기므로 fit 말고는 고칠 주체가 없다.
func TestFitRepinsDriftedPanel(t *testing.T) {
	savePanelWidth(t, 45)
	newSession(t, "e2e-pin")
	attach(t, "e2e-pin", 181, 55, true)

	w := eventuallyWindow(t, "e2e-pin", "VS Code 에 맞춰 고정", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.panel == 45 && w.work == 181
	})

	// 틀어짐을 만든다: 고정된 창의 크기는 그대로, 패널만 넓힌다.
	pane := tm(t, "list-panes", "-t", w.id, "-F", "#{pane_id} #{pane_start_command}")
	var panelID string
	for _, line := range strings.Split(pane, "\n") {
		if id, cmd, ok := strings.Cut(line, " "); ok && strings.Contains(cmd, "mux watch") {
			panelID = id
		}
	}
	if panelID == "" {
		t.Fatalf("패널 pane 을 못 찾았다:\n%s", pane)
	}
	tm(t, "resize-pane", "-t", panelID, "-x", "164")
	if d := readWindow(t, "e2e-pin"); d.panel != 164 {
		t.Fatalf("틀어짐 재현 실패: 패널 %d", d.panel)
	}

	panelAuto(t, "e2e-pin")
	got := readWindow(t, "e2e-pin")
	if got.panel != 45 || got.work != 181 {
		t.Errorf("훅 뒤 작업 %d / 패널 %d, want 181 / 45", got.work, got.panel)
	}
}

// 4e. 막 열린 패널은 창이 커지며 생긴 재배치를 드래그로 오해해 저장하지 않는다.
// 늦게 도착한 첫 크기 메시지(커지기 전 폭)와 그 순간 읽은 창 폭(커진 뒤)을 짝지으면,
// 다음 메시지의 재배치된 폭이 «창은 그대로, 패널만 바뀜» = 드래그로 읽혀 전역 저장 폭이
// 바뀌고 모든 패널이 따라간다. 실측: 160→239 에서 45 가 84 로 저장됐다.
func TestFreshPanelDoesNotSaveRelayoutAsDrag(t *testing.T) {
	savePanelWidth(t, 45)
	newSession(t, "e2e-fresh")
	attach(t, "e2e-fresh", 239, 57, false) // 패널 프로세스가 막 뜬 직후

	time.Sleep(4 * time.Second) // 틱 두 번 — 저장했다면 이미 썼다
	if s := savedPanelWidth(t); s != 45 {
		t.Errorf("저장 폭 %d, want 45 — 창 재배치를 드래그로 저장했다", s)
	}
	if w := readWindow(t, "e2e-fresh"); w.panel != 45 {
		t.Errorf("패널 %d칸, want 45", w.panel)
	}
}

// 4d. 고정되지 않은 채 남은 세션(예전 바이너리 시절에 VS Code 가 붙은 것)도 큰 터미널이
// 들렀다 떠나면 VS Code 에 맞춰 고정된다. 실측(2026-10-14): water-ai 의 VS Code 둘은
// 12:13 에 붙어 고정이 안 된 채였고, kitty 가 들렀다 떠나자 창이 VS Code 폭으로 줄어
// 패널이 편집기 화면에 들어왔다.
func TestTerminalVisitFitsAnUnfittedSession(t *testing.T) {
	savePanelWidth(t, 45)
	newSession(t, "e2e-vis-a")
	newSession(t, "e2e-vis-b")
	big := attach(t, "e2e-vis-b", 239, 57, false)
	// B 가 자리잡기 전에 A 에 붙으면 별개의 경합(저장 폭이 바뀐다)에 먼저 걸린다.
	// 여기서 묻는 것은 그게 아니다.
	eventuallyWindow(t, "e2e-vis-b", "B 정착", 5*time.Second, func(w window) bool {
		return w.width == 239 && w.panel == 45
	})
	time.Sleep(time.Second)
	attach(t, "e2e-vis-a", 80, 30, true)
	time.Sleep(1100 * time.Millisecond)
	attach(t, "e2e-vis-a", 188, 55, true)
	w := eventuallyWindow(t, "e2e-vis-a", "VS Code 에 맞춰 고정", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.panel == 45
	})
	// 예전 바이너리가 남긴 상태를 만든다: 고정 표시와 수동 크기를 지운다.
	tm(t, "set-option", "-wu", "-t", w.id, "@mux_vsfit")
	tm(t, "set-option", "-wu", "-t", w.id, "window-size")
	eventually(t, "고정 해제 재현", 5*time.Second, func() bool {
		r := readWindow(t, "e2e-vis-a")
		return r.fit == "" && r.width == 188
	})

	for _, target := range []string{"e2e-vis-a", "e2e-vis-b"} {
		if out, err := exec.Command(muxBin, "switch", target).CombinedOutput(); err != nil {
			t.Fatalf("mux switch %s: %v\n%s", target, err, out)
		}
		tgt := target
		eventually(t, "큰 터미널 → "+tgt, 5*time.Second, func() bool { return big.session() == tgt })
	}

	got := eventuallyWindow(t, "e2e-vis-a", "들렀다 떠난 뒤 VS Code 에 맞춰 고정", 8*time.Second, func(w window) bool {
		return w.fit == "1" && w.width == 188+1+45 && w.work >= 188
	})
	if got.panelLeft < 189 {
		t.Errorf("패널이 %d열에서 시작 — VS Code(188칸) 화면 안이다", got.panelLeft)
	}
}

// 5. 훅이 부르는 명령은 즉시 끝난다. resize-window 가 after-resize-window 훅을 다시 쏘고,
// 그 run-shell 은 동기라 가드가 없으면 mux 가 자기 자신을 끝없이 부른다(실측: 120초+).
func TestPanelAutoReturnsQuickly(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-fast")
	attach(t, "e2e-fast", 220, 50, false)
	attach(t, "e2e-fast", 120, 24, true)
	eventually(t, "창 고정", 8*time.Second, func() bool { return readWindow(t, "e2e-fast").fit == "1" })

	for i := 0; i < 3; i++ {
		if d := panelAuto(t, "e2e-fast"); d > 3*time.Second {
			t.Fatalf("mux panel --auto 가 %v 걸렸다 — 훅 재귀", d)
		}
	}
}

// 6. 패널 폭 저장값은 fit 을 거쳐도 그대로다. fit 이 창을 키우면 tmux 가 pane 을 비례
// 배분하는데, 패널이 그것을 드래그로 오인해 저장하면 다음 fit 이 더 넓어진다(실측 60→107).
func TestSavedPanelWidthSurvivesFit(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-width")
	vs := attach(t, "e2e-width", 120, 24, true)
	eventually(t, "창 고정", 8*time.Second, func() bool { return readWindow(t, "e2e-width").fit == "1" })
	// WT 가 붙어 창이 커지고, VS Code 가 다른 크기로 다시 붙는다 — 오염이 나던 바로 그 흐름.
	attach(t, "e2e-width", 220, 50, false)
	eventually(t, "WT 크기로", 8*time.Second, func() bool { return readWindow(t, "e2e-width").width == 220 })
	vs.close()
	eventually(t, "VS Code detach", 5*time.Second, func() bool { return vs.tty() == "" })
	attach(t, "e2e-width", 140, 30, true)
	eventually(t, "재고정", 8*time.Second, func() bool { return readWindow(t, "e2e-width").fit == "1" })
	time.Sleep(5 * time.Second) // 패널 틱 두 번 이상

	if got := savedPanelWidth(t); got != 60 {
		t.Errorf("저장된 패널 폭 %d, want 60 — fit 의 비례 배분을 드래그로 오인했다", got)
	}
}

// 7. `mux switch` 는 Windows Terminal 만 옮긴다. WT 에서 띄운 VS Code 도 WT_SESSION 을
// 물려받으므로 그것만 보면 VS Code 창이 끌려간다.
func TestSwitchNeverMovesVSCodeClient(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-sw-a")
	newSession(t, "e2e-sw-b")
	wt := attach(t, "e2e-sw-a", 220, 50, false)
	vs := attach(t, "e2e-sw-a", 120, 24, true)

	if out, err := exec.Command(muxBin, "switch", "e2e-sw-b").CombinedOutput(); err != nil {
		t.Fatalf("mux switch: %v\n%s", err, out)
	}
	eventually(t, "WT 전환", 5*time.Second, func() bool { return wt.session() == "e2e-sw-b" })
	if got := vs.session(); got != "e2e-sw-a" {
		t.Errorf("VS Code 클라이언트가 %q 로 끌려갔다, want e2e-sw-a 그대로", got)
		t.Logf("clients: %s", tm(t, "list-clients", "-F", "#{client_pid} #{client_tty} #{client_width}x#{client_height} #{client_session}"))
		msgs := strings.Split(tmq("show-messages"), "\n")
		if len(msgs) > 25 {
			msgs = msgs[len(msgs)-25:]
		}
		t.Logf("server messages:\n%s", strings.Join(msgs, "\n"))
	}
}

// 8. 디렉터리를 알고 만든 세션은 태어날 때 @project_dir 를 갖는다 — VS Code 의 tmux-project
// 프로필이 이 태그로 세션을 찾는다. 없으면 같은 프로젝트에 쌍둥이 세션이 생긴다.
func TestSessionCreatedWithDirIsTagged(t *testing.T) {
	dir := t.TempDir()
	if err := tmux.CreateSessionWithDir("e2e-tagged", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tmq("kill-session", "-t", "=e2e-tagged") })
	if got := tmq("show-options", "-qv", "-t", "e2e-tagged", "@project_dir"); got != dir {
		t.Errorf("@project_dir = %q, want %q", got, dir)
	}
}

// 9. 사용자의 자기 단말(WT·VS Code)은 ListClients 에서 Own 으로 표시된다 — 패널의 유령
// 정리가 이들을 건드리지 않는 근거. 실제 클라이언트 프로세스의 환경을 /proc 에서 읽는
// 경로라 목으로는 못 잰다.
func TestListClientsMarksOwnTerminals(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-own")
	attach(t, "e2e-own", 220, 50, false) // Windows Terminal (WT_SESSION)
	attach(t, "e2e-own", 120, 24, true)  // VS Code (TERM_PROGRAM=vscode)

	var cs []tmux.Client
	eventually(t, "클라이언트 둘", 5*time.Second, func() bool {
		var err error
		cs, err = tmux.ListClients()
		return err == nil && countSession(cs, "e2e-own") == 2
	})
	for _, c := range cs {
		if c.Session == "e2e-own" && !c.Own {
			t.Errorf("%s %dx%d 가 Own 이 아니다 — 유령 정리 대상이 된다", c.TTY, c.Width, c.Height)
		}
	}
}

func countSession(cs []tmux.Client, session string) int {
	n := 0
	for _, c := range cs {
		if c.Session == session {
			n++
		}
	}
	return n
}

// 10. resurrect 가 되살린 옛 패널(화면 캡처를 뿌린 빈 셸)은 살아 있는 패널 옆에서도 치워진다.
// 실제 사례: WSL 재부팅 → continuum 복원 → 옛 패널이 zsh 로 부활 → 훅이 새 패널을 열어 셋.
// 판정은 start command 의 복원 경로 + 스크롤백 첫 줄의 패널 헤더 + 자식 없는 셸이다.
func TestResurrectedPanelGhostIsRemoved(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-ghost")
	attach(t, "e2e-ghost", 220, 50, false)
	eventually(t, "패널", 8*time.Second, func() bool { return readWindow(t, "e2e-ghost").panel > 0 })

	// resurrect 가 남기는 모양 그대로: 화면 파일을 cat 하고 셸을 exec 한다.
	contents := filepath.Join(t.TempDir(), "resurrect", "restore", "pane_contents", "pane-e2e-ghost:1.1")
	os.MkdirAll(filepath.Dir(contents), 0o755)
	os.WriteFile(contents, []byte(" 🔔 AI 세션\n\n ✅ old 1h\n"), 0o644)
	win := readWindow(t, "e2e-ghost").id
	tm(t, "split-window", "-d", "-h", "-t", win, fmt.Sprintf("cat '%s'; exec /bin/sh", contents))
	// 그리고 사용자가 뭔가 돌리는 중인 셸 하나 — 이건 무슨 모양이든 살려야 한다.
	busy := filepath.Join(filepath.Dir(contents), "pane-e2e-ghost:1.2")
	os.WriteFile(busy, []byte(" 🔔 AI 세션\n"), 0o644)
	tm(t, "split-window", "-d", "-h", "-t", win, fmt.Sprintf("cat '%s'; exec /bin/sh -c 'sleep 30'", busy))
	eventually(t, "가짜 유령 둘", 5*time.Second, func() bool { return paneCount(t, win) == 4 })

	panelAuto(t, "e2e-ghost")
	eventually(t, "유령 제거", 5*time.Second, func() bool { return paneCount(t, win) == 3 })
	w := readWindow(t, "e2e-ghost")
	if w.panel == 0 {
		t.Error("살아 있는 패널이 사라졌다")
	}
	for _, line := range strings.Split(tm(t, "list-panes", "-t", win, "-F", "#{pane_start_command}"), "\n") {
		if strings.Contains(line, "pane-e2e-ghost:1.1") {
			t.Error("빈 셸 유령이 남아 있다")
		}
	}
	if !strings.Contains(tm(t, "list-panes", "-t", win, "-F", "#{pane_start_command}"), "sleep 30") {
		t.Error("일하는 중인 셸을 죽였다")
	}
}

func paneCount(t *testing.T, win string) int {
	t.Helper()
	return len(strings.Split(tm(t, "list-panes", "-t", win, "-F", "#{pane_id}"), "\n"))
}

// 11. TUI 에서 세션을 고르면(ui.AttachToSession, tmux 안) Windows Terminal 이 움직이고 VS Code
// 는 그대로다 — VS Code 쪽이 더 최근에 활동했어도. 무자격 switch-client 는 가장 최근 클라이언트를
// 고르므로(실측: 부트스트랩 팝업에서 세션을 고르자 VS Code 창이 옮겨가고 WT 는 제자리) 이 경로도
// 패널 클릭과 같은 규칙을 써야 한다.
func TestTUIAttachMovesWTNotVSCode(t *testing.T) {
	savePanelWidth(t, 60)
	newSession(t, "e2e-tui-a")
	newSession(t, "e2e-tui-b")
	wt := attach(t, "e2e-tui-a", 220, 50, false)
	vs := attach(t, "e2e-tui-a", 120, 24, true)
	// VS Code 를 마지막 활동 클라이언트로 만든다 — 무자격 switch-client 라면 이쪽이 움직인다.
	time.Sleep(1100 * time.Millisecond)
	vs.stdin.Write([]byte(" "))
	time.Sleep(300 * time.Millisecond)

	// TUI 가 그 세션의 pane 안에서 도는 상황: TMUX_PANE 을 그 pane 으로.
	pane := tm(t, "display-message", "-p", "-t", "e2e-tui-a", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	if err := ui.AttachToSession("e2e-tui-b", -1, -1); err != nil {
		t.Fatalf("AttachToSession: %v", err)
	}
	eventually(t, "WT 전환", 5*time.Second, func() bool { return wt.session() == "e2e-tui-b" })
	if got := vs.session(); got != "e2e-tui-a" {
		t.Errorf("VS Code 클라이언트가 %q 로 끌려갔다, want e2e-tui-a 그대로", got)
	}
}
