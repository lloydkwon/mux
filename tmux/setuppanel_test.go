package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConf(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".tmux.conf")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	return path
}

func readConf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func testBlock(muxPath string) []string { return panelBlockLines(muxPath, "a") }

func TestUpsertBlockAppendsToPlainConf(t *testing.T) {
	path := writeConf(t, "set -g mouse on\n")
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	if !strings.Contains(got, "set -g mouse on") {
		t.Errorf("the user's own line was lost:\n%s", got)
	}
	for _, want := range []string{panelBlockBegin, panelBlockEnd,
		"bind a run-shell", "set-hook -g client-session-changed", "/bin/mux"} {
		if !strings.Contains(got, want) {
			t.Errorf("block is missing %q:\n%s", want, got)
		}
	}
	// Every hook the panel needs has to land, or the panel silently stops
	// following you on whichever path was dropped.
	for _, hook := range panelHooks {
		if !strings.Contains(got, "set-hook -g "+hook) {
			t.Errorf("hook %q was not written:\n%s", hook, got)
		}
	}
}

// Re-running the command must maintain one region, not stack them. The
// single-line helpers next door cannot express this, which is why the fence
// exists.
func TestUpsertBlockReplacesInPlace(t *testing.T) {
	path := writeConf(t, "set -g mouse on\n")
	if err := upsertBlock(path, testBlock("/bin/old")); err != nil {
		t.Fatalf("first upsertBlock: %v", err)
	}
	if err := upsertBlock(path, testBlock("/bin/new")); err != nil {
		t.Fatalf("second upsertBlock: %v", err)
	}

	got := readConf(t, path)
	if strings.Contains(got, "/bin/old") {
		t.Errorf("the stale path survived:\n%s", got)
	}
	if !strings.Contains(got, "/bin/new") {
		t.Errorf("the new path is missing:\n%s", got)
	}
	if n := strings.Count(got, panelBlockBegin); n != 1 {
		t.Errorf("%d opening fences, want 1:\n%s", n, got)
	}
	if n := strings.Count(got, panelBlockEnd); n != 1 {
		t.Errorf("%d closing fences, want 1:\n%s", n, got)
	}
	if n := strings.Count(got, "set -g mouse on"); n != 1 {
		t.Errorf("the user's line was duplicated or lost (%d):\n%s", n, got)
	}
}

// tpm documents its loader as having to be the last line, and users write that
// in a comment above it. Hooks do not depend on plugins, so the block goes over.
func TestUpsertBlockSitsAboveTPM(t *testing.T) {
	path := writeConf(t, "set -g mouse on\n\n# TPM must stay last\nrun '~/.tmux/plugins/tpm/tpm'\n")
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	iBlock := strings.Index(got, panelBlockBegin)
	iTPM := strings.Index(got, "run '~/.tmux/plugins/tpm/tpm'")
	if iBlock < 0 || iTPM < 0 {
		t.Fatalf("block or tpm line missing:\n%s", got)
	}
	if iBlock > iTPM {
		t.Errorf("block landed below the tpm loader:\n%s", got)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if last := lines[len(lines)-1]; !isTPMLoader(strings.TrimSpace(last)) {
		t.Errorf("last line is %q, want the tpm loader", last)
	}

	// The note above the loader is about the loader. Wedging the block between
	// them leaves a comment pointing at the wrong line.
	if second := strings.TrimSpace(lines[len(lines)-2]); second != "# TPM must stay last" {
		t.Errorf("line above the loader is %q, want its own comment:\n%s", second, got)
	}
}

// Only a *trailing* tpm loader moves the block. Anything else last means append.
func TestUpsertBlockAppendsWhenTPMIsNotLast(t *testing.T) {
	path := writeConf(t, "run '~/.tmux/plugins/tpm/tpm'\nset -g mouse on\n")
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	if strings.Index(got, panelBlockBegin) < strings.Index(got, "set -g mouse on") {
		t.Errorf("block jumped above a line it had no reason to:\n%s", got)
	}
}

// oh-my-tmux marks everything past `# "$@"` as off-limits.
func TestUpsertBlockInsertsBeforeSentinel(t *testing.T) {
	path := writeConf(t, sampleOhMyTmuxLocal)
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	iBlock := strings.Index(got, panelBlockBegin)
	iSentinel := strings.Index(got, ohMyTmuxSentinel)
	if iBlock < 0 || iSentinel < 0 {
		t.Fatalf("block or sentinel missing:\n%s", got)
	}
	if iBlock > iSentinel {
		t.Errorf("block landed past the sentinel:\n%s", got)
	}
	if !strings.Contains(got, "tmux_conf_24b_colour=true") {
		t.Errorf("the user's own settings were lost:\n%s", got)
	}
}

func TestUpsertBlockCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tmux.conf")
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}
	if got := readConf(t, path); !strings.Contains(got, panelBlockBegin) {
		t.Errorf("nothing was written:\n%s", got)
	}
}

// The panel block and the popup bind line are separately owned. Sharing a
// marker would let SetupKeybind's oh-my-tmux cleanup take the hooks with it.
func TestPanelBlockAndPopupBindCoexist(t *testing.T) {
	path := writeConf(t, "set -g mouse on\n")
	if err := upsertBindLine(path, `bind m display-popup -E "/bin/mux"`, true); err != nil {
		t.Fatalf("upsertBindLine: %v", err)
	}
	if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	if !strings.Contains(got, muxKeybindMarker) || !strings.Contains(got, panelBlockBegin) {
		t.Fatalf("the two regions did not both survive:\n%s", got)
	}

	// stripMarkerLines runs on the oh-my-tmux path of SetupKeybind. It must take
	// the popup line and leave the panel block alone.
	removed, err := stripMarkerLines(path)
	if err != nil {
		t.Fatalf("stripMarkerLines: %v", err)
	}
	if !removed {
		t.Error("the popup line was not removed")
	}
	after := readConf(t, path)
	if strings.Contains(after, muxKeybindMarker) {
		t.Errorf("popup line survived:\n%s", after)
	}
	for _, want := range []string{panelBlockBegin, panelBlockEnd, "set-hook -g client-session-changed"} {
		if !strings.Contains(after, want) {
			t.Errorf("the panel block lost %q as collateral:\n%s", want, after)
		}
	}
}

// A hand-edited fence must not eat the rest of the config.
func TestUpsertBlockIgnoresUnclosedFence(t *testing.T) {
	path := writeConf(t, panelBlockBegin+"\nbind a run-shell \"/bin/old panel\"\nset -g mouse on\n")
	if err := upsertBlock(path, testBlock("/bin/new")); err != nil {
		t.Fatalf("upsertBlock: %v", err)
	}

	got := readConf(t, path)
	if !strings.Contains(got, "set -g mouse on") {
		t.Errorf("an unclosed fence truncated the config:\n%s", got)
	}
}

// tmux only treats `#` as a comment outside quotes, so #{pane_id} has to stay
// inside them — otherwise every hook silently becomes a no-op.
func TestPanelBlockQuotingKeepsPaneIDInsideQuotes(t *testing.T) {
	for _, line := range panelBlockLines("/path with space/mux", "a") {
		if !strings.Contains(line, "#{pane_id}") {
			continue
		}
		before, _, _ := strings.Cut(line, "#{pane_id}")
		if strings.Count(before, `"`)%2 == 0 {
			t.Errorf("#{pane_id} is outside double quotes, tmux will treat it as a comment:\n  %s", line)
		}
		if !strings.Contains(line, `"/path with space/mux `) {
			t.Errorf("a path with spaces was not quoted:\n  %s", line)
		}
	}
}

func TestPanelBlockUsesTheGivenKey(t *testing.T) {
	lines := panelBlockLines("/bin/mux", "F1")
	if !strings.Contains(strings.Join(lines, "\n"), "bind F1 run-shell") {
		t.Errorf("the key was ignored:\n%s", strings.Join(lines, "\n"))
	}
}
