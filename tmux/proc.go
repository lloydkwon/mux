package tmux

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// isAlive is the liveness check used when scanning session state files.
// Replaceable in tests.
var isAlive = processAlive

// processAlive reports whether pid is a live process.
//
// When procStart is non-empty and /proc is available, it is compared against
// the process's recorded start time to guard against PID reuse. Systems
// without /proc (macOS) skip that check and decide on existence alone.
func processAlive(pid int, procStart string) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		// EPERM means the process exists but is owned by another user.
		return errors.Is(err, os.ErrPermission)
	}
	if procStart == "" {
		return true
	}
	actual, ok := readProcStart(pid)
	if !ok {
		return true // no /proc — the existence check stands on its own
	}
	return actual == procStart
}

// readProcStart returns field 22 (starttime) of /proc/<pid>/stat, the value
// Claude Code records as "procStart". ok is false when /proc is unavailable
// or the process is gone.
func readProcStart(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// Field 2 (comm) may itself contain spaces and parentheses, so fields are
	// counted from after the final ')' — field 3 lands at index 0.
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
