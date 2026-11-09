package tmux

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	workTitle = "패널 세션별 작업 표시"
	// fixtureOtherIdle 은 fixtureIdle 과 다른 대화·다른 세션이다.
	fixtureOtherID = "aaaaaaaa-1111-4222-8333-bbbbbbbbbbbb"
)

var fixtureOtherIdle = strings.NewReplacer(
	fixtureIdleSessionID, fixtureOtherID,
	`"tmux":"project:@1.%1"`, `"tmux":"other:@2.%2"`,
	`"pid":11458`, `"pid":11459`,
).Replace(fixtureIdle)

// withWorkTitleModel 은 모델 호출을 가로채 받은 입력을 모은다. 진짜 claude 를 부르는 테스트는 없다.
func withWorkTitleModel(t *testing.T, reply func(input string) (string, error)) *[]string {
	t.Helper()
	inputs := &[]string{}
	old := askWorkTitle
	askWorkTitle = func(input string) (string, error) {
		*inputs = append(*inputs, input)
		return reply(input)
	}
	t.Cleanup(func() { askWorkTitle = old })
	return inputs
}

func fixedTitle(string) (string, error) { return workTitle, nil }

// listWork 는 패널이 틱마다 하는 읽기다.
func listWork(names ...string) []Session {
	sessions := make([]Session, len(names))
	for i, n := range names {
		sessions[i] = Session{Name: n}
	}
	return WithClaudeWork(sessions)
}

func TestWorkLineBecomesTheTitleOnceThereIsOne(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		withWorkTitleModel(t, fixedTitle)

		before := listWork("project")
		if before[0].AIWork != workPrompt || !before[0].AIWorkUntitled {
			t.Fatalf("제목 전: AIWork=%q untitled=%v, want 원문과 true", before[0].AIWork, before[0].AIWorkUntitled)
		}

		TitleNextClaudeWork(before)

		after := listWork("project")
		if after[0].AIWork != workTitle || after[0].AIWorkUntitled {
			t.Errorf("제목 뒤: AIWork=%q untitled=%v, want %q 와 false", after[0].AIWork, after[0].AIWorkUntitled, workTitle)
		}
	})
}

func TestNewWorkAsksForANewTitle(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		next := "kitty 를 최신 버전으로 올려 주세요"
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD,
			[]string{trHuman(trBase, workPrompt), trHuman(trBase.Add(time.Minute), next)})

		got := listWork("project")
		if got[0].AIWork != next || !got[0].AIWorkUntitled {
			t.Fatalf("새 일: AIWork=%q untitled=%v, want 새 원문과 true", got[0].AIWork, got[0].AIWorkUntitled)
		}
		TitleNextClaudeWork(got)
		if len(*inputs) != 2 {
			t.Errorf("모델 호출 %d번, want 2", len(*inputs))
		}
	})
}

// 짧은 응답은 작업 글을 바꾸지 않으므로 모델도 부르지 않는다. 이 값이 호출 수를 프롬프트 수보다 적게 만든다.
func TestShortReplyDoesNotAskAgain(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD,
			[]string{trHuman(trBase, workPrompt), trHuman(trBase.Add(time.Minute), "커밋해줘.")})
		got := listWork("project")
		if got[0].AIWork != workTitle || got[0].AIWorkUntitled {
			t.Fatalf("AIWork=%q untitled=%v", got[0].AIWork, got[0].AIWorkUntitled)
		}
		TitleNextClaudeWork(got)
		if len(*inputs) != 1 {
			t.Errorf("모델 호출 %d번, want 1", len(*inputs))
		}
	})
}

// «이거는 프로젝트가 진행이 되면 고객에게 알릴 사항임.» 은 앞을 모르면 제목을 지을 수 없다.
func TestTitleInputCarriesTheContext(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		now := "이거는 프로젝트가 진행이 되면 고객에게 알릴 사항임."
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{
			`{"type":"ai-title","aiTitle":"옛 주제","sessionId":"x"}`,
			trHuman(trBase, "가장 오래된 프롬프트라 맥락 수에서 밀려난다"),
			trRecap(trBase.Add(1*time.Minute), "첫째 요약입니다. 일감을 정리하는 중입니다."),
			trHuman(trBase.Add(2*time.Minute), "둘째 맥락은 사람이 친 프롬프트입니다"),
			trHuman(trBase.Add(3*time.Minute), "셋째 맥락도 사람이 친 프롬프트입니다"),
			trHuman(trBase.Add(4*time.Minute), "응."),
			`{"type":"ai-title","aiTitle":"Mantis 후속 일감","sessionId":"x"}`,
			trHuman(trBase.Add(5*time.Minute), now),
		})
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		want := "<topic>Mantis 후속 일감</topic>\n" +
			"<earlier>첫째 요약입니다. 일감을 정리하는 중입니다.</earlier>\n" +
			"<earlier>둘째 맥락은 사람이 친 프롬프트입니다</earlier>\n" +
			"<earlier>셋째 맥락도 사람이 친 프롬프트입니다</earlier>\n" +
			"<now>" + now + "</now>"
		if len(*inputs) != 1 || (*inputs)[0] != want {
			t.Errorf("입력:\n%v\nwant:\n%s", *inputs, want)
		}
	})
}

func TestTitleInputWithoutContextIsJustTheWork(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))
		if want := "<now>" + workPrompt + "</now>"; len(*inputs) != 1 || (*inputs)[0] != want {
			t.Errorf("입력 %v, want %q", *inputs, want)
		}
	})
}

func twoSessions(t *testing.T, fn func()) {
	t.Helper()
	files := map[string]string{"11458.json": fixtureIdle, "11459.json": fixtureOtherIdle}
	withClaudeSessions(t, files, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		writeSessionTranscript(t, fixtureOtherID, fixtureIdleCWD, []string{trHuman(trBase, "다른 세션의 일을 처리해 주세요 부탁합니다")})
		fn()
	})
}

// 한 번에 하나다. 패널을 처음 띄운 순간 세션 수만큼 동시에 부르지 않는다.
func TestTitleNextCallsTheModelOnce(t *testing.T) {
	twoSessions(t, func() {
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project", "other"))
		if len(*inputs) != 1 {
			t.Fatalf("한 번 불러 %d번 호출", len(*inputs))
		}
		TitleNextClaudeWork(listWork("project", "other"))
		if len(*inputs) != 2 {
			t.Fatalf("두 번 불러 %d번 호출", len(*inputs))
		}
		for _, s := range listWork("project", "other") {
			if s.AIWork != workTitle || s.AIWorkUntitled {
				t.Errorf("%s: AIWork=%q untitled=%v", s.Name, s.AIWork, s.AIWorkUntitled)
			}
		}
	})
}

// 패널은 창마다 하나씩 돈다. 다른 패널이 짓고 있는 대화는 건너뛰어야 하고, 그 대화 때문에 뒤 세션이 굶으면 안 된다.
func TestClaimedConversationIsSkipped(t *testing.T) {
	twoSessions(t, func() {
		if !claimWorkTitle(fixtureIdleSessionID) {
			t.Fatal("첫 claim 이 실패했다")
		}
		if claimWorkTitle(fixtureIdleSessionID) {
			t.Fatal("같은 대화를 두 패널이 잡았다")
		}
		inputs := withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project", "other"))
		if len(*inputs) != 1 || !strings.Contains((*inputs)[0], "다른 세션의 일") {
			t.Errorf("입력 %v, want 뒤 세션의 일 하나", *inputs)
		}
	})
}

func TestStaleClaimIsTakenOver(t *testing.T) {
	withClaudeSessions(t, nil, nil, func() {
		if !claimWorkTitle(fixtureIdleSessionID) {
			t.Fatal("첫 claim 이 실패했다")
		}
		past := time.Now().Add(-time.Second)
		if err := os.Chtimes(workTitleClaimPath(t), past, past); err != nil {
			t.Fatal(err)
		}
		if !claimWorkTitle(fixtureIdleSessionID) {
			t.Error("기한이 지난 claim 을 넘겨받지 못했다 — 도중에 죽은 패널이 그 대화를 영영 막는다")
		}
	})
}

func workTitleClaimPath(t *testing.T) string {
	t.Helper()
	dir, err := workTitleDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, workTitleKey(fixtureIdleSessionID)+".claim")
}

// 로그인이 풀렸거나 사용량이 찼거나 오프라인이면 틱마다(2초) 부르지 않는다. 원문을 그대로 그린다.
func TestFailedTitleWaitsBeforeAskingAgain(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		fail := true
		inputs := withWorkTitleModel(t, func(string) (string, error) {
			if fail {
				return "", errors.New("Not logged in")
			}
			return workTitle, nil
		})

		TitleNextClaudeWork(listWork("project"))
		TitleNextClaudeWork(listWork("project"))
		if len(*inputs) != 1 {
			t.Fatalf("실패 뒤 곧바로 다시 불렀다: %d번", len(*inputs))
		}
		if got := listWork("project"); got[0].AIWork != workPrompt {
			t.Errorf("실패 뒤 AIWork=%q, want 원문", got[0].AIWork)
		}
		info, err := os.Stat(workTitleClaimPath(t))
		if err != nil {
			t.Fatal(err)
		}
		if left := time.Until(info.ModTime()); left < 9*time.Minute || left > workTitleRetryAfter {
			t.Errorf("다음 시도까지 %v, want 약 %v", left, workTitleRetryAfter)
		}
		// 틱은 실패를 말하지 않으므로 이유를 볼 곳이 이 파일이다.
		if cause, err := os.ReadFile(workTitleClaimPath(t)); err != nil || !strings.Contains(string(cause), "Not logged in") {
			t.Errorf("claim 의 이유 %q, %v", cause, err)
		}

		past := time.Now().Add(-time.Second)
		if err := os.Chtimes(workTitleClaimPath(t), past, past); err != nil {
			t.Fatal(err)
		}
		fail = false
		TitleNextClaudeWork(listWork("project"))
		if got := listWork("project"); got[0].AIWork != workTitle {
			t.Errorf("다시 시도한 뒤 AIWork=%q", got[0].AIWork)
		}
		if _, err := os.Stat(workTitleClaimPath(t)); !os.IsNotExist(err) {
			t.Errorf("성공 뒤에도 claim 이 남았다: %v", err)
		}
	})
}

// 제목이 아닌 답은 다시 물어도 같을 가능성이 크다. 그 글에는 더 묻지 않고 원문을 그린다.
func TestUnusableReplyIsNotAskedAgain(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		inputs := withWorkTitleModel(t, func(string) (string, error) {
			return strings.Repeat("요청하신 일은 제목을 짓는 역할 밖이라 하지 않겠습니다. ", 4), nil
		})

		TitleNextClaudeWork(listWork("project"))
		got := listWork("project")
		if got[0].AIWork != workPrompt || got[0].AIWorkUntitled {
			t.Errorf("AIWork=%q untitled=%v, want 원문과 false", got[0].AIWork, got[0].AIWorkUntitled)
		}
		TitleNextClaudeWork(got)
		if len(*inputs) != 1 {
			t.Errorf("모델 호출 %d번, want 1", len(*inputs))
		}
	})
}

// 프롬프트에는 사람이 붙여 넣은 무엇이든 있을 수 있다. 디스크에는 해시와 제목만 남긴다(panel.md).
func TestWorkTitleStoreKeepsNoPrompt(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		dir, err := workTitleDir()
		if err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range entries {
			if strings.Contains(e.Name(), fixtureIdleSessionID) {
				t.Errorf("파일 이름에 대화 id: %s", e.Name())
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), workPrompt) {
				t.Errorf("%s 에 프롬프트가 있다: %s", e.Name(), data)
			}
			info, err := e.Info()
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf("%s 권한 %o, want 600", e.Name(), mode)
			}
			found = found || strings.Contains(string(data), workTitle)
		}
		if !found {
			t.Errorf("제목이 저장되지 않았다: %v", entries)
		}
	})
}

// 다른 패널이 지은 제목도 디스크에서 읽어 그린다 — 이 프로세스의 캐시만 보면 패널마다 다시 부른다.
func TestTitleFromAnotherPanelIsRead(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		resetWorkTitleCache()
		if got := listWork("project"); got[0].AIWork != workTitle {
			t.Errorf("AIWork=%q, want %q", got[0].AIWork, workTitle)
		}
	})
}

func TestBrokenStoreIsAskedAgain(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		dir, err := workTitleDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, workTitleKey(fixtureIdleSessionID)+".json")
		if err := os.WriteFile(path, []byte(`{"source":`), 0o600); err != nil {
			t.Fatal(err)
		}
		got := listWork("project")
		if got[0].AIWork != workPrompt || !got[0].AIWorkUntitled {
			t.Errorf("AIWork=%q untitled=%v", got[0].AIWork, got[0].AIWorkUntitled)
		}
	})
}

func TestOldTitlesAreSwept(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		dir, err := workTitleDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		old, recent := filepath.Join(dir, "old.json"), filepath.Join(dir, "recent.json")
		for _, p := range []string{old, recent} {
			if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		longAgo := time.Now().Add(-workTitleKeepFor - time.Hour)
		if err := os.Chtimes(old, longAgo, longAgo); err != nil {
			t.Fatal(err)
		}

		withWorkTitleModel(t, fixedTitle)
		TitleNextClaudeWork(listWork("project"))

		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("오래된 파일이 남았다: %v", err)
		}
		if _, err := os.Stat(recent); err != nil {
			t.Errorf("최근 파일이 지워졌다: %v", err)
		}
	})
}

func TestCleanWorkTitle(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"그대로", "MQTT 인증서 교체", "MQTT 인증서 교체"},
		{"따옴표", `"MQTT 인증서 교체"`, "MQTT 인증서 교체"},
		{"낫표", "«MQTT 인증서 교체»", "MQTT 인증서 교체"},
		{"마침표", "MQTT 인증서 교체.", "MQTT 인증서 교체"},
		{"줄바꿈", "MQTT 인증서\n교체", "MQTT 인증서 교체"},
		{"제어 문자", "MQTT \x1b[31m인증서\x1b[0m 교체\x07", "MQTT 인증서 교체"},
		{"비었다", "  ", ""},
		{"문장은 제목이 아니다", strings.Repeat("제목이 아닌 긴 문장입니다 ", 8), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanWorkTitle(tt.in); got != tt.want {
				t.Errorf("cleanWorkTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseWorkTitleReply(t *testing.T) {
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "성공", in: `{"type":"result","is_error":false,"structured_output":{"title":"제목"}}`, want: "제목"},
		{name: "오류 응답", in: `{"type":"result","is_error":true,"result":"Not logged in"}`, wantErr: true},
		{name: "구조화 출력이 없다", in: `{"type":"result","is_error":false,"result":"제목"}`, wantErr: true},
		{name: "JSON 이 아니다", in: `Not logged in`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWorkTitleReply([]byte(tt.in))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("got %q, %v; want %q, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// 이 호출은 사람이 없는 자리에서 사람이 붙여 넣은 글을 읽는다. 도구가 하나라도 있으면 그 글이 명령이 될 수 있다.
// 사용자 설정을 읽으면 훅이 돌고(SessionStart·Stop), 기록을 남기면 다음 스캔이 이 호출을 세션으로 본다.
func TestWorkTitleCommandCannotAct(t *testing.T) {
	args := workTitleArgs()
	for _, pair := range [][2]string{
		{"--tools", ""},
		{"--setting-sources", ""},
		{"--model", workTitleModel},
		{"--output-format", "json"},
	} {
		i := slices.Index(args, pair[0])
		if i < 0 || i+1 >= len(args) || args[i+1] != pair[1] {
			t.Errorf("%s %q 가 없다: %q", pair[0], pair[1], args)
		}
	}
	for _, flag := range []string{"-p", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands"} {
		if !slices.Contains(args, flag) {
			t.Errorf("%s 가 없다: %q", flag, args)
		}
	}
	// --bare 는 OAuth 를 읽지 않는다. 구독으로 로그인한 환경에서는 호출마다 실패한다.
	if slices.Contains(args, "--bare") {
		t.Errorf("--bare 를 쓰면 안 된다: %q", args)
	}
	i := slices.Index(args, "--json-schema")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("--json-schema 가 없다: %q", args)
	}
	if !json.Valid([]byte(args[i+1])) {
		t.Errorf("스키마가 JSON 이 아니다: %s", args[i+1])
	}
}

// TMUX 가 남아 있으면 Claude 가 상태 파일에 이 pane 을 적어, 패널의 세션이 잠깐 Claude 세션으로 보인다.
func TestWorkTitleEnvLeavesTmux(t *testing.T) {
	got := workTitleEnv([]string{"HOME=/h", "TMUX=/s,1,2", "TMUX_PANE=%3", "PATH=/b", "TMUX_TMPDIR=/t"})
	want := []string{"HOME=/h", "PATH=/b", "TMUX_TMPDIR=/t"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClaudeBinaryFallsBackToTheInstallDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })

	if got := findClaudeBinary(); got != "" {
		t.Errorf("없는데 %q", got)
	}
	bin := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findClaudeBinary(); got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

// 패널(mux watch)의 자식 claude 는 제목을 짓는 일회용 호출이다. 세면 패널이 있는 모든 세션이 그 몇 초 동안
// Claude 세션으로 보이고, mux agent ls 에 답할 수 없는 대상으로 오른다.
func TestChildOfMuxIsNotAnAgent(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("4243\n"), nil, "pgrep", "-P", "4242")
		m.OnOutput([]byte("/home/u/.local/bin/claude -p --model haiku\n"), nil, "ps", "-o", "args=", "-p", "4243")
		if got := resolveCommand(4242, "mux"); got != "mux" {
			t.Errorf("resolveCommand = %q, want mux", got)
		}
	})
}

// fakeClaudeOnPath 는 PATH 에 가짜 claude 를 두고 제목 저장소를 임시 폴더로 돌린다. 스크립트는 받은 것을 seen 에 남긴다.
func fakeClaudeOnPath(t *testing.T, body string) (seen string) {
	t.Helper()
	bin, dir := t.TempDir(), t.TempDir()
	seen = filepath.Join(dir, "seen")
	script := "#!/bin/sh\n{ cat; echo; echo \"pwd=$(pwd)\"; echo \"tmux=${TMUX:-none}\"; echo \"pane=${TMUX_PANE:-none}\"; echo \"args=$*\"; } > '" + seen + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/sock,1,2")
	t.Setenv("TMUX_PANE", "%9")
	old := workTitleDir
	workTitleDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { workTitleDir = old })
	return seen
}

// 프롬프트는 stdin 으로 간다. argv 면 ps 에 보이고, 프롬프트가 곧 명령줄이 된다.
func TestRunWorkTitleModelPassesTheInputOnStdin(t *testing.T) {
	seen := fakeClaudeOnPath(t, `printf '%s' '{"is_error":false,"structured_output":{"title":"제목"}}'`)

	got, err := runWorkTitleModel("<now>붙여 넣은 비밀</now>")
	if err != nil || got != "제목" {
		t.Fatalf("got %q, %v", got, err)
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "<now>붙여 넣은 비밀</now>") {
		t.Errorf("stdin 으로 오지 않았다:\n%s", text)
	}
	if strings.Contains(text[strings.Index(text, "args="):], "붙여 넣은 비밀") {
		t.Errorf("argv 에 입력이 있다:\n%s", text)
	}
	if !strings.Contains(text, "tmux=none") || !strings.Contains(text, "pane=none") {
		t.Errorf("tmux 환경이 남았다:\n%s", text)
	}
	dir, _ := workTitleDir()
	if !strings.Contains(text, "pwd="+dir) {
		t.Errorf("프로젝트 밖에서 돌지 않았다:\n%s", text)
	}
}

func TestRunWorkTitleModelReportsWhyItFailed(t *testing.T) {
	fakeClaudeOnPath(t, `echo 'Not logged in' >&2; exit 1`)
	if _, err := runWorkTitleModel("<now>x</now>"); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("err = %v, want stderr 를 담은 오류", err)
	}
}
