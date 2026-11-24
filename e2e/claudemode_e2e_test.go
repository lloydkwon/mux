//go:build e2e

// `mux --claude` 가 herdr 가 쥔 대화를 입양하지 않는지 끝까지 본다. tmux 는 herdr 를 모르고, 둘을 잇는 것은
// cmd/mux 의 한 줄(tmux.SetExternalHolds(herdr.ClaudeHolds))이라 단위 시험으로는 잡히지 않는다.
//
// «tmux 밖에서 도는 idle 대화» 는 살아 있는 아무 프로세스(sleep)의 pid 로 상태 파일을 직접 써서 만든다 — 가짜
// Claude 는 tmux pane 과 tty 가 있어야 뜬다. procStart 를 비우면 스캔은 프로세스가 살아 있는지만 본다(tmux/proc.go).
// herdr 는 가짜 실행 파일(MUX_HERDR_BIN)이 agent list 를 답한다.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// outsideTmuxConversation 은 tmux 밖에서 도는 idle 대화 하나를 만들고 그 상태 파일 경로를 돌려준다.
func outsideTmuxConversation(t *testing.T, sid, name, dir string) {
	t.Helper()
	proc := exec.Command("sleep", "300")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Process.Kill(); proc.Wait() })
	now := time.Now().UnixMilli()
	state, _ := json.Marshal(map[string]any{
		"pid": proc.Process.Pid, "sessionId": sid, "cwd": dir, "startedAt": now - 60000,
		"kind": "interactive", "name": name, "nameSource": "user",
		"status": "idle", "updatedAt": now, "statusUpdatedAt": now,
	})
	file := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", proc.Process.Pid))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, state, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(file) })
}

func TestClaudeModeLeavesHerdrHeldConversationsAlone(t *testing.T) {
	fakeClaude(t) // 입양은 `claude --resume` 을 띄운다 — PATH 의 claude 래퍼가 가리킬 가짜를 빌드해 둔다
	dir := filepath.Join(home, "proj-claudemode")
	os.MkdirAll(dir, 0o755)
	outsideTmuxConversation(t, "sid-free-e2e", "free-conv", dir)
	outsideTmuxConversation(t, "sid-held-e2e", "held-conv", dir)

	herdrBin := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
[ "$1 $2" = "agent list" ] || exit 2
echo '{"id":"cli:agent:list","result":{"type":"agent_list","agents":[{"pane_id":"w1:p1","agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"sid-held-e2e"},"cwd":"` + dir + `"}]}}'
`
	if err := os.WriteFile(herdrBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	prefs := filepath.Join(home, ".config", "mux", "preferences.json")
	if err := os.WriteFile(prefs, []byte(`{"sort":"name","setup_offered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(prefs) })
	tui := tm(t, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "tui-claude", "-x", "160", "-y", "40",
		"MUX_HERDR_BIN='"+herdrBin+"' MUX_NO_BOOTSTRAP=1 exec '"+muxBin+"' --claude")
	t.Cleanup(func() {
		for _, s := range []string{"tui-claude", "free-conv", "held-conv"} {
			tmq("kill-session", "-t", "="+s)
		}
	})

	eventually(t, "쥐지 않은 대화는 입양된다", 20*time.Second, func() bool {
		_, err, _ := runTmux("has-session", "-t", "=free-conv")
		return err == nil
	})
	eventually(t, "TUI 가 뜬다 (입양이 끝났다)", 10*time.Second, func() bool {
		return strings.Contains(screen(t, tui), "claude sessions")
	})
	if _, err, _ := runTmux("has-session", "-t", "=held-conv"); err == nil {
		t.Fatal("herdr 가 쥔 대화가 tmux 로 입양됐다 — 같은 대화 id 를 두 프로세스가 잡는다")
	}

	// 목록에는 보이고, Enter 는 거절한다.
	tm(t, "send-keys", "-t", tui, "/")
	tm(t, "send-keys", "-t", tui, "-l", "held-conv")
	tm(t, "send-keys", "-t", tui, "Enter")
	tm(t, "send-keys", "-t", tui, "g")
	tm(t, "send-keys", "-t", tui, "Enter")
	eventually(t, "Enter 를 거절하고 상태 줄에 이유를 남긴다", 5*time.Second, func() bool {
		return strings.Contains(screen(t, tui), "held by herdr")
	})
	if _, err, _ := runTmux("has-session", "-t", "=held-conv"); err == nil {
		t.Error("Enter 가 herdr 가 쥔 대화를 여기서 이어 열었다")
	}
}
