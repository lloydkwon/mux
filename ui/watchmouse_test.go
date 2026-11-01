package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// nil is off, and that is what keeps `go test ./ui` from asking the developer's
// own server: no hand-built watchModel carries a probe.
func TestMouseCheckIsOffWithoutAProbe(t *testing.T) {
	if cmd := mouseCheckCmd(nil); cmd != nil {
		t.Fatal("a nil probe must give no command")
	}
	if cmd := (watchModel{}).Init(); cmd == nil {
		t.Fatal("Init still has its own commands")
	}
	m := watchModel{width: 40, height: 20}
	got, _ := m.Update(watchTickMsg(time.Now()))
	if wm := got.(watchModel); !wm.lastMouseCheck.IsZero() {
		t.Fatal("the tick armed a check with no probe")
	}
}

func TestMouseCheckWaitsOutItsInterval(t *testing.T) {
	now := time.Now()
	m := watchModel{probeMouse: func() bool { return false }}

	m, ok := m.dueForMouseCheck(now)
	if !ok {
		t.Fatal("the first check should run")
	}
	if _, ok := m.dueForMouseCheck(now.Add(ghostCheckInterval - time.Second)); ok {
		t.Fatal("ran again inside the interval")
	}
	if _, ok := m.dueForMouseCheck(now.Add(ghostCheckInterval + time.Second)); !ok {
		t.Fatal("did not run after the interval")
	}
}

// The command reports the option *off*, which is what the hint is about.
func TestMouseCheckCmdInvertsTheProbe(t *testing.T) {
	msg := mouseCheckCmd(func() bool { return false })()
	if got, ok := msg.(mouseStateMsg); !ok || !got.off {
		t.Fatalf("mouse disabled should report off, got %#v", msg)
	}
	msg = mouseCheckCmd(func() bool { return true })()
	if got, ok := msg.(mouseStateMsg); !ok || got.off {
		t.Fatalf("mouse enabled should not report off, got %#v", msg)
	}
}

// The hint goes on top, belongs to no session, and moves every row under it
// down by exactly one — so a click still lands on the session drawn there.
func TestMouseHintKeepsClicksOnTheirRows(t *testing.T) {
	m := watchTestModel(40, 30)
	m.sessions = []tmux.Session{sess("web", tmux.AIStateReady), {Name: "shell"}}

	plain := m.sessionLines()
	order := strings.Join(m.sessionOrder(), ",")
	m, _ = updateWatch(m, mouseStateMsg{off: true})
	hinted := m.sessionLines()

	if len(hinted) != len(plain)+1 {
		t.Fatalf("hint should add one row: %d → %d", len(plain), len(hinted))
	}
	if hinted[0].session != "" {
		t.Fatalf("hint row belongs to %q", hinted[0].session)
	}
	if !strings.Contains(hinted[0].text, "set -g mouse on") {
		t.Fatalf("hint row should name the fix: %q", hinted[0].text)
	}
	for i := range plain {
		if got, want := m.sessionAtRow(i+1), plain[i].session; got != want {
			t.Errorf("row %d clicks to %q, want %q", i+1, got, want)
		}
	}
	if got := strings.Join(m.sessionOrder(), ","); got != order {
		t.Errorf("the cursor order changed: %q → %q", order, got)
	}

	// And it goes away when the option comes back on.
	m, _ = updateWatch(m, mouseStateMsg{off: false})
	if len(m.sessionLines()) != len(plain) {
		t.Fatal("hint outlived mouse on")
	}
}

func updateWatch(m watchModel, msg any) (watchModel, bool) {
	got, _ := m.Update(msg)
	wm, ok := got.(watchModel)
	return wm, ok
}
