package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func detailTestSession() tmux.Session {
	s := sess("api-server", tmux.AIStateApproval)
	s.AIWaitingFor = "permission prompt"
	s.AISince = time.Now().Add(-12 * time.Second)
	s.GitBranch = "feat/auth"
	s.Directory = "/home/u/Projects/api"
	return s
}

// joinHorizontalFixed concatenates the two columns line by line, so a detail
// column even one cell off its width shears every row of the panel.
func TestWatchDetailFillsItsColumn(t *testing.T) {
	s := detailTestSession()
	captured := strings.Repeat("a line of captured output\n", 40)
	events := []aiEvent{
		{at: time.Now(), session: "api-server", text: "❗ 승인 대기"},
		{at: time.Now(), session: "web", text: "✅ 작업 완료"},
	}

	for _, w := range []int{3, 8, 20, 46, 51, 80} {
		for _, h := range []int{1, 2, 3, 5, 12, 40} {
			for _, sess := range []*tmux.Session{nil, &s} {
				got := watchDetail(sess, captured, events, w, h)
				lines := strings.Split(got, "\n")
				if len(lines) != h {
					t.Errorf("%dx%d: %d lines, want %d", w, h, len(lines), h)
					continue
				}
				for i, l := range lines {
					if n := ansi.StringWidth(ansi.Strip(l)); n != w {
						t.Errorf("%dx%d: line %d measures %d cells, want %d", w, h, i, n, w)
					}
				}
			}
		}
	}
}

// The live output is the reason to look at this column; the log below it is
// context. A short column has to give the rows to the output.
func TestWatchDetailEventLogYieldsFirst(t *testing.T) {
	s := detailTestSession()
	events := make([]aiEvent, 4)
	for i := range events {
		events[i] = aiEvent{at: time.Now(), session: "web", text: "✅ 작업 완료"}
	}
	captured := strings.Repeat("output\n", 40)

	// The log gives up rows one at a time, and the output never falls below
	// detailMinPreviewLines while it still has any to give.
	prev := len(events) + 1
	for _, h := range []int{20, 12, 9, 8, 7, 6, 5, 4} {
		got := ansi.Strip(watchDetail(&s, captured, events, 50, h))
		logged := strings.Count(got, "작업 완료")
		if logged > prev {
			t.Errorf("height %d shows %d events, more than the %d at the taller size:\n%s",
				h, logged, prev, got)
		}
		prev = logged

		lines := strings.Count(got, "output")
		if logged > 0 && lines < detailMinPreviewLines {
			t.Errorf("height %d kept %d events but left only %d output lines:\n%s",
				h, logged, lines, got)
		}
	}

	// Short enough and the log is gone entirely, but the output is still there.
	shortest := ansi.Strip(watchDetail(&s, captured, events, 50, 6))
	if strings.Contains(shortest, "작업 완료") {
		t.Errorf("a 6-row column kept the log instead of the output:\n%s", shortest)
	}
	if !strings.Contains(shortest, "output") {
		t.Errorf("a 6-row column dropped the output too:\n%s", shortest)
	}
}

// More events than the budget must not push the output off the column.
func TestWatchDetailCapsTheEventLog(t *testing.T) {
	s := detailTestSession()
	events := make([]aiEvent, maxEvents)
	for i := range events {
		events[i] = aiEvent{at: time.Now(), session: "web", text: "✅ 작업 완료"}
	}

	got := ansi.Strip(watchDetail(&s, strings.Repeat("output\n", 40), events, 50, 40))
	if n := strings.Count(got, "작업 완료"); n != detailEventBudget {
		t.Errorf("column shows %d events, want it capped at %d", n, detailEventBudget)
	}
}

// Nothing selected is a real state — the panel starts there, and every session
// can go away while it is open.
func TestWatchDetailWithoutASession(t *testing.T) {
	got := ansi.Strip(watchDetail(nil, "", nil, 50, 10))
	if !strings.Contains(got, "세션을 고르세요") {
		t.Errorf("an empty column says nothing:\n%s", got)
	}
}

// The header reads "which session, on what branch", and the branch is flush
// right so the column can be scanned down. Below a certain width the branch
// goes whole rather than as a stub.
func TestWatchDetailHeaderLayout(t *testing.T) {
	s := detailTestSession()

	wide := ansi.Strip(detailHeader(s, 40))
	iName := strings.Index(wide, "api-server")
	iBranch := strings.Index(wide, "⌥ feat/auth")
	if iName < 0 || iBranch < 0 {
		t.Fatalf("header %q is missing a part", wide)
	}
	if iName > iBranch {
		t.Errorf("header %q: want name → branch", wide)
	}
	if got := ansi.StringWidth(strings.TrimRight(wide, " ")); got != 39 {
		t.Errorf("header %q: content ends at %d, want the branch flush right", wide, got)
	}

	narrow := ansi.Strip(detailHeader(s, 14))
	if strings.Contains(narrow, "⌥") {
		t.Errorf("header %q kept a branch it had no room for", narrow)
	}
	if !strings.Contains(narrow, "api-serv") {
		t.Errorf("header %q lost the name it exists to show", narrow)
	}
}

// The detail column sits directly above the event log. Both have to call a
// state by the same name or the pane contradicts itself.
func TestWatchDetailStateMatchesTheEventLog(t *testing.T) {
	s := detailTestSession()

	status := ansi.Strip(detailStatus(s, 50))
	if !strings.Contains(status, "승인 대기") {
		t.Errorf("status %q does not use the panel's wording", status)
	}
	if !strings.Contains(status, "permission prompt") {
		t.Errorf("status %q dropped the blocking reason it had room for", status)
	}

	events, _ := detectTransitions(map[string]tmux.AIState{s.Name: tmux.AIStateWorking},
		[]tmux.Session{s}, time.Now())
	if len(events) != 1 || !strings.Contains(events[0].text, "승인 대기") {
		t.Fatalf("events = %v, want the same wording", events)
	}
}

// The reason it is blocked outranks everything it competes with: a column too
// narrow for all three parts keeps the reason and drops the elapsed time.
func TestDetailStateTextDegrades(t *testing.T) {
	s := detailTestSession()

	full := detailStateText(s, 40)
	if !strings.Contains(full, "permission prompt") || !strings.Contains(full, "12s") {
		t.Errorf("a wide column showed %q, want the reason and the elapsed time", full)
	}
	if got := detailStateText(s, 4); got != s.AIState.Icon() {
		t.Errorf("a 4-cell column showed %q, want just the glyph", got)
	}
	if got := detailStateText(sess("plain", tmux.AIStateNone), 40); got != "" {
		t.Errorf("a session with no state showed %q, want nothing", got)
	}
}
