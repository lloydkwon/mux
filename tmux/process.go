package tmux

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const commandCacheTTL = 5 * time.Second

type cachedCommand struct {
	command   string
	expiresAt time.Time
}

var (
	cmdCache   = make(map[int]cachedCommand)
	cmdCacheMu sync.Mutex
)

// resolveCommand returns the logical command name for a pane.
// Results are cached by panePID with a TTL to avoid repeated pgrep/ps calls.
func resolveCommand(panePID int, rawCmd string) string {
	if panePID <= 0 {
		return rawCmd
	}

	if IsAICommand(rawCmd) {
		return rawCmd
	}
	// mux 의 자식 claude 는 패널이 제목을 짓는 일회용 호출이다(claude_work_title.go). 세면 그 몇 초 동안 패널이 있는
	// 세션이 Claude 세션으로 보이고, mux agent ls 에 답할 수 없는 대상으로 오른다.
	if rawCmd == "mux" {
		return rawCmd
	}

	cmdCacheMu.Lock()
	if cached, ok := cmdCache[panePID]; ok && time.Now().Before(cached.expiresAt) {
		cmdCacheMu.Unlock()
		return cached.command
	}
	cmdCacheMu.Unlock()

	result := scanChildProcesses(panePID, rawCmd)

	cmdCacheMu.Lock()
	cmdCache[panePID] = cachedCommand{
		command:   result,
		expiresAt: time.Now().Add(commandCacheTTL),
	}
	cmdCacheMu.Unlock()

	return result
}

// scanChildProcesses inspects child processes of the pane shell to detect AI CLIs.
// Works on both Linux and macOS using pgrep/ps.
func scanChildProcesses(panePID int, rawCmd string) string {
	childPIDs := findChildPIDs(panePID)
	if len(childPIDs) == 0 {
		return rawCmd
	}

	for _, pidStr := range childPIDs {
		args, err := runner.Output("ps", "-o", "args=", "-p", pidStr)
		if err != nil {
			continue
		}
		for _, part := range strings.Fields(string(args)) {
			base := filepath.Base(part)
			if IsAICommand(base) {
				return base
			}
		}
	}
	return rawCmd
}

// findChildPIDs returns child PIDs of the given parent.
// Tries pgrep first, falls back to ps -eo pid,ppid for macOS compatibility.
func findChildPIDs(parentPID int) []string {
	pidStr := fmt.Sprintf("%d", parentPID)

	// Try pgrep first (works reliably on Linux)
	out, err := runner.Output("pgrep", "-P", pidStr)
	if err == nil {
		if fields := strings.Fields(string(out)); len(fields) > 0 {
			return fields
		}
	}

	// Fallback: ps -eo pid,ppid (more reliable on macOS)
	out, err = runner.Output("ps", "-eo", "pid,ppid")
	if err != nil {
		return nil
	}

	var children []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == pidStr {
			children = append(children, fields[0])
		}
	}
	return children
}
