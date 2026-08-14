package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// The round trip is the whole feature: a width dragged in one process has to be
// what the next one opens at.
func TestSavePanelWidthRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.json")

	if err := savePanelWidthTo(path, 52); err != nil {
		t.Fatalf("savePanelWidthTo: %v", err)
	}
	if got := savedPanelWidthFrom(path); got != 52 {
		t.Errorf("savedPanelWidthFrom = %d, want 52", got)
	}

	// Saving again replaces rather than appends — the rename has to land on top
	// of the existing file, not beside it.
	if err := savePanelWidthTo(path, 36); err != nil {
		t.Fatalf("savePanelWidthTo (second): %v", err)
	}
	if got := savedPanelWidthFrom(path); got != 36 {
		t.Errorf("savedPanelWidthFrom after rewrite = %d, want 36", got)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want just the state file — a temp file was left behind", len(entries))
	}
}

// The file records where the user works. It is written the way preferences are.
func TestSavePanelWidthIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "panel.json")
	if err := savePanelWidthTo(path, 40); err != nil {
		t.Fatalf("savePanelWidthTo: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
}

// Every unreadable state means "nothing remembered", never an error: the panel
// has a default to fall back on, and refusing to open one would be a worse
// answer than opening it at 36.
func TestSavedPanelWidthDegradesToZero(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string // "" means: do not create the file at all
	}{
		{"missing", ""},
		{"garbage", "not json at all"},
		{"empty object", "{}"},
		{"negative", `{"width": -10}`},
		{"below the floor", `{"width": 23}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".json")
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}
			if got := savedPanelWidthFrom(path); got != 0 {
				t.Errorf("savedPanelWidthFrom = %d, want 0", got)
			}
		})
	}

	// The floor itself is fine — it is the bar, not one above it.
	path := filepath.Join(dir, "floor.json")
	if err := savePanelWidthTo(path, MinPanelWidth); err != nil {
		t.Fatalf("savePanelWidthTo: %v", err)
	}
	if got := savedPanelWidthFrom(path); got != MinPanelWidth {
		t.Errorf("savedPanelWidthFrom = %d, want %d", got, MinPanelWidth)
	}
}

// A width the panel could not render in is refused at the door rather than
// written and then quietly ignored on the way back.
func TestSavePanelWidthRefusesBelowTheFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.json")
	if err := savePanelWidthTo(path, MinPanelWidth-1); err == nil {
		t.Error("savePanelWidthTo accepted a width below MinPanelWidth")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused width still created the file")
	}
}

// The default has to be a width the panel is allowed to keep. A default below
// the floor would open a pane that immediately reports itself too narrow.
func TestDefaultPanelWidthClearsTheFloor(t *testing.T) {
	if defaultPanelWidth < MinPanelWidth {
		t.Errorf("defaultPanelWidth = %d, below MinPanelWidth %d", defaultPanelWidth, MinPanelWidth)
	}
}
