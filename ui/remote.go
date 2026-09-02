package ui

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// RemoteAttachMsg는 외부(mux switch)에서 TUI에게 "이 세션으로 attach하고
// 종료해라"라고 보내는 요청. 위젯 클릭이 WT tmux 클라이언트를 못 찾을 때
// 목록 화면에 머물러 있는 TUI를 세션으로 밀어 넣는 데 쓰인다.
type RemoteAttachMsg struct{ Name string }

// remoteSocketPath는 홈 아래 고정 경로를 쓴다.
//
// /tmp가 아닌 이유: 거기는 언제 비워져도 할 말이 없는 곳이다. 2026-09-01 실제로
// 비워졌고, 그때 TUI는 멀쩡히 살아 있는데 소켓 파일만 사라져 `mux switch`가
// 아무 말 없이 실패했다. 잃는 데이터는 없지만 고칠 수 없는 고장으로 보인다.
//
// 옛 주석은 "환경 변수에 의존하지 않는 고정 경로"를 이유로 os.TempDir()을 들었는데,
// 그 전제가 틀렸다 — os.TempDir()이 읽는 $TMPDIR이야말로 환경 변수다.
// os.UserConfigDir()(XDG_CONFIG_HOME, 없으면 $HOME)이 오히려 더 결정적이고,
// preferences.json·panel.json과 경로 해석이 일치한다. 그것마저 실패하는
// 컨텍스트에서만 옛 경로로 물러나므로, 리스너와 다이얼러는 어느 쪽이든 같은 답을
// 계산한다 — 두 역할이 한 바이너리 안에 있으니 갈릴 수가 없다.
//
// uid 접미사는 뗐다. 홈 안이라 다른 사용자와 부딪힐 일이 없다.
//
// 테스트에서 교체할 수 있게 var (실행 중인 실제 TUI의 소켓 보호) — tmux 패키지의
// panelStateFile = defaultPanelStatePath 와 같은 모양이다.
var remoteSocketPath = defaultRemoteSocketPath

func defaultRemoteSocketPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Sprintf("%s/mux-tui-%d.sock", os.TempDir(), os.Getuid())
	}
	return filepath.Join(dir, "mux", "tui.sock")
}

// ServeRemote는 TUI 프로세스가 원격 attach 요청을 받는 유닉스 소켓을 연다.
// 반환된 closer는 TUI 종료 시 호출해 소켓 파일을 정리한다.
// ponytail: TUI가 여럿이면 마지막에 뜬 것이 소켓을 가져간다 — 실사용은 1개
func ServeRemote(send func(tea.Msg)) (func(), error) {
	path := remoteSocketPath()
	// 홈 아래로 옮겼으니 디렉터리가 아직 없을 수 있다 — preferences 저장과 같은 0700.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(path) // 비정상 종료가 남긴 소켓 제거
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(io.LimitReader(c, 256))
			c.Close()
			if name := strings.TrimSpace(string(b)); name != "" {
				send(RemoteAttachMsg{Name: name})
			}
		}
	}()
	return func() { ln.Close(); os.Remove(path) }, nil
}

// RequestRemoteAttach는 떠 있는 TUI에 세션명을 보낸다. TUI가 없으면
// (소켓 부재·연결 거부) 에러 — 호출자는 원래 오류로 폴백한다.
func RequestRemoteAttach(name string) error {
	c, err := net.DialTimeout("unix", remoteSocketPath(), time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte(name))
	return err
}
