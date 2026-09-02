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
	// Global, and the first global option mux writes — @mux_panel_min_width and
	// @mux_panel_header are global but read-only, and @mux_panel_off is per
	// window. That last scope is the one that would not work here: the point is
	// that every panel on the server sees the same list, whichever session or
	// window it happens to be in.
	//
	// The option is still the right lifetime for the *drawn* view: it describes
	// what has happened while these sessions have been up, and it should die with
	// them. What was wrong was equating the server's life with the user's — a
	// socket can be unlinked out from under a living server, and then the server
	// is dead to everyone but itself. eventarchive.go is the durable copy that
	// case needs; this stays the thing panels read every tick.
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
// and returns the whole thing, newest first. live names the sessions that still
// exist; pass nil to keep everything.
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
func MergeEvents(fresh []PanelEvent, live []string) []PanelEvent {
	raw, events := loadEvents()

	added := false

	// 살아 있는데 옵션에 항목이 하나도 없는 세션은 보관본에서 채운다. 새 서버에서는
	// 모든 세션이 여기 걸리므로 "옵션이 비었을 때"라는 별도 규칙이 필요 없다.
	//
	// **이건 반드시 trimLog 앞이다.** 뒤로 옮기면 채워 넣은 만큼 옵션 값이 커진 채로
	// set-option 에 실려 16KB MAX_IMSGSIZE 벽을 넘고, 그 실패는 아래에서 버려지므로
	// 로그가 *조용히* 갱신을 멈춘다. 이 변경이 tmux 를 망가뜨릴 수 있는 유일한 길이다.
	var archive []PanelEvent
	archiveLoaded := false
	if missing := missingSessions(events, live); len(missing) > 0 {
		archive, archiveLoaded = loadArchive(), true
		for _, e := range archive {
			if !missing[e.Session] || duplicateAt(events, e) >= 0 {
				continue
			}
			events = append(events, e)
			added = true
		}
	}

	for _, e := range fresh {
		if duplicateAt(events, e) >= 0 {
			continue
		}
		events = append(events, e)
		added = true
	}
	sortEvents(events)

	// trimLog 앞의 목록을 붙잡아 둔다. 보관본은 *이걸* 합쳐야 한다 — 잘린 뒤의
	// 목록을 합치면 같은 병합에서 죽은 세션의 이력은 파일에 한 번도 못 들어가고,
	// 그러면 보관본은 이미 가지고 있던 것만 지키는 셈이 되어 존재 이유가 없어진다.
	// trimLog 은 새 배열에 담아 돌려주므로 이 슬라이스는 그대로 살아 있다.
	preTrim := events
	if kept := trimLog(events, live); len(kept) != len(events) {
		events, added = kept, true
	}

	optionWritten := false
	if added {
		if encoded, err := json.Marshal(events); err == nil && string(encoded) != raw {
			// Nothing to say: skip the write so N idle panels are not all
			// rewriting the same value at each other every two seconds.
			_ = runner.Run("tmux", "set-option", "-g", eventLogOption, string(encoded))
			optionWritten = true
		}
	}

	mergeArchive(preTrim, fresh, archive, archiveLoaded, optionWritten)
	return events
}

// mergeArchive 는 보관본을 갱신한다. 옵션 쪽 판단과 분리된 두 번째 비교가 필요한
// 이유는, 옵션의 조기 반환들이 "*옵션이* 바뀌었나"로 판단하기 때문이다. 파일 쓰기를
// 거기 얹으면 정상 틱에서도, 새 패널의 첫 병합에서도 한 번도 안 써서 — 정작 서버가
// 죽는 순간 파일은 늘 낡아 있다.
//
// 한가한 틱에는 파일을 열지도 않는다. 채울 세션도 없고 옵션도 안 바뀌었으면
// syscall 이 0이다: 패널 일곱 × 2초 × 영원히 = 디스크 I/O 없음.
//
// 잠금은 두지 않는다 — 옵션이 그렇듯. 두 패널이 서로 다른 시야로 동시에 union 해도
// 각자 상대의 항목을 *더한* 결과를 쓰므로, 진 쪽의 항목은 다음 틱에 옵션(=수렴한
// 공유본)에서 다시 합쳐져 복원된다. union 의 두 번째 항이 이 패널의 관측이 아니라
// 병합된 옵션값인 것이 그 회복의 근거다.
func mergeArchive(preTrim, fresh, archive []PanelEvent, archiveLoaded, optionWritten bool) {
	if !archiveLoaded && !optionWritten {
		return
	}
	if !archiveLoaded {
		archive = loadArchive()
	}
	next := trimArchive(unionEvents(archive, preTrim, fresh), nowMillis())
	if sameEvents(next, archive) {
		return
	}
	saveArchive(next)
}

// trimLog cuts the log to size, keeping one entry for every session that the
// cut would otherwise silence. events must already be newest-first.
//
// A plain head-slice is recency-only, and recency alone starves the quiet. On a
// live server: fifty entries covering sixty-four minutes, twenty of them one
// session's, and two of the seven running sessions with nothing in the log at
// all. The panel now draws a line per session, so "nothing at all" is a session
// that reports nothing about itself for as long as a noisier one keeps talking.
//
// Raising MaxPanelEvents does not fix that. It buys time and hits a wall:
// measured on tmux 3.4, a set-option argv of 16000 bytes goes through and 20000
// comes back "command too long" (MAX_IMSGSIZE), which at ~90 bytes an entry is
// somewhere near 170. And the order would still be recency, so a session idle
// since morning still falls off.
//
// The tail scan is what makes the log's cost bounded by *sessions* rather than
// by traffic, and it costs no schema change: the value stays a []PanelEvent and
// stays sorted, since the tail is newest-first too and every entry appended from
// it is older than the last one kept.
//
// Sessions in the log that no longer exist are dropped outright. Nothing draws
// them — the panel renders rows for live sessions only — and without this the
// keep-one rule would hold their entries forever, since a dead session produces
// nothing newer to replace them. Measured: six of fifty entries belonged to a
// session that had already gone.
func trimLog(events []PanelEvent, live []string) []PanelEvent {
	if live != nil {
		alive := make(map[string]bool, len(live))
		for _, name := range live {
			alive[name] = true
		}
		kept := events[:0:0]
		for _, e := range events {
			if alive[e.Session] {
				kept = append(kept, e)
			}
		}
		events = kept
	}
	if len(events) <= MaxPanelEvents {
		return events
	}

	kept := events[:MaxPanelEvents:MaxPanelEvents]
	seen := make(map[string]bool, len(kept))
	for _, e := range kept {
		seen[e.Session] = true
	}
	for _, e := range events[MaxPanelEvents:] {
		if seen[e.Session] {
			continue
		}
		seen[e.Session] = true
		kept = append(kept, e)
	}
	return kept
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
