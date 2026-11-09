package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// 픽스처 줄은 실제 기록(Claude Code 2.1.312)에서 본 모양을 줄였다. 쓰는 필드만 남겼다.

func trOrigin(at time.Time, content, kind string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": trTime(at), "isSidechain": false,
		"origin":  map[string]any{"kind": kind},
		"message": map[string]any{"role": "user", "content": content},
	})
	return string(b)
}

func trHuman(at time.Time, content string) string { return trOrigin(at, content, "human") }

func trRecap(at time.Time, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "system", "subtype": "away_summary", "timestamp": trTime(at),
		"content": text, "isMeta": false, "isSidechain": false,
	})
	return string(b)
}

// trCompactSummary 의 실제 줄에는 origin 이 없다(turnOrigin 뿐이다). 있어도 프롬프트가 아니라는 것을 본다.
func trCompactSummary(at time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": trTime(at), "isSidechain": false, "isCompactSummary": true,
		"origin":  map[string]any{"kind": "human"},
		"message": map[string]any{"role": "user", "content": "This session is being continued from a previous conversation."},
	})
	return string(b)
}

func trSidechainHuman(at time.Time, content string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": trTime(at), "isSidechain": true,
		"origin":  map[string]any{"kind": "human"},
		"message": map[string]any{"role": "user", "content": content},
	})
	return string(b)
}

const (
	workPrompt = "세션별로 현재 작업중인 내용을 패널에 표기해줘."
	workRecap  = "mux 작업 정리를 Mantis 에 등록하는 중입니다."
)

func TestLastWorkText(t *testing.T) {
	at := func(n int) time.Time { return trBase.Add(time.Duration(n) * time.Minute) }
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "요약보다 새 프롬프트가 지금 하는 일이다",
			lines: []string{trRecap(at(0), workRecap), trHuman(at(1), workPrompt), trAssistant(at(2), false)},
			want:  workPrompt,
		},
		{
			name:  "프롬프트보다 새 요약이 지금 하는 일이다",
			lines: []string{trHuman(at(0), workPrompt), trAssistant(at(1), false), trRecap(at(2), workRecap)},
			want:  workRecap,
		},
		{
			name:  "요약의 설정 안내는 작업이 아니다",
			lines: []string{trRecap(at(0), workRecap+" (disable recaps in /config)")},
			want:  workRecap,
		},
		{
			// 2026-11-04 실제 프롬프트 153개 중 폭 20 미만은 거의 «올려줘.»·«push 해줘.» 같은 응답이었다.
			name:  "짧은 응답은 앞의 프롬프트를 덮지 않는다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "커밋해줘."), trHuman(at(2), "#34 처리해줘.")},
			want:  workPrompt,
		},
		{
			name:  "짧은 응답은 앞의 요약도 덮지 않는다",
			lines: []string{trRecap(at(0), workRecap), trHuman(at(1), "push 해줘.")},
			want:  workRecap,
		},
		{
			name:  "폭 20 칸이면 일을 말한다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "kitty 업데이트 해줘.")},
			want:  "kitty 업데이트 해줘.",
		},
		{
			name: "mantis-progress 문의는 사람이 시킨 일이 아니다",
			lines: []string{
				trHuman(at(0), workPrompt),
				trHuman(at(1), "[mantis-progress] 지난 문의 뒤로 진행된 일을 JSON 한 줄로 답해 주세요."),
				trAssistant(at(2), false),
			},
			want: workPrompt,
		},
		{
			name: "다른 세션이 대신 친 프롬프트도 그 세션이 지금 하는 일이다",
			lines: []string{
				trHuman(at(0), workPrompt),
				trHuman(at(1), "[my-mux 세션의 요청] 파일을 읽고 일감 4건을 등록해 주세요."),
			},
			want: "[my-mux 세션의 요청] 파일을 읽고 일감 4건을 등록해 주세요.",
		},
		{
			name: "작업 알림은 사람이 친 것이 아니다",
			lines: []string{
				trHuman(at(0), workPrompt),
				trOrigin(at(1), "<task-notification>\n<task-id>b2kqhd11u</task-id>\n<status>completed</status>", "task-notification"),
			},
			want: workPrompt,
		},
		{
			name:  "도구 결과는 프롬프트가 아니다",
			lines: []string{trHuman(at(0), workPrompt), trToolResult(at(1))},
			want:  workPrompt,
		},
		{
			name:  "로컬 명령은 프롬프트가 아니다",
			lines: append([]string{trHuman(at(0), workPrompt)}, trTheme(at(1))...),
			want:  workPrompt,
		},
		{
			name:  "셸 모드는 프롬프트가 아니다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "<bash-input>git status --short --branch</bash-input>")},
			want:  workPrompt,
		},
		{
			name:  "meta 줄은 프롬프트가 아니다",
			lines: []string{trHuman(at(0), workPrompt), trUser(at(1), "Base directory for this skill: /home/u/.claude/skills/x", true)},
			want:  workPrompt,
		},
		{
			name:  "compact 요약은 프롬프트가 아니다",
			lines: []string{trHuman(at(0), workPrompt), trCompactSummary(at(1))},
			want:  workPrompt,
		},
		{
			name:  "서브에이전트의 프롬프트는 이 세션의 일이 아니다",
			lines: []string{trHuman(at(0), workPrompt), trSidechainHuman(at(1), "저장소를 훑어 관련 파일을 찾아 주세요.")},
			want:  workPrompt,
		},
		{
			// origin 없는 줄에는 사람이 안 친 것이 섞인다. 폭 29칸이라 폭으로는 걸러지지 않는다 — 실제 기록에
			// 30건 있었고, 세면 Esc 를 누를 때마다 작업 줄이 이 글이 된다.
			name:  "중단 표시는 프롬프트가 아니다",
			lines: []string{trHuman(at(0), workPrompt), trUser(at(1), "[Request interrupted by user]", false)},
			want:  workPrompt,
		},
		{
			name:  "origin 없는 줄은 사람이 친 것으로 치지 않는다",
			lines: []string{trHuman(at(0), workPrompt), trUser(at(1), "아래는 내 프로젝트 환경이다. JSON 배열만 출력하라.", false)},
			want:  workPrompt,
		},
		{
			name:  "origin 이 없는 기록만 있으면 비어 있다",
			lines: []string{trUser(at(0), workPrompt, false), trAssistant(at(1), false)},
			want:  "",
		},
		{
			// 실제 기록의 모양 그대로다 — 닫는 태그에도 id 가 붙는다.
			name:  "붙여 넣은 글은 표식만 벗긴다",
			lines: []string{trHuman(at(0), "<pasted_content id=\"5a2c\">\n사람 라벨을 새 프로젝트로 넘길 준비를 한다.\n</pasted_content id=\"5a2c\">")},
			want:  "사람 라벨을 새 프로젝트로 넘길 준비를 한다.",
		},
		{
			name:  "앱이 끼운 지시는 사람이 쓴 글이 아니다",
			lines: []string{trHuman(at(0), "<browser_instruction># Claude in Chrome\n도구 안내</browser_instruction>테스트중인 도커는 내려줘.")},
			want:  "테스트중인 도커는 내려줘.",
		},
		{
			name:  "앱이 끼운 안내도 사람이 쓴 글이 아니다",
			lines: []string{trHuman(at(0), "<system-reminder>\nscratch workspace 안내\n</system-reminder>\n\nCowork 의 선호 브라우저를 neo 로 해줘.")},
			want:  "Cowork 의 선호 브라우저를 neo 로 해줘.",
		},
		{
			name:  "인자 없는 슬래시 명령은 일을 말하지 않는다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "<command-message>matching-work</command-message>\n<command-name>/matching-work</command-name>")},
			want:  workPrompt,
		},
		{
			name:  "빈 인자도 인자가 없는 것이다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>")},
			want:  workPrompt,
		},
		{
			name:  "슬래시 명령의 인자는 일을 말한다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "<command-message>plan</command-message>\n<command-name>/plan</command-name>\n<command-args>패널의 세션 줄에 작업 표시를 더한다</command-args>")},
			want:  "패널의 세션 줄에 작업 표시를 더한다",
		},
		{
			name:  "모르는 태그로 시작하면 사람이 쓴 글이 아니다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "<new-wrapper-from-a-later-claude>본문이 무엇이든 태그째 찍지 않는다</new-wrapper-from-a-later-claude>")},
			want:  workPrompt,
		},
		{
			name:  "여러 줄은 한 줄로 편다",
			lines: []string{trHuman(at(0), "패널의 세션 아래에\n\n  현재 작업을\t적어 주세요")},
			want:  "패널의 세션 아래에 현재 작업을 적어 주세요",
		},
		{
			name:  "제어 문자와 이스케이프 열은 지운다",
			lines: []string{trHuman(at(0), "패널 \x1b[31m빨간\x1b[0m 글자\x07와 숨은‮ 방향 문자를 지운다")},
			want:  "패널 빨간 글자와 숨은 방향 문자를 지운다",
		},
		{
			name:  "그림 표식은 일을 말하지 않는다",
			lines: []string{trHuman(at(0), "[Image #3] 왼쪽 패널을 선택하면 점이 보임")},
			want:  "왼쪽 패널을 선택하면 점이 보임",
		},
		{
			name:  "그림 표식을 빼면 짧은 프롬프트도 응답이다",
			lines: []string{trHuman(at(0), workPrompt), trHuman(at(1), "[Image #1] [Image #2] 이거")},
			want:  workPrompt,
		},
		{
			name:  "프롬프트도 요약도 없으면 비어 있다",
			lines: []string{trAssistant(at(0), false), trSystem(at(1), "turn_duration")},
			want:  "",
		},
		{
			name:  "짧은 응답뿐이면 비어 있다",
			lines: []string{trHuman(at(0), "이어서 해줘."), trHuman(at(1), "응.")},
			want:  "",
		},
		{
			name:  "요약의 본문이 글이 아니면 없는 것이다",
			lines: []string{`{"type":"system","subtype":"away_summary","content":{"text":"x"}}`},
			want:  "",
		},
		{
			name:  "깨진 줄은 건너뛴다",
			lines: []string{trHuman(at(0), workPrompt), `{"type":"user","message":{`},
			want:  workPrompt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.jsonl")
			writeTranscriptFile(t, path, tt.lines)
			if got := lastWorkText(path); got != tt.want {
				t.Errorf("lastWorkText = %q, want %q", got, tt.want)
			}
		})
	}
}

// 긴 턴에서는 도구 줄이 수백 KB 쌓인다(실제 기록에서 결정하는 줄이 끝에서 657KB). 첫 꼬리 밖이어도 찾는다.
func TestLastWorkTextReachesPastALongTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{trHuman(trBase, workPrompt)}
	filler := trAssistant(trBase, false)
	for range activityFirstTail/len(filler) + 10 {
		lines = append(lines, filler)
	}
	writeTranscriptFile(t, path, lines)
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("lastWorkText = %q, want %q", got, workPrompt)
	}
}

func appendTranscript(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("append transcript: %v", err)
	}
}

// 기록은 덧붙이기만 하므로 한 번 읽은 곳은 다시 읽지 않는다 — 수십 MB 기록과 몇 시간짜리 턴에서 틱마다
// 꼬리를 다시 훑지 않는 근거다. 이미 읽은 자리를 바꿔 놓아 다시 읽으면 답이 달라지게 해서 확인한다.
func TestLastWorkTextReadsOnlyWhatWasAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{trHuman(trBase, workPrompt)})
	if got := lastWorkText(path); got != workPrompt {
		t.Fatalf("first read = %q", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 같은 바이트 수로 바꾼다 — 읽은 곳의 뜻이 그대로여야 한다.
	tampered := strings.Replace(string(data), "패널에", "책상에", 1)
	if tampered == string(data) {
		t.Fatal("fixture has no text to tamper with")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, trAssistant(trBase.Add(time.Minute), false)+"\n")
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("after a work-free append = %q, want the cached %q — an old offset was read again", got, workPrompt)
	}

	appendTranscript(t, path, trHuman(trBase.Add(2*time.Minute), "kitty 를 최신 버전으로 올려 주세요.")+"\n")
	if got := lastWorkText(path); got != "kitty 를 최신 버전으로 올려 주세요." {
		t.Errorf("after a new prompt = %q", got)
	}
}

// 쓰다 만 줄은 읽은 곳으로 치지 않는다. 치면 그 줄이 끝난 뒤에도 영영 읽지 않는다.
func TestLastWorkTextWaitsForAFinishedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{trHuman(trBase, workPrompt)})
	lastWorkText(path)

	const next = "이미지 붙여넣기가 왜 안 되는지 확인해줘."
	line := trHuman(trBase.Add(time.Minute), next)
	// 끝난 줄과 쓰다 만 줄이 한 번에 보이는 때가 위험하다 — 끝난 줄까지만 읽은 곳이다.
	appendTranscript(t, path, trAssistant(trBase, false)+"\n"+line[:len(line)/2])
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("half a line = %q, want %q until it is finished", got, workPrompt)
	}
	appendTranscript(t, path, line[len(line)/2:]+"\n")
	if got := lastWorkText(path); got != next {
		t.Errorf("finished line = %q, want %q", got, next)
	}
}

// 파일이 줄었으면 덧붙인 것이 아니다. 앞의 답과 읽은 곳은 이제 뜻이 없다.
func TestLastWorkTextRereadsAShrunkFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{
		trHuman(trBase, workPrompt),
		trAssistant(trBase, false),
		trAssistant(trBase, false),
	})
	lastWorkText(path)

	writeTranscriptFile(t, path, []string{trRecap(trBase, workRecap)})
	if got := lastWorkText(path); got != workRecap {
		t.Errorf("rewritten file = %q, want %q", got, workRecap)
	}

	writeTranscriptFile(t, path, []string{trAssistant(trBase, false)})
	if got := lastWorkText(path); got != "" {
		t.Errorf("rewritten file without work = %q, want empty — the old answer outlived its file", got)
	}
}

// 기록이 아직 없으면 비어 있고, 생기면 읽는다. 갓 띄운 Claude 가 그렇다.
func TestLastWorkTextReadsAFileThatAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if got := lastWorkText(path); got != "" {
		t.Errorf("no file yet = %q", got)
	}
	writeTranscriptFile(t, path, []string{trHuman(trBase, workPrompt)})
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("file appeared = %q, want %q", got, workPrompt)
	}
}

// 읽지 못한 것은 «한 일 없음» 이 아니다 — 아는 답을 지우지 않고, 읽은 곳도 옮기지 않아 다음에 다시 읽는다.
func TestLastWorkTextDoesNotCacheAReadFailure(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{trHuman(trBase, workPrompt)})
	chmod := func(mode os.FileMode) {
		t.Helper()
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}

	chmod(0o000)
	if got := lastWorkText(path); got != "" {
		t.Errorf("unreadable, nothing known = %q", got)
	}
	chmod(0o600)
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("readable again = %q, want %q", got, workPrompt)
	}

	const next = "이미지 붙여넣기가 왜 안 되는지 확인해줘."
	appendTranscript(t, path, trHuman(trBase.Add(time.Minute), next)+"\n")
	chmod(0o000)
	if got := lastWorkText(path); got != workPrompt {
		t.Errorf("unreadable, answer known = %q, want the known %q", got, workPrompt)
	}
	chmod(0o600)
	if got := lastWorkText(path); got != next {
		t.Errorf("readable again = %q, want %q — the failed read moved the offset", got, next)
	}
}

// 긴 붙여넣기가 틱마다 Session 을 타고 돌지 않게 자른다. 패널이 한 줄에 그릴 수 있는 것보다 충분히 길다.
func TestLastWorkTextCapsItsLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{trHuman(trBase, strings.Repeat("긴 붙여넣기 ", 5000))})
	got := lastWorkText(path)
	if n := utf8.RuneCountInString(got); n == 0 || n > maxWorkTextRunes {
		t.Errorf("%d runes, want 1..%d", n, maxWorkTextRunes)
	}
	if got != strings.TrimSpace(got) {
		t.Errorf("cut left an edge space: %q", got)
	}
}

func TestClaudeWorkWithoutASession(t *testing.T) {
	if got := claudeWork("", "/home/u/proj"); got != "" {
		t.Errorf("no session id = %q", got)
	}
}

// 상태와 상관없이 읽는다 — 승인을 기다리는 세션도, 일하는 중인 세션도 지금 하는 일을 말해야 한다.
// idle 에서만 읽는 LastActive 와 다른 점이다. 받은 목록은 고치지 않는다.
func TestWithClaudeWork(t *testing.T) {
	files := map[string]string{"11458.json": fixtureIdle, "15767.json": fixtureWaiting}
	withClaudeSessions(t, files, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		writeSessionTranscript(t, fixtureWaitingID, fixtureWaitingCWD, []string{trRecap(trBase, workRecap)})

		in := []Session{{Name: "project"}, {Name: "myname"}, {Name: "no-claude-here"}}
		got := WithClaudeWork(in)

		if got[0].AIWork != workPrompt {
			t.Errorf("idle: AIWork = %q, want %q", got[0].AIWork, workPrompt)
		}
		if got[1].AIWork != workRecap {
			t.Errorf("approval: AIWork = %q, want %q", got[1].AIWork, workRecap)
		}
		if got[2].AIWork != "" {
			t.Errorf("no Claude: AIWork = %q", got[2].AIWork)
		}
		if in[0].AIWork != "" {
			t.Errorf("the caller's list was changed: %q", in[0].AIWork)
		}
	})
}

// 상태 파일의 name 은 /clear 뒤에도 옛 대화의 것이다(2026-11-04 my-mux: fix-intellij-viewport-height).
// 작업 줄은 그 값으로 물러서지 않는다 — 기록에 없으면 비어 있고, 패널이 이벤트 줄을 그린다.
func TestWithClaudeWorkDoesNotFallBackToTheName(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		if ClaudeStatuses()["project"].Name == "" {
			t.Fatal("fixture has no name to fall back to")
		}
		if got := WithClaudeWork([]Session{{Name: "project"}}); got[0].AIWork != "" {
			t.Errorf("no transcript: AIWork = %q, want empty", got[0].AIWork)
		}
	})
}

// 목록을 읽는 길은 대화 기록을 작업 줄 때문에 읽지 않는다. mux border 는 pane 마다 15초에 한 번 새 프로세스로
// 이 길을 지나므로, 여기서 읽으면 쓰지도 않을 꼬리를 캐시 없이 매번 넓혀 읽는다(WithClaudeWork 의 주석).
func TestStatusScanDoesNotReadTheWork(t *testing.T) {
	withClaudeSessions(t, map[string]string{"11458.json": fixtureIdle}, nil, func() {
		writeSessionTranscript(t, fixtureIdleSessionID, fixtureIdleCWD, []string{trHuman(trBase, workPrompt)})
		path, err := transcriptPath(fixtureIdleSessionID, fixtureIdleCWD)
		if err != nil {
			t.Fatal(err)
		}

		if got := scanClaudeStatuses()["project"]; got.SessionID == "" {
			t.Fatal("the scan found no session")
		}
		workCacheMu.Lock()
		_, read := workCache[path]
		workCacheMu.Unlock()
		if read {
			t.Error("the status scan read the work line")
		}
	})
}

func TestLastWorkTextIsSafeConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeTranscriptFile(t, path, []string{trHuman(trBase, workPrompt)})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := lastWorkText(path); got != workPrompt {
				t.Errorf("lastWorkText = %q", got)
			}
		}()
	}
	wg.Wait()
}
