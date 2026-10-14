package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestMain cuts this package's tests off from the developer's own tmux server.
//
// `ui` has no mock runner — the seams (exePath, editorBin, src, resolveOwn …)
// keep most of it off the server, but a tea.Cmd a test runs is real code, and
// restorePanelWidth resizes $TMUX_PANE. Run from inside tmux, that is the pane
// the developer is typing in: measured 2026-10-14, every `go test ./...` from a
// Claude pane cut it to 74 columns (TestFirstSizeClampDoesNotWriteBack's
// clamp) and handed the rest to the panel, which read as the panel jumping to
// 164 columns whenever a commit was verified.
//
// Clearing TMUX and TMUX_PANE is not enough on its own: tmux with no -t and no
// $TMUX still finds the default server through $TMUX_TMPDIR. An empty directory
// there leaves it nothing to find, so a stray call fails instead of landing.
//
// HOME 과 XDG_CONFIG_HOME 도 같은 임시 디렉터리 아래로 돌린다. 하나는 격리다 — 개발자의
// ~/.config/mux/preferences.json·panel.json 을 읽거나 쓰는 테스트가 없게. 다른 하나는 결정성이다 —
// 화면은 경로를 ~ 로 줄이고(shortenPath) 그 기준이 HOME 이라, HOME=/ 인 CI 에서 골든이 달라졌다(실측).
//
// 동아시아 모호폭도 끈다. 켜진 채면 ◀ ─ 가 2칸으로 세어져 골든 넷이 전부 깨진다(실측) — 그 터미널의
// 실제 화면도 어긋나지만, 그건 이 테스트가 고정할 대상이 아니다. 끄는 방법이 재실행인 이유:
// charmbracelet/x/ansi 가 RUNEWIDTH_EASTASIAN 을 init 에서 «비공개» 변수로 읽어 둔다. TestMain 은 init 뒤라
// 환경변수를 지워도 늦고, 그 변수는 밖에서 되돌릴 수 없다. 남는 길은 변수 없는 환경으로 자신을 다시 띄우는 것이다.
func TestMain(m *testing.M) {
	if os.Getenv("RUNEWIDTH_EASTASIAN") != "" {
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "RUNEWIDTH_EASTASIAN=") {
				env = append(env, kv)
			}
		}
		if exe, err := os.Executable(); err == nil {
			panic(syscall.Exec(exe, os.Args, env))
		}
	}
	dir, err := os.MkdirTemp("", "mux-ui-test-")
	if err != nil {
		panic(err)
	}
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	os.Setenv("TMUX_TMPDIR", dir)
	os.Setenv("HOME", filepath.Join(dir, "home"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// 이 패키지의 테스트에서 tmux 를 부르면 서버를 못 찾아야 한다. 찾으면 TestMain 의 격리가
// 깨진 것이고, 그 상태로 돈 테스트는 개발자의 pane 을 리사이즈할 수 있다.
func TestTestsCannotReachTheDevelopersTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux 가 없다")
	}
	if os.Getenv("TMUX_PANE") != "" || os.Getenv("TMUX") != "" {
		t.Errorf("TMUX=%q TMUX_PANE=%q — 테스트가 개발자의 pane 을 가리킨다",
			os.Getenv("TMUX"), os.Getenv("TMUX_PANE"))
	}
	if out, err := exec.Command("tmux", "display-message", "-p", "#{pid}").CombinedOutput(); err == nil {
		t.Errorf("tmux 서버에 닿았다 (pid %s) — 테스트의 tmux 호출이 실제 서버를 바꿀 수 있다", out)
	}
}
