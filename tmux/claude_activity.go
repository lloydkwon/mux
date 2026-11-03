package tmux

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// 마지막 작업 시각 — idle 인 Claude 세션이 실제로 마지막 일을 한 때를 대화 기록에서 읽는다.
//
// 상태 파일의 statusUpdatedAt 은 «지금 상태가 시작된 때»이고 거짓이 아니다. 그런데 프롬프트에서
// 대화상자를 여는 로컬 명령(/theme 등)도 idle → waiting → idle 을 거쳐 그 시각을 새로 쓴다.
// 2026-11-03 에 다른 세션의 Claude 가 테마를 바꾸려고 쉬던 세션마다 /theme 을 보냈고, 전날 끝난
// 세션 다섯이 전부 «방금 끝난» 세션이 되어 목록의 recent 정렬이 그 보낸 순서로 섰다.
//
// 그래서 화면의 경과 시간과 정렬은 이 값을 쓰고, Since 는 상태 파일 값 그대로 둔다. 패널의 공유
// 이벤트 기록이 (세션, 상태, since) 를 이벤트의 열쇠로 쓰는데, 그 전제가 «모든 프로세스가 같은 값을
// 읽는다» 이다 — 기록을 읽은 순간에 따라 값이 달라질 수 있는 이것을 거기 넣으면 이벤트가 겹친다.

const (
	// 마지막 작업 줄은 대개 파일 끝 가까이에 있다(실제 기록 8개에서 끝에서 2~50KB). 첫 꼬리에서 못 찾으면
	// 네 배씩 넓혀 상한까지 읽는다. 기록은 수십 MB 까지 커지므로 처음부터 읽지 않는다.
	activityFirstTail = 64 << 10
	activityMaxTail   = 16 << 20

	// 캐시의 상한. 대화마다 한 칸이고, 며칠 도는 패널에서 대화가 쌓여도 이 정도면 충분하다.
	maxActivityCache = 256
)

type cachedActivity struct {
	size  int64
	mod   time.Time
	at    time.Time
	found bool
}

var (
	activityCache   = map[string]cachedActivity{}
	activityCacheMu sync.Mutex
)

// claudeLastActive 는 idle 세션의 마지막 작업 시각이다. since(상태 파일의 시각)보다 늦지 않다 —
// 기록이 더 늦은 줄을 말하면 상태 파일이 아직 따라오지 못한 새 턴이다. 기록이 없거나 읽지 못하면 0 이고,
// 화면은 그때 since 를 쓴다.
func claudeLastActive(sessionID, cwd string, since time.Time) time.Time {
	if sessionID == "" || since.IsZero() {
		return time.Time{}
	}
	path, err := transcriptPath(sessionID, cwd)
	if err != nil {
		return time.Time{}
	}
	at, ok := lastWorkAt(path)
	if !ok {
		return time.Time{}
	}
	if at.Before(since) {
		return at
	}
	return since
}

// lastWorkAt 은 기록 파일에서 마지막 작업 줄의 시각을 찾는다. 1초마다 불리므로 파일의 크기와
// 수정 시각이 그대로면 앞의 답을 다시 쓴다 — 그때 드는 것은 stat 하나다.
func lastWorkAt(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	activityCacheMu.Lock()
	c, hit := activityCache[path]
	activityCacheMu.Unlock()
	if hit && c.size == info.Size() && c.mod.Equal(info.ModTime()) {
		return c.at, c.found
	}

	at, found, err := scanLastWork(path, info.Size())
	if err != nil {
		// 읽기 실패는 «작업 줄 없음» 이 아니다. 캐시하면 기록이 다시 바뀔 때까지 — 쉬는 세션은 끝내 —
		// 상태 파일의 시각으로 돌아가므로, 다음 스캔에서 다시 읽게 둔다.
		return time.Time{}, false
	}

	activityCacheMu.Lock()
	if len(activityCache) >= maxActivityCache {
		activityCache = map[string]cachedActivity{}
	}
	activityCache[path] = cachedActivity{size: info.Size(), mod: info.ModTime(), at: at, found: found}
	activityCacheMu.Unlock()
	return at, found
}

// scanLastWork 는 파일 끝에서부터 꼬리를 넓혀 가며 마지막 작업 줄을 찾는다. err 는 읽기 실패이고,
// 작업 줄이 없는 것(found false)과 다르다.
func scanLastWork(path string, size int64) (at time.Time, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false, err
	}
	defer f.Close()

	for n := int64(activityFirstTail); ; n *= 4 {
		start := max(size-n, 0)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return time.Time{}, false, err
		}
		// 꼬리가 파일 중간에서 시작하면 첫 조각은 잘린 줄이다.
		if at, ok := lastWorkIn(buf, start > 0); ok {
			return at, true, nil
		}
		if start == 0 || n >= activityMaxTail {
			return time.Time{}, false, nil
		}
	}
}

func lastWorkIn(buf []byte, dropFirst bool) (time.Time, bool) {
	lines := bytes.Split(buf, []byte{'\n'})
	if dropFirst && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		// 디코딩 전에 거른다. 기록에는 수 MB 짜리 api-request-blob 줄이 섞여 있다.
		if !bytes.Contains(line, []byte(`"type":"assistant"`)) && !bytes.Contains(line, []byte(`"type":"user"`)) {
			continue
		}
		var row transcriptRow
		if json.Unmarshal(line, &row) != nil {
			continue // 쓰다 만 마지막 줄 등
		}
		at, err := time.Parse(time.RFC3339Nano, row.Timestamp)
		if err != nil {
			continue
		}
		row.at = at
		if isWorkRow(row) {
			return at, true
		}
	}
	return time.Time{}, false
}

// localCommandTags 는 대화를 움직이지 않는 user 줄의 머리다. 슬래시 명령(/theme, /model …)은
// <command-name> 과 <local-command-…> 줄을, 셸 모드(!)는 <bash-…> 줄을 남긴다. 머리만 본다 —
// 프롬프트 본문에 이 태그를 인용하는 일은 있다.
var localCommandTags = []string{"<command-name>", "<local-command-", "<bash-input>", "<bash-stdout>", "<bash-stderr>"}

// isWorkRow 는 Claude 가 일을 했다는 줄인지 본다: 메인 대화의 assistant 줄, 그리고 로컬 명령·meta 가
// 아닌 user 줄(프롬프트, tool_result, 중단 표시). system 줄(away_summary, turn_duration)은 세지 않는다 —
// turn_duration 은 마지막 assistant 줄 바로 뒤에 오므로 빼도 시각은 같다.
func isWorkRow(r transcriptRow) bool {
	if r.IsSidechain || r.at.IsZero() {
		return false
	}
	switch r.Type {
	case "assistant":
		return true
	case "user":
		if r.IsMeta {
			return false
		}
		text := strings.TrimSpace(userText(r.Message.Content))
		for _, tag := range localCommandTags {
			if strings.HasPrefix(text, tag) {
				return false
			}
		}
		return true
	}
	return false
}
