//go:build e2e

// fakeclaude 는 e2e 가 `mux agent` 를 시험하려고 격리 tmux 의 pane 에 띄우는 가짜 Claude Code 다.
//
// 진짜 Claude 2.1.312 에서 잰 것만 흉내 낸다(docs/design/agent-api.md):
//   - 입력창: «─» 선, «❯»+U+00A0, «─» 선. 입력 버퍼는 가짜가 직접 들고(raw 모드), 다시
//     그릴 때마다 입력창에 그려 넣는다 — 창 크기가 바뀌어(패널이 붙을 때) 다시 그려도 화면과
//     버퍼가 어긋나지 않는다. tty 에코에 맡기면 다시 그린 화면에서 친 글이 사라졌다.
//   - Enter 는 작업 중에도 받아 입력창을 바로 비우고 대기열에 넣는다. 대기열의 글은 앞 턴이
//     끝난 뒤 꺼낼 때 사람 턴 줄(turnPosition)로 기록된다.
//   - 상태 파일 ~/.claude/sessions/<pid>.json: busy → idle, statusUpdatedAt.
//   - 대화 기록: 사람 턴, 도구를 부르는 응답 그룹, tool_result, «echo: 글» 로 끝나는 응답
//     그룹(end_turn), turn_duration.
//
// 받은 줄은 $HOME/fake-<name>.log 에 그대로 남긴다 — 글자 그대로 도착했는지 보는 곳이다.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	name          = flag.String("name", "fake", "Claude 대화 이름 (상태 파일의 name)")
	replyDelay    = flag.Duration("reply-delay", 1500*time.Millisecond, "턴 하나에 걸리는 시간")
	transcriptLag = flag.Duration("transcript-lag", 0, "idle 을 쓴 뒤 회신 줄을 쓰기까지의 지연")
)

type fake struct {
	mu        sync.Mutex
	home, cwd string
	sid, ref  string
	procStart string
	pid       int
	started   int64
	history   []string
	input     []rune // 입력창의 글
	turn      int
}

func main() {
	flag.Parse()
	// raw 모드를 가장 먼저: 상태 파일을 쓰면 mux 가 곧바로 보낼 수 있는데, 그 전에 도착한 키가
	// cooked 모드로 처리되면 에코가 화면에 섞인다. 에코는 하지 않는다 — 입력창은 draw 가 그린다.
	stty := exec.Command("stty", "raw", "-echo")
	stty.Stdin = os.Stdin
	if err := stty.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude: stty:", err)
		os.Exit(1)
	}
	f, err := newFake()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude:", err)
		os.Exit(1)
	}
	f.writeState("idle")
	f.draw()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			f.draw()
		}
	}()
	queue := make(chan string, 16)
	go f.work(queue)
	f.readKeys(queue)
}

func newFake() (*fake, error) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	f := &fake{home: home, cwd: cwd, pid: os.Getpid(), started: time.Now().UnixMilli()}
	f.sid = fmt.Sprintf("fake-%d-%d", f.pid, time.Now().UnixNano())
	f.procStart = procStart()
	out, err := exec.Command("tmux", "display-message", "-p", "-t", os.Getenv("TMUX_PANE"),
		"#{session_name}:#{window_id}.#{pane_id}").Output()
	if err != nil {
		return nil, fmt.Errorf("tmux 위치를 모른다: %w", err)
	}
	f.ref = strings.TrimSpace(string(out))
	f.history = []string{"● fake claude " + *name}
	return f, nil
}

// readKeys 는 키를 한 글자씩 받아 입력 버퍼를 고친다. Enter 는 작업 중이어도 받아 대기열에
// 넣고 입력창을 바로 비운다(진짜 Claude 의 실측).
func (f *fake) readKeys(queue chan string) {
	r := bufio.NewReader(os.Stdin)
	for {
		c, _, err := r.ReadRune()
		if err != nil || c == 0x03 || c == 0x04 { // 끝, C-c, C-d
			return
		}
		f.mu.Lock()
		var submit string
		switch {
		case c == '\r' || c == '\n':
			submit, f.input = string(f.input), nil
			if submit != "" {
				f.history = append(f.history, "❯ "+submit)
			}
		case c == 0x7f || c == 0x08: // BSpace
			if len(f.input) > 0 {
				f.input = f.input[:len(f.input)-1]
			}
		case c == 0x15: // C-u
			f.input = nil
		case c >= 0x20:
			f.input = append(f.input, c)
		}
		f.mu.Unlock()
		f.draw()
		if submit != "" {
			f.log(submit)
			queue <- submit
		}
	}
}

func (f *fake) work(queue chan string) {
	for line := range queue {
		f.mu.Lock()
		f.turn++
		n := f.turn
		f.mu.Unlock()
		f.writeState("busy")
		f.appendTranscript(map[string]any{
			"type": "user", "timestamp": iso(time.Now()), "sessionId": f.sid, "promptSource": "typed",
			"origin":       map[string]any{"kind": "human"},
			"turnPosition": map[string]any{"promptIndex": n, "turnIndex": n},
			"message":      map[string]any{"role": "user", "content": line},
		})
		f.appendTranscript(map[string]any{
			"type": "assistant", "timestamp": iso(time.Now()), "requestId": fmt.Sprintf("req_%d_a", n),
			"message": map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{
				map[string]any{"type": "thinking", "thinking": "…"},
				map[string]any{"type": "tool_use", "name": "Bash"},
			}},
		})
		f.appendTranscript(map[string]any{
			"type": "user", "timestamp": iso(time.Now()),
			"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": "ok"}}},
		})
		time.Sleep(*replyDelay)
		reply := func() {
			f.appendTranscript(map[string]any{
				"type": "assistant", "timestamp": iso(time.Now()), "requestId": fmt.Sprintf("req_%d_b", n),
				"message": map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{
					map[string]any{"type": "text", "text": "echo: " + line},
				}},
			})
			f.appendTranscript(map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": iso(time.Now())})
		}
		if *transcriptLag > 0 {
			f.writeState("idle")
			time.Sleep(*transcriptLag)
			reply()
		} else {
			reply()
			f.writeState("idle")
		}
		f.mu.Lock()
		f.history = append(f.history, "● echo: "+line)
		f.mu.Unlock()
		f.draw()
	}
}

// draw 는 화면을 다시 그리고 커서를 입력창 줄의 «❯ » 뒤에 둔다 — tty 에코가 거기에 찍힌다.
func (f *fake) draw() {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := f.history
	if len(h) > 15 {
		h = h[len(h)-15:]
	}
	line := strings.Repeat("─", 40)
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for _, l := range h {
		b.WriteString(l + "\r\n")
	}
	b.WriteString(line + "\r\n❯\u00a0" + string(f.input) + "\x1b7\r\n" + line + "\r\n  ⏵⏵ fake mode\x1b8")
	os.Stdout.WriteString(b.String())
}

func (f *fake) writeState(status string) {
	now := time.Now().UnixMilli()
	st := map[string]any{
		"pid": f.pid, "sessionId": f.sid, "cwd": f.cwd, "startedAt": f.started, "procStart": f.procStart,
		"version": "2.1.312-fake", "kind": "interactive", "tmux": f.ref, "name": *name, "nameSource": "user",
		"status": status, "updatedAt": now, "statusUpdatedAt": now,
	}
	dir := filepath.Join(f.home, ".claude", "sessions")
	os.MkdirAll(dir, 0o700)
	data, _ := json.Marshal(st)
	tmp := filepath.Join(dir, fmt.Sprintf(".%d.json.tmp", f.pid))
	if os.WriteFile(tmp, data, 0o600) == nil {
		os.Rename(tmp, filepath.Join(dir, strconv.Itoa(f.pid)+".json"))
	}
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

func (f *fake) appendTranscript(row map[string]any) {
	dir := filepath.Join(f.home, ".claude", "projects", nonAlnum.ReplaceAllString(f.cwd, "-"))
	os.MkdirAll(dir, 0o700)
	fh, err := os.OpenFile(filepath.Join(dir, f.sid+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fh.Close()
	data, _ := json.Marshal(row)
	fh.Write(append(data, '\n'))
}

func (f *fake) log(line string) {
	fh, err := os.OpenFile(filepath.Join(f.home, "fake-"+*name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer fh.Close()
	fh.WriteString(line + "\n")
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// procStart 는 /proc/self/stat 의 22번째 필드다 — mux 의 isAlive 가 대조한다.
func procStart() string {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return ""
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
