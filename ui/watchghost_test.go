package ui

import (
	"testing"
	"time"
)

// The zero value is off, and that is what keeps `go test ./ui` from
// disconnecting the developer's own terminal: every watchModel built by hand in
// these tests carries no threshold. Deleting this guard would make the whole
// package hostile to run.
func TestGhostCheckIsOffWithoutAThreshold(t *testing.T) {
	if _, ok := (watchModel{}).dueForGhostCheck(time.Now()); ok {
		t.Fatal("a zero threshold must disable the check")
	}
}

func TestGhostCheckWaitsOutItsInterval(t *testing.T) {
	now := time.Now()
	m := watchModel{ghostIdle: 30 * time.Minute}

	m, ok := m.dueForGhostCheck(now)
	if !ok {
		t.Fatal("the first check should run")
	}
	if _, ok := m.dueForGhostCheck(now.Add(ghostCheckInterval - time.Second)); ok {
		t.Fatal("ran again inside the interval")
	}
	if _, ok := m.dueForGhostCheck(now.Add(ghostCheckInterval + time.Second)); !ok {
		t.Fatal("did not run after the interval")
	}
}

// The tick is what the panel does every two seconds. It must not reach tmux for
// this on a model that has the check off — the assertion is simply that the
// stamp never moves.
func TestTickDoesNotArmTheCheckWhenItIsOff(t *testing.T) {
	m := watchModel{width: 40, height: 20}
	got, _ := m.Update(watchTickMsg(time.Now()))
	if wm, ok := got.(watchModel); ok && !wm.lastGhostCheck.IsZero() {
		t.Fatal("the check armed itself with no threshold set")
	}
}

func TestGhostDetachedMsgLandsInTheLocalLog(t *testing.T) {
	m := watchModel{width: 40, height: 20}
	got, _ := m.Update(ghostDetachedMsg{tty: "/dev/pts/22", session: "ebbt", size: "269x69"})
	wm, ok := got.(watchModel)
	if !ok {
		t.Fatal("not a watchModel")
	}
	if len(wm.local) != 1 {
		t.Fatalf("want one local event, got %d", len(wm.local))
	}
	// Shared would draw this line in every window on the server for something
	// one pane did.
	if len(wm.shared) != 0 {
		t.Fatalf("must not touch the shared log, got %+v", wm.shared)
	}
	if wm.local[0].session != "ebbt" {
		t.Errorf("event should name the ghost's session, got %q", wm.local[0].session)
	}
}
