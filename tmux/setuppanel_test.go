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

func testBlock(muxPath string) []string { return panelBlockLines(muxPath, "a", "Tab") }

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
	for _, line := range panelBlockLines("/path with space/mux", "a", "Tab") {
		if !strings.Contains(line, "#{pane_id}") {
			continue
		}
		before, _, _ := strings.Cut(line, "#{pane_id}")
		if strings.Count(before, `"`)%2 == 0 {
			t.Errorf("#{pane_id} is outside double quotes, tmux will treat it as a comment:\n  %s", line)
		}
		// Quoted, but by whichever parser is going to read it: tmux runs the
		// bindings and hooks itself and takes double quotes, while the border's
		// #() body is handed to /bin/sh, where only the inner single quotes
		// survive to keep a spaced path as one argument.
		if !strings.Contains(line, `"/path with space/mux `) &&
			!strings.Contains(line, `'/path with space/mux' `) {
			t.Errorf("a path with spaces was not quoted:\n  %s", line)
		}
	}
}

func TestPanelBlockUsesTheGivenKey(t *testing.T) {
	block := strings.Join(panelBlockLines("/bin/mux", "F1", "F2"), "\n")
	if !strings.Contains(block, "bind F1 run-shell") {
		t.Errorf("the toggle key was ignored:\n%s", block)
	}
	if !strings.Contains(block, "bind F2 run-shell") {
		t.Errorf("the focus key was ignored:\n%s", block)
	}
}

// The panel has two keyboards, and the block has to install both: one that
// steps into it, and one that steers it from where you are already typing.
func TestPanelBlockBindsBothKeyboards(t *testing.T) {
	block := strings.Join(panelBlockLines("/bin/mux", "a", "Tab"), "\n")

	if !strings.Contains(block, `bind Tab run-shell "'/bin/mux' panel --focus -t #{pane_id}"`) {
		t.Errorf("no focus binding:\n%s", block)
	}
	for _, n := range navBinds {
		want := "bind " + n.key
		if n.rootless {
			want = "bind -n " + n.key
		}
		if !strings.Contains(block, want) {
			t.Errorf("no %q binding:\n%s", want, block)
		}
		if !strings.Contains(block, "nav -t #{pane_id} "+n.direction) {
			t.Errorf("%s does not steer %q:\n%s", n.key, n.direction, block)
		}
	}
}

// Moving the cursor is rootless on purpose: needing the prefix would defeat a
// keyboard meant not to interrupt what you are typing. Committing is the
// exception, and it has to be — `bind -n M-Enter` took Alt+Enter away from every
// program in every pane, and that is how Claude Code inserts a newline.
func TestPanelBlockLeavesAltEnterAlone(t *testing.T) {
	block := strings.Join(panelBlockLines("/bin/mux", "a", "Tab"), "\n")

	if strings.Contains(block, "M-Enter") {
		t.Errorf("Alt+Enter is bound, which takes Claude Code's newline key:\n%s", block)
	}
	for _, line := range strings.Split(block, "\n") {
		switch {
		case !strings.Contains(line, " nav -t "):
			continue
		case strings.Contains(line, " enter"):
			if strings.HasPrefix(line, "bind -n ") {
				t.Errorf("committing must not be rootless: %q", line)
			}
		default:
			if !strings.HasPrefix(line, "bind -n ") {
				t.Errorf("moving the cursor should not need the prefix: %q", line)
			}
		}
	}
}

// The border is the one row of a window mux can write in that is not its own
// pane, and it only makes sense with the status line turned on.
func TestPanelBlockInstallsTheBorder(t *testing.T) {
	block := strings.Join(panelBlockLines("/bin/mux", "a", "Tab"), "\n")

	if !strings.Contains(block, "set -g pane-border-status top") {
		t.Errorf("the border status line is not turned on:\n%s", block)
	}
	if !strings.Contains(block, "'/bin/mux' border -t #{pane_id} -w #{pane_width}") {
		t.Errorf("the border does not call mux:\n%s", block)
	}

	// The panel draws its own list; a summary above it would say what its first
	// rows already say, and the user asked for the other column.
	if !strings.Contains(block, panelCommand) {
		t.Errorf("the format does not skip the panel's own pane:\n%s", block)
	}
}

// Re-running setup replaces the block, so the options must not stack up.
func TestSetupPanelBorderStaysSingleOnRerun(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".tmux.conf")
	if err := os.WriteFile(path, []byte("set -g mouse on\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := upsertBlock(path, testBlock("/bin/mux")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	got := readConf(t, path)
	if n := strings.Count(got, "pane-border-status"); n != 1 {
		t.Errorf("pane-border-status appears %d times, want 1:\n%s", n, got)
	}
	if n := strings.Count(got, "pane-border-format"); n != 1 {
		t.Errorf("pane-border-format appears %d times, want 1:\n%s", n, got)
	}
}

// Every line the block writes is read by two parsers, and only the outer one is
// tmux's. An unquoted path with a space in it reaches /bin/sh as two words and
// the command does not run — measured through run-shell and display-popup
// alike, and the only symptom is a line on the status bar. macOS puts spaces in
// paths routinely, and `make install PREFIX=...` will take one anywhere.
func TestPanelBlockQuotesAPathWithSpaces(t *testing.T) {
	const path = "/Applications/My Tools/mux"
	block := panelBlockLines(path, "a", "Tab")

	for _, line := range block {
		if !strings.Contains(line, path) {
			continue // a fence, or the border-status line
		}
		if !strings.Contains(line, "'"+path+"'") {
			t.Errorf("path reaches the shell unquoted and will split on the space:\n  %s", line)
		}
	}
}

// The hooks cannot be a single-quoted string once the path carries quotes of
// its own: tmux has no escape inside single quotes, and the line is a parse
// error rather than a silent mis-parse. Braces defer the whole value, which is
// also what keeps #{pane_id} meaning the pane the hook fired for.
func TestPanelBlockHooksUseABraceBlock(t *testing.T) {
	block := panelBlockLines("/Applications/My Tools/mux", "a", "Tab")

	for _, line := range block {
		if !strings.HasPrefix(line, "set-hook ") {
			continue
		}
		if !strings.Contains(line, "{ run-shell ") || !strings.HasSuffix(line, " }") {
			t.Errorf("hook value is not a brace block:\n  %s", line)
		}
		if !strings.Contains(line, "#{pane_id}") {
			t.Errorf("hook lost its deferred pane id:\n  %s", line)
		}
	}
}

// A path may hold an apostrophe, and POSIX has no escape inside single quotes —
// the only way out is to close, emit an escaped quote, and reopen. shellQuote
// does that; a hand-rolled "'"+p+"'" would end the quoting early and hand the
// shell a broken command.
func TestPanelBlockQuotesAnApostrophe(t *testing.T) {
	block := strings.Join(panelBlockLines("/home/it's/mux", "a", "Tab"), "\n")

	if strings.Contains(block, "'/home/it's/mux'") {
		t.Errorf("apostrophe ended the quoting early:\n%s", block)
	}
	if !strings.Contains(block, `'/home/it'\''s/mux'`) {
		t.Errorf("apostrophe not escaped for the shell:\n%s", block)
	}
}
