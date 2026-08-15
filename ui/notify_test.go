package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func sess(name string, st tmux.AIState) tmux.Session {
	return tmux.Session{Name: name, ActiveCommand: "claude", AIState: st}
}

// The detector is the whole feature: the list already shows current state, so
// the only thing worth logging is a change. Getting these rules wrong either
// spams the log or silently drops the one transition that blocks on the user.
func TestDetectTransitions(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		prev     map[string]tmux.AIState
		sessions []tmux.Session
		want     []string // event text prefixes, in order
	}{
		{
			name:     "first sighting records but does not report",
			prev:     nil,
			sessions: []tmux.Session{sess("a", tmux.AIStateApproval)},
			want:     nil,
		},
		{
			name:     "entering approval is always news",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateWorking},
			sessions: []tmux.Session{sess("a", tmux.AIStateApproval)},
			want:     []string{"❗ 승인 대기"},
		},
		{
			name:     "approval from none still reports",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateNone},
			sessions: []tmux.Session{sess("a", tmux.AIStateApproval)},
			want:     []string{"❗ 승인 대기"},
		},
		{
			name:     "working to ready is a finished turn",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateWorking},
			sessions: []tmux.Session{sess("a", tmux.AIStateReady)},
			want:     []string{"✅ 작업 완료"},
		},
		{
			name:     "approval to ready is also a finished turn",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateApproval},
			sessions: []tmux.Session{sess("a", tmux.AIStateReady)},
			want:     []string{"✅ 작업 완료"},
		},
		{
			name:     "none to ready is a session appearing, not a turn ending",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateNone},
			sessions: []tmux.Session{sess("a", tmux.AIStateReady)},
			want:     nil,
		},
		{
			// A turn that drops to a shell mid-way and comes back still ended.
			name:     "shell to ready is a finished turn",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateShell},
			sessions: []tmux.Session{sess("a", tmux.AIStateReady)},
			want:     []string{"✅ 작업 완료"},
		},
		{
			name:     "entering shell stays silent",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateWorking},
			sessions: []tmux.Session{sess("a", tmux.AIStateShell)},
			want:     nil,
		},
		{
			name:     "entering working stays silent",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateReady},
			sessions: []tmux.Session{sess("a", tmux.AIStateWorking)},
			want:     nil,
		},
		{
			name:     "unchanged state reports nothing",
			prev:     map[string]tmux.AIState{"a": tmux.AIStateApproval},
			sessions: []tmux.Session{sess("a", tmux.AIStateApproval)},
			want:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := detectTransitions(tc.prev, tc.sessions, now)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d events %v, want %d", len(got), got, len(tc.want))
			}
			for i, want := range tc.want {
				if !strings.HasPrefix(got[i].text, want) {
					t.Errorf("event %d = %q, want prefix %q", i, got[i].text, want)
				}
			}
		})
	}
}

// A vanished session must drop out, or its stale state would read as a
// transition when the name comes back.
func TestDetectTransitionsForgetsGoneSessions(t *testing.T) {
	prev := map[string]tmux.AIState{"gone": tmux.AIStateWorking, "here": tmux.AIStateReady}
	_, next := detectTransitions(prev, []tmux.Session{sess("here", tmux.AIStateReady)}, time.Now())

	if _, ok := next["gone"]; ok {
		t.Error("vanished session still tracked")
	}
	if next["here"] != tmux.AIStateReady {
		t.Errorf("here = %v, want ready", next["here"])
	}
}

// The approval reason is the actionable half of the event.
func TestDetectTransitionsCarriesWaitingFor(t *testing.T) {
	s := sess("a", tmux.AIStateApproval)
	s.AIWaitingFor = "Bash: rm -rf build"
	got, _ := detectTransitions(map[string]tmux.AIState{"a": tmux.AIStateWorking},
		[]tmux.Session{s}, time.Now())

	if len(got) != 1 || !strings.Contains(got[0].text, "rm -rf build") {
		t.Fatalf("events = %v, want the waitingFor reason", got)
	}
}

func TestPushEventsCapsAndOrders(t *testing.T) {
	var log []aiEvent
	for i := 0; i < maxEvents+5; i++ {
		log = pushEvents(log, []aiEvent{{session: "s", text: string(rune('a' + i%26))}})
	}
	if len(log) != maxEvents {
		t.Fatalf("log holds %d, want %d", len(log), maxEvents)
	}

	// Newest first.
	log = pushEvents(nil, []aiEvent{{text: "first"}})
	log = pushEvents(log, []aiEvent{{text: "second"}})
	if log[0].text != "second" {
		t.Errorf("log[0] = %q, want the newest", log[0].text)
	}
}

// notifyOrder returns the session of each clickable row, in display order.
func notifyOrder(ss []tmux.Session) []string {
	var got []string
	var last string
	for _, l := range notifyLines(ss, nil, 40, 0, "", "", false) {
		if l.session != "" && l.session != last {
			got = append(got, l.session)
		}
		last = l.session
	}
	return got
}

// Rows must be ordered by the number they print. Sorting by anything else makes
// the elapsed column non-monotonic — a session at 41m listed under two at 3h
// reads as a bug, which is what sorting by creation time actually produced.
func TestNotifyLinesSortByDisplayedAge(t *testing.T) {
	now := time.Now()
	// Created order is deliberately the reverse of the state ages: the value on
	// screen has to win.
	mk := func(name string, created, since time.Duration, st tmux.AIState) tmux.Session {
		s := sess(name, st)
		s.Created = now.Add(-created)
		s.AISince = now.Add(-since)
		return s
	}
	sessions := []tmux.Session{
		mk("front", 5*time.Hour, 41*time.Minute, tmux.AIStateReady), // oldest session, freshest state
		mk("dimont", 2*time.Hour, 3*time.Hour, tmux.AIStateReady),   //
		mk("mux", time.Minute, 2*time.Minute, tmux.AIStateWorking),  // newest state
	}

	want := []string{"mux", "front", "dimont"}
	if got := notifyOrder(sessions); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v (smallest elapsed first)", got, want)
	}
}

// A session with no live state falls back to its creation age, and that is what
// it is sorted by too — the column stays readable either way.
func TestNotifyLinesSortUsesCreationWithoutState(t *testing.T) {
	now := time.Now()
	mk := func(name string, created time.Duration) tmux.Session {
		s := tmux.Session{Name: name, ActiveCommand: "claude", Created: now.Add(-created)}
		return s
	}
	sessions := []tmux.Session{mk("old", time.Hour), mk("new", time.Minute)}

	want := []string{"new", "old"}
	if got := notifyOrder(sessions); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// The blank line between sessions exists to make the click target a block
// rather than a single row, so it has to click to the same session. Spacing
// that does not is just extra height.
func TestNotifySpacingIsClickable(t *testing.T) {
	now := time.Now()
	first := sess("mux", tmux.AIStateWorking)
	first.AISince = now.Add(-time.Minute) // sorts first
	blocked := sess("api", tmux.AIStateApproval)
	blocked.AISince = now.Add(-time.Hour) // sorts last
	blocked.AIWaitingFor = "Bash: git push"
	sessions := []tmux.Session{first, blocked}

	rows := notifyLines(sessions, nil, 40, 0, "", "", false)
	owners := map[string]int{}
	for _, l := range rows {
		if l.session != "" {
			owners[l.session]++
		}
	}
	if owners["mux"] != 2 { // row + spacer
		t.Errorf("mux owns %d rows, want 2", owners["mux"])
	}
	// The spacer goes between sessions, so the last one has none — the accepted
	// cost of not trailing a blank into the event separator.
	if owners["api"] != 2 { // row + reason, no spacer
		t.Errorf("api owns %d rows, want 2", owners["api"])
	}

}

// The help page's legend promises ⌥⌥ for a linked worktree; the panel used to
// print a plain ⌥ for both, which made the legend a lie.
func TestNotifyWorktreeGlyph(t *testing.T) {
	plain := sess("repo", tmux.AIStateWorking)
	plain.GitBranch = "main"
	tree := sess("wt", tmux.AIStateWorking)
	tree.GitBranch = "feat"
	tree.IsWorktree = true

	out := ansi.Strip(strings.Join(notifyTexts(notifyLines([]tmux.Session{plain, tree}, nil, 60, 0, "", "", false)), "\n"))
	if !strings.Contains(out, "⌥ main") {
		t.Errorf("a plain repo did not render a single glyph:\n%s", out)
	}
	if !strings.Contains(out, "⌥⌥ feat") {
		t.Errorf("a worktree did not render the doubled glyph:\n%s", out)
	}
}

// The row reads left to right as "which session, how long, on what branch" —
// the age belongs to the name, so it sits against it, while the branch is flush
// right so the column can be scanned down.
func TestNotifySessionLineLayout(t *testing.T) {
	now := time.Now()
	mk := func(name string) tmux.Session {
		s := sess(name, tmux.AIStateReady)
		s.GitBranch = "main"
		s.AISince = now.Add(-90 * time.Second)
		return s
	}

	for _, name := range []string{"mux", "a-much-longer-name"} {
		row := ansi.Strip(notifySessionLine(mk(name), 44, rowFlags{}))

		iName := strings.Index(row, name)
		iAge := strings.Index(row, "1m")
		iBranch := strings.Index(row, "⌥ main")
		if iName < 0 || iAge < 0 || iBranch < 0 {
			t.Fatalf("row %q is missing a part", row)
		}
		if !(iName < iAge && iAge < iBranch) {
			t.Errorf("row %q: want name → age → branch", row)
		}
		// The age hugs the name; only the single separator space is between.
		if got := row[iName+len(name) : iAge]; got != " " {
			t.Errorf("row %q: %q between name and age, want one space", row, got)
		}
		// The branch ends at the right margin whatever the name's length.
		if got := ansi.StringWidth(strings.TrimRight(row, " ")); got != 43 {
			t.Errorf("row %q: content ends at %d, want the branch flush right", row, got)
		}
	}
}

// When the row cannot hold everything the branch is context and goes first —
// losing which session it is, or how long it has been stuck, would be worse.
func TestNotifySessionLineDropsBranchFirst(t *testing.T) {
	s := sess("dimont-onboarding", tmux.AIStateReady)
	s.GitBranch = "dimont-onboarding"
	s.AISince = time.Now().Add(-3 * time.Hour)

	row := ansi.Strip(notifySessionLine(s, 24, rowFlags{}))
	if !strings.Contains(row, "3h") {
		t.Errorf("row %q dropped the age", row)
	}
	if strings.Contains(row, "⌥") {
		t.Errorf("row %q kept a branch it had no room for", row)
	}
}

// A fixed name column strands the text far right of every short name. It must
// follow the name instead, which means its start moves with the name's length.
func TestNotifyEventLineHugsName(t *testing.T) {
	at := time.Now()
	start := func(session string) int {
		row := ansi.Strip(notifyEventLine(
			aiEvent{at: at, session: session, text: "✅ 작업 완료"}, 44))
		i := strings.Index(row, session)
		if i < 0 {
			t.Fatalf("row %q is missing the session name", row)
		}
		if got := row[i+len(session) : i+len(session)+1]; got != " " {
			t.Fatalf("row %q: %q after the name, want one space", row, got)
		}
		return strings.Index(row, "✅")
	}

	short, long := start("mux"), start("dimont-onboarding")
	if short >= long {
		t.Errorf("text starts at %d for a short name and %d for a long one — want it to follow the name", short, long)
	}
}

// The card only shows once you pick the session, so the list needs a mark that
// answers "why is that one different" before you pick it.
func TestNotifyMarksOwnSession(t *testing.T) {
	mine := sess("project", tmux.AIStateWorking)
	mine.GitBranch = "main"
	other := sess("api", tmux.AIStateWorking)
	other.GitBranch = "main"
	sessions := []tmux.Session{mine, other}

	rows := ansi.Strip(strings.Join(notifyTexts(notifySessionLines(sessions, 40, "", "project")), "\n"))
	marked := 0
	for _, line := range strings.Split(rows, "\n") {
		if strings.Contains(line, ownMarker) {
			marked++
			if !strings.Contains(line, "project") {
				t.Errorf("the marker landed on the wrong row: %q", line)
			}
		}
	}
	if marked != 1 {
		t.Errorf("%d rows marked, want exactly 1:\n%s", marked, rows)
	}

	// No own session known — the panel used to have no idea, and marking
	// something anyway would be a guess.
	none := ansi.Strip(strings.Join(notifyTexts(notifySessionLines(sessions, 40, "", "")), "\n"))
	if strings.Contains(none, ownMarker) {
		t.Errorf("marked a row with no own session known:\n%s", none)
	}
}

// The marker costs two cells, so it has to come out of the same budget as
// everything else rather than pushing the row past its width.
func TestNotifySessionLineWidthWithMarker(t *testing.T) {
	s := sess("dimont-onboarding", tmux.AIStateReady)
	s.GitBranch = "dimont-onboarding"
	s.AISince = time.Now().Add(-3 * time.Hour)

	for _, width := range []int{24, 30, 34, 40, 60} {
		for _, own := range []bool{false, true} {
			row := notifySessionLine(s, width, rowFlags{own: own})
			if got := ansi.StringWidth(ansi.Strip(row)); got != width {
				t.Errorf("width=%d own=%v: row measures %d cells", width, own, got)
			}
		}
	}

	// The branch is context and still yields before the marker does — losing
	// which session it is, or that it is this one, would be worse.
	narrow := ansi.Strip(notifySessionLine(s, 24, rowFlags{own: true}))
	if !strings.Contains(narrow, ownMarker) {
		t.Errorf("row %q dropped the marker instead of the branch", narrow)
	}
	if strings.Contains(narrow, "⌥") {
		t.Errorf("row %q kept a branch it had no room for", narrow)
	}
}

// headerSession is a session with everything the header can draw.
func headerSession(name string) tmux.Session {
	s := sess(name, tmux.AIStateWorking)
	s.Directory = "/work/projects/mux"
	s.GitBranch = "main"
	s.AISince = time.Now().Add(-90 * time.Second)
	return s
}

// The header answers the question the panel could not: which session the cursor
// is on, what it is doing, and where it lives. A detached session has no pane to
// read any of that off.
func TestPanelHeaderDescribesSelectedSession(t *testing.T) {
	ss := []tmux.Session{headerSession("mux"), sess("api", tmux.AIStateApproval)}

	out := ansi.Strip(strings.Join(notifyTexts(panelHeaderLines(ss, 40, 30, "mux")), "\n"))

	for _, want := range []string{"mux", "⌥ main", "작업 중", "1m", "/work/projects/mux"} {
		if !strings.Contains(out, want) {
			t.Errorf("header dropped %q:\n%s", want, out)
		}
	}
	// Korean, not the TUI's English — the event log below it speaks Korean and
	// two names for one state in a single pane reads as a bug.
	if strings.Contains(out, "working") {
		t.Errorf("header used the TUI's English label:\n%s", out)
	}
}

// The panel has no height budget — fixedBox clips from the bottom — so the
// header has to ration itself or it eats the event log on a short pane.
func TestPanelHeaderRationsByHeight(t *testing.T) {
	ss := []tmux.Session{headerSession("mux")}

	tests := []struct {
		height int
		rows   int // header rows including the section break
		path   bool
	}{
		{30, 4, true},  // name, state, path, break
		{12, 4, true},  // exactly the full-header floor
		{11, 3, false}, // path is the first thing to yield
		{8, 3, false},  // the short-header floor
		{7, 0, false},  // below it the header yields entirely
		{3, 0, false},
	}
	for _, tc := range tests {
		lines := panelHeaderLines(ss, 40, tc.height, "mux")
		if len(lines) != tc.rows {
			t.Errorf("height=%d: %d header rows, want %d", tc.height, len(lines), tc.rows)
		}
		got := strings.Contains(ansi.Strip(strings.Join(notifyTexts(lines), "\n")), "/work/projects/mux")
		if got != tc.path {
			t.Errorf("height=%d: path shown=%v, want %v", tc.height, got, tc.path)
		}
	}
}

// Header rows must belong to no session. sessionAtRow indexes the same slice, so
// a header row that claimed one would make a click land on a session nobody
// aimed at; sessionOrder skips empty rows, which is what keeps the keyboard
// cursor off them.
func TestPanelHeaderRowsAreNotClickTargets(t *testing.T) {
	for _, l := range panelHeaderLines([]tmux.Session{headerSession("mux")}, 40, 30, "mux") {
		if l.session != "" {
			t.Errorf("header row %q claims session %q", ansi.Strip(l.text), l.session)
		}
	}
}

// A selection naming a session that has gone must not draw a header for it — the
// cursor is a name, and names outlive the sessions they point at by one refresh.
func TestPanelHeaderSkipsUnknownSelection(t *testing.T) {
	if got := panelHeaderLines([]tmux.Session{headerSession("mux")}, 40, 30, "gone"); got != nil {
		t.Errorf("header drew %d rows for a session that is not there", len(got))
	}
	if got := panelHeaderLines([]tmux.Session{headerSession("mux")}, 40, 30, ""); got != nil {
		t.Errorf("header drew %d rows with nothing selected", len(got))
	}
}

// Every header row is exactly width cells, like every other row in the panel —
// fixedBox pads the box but a short row inside it shears the columns.
func TestPanelHeaderRowWidths(t *testing.T) {
	s := headerSession("a-rather-long-session-name")
	for _, width := range []int{24, 30, 36, 48, 84} {
		for _, l := range panelHeaderLines([]tmux.Session{s}, width, 30, s.Name) {
			if got := ansi.StringWidth(ansi.Strip(l.text)); got != width {
				t.Errorf("width=%d: header row %q measures %d cells", width, ansi.Strip(l.text), got)
			}
		}
	}
}

// The header is off unless asked for. With it off the panel must be exactly what
// it was before the header existed — including the row the first session lands
// on, which is what every click maps through.
func TestNotifyLinesHeaderIsOptIn(t *testing.T) {
	ss := []tmux.Session{headerSession("mux")}

	off := notifyLines(ss, nil, 40, 30, "mux", "", false)
	on := notifyLines(ss, nil, 40, 30, "mux", "", true)

	// The path is the tell: the session row carries a name, a state and a branch
	// of its own, but only the header prints the directory.
	offText := ansi.Strip(strings.Join(notifyTexts(off), "\n"))
	if strings.Contains(offText, "/work/projects/mux") {
		t.Errorf("the header drew with showHeader=false:\n%s", offText)
	}
	if !strings.Contains(ansi.Strip(strings.Join(notifyTexts(on), "\n")), "/work/projects/mux") {
		t.Error("the header did not draw with showHeader=true")
	}

	// Row 0 is the 🔔 heading and row 1 the blank under it, exactly as before.
	if got := firstSessionOf(off); got != 2 {
		t.Errorf("first session row = %d with the header off, want 2", got)
	}
	if got := firstSessionOf(on); got <= 2 {
		t.Errorf("first session row = %d with the header on, want it pushed down", got)
	}
}

func firstSessionOf(lines []notifyLine) int {
	for i, l := range lines {
		if l.session != "" {
			return i
		}
	}
	return -1
}
