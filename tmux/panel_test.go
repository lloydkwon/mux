package tmux

import (
	"os"
	"strings"
	"testing"
)

const paneList = "tmux list-panes -t @7 -F #{pane_id} #{pane_start_command}"

// ran reports whether any tmux command matching sub was issued. Assertions use
// it rather than counting: opening or closing by hand also records the
// manual-off mark, so the interesting command is rarely the only one.
func ran(m *mockRunner, sub string) bool {
	for _, r := range m.runs {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func mockPanelWindow(m *mockRunner) {
	m.OnOutput([]byte("@7 /work/dir\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_id} #{pane_current_path}")
}

// A window with no panel gets one, detached so the focus stays where the user
// was typing.
func TestTogglePanelOpens(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}

		self, _ := os.Executable()
		want := "tmux split-window -d -h -l " + panelWidth + " -c /work/dir -t @7 " + self + " watch"
		if !ran(m, want) {
			t.Errorf("ran %v,\nwant one of them to be %q", m.runs, want)
		}
	})
}

// The panel must start in its window's directory. Without -c the pane inherits
// the cwd of whoever ran `mux panel`, and since clicking the panel makes it the
// active pane, ListSessions then reads *that* directory as the session's — the
// bug where every session reported the same branch.
func TestTogglePanelOpensInWindowDirectory(t *testing.T) {
	// A path may contain spaces; only the first one separates it from the id.
	for _, dir := range []string{"/work/dir", "/work/my dir/deep"} {
		t.Run(dir, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte("@7 "+dir+"\n"), nil, "tmux", "display-message", "-p",
					"-t", "%3", "#{window_id} #{pane_current_path}")
				m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
					"-F", "#{pane_id} #{pane_start_command}")
				m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
				m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
				m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

				if err := TogglePanel("%3", false); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				if !ran(m, "-c "+dir+" ") {
					t.Errorf("ran %v, want the split to start in %q", m.runs, dir)
				}
			})
		})
	}
}

// An unreadable path must not stop the panel from opening.
func TestTogglePanelWithoutDirectory(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("@7\n"), nil, "tmux", "display-message", "-p",
			"-t", "%3", "#{window_id} #{pane_current_path}")
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") || ran(m, " -c ") {
			t.Errorf("ran %v, want a split with no -c", m.runs)
		}
	})
}

// tmux made the panel the active pane to deliver the click; the window being
// left behind must not keep reporting the panel as its session's command.
func TestRestoreLastPane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := RestoreLastPane("%3"); err != nil {
			t.Fatalf("RestoreLastPane: %v", err)
		}
		if want := "tmux select-pane -l -t %3"; len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})

	// No target means the current pane's window, so -t must be omitted.
	withMock(t, func(m *mockRunner) {
		if err := RestoreLastPane(""); err != nil {
			t.Fatalf("RestoreLastPane(\"\"): %v", err)
		}
		if want := "tmux select-pane -l"; len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})
}

// Pressing the key again must close the panel, not stack a second one — the
// bug that started this: two presses left two panels in the same window.
func TestTogglePanelCloses(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%9 /home/u/.local/bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}

		if !ran(m, "tmux kill-pane -t %9") {
			t.Errorf("ran %v, want the panel killed", m.runs)
		}
		if ran(m, "split-window") {
			t.Error("closing also opened a second panel")
		}
		// Closing by hand must be remembered, or the resize hooks undo it.
		if !ran(m, "set-option -w -t @7 @mux_panel_off 1") {
			t.Errorf("ran %v, want the manual-off mark recorded", m.runs)
		}
	})
}

// The worst possible bug here is killing the pane someone is working in, so
// the panel must be picked out of a window full of other panes.
func TestTogglePanelKillsOnlyThePanel(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte(
			"%1 \n"+
				"%2 vim\n"+
				"%9 /home/u/.local/bin/mux watch\n"+
				"%4 tail -f /var/log/syslog\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "tmux kill-pane -t %9") {
			t.Fatalf("ran %v, want the panel killed", m.runs)
		}
		for _, r := range m.runs {
			if strings.Contains(r, "kill-pane") && !strings.Contains(r, "%9") {
				t.Fatalf("killed the wrong pane: %q", r)
			}
		}
	})
}

// A pane running mux itself (the TUI) is not the panel — only `mux watch` is.
func TestTogglePanelIgnoresTheTUI(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%1 \n%2 /home/u/.local/bin/mux\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want a panel to be opened", m.runs)
		}
	})
}

// With no target the current pane is implied, so the -t flag must be omitted
// rather than passed empty.
func TestTogglePanelDefaultsToCurrentPane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("@7 /work/dir\n"), nil, "tmux", "display-message", "-p", "#{window_id} #{pane_current_path}")
		m.OnOutput([]byte("%1 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
	})
}

// A session that was dragged wider must reopen at that width. Applying it at
// split time rather than resizing afterwards is what avoids the pane flashing
// at the wrong size.
func TestTogglePanelOpensAtRememberedWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("72\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "-l 72 ") {
			t.Errorf("ran %v, want a split at width 72", m.runs)
		}
	})
}

// Nothing remembered, or something unusable, falls back to the default rather
// than to a zero-width pane.
func TestTogglePanelWidthFallsBack(t *testing.T) {
	for _, stored := range []string{"", "0", "-5", "wide"} {
		t.Run("stored="+stored, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				mockPanelWindow(m)
				m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
					"-F", "#{pane_id} #{pane_start_command}")
				m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
				m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
				m.OnOutput([]byte(stored+"\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

				if err := TogglePanel("%3", false); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				if !ran(m, "-l "+panelWidth+" ") {
					t.Errorf("ran %v, want the default width %s", m.runs, panelWidth)
				}
			})
		})
	}
}

func TestSetPanelWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := SetPanelWidth("work", 72); err != nil {
			t.Fatalf("SetPanelWidth: %v", err)
		}
		want := "tmux set-option -t work @mux_panel_width 72"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})

	if err := SetPanelWidth("work", 0); err == nil {
		t.Error("a non-positive width was accepted")
	}
}

func TestSessionForPane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		got, err := SessionForPane("%3")
		if err != nil || got != "work" {
			t.Fatalf("SessionForPane = %q, %v", got, err)
		}
	})

	// No target means the current pane, so the flag must be omitted entirely.
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "#{session_name}")
		if _, err := SessionForPane(""); err != nil {
			t.Fatalf("SessionForPane(\"\"): %v", err)
		}
	})
}

// A resize event only reports the pane's own size. The window's size is the
// extra fact that separates a border drag from tmux redistributing panes.
func TestWindowWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("269\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		got, err := WindowWidth("%3")
		if err != nil || got != 269 {
			t.Fatalf("WindowWidth = %d, %v", got, err)
		}
	})

	// No target means the current pane's window, so -t must be omitted.
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("150\n"), nil, "tmux", "display-message", "-p", "#{window_width}")
		if got, err := WindowWidth(""); err != nil || got != 150 {
			t.Fatalf("WindowWidth(\"\") = %d, %v", got, err)
		}
	})

	// Garbage must be an error, not a silent zero that reads as "no window".
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("wide\n"), nil, "tmux", "display-message", "-p", "#{window_width}")
		if _, err := WindowWidth(""); err == nil {
			t.Error("unparsable width was accepted")
		}
	})
}

func TestResizePaneWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := ResizePaneWidth("%3", 60); err != nil {
			t.Fatalf("ResizePaneWidth: %v", err)
		}
		if want := "tmux resize-pane -t %3 -x 60"; len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})

	withMock(t, func(m *mockRunner) {
		if err := ResizePaneWidth("", 40); err != nil {
			t.Fatalf("ResizePaneWidth(\"\"): %v", err)
		}
		if want := "tmux resize-pane -x 40"; len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})
}

// tmux cannot hide a pane from one client and show it to another, so the only
// lever is whether the panel gets created. "No client attached" must not count
// as VS Code, or a session made by a script would silently lose its panel.
func TestSessionOnlyInVSCode(t *testing.T) {
	tests := []struct {
		name    string
		clients string
		vscode  map[int]bool
		want    bool
	}{
		{
			name:    "every client is VS Code",
			clients: "16\n20\n",
			vscode:  map[int]bool{16: true, 20: true},
			want:    true,
		},
		{
			name:    "one client is a real terminal",
			clients: "16\n27\n",
			vscode:  map[int]bool{16: true},
			want:    false,
		},
		{
			name:    "nothing attached is not evidence",
			clients: "",
			vscode:  nil,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte(tc.clients), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
				old := clientEnvHas
				clientEnvHas = func(pid int, marker string) bool {
					if marker != vscodeEnvMarker {
						t.Errorf("looked for %q, want %q", marker, vscodeEnvMarker)
					}
					return tc.vscode[pid]
				}
				defer func() { clientEnvHas = old }()

				if got := SessionOnlyInVSCode("work"); got != tc.want {
					t.Errorf("SessionOnlyInVSCode = %v, want %v", got, tc.want)
				}
			})
		})
	}
}

// The hook path stays out of VS Code; pressing the key there still works, or
// there would be no way to see the panel at all.
func TestTogglePanelAutoSkipsVSCode(t *testing.T) {
	setup := func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("16\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
	}
	onlyVSCode := func(int, string) bool { return true }

	withMock(t, func(m *mockRunner) {
		setup(m)
		old := clientEnvHas
		clientEnvHas = onlyVSCode
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 0 {
			t.Errorf("ran %v, want nothing in a VS Code-only session", m.runs)
		}
	})

	withMock(t, func(m *mockRunner) {
		setup(m)
		old := clientEnvHas
		clientEnvHas = onlyVSCode
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want the explicit call to open a panel", m.runs)
		}
	})
}

// A phone over SSH cannot be told apart by environment, and the real problem
// was never the device but the width: `aggressive-resize` shrinks the window
// and a 48-column panel leaves the work pane almost nothing.
func TestTogglePanelAutoSkipsNarrowWindow(t *testing.T) {
	setup := func(m *mockRunner, width string) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("27\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte(width+"\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
	}
	notVSCode := func(int, string) bool { return false }

	tests := []struct {
		name      string
		width     string
		auto      bool
		wantSplit bool
	}{
		{name: "a phone-sized window is skipped", width: "54", auto: true, wantSplit: false},
		{name: "one column short is still skipped", width: "199", auto: true, wantSplit: false},
		{name: "exactly the minimum is wide enough", width: "200", auto: true, wantSplit: true},
		{name: "a VS Code-sized window is skipped", width: "150", auto: true, wantSplit: false},
		{name: "a desktop window is fine", width: "269", auto: true, wantSplit: true},
		{name: "pressing the key overrides the width", width: "54", auto: false, wantSplit: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				setup(m, tc.width)
				old := clientEnvHas
				clientEnvHas = notVSCode
				defer func() { clientEnvHas = old }()

				if err := TogglePanel("%3", tc.auto); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				got := ran(m, "split-window")
				if got != tc.wantSplit {
					t.Errorf("split=%v, want %v (ran %v)", got, tc.wantSplit, m.runs)
				}
			})
		})
	}
}

// The resize hooks fire constantly. If --auto still toggled, the panel would
// flap open and shut every time a terminal changed size.
func TestTogglePanelAutoNeverCloses(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%9 /home/u/.local/bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 0 {
			t.Errorf("ran %v, want nothing — the panel is already there", m.runs)
		}
	})

	// Pressing the key is still a toggle.
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%9 /home/u/.local/bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "kill-pane -t %9") {
			t.Errorf("ran %v, want the panel closed", m.runs)
		}
	})
}

// Closing the panel by hand has to survive the resize hooks, or the key stops
// meaning anything the moment a terminal is nudged.
func TestTogglePanelAutoRespectsManualOff(t *testing.T) {
	setup := func(m *mockRunner, off string) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte(off+"\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("27\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte("269\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
	}
	notVSCode := func(int, string) bool { return false }

	withMock(t, func(m *mockRunner) {
		setup(m, "1")
		old := clientEnvHas
		clientEnvHas = notVSCode
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if ran(m, "split-window") {
			t.Errorf("ran %v, want the manual-off mark respected", m.runs)
		}
	})

	// Pressing the key overrides it, and clears the mark so the hooks resume.
	withMock(t, func(m *mockRunner) {
		setup(m, "1")
		old := clientEnvHas
		clientEnvHas = notVSCode
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want the explicit call to open a panel", m.runs)
		}
		if !ran(m, "set-option -wu -t @7 @mux_panel_off") {
			t.Errorf("ran %v, want the manual-off mark cleared", m.runs)
		}
	})
}
