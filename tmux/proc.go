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

// procEnvHas reports whether pid's environment contains marker as a whole
// entry.
//
// The comparison is exact, not a substring: "TERM_PROGRAM=vscode" must not also
// match "TERM_PROGRAM=vscode-insiders".
//
// False where /proc is unavailable (macOS) or the process is gone — callers
// must read that as "unknown", never as "no".
func procEnvHas(pid int, marker string) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if string(entry) == marker {
			return true
		}
	}
	return false
}

// procEnvHasPrefix reports whether any environment entry of pid starts with
// prefix — for variables whose value is irrelevant, e.g. "WT_SESSION=" which
// Windows Terminal sets to a fresh GUID per tab.
//
// False where /proc is unavailable or the process is gone — "unknown", not "no".
func procEnvHasPrefix(pid int, prefix string) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if strings.HasPrefix(string(entry), prefix) {
			return true
		}
	}
	return false
}

// procEnvValue returns the value of the named environment variable of pid, or
// "" where /proc is unavailable, the process is gone, or the variable is unset.
func procEnvValue(pid int, name string) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return ""
	}
	prefix := name + "="
	for _, entry := range bytes.Split(data, []byte{0}) {
		if s := string(entry); strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix)
		}
	}
	return ""
}

// processCwd returns pid's current working directory, or "" where /proc is
// unavailable or the process is gone.
func processCwd(pid int) string {
	if pid <= 0 {
		return ""
	}
	cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return cwd
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

// pidUnder reports whether pid is a descendant of ancestor, walking /proc's
// parent chain.
//
// known is false where the answer cannot be had — no /proc (macOS), or the
// process is already gone. Callers must read that as "no opinion" and not as
// "no": refusing on an unknown would blank the badge on every platform without
// /proc, which is the opposite of what this check is for.
//
// It exists because a pane id is not a stable identity across tmux servers. Ids
// restart at %0 when a server does, so a status file written by a previous
// server can name a pane that now belongs to somebody else — measured, a
// three-day-old session recorded `dimont-onboarding:@0.%0` and the current
// server's %0 was `my-mux`, so the ghost took the live session's row. The pane
// still matches; what does not match is the process living in it.
func pidUnder(pid, ancestor int) (under, known bool) {
	if pid <= 0 || ancestor <= 0 {
		return false, false
	}
	if pid == ancestor {
		return true, true
	}
	// Bounded: a pane's process tree is shallow, and an unbounded walk on a
	// corrupt /proc would hang the 500ms refresh.
	for i := 0; i < 32; i++ {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if err != nil {
			return false, false // no /proc, or the process went away mid-walk
		}
		ppid := 0
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "PPid:"); ok {
				ppid, _ = strconv.Atoi(strings.TrimSpace(rest))
				break
			}
		}
		if ppid <= 1 {
			return false, true // reached init without meeting the ancestor
		}
		if ppid == ancestor {
			return true, true
		}
		pid = ppid
	}
	return false, false
}

// procHasChildren reports whether pid has any child process — for a shell,
// whether someone is running something in it.
//
// Unknown reads as true. The one caller kills panes it takes for abandoned,
// and "could not tell" must land on the side that keeps the user's work.
func procHasChildren(pid int) bool {
	p := strconv.Itoa(pid)
	data, err := os.ReadFile("/proc/" + p + "/task/" + p + "/children")
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(data)) != ""
}
