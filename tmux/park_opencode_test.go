package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// opencode 보관의 판 — withPark 위에 /proc 스캔 결과와 opencode API 의 답을 얹는다.

const ocBin = "/oc/bin/opencode"

// ocPanesArgs 는 opencode 가 pane 을 찾을 때 쓰는 list-panes 의 목 키다.
var ocPanesArgs = []string{"list-panes", "-a", "-F", "#{window_id} #{pane_id} #{pane_pid} #{session_name}"}

// withOpencodeProcs 는 /proc 스캔이 procs 를 돌려주게 한다. withPark 안에서만 부른다 — 되돌리는 것은
// withPark 다(여기서 t.Cleanup 으로 되돌리면 withPark 가 끝난 뒤에 그 판의 빈 스캔을 다시 꽂는다).
func withOpencodeProcs(t *testing.T, procs []opencodeProc) {
	t.Helper()
	scanOpencodeProcs = func() []opencodeProc { return procs }
}

func ocListArgs(dir string) []string {
	return []string{"api", "session.list", "--param", "directory=" + dir, "--param", "parentID=null",
		"--param", "order=desc", "--param", "limit=50"}
}

func ocGetArgs(id string) []string {
	return []string{"api", "session.get", "--param", "sessionID=" + id}
}

// ocNotFound 는 없는 대화를 물었을 때의 stdout 이다. 종료 코드는 1, stderr 는 «HTTP 404 Not Found» (2.2.7 실측).
func ocNotFound(id string) []byte {
	return []byte(`{"_tag":"SessionNotFoundError","sessionID":"` + id + `","message":"Session not found: ` + id + `"}` + "\n")
}

// ocTUI 는 pane 의 첫 프로세스(panePID) 밑에서 도는 opencode TUI 다.
func ocTUI(pid, panePID int, dir string, startMs int64) opencodeProc {
	return opencodeProc{PID: pid, Lineage: []int{pid, panePID, 1}, Exe: ocBin, Dir: dir, StartMs: startMs, TUI: true}
}

func TestParseOpencodeArgs(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		ok, tui    bool
		session    string
		continuing bool
	}{
		{args: nil, ok: true, tui: true},
		{args: []string{"/w/dir"}, ok: true, tui: true},
		{args: []string{"-s", "ses_a"}, ok: true, tui: true, session: "ses_a"},
		{args: []string{"--session=ses_b", "--auto"}, ok: true, tui: true, session: "ses_b"},
		{args: []string{"--log-level", "debug", "-c"}, ok: true, tui: true, continuing: true},
		// 폴더 인자 뒤의 플래그도 읽는다. 모르는 플래그의 값(x/y)은 폴더 인자로 읽혀도 해가 없다.
		{args: []string{".", "-s", "ses_c"}, ok: true, tui: true, session: "ses_c"},
		{args: []string{"-m", "x/y", "-c"}, ok: true, tui: true, continuing: true},
		// 원격 서버의 대화는 이 기계의 목록에 없다 — 같은 폴더의 로컬 대화를 고르면 틀린 대화다.
		{args: []string{"--server", "http://h:1", "."}},
		{args: []string{"--server=http://h:1", "run", "hi"}},
		// 대화를 만드는 하위 명령 — 보관하지는 않지만, 같은 폴더의 대화를 누구 것인지 모르게 한다.
		{args: []string{"run", "--auto", "-m", "x/y", "hello"}, ok: true},
		{args: []string{"--log-level", "info", "run", "hi"}, ok: true},
		{args: []string{"mini"}, ok: true},
		{args: []string{"acp"}, ok: true},
		// 서비스와 관리 명령은 대화를 들고 있지 않다.
		{args: []string{"serve", "--service"}},
		{args: []string{"session", "list"}},
		{args: []string{"api", "session.list"}},
		{args: []string{"--version"}},
	} {
		got, ok := parseOpencodeArgs(tc.args)
		if ok != tc.ok || (ok && (got.tui != tc.tui || got.session != tc.session || got.continuing != tc.continuing)) {
			t.Errorf("parseOpencodeArgs(%q) = %+v, %v; want ok=%v tui=%v session=%q continuing=%v",
				tc.args, got, ok, tc.ok, tc.tui, tc.session, tc.continuing)
		}
	}
}

// 보관할 대화는 TUI 가 뜬 뒤 그 폴더에서 갱신된 최상위 대화다. 보관된(archived) 것, 다른 폴더의 것, TUI 가
// 뜨기 전에 끝난 대화는 아니다.
func TestParkSessionKeepsTheOpencodeConversation(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n@4 %8 200 other\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[
			{"id":"ses_archived","title":"old","time":{"created":1,"updated":30000,"archived":31000},"location":{"directory":"/w/oc"}},
			{"id":"ses_now","title":"fix the tests","time":{"created":9000,"updated":20000},"location":{"directory":"/w/oc"}},
			{"id":"ses_before","title":"yesterday","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}},
			{"id":"ses_elsewhere","time":{"created":1,"updated":25000},"location":{"directory":"/w/other"}},
			{"id":"ses_nodir","time":{"created":1,"updated":25000}}
		],"cursor":{}}`), nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list) != 1 || len(list[0].Conversations) != 1 {
			t.Fatalf("parked = %+v, want one entry with one conversation", list)
		}
		c := list[0].Conversations[0]
		want := ParkedConversation{SessionID: "ses_now", CWD: "/w/oc", Task: "fix the tests", PID: 500,
			Tool: toolOpencode, Command: ocBin}
		if c != want {
			t.Errorf("conversation = %+v, want %+v", c, want)
		}
		if !slices.Contains(m.runs, "tmux kill-session -t =work") {
			t.Errorf("runs = %v, want the session closed", m.runs)
		}
	})
}

// TUI 는 탭으로 여러 대화를 오간다. 뜬 뒤 갱신된 대화는 전부, 최신 먼저 들어간다. «뜬 뒤» 의 경계는 시작
// 시각에서 1초 여유를 뺀 곳이다(시작 시각이 1초까지 어긋난다).
func TestParkSessionKeepsEveryOpencodeConversationTouchedSinceStart(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[
			{"id":"ses_edge","time":{"created":1,"updated":9000},"location":{"directory":"/w/oc"}},
			{"id":"ses_new","time":{"created":1,"updated":20000},"location":{"directory":"/w/oc"}},
			{"id":"ses_just_before","time":{"created":1,"updated":8999},"location":{"directory":"/w/oc"}}
		]}`), nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		var ids []string
		for _, c := range list[0].Conversations {
			ids = append(ids, c.SessionID)
		}
		if strings.Join(ids, ",") != "ses_new,ses_edge" {
			t.Errorf("conversations = %v, want ses_new,ses_edge", ids)
		}
	})
}

// 리뷰가 찾은 구멍: 같은 폴더의 다른 세션이 보관되며 남긴 대화도 «TUI 가 뜬 뒤 갱신됨» 이다. 그것을 이 세션
// 항목으로 옮기면 그 세션의 항목이 이름·메모째 사라진다. 이미 보관함에 든 대화는 고르지 않는다.
func TestOpencodeConversationAlreadyParkedStaysInItsEntry(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		mustSaveParked(t, []ParkedSession{{ID: "other-entry", Name: "other", Note: "그쪽 메모", ParkedAt: time.Unix(1000, 0),
			Conversations: []ParkedConversation{{SessionID: "ses_theirs", CWD: "/w/oc", Tool: toolOpencode, Command: ocBin}}}})
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[
			{"id":"ses_theirs","time":{"created":1,"updated":30000},"location":{"directory":"/w/oc"}},
			{"id":"ses_mine","time":{"created":1,"updated":20000},"location":{"directory":"/w/oc"}}
		]}`), nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list) != 2 || list[0].Name != "work" || len(list[0].Conversations) != 1 || list[0].Conversations[0].SessionID != "ses_mine" {
			t.Fatalf("parked = %+v, want work holding only ses_mine", list)
		}
		if list[1].Name != "other" || list[1].Note != "그쪽 메모" || list[1].Conversations[0].SessionID != "ses_theirs" {
			t.Errorf("other entry = %+v, want it untouched", list[1])
		}
	})
}

// 첫 화면의 opencode 가 다른 opencode 와 폴더를 함께 써도, 그 폴더에서 아무것도 갱신되지 않았으면 가릴 것이
// 없다 — 같은 세션의 Claude 대화 보관을 막지 않는다.
func TestFirstScreenOpencodeSharingAFolderDoesNotBlockClaude(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-c", "/w/c", "idle", "c", "work:@2.%5", 1000, 2000)}
	withPark(t, files, "%5 101 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/c", "sid-c")
		other := opencodeProc{PID: 900, Lineage: []int{900, 1}, Exe: ocBin, Dir: "/w/oc", StartMs: 12_000, TUI: true}
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000), other})
		m.OnOutput([]byte("@1 %3 100 work\n@2 %5 101 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[{"id":"ses_old","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}}]}`),
			nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list[0].Conversations) != 1 || list[0].Conversations[0].SessionID != "sid-c" {
			t.Errorf("parked = %+v, want the Claude conversation alone", list)
		}
	})
}

// pane 목록을 못 읽었는데 opencode 가 돌고 있으면 닫지 않는다 — 삼키면 섞인 세션에서 Claude 만 적고 닫는다.
func TestOpencodePaneListFailureLeavesTheSessionOpen(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-c", "/w/c", "idle", "c", "work:@2.%5", 1000, 2000)}
	withPark(t, files, "%5 101 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/c", "sid-c")
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput(nil, errors.New("server exited unexpectedly"), "tmux", ocPanesArgs...)

		if _, err := ParkSession(Session{Name: "work"}); err == nil || errors.Is(err, ErrNothingToPark) {
			t.Fatalf("err = %v, want the pane listing failure", err)
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("closed the session without looking for its opencode: %v", m.runs)
		}
	})
}

// Claude 와 opencode 가 한 세션에 있으면 둘 다, 창·pane 순서로 들어간다.
func TestParkSessionMixesClaudeAndOpencodeInPaneOrder(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-c", "/w/c", "idle", "c", "work:@2.%5", 1000, 2000)}
	withPark(t, files, "%5 101 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/c", "sid-c")
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n@2 %5 101 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[{"id":"ses_oc","time":{"created":1,"updated":20000},"location":{"directory":"/w/oc"}}]}`),
			nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		var got []string
		for _, c := range list[0].Conversations {
			got = append(got, c.Tool+":"+c.SessionID)
		}
		if strings.Join(got, ",") != "opencode:ses_oc,:sid-c" {
			t.Errorf("conversations = %v, want opencode (@1) before Claude (@2)", got)
		}
	})
}

// 첫 화면에 있는 opencode(대화를 아직 안 연 것)는 되살릴 것이 없다. 세션을 닫지 않는다.
func TestOpencodeWithoutAConversationIsNothingToPark(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[{"id":"ses_before","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}}]}`),
			nil, ocBin, ocListArgs("/w/oc")...)

		_, err := ParkSession(Session{Name: "work"})
		if !errors.Is(err, ErrNothingToPark) {
			t.Fatalf("err = %v, want ErrNothingToPark", err)
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("closed a session with nothing to bring back: %v", m.runs)
		}
	})
}

// -s 로 연 대화는 아직 아무것도 안 쳤어도 그 pane 의 대화다 — 보관함에서 되살린 opencode 가 그렇다.
func TestOpencodeOpenedWithSessionFlagParksThatConversation(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		p := ocTUI(500, 100, "/w/oc", 10_000)
		p.Session = "ses_old"
		withOpencodeProcs(t, []opencodeProc{p})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[]}`), nil, ocBin, ocListArgs("/w/oc")...)
		m.OnOutput([]byte(`{"data":{"id":"ses_old","title":"resumed","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}}}`),
			nil, ocBin, ocGetArgs("ses_old")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if c := list[0].Conversations[0]; c.SessionID != "ses_old" || c.Task != "resumed" {
			t.Errorf("conversation = %+v, want the one named by -s", c)
		}
	})
}

// -s 의 대화가 지워졌으면(opencode 는 그 id 를 새 대화로 받지만 아직 아무것도 안 쳤다) 되살릴 것이 없다 —
// opencode 가 죽은 것과 섞지 않는다.
func TestOpencodeWhoseSessionFlagIsGoneIsNothingToPark(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		p := ocTUI(500, 100, "/w/oc", 10_000)
		p.Session = "ses_gone"
		withOpencodeProcs(t, []opencodeProc{p})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[]}`), nil, ocBin, ocListArgs("/w/oc")...)
		m.OnOutput(ocNotFound("ses_gone"), errors.New("exit status 1: HTTP 404 Not Found"), ocBin, ocGetArgs("ses_gone")...)

		if _, err := ParkSession(Session{Name: "work"}); !errors.Is(err, ErrNothingToPark) {
			t.Fatalf("err = %v, want ErrNothingToPark", err)
		}
	})
}

// -c 는 그 폴더의 마지막 대화를 이었다.
func TestOpencodeContinuingParksTheLatestConversation(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		p := ocTUI(500, 100, "/w/oc", 10_000)
		p.Continuing = true
		withOpencodeProcs(t, []opencodeProc{p})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[
			{"id":"ses_older","time":{"created":1,"updated":3000},"location":{"directory":"/w/oc"}},
			{"id":"ses_last","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}}
		]}`), nil, ocBin, ocListArgs("/w/oc")...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if c := list[0].Conversations[0]; c.SessionID != "ses_last" {
			t.Errorf("conversation = %+v, want the latest in the folder", c)
		}
	})
}

// 같은 폴더에 opencode 가 또 있으면 어느 대화가 이 세션의 것인지 모른다. 고르지 않고, 닫지도 않는다.
func TestOpencodeSharingAFolderIsNotGuessed(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		other := opencodeProc{PID: 900, Lineage: []int{900, 1}, Exe: ocBin, Dir: "/w/oc", StartMs: 12_000}
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000), other})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput([]byte(`{"data":[{"id":"ses_now","time":{"created":1,"updated":20000},"location":{"directory":"/w/oc"}}]}`),
			nil, ocBin, ocListArgs("/w/oc")...)

		_, err := ParkSession(Session{Name: "work"})
		if err == nil || errors.Is(err, ErrNothingToPark) || !strings.Contains(err.Error(), "900") {
			t.Fatalf("err = %v, want a refusal naming the other opencode", err)
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("closed the session on a guess: %v", m.runs)
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want nothing recorded", got)
		}
	})
}

// 같은 폴더의 다른 opencode 를 따지는 것은 1단계(TUI 가 뜬 뒤 갱신된 대화)뿐이다 — park.md «같은 폴더에 다른
// opencode 클라이언트가 살아 있으면 고르지 않는다». -s 의 대화는 그 pane 의 명령줄이 정하고, -c 는 1단계가
// 비었을 때만 닿으므로 그 폴더의 최신 대화가 곧 -c 가 이은 대화다. 다른 클라이언트가 그 뒤에 무엇이든 갱신했다면
// 1단계에 걸려 거부된다. 2·3단계에서도 거부하면 되살린 뒤 아직 안 친 opencode 를 다시 보관할 수 없다.
func TestOpencodeSharingAFolderStillParksItsSessionFlagAndContinue(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*opencodeProc)
		want string
	}{
		{"-s", func(p *opencodeProc) { p.Session = "ses_old" }, "ses_old"},
		{"-c", func(p *opencodeProc) { p.Continuing = true }, "ses_last"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withPark(t, nil, "", func(m *mockRunner, _ string) {
				p := ocTUI(500, 100, "/w/oc", 10_000)
				tc.set(&p)
				other := opencodeProc{PID: 900, Lineage: []int{900, 1}, Exe: ocBin, Dir: "/w/oc", StartMs: 12_000, TUI: true}
				withOpencodeProcs(t, []opencodeProc{p, other})
				m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
				// 둘 다 TUI 가 뜬 시각(10s) 앞에 갱신됐다 — 1단계는 비고, 공유 검사는 닿지 않는다.
				m.OnOutput([]byte(`{"data":[
					{"id":"ses_last","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}},
					{"id":"ses_old","time":{"created":1,"updated":3000},"location":{"directory":"/w/oc"}}
				]}`), nil, ocBin, ocListArgs("/w/oc")...)

				list, err := ParkSession(Session{Name: "work"})
				if err != nil {
					t.Fatalf("park: %v", err)
				}
				if c := list[0].Conversations[0]; c.SessionID != tc.want {
					t.Errorf("conversation = %+v, want %s", c, tc.want)
				}
			})
		})
	}
}

// 보관함에 이미 든 대화는 2단계(-s)·3단계(-c)에서도 고르지 않는다 — 고르면 남의 항목에 든 대화가 이 세션의
// 항목으로 또 적힌다. 고를 것이 없으니 닫지도 않고, 남의 항목은 그대로다.
func TestOpencodeSessionFlagOrContinueIntoAParkedConversationIsNothingToPark(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*opencodeProc)
	}{
		{"-s", func(p *opencodeProc) { p.Session = "ses_theirs" }},
		{"-c", func(p *opencodeProc) { p.Continuing = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withPark(t, nil, "", func(m *mockRunner, _ string) {
				mustSaveParked(t, []ParkedSession{{ID: "other-entry", Name: "other", ParkedAt: time.Unix(1000, 0),
					Conversations: []ParkedConversation{{SessionID: "ses_theirs", CWD: "/w/oc", Tool: toolOpencode, Command: ocBin}}}})
				p := ocTUI(500, 100, "/w/oc", 10_000)
				tc.set(&p)
				withOpencodeProcs(t, []opencodeProc{p})
				m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
				// ses_theirs 가 그 폴더의 최신이고, TUI 가 뜬 시각(10s) 앞에 갱신됐다 — 1단계는 빈다.
				m.OnOutput([]byte(`{"data":[
					{"id":"ses_theirs","time":{"created":1,"updated":5000},"location":{"directory":"/w/oc"}},
					{"id":"ses_older","time":{"created":1,"updated":3000},"location":{"directory":"/w/oc"}}
				]}`), nil, ocBin, ocListArgs("/w/oc")...)

				if _, err := ParkSession(Session{Name: "work"}); !errors.Is(err, ErrNothingToPark) {
					t.Fatalf("err = %v, want ErrNothingToPark", err)
				}
				if ranAny(m, "tmux kill-session") {
					t.Errorf("closed the session though its conversation was already parked: %v", m.runs)
				}
				if got := mustLoadParked(t); len(got) != 1 || got[0].Name != "other" || len(got[0].Conversations) != 1 {
					t.Errorf("parked = %+v, want only the other entry, untouched", got)
				}
			})
		})
	}
}

// opencodeLimitRecorder 는 제한 시간을 아는 러너다. opencode 호출이 받은 한도를 적고, 답은 목 러너가 한다.
type opencodeLimitRecorder struct {
	*mockRunner
	limits map[string]time.Duration
}

func (r *opencodeLimitRecorder) OutputWithin(limit time.Duration, name string, args ...string) ([]byte, error) {
	r.limits[name+" "+strings.Join(args, " ")] = limit
	return r.mockRunner.Output(name, args...)
}

// opencode 에 묻는 호출은 둘 다 opencodeCallTimeout 에서 끊긴다(park.md «opencode 호출은 10초에서 끊는다»).
// 서비스가 멎으면 호출이 끝나지 않아 보관 Cmd 가 돌아오지 않는다. 목 러너는 한도를 모르고 Output 으로
// 물러서므로, 한도를 아는 러너를 끼워 실제로 한도를 넘겼는지 본다.
func TestOpencodeCallsAreBoundedByTheCallTimeout(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		rec := &opencodeLimitRecorder{mockRunner: m, limits: map[string]time.Duration{}}
		SetRunner(rec)
		m.OnOutput([]byte(`{"data":[]}`), nil, ocBin, ocListArgs("/w/oc")...)
		m.OnOutput(ocNotFound("ses_x"), errors.New("exit status 1: HTTP 404 Not Found"), ocBin, ocGetArgs("ses_x")...)

		if _, err := opencodeSessions(ocBin, "/w/oc"); err != nil {
			t.Fatalf("session.list: %v", err)
		}
		if _, _, err := opencodeSessionInfo(ocBin, "ses_x"); err != nil {
			t.Fatalf("session.get: %v", err)
		}
		for _, args := range [][]string{ocListArgs("/w/oc"), ocGetArgs("ses_x")} {
			key := ocBin + " " + strings.Join(args, " ")
			if got, ok := rec.limits[key]; !ok || got != opencodeCallTimeout {
				t.Errorf("%s: limit = %v (asked with a limit: %v), want %v", args[1], got, ok, opencodeCallTimeout)
			}
		}
	})
}

// opencode 에 물을 수 없으면(서비스가 죽었거나 실행 파일이 사라졌다) 닫지 않는다.
func TestOpencodeListFailureLeavesTheSessionOpen(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 100, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n"), nil, "tmux", ocPanesArgs...)
		m.OnOutput(nil, errors.New("connection refused"), ocBin, ocListArgs("/w/oc")...)

		if _, err := ParkSession(Session{Name: "work"}); err == nil || errors.Is(err, ErrNothingToPark) {
			t.Fatalf("err = %v, want the listing failure", err)
		}
		if ranAny(m, "tmux kill-session") {
			t.Errorf("closed the session without knowing its conversation: %v", m.runs)
		}
	})
}

// 다른 세션의 opencode 는 이 세션의 보관과 상관없다 — 물어보지도 않는다.
func TestOpencodeInAnotherSessionIsLeftAlone(t *testing.T) {
	files := map[string]string{"10.json": runFile(10, "sid-a", "/w/a", "idle", "a", "work:@1.%3", 1000, 2000)}
	withPark(t, files, "%3 100 work\n", func(m *mockRunner, home string) {
		touchTranscript(t, home, "/w/a", "sid-a")
		withOpencodeProcs(t, []opencodeProc{ocTUI(500, 200, "/w/oc", 10_000)})
		m.OnOutput([]byte("@1 %3 100 work\n@4 %8 200 other\n"), nil, "tmux", ocPanesArgs...)

		list, err := ParkSession(Session{Name: "work"})
		if err != nil {
			t.Fatalf("park: %v", err)
		}
		if len(list[0].Conversations) != 1 || list[0].Conversations[0].SessionID != "sid-a" {
			t.Errorf("parked = %+v, want only the Claude conversation", list)
		}
		if slices.ContainsFunc(m.gets, func(g string) bool { return strings.HasPrefix(g, ocBin) }) {
			t.Errorf("asked opencode about a session not being parked: %v", m.gets)
		}
	})
}

func TestRestoreParkedReopensAnOpencodeConversation(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		dir := t.TempDir()
		m.OnOutput([]byte(`{"data":{"id":"ses_a","time":{"created":1,"updated":2},"location":{"directory":"`+dir+`"}}}`),
			nil, ocBin, ocGetArgs("ses_a")...)
		p := parkedFixture("work", ParkedConversation{SessionID: "ses_a", CWD: dir, PID: 500, Tool: toolOpencode, Command: ocBin})
		mustSaveParked(t, []ParkedSession{p})

		name, err := RestoreParked(p)
		if err != nil || name != "work" {
			t.Fatalf("restore = %q, %v; want the session under its own name", name, err)
		}
		if want := "tmux new-session -d -s work -c " + dir + " " + ocBin + " -s ses_a"; !slices.Contains(m.runs, want) {
			t.Errorf("runs = %v, want %q", m.runs, want)
		}
		if got := mustLoadParked(t); len(got) != 0 {
			t.Errorf("parked = %+v, want the restored entry gone", got)
		}
	})
}

// opencode 는 없는 id 의 -s 를 «새 대화» 로 받는다. 그렇게 띄우면 빈 대화가 «되살림» 으로 세어져 항목이
// 지워진다 — 띄우지 않고 남긴다.
func TestRestoreParkedKeepsAnOpencodeConversationThatIsGone(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		m.OnOutput(ocNotFound("ses_a"), errors.New("exit status 1: HTTP 404 Not Found"), ocBin, ocGetArgs("ses_a")...)
		p := parkedFixture("work", ParkedConversation{SessionID: "ses_a", CWD: t.TempDir(), Tool: toolOpencode, Command: ocBin})
		mustSaveParked(t, []ParkedSession{p})

		// 404 는 «물을 수 없음» 이 아니라 «없음» 이다 — 그렇게 말해야 사용자가 다시 해 볼 일이 아님을 안다.
		if name, err := RestoreParked(p); name != "" || err == nil || !strings.Contains(err.Error(), "no longer has") {
			t.Fatalf("restore = %q, %v; want nothing started and the conversation reported gone", name, err)
		}
		if ranAny(m, "tmux new-session") {
			t.Errorf("opened a conversation opencode no longer has: %v", m.runs)
		}
		if got := mustLoadParked(t); len(got) != 1 {
			t.Errorf("parked = %+v, want the entry kept", got)
		}
	})
}

// opencode 에 물을 수 없으면(서비스가 죽었다) 띄우지 않고 남긴다. 그 이유를 말한다.
func TestRestoreParkedKeepsAnOpencodeConversationWhenOpencodeDoesNotAnswer(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		m.OnOutput(nil, errors.New("connection refused"), ocBin, ocGetArgs("ses_a")...)
		p := parkedFixture("work", ParkedConversation{SessionID: "ses_a", CWD: t.TempDir(), Tool: toolOpencode, Command: ocBin})
		mustSaveParked(t, []ParkedSession{p})

		if name, err := RestoreParked(p); name != "" || err == nil || !strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("restore = %q, %v; want nothing started and opencode's error", name, err)
		}
		if got := mustLoadParked(t); len(got) != 1 {
			t.Errorf("parked = %+v, want the entry kept", got)
		}
	})
}

// 적어 둔 실행 파일이 사라졌으면(버전마다 폴더를 따로 쓰는 설치의 업그레이드) PATH 의 opencode 로 되살린다.
func TestRestoreParkedFallsBackToOpencodeOnPath(t *testing.T) {
	withPark(t, nil, "", func(m *mockRunner, _ string) {
		const onPath = "/usr/local/bin/opencode"
		opencodeBinUsable = func(path string) bool { return path != ocBin }
		opencodeLookPath = func(string) (string, error) { return onPath, nil }
		dir := t.TempDir()
		m.OnOutput([]byte(`{"data":{"id":"ses_a","time":{"created":1,"updated":2},"location":{"directory":"`+dir+`"}}}`),
			nil, onPath, ocGetArgs("ses_a")...)
		p := parkedFixture("work", ParkedConversation{SessionID: "ses_a", CWD: dir, Tool: toolOpencode, Command: ocBin})

		if _, err := RestoreParked(p); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if want := "tmux new-session -d -s work -c " + dir + " " + onPath + " -s ses_a"; !slices.Contains(m.runs, want) {
			t.Errorf("runs = %v, want %q", m.runs, want)
		}
	})
}

// tool 을 모르는 옛 mux 가 보관함을 다시 쓰면 그 필드가 지워진다. opencode 의 id(ses_…)로 알아본다.
func TestLoadParkedRecognisesAnOpencodeConversationWithoutItsTool(t *testing.T) {
	withPark(t, nil, "", func(*mockRunner, string) {
		path, _ := parkedFile()
		old := `{"sessions":[{"id":"x","name":"work","parked_at":"2026-11-11T10:00:00Z","conversations":[
			{"session_id":"ses_e3659f0ecffejsLfrn0JsUVQAh","cwd":"/w/oc"},
			{"session_id":"0b6f1d2e-0000-4000-8000-000000000000","cwd":"/w/c"}]}]}`
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		got := mustLoadParked(t)
		if len(got) != 1 || got[0].Conversations[0].Tool != toolOpencode || got[0].Conversations[1].Tool != "" {
			t.Errorf("parked = %+v, want the ses_ conversation read as opencode and the UUID left to Claude", got)
		}
	})
}

// 아래는 단위 테스트가 목으로 바꾸는 /proc 읽기의 실물 확인이다. /proc 이 없으면(macOS) 건너뛴다.

func TestProcLineageReachesTheParent(t *testing.T) {
	if _, err := os.Stat("/proc/self/status"); err != nil {
		t.Skip("no /proc")
	}
	lineage := procLineage(os.Getpid())
	if len(lineage) < 2 || lineage[0] != os.Getpid() || lineage[1] != os.Getppid() {
		t.Errorf("lineage = %v, want this process then its parent %d", lineage, os.Getppid())
	}
}

func TestBootTimeIsInThePast(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc")
	}
	boot, ok := bootTimeMs()
	if !ok || boot <= 0 || boot > time.Now().UnixMilli() {
		t.Errorf("boot = %d, %v; want a time before now", boot, ok)
	}
	// 이 테스트 프로세스의 시작 시각(부팅 + 틱)은 지금보다 앞이고, 몇 분 안이다.
	ticks, ok := readProcStart(os.Getpid())
	if !ok {
		t.Fatal("no start time for this process")
	}
	n, _ := strconv.ParseInt(ticks, 10, 64)
	start := boot + n*1000/userHZ
	if age := time.Now().UnixMilli() - start; age < -opencodeStartSlackMs || age > int64(10*time.Minute/time.Millisecond) {
		t.Errorf("this process started %dms ago by /proc, want a small positive age", age)
	}
}

func TestOpencodeExe(t *testing.T) {
	if got := opencodeExe(os.Getpid(), "/home/u/.opencode/bin/opencode"); got != "/home/u/.opencode/bin/opencode" {
		t.Errorf("absolute argv0 = %q, want it kept (a symlink outlives an upgrade)", got)
	}
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("no /proc")
	}
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if got := opencodeExe(os.Getpid(), "opencode"); got != self {
		t.Errorf("relative argv0 = %q, want the running executable %q", got, self)
	}
}
