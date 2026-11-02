//go:build e2e

// `mux agent ls/prompt/wait/read` 를 격리 tmux 위의 가짜 Claude(e2e/fakeclaude)로 시험한다.
// 가짜 Claude 는 진짜 Claude 2.1.312 에서 잰 입력창·상태 파일·대화 기록만 흉내 낸다.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	fakeClaudeOnce sync.Once
	fakeClaudeBin  string
	fakeClaudeErr  error
)

// fakeClaude 는 가짜 Claude 를 한 번만 빌드한다.
func fakeClaude(t *testing.T) string {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		fakeClaudeBin = filepath.Join(filepath.Dir(muxBin), "fakeclaude")
		build := exec.Command("go", "build", "-tags", "e2e", "-o", fakeClaudeBin, "./e2e/fakeclaude")
		build.Dir = repoRoot()
		if out, err := build.CombinedOutput(); err != nil {
			fakeClaudeErr = errors.New(err.Error() + "\n" + string(out))
		}
	})
	if fakeClaudeErr != nil {
		t.Fatalf("fakeclaude 빌드 실패: %v", fakeClaudeErr)
	}
	return fakeClaudeBin
}

// startFake 는 세션 하나를 만들어 가짜 Claude 를 그 pane 의 명령으로 띄우고 pane id 를 돌려준다.
func startFake(t *testing.T, session, name string, flags ...string) string {
	t.Helper()
	bin := fakeClaude(t)
	dir := filepath.Join(home, "proj-"+session)
	os.MkdirAll(dir, 0o755)
	cmd := append([]string{bin, "-name", name}, flags...)
	pane := tm(t, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-x", "160", "-y", "40", "-c", dir,
		strings.Join(cmd, " "))
	t.Cleanup(func() { tmq("kill-session", "-t", "="+session) })
	eventually(t, "가짜 Claude 가 목록에 오른다", 10*time.Second, func() bool {
		return agentByPane(t, pane) != nil
	})
	return pane
}

type lsRow struct {
	Pane, Session, State, Name, SessionID string
	PID                                   int
}

func agentRows(t *testing.T) []lsRow {
	t.Helper()
	out, _, code := muxAgent(t, "ls", "--json")
	if code != 0 {
		return nil
	}
	var rows []lsRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	return rows
}

func agentByPane(t *testing.T, pane string) *lsRow {
	for _, r := range agentRows(t) {
		if r.Pane == pane {
			return &r
		}
	}
	return nil
}

// muxAgent 는 `mux agent …` 를 돌려 stdout·stderr·종료 코드를 돌려준다. TMUX_PANE 은 빈다 —
// 테스트는 어느 pane 에서 부르는 것도 아니다.
func muxAgent(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(muxBin, append([]string{"agent"}, args...)...)
	cmd.Env = append(os.Environ(), "TMUX_PANE=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mux agent %v: %v", args, err)
	}
	return strings.TrimSpace(stdout.String()), stderr.String(), code
}

func fakeLog(name string) string {
	data, _ := os.ReadFile(filepath.Join(home, "fake-"+name+".log"))
	return string(data)
}

// setFakeState 는 가짜 Claude 의 상태 파일을 바꾼다 — 막힘·작업 중을 흉내 낼 때.
func setFakeState(t *testing.T, pid int, status string) {
	t.Helper()
	p := filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	json.Unmarshal(data, &st)
	st["status"] = status
	st["statusUpdatedAt"] = time.Now().UnixMilli()
	if status == "waiting" {
		st["waitingFor"] = "approve Bash"
	}
	data, _ = json.Marshal(st)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRoundTrip(t *testing.T) {
	pane := startFake(t, "e2e-ag-rt", "rt-plan")
	if r := agentByPane(t, pane); r.Session != "e2e-ag-rt" || r.State != "idle" || r.Name != "rt-plan" {
		t.Fatalf("ls = %+v", r)
	}

	tok, stderr, code := muxAgent(t, "prompt", "rt-plan", "hello round trip")
	if code != 0 {
		t.Fatalf("prompt: exit %d\n%s", code, stderr)
	}
	start := time.Now()
	if _, stderr, code := muxAgent(t, "wait", "rt-plan", "--after", tok, "--timeout", "30s"); code != 0 {
		t.Fatalf("wait: exit %d\n%s", code, stderr)
	}
	// 가짜 Claude 의 턴은 1.5초다. 보내기 전의 idle 에서 멈췄다면 0.1초도 안 걸린다 — 기준은
	// 그 둘 사이에서 여유 있게 잡는다(느린 CI 에서 1.5초가 줄어들 일은 없다).
	if waited := time.Since(start); waited < 300*time.Millisecond {
		t.Errorf("wait 가 %v 만에 끝났다 — 지난 턴의 idle 에서 멈췄다", waited)
	}
	reply, stderr, code := muxAgent(t, "read", "rt-plan", "--after", tok)
	if code != 0 || reply != "echo: hello round trip" {
		t.Fatalf("read = %q, exit %d\n%s", reply, code, stderr)
	}
	if fakeLog("rt-plan") != "hello round trip\n" {
		t.Errorf("가짜 Claude 가 받은 것 = %q", fakeLog("rt-plan"))
	}
}

func TestAgentPromptIsLiteral(t *testing.T) {
	startFake(t, "e2e-ag-lit", "lit")
	text := "-n Up Space C-u 끝;"
	if _, stderr, code := muxAgent(t, "prompt", "lit", "--", text); code != 0 {
		t.Fatalf("prompt: exit %d\n%s", code, stderr)
	}
	eventually(t, "글자 그대로 도착", 5*time.Second, func() bool { return fakeLog("lit") == text+"\n" })
}

func TestAgentPromptRefusesBlockedAndBusy(t *testing.T) {
	pane := startFake(t, "e2e-ag-ref", "ref")
	pid := agentByPane(t, pane).PID

	setFakeState(t, pid, "waiting")
	if _, _, code := muxAgent(t, "prompt", "ref", "answer the dialog?"); code != 3 {
		t.Errorf("막힘: exit %d, want 3", code)
	}
	if _, _, code := muxAgent(t, "prompt", "ref", "--force", "answer the dialog?"); code != 3 {
		t.Errorf("막힘은 --force 로도: exit %d, want 3", code)
	}

	setFakeState(t, pid, "busy")
	if _, _, code := muxAgent(t, "prompt", "ref", "too early"); code != 5 {
		t.Errorf("작업 중: exit %d, want 5", code)
	}
	if fakeLog("ref") != "" {
		t.Fatalf("거부했는데 도착했다: %q", fakeLog("ref"))
	}
	if _, stderr, code := muxAgent(t, "prompt", "ref", "--force", "queued"); code != 0 {
		t.Fatalf("작업 중 --force: exit %d\n%s", code, stderr)
	}
	eventually(t, "--force 는 대기열에 넣는다", 5*time.Second, func() bool { return fakeLog("ref") == "queued\n" })
}

func TestAgentPromptKeepsADraft(t *testing.T) {
	pane := startFake(t, "e2e-ag-draft", "draft")
	tm(t, "send-keys", "-t", pane, "-l", "--", "my draft")
	time.Sleep(300 * time.Millisecond)
	if _, _, code := muxAgent(t, "prompt", "draft", "hello"); code != 5 {
		t.Errorf("초안이 있을 때: exit %d, want 5", code)
	}
	screen := tm(t, "capture-pane", "-p", "-t", pane)
	if !strings.Contains(screen, "❯ my draft") || strings.Contains(screen, "my drafthello") {
		t.Errorf("초안이 그대로 남지 않았다:\n%s", screen)
	}
	if fakeLog("draft") != "" {
		t.Errorf("아무것도 제출되면 안 되는데 %q", fakeLog("draft"))
	}
}

func TestAgentAmbiguousNameSendsNothing(t *testing.T) {
	a := startFake(t, "e2e-ag-amb1", "twin")
	b := startFake(t, "e2e-ag-amb2", "twin")
	_, stderr, code := muxAgent(t, "prompt", "twin", "which one?")
	if code != 1 || !strings.Contains(stderr, a) || !strings.Contains(stderr, b) {
		t.Errorf("exit %d, stderr:\n%s — 두 pane 을 다 적고 1 이어야 한다", code, stderr)
	}
	if fakeLog("twin") != "" {
		t.Errorf("모호한데 보냈다: %q", fakeLog("twin"))
	}
	// pane id 로는 지목된다.
	if _, stderr, code := muxAgent(t, "prompt", a, "this one"); code != 0 {
		t.Errorf("pane id 지목: exit %d\n%s", code, stderr)
	}
}

func TestAgentReadWaitsForALaggingTranscript(t *testing.T) {
	startFake(t, "e2e-ag-lag", "lag", "-reply-delay", "500ms", "-transcript-lag", "3s")
	tok, stderr, code := muxAgent(t, "prompt", "lag", "slow writer")
	if code != 0 {
		t.Fatalf("prompt: exit %d\n%s", code, stderr)
	}
	if _, stderr, code := muxAgent(t, "wait", "lag", "--after", tok, "--timeout", "30s"); code != 0 {
		t.Fatalf("wait: exit %d\n%s", code, stderr)
	}
	reply, stderr, code := muxAgent(t, "read", "lag", "--after", tok)
	if code != 0 || reply != "echo: slow writer" {
		t.Errorf("read = %q, exit %d\n%s", reply, code, stderr)
	}
}

func TestAgentWaitTimesOut(t *testing.T) {
	startFake(t, "e2e-ag-slow", "slow", "-reply-delay", "10s")
	tok, stderr, code := muxAgent(t, "prompt", "slow", "take your time")
	if code != 0 {
		t.Fatalf("prompt: exit %d\n%s", code, stderr)
	}
	if _, _, code := muxAgent(t, "wait", "slow", "--after", tok, "--timeout", "1s"); code != 4 {
		t.Errorf("wait --timeout 1s: exit %d, want 4", code)
	}
	if _, _, code := muxAgent(t, "read", "slow", "--after", tok); code != 5 {
		t.Errorf("턴 진행 중 read: exit %d, want 5", code)
	}
}
