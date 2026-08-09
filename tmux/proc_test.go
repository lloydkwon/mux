package tmux

import (
	"os"
	"testing"
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
