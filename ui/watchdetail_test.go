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
				got := detailView{session: sess, captured: captured, events: events}.render(w, h)
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
		got := ansi.Strip(detailView{session: &s, captured: captured, events: events}.render(50, h))
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
	shortest := ansi.Strip(detailView{session: &s, captured: captured, events: events}.render(50, 6))
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

	got := ansi.Strip(detailView{session: &s, captured: strings.Repeat("output\n", 40), events: events}.render(50, 40))
	if n := strings.Count(got, "작업 완료"); n != detailEventBudget {
		t.Errorf("column shows %d events, want it capped at %d", n, detailEventBudget)
	}
}

// Nothing selected is a real state — the panel starts there, and every session
// can go away while it is open.
func TestWatchDetailWithoutASession(t *testing.T) {
	got := ansi.Strip(detailView{}.render(50, 10))
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

// The session this pane lives in is already on screen beside the panel.
// Capturing it draws the same screen twice, for fifty columns.
func TestWatchDetailSelfCard(t *testing.T) {
	s := sess("project", tmux.AIStateWorking)
	s.Directory = "/home/u/Projects/mux"
	s.GitBranch = "main"
	s.WindowCount = 3
	s.Created = time.Now().Add(-43 * time.Minute)
	captured := strings.Repeat("SHOULD NOT APPEAR\n", 40)
	usage := &tmux.TokenUsage{InputTokens: 1_200_000, OutputTokens: 84_000, TotalCost: 1.24}

	got := ansi.Strip(detailView{
		session: &s, captured: captured, own: true, usage: usage,
	}.render(60, 20))

	if strings.Contains(got, "SHOULD NOT APPEAR") {
		t.Errorf("the card mirrored the pane beside it:\n%s", got)
	}
	for _, want := range []string{"지금 이 창입니다", "Projects/mux", "창 3", "가동 43m", "$1.24"} {
		if !strings.Contains(got, want) {
			t.Errorf("card is missing %q:\n%s", want, got)
		}
	}

	// The same session, not marked own, is still mirrored — this is a property
	// of which pane you are looking from, not of the session.
	other := ansi.Strip(detailView{session: &s, captured: captured}.render(60, 20))
	if !strings.Contains(other, "SHOULD NOT APPEAR") {
		t.Errorf("another session's output was withheld:\n%s", other)
	}
}

// A session with no cost to report must not leave a hole where the line was.
func TestWatchDetailSelfCardWithoutCost(t *testing.T) {
	s := sess("project", tmux.AIStateWorking)
	s.Directory = "/home/u/Projects/mux"
	s.WindowCount = 1

	got := ansi.Strip(detailView{session: &s, own: true}.render(60, 20))
	if strings.Contains(got, "$") {
		t.Errorf("a cost line appeared with no usage loaded:\n%s", got)
	}
	if !strings.Contains(got, "지금 이 창입니다") {
		t.Errorf("card lost its notice:\n%s", got)
	}
}

// The notice is the line that explains why this column looks different from
// every other session's, so it is the last thing to go.
func TestWatchDetailSelfCardYieldsFromTheBottom(t *testing.T) {
	s := sess("project", tmux.AIStateWorking)
	s.Directory = "/home/u/Projects/mux"
	s.WindowCount = 1
	s.Created = time.Now().Add(-time.Hour)
	usage := &tmux.TokenUsage{InputTokens: 10, OutputTokens: 10, TotalCost: 1}

	// Shrinking must never drop the notice while a lower fact survives, and the
	// column must still be exactly its size at every height.
	for _, h := range []int{4, 5, 6, 7, 8, 12} {
		out := detailView{session: &s, own: true, usage: usage}.render(60, h)
		if n := len(strings.Split(out, "\n")); n != h {
			t.Fatalf("height %d rendered %d lines", h, n)
		}
		got := ansi.Strip(out)
		if !strings.Contains(got, "지금 이 창입니다") {
			t.Errorf("height %d dropped the notice:\n%s", h, got)
		}
		if strings.Contains(got, "$1.00") && !strings.Contains(got, "Projects/mux") {
			t.Errorf("height %d kept the cost but dropped the path:\n%s", h, got)
		}
	}
}
