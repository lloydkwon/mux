package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// fakeRunner lets a ui test drive tmux.SwitchClient without touching the
// developer's own server. The tmux package's own mock is not exported, and the
// CommandRunner interface is — which is the seam meant for exactly this.
type fakeRunner struct {
	runs   []string
	runErr error
}

func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	// "0" is what PaneActive reads as "not active", which keeps restoreFocus
	// from issuing a select-pane during these tests.
	return []byte("0"), nil
}

func (f *fakeRunner) Run(name string, args ...string) error {
	f.runs = append(f.runs, name+" "+strings.Join(args, " "))
	return f.runErr
}

// withFakeTmux points the tmux package at a fake and restores the real one.
func withFakeTmux(t *testing.T, f *fakeRunner) {
	t.Helper()
	// selfPane reads this; an empty pane id keeps restoreFocus off a real one.
	t.Setenv("TMUX_PANE", "")
	tmux.SetRunner(f)
	t.Cleanup(func() { tmux.SetRunner(nil) })
}

// withFakeEditor replaces the launcher and records what it was asked to open.
func withFakeEditor(t *testing.T, err error) *[]string {
	t.Helper()
	var calls []string
	prev := openEditor
	openEditor = func(bin, dir string) error {
		calls = append(calls, bin+" "+dir)
		return err
	}
	t.Cleanup(func() { openEditor = prev })
	return &calls
}

func editorTestModel(bin string) watchModel {
	m := watchTestModel(48, 20)
	tagged := sess("api", tmux.AIStateWorking)
	tagged.Directory = "/home/u/wandered"
	tagged.ProjectDir = "/home/u/projects/api"
	plain := sess("mux", tmux.AIStateWorking)
	plain.Directory = "/home/u/projects/mux"
	m.sessions = []tmux.Session{tagged, plain}
	m.editorBin = bin
	return m
}

// ProjectDir over Directory is the whole point: Directory follows the active
// pane and is overwritten by the AI's own cwd, so on a session whose shell has
// wandered it names somewhere that is not the project.
func TestEditorDirPrefersProjectDir(t *testing.T) {
	m := editorTestModel("/usr/bin/code")

	if got := m.editorDirFor("api"); got != "/home/u/projects/api" {
		t.Errorf("tagged session opened %q, want its @project_dir", got)
	}
	// No tag is not evidence of anything, so the pane's directory stands.
	if got := m.editorDirFor("mux"); got != "/home/u/projects/mux" {
		t.Errorf("untagged session opened %q, want its directory", got)
	}
	// A session the panel does not know cannot name a folder.
	if got := m.editorDirFor("gone"); got != "" {
		t.Errorf("unknown session opened %q, want nothing", got)
	}
}

// editorBin is the only switch. Empty means the option is off or no editor was
// found, and both must reach the same place: nothing opens.
func TestEditorDirIsEmptyWithoutBin(t *testing.T) {
	m := editorTestModel("")
	if got := m.editorDirFor("api"); got != "" {
		t.Errorf("editorDirFor = %q with no editor, want empty", got)
	}
}

// The zero-value model must not launch anything — that is what every existing
// test builds, and what keeps `go test ./ui` off the developer's desktop.
func TestRightClickWithoutEditorOpensNothing(t *testing.T) {
	f := &fakeRunner{}
	withFakeTmux(t, f)
	calls := withFakeEditor(t, nil)

	m := editorTestModel("")
	m = m.reselect()
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if cmd == nil {
		t.Fatal("the right click issued no command")
	}
	cmd()

	if len(*calls) != 0 {
		t.Errorf("opened an editor with no editor found: %v", *calls)
	}
}

func leftPress(y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func rightPress(y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress}
}

// The two buttons are two different answers, and neither may do the other's
// job: a left click that raised a window would be the surprise this split
// exists to remove, and a right click that moved the client would take the user
// out of the terminal they asked to stay in.
func TestLeftSwitchesAndRightOpensEditor(t *testing.T) {
	t.Run("left switches and opens nothing", func(t *testing.T) {
		f := &fakeRunner{}
		withFakeTmux(t, f)
		calls := withFakeEditor(t, nil)

		m := editorTestModel("/usr/bin/code")
		m.selected = "demo"
		_, cmd := m.Update(leftPress(firstSessionRow(m)))
		if cmd == nil {
			t.Fatal("the left click issued no command")
		}
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message: %#v", msg)
		}

		if len(*calls) != 0 {
			t.Errorf("a left click opened an editor: %v", *calls)
		}
		if len(f.runs) == 0 || !strings.Contains(f.runs[0], "switch-client") {
			t.Errorf("tmux calls = %v, want a switch-client", f.runs)
		}
	})

	t.Run("right opens the editor and does not switch", func(t *testing.T) {
		f := &fakeRunner{}
		withFakeTmux(t, f)
		calls := withFakeEditor(t, nil)

		m := editorTestModel("/usr/bin/code")
		_, cmd := m.Update(rightPress(firstSessionRow(m)))
		if cmd == nil {
			t.Fatal("the right click issued no command")
		}
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message: %#v", msg)
		}

		want := "/usr/bin/code /home/u/projects/api"
		if len(*calls) != 1 || (*calls)[0] != want {
			t.Errorf("editor calls = %v, want [%q]", *calls, want)
		}
		for _, r := range f.runs {
			if strings.Contains(r, "switch-client") {
				t.Errorf("a right click switched sessions: %v", f.runs)
			}
		}
	})
}

// The keyboard is the left click's twin, and `mux nav enter` arrives as this
// exact key. There is no right button to press here, so enter must not grow the
// editor's job.
func TestEnterSwitchesOnly(t *testing.T) {
	f := &fakeRunner{}
	withFakeTmux(t, f)
	calls := withFakeEditor(t, nil)

	m := editorTestModel("/usr/bin/code")
	m.selected = "api"
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter issued no command")
	}
	cmd()

	if len(*calls) != 0 {
		t.Errorf("enter opened an editor: %v", *calls)
	}
	if len(f.runs) == 0 || !strings.Contains(f.runs[0], "switch-client") {
		t.Errorf("tmux calls = %v, want a switch-client", f.runs)
	}
}

// A release carries the same button under SGR — measured, a right click arrives
// as press then release — so acting on both would open two windows per click.
func TestOnlyPressesAct(t *testing.T) {
	f := &fakeRunner{}
	withFakeTmux(t, f)
	calls := withFakeEditor(t, nil)

	m := editorTestModel("/usr/bin/code")
	row := firstSessionRow(m)
	for _, msg := range []tea.MouseMsg{
		{Y: row, Button: tea.MouseButtonRight, Action: tea.MouseActionRelease},
		{Y: row, Button: tea.MouseButtonRight, Action: tea.MouseActionMotion},
	} {
		if _, cmd := m.Update(msg); cmd != nil {
			cmd()
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a release or a drag opened an editor: %v", *calls)
	}
}

// The launch failing is the case that must not be silent — under WSL a stale
// WSL_INTEROP makes every /mnt/c launch fail, and fire-and-forget would leave
// the click looking like it did nothing.
func TestEditorFailureIsLoggedLocally(t *testing.T) {
	f := &fakeRunner{}
	withFakeTmux(t, f)
	withFakeEditor(t, errRefreshTest)

	m := editorTestModel("/usr/bin/code")
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	msg := cmd()

	failed, ok := msg.(editorFailedMsg)
	if !ok {
		t.Fatalf("msg = %#v, want editorFailedMsg", msg)
	}

	updated, _ := m.Update(failed)
	got := updated.(watchModel)
	if len(got.events) != 1 || !strings.Contains(got.events[0].text, "VS Code 실행 실패") {
		t.Fatalf("events = %v, want a launch failure", got.events)
	}
	// Local, not shared: this pane's launch failed, which is not news in anyone
	// else's window.
	if len(got.shared) != 0 {
		t.Errorf("the failure reached the shared log: %v", got.shared)
	}
}

func writeFakeCode(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "code")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// PATH first, because that is where an install that wants to be found puts
// itself — WSL's Windows install included.
func TestFindEditorPrefersPath(t *testing.T) {
	onPath := writeFakeCode(t, t.TempDir())
	elsewhere := writeFakeCode(t, t.TempDir())

	t.Setenv("PATH", filepath.Dir(onPath))
	prev := editorCandidates
	editorCandidates = func() []string { return []string{elsewhere} }
	t.Cleanup(func() { editorCandidates = prev })

	if got := findEditor(); got != onPath {
		t.Errorf("findEditor = %q, want the one on PATH (%q)", got, onPath)
	}
}

// A snap, a flatpak, an unpacked tarball and a macOS bundle can all be installed
// with nothing named `code` on PATH. Reporting "not installed" there would be
// wrong about an application the user is looking at.
func TestFindEditorFallsBackToCandidates(t *testing.T) {
	installed := writeFakeCode(t, t.TempDir())

	t.Setenv("PATH", t.TempDir()) // empty directory: nothing to find
	prev := editorCandidates
	editorCandidates = func() []string { return []string{"/nope/code", installed} }
	t.Cleanup(func() { editorCandidates = prev })

	if got := findEditor(); got != installed {
		t.Errorf("findEditor = %q, want %q", got, installed)
	}
}

// Nothing found stands the feature down; it must not return a path that is not
// there for RunWatch to hand to exec.
func TestFindEditorEmptyWhenAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prev := editorCandidates
	editorCandidates = func() []string { return []string{"/nope/code"} }
	t.Cleanup(func() { editorCandidates = prev })

	if got := findEditor(); got != "" {
		t.Errorf("findEditor = %q, want empty", got)
	}
}

func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()
	exe := writeFakeCode(t, dir)

	plain := filepath.Join(dir, "readme")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		want bool
	}{
		{exe, true},
		{plain, false}, // present but not runnable
		{dir, false},   // a directory with the right name is not a binary
		{dir + "/missing", false},
	} {
		if got := isExecutableFile(tc.path); got != tc.want {
			t.Errorf("isExecutableFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// The panel row is about forty cells wide; a multi-line error there costs the
// whole log.
func TestFirstLine(t *testing.T) {
	if got := firstLine("boom\nstack\nmore"); got != "boom" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("boom"); got != "boom" {
		t.Errorf("firstLine = %q", got)
	}
}
