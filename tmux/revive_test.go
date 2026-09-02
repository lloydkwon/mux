package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// reviveHarness 는 복구 판단에 필요한 세 가지 바깥 세계를 전부 가짜로 세운다:
// 상태 파일, "이 PID 가 tmux 서버인가", 그리고 신호. 신호는 보냈는지만 기록한다 —
// 이 테스트들이 지키려는 것은 대부분 "보내지 않는다" 쪽이다.
type reviveHarness struct {
	dir     string
	sock    string
	signals []int
}

func newReviveHarness(t *testing.T, st serverState, alive, isServer bool) *reviveHarness {
	t.Helper()
	h := &reviveHarness{dir: t.TempDir()}
	h.sock = st.SocketPath

	oldFile, oldAlive, oldServer, oldSignal := serverStateFile, isAlive, procIsTmuxServer, signalProcess
	path := filepath.Join(h.dir, "server.json")
	serverStateFile = func() (string, error) { return path, nil }
	isAlive = func(int, string) bool { return alive }
	procIsTmuxServer = func(int) bool { return isServer }
	signalProcess = func(pid int, _ syscall.Signal) error {
		h.signals = append(h.signals, pid)
		return nil
	}
	t.Cleanup(func() {
		serverStateFile, isAlive, procIsTmuxServer, signalProcess = oldFile, oldAlive, oldServer, oldSignal
	})

	if st.PID != 0 {
		if err := saveServerStateTo(path, st); err != nil {
			t.Fatalf("상태 저장: %v", err)
		}
	}
	return h
}

func liveState(t *testing.T) serverState {
	t.Helper()
	return serverState{PID: 4242, ProcStart: "998877", SocketPath: filepath.Join(t.TempDir(), "default")}
}

// 소켓이 있으면 절대 신호를 보내지 않는다.
//
// 이게 이 파일에서 가장 중요한 테스트다. 소켓이 있다는 것은 누군가 그 경로를 쓰고
// 있다는 뜻이고, 새 서버가 이미 자리를 차지한 경우 SIGUSR1 은 옛 서버가 경로를
// 되가져가게 만들어 지금 쓰는 서버를 대신 고아로 만든다 — 고치려는 사고를 우리
// 손으로 일으키는 셈이다.
func TestReviveStandsDownWhenTheSocketIsThere(t *testing.T) {
	st := liveState(t)
	h := newReviveHarness(t, st, true, true)
	if err := os.WriteFile(st.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if reviveServer() {
		t.Fatal("소켓이 있는데 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("신호를 보냈다: %v", h.signals)
	}
}

func TestReviveDoesNothingWithoutRememberedState(t *testing.T) {
	h := newReviveHarness(t, serverState{}, true, true)
	if reviveServer() {
		t.Fatal("기억한 서버가 없는데 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("신호를 보냈다: %v", h.signals)
	}
}

func TestReviveGivesUpOnADeadPID(t *testing.T) {
	h := newReviveHarness(t, liveState(t), false, true)
	if reviveServer() {
		t.Fatal("죽은 PID 를 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("죽은 PID 에 신호를 보냈다: %v", h.signals)
	}
}

// PID 는 살아 있지만 tmux 서버가 아니면 보내지 않는다 — PID 재사용의 마지막 방어선.
func TestReviveGivesUpOnAProcessThatIsNotTmux(t *testing.T) {
	h := newReviveHarness(t, liveState(t), true, false)
	if reviveServer() {
		t.Fatal("tmux 가 아닌 프로세스를 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("남의 프로세스에 신호를 보냈다: %v", h.signals)
	}
}

// man 이 명시한 실패 조건: 부모 디렉터리가 없으면 tmux 는 소켓을 못 만든다.
func TestReviveGivesUpWhenTheParentDirectoryIsGone(t *testing.T) {
	st := serverState{PID: 4242, ProcStart: "998877", SocketPath: "/nonexistent-mux-revive/default"}
	h := newReviveHarness(t, st, true, true)
	if reviveServer() {
		t.Fatal("부모 디렉터리가 없는데 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("신호를 보냈다: %v", h.signals)
	}
}

// procStart 를 기록하지 못한 상태는 쓰지 않는다.
//
// /proc 이 없는 곳(macOS)에서는 PID 재사용을 가려낼 수 없다. 기능이 한 플랫폼에서
// 서지 않는 것이 아무 프로세스나 깨우는 것보다 낫다.
func TestReviveRefusesStateWithNoProcStart(t *testing.T) {
	st := liveState(t)
	st.ProcStart = ""
	h := newReviveHarness(t, st, true, true)
	if reviveServer() {
		t.Fatal("procStart 없이 되살렸다고 답했다")
	}
	if len(h.signals) != 0 {
		t.Fatalf("신호를 보냈다: %v", h.signals)
	}
}

// 조건이 전부 맞으면 신호를 보내고, 소켓이 돌아오면 되살렸다고 답한다.
func TestReviveSignalsAndWaitsForTheSocket(t *testing.T) {
	st := liveState(t)
	h := newReviveHarness(t, st, true, true)
	// tmux 가 신호를 받고 소켓을 다시 만드는 흉내.
	signalProcess = func(pid int, _ syscall.Signal) error {
		h.signals = append(h.signals, pid)
		return os.WriteFile(st.SocketPath, nil, 0o600)
	}

	if !reviveServer() {
		t.Fatal("소켓이 돌아왔는데 되살렸다고 답하지 않았다")
	}
	if len(h.signals) != 1 || h.signals[0] != st.PID {
		t.Fatalf("신호 %v, 원하는 값 [%d]", h.signals, st.PID)
	}
}

// 소켓이 끝내 안 나타나면 실패로 답한다 — tmux 는 bind 에 실패해도 기존 fd 를
// 유지하므로 여기서는 잘못될 것이 없다.
func TestReviveReportsFailureWhenTheSocketNeverAppears(t *testing.T) {
	h := newReviveHarness(t, liveState(t), true, true)
	if reviveServer() {
		t.Fatal("소켓이 없는데 되살렸다고 답했다")
	}
	if len(h.signals) != 1 {
		t.Fatalf("신호를 한 번 보냈어야 한다: %v", h.signals)
	}
}

// 한 프로세스에서 두 번 시도하지 않는다. 2초 틱마다 죽은 PID 에 신호를 쏘는 것은
// 복구가 아니라 소음이다.
func TestReviveTriesOnlyOncePerProcess(t *testing.T) {
	h := newReviveHarness(t, liveState(t), true, true)
	reviveOnce = sync.Once{}
	t.Cleanup(func() { reviveOnce = sync.Once{} })

	ReviveServer()
	ReviveServer()
	ReviveServer()

	if len(h.signals) != 1 {
		t.Fatalf("신호를 %d번 보냈다, 한 번이어야 한다", len(h.signals))
	}
}

func TestRememberServerRecordsPIDAndSocket(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("4242 /tmp/tmux-1000/default\n"), nil,
			"tmux", "display-message", "-p", "#{pid} #{socket_path}")

		RememberServer()

		path, _ := serverStateFile()
		st, ok := loadServerStateFrom(path)
		if !ok {
			t.Fatal("서버 메모가 쓰이지 않았다")
		}
		if st.PID != 4242 || st.SocketPath != "/tmp/tmux-1000/default" {
			t.Fatalf("기록 %+v", st)
		}
	})
}

// 한 왕복으로 끝낸다 — PID 와 소켓 경로는 늘 같이만 쓰이므로 두 번 부를 이유가 없다.
func TestRememberServerAsksTmuxOnce(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("4242 /sock\n"), nil,
			"tmux", "display-message", "-p", "#{pid} #{socket_path}")
		RememberServer()

		asks := 0
		for _, c := range m.gets {
			if strings.Contains(c, "display-message") {
				asks++
			}
		}
		if asks != 1 {
			t.Fatalf("display-message 를 %d번 불렀다", asks)
		}
	})
}

// 답이 이상하면 아무것도 쓰지 않는다. 훅에서는 클라이언트가 없어 display-message
// 자체가 빈 답을 낼 수 있고, 그걸 기록하면 다음 사고 때 엉뚱한 데를 가리킨다.
func TestRememberServerIgnoresAMalformedAnswer(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte(" \n"), nil,
			"tmux", "display-message", "-p", "#{pid} #{socket_path}")
		RememberServer()

		path, _ := serverStateFile()
		if _, ok := loadServerStateFrom(path); ok {
			t.Fatal("망가진 답을 기록했다")
		}
	})
}
