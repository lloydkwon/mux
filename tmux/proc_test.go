package tmux

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadProcStartSelf(t *testing.T) {
	start, ok := readProcStart(os.Getpid())
	if !ok {
		t.Skip("no /proc on this platform")
	}
	if start == "" {
		t.Fatal("expected non-empty starttime")
	}
	for _, r := range start {
		if r < '0' || r > '9' {
			t.Fatalf("expected all-digit starttime, got %q", start)
		}
	}
}

func TestReadProcStartMissingPID(t *testing.T) {
	if _, ok := readProcStart(0x7FFFFFFF); ok {
		t.Error("expected ok=false for a pid that cannot exist")
	}
}

func TestProcessAliveSelf(t *testing.T) {
	pid := os.Getpid()

	if !processAlive(pid, "") {
		t.Error("expected own process to be alive with no procStart")
	}

	start, ok := readProcStart(pid)
	if !ok {
		t.Skip("no /proc — start-time comparison not exercised")
	}
	if !processAlive(pid, start) {
		t.Error("expected own process to be alive with matching procStart")
	}
	if processAlive(pid, "999999999") {
		t.Error("expected mismatched procStart to be treated as a reused pid")
	}
}

func TestProcessAliveRejectsDeadPIDs(t *testing.T) {
	for _, pid := range []int{0, -1, 0x7FFFFFFF} {
		if processAlive(pid, "") {
			t.Errorf("expected pid %d to be reported dead", pid)
		}
	}
}

// 끝났는데 부모가 아직 거두지 않은 프로세스(좀비)는 signal 0 을 받고 /proc 에도 시작 시각째 남아 있다. PID 1 이 고아를 거두지
// 않는 컨테이너에서는 영영 남는다.
func TestProcessAliveRejectsZombies(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	pid := cmd.Process.Pid
	start, ok := readProcStart(pid)
	if !ok {
		t.Skip("no /proc on this platform")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(stat), ") Z ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("좀비가 되지 않았다: %s", stat)
		}
	}
	if processAlive(pid, "") || processAlive(pid, start) {
		t.Error("좀비를 살아 있다고 봤다")
	}
}
