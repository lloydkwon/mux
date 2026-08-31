package tmux

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verbatim session files captured from a live Claude Code 2.1.226 install:
// one blocked on the user, one sitting at the prompt.
const (
	fixtureWaiting = `{"pid":15767,"sessionId":"fa63b76b-20f5-47b7-a232-b64bc702d4b1","cwd":"/home/sokhoon/Projects/temp/mux","startedAt":1786273108310,"procStart":"683395","version":"2.1.226","peerProtocol":1,"kind":"interactive","entrypoint":"cli","tmux":"myname:@0.%0","messagingSocketPath":"/run/user/1000/cc-socks/15767.sock","name":"mux-ec","nameSource":"derived","status":"waiting","updatedAt":1786273497562,"statusUpdatedAt":1786273497562,"waitingFor":"input needed"}`
	fixtureIdle    = `{"pid":11458,"sessionId":"4f3a9a6c-9f32-4428-89fd-bde7fc6fccb3","cwd":"/home/sokhoon/Projects/temp/apache-camel-4.18.3","startedAt":1786272948708,"procStart":"667618","version":"2.1.226","peerProtocol":1,"kind":"interactive","entrypoint":"cli","tmux":"project:@1.%1","messagingSocketPath":"/run/user/1000/cc-socks/11458.sock","name":"apache-camel-4-18-3-78","nameSource":"derived","status":"idle","updatedAt":1786273115347,"statusUpdatedAt":1786273115347}`
)

// withClaudeSessions writes the given files into a temporary ~/.claude/sessions,
// redirects the home lookup at it, and treats every pid as alive unless the
// test overrides aliveFn.
func withClaudeSessions(t *testing.T, files map[string]string, aliveFn func(int, string) bool, fn func()) {
	t.Helper()

	home := t.TempDir()
	dir := filepath.Join(home, claudeDir, sessionsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create sessions dir: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if aliveFn == nil {
		aliveFn = func(int, string) bool { return true }
	}

	// The scan asks tmux which session each pane is in. Left on the real runner
	// that is a live `tmux list-panes -a` against whatever server the developer
	// happens to be sitting in, which is both non-hermetic and slow. An empty
	// mock answers "no pane is known", so these tests exercise the fallback to
	// the name Claude recorded; withClaudePanes is the one that supplies panes.
	oldRunner := runner
	SetRunner(newMockRunner())

	oldHome, oldAlive := homeDir, isAlive
	homeDir = func() (string, error) { return home, nil }
	isAlive = aliveFn
	resetClaudeStatusCache()
	defer func() {
		runner = oldRunner
		homeDir, isAlive = oldHome, oldAlive
		resetClaudeStatusCache()
	}()

	fn()
}

func resetClaudeStatusCache() {
	claudeStatusCacheMu.Lock()
	claudeStatusCache = cachedClaudeStatuses{}
	claudeStatusCacheMu.Unlock()
}

func TestMapClaudeState(t *testing.T) {
	tests := []struct {
		status string
		want   AIState
	}{
		{"busy", AIStateWorking},
		{"waiting", AIStateApproval},
		{"idle", AIStateReady},
		{"shell", AIStateShell},
		{"", AIStateNone},
		{"something-new", AIStateNone},
	}
	for _, tt := range tests {
		if got := mapClaudeState(tt.status); got != tt.want {
			t.Errorf("mapClaudeState(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestAIStateString(t *testing.T) {
	tests := map[AIState]string{
		AIStateNone:     "",
		AIStateWorking:  "working",
		AIStateApproval: "approval",
		AIStateReady:    "waiting",
		AIStateShell:    "shell",
	}
	for state, want := range tests {
		if got := state.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", state, got, want)
		}
	}
}

func TestTmuxSessionName(t *testing.T) {
	tests := map[string]string{
		"myname:@0.%0":  "myname",
		"a-b_c:@12.%34": "a-b_c",
		"":              "",
		"nocolon":       "",
	}
	for ref, want := range tests {
		if got := tmuxSessionName(ref); got != want {
			t.Errorf("tmuxSessionName(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestScanClaudeStatusesRealFixtures(t *testing.T) {
	files := map[string]string{
		"15767.json": fixtureWaiting,
		"11458.json": fixtureIdle,
	}
	withClaudeSessions(t, files, nil, func() {
		got := scanClaudeStatuses()
		if len(got) != 2 {
			t.Fatalf("expected 2 sessions, got %d: %v", len(got), got)
		}

		blocked := got["myname"]
		if blocked.State != AIStateApproval {
			t.Errorf("myname state = %v, want approval", blocked.State)
		}
		if blocked.WaitingFor != "input needed" {
			t.Errorf("myname waitingFor = %q", blocked.WaitingFor)
		}
		if want := time.UnixMilli(1786273497562); !blocked.Since.Equal(want) {
			t.Errorf("myname Since = %v, want %v", blocked.Since, want)
		}
		if blocked.PID != 15767 {
			t.Errorf("myname PID = %d", blocked.PID)
		}

		ready := got["project"]
		if ready.State != AIStateReady {
			t.Errorf("project state = %v, want waiting", ready.State)
		}
		if ready.WaitingFor != "" {
			t.Errorf("project waitingFor = %q, want empty for a ready session", ready.WaitingFor)
		}
	})
}

// A crashed session leaves its file behind forever. Without a liveness check it
// would display as a permanently busy ghost row.
func TestScanClaudeStatusesSkipsDeadProcess(t *testing.T) {
	files := map[string]string{"15767.json": fixtureWaiting}
	withClaudeSessions(t, files, func(int, string) bool { return false }, func() {
		if got := scanClaudeStatuses(); len(got) != 0 {
			t.Errorf("expected dead process to be skipped, got %v", got)
		}
	})
}

func TestScanClaudeStatusesPassesProcStart(t *testing.T) {
	files := map[string]string{"15767.json": fixtureWaiting}
	var gotPID int
	var gotStart string
	alive := func(pid int, start string) bool {
		gotPID, gotStart = pid, start
		return true
	}
	withClaudeSessions(t, files, alive, func() {
		scanClaudeStatuses()
		if gotPID != 15767 || gotStart != "683395" {
			t.Errorf("liveness check got (%d, %q), want (15767, \"683395\")", gotPID, gotStart)
		}
	})
}

// Two Claude sessions in different windows of one tmux session collide on the
// map key; the one that needs the user must win, regardless of read order.
func TestScanClaudeStatusesPrefersMoreUrgent(t *testing.T) {
	busy := `{"pid":100,"tmux":"shared:@0.%0","status":"busy","statusUpdatedAt":1786273497562}`
	blocked := `{"pid":200,"tmux":"shared:@1.%1","status":"waiting","waitingFor":"permission prompt","statusUpdatedAt":1786273000000}`

	for _, order := range []map[string]string{
		{"1-busy.json": busy, "2-blocked.json": blocked},
		{"1-blocked.json": blocked, "2-busy.json": busy},
	} {
		withClaudeSessions(t, order, nil, func() {
			got := scanClaudeStatuses()
			if len(got) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(got))
			}
			if s := got["shared"]; s.State != AIStateApproval || s.PID != 200 {
				t.Errorf("expected the blocked session to win, got state=%v pid=%d", s.State, s.PID)
			}
		})
	}
}

func TestScanClaudeStatusesSkipsNonTmuxSessions(t *testing.T) {
	files := map[string]string{
		"1.json": `{"pid":1,"status":"busy","statusUpdatedAt":1786273497562}`,
	}
	withClaudeSessions(t, files, nil, func() {
		if got := scanClaudeStatuses(); len(got) != 0 {
			t.Errorf("expected sessions without a tmux field to be skipped, got %v", got)
		}
	})
}

// Session files are rewritten in place, so a scan can catch a partial write.
// That must cost us one file, not the whole scan.
func TestScanClaudeStatusesToleratesTornWrites(t *testing.T) {
	files := map[string]string{
		"broken.json": `{"pid":1,"tmux":"other:@0.%0","stat`,
		"15767.json":  fixtureWaiting,
	}
	withClaudeSessions(t, files, nil, func() {
		got := scanClaudeStatuses()
		if len(got) != 1 {
			t.Fatalf("expected the valid file to survive, got %v", got)
		}
		if _, ok := got["myname"]; !ok {
			t.Errorf("expected myname to be present, got %v", got)
		}
	})
}

func TestScanClaudeStatusesIgnoresNonJSON(t *testing.T) {
	files := map[string]string{
		"notes.txt":      fixtureWaiting,
		"15767.json.tmp": fixtureWaiting,
		"11458.json":     fixtureIdle,
	}
	withClaudeSessions(t, files, nil, func() {
		got := scanClaudeStatuses()
		if len(got) != 1 {
			t.Errorf("expected only the .json file to be read, got %v", got)
		}
	})
}

func TestScanClaudeStatusesMissingDirectory(t *testing.T) {
	home := t.TempDir() // no .claude inside
	oldHome := homeDir
	homeDir = func() (string, error) { return home, nil }
	resetClaudeStatusCache()
	defer func() {
		homeDir = oldHome
		resetClaudeStatusCache()
	}()

	got := scanClaudeStatuses()
	if got == nil {
		t.Fatal("expected a non-nil map so callers can index it freely")
	}
	if len(got) != 0 {
		t.Errorf("expected an empty map, got %v", got)
	}
}

func TestClaudeStatusElapsed(t *testing.T) {
	now := time.Now()

	if got := (ClaudeStatus{}).Elapsed(now); got != 0 {
		t.Errorf("unknown start time should elapse 0, got %v", got)
	}

	past := ClaudeStatus{Since: now.Add(-3 * time.Minute)}
	if got := past.Elapsed(now); got != 3*time.Minute {
		t.Errorf("Elapsed = %v, want 3m", got)
	}

	// Clock skew must never render as a countdown.
	future := ClaudeStatus{Since: now.Add(5 * time.Second)}
	if got := future.Elapsed(now); got != 0 {
		t.Errorf("future start time should clamp to 0, got %v", got)
	}
}

func TestClaudeStatusesCaches(t *testing.T) {
	files := map[string]string{"15767.json": fixtureWaiting}
	withClaudeSessions(t, files, nil, func() {
		first := ClaudeStatuses()
		if len(first) != 1 {
			t.Fatalf("expected 1 session, got %d", len(first))
		}

		dir, err := claudeSessionsDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "15767.json")); err != nil {
			t.Fatal(err)
		}

		if second := ClaudeStatuses(); len(second) != 1 {
			t.Errorf("expected the cached result within the TTL, got %v", second)
		}
	})
}

// withClaudePanes runs fn with the given `pane_id session_name` lines standing
// in for the live pane list, so a test can rename a session out from under a
// status file the way tmux does.
func withClaudePanes(t *testing.T, files map[string]string, panes string, fn func()) {
	t.Helper()
	withClaudeSessions(t, files, nil, func() {
		m := newMockRunner()
		m.OnOutput([]byte(panes), nil, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name}")
		old := runner
		SetRunner(m)
		resetClaudeStatusCache()
		defer func() {
			runner = old
			resetClaudeStatusCache()
		}()
		fn()
	})
}

// The bug this exists for. Claude writes its tmux reference once, at startup,
// and the session half of it is a name; rename the session and the file keeps
// pointing at a session that no longer exists. Nothing errors — the badge just
// stops updating, which is indistinguishable from a session that went quiet.
// Measured in the field: `mux` renamed to `my-mux` displayed a finished turn
// for over half an hour while the file said busy and was being rewritten.
func TestScanClaudeStatusesFollowsARenamedSession(t *testing.T) {
	files := map[string]string{"15767.json": fixtureWaiting} // recorded "myname:@0.%0"

	withClaudePanes(t, files, "%0 renamed-later\n", func() {
		got := scanClaudeStatuses()

		if _, stale := got["myname"]; stale {
			t.Error("indexed under the name Claude recorded, which no session carries any more")
		}
		s, ok := got["renamed-later"]
		if !ok {
			t.Fatalf("the session holding pane %%0 has no state: %v", got)
		}
		if s.State != AIStateApproval {
			t.Errorf("state = %v, want approval", s.State)
		}
	})
}

// The pane list is the better answer, not the only one. When tmux cannot be
// asked the recorded name is all there is, and it is right far more often than
// it is wrong — a session that was never renamed.
func TestScanClaudeStatusesFallsBackToTheRecordedName(t *testing.T) {
	files := map[string]string{"15767.json": fixtureWaiting}

	// withClaudeSessions installs a mock with no answer for list-panes.
	withClaudeSessions(t, files, nil, func() {
		got := scanClaudeStatuses()

		if _, ok := got["myname"]; !ok {
			t.Errorf("no state under the recorded name with tmux unreachable: %v", got)
		}
	})
}

// A session name may contain spaces; a pane id never does, which is why the
// pane id leads the format.
func TestPaneSessionsSplitOnTheFirstSpaceOnly(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%0 my project\n%1 solo\n"), nil,
			"tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name}")

		got := paneSessions()
		if got["%0"] != "my project" {
			t.Errorf("pane %%0 = %q, want %q", got["%0"], "my project")
		}
		if got["%1"] != "solo" {
			t.Errorf("pane %%1 = %q, want %q", got["%1"], "solo")
		}
	})
}

func TestTmuxPaneID(t *testing.T) {
	cases := map[string]string{
		"myname:@0.%0":   "%0",
		"my-mux:@1.%2":   "%2",
		"sess:@10.%123":  "%123",
		"sess:@0":        "", // no pane half
		"":               "",
		"sess:@0.broken": "", // not a pane id
		"sess:@0.":       "", // 점만 남고 pane 절반이 없음
	}
	for ref, want := range cases {
		if got := tmuxPaneID(ref); got != want {
			t.Errorf("tmuxPaneID(%q) = %q, want %q", ref, got, want)
		}
	}
}
