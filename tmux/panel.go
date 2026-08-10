package tmux

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// panelWidth is the panel pane's column count when the session has no
	// remembered width — every new session, and every session after a tmux
	// server restart.
	panelWidth = "48"

	// panelWidthOption remembers a session's panel width as a tmux user option.
	//
	// Deliberately not preferences.json. `mux watch` is a separate process from
	// the TUI, which holds its preferences in memory from startup and writes the
	// whole file back — a width saved by the panel would be silently clobbered.
	// A tmux option also needs no cleanup: it dies with the session, and so does
	// the panel, since neither survives a server restart.
	panelWidthOption = "@mux_panel_width"

	// panelCommand identifies the panel among a window's panes. tmux records
	// what each pane was started with, so no marker has to be maintained.
	//
	// ponytail: substring match on the start command. A pane the user launched
	// by hand with `mux watch` also matches — which is correct, that *is* the
	// panel.
	panelCommand = "mux watch"
)

// TogglePanel closes the AI session panel in target's window if there is one,
// and opens it otherwise. Pass "" for the current pane.
//
// A window that has just been created cannot already hold a panel, so on an
// after-new-window hook this reduces to "open" — which is why the keybinding
// and the hooks can share one command instead of needing an open-only variant.
//
// auto marks the hook path. It skips windows whose session is only being viewed
// from VS Code, where the panel costs more width than it is worth. The
// keybinding passes false: pressing it is a decision, and refusing there would
// leave no way to see the panel in VS Code at all.
func TogglePanel(target string, auto bool) error {
	window, dir, err := panelWindow(target)
	if err != nil {
		return err
	}

	pane, err := findPanelPane(window)
	if err != nil {
		return err
	}
	if pane != "" {
		return runner.Run("tmux", "kill-pane", "-t", pane)
	}

	session, sessionErr := SessionForPane(target)
	// Not errors: these are the places the panel is not wanted. A window too
	// narrow to spare the width is the mobile case; a VS Code-only session is
	// the other. Both only apply to the hook path — pressing the key opens it
	// anywhere, because that is a decision rather than a default.
	if auto {
		if sessionErr == nil && SessionOnlyInVSCode(session) {
			return nil
		}
		if w, err := WindowWidth(target); err == nil && w < MinWindowWidth {
			return nil
		}
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate mux binary: %w", err)
	}

	// Open at the width this session was last dragged to. Doing it here rather
	// than resizing after the fact means the pane never appears at the wrong
	// size first, and `mux watch` has no startup race with its own resize.
	width := panelWidth
	if sessionErr == nil {
		if w := PanelWidth(session); w > 0 {
			width = strconv.Itoa(w)
		}
	}

	// -d keeps the focus in the pane you were working in.
	args := []string{"split-window", "-d", "-h", "-l", width}
	if dir != "" {
		// Start the panel in the window's own directory. Without -c the new pane
		// inherits the cwd of whatever invoked `mux panel` — a shell, or tmux
		// itself from a hook — and that leaks: clicking the panel makes it the
		// active pane, and ListSessions reads the session's directory from
		// #{pane_current_path} of the active pane. Every session then reports the
		// panel's directory, so every branch reads the same.
		args = append(args, "-c", dir)
	}
	args = append(args, "-t", window, self+" watch")
	return runner.Run("tmux", args...)
}

// RestoreLastPane moves focus back to the pane that was active before, in
// target's window.
//
// Clicking the panel makes it the active pane — tmux's own MouseDown1Pane
// binding runs select-pane before forwarding the event, and that cannot be
// avoided without rebinding it globally. Left alone, the window we just
// switched away from keeps reporting the panel's command as its own.
// Pass "" for the current pane's window.
func RestoreLastPane(target string) error {
	args := []string{"select-pane", "-l"}
	if target != "" {
		args = append(args, "-t", target)
	}
	return runner.Run("tmux", args...)
}

// SessionForPane reports the tmux session holding target. Pass "" for the
// current pane. Only valid from inside tmux.
func SessionForPane(target string) (string, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "#{session_name}")

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return "", fmt.Errorf("resolve session: %w", err)
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no tmux session for target %q", target)
	}
	return name, nil
}

// PanelWidth reports the remembered panel width for a session, or 0 when none
// has been recorded. A missing option is not an error — -q keeps tmux quiet.
func PanelWidth(session string) int {
	out, err := runner.Output("tmux", "show-options", "-qv",
		"-t", session, panelWidthOption)
	if err != nil {
		return 0
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// vscodeEnvMarker is what VS Code's integrated terminal exports into every
// shell it starts, and so into a tmux client started from one.
const vscodeEnvMarker = "TERM_PROGRAM=vscode"

// MinWindowWidth is the narrowest window worth putting a panel in: twice the
// panel's own width, so it never takes more than half.
//
// This is how "not on mobile" is decided. A phone over SSH cannot be identified
// by environment the way VS Code can — every client app differs — and the real
// problem was never the device but the width. Measured, a phone lands near 54
// columns, where `aggressive-resize` shrank the window and the panel held its
// 48, leaving the work pane 5.
const MinWindowWidth = 96

// clientEnvHas is the /proc lookup, replaceable in tests.
var clientEnvHas = procEnvHas

// SessionOnlyInVSCode reports whether every client attached to session is a
// VS Code integrated terminal.
//
// False when no client is attached: a session created detached has nobody to
// judge by, and defaulting to "skip" there would silently deny a panel to every
// session a script makes. No evidence is not evidence.
//
// This is the closest tmux allows to "hide it in VS Code". A pane belongs to a
// window, not a client, so a window seen from both terminals shows the panel to
// both — the only lever is whether it gets created at all.
func SessionOnlyInVSCode(session string) bool {
	out, err := runner.Output("tmux", "list-clients", "-t", session, "-F", "#{client_pid}")
	if err != nil {
		return false
	}

	seen := false
	for _, line := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		seen = true
		if !clientEnvHas(pid, vscodeEnvMarker) {
			return false
		}
	}
	return seen
}

// WindowWidth reports the width of target's window. Pass "" for the current
// pane's window.
//
// A resize event only tells a program its own pane's new size. Knowing whether
// the *window* changed too is what separates a border drag from tmux
// redistributing panes after a re-layout.
func WindowWidth(target string) (int, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "#{window_width}")

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return 0, fmt.Errorf("resolve window width: %w", err)
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse window width: %w", err)
	}
	return w, nil
}

// ResizePaneWidth sets target's pane to width columns. Pass "" for the current
// pane.
func ResizePaneWidth(target string, width int) error {
	args := []string{"resize-pane"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "-x", strconv.Itoa(width))
	return runner.Run("tmux", args...)
}

// SetPanelWidth records the panel width for a session.
func SetPanelWidth(session string, width int) error {
	if width <= 0 {
		return fmt.Errorf("panel width must be positive, got %d", width)
	}
	return runner.Run("tmux", "set-option", "-t", session,
		panelWidthOption, strconv.Itoa(width))
}

// panelWindow resolves the window holding target and that window's current
// directory, in one round trip.
//
// A window id never contains a space and a path may, so the split is on the
// first one only — cutting anywhere else would silently open the panel in a
// truncated directory.
func panelWindow(target string) (window, dir string, err error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "#{window_id} #{pane_current_path}")

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return "", "", fmt.Errorf("resolve window: %w", err)
	}
	window, dir, _ = strings.Cut(strings.TrimSpace(string(out)), " ")
	if window == "" {
		return "", "", fmt.Errorf("no tmux window for target %q", target)
	}
	// An unreadable path is not fatal: opening the panel in the wrong directory
	// beats not opening it.
	return window, dir, nil
}

// findPanelPane returns the id of the panel pane in window, or "" when the
// window has none.
func findPanelPane(window string) (string, error) {
	out, err := runner.Output("tmux", "list-panes", "-t", window,
		"-F", "#{pane_id} #{pane_start_command}")
	if err != nil {
		return "", fmt.Errorf("list panes: %w", err)
	}

	for _, line := range strings.Split(string(out), "\n") {
		id, cmd, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue // a pane with no recorded start command
		}
		if strings.Contains(cmd, panelCommand) {
			return id, nil
		}
	}
	return "", nil
}
