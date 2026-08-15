package ui

import (
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lloydkwon/mux/tmux"
)

// eventsMergedMsg carries the shared log back from tmux.
type eventsMergedMsg struct {
	events []aiEvent
}

// mergeEventsCmd folds this panel's newly observed transitions into the log all
// panels share and brings the whole thing back.
//
// A command rather than a call inside Update for one concrete reason: the store
// shells out to tmux, and ui has no mock runner. Several tests drive
// Update(sessionsLoadedMsg{...}) directly, and doing the merge there would make
// `go test ./ui` write options into the developer's real tmux server. A command
// is inert until something runs it.
func mergeEventsCmd(fresh []aiEvent) tea.Cmd {
	return func() tea.Msg {
		return eventsMergedMsg{events: fromPanelEvents(tmux.MergeEvents(toPanelEvents(fresh)))}
	}
}

// combineEvents interleaves the shared log with this pane's own failures,
// newest first.
//
// By time rather than by appending one list to the other: a failed click is only
// worth reading next to what was happening when it failed.
func combineEvents(shared, local []aiEvent) []aiEvent {
	if len(local) == 0 {
		return shared
	}
	out := make([]aiEvent, 0, len(shared)+len(local))
	out = append(out, shared...)
	out = append(out, local...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	if len(out) > maxEvents {
		out = out[:maxEvents]
	}
	return out
}

func toPanelEvents(evs []aiEvent) []tmux.PanelEvent {
	if len(evs) == 0 {
		return nil
	}
	out := make([]tmux.PanelEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, tmux.PanelEvent{
			Session: e.session,
			State:   e.state,
			At:      e.at.UnixMilli(),
			Since:   millisOrZero(e.since),
			Text:    e.text,
		})
	}
	return out
}

func fromPanelEvents(evs []tmux.PanelEvent) []aiEvent {
	if len(evs) == 0 {
		return nil
	}
	out := make([]aiEvent, 0, len(evs))
	for _, e := range evs {
		ev := aiEvent{
			at:      time.UnixMilli(e.At),
			session: e.Session,
			text:    e.Text,
			state:   e.State,
		}
		if e.Since != 0 {
			ev.since = time.UnixMilli(e.Since)
		}
		out = append(out, ev)
	}
	return out
}

// millisOrZero keeps a zero time zero rather than turning it into 1970, which
// the dedup rules read as "this transition has no timestamp of its own".
func millisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
