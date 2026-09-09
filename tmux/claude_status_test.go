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

	oldHome, oldAlive, oldUnder := homeDir, isAlive, pidInPane
	homeDir = func() (string, error) { return home, nil }
	isAlive = aliveFn
	// 기본은 「모른다」다. 진짜 /proc 을 읽게 두면 픽스처의 낮은 pid 가 이 기계에
	// 실제로 존재해 답이 기계마다 달라진다 — 한 번 그렇게 깨졌다.
	pidInPane = func(int, int) (bool, bool) { return false, false }
	resetClaudeStatusCache()
	resetClaudeRunCache()
	defer func() {
		runner = oldRunner
		homeDir, isAlive, pidInPane = oldHome, oldAlive, oldUnder
		resetClaudeStatusCache()
		resetClaudeRunCache()
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
		m.OnOutput([]byte(panes), nil, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}")
		old := runner
		SetRunner(m)
		resetClaudeStatusCache()
		resetClaudeRunCache()
		defer func() {
			runner = old
			resetClaudeStatusCache()
			resetClaudeRunCache()
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

	withClaudePanes(t, files, "%0 111 renamed-later\n", func() {
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

// A session name may contain spaces; neither a pane id nor a pid ever does,
// which is why those two lead the format and the name takes the remainder.
func TestPaneSessionsSplitOnTheFirstSpacesOnly(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%0 111 my project\n%1 222 solo\n"), nil,
			"tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}")

		got := paneSessions()
		if got["%0"].session != "my project" || got["%0"].pid != 111 {
			t.Errorf("pane %%0 = %+v, want {my project 111}", got["%0"])
		}
		if got["%1"].session != "solo" || got["%1"].pid != 222 {
			t.Errorf("pane %%1 = %+v, want {solo 222}", got["%1"])
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

// A hand-set name does not move: measured, two renamed sessions carried the
// same label for a day. Repeating a fixed label on every transition is what the
// Name field exists to stop, so the user's own naming is dropped and the panel
// reports the turn's cost instead.
func TestClaudeTaskNameDropsAUserSetName(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"derived", "panel-work"},
		{"auto", "panel-work"},
		// Missing on 12 of 67 files here — older Claude builds wrote none. That
		// is not evidence either way, and treating it as the tool's is what mux
		// did before it read the field at all.
		{"", "panel-work"},
		{"user", ""},
	}
	for _, c := range cases {
		got := claudeTaskName(claudeSessionFile{Name: " panel-work ", NameSource: c.source})
		if got != c.want {
			t.Errorf("nameSource %q: got %q, want %q", c.source, got, c.want)
		}
	}
}

// pane id 가 맞아도 그 프로세스가 거기 살지 않으면 세션을 차지하면 안 된다.
//
// tmux 서버가 새로 뜨면 pane id 는 %0 부터 다시 시작한다. 그래서 이전 서버가 쓴
// 상태 파일이 지금 서버의 남의 pane 을 정확히 가리킬 수 있다. 실측: 3일 된
// `dimont-onboarding:@0.%0` 이 현재 서버의 %0(=my-mux) 에 붙어, 방금 무언가를 한
// 세션 옆에 죽은 세션의 「작업 완료」가 떴다. 가르는 것은 이름이 아니라 프로세스다.
func TestScanClaudeStatusesIgnoresAReusedPaneID(t *testing.T) {
	live := `{"pid":100,"tmux":"work:@0.%0","status":"shell","statusUpdatedAt":1786273497562}`
	ghost := `{"pid":200,"tmux":"gone:@0.%0","status":"idle","statusUpdatedAt":1786000000000}`

	withClaudePanes(t, map[string]string{"1-live.json": live, "2-ghost.json": ghost},
		"%0 111 work\n", func() {
			// pane %0 의 프로세스는 111 이고, 거기 사는 것은 100 뿐이다.
			pidInPane = func(pid, anc int) (bool, bool) { return pid == 100 && anc == 111, true }
			got := scanClaudeStatuses()
			s, ok := got["work"]
			if !ok {
				t.Fatal("살아 있는 세션이 사라졌다")
			}
			if s.PID != 100 {
				t.Errorf("pid %d 가 이겼다 — 재사용된 pane id 로 유령이 세션을 차지했다", s.PID)
			}
			if len(got) != 1 {
				t.Errorf("항목이 %d 개다: %v — 유령이 자기 항목을 만들었다", len(got), got)
			}
		})
}

// 없어진 pane 을 가리키는 상태 파일도 그 세션을 차지하면 안 된다.
//
// 프로세스가 자기 pane 보다 오래 살 수 있다(창을 닫아도 프로세스는 돈다). 그 파일이
// 세션 «이름» 으로 폴백하면 살아 있는 세션의 행을 유령이 가져간다. 실측: my-mux 에
// 파일이 둘이었고, 없는 창 @27.%83 의 이틀 된 idle 이 배경 셸 때문에 shell(1) 이 된
// 살아 있는 쪽을 urgency 로 눌렀다.
func TestScanClaudeStatusesIgnoresAGhostPane(t *testing.T) {
	live := `{"pid":100,"tmux":"work:@0.%0","status":"shell","statusUpdatedAt":1786273497562}`
	ghost := `{"pid":200,"tmux":"work:@27.%83","status":"idle","statusUpdatedAt":1786000000000}`

	withClaudePanes(t, map[string]string{"1-live.json": live, "2-ghost.json": ghost},
		"%0 111 work\n", func() {
			got := scanClaudeStatuses()
			s, ok := got["work"]
			if !ok {
				t.Fatal("살아 있는 세션이 사라졌다")
			}
			if s.PID != 100 {
				t.Errorf("pid %d 가 이겼다 — 없는 pane 의 유령이 세션을 차지했다", s.PID)
			}
		})
}

// 폴백 자체는 남아 있어야 한다. pane 목록을 «아예 못 읽었을» 때는 Claude 가 적어 둔
// 이름이 가진 전부이고, 그때 버리면 tmux 밖에서 도는 mux 가 배지를 통째로 잃는다.
func TestScanClaudeStatusesFallsBackWhenPanesUnreadable(t *testing.T) {
	files := map[string]string{
		"1.json": `{"pid":100,"tmux":"work:@0.%0","status":"busy","statusUpdatedAt":1786273497562}`,
	}
	// withClaudeSessions 의 빈 mock 은 list-panes 에 에러를 돌려준다 = nil 목록.
	withClaudeSessions(t, files, nil, func() {
		if s, ok := scanClaudeStatuses()["work"]; !ok || s.PID != 100 {
			t.Errorf("pane 목록을 못 읽었을 때 이름 폴백이 사라졌다: %v", scanClaudeStatuses())
		}
	})
}
