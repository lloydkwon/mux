package tmux

import (
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultPanelKey toggles the panel in the current window.
	DefaultPanelKey = "a"

	// DefaultFocusKey steps into the panel and back out again. Tab because the
	// gesture is "the other pane" rather than a direction, and because tmux
	// leaves it unbound in the prefix table.
	DefaultFocusKey = "Tab"

	// panelBlockBegin and panelBlockEnd fence the region of the config mux owns.
	//
	// A fence rather than the per-line tag `muxKeybindMarker` uses, because this
	// is many lines and those helpers can only ever maintain one — and because
	// sharing that tag would break both idempotency tests that count it, and let
	// the oh-my-tmux cleanup pass in SetupKeybind delete the hooks as collateral.
	panelBlockBegin = "# mux panel {"
	panelBlockEnd   = "# mux panel }"
)

// navBinds are the keys that steer the panel's cursor without moving the focus.
// The directions are NavPanel's vocabulary, so the binding says what it means
// and the panel's own key handling can change without every user's tmux.conf
// changing with it.
var navBinds = []struct{ key, direction string }{
	{"M-Up", "up"},
	{"M-Down", "down"},
	{"M-Enter", "enter"},
}

// panelHooks are the tmux events after which a window may be on screen without
// a panel. Each one calls the same ensure — `mux panel --auto` opens a missing
// panel and never closes one — so firing more often than necessary costs a
// display-message and a list-panes, and nothing else.
//
// Verified against a real server, since a hook that silently does not fire is
// indistinguishable from a broken feature:
//
//   - client-session-changed is the one that matters most. Clicking a session in
//     the panel runs switch-client, which lands you in a window that has never
//     had a panel — the panel appearing to vanish.
//   - after-select-window covers moving between windows of one session.
//   - client-attached covers attaching from outside tmux.
//   - after-new-window / after-new-session cover windows that did not exist yet.
//   - client-resized / after-resize-window bring the panel back to a window that
//     had grown too narrow to spare the columns.
//
// There is no recursion: the ensure runs split-window, which makes a pane rather
// than a window, so it cannot re-trigger after-new-window.
var panelHooks = []string{
	"after-new-window",
	"after-new-session",
	"after-select-window",
	"client-attached",
	"client-session-changed",
	"client-resized",
	"after-resize-window",
}

// SetupPanel writes the panel key binding and the hooks that keep a panel in
// every window into the user's tmux config.
//
// Companion to SetupKeybind, and the same shape: an absolute path resolved at
// runtime, an idempotent owned region, and oh-my-tmux routed to
// .tmux.conf.local before the sentinel.
func SetupPanel(key, focusKey string) error {
	muxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find mux executable: %w", err)
	}

	confPath, err := findTmuxConf()
	if err != nil {
		return err
	}

	target := confPath
	if isOhMyTmux(confPath) {
		target = findTmuxConfLocal(confPath)
	}
	if err := upsertBlock(target, panelBlockLines(muxPath, key, focusKey)); err != nil {
		return err
	}

	if target != confPath {
		fmt.Printf("Detected oh-my-tmux. Added the panel block to %s\n\n", target)
	} else {
		fmt.Printf("Added the panel block to %s\n\n", target)
	}
	fmt.Printf("Reload tmux config:\n  tmux source-file %s\n\n", target)
	fmt.Printf("Then press: prefix + %s to toggle the panel in a window,\n", key)
	fmt.Printf("            prefix + %s to step into it and back out again.\n", focusKey)
	fmt.Printf("New windows, new sessions and session switches get one on their own.\n")
	if !MouseEnabled() {
		fmt.Printf("\nNote: `mouse` is off, so the panel's border cannot be dragged and its\n")
		fmt.Printf("      rows cannot be clicked. Enable it with:\n")
		fmt.Printf("  set -g mouse on\n")
	}
	return nil
}

// MouseEnabled reports whether tmux is forwarding mouse events.
//
// Read, never written. Dragging the panel's border and clicking its rows both go
// through tmux's own mouse handling, so with `mouse off` the panel looks broken
// rather than merely reduced — worth saying once at setup. Turning it on is
// still the user's call: it also takes over wheel scrolling and text selection
// in every pane, which is not a side effect to hand someone unasked.
//
// Unreadable reads as enabled: the point is to warn, and a warning that fires
// because tmux was not running would be noise.
func MouseEnabled() bool {
	out, err := runner.Output("tmux", "show-options", "-gqv", "mouse")
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != "off"
}

// panelBlockLines renders the owned region, fences included.
//
// The hook value is single-quoted and the run-shell argument double-quoted, so
// the `#` in `#{pane_id}` stays inside quotes — tmux only treats it as a comment
// outside them. The path is quoted because it may contain spaces.
func panelBlockLines(muxPath, key, focusKey string) []string {
	ensure := fmt.Sprintf(`'run-shell "%s panel --auto -t #{pane_id}"'`, muxPath)

	lines := []string{
		panelBlockBegin,
		fmt.Sprintf(`bind %s run-shell "%s panel -t #{pane_id}"`, key, muxPath),
		fmt.Sprintf(`bind %s run-shell "%s panel --focus -t #{pane_id}"`, focusKey, muxPath),
	}
	// The keyboard that never takes the focus. Alt combinations without a
	// prefix, so they cost one keystroke and cannot collide with tmux's own
	// prefix + arrow pane navigation.
	for _, n := range navBinds {
		lines = append(lines, fmt.Sprintf(`bind -n %-7s run-shell "%s nav -t #{pane_id} %s"`,
			n.key, muxPath, n.direction))
	}
	width := 0
	for _, h := range panelHooks {
		if len(h) > width {
			width = len(h)
		}
	}
	for _, h := range panelHooks {
		lines = append(lines, fmt.Sprintf("set-hook -g %-*s %s", width, h, ensure))
	}
	return append(append(lines, borderLines(muxPath)...), panelBlockEnd)
}

// borderLines turn on the summary tmux draws above each pane.
//
// This is the one row of a window mux can write in that is not its own pane: the
// pane below is the user's shell, and its title already belongs to whatever runs
// there — Claude Code names its pane after the task in hand, and taking that
// over would cost more than this line gives.
//
// The conditional skips the panel's own pane, which is what makes this the
// *other* column's line. Deciding it in the format rather than in `mux border`
// means the panel costs no process at all, and mux never has to work out which
// pane it is being asked about.
//
// `#()` runs the command in the background and inserts the last line it printed,
// re-reading at status-interval. Its output is cached per command string, and
// #{pane_id} makes that string differ per pane — so one cache entry per pane,
// which is what stops every border showing the first pane's line.
func borderLines(muxPath string) []string {
	return []string{
		"set -g pane-border-status top",
		// Two levels of quoting, because there are two parsers. The option value
		// is double-quoted for tmux, which is what keeps the `#` of #{pane_id}
		// from starting a comment. The path inside #() is single-quoted for
		// /bin/sh, which is what runs it — the outer quotes mean nothing there,
		// and a path with a space in it would otherwise be two arguments.
		fmt.Sprintf(`set -g pane-border-format "#{?#{m:*%s*,#{pane_start_command}},,#('%s' border -t #{pane_id} -w #{pane_width})}"`,
			panelCommand, muxPath),
	}
}

// upsertBlock writes the fenced region into path, replacing an existing one.
//
// Placement, in priority order:
//
//  1. before oh-my-tmux's `# "$@"` sentinel — it marks the end of user-editable
//     territory and writing past it is explicitly warned against;
//  2. before a trailing tpm loader (`run '…/tpm'`), which its own documentation
//     requires to be the last line. Hooks do not depend on plugins, so sitting
//     above it costs nothing and keeps that promise;
//  3. at the end.
//
// os.WriteFile rather than a temp file and rename: it follows symlinks, and
// oh-my-tmux installs `~/.tmux.conf` as one. Renaming over it would replace the
// link and leave the real file behind.
func upsertBlock(path string, block []string) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	lines := strings.Split(string(content), "\n")
	if begin, end, ok := findBlock(lines); ok {
		rest := append([]string{}, lines[end+1:]...)
		lines = append(lines[:begin], append(block, rest...)...)
	} else {
		lines = insertBlock(lines, block)
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// findBlock locates the owned region. An opening fence with no closing one is
// treated as absent rather than as running to the end of the file: truncating a
// user's config on a hand-edited fence is not a failure worth risking.
func findBlock(lines []string) (begin, end int, ok bool) {
	begin, end = -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case panelBlockBegin:
			if begin < 0 {
				begin = i
			}
		case panelBlockEnd:
			if begin >= 0 && end < 0 {
				end = i
			}
		}
	}
	return begin, end, begin >= 0 && end > begin
}

// insertBlock places a first-time block, framed by blank lines.
func insertBlock(lines, block []string) []string {
	framed := append([]string{""}, block...)

	if at := insertionPoint(lines); at >= 0 {
		framed = append(framed, "")
		out := make([]string, 0, len(lines)+len(framed))
		out = append(out, lines[:at]...)
		out = append(out, framed...)
		return append(out, lines[at:]...)
	}

	if len(lines) > 0 && lines[len(lines)-1] == "" {
		framed = framed[1:] // the file already ends blank
	}
	return append(lines, framed...)
}

// insertionPoint returns the line the block must sit above, or -1 to append.
func insertionPoint(lines []string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == ohMyTmuxSentinel {
			return i
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !isTPMLoader(trimmed) {
			return -1 // the last real line is something else; append
		}
		// Comments sitting directly on top of the loader describe it — usually
		// the "keep this last" note itself. Going above them keeps the two
		// together instead of wedging the block in between.
		for i > 0 {
			prev := strings.TrimSpace(lines[i-1])
			if prev == "" || !strings.HasPrefix(prev, "#") {
				break
			}
			i--
		}
		return i
	}
	return -1
}

// isTPMLoader reports whether line is tpm's bootstrap, which tpm documents as
// having to be the last line of the config.
func isTPMLoader(line string) bool {
	if !strings.HasPrefix(line, "run ") && !strings.HasPrefix(line, "run-shell ") {
		return false
	}
	return strings.Contains(line, "tpm")
}
