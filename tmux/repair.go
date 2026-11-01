package tmux

import (
	"fmt"
	"os"
	"strings"
)

// RepairOwnedConfig brings mux's own regions of the tmux config back in line
// with the binary that is running.
//
// `setup-keybind`, `setup-panel` and `setup-resurrect` write an absolute path,
// resolved once from os.Executable(). That path is right until the day mux is installed
// somewhere else — a different PREFIX, a `go install` when the last one was a
// release tarball, a GOBIN that moved — and then nothing says so. tmux goes on
// calling a binary the user believes they replaced, and the symptom is a
// feature that quietly behaves like an old version. Removing the old copy is
// worse rather than better: every hook then fails, on every window event, with
// an error that only reaches the status line.
//
// The regions are mux-owned and marker-fenced, which is what makes rewriting
// them safe: `setup-panel` already replaces the block wholesale, so this is the
// same operation with the keys carried over rather than re-typed. A block whose
// keys cannot be read back is left alone — a hand-edited region is not ours to
// guess at.
//
// It is deliberately a full comparison rather than a path comparison. A block
// written by an older mux differs in shape as well as in path (the quoting fix
// for paths with spaces, for one), and healing that on the next run is the same
// job.
//
// Returns whether anything was written.
func RepairOwnedConfig() (bool, error) {
	muxPath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("locate mux binary: %w", err)
	}

	confPath, err := findTmuxConf()
	if err != nil {
		return false, err
	}
	target := confPath
	if isOhMyTmux(confPath) {
		target = findTmuxConfLocal(confPath)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		return false, nil // nothing installed here yet — setup's job, not ours
	}
	lines := strings.Split(string(content), "\n")

	changed := false
	if block, ok := repairedPanelBlock(lines, muxPath); ok {
		if err := upsertBlock(target, block); err != nil {
			return changed, err
		}
		if err := applyToServer(block); err != nil {
			return true, err
		}
		changed = true
	}
	if block, ok := repairedResurrectBlock(lines, muxPath); ok {
		if err := upsertFenced(target, resurrectBlockBegin, resurrectBlockEnd, block); err != nil {
			return changed, err
		}
		if err := applyToServer(block); err != nil {
			return true, err
		}
		changed = true
	}
	if line, ok := repairedPopupBind(lines, muxPath); ok {
		if err := upsertBindLine(target, line, false); err != nil {
			return changed, err
		}
		if err := applyToServer([]string{line}); err != nil {
			return true, err
		}
		changed = true
	}
	return changed, nil
}

// repairedPanelBlock returns the block the panel region should hold, and
// whether it differs from what is there. ok is false when there is no block,
// when its keys cannot be read back, or when it is already correct.
func repairedPanelBlock(lines []string, muxPath string) ([]string, bool) {
	begin, end, found := findBlock(lines)
	if !found {
		return nil, false
	}
	existing := lines[begin : end+1]

	key, focusKey, ok := panelBlockKeys(existing)
	if !ok {
		return nil, false
	}
	want := panelBlockLines(muxPath, key, focusKey)
	if equalLines(existing, want) {
		return nil, false
	}
	return want, true
}

// panelBlockKeys reads the two keys back out of an installed block, so a repair
// keeps whatever the user chose at setup time. The toggle line is the one that
// runs `panel -t`, the focus line the one that runs `panel --focus -t`.
func panelBlockKeys(block []string) (key, focusKey string, ok bool) {
	for _, line := range block {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "bind" {
			continue
		}
		switch {
		case strings.Contains(line, " panel --focus -t "):
			focusKey = fields[1]
		case strings.Contains(line, " panel -t "):
			key = fields[1]
		}
	}
	return key, focusKey, key != "" && focusKey != ""
}

// repairedResurrectBlock returns the session save/restore region as it should
// be for this binary, and whether that differs from what is there.
//
// The region names mux in its restore hooks, so it goes stale the same way the
// panel block does. Its settings are read back out of the block rather than
// re-decided: whether it loads the plugins (a run-shell line), from where (the
// resurrect entry point's directory), and whether the hooks are ours to write
// (not when the rest of the config sets them). A block that loads plugins from
// a path that cannot be read back is left alone. A block written before the
// hooks existed gains them here, which is how an existing install picks up the
// fix for the restored dead panel.
func repairedResurrectBlock(lines []string, muxPath string) ([]string, bool) {
	begin, end, found := findFenced(lines, resurrectBlockBegin, resurrectBlockEnd)
	if !found {
		return nil, false
	}
	existing := lines[begin : end+1]

	loadPlugins, pluginDir := false, ""
	const entry = "/tmux-resurrect/resurrect.tmux"
	for _, line := range existing {
		if !strings.HasPrefix(strings.TrimSpace(line), "run-shell") || !strings.Contains(line, entry) {
			continue
		}
		loadPlugins = true
		start := strings.Index(line, `"'`)
		stop := strings.Index(line, entry+`'"`)
		if start < 0 || stop < start {
			return nil, false
		}
		pluginDir = line[start+2 : stop]
	}
	if loadPlugins && (pluginDir == "" || strings.Contains(pluginDir, "'")) {
		return nil, false // a quoted path we cannot round-trip — not ours to guess at
	}

	rest := strings.Join(append(append([]string{}, lines[:begin]...), lines[end+1:]...), "\n")
	want := resurrectBlockLines(muxPath, pluginDir, loadPlugins, !userSetsRestoreHooks(rest))
	if equalLines(existing, want) {
		return nil, false
	}
	return want, true
}

// repairedPopupBind returns the bind line the popup region should hold, and
// whether it differs from the tagged line already there.
func repairedPopupBind(lines []string, muxPath string) (string, bool) {
	key := ""
	for _, line := range lines {
		if !strings.Contains(line, muxKeybindMarker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "bind" || fields[0] == "bind-key") {
			key = fields[1]
		}
	}
	if key == "" {
		return "", false
	}

	want := popupBindLine(key, muxPath)
	for _, line := range lines {
		if strings.Contains(line, muxKeybindMarker) && strings.TrimSpace(line) == want+"  "+muxKeybindMarker {
			return "", false
		}
	}
	return want, true
}

// applyToServer hands the repaired lines to the running tmux server, so the fix
// takes effect now rather than at the next config reload. Sourcing a temporary
// file holding only mux's own lines is what keeps this from re-running the
// user's whole config as a side effect of opening the TUI.
//
// A missing server is not a failure: there is nothing to correct, and the file
// on disk is already right for the next one.
func applyToServer(lines []string) error {
	if os.Getenv("TMUX") == "" {
		return nil
	}

	f, err := os.CreateTemp("", "mux-repair-*.conf")
	if err != nil {
		return nil // the file on disk is repaired; this half is best-effort
	}
	defer os.Remove(f.Name())

	_, writeErr := f.WriteString(strings.Join(lines, "\n") + "\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return nil
	}
	_ = runner.Run("tmux", "source-file", f.Name())
	return nil
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
