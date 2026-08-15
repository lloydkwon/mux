package tmux

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	// MaxPanelEvents caps the shared transition log.
	//
	// Fifty rather than the twenty a single panel used to keep for itself: the
	// log is now one line per transition across every session on the server, so
	// a busy session would otherwise push a quieter one's history out within a
	// couple of turns. The panel still draws only what fits its pane.
	MaxPanelEvents = 50

	// eventLogOption is the tmux option every panel reads the log from and
	// writes it back to.
	//
	// Global, and the first global option mux writes — @mux_panel_min_width is
	// global but read-only, @mux_panel_width is per session and @mux_panel_off
	// per window. Neither scope works here: the point is that every panel on the
	// server sees the same list, whichever session or window it happens to be in.
	//
	// Living on the server rather than on disk is the right lifetime too. A
	// "recent events" list describes what has happened while these sessions have
	// been up; it should die with them, and nothing has to clean it up.
	eventLogOption = "@mux_events"

	// eventDedupWindowMillis is how far apart two sightings of the same
	// transition may be and still be one event, when there is no AISince to key
	// on. Panels tick every 2s and their ticks are not aligned, so the same
	// change reaches them at different moments; ten seconds covers that with
	// room to spare, and a session cannot enter the same state twice inside it
	// without having left it first — which is itself a transition.
	eventDedupWindowMillis = 10_000
)

// PanelEvent is one AI state transition, as every panel on the server sees it.
//
// The JSON tags are short because this round-trips through a tmux option value
// on every tick of every panel.
//
// Text is stored rather than re-rendered from the other fields. The log is a
// record of what the panel said at the time, and keeping it here means this
// package needs to know nothing about how the panel words things — which is the
// direction the dependency already runs.
type PanelEvent struct {
	Session string  `json:"s"`
	State   AIState `json:"st"`
	At      int64   `json:"at"` // unix millis, when a panel first saw the change
	Since   int64   `json:"si"` // unix millis the state itself began; 0 if unknown
	Text    string  `json:"tx"`
}

// LoadEvents returns the shared log, newest first.
//
// Every failure degrades to an empty log rather than an error: the option is a
// place a user can reach with `set-option`, and a panel that refused to draw
// because someone pasted junk into it would be worse than one that starts over.
func LoadEvents() []PanelEvent {
	_, events := loadEvents()
	return events
}

// loadEvents also returns the raw option value, so MergeEvents can tell whether
// it has anything new to write.
func loadEvents() (string, []PanelEvent) {
	out, err := runner.Output("tmux", "show-options", "-gqv", eventLogOption)
	if err != nil {
		return "", nil
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", nil
	}
	var events []PanelEvent
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		return raw, nil
	}
	return raw, events
}

// MergeEvents folds this panel's newly observed transitions into the shared log
// and returns the whole thing, newest first.
//
// Called with no fresh events it still reads, because reading is the only way a
// panel learns what the others have seen.
//
// There is no lock, and none is needed. Every panel watches the same server-wide
// session list, so one transition is observed by all of them within a tick or
// two and written by each — the redundancy is the retry. A write that loses the
// race drops an event that another panel is about to write again anyway. What
// that does require is that the same transition produce the same entry
// everywhere, which is what duplicateAt decides.
func MergeEvents(fresh []PanelEvent) []PanelEvent {
	raw, events := loadEvents()

	added := false
	for _, e := range fresh {
		if duplicateAt(events, e) >= 0 {
			continue
		}
		events = append(events, e)
		added = true
	}
	sortEvents(events)
	if len(events) > MaxPanelEvents {
		events = events[:MaxPanelEvents]
		added = true
	}
	if !added {
		return events
	}

	encoded, err := json.Marshal(events)
	if err != nil {
		return events
	}
	// Nothing to say: skip the write so N idle panels are not all rewriting the
	// same value at each other every two seconds.
	if string(encoded) == raw {
		return events
	}
	_ = runner.Run("tmux", "set-option", "-g", eventLogOption, string(encoded))
	return events
}

// duplicateAt reports where in the log e already appears, or -1.
//
// Two rules, because only one of the two state producers timestamps itself.
// Claude writes statusUpdatedAt into its own status file, so AISince is a value
// every process reads identically and (session, state, since) names a transition
// exactly. Screen detection carries no timestamp at all — every tool it
// recognises arrives with Since == 0 — so those fall back to "the same session
// entering the same state at about the same moment", which is the most that can
// be known about them.
func duplicateAt(log []PanelEvent, e PanelEvent) int {
	for i, c := range log {
		if c.Session != e.Session || c.State != e.State {
			continue
		}
		if c.Since != 0 || e.Since != 0 {
			if c.Since == e.Since {
				return i
			}
			continue
		}
		if abs64(c.At-e.At) <= eventDedupWindowMillis {
			return i
		}
	}
	return -1
}

// sortEvents orders the log newest first. The session name breaks ties so the
// serialized value is identical on every panel that computes it — otherwise two
// panels holding the same set would write different strings back at each other.
func sortEvents(events []PanelEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].At != events[j].At {
			return events[i].At > events[j].At
		}
		return events[i].Session < events[j].Session
	})
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
