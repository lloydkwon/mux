package ui

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// recordRunner records every tmux command the panel runs, and answers the
// lookups it makes on the way. It is a `ui`-side mock because tmux's own is
// unexported — `tmux.CommandRunner` and `tmux.SetRunner` are the seam.
type recordRunner struct {
	mu   sync.Mutex
	runs [][]string
	out  map[string]string
}

func (r *recordRunner) Output(name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []byte(r.out[strings.Join(append([]string{name}, args...), " ")]), nil
}

func (r *recordRunner) Run(name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, append([]string{name}, args...))
	return nil
}

func (r *recordRunner) ran(want ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.runs {
		if len(got) < len(want) {
			continue
		}
		match := true
		for i, w := range want {
			if got[i] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// withRecorder installs the recorder for one test, with the pane lookups
// already answered.
func withRecorder(t *testing.T) *recordRunner {
	t.Helper()
	t.Setenv("TMUX_PANE", "%9")
	r := &recordRunner{out: map[string]string{
		"tmux display-message -p -t %9 #{session_name}":                   "work\n",
		"tmux display-message -p -t %9 #{pane_active}":                    "0\n",
		"tmux display-message -p -t %9 #{window_id} #{pane_current_path}": "@7 /work\n",
	}}
	tmux.SetRunner(r)
	t.Cleanup(func() { tmux.SetRunner(nil) })
	return r
}

func focusTestModel() watchModel {
	m := watchTestModel(48, 30)
	m.sessions = []tmux.Session{sess("mux", tmux.AIStateWorking)}
	return m
}

// The focus key is a round trip, and esc is the other half of it: having
// stepped in and looked, leaving without choosing must be one key.
func TestEscapeLeavesTheFocusedPanel(t *testing.T) {
	r := withRecorder(t)
	r.out["tmux display-message -p -t %9 #{pane_active}"] = "1\n" // focused
	m := focusTestModel()
	m = m.reselect()
	before := m.selected

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc did nothing")
	}
	cmd()

	if !r.ran("tmux", "select-pane", "-l", "-t", "%9") {
		t.Errorf("ran %v, want the focus handed back", r.runs)
	}
	if got := updated.(watchModel).selected; got != before {
		t.Errorf("esc moved the cursor to %+v — it should only leave", got)
	}
}

// Keys can also arrive by send-keys, where the panel never held the focus. The
// guard on restoreFocus is what keeps esc from selecting some unrelated pane
// there, so it has to stay in force on this path too.
func TestEscapeDoesNothingWhenNotFocused(t *testing.T) {
	r := withRecorder(t) // pane_active is "0" by default
	m := focusTestModel()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc did nothing at all")
	}
	cmd()

	if r.ran("tmux", "select-pane") {
		t.Errorf("esc moved the focus from a pane that did not have it: %v", r.runs)
	}
}
