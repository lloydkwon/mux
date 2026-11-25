//go:build e2e

// 보관(p)과 보관함(P)을 격리 tmux 위의 가짜 Claude·가짜 opencode 로 끝까지 돌린다. TUI 에서 보관하면 세션이 닫히고
// parked.json 에 대화가 남는다. 보관함에서 고르면 같은 이름의 세션에서 같은 id 로 이어진다.
// 가짜 Claude 는 TestMain 이 서버보다 먼저 PATH 에 둔 `claude` 래퍼로 다시 뜬다.
package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// transcriptOf 는 대화 기록 파일의 내용이다. 없으면 빈 문자열.
func transcriptOf(sid string) string {
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sid+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	data, _ := os.ReadFile(matches[0])
	return string(data)
}

func parkedJSON() string {
	data, _ := os.ReadFile(filepath.Join(home, ".config", "mux", "parked.json"))
	return string(data)
}

// startTUI 는 mux TUI 를 세션 하나의 pane 으로 띄우고 그 pane id 를 돌려준다. 첫 실행 설정 제안이
// 화면을 가리지 않게 preferences 를 미리 둔다(scripts/tui-snapshot.sh 와 같은 방법).
func startTUI(t *testing.T, session string) string {
	t.Helper()
	prefs := filepath.Join(home, ".config", "mux", "preferences.json")
	if err := os.WriteFile(prefs, []byte(`{"sort":"name","setup_offered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(prefs) })
	pane := tm(t, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-x", "160", "-y", "40",
		"MUX_NO_BOOTSTRAP=1 exec '"+muxBin+"'")
	t.Cleanup(func() { tmq("kill-session", "-t", "="+session) })
	return pane
}

func screen(t *testing.T, pane string) string {
	t.Helper()
	return tmq("capture-pane", "-p", "-t", pane)
}

func TestParkAndRestore(t *testing.T) {
	const session = "e2e-park"
	fake := startFake(t, session, "parkme")
	sid := agentByPane(t, fake).SessionID
	// 기록이 없는 대화는 보관되지 않는다(되살릴 것이 없다). 턴 하나를 돌린다.
	tm(t, "send-keys", "-t", fake, "-l", "hello")
	tm(t, "send-keys", "-t", fake, "Enter")
	eventually(t, "턴이 끝나 기록이 생긴다", 15*time.Second, func() bool {
		return strings.Contains(transcriptOf(sid), "echo: hello")
	})

	tui := startTUI(t, "tui-park")
	eventually(t, "TUI 가 뜬다", 10*time.Second, func() bool { return strings.Contains(screen(t, tui), session) })
	// 필터로 그 세션 하나만 남긴다 — 친 필터에서는 액션 행이 빠져 커서가 그 행에 앉는다.
	tm(t, "send-keys", "-t", tui, "/")
	tm(t, "send-keys", "-t", tui, "-l", session)
	tm(t, "send-keys", "-t", tui, "Enter")
	tm(t, "send-keys", "-t", tui, "g")
	tm(t, "send-keys", "-t", tui, "p")
	eventually(t, "보관 확인 줄", 5*time.Second, func() bool {
		return strings.Contains(screen(t, tui), `Park "`+session+`"?`)
	})
	tm(t, "send-keys", "-t", tui, "y")
	eventually(t, "세션이 닫히고 대화가 보관함에 남는다", 10*time.Second, func() bool {
		_, err, _ := runTmux("has-session", "-t", "="+session)
		return err != nil && strings.Contains(parkedJSON(), sid)
	})

	tm(t, "send-keys", "-t", tui, "Escape")
	eventually(t, "Parked 행", 5*time.Second, func() bool {
		return strings.Contains(screen(t, tui), "Parked sessions")
	})
	tm(t, "send-keys", "-t", tui, "P")
	eventually(t, "보관함 팝업", 5*time.Second, func() bool {
		return strings.Contains(screen(t, tui), "Parked sessions · 1")
	})
	tm(t, "send-keys", "-t", tui, "Enter")

	// TUI 가 끝난 뒤 cmd/mux 가 같은 이름의 세션을 만들고 `claude --resume <id>` 를 띄운다. 이 pane 에는
	// 붙은 클라이언트가 없어 attach 는 실패하지만, 그 전에 복원은 끝나 있다.
	eventually(t, "같은 이름의 세션에서 같은 대화가 이어진다", 15*time.Second, func() bool {
		for _, r := range agentRows(t) {
			if r.Session == session && r.SessionID == sid {
				return true
			}
		}
		return false
	})
	if strings.Contains(parkedJSON(), sid) {
		t.Errorf("복원한 대화가 보관함에 남았다:\n%s", parkedJSON())
	}
}

// 확인 창(x)이 떠 있는 사이 그 세션이 사라지면 y 가 앞부분이 같은 이웃을 닫으면 안 된다.
func TestKillConfirmNeverReachesANeighbour(t *testing.T) {
	const session, neighbour = "e2e-kx", "e2e-kx-2"
	newSession(t, session)
	newSession(t, neighbour)
	tui := startTUI(t, "tui-kx")
	eventually(t, "TUI 가 뜬다", 10*time.Second, func() bool { return strings.Contains(screen(t, tui), neighbour) })
	tm(t, "send-keys", "-t", tui, "/")
	tm(t, "send-keys", "-t", tui, "-l", session)
	tm(t, "send-keys", "-t", tui, "Enter")
	tm(t, "send-keys", "-t", tui, "g")
	tm(t, "send-keys", "-t", tui, "x")
	eventually(t, "닫기 확인 줄", 5*time.Second, func() bool {
		return strings.Contains(screen(t, tui), `Kill "`+session+`"?`)
	})
	tm(t, "kill-session", "-t", "="+session)
	tm(t, "send-keys", "-t", tui, "y")
	time.Sleep(1500 * time.Millisecond)
	if _, err, _ := runTmux("has-session", "-t", "="+neighbour); err != nil {
		t.Errorf("이웃 세션 %s 을 닫았다", neighbour)
	}
}

var (
	fakeOpencodeOnce sync.Once
	fakeOpencodeBin  string
	fakeOpencodeErr  error
)

// fakeOpencode 는 가짜 opencode 를 한 번만 빌드한다. 이름이 opencode 여야 mux 가 /proc 에서 알아본다.
func fakeOpencode(t *testing.T) string {
	t.Helper()
	fakeOpencodeOnce.Do(func() {
		fakeOpencodeBin = filepath.Join(filepath.Dir(muxBin), "fakeopencode", "opencode")
		build := exec.Command("go", "build", "-tags", "e2e", "-o", fakeOpencodeBin, "./e2e/fakeopencode")
		build.Dir = repoRoot()
		if out, err := build.CombinedOutput(); err != nil {
			fakeOpencodeErr = errors.New(err.Error() + "\n" + string(out))
		}
	})
	if fakeOpencodeErr != nil {
		t.Fatalf("fakeopencode 빌드 실패: %v", fakeOpencodeErr)
	}
	return fakeOpencodeBin
}

var opencodeSessionLine = regexp.MustCompile(`session: (ses_\w+)`)

// TestParkAndRestoreOpencode 는 opencode 대화를 보관했다가 같은 대화로 되살린다. 진짜 opencode 처럼 가짜도
// 어느 대화를 열었는지 어디에도 적지 않는다 — mux 는 /proc 의 프로세스와 API 의 대화 목록으로 알아낸다.
func TestParkAndRestoreOpencode(t *testing.T) {
	const session = "e2e-park-oc"
	bin := fakeOpencode(t)
	dir := filepath.Join(home, "proj-"+session)
	os.MkdirAll(dir, 0o755)
	pane := tm(t, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-x", "160", "-y", "40", "-c", dir, bin)
	t.Cleanup(func() { tmq("kill-session", "-t", "="+session) })
	eventually(t, "가짜 opencode 가 뜬다", 10*time.Second, func() bool {
		return strings.Contains(screen(t, pane), "session: none")
	})
	// 첫 화면의 opencode 는 되살릴 것이 없다. 한 줄 쳐서 대화를 만든다.
	tm(t, "send-keys", "-t", pane, "-l", "hello")
	tm(t, "send-keys", "-t", pane, "Enter")
	var sid string
	eventually(t, "대화가 생긴다", 10*time.Second, func() bool {
		if m := opencodeSessionLine.FindStringSubmatch(screen(t, pane)); m != nil {
			sid = m[1]
		}
		return sid != ""
	})

	tui := startTUI(t, "tui-park-oc")
	eventually(t, "TUI 가 뜬다", 10*time.Second, func() bool { return strings.Contains(screen(t, tui), session) })
	tm(t, "send-keys", "-t", tui, "/")
	tm(t, "send-keys", "-t", tui, "-l", session)
	tm(t, "send-keys", "-t", tui, "Enter")
	tm(t, "send-keys", "-t", tui, "g")
	tm(t, "send-keys", "-t", tui, "p")
	eventually(t, "보관 확인 줄", 5*time.Second, func() bool {
		s := screen(t, tui)
		return strings.Contains(s, `Park "`+session+`"?`) && strings.Contains(s, "opencode conversation")
	})
	tm(t, "send-keys", "-t", tui, "y")
	eventually(t, "세션이 닫히고 opencode 대화가 보관함에 남는다", 10*time.Second, func() bool {
		_, err, _ := runTmux("has-session", "-t", "="+session)
		js := parkedJSON()
		return err != nil && strings.Contains(js, sid) && strings.Contains(js, `"tool": "opencode"`)
	})

	tm(t, "send-keys", "-t", tui, "Escape")
	tm(t, "send-keys", "-t", tui, "P")
	eventually(t, "보관함 팝업", 5*time.Second, func() bool {
		s := screen(t, tui)
		return strings.Contains(s, "Parked sessions · ") && strings.Contains(s, "▶ "+session)
	})
	tm(t, "send-keys", "-t", tui, "Enter")

	// cmd/mux 가 같은 이름의 세션을 만들고, 보관할 때 돌던 그 실행 파일로 `opencode -s <id>` 를 띄운다.
	eventually(t, "같은 이름의 세션에서 같은 opencode 대화가 열린다", 15*time.Second, func() bool {
		panes := tmq("list-panes", "-t", "="+session, "-F", "#{pane_id}")
		return panes != "" && strings.Contains(screen(t, strings.Fields(panes)[0]), "session: "+sid)
	})
	if start := tmq("list-panes", "-t", "="+session, "-F", "#{pane_start_command}"); !strings.Contains(start, bin) || !strings.Contains(start, "-s "+sid) {
		t.Errorf("복원한 pane 의 명령 = %q, want %s -s %s", start, bin, sid)
	}
	if strings.Contains(parkedJSON(), sid) {
		t.Errorf("복원한 대화가 보관함에 남았다:\n%s", parkedJSON())
	}
}
