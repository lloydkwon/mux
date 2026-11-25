package tmux

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 작업의 대화 — worktree 폴더의 기록에서 이어 갈 대화를 고르고, 첫 프롬프트를 보냈는지 본다. 설계는 docs/design/task.md.

// taskConversation 은 worktree 폴더의 대화 하나다.
type taskConversation struct {
	id       string
	title    string // 사람이 준 이름(custom-title)
	prompted bool   // 사람 턴이 있다 — 프롬프트를 보냈다
	here     bool   // 기록의 cwd 가 그 worktree 다
	review   bool   // 이름이 한 번이라도 r-<id> 였다 — 리뷰어의 대화다
	modified time.Time
}

// latestTaskConversation 은 그 worktree 에서 이어 갈 대화다 — 가장 최근 것.
//
// 세 가지를 거른다. worktree 의 .git 파일보다 오래된 기록은 같은 id 로 했던 지난 작업의 것이다. 이름도 사람 턴도
// 없는 기록은 worktree 안에서 돈 `claude -p` 같은 것이라, 이으면 엉뚱한 대화가 열린다. 대화 id 의 모양이 아닌 파일
// 이름은 셸 명령에 들어가므로 버린다.
//
// prompted 는 이 worktree 의 **모든** 대화를 본다. /clear 뒤의 새 대화에는 사람 턴이 없어서, 가장 최근 것만 보면 이미
// 보낸 프롬프트를 다시 보낸다.
//
// 읽지 못한 것은 오류다. «없음» 으로 읽으면 이미 보낸 프롬프트를 다시 보낸다.
func latestTaskConversation(t Task) (taskConversation, bool, error) {
	born, err := os.Stat(filepath.Join(t.Worktree, ".git"))
	if err != nil {
		return taskConversation{}, false, fmt.Errorf("worktree 를 읽지 못했다: %w", err)
	}
	convs, err := taskConversations(t, born.ModTime())
	if err != nil {
		return taskConversation{}, false, err
	}
	var best taskConversation
	prompted := false
	for _, conv := range convs {
		prompted = prompted || conv.prompted
		if conv.modified.After(best.modified) {
			best = conv
		}
	}
	best.prompted = prompted
	return best, best.id != "", nil
}

// maxProjectDirName 은 Claude 가 기록 폴더 이름을 자르는 길이다. 넘으면 앞 200자에 «-해시» 를 붙인다(2.1.332).
const maxProjectDirName = 200

// taskConversations 는 worktree 에서 연 워커의 대화들 중 since 뒤의 것이다. 같은 폴더에서 돈 리뷰어의 대화는 뺀다 —
// 넣으면 워커 대신 리뷰어를 이어 열고, 리뷰어가 받은 글을 «첫 프롬프트를 보냈다» 로 센다.
func taskConversations(t Task, since time.Time) ([]taskConversation, error) {
	home, err := homeDir()
	if err != nil {
		return nil, fmt.Errorf("홈 폴더를 알 수 없다: %w", err)
	}
	dir := projectDirName(t.Worktree)
	// 잘린 이름은 이웃 작업의 폴더와 같을 수 있다 — 그때만 기록의 cwd 로 가른다.
	truncated := len(dir) > maxProjectDirName
	if truncated {
		dir = dir[:maxProjectDirName] + "-*"
	}
	paths, _ := filepath.Glob(filepath.Join(home, claudeDir, "projects", dir, "*.jsonl"))
	var convs []taskConversation
	for _, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // 그 사이 지워졌다
		}
		if err != nil {
			return nil, fmt.Errorf("대화 기록을 읽지 못했다: %w", err)
		}
		if info.ModTime().Before(since) {
			continue
		}
		conv, ok, err := readTaskConversation(path, t)
		if err != nil {
			return nil, fmt.Errorf("대화 기록을 읽지 못했다: %w", err)
		}
		if !ok || conv.review || !conversationIDRe.MatchString(conv.id) || (truncated && !conv.here) {
			continue
		}
		conv.modified = info.ModTime()
		convs = append(convs, conv)
	}
	return convs, nil
}

// projectDirName 은 Claude 가 폴더 경로로 짓는 기록 폴더 이름이다(자르기 전). encodePath 와 다른 것은 이모지처럼
// UTF-16 에서 두 칸인 글자다 — Claude 는 칸마다 «-» 를 쓴다. 못 찾으면 프롬프트를 다시 보내게 되므로 여기서는 맞춘다.
func projectDirName(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xffff:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// conversationIDRe 는 셸 명령에 넣어도 되는 대화 id 다(UUID).
var conversationIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// taskConversationNeedles 는 디코딩할 줄의 표식이다. 기록에는 수 MB 짜리 줄이 섞여 있다.
var taskConversationNeedles = [][]byte{[]byte(`"type":"custom-title"`), []byte(`"kind":"human"`)}

// readTaskConversation 은 기록 파일에서 이름과 사람 턴을 읽는다. 둘 다 없으면 이을 대화가 아니다.
func readTaskConversation(path string, t Task) (taskConversation, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return taskConversation{}, false, err
	}
	defer f.Close()
	conv := taskConversation{id: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	cwd, reviewer := cwdNeedle(t.Worktree), ReviewSessionName(t.ID)
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		conv.here = conv.here || bytes.Contains(line, cwd)
		conv.readLine(line, reviewer)
		if err == io.EOF {
			return conv, conv.prompted || conv.title != "", nil
		}
		if err != nil {
			return taskConversation{}, false, err
		}
	}
}

// cwdNeedle 은 기록 줄에 적힌 그 폴더의 모양이다. Claude 는 <, >, & 를 이스케이프하지 않는다.
func cwdNeedle(dir string) []byte {
	var b bytes.Buffer
	b.WriteString(`"cwd":`)
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(dir) // 문자열은 실패하지 않는다
	return bytes.TrimRight(b.Bytes(), "\n")
}

func (c *taskConversation) readLine(line []byte, reviewer string) {
	if !bytes.Contains(line, taskConversationNeedles[0]) && !bytes.Contains(line, taskConversationNeedles[1]) {
		return
	}
	var row struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		Entrypoint  string `json:"entrypoint"`
		Origin      *struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	}
	if json.Unmarshal(line, &row) != nil {
		return
	}
	switch {
	case row.Type == "custom-title":
		c.title = cleanWorkText(row.CustomTitle)
		c.review = c.review || c.title == reviewer
	case row.Type == "user" && row.Origin != nil && row.Origin.Kind == "human" && !strings.HasPrefix(row.Entrypoint, "sdk"):
		c.prompted = true
	}
}
