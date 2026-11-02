package tmux

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 트랜스크립트 — Claude 대화 기록 ~/.claude/projects/<cwd>/<sessionId>.jsonl 에서
// 턴을 나누고 회신을 고른다. `mux agent wait/read` 가 쓴다(docs/design/agent-api.md).
//
// 화면이 아니라 이 파일을 읽는 이유: Claude 는 대체 화면에 그려서 capture-pane 으로는
// 보이는 부분만 읽힌다. 회차 루프가 먼저 겪었고, 회신은 늘 여기서 꺼냈다.

// maxTranscriptChain 은 continued-in 을 따라가는 파일 수의 상한이다. compact 가 그만큼
// 이어지는 대화는 없다 — 고리가 생긴 기록을 끝없이 돌지 않게 하는 울타리다.
const maxTranscriptChain = 20

// transcriptRow 는 JSONL 한 줄에서 쓰는 필드만 남긴 것이다. Claude 의 비공개 형식이라
// 모든 필드는 없어도 되고, 없으면 제로값이다.
type transcriptRow struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Timestamp        string `json:"timestamp"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	TurnPosition *struct {
		TurnIndex int `json:"turnIndex"`
	} `json:"turnPosition"`
	RequestID            string `json:"requestId"`
	UUID                 string `json:"uuid"`
	ContinuedInSessionID string `json:"continuedInSessionId"`
	Message              struct {
		ID         string          `json:"id"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`

	// at 은 Timestamp 를 시간으로 푼 것이다. 문자열로 비교하면 "…43.512Z" 가 "…43Z"
	// 보다 앞에 정렬된다 — 회차 루프가 한 번 그렇게 틀렸다.
	at time.Time
}

// transcriptNeedles 는 디코딩할 가치가 있는 줄의 표식이다. 대화 기록에는 수 MB 짜리
// api-request-blob 줄이 섞여 있어, 전부 디코딩하면 wait 의 폴링이 무거워진다.
var transcriptNeedles = [][]byte{
	[]byte(`"type":"user"`),
	[]byte(`"type":"assistant"`),
	[]byte(`"type":"system"`),
	[]byte(`"type":"continued-in"`),
}

// transcriptPath 는 대화의 기록 파일을 찾는다. cwd 로 만든 경로가 먼저고, 없으면
// projects/*/ 에서 찾는다 — encodePath 가 Claude 의 인코딩과 어긋나는 경로(비 ASCII)가
// 있어도 기록을 놓치지 않게.
func transcriptPath(sessionID, cwd string) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	projects := filepath.Join(home, claudeDir, "projects")
	if cwd != "" {
		p := filepath.Join(projects, encodePath(cwd), sessionID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(projects, "*", sessionID+".jsonl"))
	if len(matches) > 0 {
		return matches[0], nil
	}
	return "", fmt.Errorf("대화 기록을 찾지 못했다: %s", sessionID)
}

// readTranscriptRows 는 기록 파일 하나를 읽는다. 1MB 가 넘는 줄과 NUL 이 든 줄이
// 실제로 있어서 Scanner 의 상한 대신 bufio.Reader 를 쓰고, 디코딩에 실패한 줄(반쯤
// 쓰인 마지막 줄 등)은 건너뛴다.
func readTranscriptRows(r io.Reader) []transcriptRow {
	var rows []transcriptRow
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && hasAnyNeedle(line) {
			var row transcriptRow
			if json.Unmarshal(line, &row) == nil {
				if t, perr := time.Parse(time.RFC3339Nano, row.Timestamp); perr == nil {
					row.at = t
				}
				rows = append(rows, row)
			}
		}
		if err != nil {
			return rows
		}
	}
}

func hasAnyNeedle(line []byte) bool {
	for _, n := range transcriptNeedles {
		if bytes.Contains(line, n) {
			return true
		}
	}
	return false
}

// transcriptChain 은 sessionID 의 기록에서 시작해 continued-in 을 따라간 줄 전체와,
// 거쳐 간 세션 id 목록을 돌려준다. compact 가 턴 도중에 일어나면 프롬프트는 앞 파일에,
// 회신은 뒤 파일에 남기 때문에 둘을 이어 읽어야 한다.
func transcriptChain(sessionID, cwd string) ([]transcriptRow, []string, error) {
	var all []transcriptRow
	var ids []string
	seen := map[string]bool{}
	for id := sessionID; id != "" && !seen[id] && len(ids) < maxTranscriptChain; {
		seen[id] = true
		path, err := transcriptPath(id, cwd)
		if err != nil {
			if len(ids) == 0 {
				return nil, nil, err
			}
			// 이어지는 파일이 아직 없다(compact 직후). 지금까지 읽은 것으로 판단하되 그 대화 id 는
			// 사슬에 넣는다 — 상태 파일은 이미 그 id 를 가리킬 수 있다.
			ids = append(ids, id)
			break
		}
		f, err := os.Open(path)
		if err != nil {
			if len(ids) == 0 {
				return nil, nil, fmt.Errorf("대화 기록을 열지 못했다: %w", err)
			}
			break
		}
		rows := readTranscriptRows(f)
		f.Close()
		ids = append(ids, id)
		all = append(all, rows...)
		id = continuedIn(rows)
	}
	return all, ids, nil
}

// continuedIn 은 기록이 이어지는 다음 세션 id 다. 없으면 "".
func continuedIn(rows []transcriptRow) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Type == "continued-in" && rows[i].ContinuedInSessionID != "" {
			return rows[i].ContinuedInSessionID
		}
	}
	return ""
}

// transcriptTurns 는 턴 시작 줄의 위치다. 지금의 Claude 는 턴을 여는 user 줄마다
// turnPosition 을 단다(사람·작업 알림·peer·system 모두). compact 요약 줄도 같은 번호를
// 다시 쓰므로 뺀다. turnPosition 이 하나도 없는 옛 기록은 보수적인 대체 규칙을 쓴다.
func transcriptTurns(rows []transcriptRow) []int {
	modern := false
	for _, r := range rows {
		if r.Type == "user" && r.TurnPosition != nil {
			modern = true
			break
		}
	}
	var starts []int
	for i, r := range rows {
		if r.Type != "user" || r.IsCompactSummary || r.IsSidechain {
			continue
		}
		if modern {
			if r.TurnPosition != nil {
				starts = append(starts, i)
			}
			continue
		}
		if !r.IsMeta && legacyPromptText(r.Message.Content) {
			starts = append(starts, i)
		}
	}
	return starts
}

// legacyPromptText 는 옛 기록에서 «사람이 친 프롬프트» 처럼 보이는 user 줄인지 본다:
// 글이 있고, tool_result 만 있는 줄이 아니고, 로컬 명령 태그가 아니다.
func legacyPromptText(content json.RawMessage) bool {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s != "" && !bytes.Contains([]byte(s), []byte("<local-command")) &&
			!bytes.Contains([]byte(s), []byte("<command-name>"))
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return true
		}
	}
	return false
}

// isHumanTurn 은 사람이 친(우리가 tmux 로 친 것도 같다) 프롬프트로 시작한 턴인지 본다.
// 작업 알림·peer 메시지·system 턴은 isMeta 이거나 origin 이 human 이 아니다.
func isHumanTurn(r transcriptRow) bool {
	if r.Origin != nil {
		return r.Origin.Kind == "human"
	}
	return !r.IsMeta
}

// turnSpan 은 고른 턴의 [start, end) 범위다. open 은 뒤에 다른 턴이 아직 없다는 뜻이다 —
// 그 턴이 진행 중일 수도, 끝났는데 다음 프롬프트가 없을 뿐일 수도 있다.
type turnSpan struct {
	start, end int
	open       bool
}

// findTurn 은 after 가 있으면 그 시각 이후의 사람 턴을, 없으면 마지막 턴을 고른다. after 와 같은
// 밀리초도 인정한다 — 프롬프트 줄은 늘 보낸 시각 뒤에 쓰인다.
//
// textHash 가 있으면 그 시각 이후의 사람 턴 중 본문 해시가 같은 것을 먼저 고른다(AgentToken 의
// 주석 — 대기열의 다른 글). 맞는 것이 없으면 시각 이후의 첫 사람 턴이다.
func findTurn(rows []transcriptRow, after *time.Time, textHash uint32) (turnSpan, bool) {
	starts := transcriptTurns(rows)
	if len(starts) == 0 {
		return turnSpan{}, false
	}
	pick := -1
	if after == nil {
		pick = len(starts) - 1
	} else {
		for k, i := range starts {
			r := rows[i]
			if !isHumanTurn(r) || r.at.IsZero() || r.at.Before(*after) {
				continue
			}
			if pick < 0 {
				pick = k
			}
			if textHash != 0 && promptHash(userText(r.Message.Content)) == textHash {
				pick = k
				break
			}
		}
	}
	if pick < 0 {
		return turnSpan{}, false
	}
	span := turnSpan{start: starts[pick], end: len(rows), open: true}
	idx := turnIndexOf(rows[starts[pick]])
	for _, i := range starts[pick+1:] {
		// 같은 번호를 다시 쓰는 줄은 같은 턴이다(작업 알림이 턴 도중에 끼는 경우).
		if idx >= 0 && turnIndexOf(rows[i]) == idx {
			continue
		}
		span.end, span.open = i, false
		break
	}
	return span, true
}

// userText 는 user 줄의 본문이다: 문자열이면 그대로, 블록이면 text 블록을 이어서.
func userText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, bl := range blocks {
		if bl.Type == "text" {
			b.WriteString(bl.Text)
		}
	}
	return b.String()
}

func turnIndexOf(r transcriptRow) int {
	if r.TurnPosition == nil {
		return -1
	}
	return r.TurnPosition.TurnIndex
}

// turnReply 는 턴의 회신이다: 마지막 assistant 응답 그룹(requestId 단위)의 글.
// final 은 그 그룹이 글을 갖고 tool_use 가 아닌 이유로 멈췄다는 뜻이다.
//
// 앞 그룹으로 물러서지 않는다. 마지막 그룹에 글이 없으면 회신이 없는 것이다 — 물러서면
// «확인해 보겠다» 같은 중간 문장이 회신으로 나간다.
func turnReply(rows []transcriptRow, span turnSpan) (text string, final bool) {
	type group struct {
		texts    []string
		lastStop string
	}
	var order []string
	groups := map[string]*group{}
	for _, r := range rows[span.start:span.end] {
		if r.Type != "assistant" || r.IsSidechain {
			continue
		}
		key := r.RequestID
		if key == "" {
			key = r.Message.ID
		}
		if key == "" {
			key = r.UUID
		}
		g, ok := groups[key]
		if !ok {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		g.texts = append(g.texts, assistantTexts(r.Message.Content)...)
		if r.Message.StopReason != "" {
			g.lastStop = r.Message.StopReason
		}
	}
	if len(order) == 0 {
		return "", false
	}
	g := groups[order[len(order)-1]]
	text = joinTexts(g.texts)
	return text, text != "" && g.lastStop != "" && g.lastStop != "tool_use"
}

func assistantTexts(content json.RawMessage) []string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return out
}

func joinTexts(texts []string) string {
	var b bytes.Buffer
	for _, t := range texts {
		t = string(bytes.TrimSpace([]byte(t)))
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(t)
	}
	return b.String()
}

// turnEnded 는 턴이 끝났다는 기록상의 증거다: 뒤에 다른 턴이 시작됐거나, turn_duration 줄이 있다.
// 상태 파일의 statusUpdatedAt 이 움직이지 않는 경우(shell 이 idle 로 강등될 때)를 위한 것이다.
//
// «마지막 응답 그룹이 end_turn 으로 끝났다» 는 증거로 받지 않는다. Stop 훅이 턴을 막아 이어 가면
// (이 사용자의 go vet 훅이 그렇다) end_turn 그룹 뒤에 턴이 계속된다 — 그 모양만 보고 끝냈다간
// wait 가 일찍 끝나고 read 가 훅 이전의 글을 낸다.
func turnEnded(rows []transcriptRow, span turnSpan) bool {
	if !span.open {
		return true
	}
	for _, r := range rows[span.start:span.end] {
		if r.Type == "system" && r.Subtype == "turn_duration" {
			return true
		}
	}
	return false
}
