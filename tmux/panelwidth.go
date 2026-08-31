package tmux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MinPanelWidth is the narrowest pane the panel can say anything useful in.
// Below it the columns collide and it shows a notice instead.
//
// It lives here rather than beside the renderer because it is the floor on both
// sides of the round trip: `ui` refuses to draw below it, and this file refuses
// to remember or restore a width below it. Two copies of the number would let a
// hand-edited file open a pane the renderer immediately gives up on.
const MinPanelWidth = 24

// panelStateFile locates the file the panel's width is remembered in.
//
// A variable so tests can point it somewhere disposable — the same seam
// clientEnvHas uses. Without it every TogglePanel test would read whatever the
// developer's own panel happens to be set to.
var panelStateFile = defaultPanelStatePath

// panelState is what survives a tmux server restart.
//
// Deliberately not preferences.json, and deliberately not a per-session map.
//
// Not preferences.json because the TUI reads that file into memory at startup
// and writes the whole struct back on every preference change — a width saved by
// `mux watch`, which is a separate process, would be silently clobbered by the
// next sort toggle.
//
// Not per-session, and no longer per-session anywhere. There used to be a
// @mux_panel_width tmux option consulted ahead of this file, so two sessions on
// one server could differ — but it meant dragging a border in one session left
// every other panel where it was, and someone dragging a border is saying how
// wide the panel is, not how wide it is here. This file is now the whole answer.
type panelState struct {
	Width int `json:"width"`
}

func defaultPanelStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "panel.json"), nil
}

// SavedPanelWidth reports the width the panel was last dragged to, or 0 when
// there is none to restore.
//
// Every failure reads as 0 rather than as an error: a missing file is the normal
// first run, and a corrupt or hand-edited one should fall back to the default
// width instead of refusing to open a panel. A width below MinPanelWidth is
// treated the same way — the save path clamps, so one can only get there by hand.
func SavedPanelWidth() int {
	path, err := panelStateFile()
	if err != nil {
		return 0
	}
	return savedPanelWidthFrom(path)
}

func savedPanelWidthFrom(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var state panelState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0
	}
	if state.Width < MinPanelWidth {
		return 0
	}
	return state.Width
}

// SavePanelWidth records the width to open future panels at.
func SavePanelWidth(width int) error {
	path, err := panelStateFile()
	if err != nil {
		return err
	}
	return savePanelWidthTo(path, width)
}

// savePanelWidthTo writes atomically — temp file, 0600, rename — so a panel
// killed mid-write leaves the previous width rather than a truncated file. Same
// shape as the TUI's savePreferencesTo.
//
// The floor is checked here rather than in the exported wrapper so it holds
// whatever the entry point: a width the reader would refuse to restore is one
// there is no point in having written.
func savePanelWidthTo(path string, width int) error {
	if width < MinPanelWidth {
		return fmt.Errorf("panel width must be at least %d, got %d", MinPanelWidth, width)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create panel state directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".panel-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary panel state: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set panel state permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(panelState{Width: width}); err != nil {
		return fmt.Errorf("write panel state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close panel state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace panel state: %w", err)
	}
	ok = true
	return nil
}
