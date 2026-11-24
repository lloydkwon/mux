package tmux

import (
	"fmt"
	"os"
	"path/filepath"
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

// adoptsConversationAt 는 cwdFor 가 정한 폴더의 idle 대화 하나를 상태 파일로 두고,
// 스캔(ClaudeSessions)을 거쳐 AdoptableRuns 가 그 대화를 고르는지 답한다.
//
// 상태 파일은 fn 안에서 쓴다. 기본 AO 폴더는 임시 home 아래에 있어야 하는데,
// withClaudeSessions 가 home 을 안에서 만들기 때문이다. AO_DATA_DIR 은 부르는 쪽이
// t.Setenv 로 정한다 — AO 세션 안에서 돌면 환경에 이미 서 있을 수 있다.
func adoptsConversationAt(t *testing.T, cwdFor func(home string) string) bool {
	t.Helper()
	var adopted bool
	withClaudeSessions(t, nil, nil, func() {
		home, err := homeDir()
		if err != nil {
			t.Fatalf("homeDir: %v", err)
		}
		cwd := cwdFor(home)
		file := filepath.Join(home, claudeDir, sessionsDir, "ao.json")
		content := runFile(1, "sid-ao", cwd, "idle", "conv", "", 1000, 2000)
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatalf("write state file: %v", err)
		}
		resetClaudeRunCache()

		runs := ClaudeSessions()
		// AO 가 관리하는 대화도 목록에서는 숨기지 않는다 — 입양만 하지 않는다.
		if len(runs) != 1 {
			t.Fatalf("runs = %d, want the conversation at %s listed", len(runs), cwd)
		}
		adopted = len(AdoptableRuns(runs)) == 1
	})
	return adopted
}

// Agent Orchestrator(AO)가 띄운 대화는 턴 사이에 idle 로 보이지만 AO 가 원래
// 프로세스에 다음 메시지를 계속 보낸다. 입양하면 같은 대화 id 를 두 프로세스가
// 잡는다(이슈 #1). 판정은 cwd 가 AO 데이터 폴더 아래인지다.
func TestAdoptableRunsSkipsAOManagedConversations(t *testing.T) {
	t.Run("default AO data dir is skipped", func(t *testing.T) {
		t.Setenv("AO_DATA_DIR", "")
		if adoptsConversationAt(t, func(home string) string {
			return filepath.Join(home, ".ao", "data", "worktrees", "mux", "feat-x")
		}) {
			t.Error("a conversation under ~/.ao/data was adopted")
		}
	})

	t.Run("AO_DATA_DIR is skipped", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "custom-ao") // 없어도 된다
		t.Setenv("AO_DATA_DIR", custom)
		if adoptsConversationAt(t, func(string) string {
			return filepath.Join(custom, "worktrees", "mux", "feat-x")
		}) {
			t.Error("a conversation under AO_DATA_DIR was adopted")
		}
	})

	t.Run("default stays skipped when AO_DATA_DIR is set", func(t *testing.T) {
		t.Setenv("AO_DATA_DIR", filepath.Join(t.TempDir(), "custom-ao"))
		if adoptsConversationAt(t, func(home string) string {
			return filepath.Join(home, ".ao", "data", "worktrees", "mux", "orchestrator", "o1")
		}) {
			t.Error("AO_DATA_DIR replaced the default root instead of adding to it")
		}
	})

	t.Run("sibling prefix is adopted", func(t *testing.T) {
		t.Setenv("AO_DATA_DIR", "")
		if !adoptsConversationAt(t, func(home string) string {
			return filepath.Join(home, ".ao", "data-backup", "x")
		}) {
			t.Error("~/.ao/data-backup was taken for ~/.ao/data — the prefix needs a boundary")
		}
	})

	t.Run("plain directory is adopted", func(t *testing.T) {
		t.Setenv("AO_DATA_DIR", "")
		if !adoptsConversationAt(t, func(string) string { return "/w/plain" }) {
			t.Error("an ordinary conversation was not adopted")
		}
	})

	t.Run("real path under a symlinked root is skipped", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real-ao")
		if err := os.MkdirAll(filepath.Join(real, "worktrees", "mux", "feat-x"), 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "link-ao")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		// Claude 는 물리 경로를 적는다. t.TempDir() 자체가 심링크 아래일 수 있으니
		// 실제 경로는 풀어서 구한다.
		physical, err := filepath.EvalSymlinks(real)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("AO_DATA_DIR", link)
		if adoptsConversationAt(t, func(string) string {
			return filepath.Join(physical, "worktrees", "mux", "feat-x")
		}) {
			t.Error("the physical path under a symlinked AO_DATA_DIR was adopted")
		}
	})

	t.Run("relative or root AO_DATA_DIR is ignored", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		cases := []struct{ env, cwd string }{
			// 상대 경로를 이 프로세스의 폴더 기준으로 풀면 이 cwd 가 걸린다.
			{env: "rel-ao", cwd: filepath.Join(wd, "rel-ao", "x")},
			// "/" 를 받아들이면 모든 대화가 막힌다.
			{env: "/", cwd: "/w/plain"},
		}
		for _, c := range cases {
			t.Setenv("AO_DATA_DIR", c.env)
			if !adoptsConversationAt(t, func(string) string { return c.cwd }) {
				t.Errorf("AO_DATA_DIR=%q blocked %s; it should be ignored", c.env, c.cwd)
			}
		}
	})
}

// 시작할 때의 입양 전체로 본다: AO 대화와 평범한 대화가 함께 있으면 평범한 것만
// 세션이 생긴다.
func TestAdoptClaudeSessionsLeavesAOManagedAlone(t *testing.T) {
	withoutSecondWait(t)
	t.Setenv("AO_DATA_DIR", "")
	files := map[string]string{
		"plain.json": runFile(2, "sid-plain", "/w/plain", "idle", "plain", "", 1000, 2000),
	}
	withClaudeSessions(t, files, nil, func() {
		m, ok := runner.(*mockRunner)
		if !ok {
			t.Fatalf("runner = %T, want the helper's mock", runner)
		}
		home, err := homeDir()
		if err != nil {
			t.Fatalf("homeDir: %v", err)
		}
		aoCWD := filepath.Join(home, ".ao", "data", "worktrees", "mux", "feat-x")
		content := runFile(1, "sid-ao", aoCWD, "idle", "ao-worker", "", 1000, 3000)
		if err := os.WriteFile(filepath.Join(home, claudeDir, sessionsDir, "ao.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("write state file: %v", err)
		}
		resetClaudeRunCache()

		AdoptClaudeSessions(nil)

		var created []string
		for _, r := range m.runs {
			if strings.Contains(r, "new-session") {
				created = append(created, r)
			}
		}
		if len(created) != 1 {
			t.Fatalf("new-session calls = %v, want exactly the ordinary conversation", created)
		}
		if !strings.Contains(created[0], "--resume sid-plain") {
			t.Errorf("created %q, want the ordinary conversation resumed", created[0])
		}
	})
}

// herdr 가 쥔 대화는 목록에는 보이되 입양하지 않는다 — AO 와 같은 꼴이다. herdr-loop 는 idle 인 실행세션에
// `herdr agent prompt` 로 다음 지시를 넣으니, 여기서 같은 id 로 resume 하면 두 프로세스가 한 대화를 잡는다.
// tmux 는 herdr 를 모르고, 무엇이 쥐었는지는 SetExternalHolds 로 받는다.
func TestAdoptableRunsSkipsConversationsHeldElsewhere(t *testing.T) {
	t.Setenv("AO_DATA_DIR", "")
	cases := []struct {
		name         string
		holds        ExternalHolds
		held, unsure bool
	}{
		{name: "off by default", holds: ExternalHolds{}},
		{name: "held by its id", holds: ExternalHolds{By: "herdr", IDs: map[string]bool{"sid-1": true}}, held: true},
		{name: "another id is not this one", holds: ExternalHolds{By: "herdr", IDs: map[string]bool{"sid-other": true}}},
		{name: "an agent that told no id, in the same folder", holds: ExternalHolds{By: "herdr", Dirs: map[string]bool{"/w/plain": true}}, held: true},
		{name: "an agent that told no id, elsewhere", holds: ExternalHolds{By: "herdr", Dirs: map[string]bool{"/w/other": true}}},
		{name: "unreadable counts as held", holds: ExternalHolds{By: "herdr", Unknown: true}, held: true, unsure: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"a.json": runFile(1, "sid-1", "/w/plain", "idle", "conv", "", 1000, 2000)}
			withClaudeSessions(t, files, nil, func() {
				SetExternalHolds(func() ExternalHolds { return tc.holds })
				defer SetExternalHolds(nil)
				runs := ClaudeSessions()
				if len(runs) != 1 {
					t.Fatalf("runs = %d, want the conversation listed either way", len(runs))
				}
				r := runs[0]
				if (r.HeldBy == "herdr") != tc.held || r.HeldUnsure != tc.unsure {
					t.Errorf("HeldBy=%q HeldUnsure=%v, want held=%v unsure=%v", r.HeldBy, r.HeldUnsure, tc.held, tc.unsure)
				}
				if adopted := len(AdoptableRuns(runs)) == 1; adopted == tc.held {
					t.Errorf("adopted = %v, want %v", adopted, !tc.held)
				}
			})
		})
	}
}

// tmux 안의 대화는 묻지 않는다 — Enter 가 그 세션으로 갈 뿐 아무것도 시작하지 않는다. «못 읽음» 이어도 그렇다.
func TestHoldsLeaveConversationsInTmuxAlone(t *testing.T) {
	files := map[string]string{"tmux.json": runFile(2, "sid-t", "/w/proj", "idle", "in-tmux", "sess:@0.%0", 1000, 2000)}
	withClaudePanes(t, files, "%0 2 sess\n", func() {
		SetExternalHolds(func() ExternalHolds {
			return ExternalHolds{By: "herdr", IDs: map[string]bool{"sid-t": true}, Unknown: true}
		})
		defer SetExternalHolds(nil)
		runs := ClaudeSessions()
		if len(runs) != 1 || runs[0].TmuxSession != "sess" {
			t.Fatalf("runs = %+v, want the conversation in tmux", runs)
		}
		if runs[0].HeldBy != "" {
			t.Errorf("HeldBy = %q for a conversation in tmux", runs[0].HeldBy)
		}
	})
}

// 시작할 때의 입양 전체로 본다: herdr 가 쥔 대화와 평범한 대화가 함께 있으면 평범한 것만 세션이 생긴다.
func TestAdoptClaudeSessionsLeavesHerdrHeldAlone(t *testing.T) {
	withoutSecondWait(t)
	t.Setenv("AO_DATA_DIR", "")
	files := map[string]string{
		"plain.json": runFile(2, "sid-plain", "/w/plain", "idle", "plain", "", 1000, 2000),
		"herdr.json": runFile(1, "sid-herdr", "/w/loop", "idle", "loop-exec", "", 1000, 3000),
	}
	withClaudeSessions(t, files, nil, func() {
		m, ok := runner.(*mockRunner)
		if !ok {
			t.Fatalf("runner = %T, want the helper's mock", runner)
		}
		SetExternalHolds(func() ExternalHolds {
			return ExternalHolds{By: "herdr", IDs: map[string]bool{"sid-herdr": true}}
		})
		defer SetExternalHolds(nil)

		AdoptClaudeSessions(nil)

		var created []string
		for _, r := range m.runs {
			if strings.Contains(r, "new-session") {
				created = append(created, r)
			}
		}
		if len(created) != 1 || !strings.Contains(created[0], "--resume sid-plain") {
			t.Fatalf("new-session calls = %v, want only the ordinary conversation resumed", created)
		}
	})
}
