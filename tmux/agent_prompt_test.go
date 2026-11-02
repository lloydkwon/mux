package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakePane 은 Claude 의 입력창을 문자열 하나로 흉내 내는 러너다. send-keys -l 은 붙이고,
// BSpace 는 지우고, Enter 는 제출하거나(입력창이 빈다) 먹는다.
type fakePane struct {
	pane        string
	box         string
	inMode      bool
	sync        bool
	noBox       bool   // 대화상자가 입력창을 가렸다
	swallow     int    // 먹을 Enter 수
	clearOnType string // 치기 시작하면 사라지는 제안 문구
	submitted   []string
	literals    int
	enters      int
	runs        []string
	onSubmit    func()
	afterType   string // 우리 글 뒤에 사람이 이어 친 글
}

func (f *fakePane) screen() string {
	if f.noBox {
		return "● Bash 를 실행할까?\n  ❯ 1. Yes\n    2. No\n"
	}
	line := strings.Repeat("─", 40)
	// 위쪽 기록과 대기열에도 «❯ 글» 줄이 있지만 그 위는 «─» 선이 아니다.
	return "● 앞 대화\n❯ 예전 프롬프트\n" + line + "\n❯ " + f.box + " \n" + line + "\n  ⏵⏵ auto mode on\n"
}

func (f *fakePane) Output(name string, args ...string) ([]byte, error) {
	k := strings.Join(args, " ")
	switch k {
	case "capture-pane -p -t " + f.pane:
		return []byte(f.screen()), nil
	case "display-message -p #{socket_path}":
		return []byte("/tmp/tmux-test/default\n"), nil
	case "display-message -p -t " + f.pane + " #{pane_in_mode} #{pane_synchronized}":
		return []byte(fmt.Sprintf("%d %d\n", b2i(f.inMode), b2i(f.sync))), nil
	}
	return nil, fmt.Errorf("fakePane: unexpected Output %s %s", name, k)
}

func (f *fakePane) Run(name string, args ...string) error {
	f.runs = append(f.runs, strings.Join(args, " "))
	if len(args) < 4 || args[0] != "send-keys" || args[2] != f.pane {
		return fmt.Errorf("fakePane: unexpected Run %v", args)
	}
	switch rest := args[3:]; {
	case rest[0] == "-l":
		f.literals++
		lit := rest[len(rest)-1]
		if strings.HasSuffix(lit, `\;`) {
			lit = strings.TrimSuffix(lit, `\;`) + ";"
		}
		if f.clearOnType != "" && f.box == f.clearOnType {
			f.box = ""
		}
		f.box += lit + f.afterType
	case rest[0] == "Enter":
		f.enters++
		if f.swallow > 0 {
			f.swallow--
			return nil
		}
		f.submitted = append(f.submitted, f.box)
		f.box = ""
		if f.onSubmit != nil {
			f.onSubmit()
		}
	case rest[0] == "-N" && rest[2] == "BSpace":
		n, _ := strconv.Atoi(rest[1])
		r := []rune(f.box)
		if n > len(r) {
			n = len(r)
		}
		f.box = string(r[:len(r)-n])
	case rest[0] == "-X" && rest[1] == "cancel":
		f.inMode = false
	default:
		return fmt.Errorf("fakePane: unexpected keys %v", rest)
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// withPromptPane 은 상태 파일 하나와 가짜 pane, 가짜 시계를 세운다.
func withPromptPane(t *testing.T, status string, f *fakePane, fn func(a Agent)) {
	t.Helper()
	if f.pane == "" {
		f.pane = "%5"
	}
	files := map[string]string{"300.json": sessionFile(300, "s-300", "w:@1."+f.pane, status, "target", 1)}
	withClaudeSessions(t, files, nil, func() {
		SetRunner(f)
		now := time.UnixMilli(1793589600000)
		oldNow, oldSleep, oldLock := agentNow, agentSleep, agentLockDir
		agentNow = func() time.Time { return now }
		agentSleep = func(d time.Duration) { now = now.Add(d) }
		lockDir := t.TempDir()
		agentLockDir = func() string { return lockDir }
		defer func() { agentNow, agentSleep, agentLockDir = oldNow, oldSleep, oldLock }()
		fn(Agent{PaneID: f.pane, Session: "w", PID: 300, SessionID: "s-300", CWD: "/home/u/proj", State: AgentIdle})
	})
	if f.literals > 1 {
		t.Errorf("글을 %d번 쳤다 — 한 번만 쳐야 한다", f.literals)
	}
}

// rewriteState 는 상태 파일을 바꾼다(제출 뒤 대화상자가 뜨는 경우 등).
func rewriteState(t *testing.T, status string, updated int64) {
	t.Helper()
	home, _ := homeDir()
	content := strings.Replace(sessionFile(300, "s-300", "w:@1.%5", status, "target", 1),
		`"statusUpdatedAt":1793589532061`, `"statusUpdatedAt":`+strconv.FormatInt(updated, 10), 1)
	if err := os.WriteFile(filepath.Join(home, claudeDir, sessionsDir, "300.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPromptHappyPath(t *testing.T) {
	f := &fakePane{}
	withPromptPane(t, "idle", f, func(a Agent) {
		tok, err := PromptAgent(a, "/govgen-reply ~/.claude/govgen/rounds/G-93/message.md", false)
		if err != nil {
			t.Fatal(err)
		}
		if tok.SessionID != "s-300" || tok.SentAt.UnixMilli() != 1793589600000 {
			t.Errorf("token = %v", tok)
		}
	})
	want := []string{
		"send-keys -t %5 -l -- /govgen-reply ~/.claude/govgen/rounds/G-93/message.md",
		"send-keys -t %5 Enter",
	}
	if strings.Join(f.runs, "\n") != strings.Join(want, "\n") {
		t.Errorf("보낸 키:\n%s\nwant:\n%s", strings.Join(f.runs, "\n"), strings.Join(want, "\n"))
	}
	if len(f.submitted) != 1 || f.submitted[0] != "/govgen-reply ~/.claude/govgen/rounds/G-93/message.md" {
		t.Errorf("제출된 것 = %q", f.submitted)
	}
}

func TestPromptRefusesWithoutSendingAnything(t *testing.T) {
	tests := []struct {
		name   string
		status string
		force  bool
		pane   fakePane
		want   error
	}{
		{"blocked", "waiting", false, fakePane{}, ErrAgentBlocked},
		{"blocked even with force", "waiting", true, fakePane{}, ErrAgentBlocked},
		{"working", "busy", false, fakePane{}, ErrAgentNotReady},
		{"unknown state", "compacting", false, fakePane{}, ErrAgentNotReady},
		{"copy mode", "idle", false, fakePane{inMode: true}, ErrAgentNotReady},
		{"synchronize-panes even with force", "idle", true, fakePane{sync: true}, ErrAgentNotReady},
		{"dialog covers the box", "idle", false, fakePane{noBox: true}, ErrAgentNotReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.pane
			withPromptPane(t, tt.status, &f, func(a Agent) {
				if _, err := PromptAgent(a, "hello", tt.force); !errors.Is(err, tt.want) {
					t.Errorf("err = %v, want %v", err, tt.want)
				}
			})
			if len(f.runs) != 0 {
				t.Errorf("아무것도 보내지 말아야 하는데 %v", f.runs)
			}
		})
	}
}

func TestPromptForceSendsToAWorkingAgentAndLeavesCopyMode(t *testing.T) {
	f := &fakePane{inMode: true}
	withPromptPane(t, "busy", f, func(a Agent) {
		if _, err := PromptAgent(a, "then this", true); err != nil {
			t.Fatal(err)
		}
	})
	if f.runs[0] != "send-keys -t %5 -X cancel" || len(f.submitted) != 1 {
		t.Errorf("runs = %v, submitted = %v", f.runs, f.submitted)
	}
}

func TestPromptKeepsAHumanDraft(t *testing.T) {
	// 사람이 쓰던 초안이 있으면 친 글만 지우고 물러선다. 초안은 그대로다.
	f := &fakePane{box: "쓰던 초안"}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "hello", false); !errors.Is(err, ErrAgentNotReady) {
			t.Errorf("err = %v", err)
		}
	})
	if f.box != "쓰던 초안" || f.enters != 0 {
		t.Errorf("입력창 = %q, Enter %d번 — 초안이 남고 Enter 는 없어야 한다", f.box, f.enters)
	}
}

func TestPromptReplacesASuggestion(t *testing.T) {
	// 흐린 제안 문구는 치기 시작하면 사라진다 — 그러면 입력창이 정확히 우리 글이 된다.
	f := &fakePane{box: "Try: run the tests", clearOnType: "Try: run the tests"}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "hello", false); err != nil {
			t.Fatal(err)
		}
	})
	if len(f.submitted) != 1 || f.submitted[0] != "hello" {
		t.Errorf("제출된 것 = %q", f.submitted)
	}
}

func TestPromptRetriesASwallowedEnterOnce(t *testing.T) {
	f := &fakePane{swallow: 1}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "hello", false); err != nil {
			t.Fatal(err)
		}
	})
	if f.enters != 2 || len(f.submitted) != 1 {
		t.Errorf("Enter %d번, 제출 %v — Enter 는 정확히 두 번, 제출은 한 번", f.enters, f.submitted)
	}
}

func TestPromptRollsBackWhenEnterNeverLands(t *testing.T) {
	f := &fakePane{swallow: 99}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "hello", false); !errors.Is(err, ErrNotSubmitted) {
			t.Errorf("err = %v", err)
		}
	})
	if f.enters != 2 || f.box != "" {
		t.Errorf("Enter %d번, 입력창 %q — 두 번 뒤 되돌려야 한다", f.enters, f.box)
	}
}

func TestPromptConfirmedByStateWhenADialogCoversTheBox(t *testing.T) {
	// 제출하자마자 권한 대화상자가 입력창을 가렸다. 상태가 보낸 뒤에 바뀌었으니 제출이다.
	f := &fakePane{}
	withPromptPane(t, "idle", f, func(a Agent) {
		f.onSubmit = func() {
			f.noBox = true
			rewriteState(t, "waiting", agentNow().UnixMilli())
		}
		if _, err := PromptAgent(a, "hello", false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPromptForeignTextAfterEnterIsUnknown(t *testing.T) {
	// Enter 뒤 입력창에 우리 것도 빈 것도 아닌 글이 있다 — 사람이 같이 쳤다. 되돌리지 않는다.
	f := &fakePane{}
	withPromptPane(t, "idle", f, func(a Agent) {
		f.onSubmit = func() { f.box = "someone typing" }
		if _, err := PromptAgent(a, "hello", false); !errors.Is(err, ErrPromptUnknown) {
			t.Errorf("err = %v", err)
		}
	})
	if f.box != "someone typing" {
		t.Errorf("남의 글을 건드렸다: %q", f.box)
	}
}

func TestPromptEscapesATrailingSemicolon(t *testing.T) {
	f := &fakePane{}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "끝;", false); err != nil {
			t.Fatal(err)
		}
	})
	if f.runs[0] != `send-keys -t %5 -l -- 끝\;` || f.submitted[0] != "끝;" {
		t.Errorf("runs = %v, submitted = %v", f.runs, f.submitted)
	}
}

func TestTmuxLiteral(t *testing.T) {
	for in, want := range map[string]string{"a;": `a\;`, `a\;`: `a\\;`, ";": `\;`, "a;b": "a;b", "plain": "plain"} {
		if got := tmuxLiteral(in); got != want {
			t.Errorf("tmuxLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidatePromptText(t *testing.T) {
	ok := []string{"hello", "/govgen-reply ~/x/message.md", "한글 메시지 ❯ 포함", strings.Repeat("가", maxPromptRunes)}
	for _, s := range ok {
		if err := ValidatePromptText(s); err != nil {
			t.Errorf("ValidatePromptText(%q…) = %v", firstRunes(s, 20), err)
		}
	}
	bad := []string{"", "   ", "two\nlines", "tab\there", "esc\x1b[0m", "!ls", strings.Repeat("a", maxPromptRunes+1),
		"zwj \U0001F469\u200D\U0001F4BB", "nfd \u1112\u1161\u11ab", "e\u0301",
		"flag \U0001F1F0\U0001F1F7", "skin \U0001F44D\U0001F3FD", "spacing mark \u0915\u093F", "zwsp a\u200Bb"}
	for _, s := range bad {
		if err := ValidatePromptText(s); err == nil {
			t.Errorf("ValidatePromptText(%q…) 가 통과했다", firstRunes(s, 20))
		}
	}
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func TestParseInputBox(t *testing.T) {
	line := strings.Repeat("─", 30)
	tests := []struct {
		name, screen, want string
		ok                 bool
	}{
		{"empty box with cursor cell", "x\n" + line + "\n❯  \n" + line + "\n  status", "  ", true},
		{"typed", line + "\n❯ hello world\n" + line + "\n", " hello world", true},
		{"wrapped", line + "\n❯ 가나다\n  라마바\n" + line + "\n", " 가나다  라마바", true},
		{"history above an empty box", "❯ old\n● reply\n" + line + "\n❯ \n" + line + "\n", " ", true},
		{"dialog only", "● Allow?\n  ❯ 1. Yes\n    2. No\n", "", false},
		{"no line above", "❯ hello\n" + line + "\n", "", false},
		{"no closing line", line + "\n❯ hello\n", "", false},
	}
	for _, tt := range tests {
		got, ok := parseInputBox(tt.screen)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: parseInputBox = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
	if normalizeBox(" 가나다  라마바 ") != "가나다라마바" {
		t.Error("normalizeBox 가 U+00A0 와 줄바꿈 들여쓰기를 지우지 못했다")
	}
}

func TestPromptNeverDeletesWhatAHumanTypedMeanwhile(t *testing.T) {
	// 우리 글 뒤에 사람이 "abc" 를 이어 쳤다. BSpace 는 커서 앞부터 지우므로, 지우면 사람 글이
	// 사라진다 — 건드리지 않고 «모름» 으로 물러선다.
	f := &fakePane{afterType: "abc"}
	withPromptPane(t, "idle", f, func(a Agent) {
		if _, err := PromptAgent(a, "hello", false); !errors.Is(err, ErrPromptUnknown) {
			t.Errorf("err = %v", err)
		}
	})
	if f.box != "helloabc" || f.enters != 0 {
		t.Errorf("입력창 = %q, Enter %d번 — 아무것도 지우거나 제출하지 말아야 한다", f.box, f.enters)
	}
	for _, r := range f.runs {
		if strings.Contains(r, "BSpace") {
			t.Errorf("BSpace 를 보냈다: %v", f.runs)
		}
	}
}

func TestPromptForceDoesNotTrustAStateChangeAlone(t *testing.T) {
	// 작업 중인 대상에 --force 로 보냈다. Enter 가 먹혔는데 앞 턴의 권한 대화상자가 떠서 입력창이
	// 가려지고 상태가 바뀌었다 — 우리 글은 가려진 입력창에 남아 있다. 성공으로 읽으면 안 된다.
	f := &fakePane{swallow: 99}
	withPromptPane(t, "busy", f, func(a Agent) {
		f.onSubmit = nil
		var once bool
		inner := agentSleep
		agentSleep = func(d time.Duration) {
			inner(d)
			if f.enters == 1 && !once {
				once = true
				f.noBox = true
				rewriteState(t, "waiting", agentNow().UnixMilli())
			}
		}
		if _, err := PromptAgent(a, "hello", true); err == nil {
			t.Error("Enter 가 먹혔는데 성공으로 읽었다")
		}
	})
}
