package ui

import (
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
