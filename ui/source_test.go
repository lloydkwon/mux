package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/lloydkwon/mux/tmux"
)

// fakeSource hands back what it was given. It is the first mock the ui package
// has had, and it is safe to add because it calls nothing — a test using it
// reaches neither tmux nor herdr.
type fakeSource struct {
	sessions []tmux.Session
	err      error
	captured string
	// asked records the preview keys, so a test can check the tree resolves to
	// the pane it means.
	asked []previewKey
}

func (f *fakeSource) Sessions() ([]tmux.Session, error) { return f.sessions, f.err }

func (f *fakeSource) Windows(session string) ([]tmux.Window, error) {
	for _, s := range f.sessions {
		if s.Name == session {
			return s.Windows, nil
		}
	}
	return nil, nil
}

func (f *fakeSource) Panes(string, int) ([]tmux.Pane, error) { return nil, nil }

func (f *fakeSource) Capture(key previewKey) (string, error) {
	f.asked = append(f.asked, key)
	return f.captured, nil
}

// The whole compatibility story in one test: a Model nobody told about a source
// is the tmux Model it always was, and it can still change things.
func TestZeroModelIsTmuxAndWritable(t *testing.T) {
	var m Model
	if m.readOnly {
		t.Error("a zero Model came out read-only")
	}
	if _, ok := m.source().(tmuxSource); !ok {
		t.Errorf("a nil source resolved to %T, want tmuxSource", m.source())
	}
	if got := m.extraBar(); got != "" {
		t.Errorf("extraBar = %q, want empty for a plain tmux Model", got)
	}
}

func TestSourceLoadsSessionsFromTheSourceItWasGiven(t *testing.T) {
	fake := &fakeSource{sessions: []tmux.Session{
		{Name: "alpha"}, {Name: "beta"},
	}}
	m := Model{tree: newTreeState(), src: fake}

	msg := m.loadSessionsCmd()()
	loaded, ok := msg.(sessionsLoadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want sessionsLoadedMsg", msg)
	}
	if len(loaded.sessions) != 2 || loaded.sessions[0].Name != "alpha" {
		t.Errorf("sessions = %v, want the source's own", names(loaded.sessions))
	}
}

// A source that cannot answer must reach m.err, where extraBar draws it — the
// same path a missing tmux binary already takes. Nothing new is needed for
// herdr being down; this pins that.
func TestSourceErrorReachesTheErrorBar(t *testing.T) {
	fake := &fakeSource{err: errors.New("herdr workspace: no server")}
	m := Model{tree: newTreeState(), src: fake}

	msg := m.loadSessionsCmd()()
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if m.err == nil {
		t.Fatal("the source error was dropped")
	}
	if got := m.extraBar(); got == "" {
		t.Error("the error bar drew nothing for a source that is down")
	}
}

func TestSourcePreviewGoesThroughTheSource(t *testing.T) {
	fake := &fakeSource{captured: "hello from herdr"}
	m := Model{tree: newTreeState(), src: fake}

	msg := m.refreshPreviewCmd(previewKey{session: "alpha", window: 2, pane: -1})()
	loaded, ok := msg.(previewLoadedMsg)
	if !ok {
		t.Fatalf("msg = %T, want previewLoadedMsg", msg)
	}
	if loaded.content != "hello from herdr" {
		t.Errorf("content = %q, want the source's", loaded.content)
	}
	if len(fake.asked) != 1 || fake.asked[0].window != 2 {
		t.Errorf("asked = %v, want the key it was handed", fake.asked)
	}
}

// A read-only list opens showing its agents: those rows are the reason to look
// at it, and six collapsed chevrons ask the user to press tab six times for
// what they came for.
func TestReadOnlyListOpensExpanded(t *testing.T) {
	fake := &fakeSource{sessions: []tmux.Session{
		{Name: "alpha", Windows: []tmux.Window{{Index: 1, Name: "exec"}}},
	}}
	m := NewHerdrModel()
	m.src = fake

	updated, _ := m.Update(m.loadSessionsCmd()())
	m = updated.(Model)

	if !m.tree.isSessionExpanded("alpha") {
		t.Fatal("the list opened collapsed")
	}
	// The windows arrived with the session list, so the subtree has content on
	// the very first frame rather than a tick later.
	var windows int
	for _, it := range m.items {
		if it.kind == itemWindow {
			windows++
		}
	}
	if windows != 1 {
		t.Errorf("window rows = %d, want the seeded one on the first frame", windows)
	}
}

// Seen-once, not always: a subtree the user collapsed must stay collapsed.
func TestAutoExpandDoesNotUndoACollapse(t *testing.T) {
	fake := &fakeSource{sessions: []tmux.Session{{Name: "alpha"}}}
	m := NewHerdrModel()
	m.src = fake

	updated, _ := m.Update(m.loadSessionsCmd()())
	m = updated.(Model)
	m.tree.setSessionExpanded("alpha", false)

	updated, _ = m.Update(m.loadSessionsCmd()())
	m = updated.(Model)

	if m.tree.isSessionExpanded("alpha") {
		t.Error("the next tick re-opened a subtree the user had collapsed")
	}
}

// A tmux Model must not start expanding things on its own.
func TestTmuxListStillOpensCollapsed(t *testing.T) {
	fake := &fakeSource{sessions: []tmux.Session{{Name: "alpha"}}}
	m := Model{tree: newTreeState(), src: fake} // no readOnly

	updated, _ := m.Update(m.loadSessionsCmd()())
	m = updated.(Model)

	if m.tree.isSessionExpanded("alpha") {
		t.Error("a writable list expanded itself")
	}
}

// readOnlyModel is a herdr-shaped Model with one session under the cursor.
func readOnlyModel(t *testing.T) Model {
	t.Helper()
	fake := &fakeSource{sessions: []tmux.Session{
		{Name: "alpha", Windows: []tmux.Window{{Index: 1, Name: "exec"}}},
	}}
	m := NewHerdrModel()
	m.src = fake
	m.width, m.height = 100, 24
	updated, _ := m.Update(m.loadSessionsCmd()())
	return updated.(Model)
}

// The guard lives in activateCurrent alone, which enter and the double click
// share. A second guard in cmd/mux is exactly the drift that sharing prevents.
func TestReadOnlyEnterDoesNotAttach(t *testing.T) {
	m := readOnlyModel(t)
	updated, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)

	if got.AttachName() != "" {
		t.Errorf("attach target = %q, want none", got.AttachName())
	}
	if cmd != nil {
		t.Error("enter returned a command; read-only enter must do nothing")
	}
}

// Each of these opens a mode that would change tmux. The mode must not open at
// all — refusing later, inside the sub-model, would let the user type a name
// first and be told no afterwards.
func TestReadOnlyRefusesTheWriteKeys(t *testing.T) {
	for _, key := range []rune{'n', 'x', 'r', 'm'} {
		m := readOnlyModel(t)
		updated, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if got := updated.(Model); got.mode != modeList {
			t.Errorf("%q opened mode %v, want the list to stay put", key, got.mode)
		}
	}
}

// "New shell" detaches and "New tmux session" creates. Both are writes, and in
// herdr mode neither has anything to act on.
func TestReadOnlyDropsTheActionRows(t *testing.T) {
	m := readOnlyModel(t)
	for _, it := range m.items {
		if it.kind == itemNewShell || it.kind == itemNewSession {
			t.Fatalf("an action row survived into the read-only list: %v", it.kind)
		}
	}
}

// A key that silently does nothing is indistinguishable from a broken program,
// so the bar says why — permanently, because a bar that comes and goes moves
// chromeTop and shifts every click target by a row.
func TestReadOnlySaysSoOnScreen(t *testing.T) {
	m := readOnlyModel(t)
	if got := m.extraBar(); got == "" {
		t.Fatal("nothing on screen explains why the write keys do nothing")
	}
	if !strings.Contains(m.titleBar(), "herdr") {
		t.Error("the title still claims to be listing tmux sessions")
	}
	// The help bar must not advertise keys the guards refuse.
	help := ansi.Strip(renderHelp(true))
	for _, gone := range []string{"attach", "new", "kill", "rename", "memo"} {
		if strings.Contains(help, gone) {
			t.Errorf("help still offers %q in read-only mode", gone)
		}
	}
	if !strings.Contains(help, "filter") {
		t.Error("help dropped a key that still works")
	}
}
