package tmux

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// 지금 하는 일 — 패널이 세션 이름 아래에 적는 한 줄을 대화 기록에서 읽는다.
//
// 상태 파일의 name 은 이 일을 못 한다. /clear 뒤에도 옛 대화의 이름이 남고(2026-11-04 my-mux 는 Mantis
// 정리를 하면서 fix-intellij-viewport-height 였다), 폴더에서 만든 이름(mantis-5c, zorin-5d)은 일을 말하지
// 않는다. 기록의 ai-title 은 그보다 낫지만 대화의 주제라 작업이 바뀌어도 늦게 따라온다.
//
// 그래서 기록에서 가장 나중에 나온 둘 중 하나를 쓴다:
//   - away_summary: 자리를 비운 사이 Claude 가 쓴 요약. 일의 내용과 상태를 말한다.
//   - 사람이 친 프롬프트: 지금 시킨 일. 요약은 자리를 비워야 생기므로 일하는 동안은 이것뿐이다.
//
// 글을 고르는 데는 모델을 부르지 않는다. 틱마다 도는 경로이고, 이 두 글은 이미 공짜로 있다. 고른 글을 제목으로
// 다듬는 일만 틱 밖에서 따로 한다(claude_work_title.go). 제목이 생기기 전과 짓지 못했을 때는 이 글을 그대로 그린다.
//
// 이 값은 이벤트 기록(@mux_events, events.json)에 넣지 않는다. 프롬프트에는 사람이 붙여 넣은 무엇이든 있을 수
// 있어서, 화면에 그 pane 이 보여 주는 만큼만 보이고 디스크에는 새로 남기지 않는다.

const (
	// minWorkPromptWidth 는 일을 말한다고 볼 프롬프트의 최소 칸 수다. 2026-11-04 살아 있는 대화 7개의 프롬프트
	// 153개에서 20칸 아래는 «올려줘.»·«push 해줘.»·«#34 처리해줘.» 같은 응답이었고, 그 위로 «kitty 업데이트
	// 해줘.»(20칸) 부터 일을 말했다. 응답이 앞의 일을 덮으면 패널이 «커밋해줘.» 만 보여 준다.
	minWorkPromptWidth = 20

	// maxWorkTextRunes 는 Session 을 타고 틱마다 도는 글의 상한이다. 긴 붙여넣기를 들고 다니지 않게 자르고,
	// 패널이 한 줄에 그릴 수 있는 것보다는 길다.
	maxWorkTextRunes = 200

	// agentInquiryMarker 로 시작하는 프롬프트는 사람이 시킨 일이 아니라 mantis-progress 타이머의 진행 문의다
	// (dotfiles/bin/mantis-progress 의 MARKER). 세면 문의를 받은 세션의 작업 줄이 그 문의가 된다. 타이머는
	// 2026-11-05 에 꺼 두었지만 손으로 부를 수 있고, 이미 기록에 들어간 문의도 남아 있어 그대로 거른다. 다른 세션이
	// mux agent prompt 로 맡긴 일은 표식이 없고, 그 세션이 실제로 하는 일이라 센다.
	agentInquiryMarker = "[mantis-progress]"

	// recapConfigHint 는 Claude 가 요약 끝에 붙이는 설정 안내다. 언제나 붙지는 않는다.
	recapConfigHint = "(disable recaps in /config)"

	// 캐시의 상한. maxActivityCache 와 같은 근거다.
	maxWorkCache = 256
)

var (
	// imageTagRe 는 프롬프트에 그림을 붙이면 남는 표식이다. 일을 말하지 않고 앞자리 칸만 먹는다.
	imageTagRe = regexp.MustCompile(`\[Image #\d+\]`)
	// pastedTagRe 는 붙여 넣은 글을 감싸는 표식이다(닫는 태그에도 id 가 붙는다). 안의 글이 시킨 일이므로 표식만 벗긴다.
	pastedTagRe = regexp.MustCompile(`</?pasted_content[^>]*>`)
	// injectedBlockRe 는 앱이 프롬프트 앞에 끼운 안내다. 사람이 쓴 글이 아니므로 안의 글까지 지운다.
	injectedBlockRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>|<browser_instruction>.*?</browser_instruction>`)
	// commandArgsRe 는 슬래시 명령의 인자다. /plan 뒤에 쓴 글은 시킨 일이다.
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

// cachedWork 는 기록을 어디까지 읽었고 거기까지의 답이 무엇인지다. 기록은 덧붙이기만 하므로 그 뒤만 읽으면
// 되고, 새 줄에 답이 없으면 앞의 답이 그대로 답이다. 몇 시간짜리 턴이면 프롬프트가 끝에서 수 MB 뒤로 밀리는데,
// 틱마다 꼬리를 넓혀 가며 다시 읽을 일이 없다.
type cachedWork struct {
	scanned int64
	text    string
}

var (
	workCache   = map[string]cachedWork{}
	workCacheMu sync.Mutex
)

// WithClaudeWork 는 세션마다 지금 하는 일(AIWork)을 채운 새 목록을 돌려준다. 제목이 있으면 제목이고, 없으면 원문에
// AIWorkUntitled 를 세운다. 저장된 답만 읽고 모델은 부르지 않는다(TitleNextClaudeWork).
//
// ListSessions 에 넣지 않은 것은 비용 때문이다. mux border 는 pane 마다 status-interval(15초)로 새 프로세스가
// 떠서 ListSessions 와 같은 상태 스캔을 거치는데, 캐시 없는 프로세스는 매번 꼬리를 처음부터 넓혀 읽는다 —
// 2026-11-04 에 그 안에 넣어 재 보니 한 번에 읽는 양이 0.4MB → 2.3MB 였고, 그 값을 쓰지도 않았다. 며칠 도는
// 패널만 부르면 새로 붙은 바이트만 읽는다.
func WithClaudeWork(sessions []Session) []Session {
	statuses := ClaudeStatuses()
	out := make([]Session, len(sessions))
	for i, s := range sessions {
		if st, ok := statuses[s.Name]; ok {
			s.AIWork = claudeWork(st.SessionID, st.CWD)
			if s.AIWork != "" {
				title, known := storedWorkTitle(st.SessionID, s.AIWork)
				s.AIWorkUntitled = !known
				if title != "" {
					s.AIWork = title
				}
			}
		}
		out[i] = s
	}
	return out
}

// claudeWork 는 대화가 지금 하는 일을 한 줄로 말한다. 기록이 없거나 말할 것이 없으면 빈 값이고, 패널은 그때
// 마지막 이벤트를 그린다. 상태와 상관없이 읽는다 — 일하는 중인 세션이야말로 이것을 말해야 한다.
func claudeWork(sessionID, cwd string) string {
	if sessionID == "" {
		return ""
	}
	path, err := transcriptPath(sessionID, cwd)
	if err != nil {
		return ""
	}
	return lastWorkText(path)
}

// lastWorkText 는 패널 틱마다(2초) 불린다. 파일이 그대로면 드는 것은 stat 하나고, 자랐으면 자란 만큼만 읽는다.
func lastWorkText(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	size := info.Size()

	workCacheMu.Lock()
	c, hit := workCache[path]
	workCacheMu.Unlock()
	if hit && c.scanned == size {
		return c.text
	}

	// 줄었으면 덧붙인 것이 아니다 — 앞의 답도, 읽은 곳도 이제 뜻이 없다.
	known, from := "", int64(0)
	if hit && c.scanned < size {
		known, from = c.text, c.scanned
	}

	text, found, scanned, err := scanWorkText(path, from, size)
	if err != nil {
		// 읽기 실패는 «한 일 없음» 이 아니다. 아는 답을 그대로 두고, 읽은 곳도 옮기지 않아 다음에 다시 읽는다.
		return c.text
	}
	if !found {
		text = known
	}

	workCacheMu.Lock()
	if len(workCache) >= maxWorkCache {
		workCache = map[string]cachedWork{}
	}
	workCache[path] = cachedWork{scanned: scanned, text: text}
	workCacheMu.Unlock()
	return text
}

// scanWorkText 는 [from, size) 를 끝에서부터 넓혀 가며 마지막 작업 글을 찾는다. scanned 는 다음에 읽기 시작할
// 곳이다 — 쓰다 만 마지막 줄은 읽은 곳으로 치지 않는다. 치면 그 줄이 끝난 뒤에도 영영 읽지 않는다.
func scanWorkText(path string, from, size int64) (text string, found bool, scanned int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, 0, err
	}
	defer f.Close()

	scanned = from
	for n := int64(activityFirstTail); ; n *= 4 {
		start := max(size-n, from)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return "", false, 0, err
		}
		last := bytes.LastIndexByte(buf, '\n')
		if last >= 0 {
			scanned = max(scanned, start+int64(last)+1)
			// 꼬리가 읽은 곳보다 뒤에서 시작하면 첫 조각은 잘린 줄이다.
			if text, ok := workTextIn(buf[:last], start > from); ok {
				return text, true, scanned, nil
			}
		}
		if start == from {
			return "", false, scanned, nil
		}
		if n >= activityMaxTail {
			// 상한까지 없으면 앞은 포기한다. 줄바꿈이 하나도 없던 창이면 그 줄도 넘어간다 — 틱마다 상한만큼
			// 다시 읽는 것보다 낫다.
			if last < 0 {
				scanned = size
			}
			return "", false, scanned, nil
		}
	}
}

func workTextIn(buf []byte, dropFirst bool) (string, bool) {
	lines := bytes.Split(buf, []byte{'\n'})
	if dropFirst && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if text, ok := workTextInLine(lines[i]); ok {
			return text, true
		}
	}
	return "", false
}

func workTextInLine(line []byte) (string, bool) {
	// 디코딩 전에 거른다. 기록에는 수 MB 짜리 api-request-blob 줄이 섞여 있다.
	if !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"away_summary"`)) {
		return "", false
	}
	var row transcriptRow
	if json.Unmarshal(line, &row) != nil {
		return "", false
	}
	return workTextOf(row)
}

// workTextOf 는 그 줄이 일을 말하면 글을 돌려준다.
func workTextOf(r transcriptRow) (string, bool) {
	if r.IsSidechain {
		return "", false // 서브에이전트의 프롬프트는 이 세션이 시킨 일의 한 조각이다
	}
	switch r.Type {
	case "system":
		if r.Subtype != "away_summary" {
			return "", false
		}
		var recap string
		if json.Unmarshal(r.Content, &recap) != nil {
			return "", false
		}
		text := cleanWorkText(strings.TrimSuffix(strings.TrimSpace(recap), recapConfigHint))
		return text, text != ""
	case "user":
		// origin 이 human 인 줄만 사람이 친 것이다. isHumanTurn 의 «origin 이 없으면 meta 가 아닌 줄» 은 턴을 나누는
		// 데는 맞지만 여기서는 틀리다: 그런 줄에 [Request interrupted by user](실제 기록 30건, 폭 29칸),
		// SDK 호출의 프롬프트, 로컬 명령이 다 있다. 옛 기록은 작업 줄이 비고 패널이 이벤트 줄을 그린다.
		if r.IsCompactSummary || r.Origin == nil || r.Origin.Kind != "human" {
			return "", false
		}
		text := promptWorkText(strings.TrimSpace(userText(r.Message.Content)))
		return text, text != ""
	}
	return "", false
}

// promptWorkText 는 사람이 친 프롬프트에서 일을 말하는 글을 꺼낸다. 없으면 빈 값이다.
func promptWorkText(raw string) string {
	if strings.HasPrefix(raw, agentInquiryMarker) {
		return ""
	}
	if strings.HasPrefix(raw, "<command-") {
		m := commandArgsRe.FindStringSubmatch(raw)
		if m == nil {
			return "" // 인자 없는 슬래시 명령(/matching-work)은 일을 말하지 않는다
		}
		raw = m[1]
	}
	raw = injectedBlockRe.ReplaceAllString(raw, " ")
	text := cleanWorkText(pastedTagRe.ReplaceAllString(raw, " "))
	// 벗기고도 태그로 시작하면 사람이 쓴 글이 아니라 Claude 가 감싼 것이다(<bash-input> 과 앞으로 생길 무엇).
	// 패널에 태그째 찍느니 앞의 일을 그대로 둔다. «<div> 가 깨져요» 같은 프롬프트를 놓치는 값은 작다.
	if strings.HasPrefix(text, "<") || ansi.StringWidth(text) < minWorkPromptWidth {
		return ""
	}
	return text
}

// cleanWorkText 는 글을 패널의 한 줄로 만든다. 프롬프트는 무엇이든 담을 수 있다 — 이스케이프 열과 제어 문자는
// 화면을 망가뜨리고, 방향 문자(Cf)는 줄의 순서를 뒤집는다.
func cleanWorkText(s string) string {
	s = imageTagRe.ReplaceAllString(ansi.Strip(s), " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	text := strings.Join(strings.Fields(s), " ")
	if runes := []rune(text); len(runes) > maxWorkTextRunes {
		text = strings.TrimSpace(string(runes[:maxWorkTextRunes]))
	}
	return text
}
