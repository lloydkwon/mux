package tmux

import (
	"strings"
	"sync"
	"time"

	"github.com/lloydkwon/mux/tmux/detect"
)

// Screen detection reads what an agent has drawn and decides what it is doing.
// It is the second state provider, beside the Claude state file — and unlike
// that one it covers every agent herdr ships rules for, not just Claude.
//
// The cost model is the thing to preserve here. The 500ms tick already fans out
// over sessions, and a capture per session would mean 2N process spawns a
// second. tmux takes a whole command list in one invocation, so every session's
// screen arrives in a single fork:
//
//	tmux capture-pane -p -t a ; display-message -p SEP ; capture-pane -p -t b ; ...
//
// Measured on this machine: 4ms for three sessions batched, against 12ms for
// three separate calls, and the gap widens with session count. The result is
// cached whole-map like ClaudeStatuses, not per key, because it is produced by
// one round trip.

// screenStateTTL matches claudeStatusTTL: long enough that a burst of ticks
// costs one capture, short enough that a state change shows up in the next tick
// or the one after.
const screenStateTTL = 1 * time.Second

// captureSeparator delimits panes inside one batched capture. It is emitted by
// display-message between captures, so it cannot appear from a pane's own
// output being mistaken for a delimiter — panes are separated by a line tmux
// itself wrote.
const captureSeparator = "@@@mux-capture-sep@@@"

// screenPaneFormat enumerates every pane on the server with what detection needs
// besides the screen. #{pane_title} is the pane's terminal title, which is
// where six manifests keep their strongest rules — Claude's spinner lives only
// there, never in the pane body.
const screenPaneFormat = "#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_pid}\t#{pane_title}"

// ScreenState is what screen detection concluded for one session.
type ScreenState struct {
	// Tool is the agent's manifest id, e.g. "claude" or "codex".
	Tool string
	// State is the detected state, already mapped onto mux's AIState.
	State AIState
	// RuleID names the manifest rule that decided it, for debugging.
	RuleID string
}

var (
	screenCacheMu sync.Mutex
	screenCache   map[string]ScreenState
	screenExpires time.Time
)

// resetScreenCache drops the cache. Tests call it; so does withMock.
func resetScreenCache() {
	screenCacheMu.Lock()
	defer screenCacheMu.Unlock()
	screenCache = nil
	screenExpires = time.Time{}
}

// ScreenStates returns screen-derived state for every session that is running a
// recognized agent, keyed by tmux session name.
//
// Sessions running something else are absent from the map rather than present
// with a zero value, so a caller can tell "no agent" from "agent, no state".
func ScreenStates() map[string]ScreenState {
	screenCacheMu.Lock()
	if screenCache != nil && time.Now().Before(screenExpires) {
		cached := screenCache
		screenCacheMu.Unlock()
		return cached
	}
	screenCacheMu.Unlock()

	states := scanScreenStates()

	screenCacheMu.Lock()
	screenCache = states
	screenExpires = time.Now().Add(screenStateTTL)
	screenCacheMu.Unlock()
	return states
}

// agentPane is one session's active pane, with everything detection needs
// except the screen text.
type agentPane struct {
	session string
	paneID  string
	title   string
	tool    string
}

// scanScreenStates enumerates panes, captures them in one call, and classifies.
func scanScreenStates() map[string]ScreenState {
	panes := activeAgentPanes()
	if len(panes) == 0 {
		return map[string]ScreenState{}
	}

	screens := captureBatch(panes)
	states := make(map[string]ScreenState, len(panes))
	for index, pane := range panes {
		if index >= len(screens) {
			break
		}
		detection := detect.Detect(pane.tool, detect.Input{
			Screen:   screens[index],
			OSCTitle: pane.title,
			// tmux does not expose the agent's OSC 9 progress sequence, so the
			// handful of rules keyed on it can never fire here. Every manifest
			// that uses one has other rules for the same state.
		})
		if detection.SkipStateUpdate {
			// The pane is showing a transcript or a menu rather than live prompt
			// state. herdr freezes the previous state here; mux is stateless per
			// tick, so the honest equivalent is to report no screen state and
			// let whatever else knows something answer.
			continue
		}
		states[pane.session] = ScreenState{
			Tool:   pane.tool,
			State:  screenState(detection.State),
			RuleID: detection.RuleID,
		}
	}
	return states
}

// activeAgentPanes finds one agent pane per session: the first pane, in any
// window, running something the engine has rules for.
//
// One `list-panes -a` covers the whole server. #{pane_title} is the pane's
// terminal title, which is where six manifests keep their strongest rules —
// Claude's spinner, for one, is only ever in the title.
func activeAgentPanes() []agentPane {
	out, err := runner.Output("tmux", "list-panes", "-a", "-F", screenPaneFormat)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var panes []agentPane
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) < 5 {
			continue
		}
		session := fields[0]
		// One pane per session, and it is the first that turns out to be
		// running an agent — not simply the first pane, which is why the
		// bookkeeping below happens after the Supported check.
		//
		// That detail is what makes this cover more than ActiveCommand does.
		// `list-panes -a` walks every window, so an agent sitting in a
		// background window is still found, and so is one sharing its window
		// with something else — mux's own sidebar takes pane 0 of every window
		// it manages, which would otherwise hide the agent beside it in every
		// session the user has.
		if seen[session] {
			continue
		}

		pid := atoiSafe(fields[3])
		tool := resolveCommand(pid, fields[2])
		if !detect.Supported(tool) {
			continue
		}

		seen[session] = true
		panes = append(panes, agentPane{
			session: session,
			paneID:  fields[1],
			title:   fields[4],
			tool:    tool,
		})
	}
	return panes
}

// captureBatch captures every pane in one tmux invocation, returning the
// screens in the same order as panes.
//
// A capture that fails yields an empty screen for that pane rather than
// dropping the whole batch, so one dead pane cannot blind every session.
func captureBatch(panes []agentPane) []string {
	args := make([]string, 0, len(panes)*8)
	for index, pane := range panes {
		if index > 0 {
			args = append(args, ";", "display-message", "-p", captureSeparator, ";")
		}
		// No -e: detection reads plain text, and SGR escapes would have to be
		// stripped before any rule could match.
		args = append(args, "capture-pane", "-p", "-t", pane.paneID)
	}

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return nil
	}
	return splitCaptures(string(out), len(panes))
}

// splitCaptures cuts a batched capture back into one screen per pane.
func splitCaptures(output string, want int) []string {
	parts := strings.Split(output, captureSeparator+"\n")
	screens := make([]string, want)
	for index := range screens {
		if index >= len(parts) {
			break
		}
		// capture-pane pads to the pane height with blank lines; detection
		// expects them gone, the way herdr's bottom-anchored read produces.
		screens[index] = trimTrailingBlankLines(parts[index])
	}
	return screens
}

func trimTrailingBlankLines(screen string) string {
	lines := strings.Split(strings.TrimSuffix(screen, "\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 {
		return ""
	}
	return strings.Join(lines[:end], "\n") + "\n"
}

// screenState maps the engine's states onto mux's badge states.
//
// herdr's Idle means "finished, awaiting input", which is mux's Ready; its
// Blocked means "needs an answer", which is mux's Approval. Unknown has no
// badge.
func screenState(state detect.State) AIState {
	switch state {
	case detect.StateWorking:
		return AIStateWorking
	case detect.StateBlocked:
		return AIStateApproval
	case detect.StateIdle:
		return AIStateReady
	default:
		return AIStateNone
	}
}

func atoiSafe(value string) int {
	result := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0
		}
		result = result*10 + int(ch-'0')
	}
	return result
}
