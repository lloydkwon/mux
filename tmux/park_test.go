package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// withPark 는 보관 테스트의 판이다. withClaudeSessions 의 임시 HOME(상태 파일)에, 주어진 pane 목록을
// 답하는 목 러너와 임시 parked.json 을 얹는다. PID 대기는 0 이라 테스트가 실제 초를 쓰지 않는다.
func withPark(t *testing.T, files map[string]string, panes string, fn func(m *mockRunner, home string)) {
	t.Helper()
	withClaudeSessions(t, files, nil, func() {
		home, err := homeDir()
		if err != nil {
			t.Fatal(err)
		}
		m := newMockRunner()
		m.OnOutput([]byte(panes), nil, "tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_pid} #{session_name}")
		oldRunner, oldFile, oldWait := runner, parkedFile, parkExitTimeout
		SetRunner(m)
		path := filepath.Join(t.TempDir(), "parked.json")
		parkedFile = func() (string, error) { return path, nil }
		parkExitTimeout = 0
		resetClaudeRunCache()
		defer func() {
			runner, parkedFile, parkExitTimeout = oldRunner, oldFile, oldWait
			resetClaudeRunCache()
		}()
		fn(m, home)
	})
}

// touchTranscript 는 대화 기록 파일을 만든다 — 보관은 기록이 있는 대화만 받는다.
func touchTranscript(t *testing.T, home, cwd, sid string) {
	t.Helper()
	writeTranscriptFile(t, filepath.Join(home, claudeDir, projectsDir, encodePath(cwd), sid+".jsonl"),
		[]string{`{"type":"user","timestamp":"2026-11-03T00:00:00Z"}`})
}

func mustSaveParked(t *testing.T, list []ParkedSession) {
	t.Helper()
	if err := saveParked(list); err != nil {
		t.Fatalf("save parked: %v", err)
	}
}

func mustLoadParked(t *testing.T) []ParkedSession {
	t.Helper()
	list, err := LoadParked()
	if err != nil {
		t.Fatalf("load parked: %v", err)
	}
	return list
}

func ranAny(m *mockRunner, prefix string) bool {
	return slices.ContainsFunc(m.runs, func(r string) bool { return strings.HasPrefix(r, prefix) })
}

func TestParkedStoreRoundTripsNewestFirst(t *testing.T) {
	withPark(t, nil, "", func(*mockRunner, string) {
		if got := mustLoadParked(t); got != nil {
			t.Fatalf("no file should read as nothing parked, got %v", got)
		}
		old := ParkedSession{ID: "a", Name: "old", ParkedAt: time.Unix(1000, 0),
			Conversations: []ParkedConversation{{SessionID: "a", CWD: "/w/a"}}}
		recent := ParkedSession{ID: "b", Name: "recent", ParkedAt: time.Unix(2000, 0),
			Conversations: []ParkedConversation{{SessionID: "b", CWD: "/w/b"}}}
		nameless := ParkedSession{ID: "c", ParkedAt: time.Unix(3000, 0),
			Conversations: []ParkedConversation{{SessionID: "c"}}}
		empty := ParkedSession{ID: "d", Name: "empty", ParkedAt: time.Unix(4000, 0)}
		mustSaveParked(t, []ParkedSession{old, nameless, recent, empty})

		got := mustLoadParked(t)
		if len(got) != 2 || got[0].Name != "recent" || got[1].Name != "old" {
			t.Fatalf("parked = %+v, want recent then old, without the nameless and empty entries", got)
		}
		path, _ := parkedFile()
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("parked.json mode = %v (%v), want 0600", info.Mode().Perm(), err)
		}
	})
}

// 깨진 파일을 빈 목록으로 읽으면, 다음 쓰기가 다른 보관 항목을 전부 지운다.
func TestParkSessionLeavesTheSessionOpenWhenTheListIsCorrupt(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		path, _ := parkedFile()
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadParked(); err == nil {
			t.Fatal("a corrupt parked.json read as a list")
		}
		if _, err := ParkSession(Session{Name: "work"}); err == nil {
			t.Fatal("parking over a corrupt list succeeded")
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("killed the session although its conversation could not be recorded: %v", m.runs)
		}
	})
}

// 기록이 없는 대화(갓 띄운 Claude)는 --resume 할 것이 없다. 그런 것만 있으면 세션을 닫지 않는다.
func TestParkSessionNeedsATranscript(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(m *mockRunner, _ string) {
		_, err := ParkSession(Session{Name: "work"})
		if !errors.Is(err, ErrNothingToPark) {
			t.Fatalf("err = %v, want ErrNothingToPark", err)
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("killed a session with nothing to bring back: %v", m.runs)
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want nothing recorded", got)
		}
	})
}

// 한 세션의 대화는 전부 한 항목에, 창·pane 순서로 들어간다. 다른 세션의 대화는 들어가지 않는다.
func TestParkSessionRemembersEveryConversationInOrder(t *testing.T) {
	files := map[string]string{
		"10.json": runFile(10, "sid-late", "/w/b", "idle", "late", "work:@2.%5", 1000, 9000),
		"11.json": runFile(11, "sid-early", "/w/a", "idle", "early", "work:@1.%3", 1000, 2000),
		"12.json": runFile(12, "sid-other", "/w/c", "idle", "other", "elsewhere:@4.%8", 1000, 3000),
	}
	panes := "%3 100 work\n%5 101 work\n%8 102 elsewhere\n"
	withPark(t, files, panes, func(m *mockRunner, home string) {
		for sid, cwd := range map[string]string{"sid-late": "/w/b", "sid-early": "/w/a", "sid-other": "/w/c"} {
			touchTranscript(t, home, cwd, sid)
		}
		list, err := ParkSession(Session{Name: "work", ProjectDir: "/w", Note: "다음 주에"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("parked = %+v, want one entry", list)
		}
		p := list[0]
		if p.Name != "work" || p.ProjectDir != "/w" || p.Note != "다음 주에" || !strings.HasSuffix(p.ID, "-sid-earl") {
			t.Errorf("entry = %+v, want name, project, note and a fresh id ending in the first conversation", p)
		}
		var ids []string
		for _, c := range p.Conversations {
			ids = append(ids, c.SessionID)
		}
		if strings.Join(ids, ",") != "sid-early,sid-late" {
			t.Errorf("conversations = %v, want window/pane order and nothing from another session", ids)
		}
		if p.Conversations[0].CWD != "/w/a" || p.Conversations[0].PID != 11 || p.Conversations[0].Task != "early" {
			t.Errorf("conversation = %+v, want its cwd, pid and task", p.Conversations[0])
		}
		// = 로 정확히: «work» 가 이미 없으면 접두사 일치로 «work-2» 를 죽일 수 있다.
		if !slices.Contains(m.runs, "tmux kill-session -t =work") {
			t.Errorf("runs = %v, want an exact-match kill-session", m.runs)
		}
	})
}

func TestParkSessionReplacesAnEntryHoldingTheSameConversation(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(_ *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		mustSaveParked(t, []ParkedSession{
			{ID: "sid-a", Name: "before", ParkedAt: time.Unix(1000, 0),
				Conversations: []ParkedConversation{{SessionID: "sid-a", CWD: "/w/a"}}},
			{ID: "sid-z", Name: "unrelated", ParkedAt: time.Unix(1500, 0),
				Conversations: []ParkedConversation{{SessionID: "sid-z", CWD: "/w/z"}}},
		})
		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list) != 2 || list[0].Name != "work" || list[1].Name != "unrelated" {
			t.Errorf("parked = %+v, want the new entry in place of the old one and the unrelated one kept", list)
		}
	})
}

// kill 이 실패했는데 세션이 아직 있으면, 그 세션은 살아 있으니 보관 항목을 되돌린다.
func TestParkSessionRollsBackWhenKillFails(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		m.OnRun(errors.New("no permission"), "tmux", "kill-session", "-t", "=work")
		if _, err := ParkSession(Session{Name: "work"}); err == nil {
			t.Fatal("a failed kill reported success")
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want the entry rolled back for a session that is still open", got)
		}
	})
}

func parkedFixture(name string, convs ...ParkedConversation) ParkedSession {
	return ParkedSession{ID: convs[0].SessionID, Name: name, ParkedAt: time.Unix(1000, 0), Conversations: convs}
}

func TestRestoreParkedResumesUnderTheSameName(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, home string) {
		dirA, dirB := t.TempDir(), t.TempDir()
		touchTranscript(t, home, dirA, "sid-a")
		touchTranscript(t, home, dirB, "sid-b")
		p := parkedFixture("work",
			ParkedConversation{SessionID: "sid-a", CWD: dirA, PID: 11},
			ParkedConversation{SessionID: "sid-b", CWD: dirB, PID: 12})
		p.ProjectDir, p.Note = "/w", "다음 주에"
		mustSaveParked(t, []ParkedSession{p})

		name, err := RestoreParked(p)
		if err != nil || name != "work" {
			t.Fatalf("restore = %q, %v; want the session under its own name", name, err)
		}
		for _, want := range []string{
			"tmux new-session -d -s work -c " + dirA + " claude --resume sid-a",
			"tmux new-window -d -t =work: -c " + dirB + " claude --resume sid-b",
			"tmux set-option -t =work: @project_dir /w",
			"tmux set-option -t =work: @mux_note 다음 주에",
		} {
			if !slices.Contains(m.runs, want) {
				t.Errorf("missing %q in %v", want, m.runs)
			}
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want the restored entry gone", got)
		}
	})
}

func TestRestoreParkedSuffixesATakenName(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, home string) {
		m.OnOutput([]byte("work\n"), nil, "tmux", "list-sessions", "-F", "#{session_name}")
		dirA, dirB := t.TempDir(), t.TempDir()
		touchTranscript(t, home, dirA, "sid-a")
		touchTranscript(t, home, dirB, "sid-b")
		p := parkedFixture("work",
			ParkedConversation{SessionID: "sid-a", CWD: dirA},
			ParkedConversation{SessionID: "sid-b", CWD: dirB})
		name, err := RestoreParked(p)
		if err != nil || name != "work-2" {
			t.Fatalf("restore = %q, %v; want the suffixed name tmux accepted", name, err)
		}
		if !slices.Contains(m.runs, "tmux new-window -d -t =work-2: -c "+dirB+" claude --resume sid-b") {
			t.Errorf("runs = %v, want the second window in the session actually made", m.runs)
		}
	})
}

// 이미 tmux 에 살아 있는 대화는 또 띄우지 않는다 — 한 기록에 두 프로세스가 붙는다. 그리로 간다.
func TestRestoreParkedGoesToALiveCopy(t *testing.T) {
	files := map[string]string{"77.json": runFile(77, "sid-a", "/w/a", "idle", "a", "other:@0.%1", 1000, 2000)}
	withPark(t, files, "%1 50 other\n", func(m *mockRunner, _ string) {
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: t.TempDir(), PID: 11})
		mustSaveParked(t, []ParkedSession{p})
		name, err := RestoreParked(p)
		if err != nil || name != "other" {
			t.Fatalf("restore = %q, %v; want the session already holding the conversation", name, err)
		}
		if ranAny(m, "tmux new-session") {
			t.Errorf("started a second process on a live conversation: %v", m.runs)
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want the entry gone once its conversation is live", got)
		}
	})
}

// 보관하며 SIGHUP 을 보낸 그 프로세스가 아직 끝나는 중이면, 그것은 «살아 있는 사본» 이 아니다.
func TestRestoreParkedIgnoresTheParkedProcess(t *testing.T) {
	files := map[string]string{"11.json": runFile(11, "sid-a", "/w/a", "busy", "a", "", 1000, 2000)}
	withPark(t, files, "", func(m *mockRunner, home string) {
		dir := t.TempDir()
		touchTranscript(t, home, dir, "sid-a")
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: dir, PID: 11})
		if _, err := RestoreParked(p); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if !slices.Contains(m.runs, "tmux new-session -d -s work -c "+dir+" claude --resume sid-a") {
			t.Errorf("runs = %v, want a plain resume — not a fork against the dying parked process", m.runs)
		}
	})
}

// tmux 밖에서 지금 작업 중인 사본이 있으면 --claude 의 enter 처럼 포크한다.
func TestRestoreParkedForksAConversationBusyElsewhere(t *testing.T) {
	files := map[string]string{"77.json": runFile(77, "sid-a", "/w/a", "busy", "a", "", 1000, 2000)}
	withPark(t, files, "", func(m *mockRunner, home string) {
		dir := t.TempDir()
		touchTranscript(t, home, dir, "sid-a")
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: dir, PID: 11})
		if _, err := RestoreParked(p); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if !slices.Contains(m.runs, "tmux new-session -d -s work -c "+dir+" claude --resume sid-a --fork-session") {
			t.Errorf("runs = %v, want a forked resume", m.runs)
		}
	})
}

// 띄우지 못한 대화는 항목에 남는다 — 다음에 Parked 행으로 다시 보이는 것이 실패의 보고다.
func TestRestoreParkedKeepsAConversationWhoseDirectoryIsGone(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, home string) {
		dir := t.TempDir()
		touchTranscript(t, home, dir, "sid-a")
		gone := filepath.Join(t.TempDir(), "removed-worktree")
		p := parkedFixture("work",
			ParkedConversation{SessionID: "sid-a", CWD: dir},
			ParkedConversation{SessionID: "sid-b", CWD: gone})
		mustSaveParked(t, []ParkedSession{p})

		name, err := RestoreParked(p)
		if name != "work" || err == nil {
			t.Fatalf("restore = %q, %v; want the session made and the missing directory reported", name, err)
		}
		if ranAny(m, "tmux new-window") {
			t.Errorf("opened a window in a directory that is gone: %v", m.runs)
		}
		got := mustLoadParked(t)
		if len(got) != 1 || len(got[0].Conversations) != 1 || got[0].Conversations[0].SessionID != "sid-b" {
			t.Errorf("parked = %+v, want only the conversation that did not come back", got)
		}
	})
}

func TestRestoreParkedKeepsTheEntryWhenNothingStarts(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, home string) {
		dir := t.TempDir()
		touchTranscript(t, home, dir, "sid-a")
		m.OnRun(errors.New("server exited"), "tmux", "new-session", "-d", "-s", "work", "-c", dir, "claude", "--resume", "sid-a")
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: dir})
		mustSaveParked(t, []ParkedSession{p})
		name, err := RestoreParked(p)
		if name != "" || err == nil {
			t.Fatalf("restore = %q, %v; want no name and the error", name, err)
		}
		if got := mustLoadParked(t); len(got) != 1 || len(got[0].Conversations) != 1 {
			t.Errorf("parked = %+v, want the entry untouched", got)
		}
	})
}

func TestForgetParkedDropsOnlyThatEntry(t *testing.T) {
	withPark(t, nil, "", func(*mockRunner, string) {
		mustSaveParked(t, []ParkedSession{
			parkedFixture("one", ParkedConversation{SessionID: "sid-1"}),
			parkedFixture("two", ParkedConversation{SessionID: "sid-2"}),
		})
		if err := ForgetParked("sid-1"); err != nil {
			t.Fatalf("forget: %v", err)
		}
		got := mustLoadParked(t)
		if len(got) != 1 || got[0].Name != "two" {
			t.Errorf("parked = %+v, want only the other entry", got)
		}
		if err := ForgetParked("missing"); err != nil {
			t.Errorf("forgetting an id that is not there = %v, want nil", err)
		}
	})
}

// kill 이 실패했지만 세션이 이미 없으면, 항목이 대화로 가는 유일한 길이다 — 남긴다.
func TestParkSessionKeepsTheEntryWhenTheSessionIsAlreadyGone(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		m.OnRun(errors.New("can't find session"), "tmux", "kill-session", "-t", "=work")
		m.OnRun(errors.New("can't find session"), "tmux", "has-session", "-t", "=work")
		if _, err := ParkSession(Session{Name: "work"}); err != nil {
			t.Fatalf("park = %v, want success for a session that is gone either way", err)
		}
		if got := mustLoadParked(t); len(got) != 1 {
			t.Errorf("parked = %+v, want the entry kept", got)
		}
	})
}

// 같은 대화를 다시 보관하면 그 대화만 새 항목으로 옮겨 간다. 옛 항목의 다른 대화는 남는다.
func TestParkSessionMovesOnlyTheOverlappingConversation(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(_ *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		mustSaveParked(t, []ParkedSession{{ID: "old", Name: "pair", ParkedAt: time.Unix(1000, 0),
			Conversations: []ParkedConversation{{SessionID: "sid-a", CWD: "/w/a"}, {SessionID: "sid-b", CWD: "/w/b"}}}})
		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list) != 2 || list[1].ID != "old" || len(list[1].Conversations) != 1 || list[1].Conversations[0].SessionID != "sid-b" {
			t.Errorf("parked = %+v, want the old entry kept with only sid-b", list)
		}
	})
}

// 리뷰가 찾은 사고: 일부만 복원돼 남은 항목과, 복원된 쪽을 다시 보관한 항목이 같은 열쇠를 가지면
// 새 항목을 지우는 순간 남아 있던 대화까지 지워진다. 열쇠는 보관할 때마다 새로 만든다.
func TestPartialRestoreThenReparkKeepsTheOtherConversation(t *testing.T) {
	withPark(t, nil, "%1 50 work\n", func(m *mockRunner, home string) {
		dirA, dirB := t.TempDir(), t.TempDir()
		touchTranscript(t, home, dirA, "sid-a")
		touchTranscript(t, home, dirB, "sid-b")
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: dirA}, ParkedConversation{SessionID: "sid-b", CWD: dirB})
		mustSaveParked(t, []ParkedSession{p})
		m.OnRun(errors.New("no space"), "tmux", "new-window", "-d", "-t", "=work:", "-c", dirB, "claude", "--resume", "sid-b")
		if name, err := RestoreParked(p); name != "work" || err == nil {
			t.Fatalf("restore = %q, %v; want the session and the failed window reported", name, err)
		}

		// 되살린 sid-a 가 그 세션에서 돈다 — 그것을 다시 보관한다.
		sessions := filepath.Join(home, claudeDir, sessionsDir)
		if err := os.WriteFile(filepath.Join(sessions, "21.json"),
			[]byte(runFile(21, "sid-a", dirA, "idle", "a", "work:@0.%1", 1000, 2000)), 0o600); err != nil {
			t.Fatal(err)
		}
		list, err := ParkSession(Session{Name: "work"})
		if err != nil || len(list) != 2 || list[0].ID == list[1].ID {
			t.Fatalf("re-park = %+v, %v; want two entries with distinct ids", list, err)
		}
		if err := ForgetParked(list[0].ID); err != nil {
			t.Fatal(err)
		}
		got := mustLoadParked(t)
		if len(got) != 1 || got[0].Conversations[0].SessionID != "sid-b" {
			t.Errorf("parked = %+v, want sid-b still parked after forgetting the re-parked sid-a", got)
		}
	})
}

// Claude 는 오래된 기록을 지운다. 기록 없이 --resume 하면 pane 이 곧 끝나므로 띄우지 않고 남긴다.
func TestRestoreParkedKeepsAConversationWhoseTranscriptIsGone(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: t.TempDir()})
		mustSaveParked(t, []ParkedSession{p})
		if name, err := RestoreParked(p); name != "" || err == nil {
			t.Fatalf("restore = %q, %v; want nothing started", name, err)
		}
		if ranAny(m, "tmux new-session") {
			t.Errorf("resumed a conversation with no transcript: %v", m.runs)
		}
		if got := mustLoadParked(t); len(got) != 1 {
			t.Errorf("parked = %+v, want the entry kept", got)
		}
	})
}

// tmux 는 -c 를 포맷으로 펼친다 — #(…) 는 셸 명령이다(17절). 디렉터리의 # 는 ## 로 넘긴다.
func TestRestoreParkedEscapesHashInDirectories(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, home string) {
		base := t.TempDir()
		dirA, dirB := filepath.Join(base, "a#(x)"), filepath.Join(base, "b#{y}")
		for _, d := range []string{dirA, dirB} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		touchTranscript(t, home, dirA, "sid-a")
		touchTranscript(t, home, dirB, "sid-b")
		p := parkedFixture("work", ParkedConversation{SessionID: "sid-a", CWD: dirA}, ParkedConversation{SessionID: "sid-b", CWD: dirB})
		if _, err := RestoreParked(p); err != nil {
			t.Fatalf("restore: %v", err)
		}
		for _, want := range []string{
			"tmux new-session -d -s work -c " + filepath.Join(base, "a##(x)") + " claude --resume sid-a",
			"tmux new-window -d -t =work: -c " + filepath.Join(base, "b##{y}") + " claude --resume sid-b",
		} {
			if !slices.Contains(m.runs, want) {
				t.Errorf("missing %q in %v", want, m.runs)
			}
		}
	})
}

func TestLoadParkedKeepsOneEntryPerID(t *testing.T) {
	withPark(t, nil, "", func(*mockRunner, string) {
		mustSaveParked(t, []ParkedSession{
			{ID: "same", Name: "older", ParkedAt: time.Unix(1000, 0), Conversations: []ParkedConversation{{SessionID: "a"}}},
			{ID: "same", Name: "newer", ParkedAt: time.Unix(2000, 0), Conversations: []ParkedConversation{{SessionID: "b"}}},
		})
		if got := mustLoadParked(t); len(got) != 1 || got[0].Name != "newer" {
			t.Errorf("parked = %+v, want only the newest entry for a shared id", got)
		}
	})
}
