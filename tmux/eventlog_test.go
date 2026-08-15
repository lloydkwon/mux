package tmux

import (
	"encoding/json"
	"strings"
	"testing"
)

const showEvents = "tmux show-options -gqv @mux_events"

// mockEventLog registers what the option currently holds.
func mockEventLog(m *mockRunner, events []PanelEvent) {
	raw := ""
	if events != nil {
		b, _ := json.Marshal(events)
		raw = string(b)
	}
	m.OnOutput([]byte(raw+"\n"), nil, "tmux", "show-options", "-gqv", eventLogOption)
}

// writtenLog returns the value the last set-option wrote, or "" if none did.
func writtenLog(m *mockRunner) string {
	const prefix = "tmux set-option -g " + eventLogOption + " "
	for _, r := range m.runs {
		if strings.HasPrefix(r, prefix) {
			return strings.TrimPrefix(r, prefix)
		}
	}
	return ""
}

func ev(session string, state AIState, at, since int64) PanelEvent {
	return PanelEvent{Session: session, State: state, At: at, Since: since, Text: "x"}
}

// An unset option is the normal state of a fresh tmux server, not an error.
func TestLoadEventsEmpty(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", eventLogOption)
		if got := LoadEvents(); len(got) != 0 {
			t.Errorf("LoadEvents on an unset option = %v, want empty", got)
		}
	})
}

// The option is somewhere a user can reach with set-option. Junk in it must cost
// the history, never the panel.
func TestLoadEventsSurvivesGarbage(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("not json at all\n"), nil, "tmux", "show-options", "-gqv", eventLogOption)
		if got := LoadEvents(); len(got) != 0 {
			t.Errorf("LoadEvents on garbage = %v, want empty", got)
		}
	})
}

// Every panel observes the same transition and writes it. Without a stable key
// the log would grow one copy per panel, which is the whole reason AISince is
// carried on the event.
func TestMergeEventsDedupesBySince(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		existing := ev("web", AIStateReady, 1_000, 900)
		mockEventLog(m, []PanelEvent{existing})

		// The same transition as another panel would time it: different At,
		// identical Since.
		got := MergeEvents([]PanelEvent{ev("web", AIStateReady, 2_500, 900)})

		if len(got) != 1 {
			t.Fatalf("merged to %d events, want 1: %v", len(got), got)
		}
		if got[0].At != 1_000 {
			t.Errorf("the existing entry was replaced: %v", got[0])
		}
		if w := writtenLog(m); w != "" {
			t.Errorf("nothing changed but the log was rewritten: %s", w)
		}
	})
}

// Screen detection carries no timestamp, so every tool it recognises arrives
// with Since == 0. Those fall back to "same session, same state, about now".
func TestMergeEventsDedupesUntimestampedWithinWindow(t *testing.T) {
	tests := []struct {
		name string
		at   int64
		want int
	}{
		{"inside the window", 1_000 + eventDedupWindowMillis - 1, 1},
		{"on the boundary", 1_000 + eventDedupWindowMillis, 1},
		{"past it", 1_000 + eventDedupWindowMillis + 1, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				mockEventLog(m, []PanelEvent{ev("api", AIStateWorking, 1_000, 0)})
				got := MergeEvents([]PanelEvent{ev("api", AIStateWorking, tc.at, 0)})
				if len(got) != tc.want {
					t.Errorf("merged to %d events, want %d: %v", len(got), tc.want, got)
				}
			})
		})
	}
}

// A different session, or the same one entering a different state, is a
// different event however close together they land.
func TestMergeEventsKeepsDistinctTransitions(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, []PanelEvent{ev("api", AIStateWorking, 1_000, 0)})
		got := MergeEvents([]PanelEvent{
			ev("api", AIStateReady, 1_001, 0),
			ev("web", AIStateWorking, 1_001, 0),
		})
		if len(got) != 3 {
			t.Errorf("merged to %d events, want 3: %v", len(got), got)
		}
	})
}

// The log is one line per transition across every session now, so it has to
// drop the oldest rather than grow without bound.
func TestMergeEventsCapsAtMax(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		var existing []PanelEvent
		for i := 0; i < MaxPanelEvents; i++ {
			existing = append(existing, ev("s", AIStateReady, int64(i+1)*1_000, int64(i+1)))
		}
		mockEventLog(m, existing)

		got := MergeEvents([]PanelEvent{ev("s", AIStateReady, 999_000, 999)})

		if len(got) != MaxPanelEvents {
			t.Fatalf("merged to %d events, want %d", len(got), MaxPanelEvents)
		}
		if got[0].At != 999_000 {
			t.Errorf("newest event is not first: %v", got[0])
		}
		for _, e := range got {
			if e.At == 1_000 {
				t.Error("the oldest event survived the cap")
			}
		}
	})
}

// N idle panels rewriting the same value at each other every two seconds is
// pure noise on the server.
func TestMergeEventsWritesNothingWhenUnchanged(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, []PanelEvent{ev("web", AIStateReady, 1_000, 900)})
		MergeEvents(nil)
		if w := writtenLog(m); w != "" {
			t.Errorf("an idle merge wrote %s", w)
		}
	})
}

// Two panels holding the same set must serialize it identically, or each write
// looks like a change to the other and they rewrite the option forever.
func TestMergeEventsSerializationIsDeterministic(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		mockEventLog(m, nil)
		MergeEvents([]PanelEvent{
			ev("b", AIStateReady, 1_000, 1),
			ev("a", AIStateReady, 1_000, 2),
			ev("c", AIStateWorking, 5_000, 3),
		})
		first := writtenLog(m)
		if first == "" {
			t.Fatal("nothing was written")
		}
		if strings.Contains(first, "\n") {
			t.Errorf("the option value spans lines, which show-options -v cannot round-trip: %q", first)
		}

		var decoded []PanelEvent
		if err := json.Unmarshal([]byte(first), &decoded); err != nil {
			t.Fatalf("wrote invalid JSON: %v", err)
		}
		if decoded[0].Session != "c" || decoded[1].Session != "a" || decoded[2].Session != "b" {
			t.Errorf("want newest first then name order, got %v", decoded)
		}
	})
}
