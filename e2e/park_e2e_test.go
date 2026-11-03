//go:build e2e

// 보관(p)과 보관함(P)을 격리 tmux 위의 가짜 Claude 로 끝까지 돌린다. TUI 에서 보관하면 세션이 닫히고
// parked.json 에 대화가 남는다. 보관함에서 고르면 같은 이름의 세션에서 같은 id 로 이어진다.
// 가짜 Claude 는 TestMain 이 서버보다 먼저 PATH 에 둔 `claude` 래퍼로 다시 뜬다.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
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
