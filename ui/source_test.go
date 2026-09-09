package ui

import (
	"errors"
	"testing"

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
