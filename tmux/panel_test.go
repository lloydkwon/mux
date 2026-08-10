package tmux

import (
	"os"
	"strings"
	"testing"
)

const paneList = "tmux list-panes -t @7 -F #{pane_id} #{pane_start_command}"

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

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}

		self, _ := os.Executable()
		want := "tmux split-window -d -h -l " + panelWidth + " -c /work/dir -t @7 " + self + " watch"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v,\nwant [%q]", m.runs, want)
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
				m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
				m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

				if err := TogglePanel("%3"); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				if len(m.runs) != 1 || !strings.Contains(m.runs[0], "-c "+dir+" ") {
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
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 1 || strings.Contains(m.runs[0], " -c ") {
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

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}

		want := "tmux kill-pane -t %9"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
		for _, r := range m.runs {
			if strings.Contains(r, "split-window") {
				t.Error("closing also opened a second panel")
			}
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

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 1 || m.runs[0] != "tmux kill-pane -t %9" {
			t.Fatalf("ran %v, want only the panel killed", m.runs)
		}
	})
}

// A pane running mux itself (the TUI) is not the panel — only `mux watch` is.
func TestTogglePanelIgnoresTheTUI(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%1 \n%2 /home/u/.local/bin/mux\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 1 || !strings.Contains(m.runs[0], "split-window") {
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

		if err := TogglePanel(""); err != nil {
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
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("72\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := TogglePanel("%3"); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 1 || !strings.Contains(m.runs[0], "-l 72 ") {
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
				m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
				m.OnOutput([]byte(stored+"\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

				if err := TogglePanel("%3"); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				if len(m.runs) != 1 || !strings.Contains(m.runs[0], "-l "+panelWidth+" ") {
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
