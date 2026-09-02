package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRemoteAttachRoundTrip(t *testing.T) {
	sock := t.TempDir() + "/tui.sock"
	orig := remoteSocketPath
	remoteSocketPath = func() string { return sock }
	t.Cleanup(func() { remoteSocketPath = orig })

	got := make(chan tea.Msg, 1)
	closeRemote, err := ServeRemote(func(m tea.Msg) { got <- m })
	if err != nil {
		t.Fatalf("ServeRemote: %v", err)
	}
	defer closeRemote()

	if err := RequestRemoteAttach("dyfa-front"); err != nil {
		t.Fatalf("RequestRemoteAttach: %v", err)
	}
	select {
	case m := <-got:
		if am, ok := m.(RemoteAttachMsg); !ok || am.Name != "dyfa-front" {
			t.Fatalf("got %#v, want RemoteAttachMsg{dyfa-front}", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attach 요청이 리스너에 도달하지 않음")
	}
}

func TestRequestRemoteAttachNoListener(t *testing.T) {
	sock := t.TempDir() + "/none.sock"
	orig := remoteSocketPath
	remoteSocketPath = func() string { return sock }
	t.Cleanup(func() { remoteSocketPath = orig })

	// TUI가 없으면 에러를 돌려줘야 호출자가 원래 오류로 폴백한다
	if err := RequestRemoteAttach("nope"); err == nil {
		t.Fatal("리스너 없이 성공하면 안 됨")
	}
}

// TestRemoteSocketPathLeavesTmp는 소켓 경로가 $TMPDIR을 따라가지 않음을 못박는다.
//
// 원래 경로는 os.TempDir() 아래였고, /tmp가 비워지자 TUI는 살아 있는데 소켓만
// 사라져 mux switch가 조용히 실패했다. 여기서 TMPDIR을 옮겨 놓고도 경로가
// 꿈쩍하지 않아야 그 회귀를 잡는다.
func TestRemoteSocketPathLeavesTmp(t *testing.T) {
	cfg := t.TempDir()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("TMPDIR", tmp)

	got := defaultRemoteSocketPath()

	want := filepath.Join(cfg, "mux", "tui.sock")
	if got != want {
		t.Fatalf("경로 %q, 원하는 값 %q", got, want)
	}
	if strings.HasPrefix(got, tmp) {
		t.Fatalf("소켓이 여전히 TMPDIR 아래다: %q", got)
	}
}

// TestServeRemoteCreatesItsDirectory는 홈 아래로 옮기며 생긴 새 전제를 지킨다:
// ~/.config/mux 가 아직 없는 첫 실행에서도 리스너가 떠야 한다.
func TestServeRemoteCreatesItsDirectory(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mux", "tui.sock")
	orig := remoteSocketPath
	remoteSocketPath = func() string { return sock }
	t.Cleanup(func() { remoteSocketPath = orig })

	closeRemote, err := ServeRemote(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("ServeRemote: %v", err)
	}
	defer closeRemote()

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("소켓이 만들어지지 않음: %v", err)
	}
}
