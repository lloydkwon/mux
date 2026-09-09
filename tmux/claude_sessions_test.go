package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// runFile builds a session state file the way Claude writes one.
func runFile(pid int, sid, cwd, status, name, tmuxRef string, startedAt, statusAt int64) string {
	ref := ""
	if tmuxRef != "" {
		ref = fmt.Sprintf(`"tmux":%q,`, tmuxRef)
	}
	return fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,%s"status":%q,"name":%q,`+
		`"nameSource":"user","startedAt":%d,"statusUpdatedAt":%d,"procStart":"1"}`,
		pid, sid, cwd, ref, status, name, startedAt, statusAt)
}

func runNamed(runs []ClaudeRun, name string) (ClaudeRun, bool) {
	for _, r := range runs {
		if r.Session.Name == name {
			return r, true
		}
	}
	return ClaudeRun{}, false
}

// The whole reason this scan exists: ClaudeStatuses drops every file with no
// tmux field, and measured on a live machine that was nine of twelve.
func TestClaudeSessionsKeepsConversationsOutsideTmux(t *testing.T) {
	files := map[string]string{
		"1.json": runFile(1, "aaaaaaaa-1", "/w/one", "idle", "one", "", 1000, 2000),
		"2.json": runFile(2, "bbbbbbbb-2", "/w/two", "busy", "two", "", 1000, 3000),
	}
	withClaudeSessions(t, files, nil, func() {
		runs := ClaudeSessions()
		if len(runs) != 2 {
			t.Fatalf("runs = %d, want both conversations", len(runs))
		}
		if _, ok := runNamed(runs, "one"); !ok {
			t.Error("a conversation outside tmux was dropped")
		}
		for _, r := range runs {
			if r.TmuxSession != "" || !r.Elsewhere {
				t.Errorf("%s: TmuxSession=%q Elsewhere=%v, want unreachable",
					r.Session.Name, r.TmuxSession, r.Elsewhere)
			}
			if r.Session.Attached {
				t.Errorf("%s is marked attached but is not reachable in tmux", r.Session.Name)
			}
		}
	})
}

// One conversation is one row however many processes hold it. Measured: the
// same id alive in a herdr pane and a tmux pane at once, because the user had
// resumed it by hand. The tmux one wins because it is the one enter can go to.
func TestClaudeSessionsCollapsesOneConversationHeldTwice(t *testing.T) {
	files := map[string]string{
		"herdr.json": runFile(1, "shared-id", "/w/proj", "idle", "dup", "", 1000, 5000),
		"tmux.json":  runFile(2, "shared-id", "/w/proj", "idle", "dup", "sess:@0.%0", 1000, 2000),
	}
	withClaudePanes(t, files, "%0 2 sess\n", func() {
		runs := ClaudeSessions()
		if len(runs) != 1 {
			t.Fatalf("runs = %d, want one row for one conversation", len(runs))
		}
		r := runs[0]
		if r.TmuxSession != "sess" {
			t.Errorf("TmuxSession = %q, want the reachable copy to win despite being older", r.TmuxSession)
		}
		if r.Elsewhere {
			t.Error("a conversation reachable in tmux was marked as elsewhere")
		}
		if !r.Session.Attached {
			t.Error("a reachable conversation was not marked attached")
		}
	})
}

func TestClaudeSessionsDropsDeadProcesses(t *testing.T) {
	files := map[string]string{
		"live.json": runFile(1, "live-id", "/w/a", "idle", "live", "", 1000, 2000),
		"dead.json": runFile(2, "dead-id", "/w/b", "idle", "dead", "", 1000, 2000),
	}
	alive := func(pid int, _ string) bool { return pid == 1 }
	withClaudeSessions(t, files, alive, func() {
		runs := ClaudeSessions()
		if len(runs) != 1 || runs[0].Session.Name != "live" {
			t.Fatalf("runs = %v, want only the live one", runs)
		}
	})
}

// Session.Name keys expansion, the order map and the cursor, so two rows may
// not share one. A plan/exec pair reused across projects collides by design.
func TestClaudeSessionNamesAreUnique(t *testing.T) {
	files := map[string]string{
		"a.json": runFile(1, "aaaaaaaa-1111", "/w/one", "idle", "plan", "", 1000, 3000),
		"b.json": runFile(2, "bbbbbbbb-2222", "/w/two", "idle", "plan", "", 1000, 2000),
		"c.json": runFile(3, "cccccccc-3333", "/w/three", "idle", "solo", "", 1000, 1000),
	}
	withClaudeSessions(t, files, nil, func() {
		runs := ClaudeSessions()
		seen := map[string]bool{}
		for _, r := range runs {
			if seen[r.Session.Name] {
				t.Fatalf("duplicate row name %q", r.Session.Name)
			}
			seen[r.Session.Name] = true
		}
		// The one that does not collide keeps its plain name.
		if !seen["solo"] {
			t.Errorf("names = %v, want the uncontested one left alone", seen)
		}
		if seen["plan"] {
			t.Errorf("names = %v, want both colliding rows disambiguated", seen)
		}
	})
}

// A row with no live state still needs an age, and startedAt is the one real
// clock a conversation has.
func TestClaudeSessionCreatedComesFromStartedAt(t *testing.T) {
	files := map[string]string{
		"a.json": runFile(1, "id-1", "/w/one", "idle", "one", "", 1700, 0),
	}
	withClaudeSessions(t, files, nil, func() {
		runs := ClaudeSessions()
		if len(runs) != 1 {
			t.Fatalf("runs = %d", len(runs))
		}
		if got, want := runs[0].Session.Created, time.UnixMilli(1700); !got.Equal(want) {
			t.Errorf("Created = %v, want %v", got, want)
		}
	})
}

// Two Claude processes appending to one transcript is not a state either of
// them expects, so a conversation something else still holds is forked.
func TestClaudeResumeForksOnlyWhenSomethingElseHoldsIt(t *testing.T) {
	if got := ClaudeResumeArgs("abc", false); len(got) != 2 || got[1] != "abc" {
		t.Errorf("args = %v, want a plain resume", got)
	}
	got := ClaudeResumeArgs("abc", true)
	if len(got) != 3 || got[2] != "--fork-session" {
		t.Errorf("args = %v, want the fork guard", got)
	}
}

// A conversation's own label is the row name, so resuming the same one twice
// collides. tmux refuses a duplicate rather than choosing, which from the TUI
// reads as enter simply failing — so the name is suffixed and the caller is
// told which one it got.
func TestCreateSessionWithCommandAvoidsADuplicateName(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("other\n"), nil, "tmux", "list-sessions", "-F", "#{session_name}")
		name, err := CreateSessionWithCommand("govgen-plan", "/w/govgen",
			[]string{"claude", "--resume", "abc"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "govgen-plan" {
			t.Errorf("name = %q, want the one asked for", name)
		}
		want := "tmux new-session -d -s govgen-plan -c /w/govgen claude --resume abc"
		var found bool
		for _, r := range m.runs {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("runs = %v, want %q", m.runs, want)
		}
	})
}

func TestCreateSessionWithCommandSuffixesATakenName(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("govgen-plan\ngovgen-plan-2\n"), nil,
			"tmux", "list-sessions", "-F", "#{session_name}")
		name, err := CreateSessionWithCommand("govgen-plan", "/w", []string{"claude"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "govgen-plan-3" {
			t.Errorf("name = %q, want the first free suffix", name)
		}
	})
}

// No server means no sessions, which is the right answer here — unlike
// has-session, where a tmux that cannot answer looks exactly like a free name.
func TestCreateSessionWithCommandTreatsNoServerAsNoSessions(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput(nil, fmt.Errorf("exit status 1"), "tmux", "list-sessions", "-F", "#{session_name}")
		name, err := CreateSessionWithCommand("solo", "/w", []string{"claude"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "solo" {
			t.Errorf("name = %q, want the plain name", name)
		}
	})
}

func TestCreateSessionWithCommandRefusesAnEmptyName(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte(""), nil, "tmux", "list-sessions", "-F", "#{session_name}")
		if _, err := CreateSessionWithCommand("", "/w", []string{"claude"}); err == nil {
			t.Error("an empty session name was accepted")
		}
	})
}

// tmux gives "." and ":" meaning inside a target, so a session named with
// either cannot be addressed once created — which would strand the session mux
// just made.
func TestSafeSessionNameKeepsTheSessionAddressable(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"has.dot":          "has-dot",
		"has:colon":        "has-colon",
		"  spaced  name  ": "spaced-name",
		"실행_거브젠":           "실행_거브젠",
	}
	for in, want := range cases {
		if got := SafeSessionName(in); got != want {
			t.Errorf("SafeSessionName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeSessionName(strings.Repeat("x", 80)); len(got) > 40 {
		t.Errorf("length = %d, want it capped", len(got))
	}
}

// This table is the safety specification, not a convenience.
//
// A plain --resume is safe only because the copy left behind writes nothing,
// and only a finished turn guarantees that. Loosening any row here loosens the
// premise the whole adoption rests on.
func TestAdoptableRuns(t *testing.T) {
	run := func(name string, st AIState, tmuxSession string) ClaudeRun {
		return ClaudeRun{
			Session:     Session{Name: name, AIState: st},
			SessionID:   "id-" + name,
			CWD:         "/w/" + name,
			TmuxSession: tmuxSession,
			Elsewhere:   tmuxSession == "",
		}
	}
	runs := []ClaudeRun{
		run("idle", AIStateReady, ""),
		run("already-in-tmux", AIStateReady, "sess"),
		run("working", AIStateWorking, ""),
		run("blocked", AIStateApproval, ""),
		run("shell", AIStateShell, ""),
		run("nostate", AIStateNone, ""),
	}
	got := AdoptableRuns(runs)
	if len(got) != 1 || got[0].Session.Name != "idle" {
		var names []string
		for _, r := range got {
			names = append(names, r.Session.Name)
		}
		t.Fatalf("adoptable = %v, want only the finished turn", names)
	}
}

// A row with nothing to address it by cannot be created and must not be
// attempted — the error would be reported once per launch, for ever.
func TestAdoptableRunsSkipsUnaddressableRows(t *testing.T) {
	runs := []ClaudeRun{
		{Session: Session{Name: "no-cwd", AIState: AIStateReady}, SessionID: "a"},
		{Session: Session{Name: "no-id", AIState: AIStateReady}, CWD: "/w"},
		{Session: Session{Name: "  ", AIState: AIStateReady}, SessionID: "c", CWD: "/w"},
	}
	if got := AdoptableRuns(runs); len(got) != 0 {
		t.Errorf("adoptable = %d, want none", len(got))
	}
}

// withoutSecondWait makes the batch's one-second spacing free in tests. The
// spacing exists to keep tmux-resurrect's per-second save files apart, which no
// test needs to reproduce.
func withoutSecondWait(t *testing.T) {
	t.Helper()
	old := waitForNextSecond
	waitForNextSecond = func() {}
	t.Cleanup(func() { waitForNextSecond = old })
}

func TestAdoptClaudeSessionsResumesUnderTheSameID(t *testing.T) {
	withoutSecondWait(t)
	files := map[string]string{
		"a.json": runFile(1, "sid-aaa", "/w/one", "idle", "one", "", 1000, 2000),
		"b.json": runFile(2, "sid-bbb", "/w/two", "idle", "two", "", 1000, 3000),
		// Already in tmux, and a busy one: neither may be touched.
		"c.json": runFile(3, "sid-ccc", "/w/three", "idle", "three", "sess:@0.%0", 1000, 4000),
		"d.json": runFile(4, "sid-ddd", "/w/four", "busy", "four", "", 1000, 5000),
	}
	withClaudePanes(t, files, "%0 3 sess\n", func() {
		adopted := AdoptClaudeSessions(nil)
		if len(adopted) != 2 {
			var got []string
			for _, a := range adopted {
				got = append(got, a.Run.Session.Name)
			}
			t.Fatalf("adopted = %v, want the two homeless idle ones", got)
		}
		for _, a := range adopted {
			if a.Err != nil {
				t.Errorf("%s: %v", a.Run.Session.Name, a.Err)
			}
		}
	})
}

// The whole self-limiting property rests on the id being kept, so the batch
// must never pass --fork-session.
func TestAdoptClaudeSessionsNeverForks(t *testing.T) {
	withoutSecondWait(t)
	files := map[string]string{
		"a.json": runFile(1, "sid-aaa", "/w/one", "idle", "one", "", 1000, 2000),
	}
	withClaudeSessions(t, files, nil, func() {
		m, ok := runner.(*mockRunner)
		if !ok {
			t.Fatalf("runner = %T, want the helper's mock", runner)
		}
		AdoptClaudeSessions(nil)

		var created string
		for _, r := range m.runs {
			if strings.Contains(r, "new-session") {
				created = r
			}
		}
		if created == "" {
			t.Fatalf("no session was created; runs = %v", m.runs)
		}
		if strings.Contains(created, "--fork-session") {
			t.Errorf("created %q; forking breaks the shared id that stops re-adoption", created)
		}
		if !strings.Contains(created, "--resume sid-aaa") {
			t.Errorf("created %q, want a resume of the conversation's own id", created)
		}
		if !strings.Contains(created, "-c /w/one") {
			t.Errorf("created %q, want it started in the conversation's directory", created)
		}
	})
}

func TestAdoptClaudeSessionsDoesNothingWhenEverythingHasAHome(t *testing.T) {
	withoutSecondWait(t)
	files := map[string]string{
		"c.json": runFile(3, "sid-ccc", "/w/three", "idle", "three", "sess:@0.%0", 1000, 4000),
	}
	withClaudePanes(t, files, "%0 3 sess\n", func() {
		if adopted := AdoptClaudeSessions(nil); adopted != nil {
			t.Errorf("adopted = %v, want nothing on a settled machine", adopted)
		}
	})
}
