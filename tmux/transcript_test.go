package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 대화 기록 줄을 만드는 도우미. 실측한 Claude 2.1.312 기록의 모양을 따른다:
// 턴을 여는 user 줄에 turnPosition, 사람 턴은 origin.kind "human", 회신은 requestId 단위.

func jsonl(t *testing.T, rows ...map[string]any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func humanTurn(ts string, idx int, text string) map[string]any {
	return map[string]any{
		"type": "user", "timestamp": ts, "promptSource": "typed",
		"origin":       map[string]any{"kind": "human"},
		"turnPosition": map[string]any{"promptIndex": idx, "turnIndex": idx},
		"message":      map[string]any{"role": "user", "content": text},
	}
}

func metaTurn(ts string, idx int, text string) map[string]any {
	return map[string]any{
		"type": "user", "timestamp": ts, "isMeta": true,
		"origin":       map[string]any{"kind": "task-notification"},
		"turnPosition": map[string]any{"turnIndex": idx},
		"message":      map[string]any{"role": "user", "content": text},
	}
}

func toolResult(ts string) map[string]any {
	return map[string]any{
		"type": "user", "timestamp": ts,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": "ok"}}},
	}
}

func assistantRow(ts, rid, stop string, blocks ...map[string]any) map[string]any {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = b
	}
	msg := map[string]any{"role": "assistant", "content": content}
	if stop != "" {
		msg["stop_reason"] = stop
	}
	return map[string]any{"type": "assistant", "timestamp": ts, "requestId": rid, "message": msg}
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func toolUse() map[string]any      { return map[string]any{"type": "tool_use", "name": "Bash"} }
func thinking() map[string]any     { return map[string]any{"type": "thinking", "thinking": "…"} }

func rowsOf(t *testing.T, s string) []transcriptRow {
	t.Helper()
	return readTranscriptRows(strings.NewReader(s))
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestTurnReplyTakesTheLastGroupOnly(t *testing.T) {
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "do it"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "tool_use", text("확인해 보겠다."), toolUse()),
		toolResult("2026-11-02T03:00:02.000Z"),
		assistantRow("2026-11-02T03:00:03.000Z", "r2", "end_turn", thinking(), text("결과는 이렇다."), text("끝.")),
	))
	span, ok := findTurn(rows, nil, 0)
	if !ok {
		t.Fatal("턴을 찾지 못했다")
	}
	got, final := turnReply(rows, span)
	if !final || got != "결과는 이렇다.\n\n끝." {
		t.Errorf("turnReply = %q, %v", got, final)
	}
}

func TestTurnReplyNeverFallsBackToAnEarlierGroup(t *testing.T) {
	// 마지막 그룹에 글이 없다 — 앞 그룹의 «확인해 보겠다» 를 회신으로 내면 안 된다.
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "do it"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "tool_use", text("확인해 보겠다."), toolUse()),
		toolResult("2026-11-02T03:00:02.000Z"),
		assistantRow("2026-11-02T03:00:03.000Z", "r2", "end_turn", thinking()),
	))
	span, _ := findTurn(rows, nil, 0)
	if got, final := turnReply(rows, span); final || got != "" {
		t.Errorf("turnReply = %q, %v — 글 없는 마지막 그룹은 회신이 없어야 한다", got, final)
	}
}

func TestTurnReplyIsNotFinalWhileTheLastGroupCallsATool(t *testing.T) {
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "do it"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "tool_use", text("돌려 본다."), toolUse()),
	))
	span, _ := findTurn(rows, nil, 0)
	if _, final := turnReply(rows, span); final {
		t.Error("tool_use 로 멈춘 그룹을 끝난 회신으로 봤다")
	}
	if turnEnded(rows, span) {
		t.Error("도구를 부르는 중인 턴을 끝났다고 봤다")
	}
}

func TestFindTurnAfterPicksOurHumanTurn(t *testing.T) {
	// 우리 프롬프트 뒤에 작업 알림 턴이 끼어도, --after 는 사람 턴을 고르고 그 턴은 알림
	// 턴이 시작되는 곳에서 끝난다.
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T02:59:00.000Z", 1, "earlier"),
		assistantRow("2026-11-02T02:59:01.000Z", "r0", "end_turn", text("old")),
		metaTurn("2026-11-02T03:00:00.100Z", 2, "<task-notification>"),
		assistantRow("2026-11-02T03:00:00.200Z", "r1", "end_turn", text("noted")),
		humanTurn("2026-11-02T03:00:00.512Z", 3, "ours"),
		assistantRow("2026-11-02T03:00:02Z", "r2", "end_turn", text("reply to ours")),
		metaTurn("2026-11-02T03:00:05.000Z", 4, "<task-notification>"),
		assistantRow("2026-11-02T03:00:06.000Z", "r3", "end_turn", text("another")),
	))
	after := mustTime(t, "2026-11-02T03:00:00.000Z")
	span, ok := findTurn(rows, &after, 0)
	if !ok {
		t.Fatal("우리 턴을 못 찾았다")
	}
	if span.open {
		t.Error("뒤에 다른 턴이 있는데 open 이다")
	}
	if got, final := turnReply(rows, span); !final || got != "reply to ours" {
		t.Errorf("turnReply = %q, %v", got, final)
	}
}

func TestFindTurnAfterAcceptsTheSameMillisecondAndMixedPrecision(t *testing.T) {
	// "…43Z" 와 "…43.512Z" 를 문자열로 비교하면 순서가 뒤집힌다. 시간으로 비교한다.
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:43Z", 1, "whole second"),
		humanTurn("2026-11-02T03:00:43.512Z", 2, "ours"),
	))
	after := mustTime(t, "2026-11-02T03:00:43.512Z")
	span, ok := findTurn(rows, &after, 0)
	if !ok || span.start != 1 {
		t.Fatalf("findTurn = %+v, %v — 같은 밀리초의 턴을 골라야 한다", span, ok)
	}
	late := mustTime(t, "2026-11-02T03:00:44Z")
	if _, ok := findTurn(rows, &late, 0); ok {
		t.Error("보낸 시각 뒤에 턴이 없는데 찾았다")
	}
}

func TestFindTurnSkipsCompactSummaryAndSidechain(t *testing.T) {
	summary := humanTurn("2026-11-02T03:00:05.000Z", 1, "summary")
	summary["isCompactSummary"] = true
	side := humanTurn("2026-11-02T03:00:06.000Z", 2, "side")
	side["isSidechain"] = true
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "ours"),
		summary, side,
		assistantRow("2026-11-02T03:00:07.000Z", "r1", "end_turn", text("done")),
	))
	span, ok := findTurn(rows, nil, 0)
	if !ok || span.start != 0 || !span.open {
		t.Fatalf("findTurn = %+v, %v", span, ok)
	}
}

func TestLegacyTranscriptWithoutTurnPosition(t *testing.T) {
	legacy := func(ts, text string) map[string]any {
		return map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": text}}
	}
	rows := rowsOf(t, jsonl(t,
		legacy("2026-11-02T03:00:00.000Z", "first"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "end_turn", text("one")),
		legacy("2026-11-02T03:00:02.000Z", "<command-name>/rename</command-name>"),
		toolResult("2026-11-02T03:00:03.000Z"),
		legacy("2026-11-02T03:00:04.000Z", "second"),
		assistantRow("2026-11-02T03:00:05.000Z", "r2", "end_turn", text("two")),
	))
	starts := transcriptTurns(rows)
	if len(starts) != 2 {
		t.Fatalf("옛 기록의 턴 = %v, 둘이어야 한다 (로컬 명령·tool_result 는 턴이 아니다)", starts)
	}
	span, _ := findTurn(rows, nil, 0)
	if got, _ := turnReply(rows, span); got != "two" {
		t.Errorf("turnReply = %q", got)
	}
}

func TestTurnEndedByTurnDuration(t *testing.T) {
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "go"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "", text("partial")), // stop_reason 이 아직 없다
		map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": "2026-11-02T03:00:02.000Z"},
	))
	span, _ := findTurn(rows, nil, 0)
	if !turnEnded(rows, span) {
		t.Error("turn_duration 줄이 있으면 턴이 끝난 것이다")
	}
}

func TestReadTranscriptRowsSurvivesHugeAndTornLines(t *testing.T) {
	huge := map[string]any{"type": "api-request-blob", "blob": strings.Repeat("x", 2<<20)}
	bigAssistant := assistantRow("2026-11-02T03:00:01.000Z", "r1", "end_turn", text(strings.Repeat("가", 600_000)))
	s := jsonl(t, humanTurn("2026-11-02T03:00:00.000Z", 1, "go"), huge, bigAssistant) +
		`{"type":"assistant","timestamp":"2026-11-02T03:00:09` // 반쯤 쓰인 마지막 줄
	rows := rowsOf(t, s)
	if len(rows) != 2 {
		t.Fatalf("줄 수 = %d, 사람 턴과 큰 회신 둘이어야 한다", len(rows))
	}
	span, _ := findTurn(rows, nil, 0)
	if got, final := turnReply(rows, span); !final || len([]rune(got)) != 600_000 {
		t.Errorf("1MB 넘는 회신을 읽지 못했다 (final=%v, len=%d)", final, len([]rune(got)))
	}
}

// withProjects 는 임시 HOME 의 ~/.claude/projects 에 기록 파일을 둔다.
func withProjects(t *testing.T, files map[string]string) {
	t.Helper()
	home := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(home, claudeDir, "projects", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })
}

func TestTranscriptChainFollowsContinuedIn(t *testing.T) {
	// compact 가 턴 도중에 일어나면 프롬프트는 A 에, 회신은 B 에 남는다.
	cwd := "/home/u/dev/my_proj"
	dir := encodePath(cwd)
	withProjects(t, map[string]string{
		dir + "/A.jsonl": jsonl(t,
			humanTurn("2026-11-02T03:00:00.000Z", 1, "ours"),
			assistantRow("2026-11-02T03:00:01.000Z", "r1", "tool_use", text("작업 중"), toolUse()),
			map[string]any{"type": "continued-in", "continuedInSessionId": "B"},
		),
		dir + "/B.jsonl": jsonl(t,
			assistantRow("2026-11-02T03:00:09.000Z", "r2", "end_turn", text("compact 뒤의 회신")),
		),
	})
	rows, ids, err := transcriptChain("A", cwd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "A,B" {
		t.Errorf("거쳐 간 대화 = %v", ids)
	}
	after := mustTime(t, "2026-11-02T03:00:00.000Z")
	span, _ := findTurn(rows, &after, 0)
	if got, final := turnReply(rows, span); !final || got != "compact 뒤의 회신" {
		t.Errorf("turnReply = %q, %v", got, final)
	}
}

func TestTranscriptPathFallsBackToGlob(t *testing.T) {
	// encodePath 가 Claude 의 인코딩과 어긋나도(비 ASCII 경로) 기록을 찾는다.
	withProjects(t, map[string]string{"-claude-own-encoding/S1.jsonl": "{}\n"})
	p, err := transcriptPath("S1", "/home/u/한글폴더")
	if err != nil || !strings.HasSuffix(p, "-claude-own-encoding/S1.jsonl") {
		t.Errorf("transcriptPath = %q, %v", p, err)
	}
	if _, err := transcriptPath("missing", "/x"); err == nil {
		t.Error("없는 대화에 경로를 냈다")
	}
}

func TestTranscriptChainStopsOnACycle(t *testing.T) {
	withProjects(t, map[string]string{
		"-x/A.jsonl": jsonl(t, map[string]any{"type": "continued-in", "continuedInSessionId": "B"}),
		"-x/B.jsonl": jsonl(t, map[string]any{"type": "continued-in", "continuedInSessionId": "A"}),
	})
	_, ids, err := transcriptChain("A", "/x")
	if err != nil || len(ids) != 2 {
		t.Errorf("고리 = %v, %v — 두 파일만 읽고 멈춰야 한다", ids, err)
	}
}

func TestFindTurnPrefersTheTurnWithOurText(t *testing.T) {
	// --force 로 보낸 글은 꺼낼 때의 시각으로 기록된다. 먼저 대기열에 있던 A 도 보낸 시각 뒤의
	// 사람 턴이므로, 시각만으로 고르면 B 의 토큰이 A 의 턴을 고른다.
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:05.000Z", 1, "queued A"),
		assistantRow("2026-11-02T03:00:06.000Z", "r1", "end_turn", text("reply to A")),
		humanTurn("2026-11-02T03:00:07.000Z", 2, "ours B"),
		assistantRow("2026-11-02T03:00:08.000Z", "r2", "end_turn", text("reply to B")),
	))
	after := mustTime(t, "2026-11-02T03:00:00.000Z")
	span, ok := findTurn(rows, &after, promptHash("ours B"))
	if got, _ := turnReply(rows, span); !ok || got != "reply to B" {
		t.Errorf("turnReply = %q, %v — 본문이 같은 턴을 골라야 한다", got, ok)
	}
	// 본문이 바뀌어 기록되는 경우(슬래시 명령)는 맞는 턴이 없다 — 시각으로 고른다.
	span, _ = findTurn(rows, &after, promptHash("/govgen-reply x"))
	if got, _ := turnReply(rows, span); got != "reply to A" {
		t.Errorf("해시가 안 맞으면 시각 이후 첫 사람 턴이어야 한다: %q", got)
	}
}

func TestTurnEndedIgnoresAnEndTurnGroupAlone(t *testing.T) {
	// Stop 훅이 턴을 이어 가면 end_turn 그룹 뒤에도 턴이 계속된다. 그 모양만으로 끝내지 않는다.
	rows := rowsOf(t, jsonl(t,
		humanTurn("2026-11-02T03:00:00.000Z", 1, "go"),
		assistantRow("2026-11-02T03:00:01.000Z", "r1", "end_turn", text("다 했다")),
	))
	span, _ := findTurn(rows, nil, 0)
	if turnEnded(rows, span) {
		t.Error("turn_duration 도 다음 턴도 없는데 끝났다고 봤다")
	}
}

func TestTranscriptChainCountsAPendingContinuation(t *testing.T) {
	// compact 직후: A 는 B 로 이어진다고 적었는데 B 파일이 아직 없다. 상태 파일은 이미 B 를
	// 가리킬 수 있으므로 B 도 사슬에 있어야 한다(없으면 토큰 불일치로 1).
	withProjects(t, map[string]string{
		"-x/A.jsonl": jsonl(t, map[string]any{"type": "continued-in", "continuedInSessionId": "B"}),
	})
	_, ids, err := transcriptChain("A", "/x")
	if err != nil || strings.Join(ids, ",") != "A,B" {
		t.Errorf("사슬 = %v, %v", ids, err)
	}
}
