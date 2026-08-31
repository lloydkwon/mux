package ui

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// RemoteAttachMsg는 외부(mux switch)에서 TUI에게 "이 세션으로 attach하고
// 종료해라"라고 보내는 요청. 위젯 클릭이 WT tmux 클라이언트를 못 찾을 때
// 목록 화면에 머물러 있는 TUI를 세션으로 밀어 넣는 데 쓰인다.
type RemoteAttachMsg struct{ Name string }

// remoteSocketPath는 환경 변수에 의존하지 않는 고정 경로를 쓴다 —
// wsl.exe 직접 실행처럼 XDG_RUNTIME_DIR이 없는 컨텍스트에서도
// 리스너와 다이얼러가 같은 경로를 계산해야 하기 때문.
// 테스트에서 교체할 수 있게 var (실행 중인 실제 TUI의 소켓 보호).
var remoteSocketPath = func() string {
	return fmt.Sprintf("%s/mux-tui-%d.sock", os.TempDir(), os.Getuid())
}

// ServeRemote는 TUI 프로세스가 원격 attach 요청을 받는 유닉스 소켓을 연다.
// 반환된 closer는 TUI 종료 시 호출해 소켓 파일을 정리한다.
// ponytail: TUI가 여럿이면 마지막에 뜬 것이 소켓을 가져간다 — 실사용은 1개
func ServeRemote(send func(tea.Msg)) (func(), error) {
	path := remoteSocketPath()
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
