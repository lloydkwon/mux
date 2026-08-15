package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

// The reported bug: a panel created just now observes nothing on its first tick
// — every session is "seen for the first time" — so it used to open on 아직 없음
// beside panels that had been up for hours. It must draw the shared log instead.
func TestWatchDrawsSharedLogItNeverObserved(t *testing.T) {
	m := watchTestModel(48, 30)
	m.sessions = []tmux.Session{sess("api", tmux.AIStateReady)}

	if got := len(m.events); got != 0 {
		t.Fatalf("a fresh model already has %d events", got)
	}

	updated, _ := m.Update(eventsMergedMsg{events: []aiEvent{{
		at: time.Now().Add(-time.Minute), session: "api", text: "✅ 작업 완료",
		state: tmux.AIStateReady,
	}}})

	out := ansi.Strip(updated.(watchModel).View())
	if !strings.Contains(out, "✅ 작업 완료") {
		t.Errorf("the panel did not draw an event it had not observed itself:\n%s", out)
	}
	if strings.Contains(out, "아직 없음") {
		t.Errorf("the panel still reports an empty log:\n%s", out)
	}
}

// A failed click is true of this pane only. Putting it in the shared store would
// print a line nobody else can act on in every window on the server.
func TestWatchSwitchFailureStaysLocal(t *testing.T) {
	m := watchTestModel(48, 30)
	m.sessions = []tmux.Session{sess("api", tmux.AIStateReady)}

	updated, _ := m.Update(switchFailedMsg{session: "api", err: errors.New("boom")})
	w := updated.(watchModel)

	if len(w.shared) != 0 {
		t.Errorf("a failed click reached the shared log: %v", w.shared)
	}
	if len(w.local) != 1 {
		t.Fatalf("local events = %d, want 1", len(w.local))
	}
	if !strings.Contains(ansi.Strip(w.View()), "전환 실패") {
		t.Error("the failure was not drawn")
	}
}

// The shared log is replaced wholesale on every merge, so the row a panel drew
// optimistically from its own observation cannot survive next to the store's
// copy of the same transition.
func TestWatchMergeReplacesTheOptimisticRow(t *testing.T) {
	at := time.Now()
	m := watchTestModel(48, 30)
	m.shared = []aiEvent{{at: at, session: "api", text: "✅ 작업 완료", state: tmux.AIStateReady}}
	m.events = m.shared

	updated, _ := m.Update(eventsMergedMsg{events: []aiEvent{{
		at: at.Add(-2 * time.Second), session: "api", text: "✅ 작업 완료",
		state: tmux.AIStateReady,
	}}})

	if got := len(updated.(watchModel).events); got != 1 {
		t.Errorf("events = %d, want the merge to replace rather than accumulate", got)
	}
}

// Local failures interleave with the shared log by time — a failed click is only
// worth reading next to what was happening when it failed.
func TestCombineEventsOrdersByTime(t *testing.T) {
	now := time.Now()
	shared := []aiEvent{
		{at: now, session: "a", text: "newest"},
		{at: now.Add(-2 * time.Minute), session: "b", text: "oldest"},
	}
	local := []aiEvent{{at: now.Add(-time.Minute), session: "c", text: "middle"}}

	got := combineEvents(shared, local)

	want := []string{"newest", "middle", "oldest"}
	if len(got) != len(want) {
		t.Fatalf("combined to %d events, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].text != w {
			t.Errorf("position %d = %q, want %q", i, got[i].text, w)
		}
	}
}

// The whole shared store rests on this: two panels seeing one transition must
// produce the same identity for it, and only since is the same in both.
func TestPanelEventConversionKeepsSince(t *testing.T) {
	since := time.UnixMilli(1_700_000_000_000)
	in := []aiEvent{
		{at: time.Now(), session: "a", text: "t", state: tmux.AIStateReady, since: since},
		{at: time.Now(), session: "b", text: "t", state: tmux.AIStateWorking}, // no since
	}

	round := fromPanelEvents(toPanelEvents(in))

	if !round[0].since.Equal(since) {
		t.Errorf("since did not survive the round trip: %v", round[0].since)
	}
	if !round[1].since.IsZero() {
		t.Errorf("a missing since became %v — the dedup rules read that as a real timestamp", round[1].since)
	}
}
