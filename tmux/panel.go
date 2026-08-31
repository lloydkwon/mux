package tmux

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// defaultPanelWidth is the panel pane's column count when nothing has ever
	// been dragged — the very first panel on a machine, and nothing else.
	//
	// The panel is a sidebar, so the number to beat is how little it can take
	// and still answer its questions. At 36 a session row keeps its badge, its
	// full name and its age, and still leaves ten cells for the branch (the
	// renderer drops the branch below six); the event log keeps its timestamp,
	// a name and a state label. Below about 32 the branch goes and event text
	// starts being cut, so this is the floor worth defaulting to rather than the
	// floor the panel survives — that one is MinPanelWidth, and it is much
	// lower because a user dragging deliberately gets to choose.
	defaultPanelWidth = 36

	// panelWidthOption remembers a session's panel width as a tmux user option.
	//
	// This is the *live* half of the memory and it is per session: two sessions
	// on one server can want different widths, and the option dies with its
	// session so nothing has to clean it up.
	//
	// It cannot be the whole story, because it does not survive a tmux server
	// restart — see SavedPanelWidth, which is the disk half. Neither is
	// preferences.json, for the reason spelled out on panelState.
	panelWidthOption = "@mux_panel_width"

	// panelCommand identifies the panel among a window's panes. tmux records
	// what each pane was started with, so no marker has to be maintained.
	//
	// ponytail: substring match on the start command. A pane the user launched
	// by hand with `mux watch` also matches — which is correct, that *is* the
	// panel.
	panelCommand = "mux watch"

	// panelTitle names the panel's pane, so a pane that *was* the panel can
	// still be recognised after tmux-resurrect hands it back as a bare shell.
	//
	// A pane title is the only thing that survives a restore. Measured against
	// the plugin: it saves and restores the title verbatim with no option to
	// turn that off, it saves no user options at all — a restored pane is a new
	// tmux object with nothing on it — and it re-creates the pane running
	// `cat <contents>; exec $SHELL`, so pane_start_command no longer says
	// `mux watch` either.
	//
	// resurrect cannot bring the panel itself back, and that is not fixable from
	// here: its save strategy records a pane's *child* process, and the panel is
	// the pane process with no child, so the command it saves is empty. Hence
	// this: mark the pane, and close what comes back dead.
	panelTitle = "mux panel"
)

// TogglePanel opens or closes the AI session panel in target's window. Pass ""
// for the current pane.
//
// auto marks the hook path, and changes the verb from toggle to ensure: it
// opens a missing panel but never closes one. That is what lets the resize
// hooks call the same command — they fire constantly, and a toggle there would
// flap. On after-new-window it behaves as before, since a fresh window cannot
// already hold a panel.
//
// The hook path also stands down in four cases, none of them an error: the
// session carries a @project_dir tag (tmux-project opened it for a VS Code
// window), the window is too narrow to spare the width, its session is only
// being viewed from VS Code, or the user closed the panel here by hand.
// Pressing the key passes auto=false and overrides all four — that is a
// decision, not a default, and refusing it would leave no way to see the
// panel at all.
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
		if auto {
			return nil // ensure: already there, nothing to do
		}
		// Closing by hand is a decision the resize hooks must not undo.
		_ = SetPanelDisabled(window, true)
		return runner.Run("tmux", "kill-pane", "-t", pane)
	}

	// Everything below only runs when a panel has to be created, so the common
	// hook case — a window that already has one — costs a single list-panes.

	// No live panel, but something that used to be one may still be sitting in
	// its place: tmux-resurrect restores the pane as a bare shell. Closing it
	// comes before every stand-down check below, because a window mux will not
	// put a panel in is a window that wants those columns back most.
	if ghost, alone, err := findGhostPane(window); err == nil && ghost != "" {
		if alone {
			// The ghost is the window's only pane, which means someone adopted
			// it as their shell — killing it kills the session. Clearing the
			// title is enough: nothing mistakes the pane for a ghost again, and
			// the split below puts a real panel beside it.
			_ = runner.Run("tmux", "select-pane", "-t", ghost, "-T", "")
		} else {
			_ = runner.Run("tmux", "kill-pane", "-t", ghost)
		}
	}

	if auto && PanelDisabled(window) {
		return nil
	}

	session, sessionErr := SessionForPane(target)
	if auto {
		// A session the tmux-project profile opened belongs to one VS Code
		// window, and its integrated terminal is not where 48 columns are
		// spare. The tag answers this where SessionOnlyInVSCode cannot: that
		// one has to inspect an attached client, but after-new-session fires
		// before any client attaches, so it read "not VS Code" and the panel
		// went in — and ensure semantics mean it never came back out.
		if sessionErr == nil && SessionProjectDir(session) != "" {
			return nil
		}
		if sessionErr == nil && SessionOnlyInVSCode(session) {
			return nil
		}
		if w, err := WindowWidth(target); err == nil && w < MinWindowWidth() {
			return nil
		}
	} else {
		// Opening by hand clears the manual-off mark, so the hooks resume.
		_ = SetPanelDisabled(window, false)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate mux binary: %w", err)
	}

	// Open at the width the user last dragged to. Doing it here rather than
	// resizing after the fact means the pane never appears at the wrong size
	// first, and `mux watch` has no startup race with its own resize.
	//
	// Three sources, most specific first: what this session was dragged to, what
	// any panel was last dragged to (which is what carries across a tmux server
	// restart), and only then the built-in default.
	width := defaultPanelWidth
	if w := SavedPanelWidth(); w > 0 {
		width = w
	}
	if sessionErr == nil {
		if w := PanelWidth(session); w > 0 {
			width = w
		}
	}

	// -d keeps the focus in the pane you were working in. No -b, so the panel
	// lands *after* the current pane — the right edge.
	//
	// It opened on the left for two releases, on the reasoning that a glance goes
	// left before it goes right. That holds on a monitor and not on a phone: a
	// client narrower than the window shows the leading columns, so a panel on
	// the left is the half you can see and the session you came to read is the
	// half you cannot. The pane the user types in has the better claim.
	//
	// -f is what makes that the *window's* right edge, and it is load-bearing.
	// Without it tmux splits the window's active pane — `-t <window>` resolves to
	// that pane, not to the window as a whole — so the panel is carved out of
	// whichever pane the user happened to be in. Measured in a 236-column window
	// already split three ways: the panel opened between two existing panes, and
	// because the active one was 46 columns wide tmux could not spare the 48 it
	// asked for and gave it 1. A sidebar that lands mid-window at one column is
	// indistinguishable from a broken feature, and the hooks reopen it that way
	// on every window.
	//
	// With -f the pane spans the full window height and takes its width from the
	// window, so the only thing that has to be wide enough is the window — which
	// MinWindowWidth already gates the auto path on.
	args := []string{"split-window", "-d", "-f", "-h", "-l", strconv.Itoa(width)}
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

// navKeys maps the directions `mux nav` accepts onto the keys the panel reads.
//
// The verbs are the vocabulary rather than raw key names so a binding says what
// it means, and so the panel's key handling can change without every user's
// tmux.conf having to.
var navKeys = map[string]string{
	"up":     "Up",
	"down":   "Down",
	"top":    "Home",
	"bottom": "End",
	"enter":  "Enter",
}

// NavPanel steers the panel's selection from a tmux binding.
//
// send-keys writes into the target pane's terminal without making it active,
// which is the entire point: focusing the panel would take the keyboard away
// from the pane you are typing in, and a sidebar that costs you your cursor is
// worse than one you have to reach for with the mouse.
//
// A window with no panel is not an error — the binding is global and most
// windows will not have one, and a failing run-shell puts a message on the
// user's status line every time they press the key.
func NavPanel(target, direction string) error {
	key, ok := navKeys[direction]
	if !ok {
		return fmt.Errorf("unknown direction %q (want up, down, top, bottom or enter)", direction)
	}

	window, _, err := panelWindow(target)
	if err != nil {
		return err
	}
	pane, err := findPanelPane(window)
	if err != nil {
		return err
	}
	if pane == "" {
		return nil
	}
	return runner.Run("tmux", "send-keys", "-t", pane, key)
}

// FocusPanel moves the focus into the window's panel, or back out of it when the
// panel already holds it. One key both ways, because the panel is somewhere you
// visit rather than somewhere you work.
//
// Going back is `select-pane -l` rather than "the pane to the right": that is
// the same mechanism restoreFocus uses after a click, and it returns to the pane
// you actually came from however many panes sit beside the panel.
//
// A window with no panel does nothing and reports no error, exactly as NavPanel
// does — the binding is global, most windows will not have one, and a failing
// run-shell writes to the status line on every press. Opening one is a different
// key's job.
func FocusPanel(target string) error {
	window, _, err := panelWindow(target)
	if err != nil {
		return err
	}
	pane, err := findPanelPane(window)
	if err != nil {
		return err
	}
	if pane == "" {
		return nil
	}
	if PaneActive(pane) {
		return RestoreLastPane(pane)
	}
	return runner.Run("tmux", "select-pane", "-t", pane)
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

// PaneActive reports whether target is the active pane of its window. Pass ""
// for the current pane.
//
// This is the guard on RestoreLastPane. `select-pane -l` is only ever correct
// when this pane is the one holding focus; called when it is not, it selects
// whatever the window visited before the pane the user is actually in.
func PaneActive(target string) bool {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "#{pane_active}")

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
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

// DefaultMinWindowWidth is the narrowest window worth putting a panel in.
//
// This is how "small screen" is decided, and it covers what environment cannot.
// A phone over SSH has no marker to match on — every client app differs — and
// the device was never the point: measured, a phone lands near 54 columns,
// where `aggressive-resize` shrank the window and the panel held the 48 it
// defaulted to then, leaving the work pane 5.
//
// The value is the panel's own columns plus enough left over to work in. It is
// not derived from defaultPanelWidth, because the panel's actual width is
// whatever the user dragged it to; TestDefaultMinWindowWidthLeavesRoomToWork
// only pins that the default cannot grow past what this bar can seat.
//
// It was 200, chosen to also exclude VS Code's integrated terminal (149-150
// columns) on width alone. That cost more than it bought: a Windows Terminal
// running at 188 — an ordinary window anyone would work in — was silently
// refused a panel, and a stand-down is indistinguishable from a broken feature.
// VS Code is SessionOnlyInVSCode's job anyway. The width rule now only stands
// alone for a window with nobody attached to inspect, and there it may open a
// panel in a 149-column window it used to skip. That is the trade.
const DefaultMinWindowWidth = 140

// minWindowWidthOption moves the bar for a screen the default was not measured
// on. Global rather than per-session: it describes the terminal you attach with,
// not any one session.
const minWindowWidthOption = "@mux_panel_min_width"

// MinWindowWidth is the bar in force. A missing, unparseable or non-positive
// option falls back to the default — a typo in a tmux.conf must not disable the
// panel everywhere, since nothing would say why.
func MinWindowWidth() int {
	out, err := runner.Output("tmux", "show-options", "-gqv", minWindowWidthOption)
	if err != nil {
		return DefaultMinWindowWidth
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || w <= 0 {
		return DefaultMinWindowWidth
	}
	return w
}

// panelHeaderOption turns the panel's session header on. Off unless set.
//
// Global and read-only, like @mux_panel_min_width and unlike @mux_panel_width or
// @mux_panel_off: this is a preference someone writes in their tmux.conf, not
// state mux keeps.
const panelHeaderOption = "@mux_panel_header"

// PanelHeaderEnabled reports whether the panel should draw its session header.
//
// Default off, because `mux border` now puts the same facts on the top border of
// the pane you are in. What the header still answers is the case the border
// cannot reach — the details of a session you have *selected* but are not in —
// so it stays available rather than being deleted.
//
// Anything unrecognised reads as off. A typo should leave the panel exactly as
// it was, not put chrome back that the user turned off.
func PanelHeaderEnabled() bool {
	out, err := runner.Output("tmux", "show-options", "-gqv", panelHeaderOption)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(string(out))) {
	case "on", "1", "true", "yes":
		return true
	default:
		return false
	}
}

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

// WindowShape reports the width of target's window and how many panes it holds.
//
// Both in one call because the panel needs both on every resize and they must
// describe the same instant: a width read now against a pane count read a tick
// ago would misread a split as a drag, which is the whole thing the count is
// there to prevent.
func WindowShape(target string) (width, panes int, err error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "#{window_width} #{window_panes}")

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve window shape: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("parse window shape: %q", strings.TrimSpace(string(out)))
	}
	width, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parse window width: %w", err)
	}
	panes, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parse window panes: %w", err)
	}
	return width, panes, nil
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

// panelDisabledOption marks a window where the user closed the panel by hand.
// Without it the resize hooks would reopen it on the next stray resize and the
// key would stop meaning anything. Per window, because the panel is per window.
const panelDisabledOption = "@mux_panel_off"

// PanelDisabled reports whether the panel was closed by hand in this window.
func PanelDisabled(window string) bool {
	out, err := runner.Output("tmux", "show-options", "-wqv", "-t", window, panelDisabledOption)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// SetPanelDisabled records, or clears, the manual-off mark for a window.
func SetPanelDisabled(window string, off bool) error {
	if !off {
		return runner.Run("tmux", "set-option", "-wu", "-t", window, panelDisabledOption)
	}
	return runner.Run("tmux", "set-option", "-w", "-t", window, panelDisabledOption, "1")
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

// findGhostPane returns the id of a pane that carries the panel's title but is
// no longer running it — what tmux-resurrect leaves behind — or "" when the
// window has none. alone reports that the ghost is the window's only pane.
//
// This is a second list-panes rather than another field on findPanelPane's, and
// deliberately so. It only ever runs on the path that is about to create a
// panel; the hook path, which fires constantly and almost always finds a live
// panel, still costs exactly one list-panes. alone comes from counting the
// lines of the same output, so it adds no call either.
//
// The title is compared whole rather than by substring: this kills a pane, and
// the one thing worse than a leftover pane is closing one the user was in.
// alone exists because an exact title is not proof either — resurrect restores
// titles verbatim, so a user who kept working in the restored shell owns a pane
// named exactly like the panel, and if it is all the window has, killing it
// kills the session (measured 2026-08-30: two sessions lost that way).
func findGhostPane(window string) (ghost string, alone bool, err error) {
	out, err := runner.Output("tmux", "list-panes", "-t", window,
		"-F", "#{pane_id} #{pane_title}")
	if err != nil {
		return "", false, fmt.Errorf("list panes: %w", err)
	}

	panes := 0
	for _, line := range strings.Split(string(out), "\n") {
		id, title, found := strings.Cut(strings.TrimSpace(line), " ")
		if strings.TrimSpace(line) != "" {
			panes++
		}
		if !found {
			continue // a pane with no title
		}
		if title == panelTitle {
			ghost = id
		}
	}
	return ghost, ghost != "" && panes == 1, nil
}

// MarkPanelPane names target as the panel's pane. Pass "" for the current pane.
//
// `select-pane -T` sets the title and returns; it does not make the pane active,
// which matters because the panel is created detached and must stay that way.
func MarkPanelPane(target string) error {
	args := []string{"select-pane"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "-T", panelTitle)
	return runner.Run("tmux", args...)
}
