package tmux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// tmux 는 소켓 파일이 사라진 서버를 되살릴 방법을 하나 문서화해 두었다:
//
//	If the socket is accidentally removed, the SIGUSR1 signal may be sent to
//	the tmux server process to recreate it (note that this will fail if any
//	parent directories are missing).
//
// 2026-09-01 /tmp 이 통째로 비워지며 /tmp/tmux-1000/default 이 unlink 됐다.
// 서버는 멀쩡히 살아 LISTEN 중이었지만 경로를 잃은 유닉스 소켓은 다시 연결할 수
// 없어, 세션 7개와 그 안의 claude 프로세스 30개가 통째로 도달 불가가 됐다. 그
// 뒤 실행한 tmux 는 소켓이 없으니 새 빈 서버를 만들었고, 그때부터 mux 는 그 빈
// 서버를 정확히 그렸다 — 사용자에게는 "데이터가 전부 날아갔다"로 보였다.
//
// 그러니까 해법은 처음부터 있었다. 사람이 알아야만 쓸 수 있었을 뿐이다.

const (
	// reviveWait 는 SIGUSR1 뒤 소켓이 나타나기를 기다리는 총 시간.
	// tmux 는 신호 핸들러에서 곧바로 bind 하므로 넉넉하다.
	reviveWait = 500 * time.Millisecond
	// reviveInterval 은 그 사이의 폴링 간격.
	reviveInterval = 25 * time.Millisecond
	// tmuxServerComm 은 리눅스에서 tmux 서버 프로세스의 comm. 실측값이다.
	tmuxServerComm = "tmux: server"
)

// serverStateFile 은 서버에 닿는 법을 적어 두는 파일.
//
// panelStateFile 과 같은 이음매 — 테스트가 개발자의 실제 ~/.config/mux 를
// 건드리지 않게 하는 유일한 방법이다.
var serverStateFile = defaultServerStatePath

// serverState 는 소켓이 사라진 뒤에도 서버를 특정하는 데 필요한 전부다.
//
// PID 만으로는 부족하고 ProcStart 가 같이 있어야 한다. 이건 편의가 아니라
// 안전장치다 — 소켓이 없다는 것은 서버가 죽었을 수도 있다는 뜻이고, 죽은 서버의
// 번호를 물려받은 엉뚱한 프로세스에 SIGUSR1 을 쏘면 그건 우리가 고치려는 사고보다
// 나쁘다.
//
// SocketPath 는 계산하지 않고 tmux 에게 받는다(#{socket_path}). TMUX_TMPDIR 을
// 우리가 다시 해석하지 않아도 되고, 사용자가 그 변수를 바꾼 뒤에도 맞는다.
type serverState struct {
	PID        int    `json:"pid"`
	ProcStart  string `json:"procStart"`
	SocketPath string `json:"socketPath"`
}

func defaultServerStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "server.json"), nil
}

// RememberServer 는 지금 닿아 있는 tmux 서버를 적어 둔다.
//
// 한 번의 왕복으로 PID 와 소켓 경로를 같이 받는다 — listFormat 이 그러듯,
// 둘은 항상 같이만 쓰이므로 두 번 부를 이유가 없다.
//
// 실패는 전부 무시한다. 이건 다음 사고를 대비한 메모이지 지금 해야 할 일이 아니고,
// 훅에서 불릴 때는 클라이언트가 없어 display-message 자체가 실패할 수 있다.
func RememberServer() {
	out, err := runner.Output("tmux", "display-message", "-p", "#{pid} #{socket_path}")
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return
	}
	start, _ := readProcStart(pid)
	next := serverState{PID: pid, ProcStart: start, SocketPath: fields[1]}

	path, err := serverStateFile()
	if err != nil {
		return
	}
	// 값이 그대로면 쓰지 않는다. 이건 틱마다 불릴 수 있다.
	if cur, ok := loadServerStateFrom(path); ok && cur == next {
		return
	}
	_ = saveServerStateTo(path, next)
}

// OS 에 직접 묻는 두 가지는 이음매로 뺀다 — 이 패키지가 isAlive·shellJobs 에
// 쓰는 것과 같은 방식이다. 테스트가 실제 프로세스에 신호를 보낼 수는 없다.
var (
	procIsTmuxServer = isTmuxServer
	signalProcess    = syscall.Kill
)

// reviveOnce 는 한 프로세스가 복구를 한 번만 시도하게 막는다.
//
// 2초 틱마다 죽은 PID 에 신호를 쏘는 것은 복구가 아니라 소음이다. 한 번 실패했다면
// 그 서버는 돌아오지 않는다.
var reviveOnce sync.Once

// ReviveServer 는 소켓만 사라진 tmux 서버에 SIGUSR1 을 보내 소켓을 다시 만들게
// 한다. 실제로 되살렸을 때만 true.
//
// 조건이 전부 맞을 때만 움직인다. 하나라도 어긋나면 조용히 물러난다 —
// 이 함수가 틀렸을 때의 대가는 살아 있는 서버를 고아로 만드는 것이라, 안 하는 쪽이
// 언제나 덜 나쁘다.
func ReviveServer() bool {
	revived := false
	reviveOnce.Do(func() { revived = reviveServer() })
	return revived
}

func reviveServer() bool {
	path, err := serverStateFile()
	if err != nil {
		return false
	}
	st, ok := loadServerStateFrom(path)
	if !ok || st.PID <= 0 || st.SocketPath == "" {
		return false
	}

	// 1. 소켓이 있으면 절대 건드리지 않는다.
	//
	// 있다는 것은 누군가 그 경로를 쓰고 있다는 뜻이다 — 우리 서버가 멀쩡하거나,
	// 새 서버가 이미 그 자리를 차지했거나. 후자에 SIGUSR1 을 쏘면 옛 서버가 경로를
	// 되가져가면서 지금 쓰는 서버를 대신 고아로 만든다.
	if _, err := os.Stat(st.SocketPath); err == nil {
		return false
	}

	// 2. 부모 디렉터리가 있어야 한다 — man 이 명시한 실패 조건.
	if fi, err := os.Stat(filepath.Dir(st.SocketPath)); err != nil || !fi.IsDir() {
		return false
	}

	// 3. 기억한 그 프로세스가 아직 그 자리에 있어야 한다.
	//
	// ProcStart 가 비어 있으면 포기한다. /proc 이 없는 곳(macOS)에서는 PID 재사용을
	// 가려낼 수단이 없고, 그러면 남의 프로세스에 신호를 보내게 된다. 기능이 한
	// 플랫폼에서 서지 않는 것이 아무 프로세스나 깨우는 것보다 낫다.
	if st.ProcStart == "" || !isAlive(st.PID, st.ProcStart) || !procIsTmuxServer(st.PID) {
		return false
	}

	if err := signalProcess(st.PID, syscall.SIGUSR1); err != nil {
		return false
	}

	// tmux 는 새 소켓 생성에 실패해도 기존 fd 를 유지하고 accept 를 다시 건다.
	// 그래서 최악의 경우가 "아무것도 바뀌지 않음"이고, 여기서는 기다렸다 확인만 한다.
	for waited := time.Duration(0); waited < reviveWait; waited += reviveInterval {
		time.Sleep(reviveInterval)
		if _, err := os.Stat(st.SocketPath); err == nil {
			return true
		}
	}
	return false
}

// isTmuxServer 는 pid 가 정말 tmux 서버인지 본다.
//
// ProcStart 대조 위에 하나 더 얹는 이유는 신호가 되돌릴 수 없기 때문이다.
// /proc 이 없으면 false — 여기서 "모르겠다"는 "하지 말라"로 읽어야 한다.
func isTmuxServer(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == tmuxServerComm
}

func loadServerStateFrom(path string) (serverState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return serverState{}, false
	}
	var st serverState
	if err := json.Unmarshal(data, &st); err != nil {
		return serverState{}, false
	}
	return st, true
}

// saveServerStateTo 는 panelwidth.go 의 원자적 쓰기를 그대로 따른다.
func saveServerStateTo(path string, st serverState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create server state directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary server state: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set server state permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		return fmt.Errorf("write server state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close server state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace server state: %w", err)
	}
	ok = true
	return nil
}
