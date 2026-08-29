package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installedBlock is what setup-panel would have written for muxPath.
func installedBlock(t *testing.T, dir, muxPath, key, focusKey string) string {
	t.Helper()
	path := filepath.Join(dir, ".tmux.conf")
	body := strings.Join(panelBlockLines(muxPath, key, focusKey), "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	return path
}

// The case this exists for: mux moved, and the config still names where it used
// to be. tmux keeps calling a binary the user believes they replaced.
func TestRepairedPanelBlockFollowsTheBinary(t *testing.T) {
	old := panelBlockLines("/old/place/mux", "a", "Tab")

	got, ok := repairedPanelBlock(old, "/new/place/mux")
	if !ok {
		t.Fatal("a block naming another binary was left alone")
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "/old/place/mux") {
		t.Errorf("the old path survived the repair:\n%s", joined)
	}
	if !strings.Contains(joined, "'/new/place/mux'") {
		t.Errorf("the running binary is not named:\n%s", joined)
	}
}

// The keys are the user's choice, not ours to reset.
func TestRepairedPanelBlockKeepsTheKeys(t *testing.T) {
	old := panelBlockLines("/old/mux", "F1", "F2")

	got, ok := repairedPanelBlock(old, "/new/mux")
	if !ok {
		t.Fatal("no repair produced")
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "bind F1 run-shell") || !strings.Contains(joined, "bind F2 run-shell") {
		t.Errorf("repair reset the keys:\n%s", joined)
	}
}

// A correct block must not be rewritten. Otherwise opening the TUI writes to
// the user's tmux config every single time.
func TestRepairedPanelBlockLeavesACorrectOneAlone(t *testing.T) {
	current := panelBlockLines("/bin/mux", "a", "Tab")

	if _, ok := repairedPanelBlock(current, "/bin/mux"); ok {
		t.Error("a block that already names this binary was rewritten")
	}
}

// A hand-edited region is not ours to guess at: without both keys the block
// cannot be regenerated faithfully, so it is left exactly as it is.
func TestRepairedPanelBlockSkipsAnUnreadableRegion(t *testing.T) {
	block := []string{panelBlockBegin, "bind a run-shell \"'/old/mux' panel -t #{pane_id}\"", panelBlockEnd}

	if _, ok := repairedPanelBlock(block, "/new/mux"); ok {
		t.Error("regenerated a block whose focus key could not be read back")
	}
}

// No block at all is setup-panel's business, not the repair's.
func TestRepairedPanelBlockIgnoresAConfigWithoutOne(t *testing.T) {
	if _, ok := repairedPanelBlock([]string{"set -g mouse on", ""}, "/new/mux"); ok {
		t.Error("wrote a panel block into a config that never asked for one")
	}
}

// The same for the popup bind, which lives on its own tagged line.
func TestRepairedPopupBindFollowsTheBinary(t *testing.T) {
	lines := []string{popupBindLine("m", "/old/mux") + "  " + muxKeybindMarker}

	got, ok := repairedPopupBind(lines, "/new/mux")
	if !ok {
		t.Fatal("a stale popup bind was left alone")
	}
	if !strings.Contains(got, "'/new/mux'") || strings.Contains(got, "/old/mux") {
		t.Errorf("popup bind still names the old binary: %s", got)
	}
	if !strings.HasPrefix(got, "bind m ") {
		t.Errorf("popup bind lost its key: %s", got)
	}
}

func TestRepairedPopupBindLeavesACorrectOneAlone(t *testing.T) {
	lines := []string{popupBindLine("m", "/bin/mux") + "  " + muxKeybindMarker}

	if _, ok := repairedPopupBind(lines, "/bin/mux"); ok {
		t.Error("a correct popup bind was rewritten")
	}
}

func TestRepairedPopupBindIgnoresAnUntaggedConfig(t *testing.T) {
	if _, ok := repairedPopupBind([]string{`bind m display-popup -E "mux"`}, "/new/mux"); ok {
		t.Error("claimed a bind line mux does not own")
	}
}

// End to end on a real file: an installed block naming another binary is
// rewritten in place, and the fences stay put.
func TestRepairWritesTheBlockBack(t *testing.T) {
	dir := t.TempDir()
	path := installedBlock(t, dir, "/old/mux", "a", "Tab")

	lines := strings.Split(readFile(t, path), "\n")
	block, ok := repairedPanelBlock(lines, "/new/mux")
	if !ok {
		t.Fatal("no repair produced for an installed stale block")
	}
	if err := upsertBlock(path, block); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readFile(t, path)
	if strings.Contains(got, "/old/mux") {
		t.Errorf("old path left on disk:\n%s", got)
	}
	if strings.Count(got, panelBlockBegin) != 1 || strings.Count(got, panelBlockEnd) != 1 {
		t.Errorf("fences duplicated:\n%s", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
