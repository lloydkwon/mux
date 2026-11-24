package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 픽스처는 2026-09 의 실제 응답이다(Claude 에이전트 아홉, 모두 kind "id"). 0.9.5 의 스키마도 같은 모양이다.
func TestClaudeHoldsReadsTheConversationIDs(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput(fixture(t, "agent_list.json"), nil, mockBin, "agent", "list")
		h := ClaudeHolds()
		if h.By != "herdr" || h.Unknown {
			t.Fatalf("By=%q Unknown=%v, want herdr and read", h.By, h.Unknown)
		}
		if len(h.IDs) != 9 || !h.IDs["cb979a8e-6d4b-4238-9c31-e4a8ac5e6dad"] {
			t.Errorf("IDs = %v, want the nine conversation ids", h.IDs)
		}
		if len(h.Dirs) != 0 {
			t.Errorf("Dirs = %v, want none when every agent told its id", h.Dirs)
		}
	})
}

// id 를 알리지 않은 Claude 에이전트(통합이 빠졌거나 아직 보고 전, kind "path" 도)는 그 폴더를 쥔 것으로 본다.
// Claude 가 아닌 에이전트는 상관없다.
func TestClaudeHoldsFallsBackToTheFolder(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		out := `{"id":"cli:agent:list","result":{"type":"agent_list","agents":[
			{"pane_id":"w1:p1","agent":"claude","cwd":"/w/a","foreground_cwd":"/w/a/sub"},
			{"pane_id":"w1:p2","agent":"claude","agent_session":{"agent":"claude","kind":"path","source":"herdr:claude","value":"/t.jsonl"},"cwd":"/w/b"},
			{"pane_id":"w1:p3","agent":"codex","cwd":"/w/c"},
			{"pane_id":"w1:p4","agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"sid-d"},"cwd":"/w/d"}
		]}}`
		m.OnOutput([]byte(out), nil, mockBin, "agent", "list")
		h := ClaudeHolds()
		for _, dir := range []string{"/w/a", "/w/a/sub", "/w/b"} {
			if !h.Dirs[dir] {
				t.Errorf("Dirs = %v, want %s", h.Dirs, dir)
			}
		}
		if h.Dirs["/w/c"] || h.Dirs["/w/d"] {
			t.Errorf("Dirs = %v, want neither the codex folder nor the one whose id is known", h.Dirs)
		}
		if !h.IDs["sid-d"] || len(h.IDs) != 1 {
			t.Errorf("IDs = %v, want sid-d only", h.IDs)
		}
	})
}

// 폴더는 문자열이 같을 때만 맞으므로 끝 / 를 걷고 심링크를 푼 꼴도 넣는다 — Claude 는 물리 경로를 적는다.
func TestClaudeHoldsFolderAlsoMatchesItsCleanAndRealForm(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	withMock(t, func(m *mockRunner) {
		out := `{"id":"x","result":{"agents":[{"pane_id":"w1:p1","agent":"claude","cwd":"` + link + `/"}]}}`
		m.OnOutput([]byte(out), nil, mockBin, "agent", "list")
		h := ClaudeHolds()
		if !h.Dirs[link] || !h.Dirs[physical] {
			t.Errorf("Dirs = %v, want %s and its physical %s", h.Dirs, link, physical)
		}
	})
}

// 읽지 못하면 «못 읽음» 이다 — 쥔 것이 없다고 보면 같은 id 를 두 프로세스가 잡을 수 있다.
func TestClaudeHoldsUnreadableIsUnknown(t *testing.T) {
	for name, tc := range map[string]struct {
		out []byte
		err error
	}{
		"other error":    {nil, errors.New(`exit status 1: {"error":{"code":"protocol_mismatch"}}`)},
		"not json":       {[]byte("garbage"), nil},
		"error envelope": {[]byte(`{"id":"x","error":{"code":"boom","message":"boom"}}`), nil},
	} {
		t.Run(name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput(tc.out, tc.err, mockBin, "agent", "list")
				if h := ClaudeHolds(); !h.Unknown {
					t.Errorf("Unknown = false for %s", name)
				}
			})
		})
	}
}

// fakeHerdr 는 진짜 러너가 부를 가짜 herdr 실행 파일을 만든다 — stderr·종료 코드·시간 제한은 목으로 못 잰다.
func fakeHerdr(t *testing.T, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUX_HERDR_BIN", bin)
	old := runner
	SetRunner(nil) // 진짜 러너
	t.Cleanup(func() { runner = old })
}

// 서버가 꺼져 있으면 herdr 는 stdout 을 비우고 오류 봉투를 stderr 로 낸다(종료 코드 1, 0.9.5 실측). 쥔 것이 없다 —
// 이것을 «못 읽음» 으로 보면 herdr 를 안 쓰는 날에도 --claude 의 Enter 가 모두 막힌다.
func TestClaudeHoldsServerNotRunningHoldsNothing(t *testing.T) {
	fakeHerdr(t, `echo '{"id":"cli:agent:list","error":{"code":"server_not_running","message":"no herdr server is running"}}' >&2; exit 1`)
	h := ClaudeHolds()
	if h.Unknown || len(h.IDs) != 0 || len(h.Dirs) != 0 {
		t.Errorf("got %+v, want nothing held and nothing unknown", h)
	}
}

// 서버가 멎어 답이 없으면 끊고 «못 읽음» 이다. --claude 의 스캔이 이 답을 기다리므로 끝이 있어야 한다.
func TestClaudeHoldsGivesUpOnAHungServer(t *testing.T) {
	old := holdsLimit
	holdsLimit = 200 * time.Millisecond
	t.Cleanup(func() { holdsLimit = old })
	fakeHerdr(t, `sleep 5`)
	start := time.Now()
	h := ClaudeHolds()
	if !h.Unknown {
		t.Errorf("Unknown = false for a server that never answered")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %v, want it cut at the limit", took)
	}
}

// herdr 가 없으면 herdr 가 쥔 것도 없다.
func TestClaudeHoldsWithoutHerdr(t *testing.T) {
	t.Setenv("MUX_HERDR_BIN", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	h := ClaudeHolds()
	if h.Unknown || len(h.IDs) != 0 {
		t.Errorf("got %+v, want nothing held", h)
	}
	if !strings.EqualFold(h.By, "herdr") {
		t.Errorf("By = %q", h.By)
	}
}
