package ui

import (
	"os"
	"os/exec"
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
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mux-ui-test-")
	if err != nil {
		panic(err)
	}
	os.Unsetenv("TMUX")
	os.Unsetenv("TMUX_PANE")
	os.Setenv("TMUX_TMPDIR", dir)
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
