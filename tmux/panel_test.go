package tmux

import (
	"os"
	"strconv"
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
		want := "tmux split-window -d -f -h -l " + strconv.Itoa(defaultPanelWidth) +
			" -c /work/dir -t @7 " + self + " watch"
		if !ran(m, want) {
			t.Errorf("ran %v,\nwant one of them to be %q", m.runs, want)
		}
	})
}

// The split must be full-width, not a split of whatever pane happened to be
// active.
//
// Its own test because the failure is invisible in a single-pane window — which
// is where the panel is usually opened — and only shows up once a window has
// been split: tmux carves the panel out of the active pane, so an active pane
// narrower than the panel width yields a one-column sidebar wedged between two
// existing panes. -f is the whole fix, so -f is what this pins.
func TestTogglePanelSplitsTheWindowNotTheActivePane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}

		if !ran(m, "split-window -d -f ") {
			t.Errorf("ran %v, want the split to carry -f so it spans the window", m.runs)
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
				if !ran(m, "-l "+strconv.Itoa(defaultPanelWidth)+" ") {
					t.Errorf("ran %v, want the default width", m.runs)
				}
			})
		})
	}
}

// A remembered width outranks the default — dragging the panel narrow has to
// stick across reopens.
func TestTogglePanelRememberedWidthWins(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("40\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "-l 40 ") {
			t.Errorf("ran %v, want the remembered width 40", m.runs)
		}
	})
}

// The disk copy is what carries a dragged width across a tmux server restart,
// where the session option cannot: the option dies with the server, and every
// panel would otherwise reopen at the default the user had already rejected.
func TestTogglePanelFallsBackToSavedWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		// A fresh server: the session remembers nothing.
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := SavePanelWidth(52); err != nil {
			t.Fatalf("SavePanelWidth: %v", err)
		}
		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "-l 52 ") {
			t.Errorf("ran %v, want the saved width 52", m.runs)
		}
	})
}

// The session's own width is the more specific fact, so it outranks the disk
// copy — two sessions on one server are allowed to differ.
func TestTogglePanelSessionWidthOutranksSavedWidth(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("40\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")

		if err := SavePanelWidth(52); err != nil {
			t.Fatalf("SavePanelWidth: %v", err)
		}
		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "-l 40 ") {
			t.Errorf("ran %v, want the session's own width 40", m.runs)
		}
	})
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

// The bar has to be movable, because the default was measured on somebody
// else's terminal — but a bad value must not silently disable the panel
// everywhere, since a stand-down says nothing about why.
func TestMinWindowWidth(t *testing.T) {
	tests := []struct {
		name   string
		option string
		want   int
	}{
		{name: "no option set", option: "", want: DefaultMinWindowWidth},
		{name: "an explicit bar", option: "120", want: 120},
		{name: "whitespace is trimmed", option: "  96  ", want: 96},
		{name: "zero falls back", option: "0", want: DefaultMinWindowWidth},
		{name: "negative falls back", option: "-40", want: DefaultMinWindowWidth},
		{name: "a typo falls back", option: "wide", want: DefaultMinWindowWidth},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte(tc.option+"\n"), nil, "tmux",
					"show-options", "-gqv", "@mux_panel_min_width")
				if got := MinWindowWidth(); got != tc.want {
					t.Errorf("MinWindowWidth() = %d, want %d", got, tc.want)
				}
			})
		})
	}

	// tmux itself failing is not a reason to stop opening panels.
	withMock(t, func(m *mockRunner) {
		if got := MinWindowWidth(); got != DefaultMinWindowWidth {
			t.Errorf("MinWindowWidth() with no tmux = %d, want %d", got, DefaultMinWindowWidth)
		}
	})
}

// The default has to leave room for what it lets in: the panel's own columns
// plus a work pane worth working in.
func TestDefaultMinWindowWidthLeavesRoomToWork(t *testing.T) {
	if work := DefaultMinWindowWidth - defaultPanelWidth; work < 80 {
		t.Errorf("a window at the minimum leaves the work pane %d columns, want at least 80", work)
	}
}

// One key both ways. The panel is somewhere you visit, so the binding that
// takes you there is the binding that brings you back.
func TestFocusPanel(t *testing.T) {
	// Focus is elsewhere: step into the panel.
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%5 mux watch\n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("0\n"), nil, "tmux", "display-message", "-p", "-t", "%5", "#{pane_active}")

		if err := FocusPanel("%3"); err != nil {
			t.Fatalf("FocusPanel: %v", err)
		}
		if !ran(m, "select-pane -t %5") {
			t.Errorf("did not select the panel: %v", m.runs)
		}
	})

	// The panel already has it: go back to wherever the user came from. Not
	// "the pane to the right" — select-pane -l returns to the actual previous
	// pane however many sit beside the panel.
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%5 mux watch\n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("1\n"), nil, "tmux", "display-message", "-p", "-t", "%5", "#{pane_active}")

		if err := FocusPanel("%3"); err != nil {
			t.Fatalf("FocusPanel: %v", err)
		}
		if !ran(m, "select-pane -l") {
			t.Errorf("did not step back out: %v", m.runs)
		}
	})

	// No panel in this window: nothing, and no error. The binding is global and
	// a failing run-shell writes to the status line on every press.
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")

		if err := FocusPanel("%3"); err != nil {
			t.Fatalf("FocusPanel with no panel: %v", err)
		}
		if ran(m, "select-pane") {
			t.Errorf("moved the focus with no panel to move it to: %v", m.runs)
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

// A session the tmux-project profile opened belongs to one VS Code window, and
// the tag says so from the moment the session exists. SessionOnlyInVSCode
// cannot: it inspects attached clients, and after-new-session fires before any
// client attaches — so it answered "not VS Code" and the panel went in for
// good, ensure semantics never taking it back out.
func TestTogglePanelAutoSkipsProjectSession(t *testing.T) {
	setup := func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("/home/u/dev/front\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@project_dir")
		// Nothing is attached — the case that defeated the client-based check.
		m.OnOutput([]byte(""), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
	}

	withMock(t, func(m *mockRunner) {
		setup(m)
		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if len(m.runs) != 0 {
			t.Errorf("ran %v, want nothing in a tmux-project session", m.runs)
		}
	})

	// The key still overrides it — that is a decision, not a default.
	withMock(t, func(m *mockRunner) {
		setup(m)
		if err := TogglePanel("%3", false); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want the explicit call to open a panel", m.runs)
		}
	})
}

// An untagged session is unaffected: the tag is evidence, its absence is not.
func TestTogglePanelAutoOpensInUntaggedSession(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@project_dir")
		m.OnOutput([]byte("27\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte("300\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
		old := clientEnvHas
		clientEnvHas = func(int, string) bool { return false }
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want a panel in an untagged, wide, non-VS Code window", m.runs)
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
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", "@mux_panel_min_width")
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
		{name: "one column short is still skipped", width: "139", auto: true, wantSplit: false},
		{name: "exactly the minimum is wide enough", width: "140", auto: true, wantSplit: true},
		// The regression: an ordinary Windows Terminal window was refused a panel
		// while the bar sat at 200, and a stand-down says nothing about why.
		{name: "an ordinary terminal window gets one", width: "188", auto: true, wantSplit: true},
		// A VS Code-sized window now clears the width bar. That case belongs to
		// SessionOnlyInVSCode, which inspects the client rather than guessing from
		// columns — see TestTogglePanelAutoSkipsVSCode.
		{name: "a VS Code-sized window passes on width alone", width: "150", auto: true, wantSplit: true},
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

// ghostList is what a window looks like once tmux-resurrect has restored it:
// the panel's pane is back, in its place and at its width, running a bare shell.
const ghostList = "%3 \n%9 cat '/home/u/.local/share/tmux/resurrect/restore/pane_contents//pane-work:0.0'; exec /usr/bin/zsh\n"

// mockGhostWindow sets up a restored window whose panel pane came back dead,
// with titles as resurrect leaves them — the panel's own restored verbatim.
func mockGhostWindow(m *mockRunner, titles string) {
	mockPanelWindow(m)
	m.OnOutput([]byte(ghostList), nil, "tmux", "list-panes", "-t", "@7",
		"-F", "#{pane_id} #{pane_start_command}")
	m.OnOutput([]byte(titles), nil, "tmux", "list-panes", "-t", "@7",
		"-F", "#{pane_id} #{pane_title}")
	m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
	m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
	m.OnOutput([]byte("27\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
	m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", "@mux_panel_min_width")
	m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
}

// tmux-resurrect cannot bring `mux watch` back — it records a pane's child
// process and the panel has none — so a restored window holds a 36-column shell
// where the panel was. Left alone, mux does not recognise it and opens a second
// panel beside it. The title is what survives the restore, and what says the
// pane can go.
func TestTogglePanelClosesARestoredGhost(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockGhostWindow(m, "%3 a-hostname\n%9 "+panelTitle+"\n")
		m.OnOutput([]byte("269\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		old := clientEnvHas
		clientEnvHas = func(int, string) bool { return false }
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "kill-pane -t %9") {
			t.Errorf("ran %v, want the restored pane closed", m.runs)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want a real panel opened in its place", m.runs)
		}
		// Order matters: splitting first would size the new pane against a window
		// the dead one is still taking 36 columns of.
		for _, r := range m.runs {
			if strings.Contains(r, "split-window") {
				t.Errorf("ran %v, want the kill before the split", m.runs)
			}
			if strings.Contains(r, "kill-pane") {
				break
			}
		}
	})
}

// This closes a pane. A near-miss must not be enough — the title is compared
// whole, never by substring.
func TestTogglePanelLeavesOtherTitlesAlone(t *testing.T) {
	for _, title := range []string{"a-hostname", panelTitle + " notes", "mux", ""} {
		t.Run(title, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				mockGhostWindow(m, "%3 a-hostname\n%9 "+title+"\n")
				m.OnOutput([]byte("269\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
				old := clientEnvHas
				clientEnvHas = func(int, string) bool { return false }
				defer func() { clientEnvHas = old }()

				if err := TogglePanel("%3", true); err != nil {
					t.Fatalf("TogglePanel: %v", err)
				}
				if ran(m, "kill-pane") {
					t.Errorf("ran %v, want no pane closed for title %q", m.runs, title)
				}
			})
		})
	}
}

// A window mux will not put a panel in is the window that wants those columns
// back most, so the dead pane goes whether or not a live one replaces it.
func TestTogglePanelClosesTheGhostEvenWhenItWillNotOpen(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockGhostWindow(m, "%3 a-hostname\n%9 "+panelTitle+"\n")
		m.OnOutput([]byte("54\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		old := clientEnvHas
		clientEnvHas = func(int, string) bool { return false }
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if !ran(m, "kill-pane -t %9") {
			t.Errorf("ran %v, want the dead pane closed in a narrow window too", m.runs)
		}
		if ran(m, "split-window") {
			t.Errorf("ran %v, want no panel in a 54-column window", m.runs)
		}
	})
}

// An exact title is not proof of a ghost. resurrect restores titles verbatim,
// so a user who kept working in the restored shell owns a pane named exactly
// like the panel — and when it is all the window has, killing it kills the
// session. The pane keeps its life and loses its title, and the real panel
// opens beside it.
func TestTogglePanelUntitlesAGhostThatIsTheOnlyPane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 cat '/home/u/.local/share/tmux/resurrect/restore/pane_contents//pane-work:0.0'; exec /usr/bin/zsh\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("%3 "+panelTitle+"\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_title}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-wqv", "-t", "@7", "@mux_panel_off")
		m.OnOutput([]byte("work\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{session_name}")
		m.OnOutput([]byte("27\n"), nil, "tmux", "list-clients", "-t", "work", "-F", "#{client_pid}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", "@mux_panel_min_width")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-qv", "-t", "work", "@mux_panel_width")
		m.OnOutput([]byte("269\n"), nil, "tmux", "display-message", "-p", "-t", "%3", "#{window_width}")
		old := clientEnvHas
		clientEnvHas = func(int, string) bool { return false }
		defer func() { clientEnvHas = old }()

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		if ran(m, "kill-pane") {
			t.Errorf("ran %v, want the window's only pane left alive", m.runs)
		}
		if !ran(m, "select-pane -t %3 -T") {
			t.Errorf("ran %v, want the adopted pane's title cleared", m.runs)
		}
		if !ran(m, "split-window") {
			t.Errorf("ran %v, want a real panel opened beside it", m.runs)
		}
	})
}

// The hooks fire constantly and almost always find a live panel. That path must
// stay at one list-panes: the ghost lookup is a second one, and it only belongs
// on the path that is about to create a pane anyway.
func TestTogglePanelDoesNotLookForAGhostWhenThePanelIsThere(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%9 /home/u/.local/bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")

		if err := TogglePanel("%3", true); err != nil {
			t.Fatalf("TogglePanel: %v", err)
		}
		for _, g := range m.gets {
			if strings.Contains(g, "#{pane_title}") {
				t.Errorf("asked %v, want no title lookup when a panel is already there", m.gets)
			}
		}
	})
}

// The mark itself. `select-pane -T` sets the title and returns — it must not be
// the thing that makes the panel active.
func TestMarkPanelPane(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := MarkPanelPane("%9"); err != nil {
			t.Fatalf("MarkPanelPane: %v", err)
		}
		if want := "tmux select-pane -t %9 -T " + panelTitle; m.runs[0] != want {
			t.Errorf("ran %q, want %q", m.runs[0], want)
		}
	})

	withMock(t, func(m *mockRunner) {
		if err := MarkPanelPane(""); err != nil {
			t.Fatalf("MarkPanelPane: %v", err)
		}
		if want := "tmux select-pane -T " + panelTitle; m.runs[0] != want {
			t.Errorf("ran %q, want %q", m.runs[0], want)
		}
	})
}

// The panel is steered by send-keys rather than by being focused: focusing it
// would take the keyboard away from the pane the user is typing in, which is
// the one thing an always-visible sidebar must not cost.
func TestNavPanel(t *testing.T) {
	mockPanel := func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n%9 /home/u/.local/bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@7", "-F", "#{pane_id} #{pane_start_command}")
	}

	for direction, key := range map[string]string{
		"up": "Up", "down": "Down", "top": "Home", "bottom": "End", "enter": "Enter",
	} {
		t.Run(direction, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				mockPanel(m)
				if err := NavPanel("%3", direction); err != nil {
					t.Fatalf("NavPanel(%q): %v", direction, err)
				}
				want := "tmux send-keys -t %9 " + key
				if !ran(m, want) {
					t.Errorf("ran %v, want %q", m.runs, want)
				}
				// select-pane would defeat the whole point.
				if ran(m, "select-pane") {
					t.Errorf("ran %v, want the focus left alone", m.runs)
				}
			})
		})
	}
}

// The binding is global and most windows have no panel. Failing there would put
// a message on the status line every time the key is pressed.
func TestNavPanelWithoutAPanel(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockPanelWindow(m)
		m.OnOutput([]byte("%3 \n"), nil, "tmux", "list-panes", "-t", "@7",
			"-F", "#{pane_id} #{pane_start_command}")

		if err := NavPanel("%3", "down"); err != nil {
			t.Fatalf("NavPanel on a window with no panel: %v", err)
		}
		if ran(m, "send-keys") {
			t.Errorf("ran %v, want nothing sent", m.runs)
		}
	})
}

// A direction that is not one of the five is a typo in the user's tmux.conf,
// and silently doing nothing would leave them pressing a dead key.
func TestNavPanelRejectsUnknownDirection(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := NavPanel("%3", "sideways"); err == nil {
			t.Error("an unknown direction was accepted")
		}
		if len(m.runs) != 0 {
			t.Errorf("ran %v, want nothing before the direction was checked", m.runs)
		}
	})
}

// select-pane -l is only ever right when this pane holds the focus. On the key
// path the panel is never made active, and restoring there selects whatever the
// window visited before the pane the user is typing in.
func TestPaneActive(t *testing.T) {
	tests := []struct {
		out  string
		want bool
	}{
		{"1\n", true},
		{"0\n", false},
		{"\n", false},
	}
	for _, tt := range tests {
		withMock(t, func(m *mockRunner) {
			m.OnOutput([]byte(tt.out), nil, "tmux", "display-message", "-p", "-t", "%9", "#{pane_active}")
			if got := PaneActive("%9"); got != tt.want {
				t.Errorf("PaneActive with %q = %v, want %v", tt.out, got, tt.want)
			}
		})
	}

	// An unanswerable question is not a yes: guessing active would fire the
	// select-pane this guard exists to prevent.
	withMock(t, func(m *mockRunner) {
		if PaneActive("%9") {
			t.Error("an unreadable pane reported active")
		}
	})
}

// Off unless asked for, and only for values a person would plausibly write.
// A typo must leave the panel as it was rather than putting back chrome the
// user turned off.
func TestPanelHeaderEnabled(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"on", true},
		{"On", true},
		{"1", true},
		{"true", true},
		{"yes", true},
		{" on \n", true},
		{"off", false},
		{"0", false},
		{"banana", false},
	}
	for _, tc := range tests {
		withMock(t, func(m *mockRunner) {
			m.OnOutput([]byte(tc.value), nil, "tmux", "show-options", "-gqv", panelHeaderOption)
			if got := PanelHeaderEnabled(); got != tc.want {
				t.Errorf("value %q: enabled=%v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// A tmux server that will not answer is not a reason to draw chrome.
func TestPanelHeaderEnabledOnError(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if PanelHeaderEnabled() {
			t.Error("an unanswered show-options turned the header on")
		}
	})
}

// Both facts come from one call because they must describe the same instant.
func TestWindowShapeReadsWidthAndPanes(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("237 3\n"), nil, "tmux", "display-message", "-p", "-t", "%3",
			"#{window_width} #{window_panes}")

		w, p, err := WindowShape("%3")
		if err != nil {
			t.Fatalf("WindowShape: %v", err)
		}
		if w != 237 || p != 3 {
			t.Errorf("got %dx%d panes, want 237/3", w, p)
		}
	})
}

// tmux prints a bare space for a pane that has gone away, and the panel must
// read that as "could not tell" rather than as a zero-pane window.
func TestWindowShapeRefusesAPartialAnswer(t *testing.T) {
	for _, out := range []string{" \n", "237\n", "\n", "237 x\n"} {
		withMock(t, func(m *mockRunner) {
			m.OnOutput([]byte(out), nil, "tmux", "display-message", "-p", "-t", "%3",
				"#{window_width} #{window_panes}")

			if _, _, err := WindowShape("%3"); err == nil {
				t.Errorf("output %q was accepted as a shape", out)
			}
		})
	}
}
