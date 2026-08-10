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

// shellJobCmdlines returns the command lines of Claude's own shell jobs under
// pid: the children whose cmdline names a shell snapshot, which is how Claude
// Code's Bash tool launches them. Other children — MCP servers, helpers — are
// deliberately excluded, since they say nothing about whether a turn is over.
//
// Returns nil where /proc is unavailable (macOS) or nothing matches, which the
// caller must read as "no information" rather than "no jobs".
func shellJobCmdlines(pid int) []string {
	if pid <= 0 {
		return nil
	}
	p := strconv.Itoa(pid)
	data, err := os.ReadFile("/proc/" + p + "/task/" + p + "/children")
	if err != nil {
		return nil
	}

	var cmds []string
	for _, child := range strings.Fields(string(data)) {
		raw, err := os.ReadFile("/proc/" + child + "/cmdline")
		if err != nil {
			continue // the child exited between listing and reading
		}
		// cmdline separates argv with NULs.
		cmd := strings.ReplaceAll(string(raw), "\x00", " ")
		if strings.Contains(cmd, "shell-snapshots/") {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
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
