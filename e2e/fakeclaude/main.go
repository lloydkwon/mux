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
//
// `-p` 로 불리면 패널의 제목 호출(tmux/claude_work_title.go)이다. 상태 파일도 입력창도 없이 답만 하고 끝낸다.
// 호출이 도착한 사실은 $HOME/fake-titles.log 에 한 줄씩 남는다. `-p --session-id <id> '/rename …'` 이면 작업의 씨앗
// 대화(tmux/task_start.go)다 — 진짜 2.1.332 에서 잰 대로 이름 줄만 든 기록을 남기고, 같은 id 면 거절한다.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
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
	// 보관함 복원(mux 의 RestoreParked)이 치는 `claude --resume <id>` 를 받는다. 같은 id 로 상태 파일과
	// 기록을 이어 쓰는 것이 진짜 Claude 의 --resume 이다. --fork-session 이면 새 id 를 받는다.
	resume = flag.String("resume", "", "이어 갈 대화 id")
	fork   = flag.Bool("fork-session", false, "--resume 한 대화를 새 id 로 이어 간다")
)

// slowExit 은 .fake-slow-exit 가 있을 때 터미널이 끊긴 뒤 더 사는 시간이다.
const slowExit = 3 * time.Second

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
	ask       *fakeAsk // 떠 있는 질문 대화상자(ask.go). 있으면 키가 그리로 간다
}

func main() {
	// flag.Parse 보다 먼저다. 제목 호출의 인자(--model, --json-schema …)를 이 가짜는 정의하지 않는다.
	if slices.Contains(os.Args[1:], "-p") {
		if id := argAfter("--session-id"); id != "" {
			seedConversation(id, strings.TrimPrefix(os.Args[len(os.Args)-1], "/rename "))
			return
		}
		answerTitle()
		return
	}
	flag.Parse()
	// 폴더에 .fake-slow-exit 가 있으면 터미널이 끊긴 뒤에도 잠깐 더 산다 — 끝낼 때 훅이 도는 진짜 Claude 의 모양이다.
	if _, err := os.Stat(".fake-slow-exit"); err == nil {
		signal.Ignore(syscall.SIGHUP)
		defer time.Sleep(slowExit)
	}
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
	// 폴더에 .fake-dialog 가 있으면 대화상자를 띄운 채 시작한다 — 시작하자마자 사람을 기다리는 Claude 다.
	if _, err := os.Stat(".fake-dialog"); err == nil {
		f.startWithDialog()
	} else {
		f.writeState("idle")
		f.draw()
	}

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

var nowTagRe = regexp.MustCompile(`(?s)<now>(.*)</now>`)

// answerTitle 은 «제목: <지금 하는 일>» 을 진짜 claude -p --output-format json 의 모양으로 답한다.
func answerTitle() {
	input, _ := io.ReadAll(os.Stdin)
	now := ""
	if m := nowTagRe.FindSubmatch(input); m != nil {
		now = string(m[1])
	}
	if home, err := os.UserHomeDir(); err == nil {
		if f, err := os.OpenFile(filepath.Join(home, "fake-titles.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, now)
			f.Close()
		}
	}
	reply, _ := json.Marshal(map[string]any{
		"type": "result", "is_error": false,
		"structured_output": map[string]string{"title": "제목: " + now},
	})
	os.Stdout.Write(reply)
}

func argAfter(flagName string) string {
	if i := slices.Index(os.Args, flagName); i >= 0 && i+1 < len(os.Args) {
		return os.Args[i+1]
	}
	return ""
}

// transcriptFile 은 이 폴더에서 연 대화의 기록 파일이다.
func transcriptFile(home, cwd, sid string) string {
	return filepath.Join(home, ".claude", "projects", nonAlnum.ReplaceAllString(cwd, "-"), sid+".jsonl")
}

// seedConversation 은 모델 없이 이름만 든 대화를 만든다.
func seedConversation(id, title string) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	path := transcriptFile(home, cwd, id)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "Error: Session ID %s is already in use.\n", id)
		os.Exit(1)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	row, _ := json.Marshal(map[string]any{"type": "custom-title", "customTitle": title, "sessionId": id})
	if err := os.WriteFile(path, append(row, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if f, err := os.OpenFile(filepath.Join(home, "fake-seeds.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, id, title)
		f.Close()
	}
	reply, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "num_turns": 0, "result": "Session renamed to: " + title})
	os.Stdout.Write(reply)
}

// resumedTitle 은 기록에 적힌 사람이 준 이름이다.
func resumedTitle(path string) string {
	data, _ := os.ReadFile(path)
	title := ""
	for _, line := range strings.Split(string(data), "\n") {
		var row struct{ Type, CustomTitle string }
		if json.Unmarshal([]byte(line), &row) == nil && row.Type == "custom-title" {
			title = row.CustomTitle
		}
	}
	return title
}

func newFake() (*fake, error) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	f := &fake{home: home, cwd: cwd, pid: os.Getpid(), started: time.Now().UnixMilli()}
	f.sid = fmt.Sprintf("fake-%d-%d", f.pid, time.Now().UnixNano())
	if *resume != "" && !*fork {
		f.sid = *resume
		if title := resumedTitle(transcriptFile(home, cwd, *resume)); title != "" {
			*name = title
		}
	}
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
		if c == 0x1b {
			// 이스케이프 열(화살표 ESC [ A/B, ESC O A/B)이면 대화상자의 커서를 옮기고 나머지는 버린다. 홀로 온 Esc 만
			// 대화상자를 거둔다(tmux 의 send-keys Escape).
			if r.Buffered() > 0 {
				if next, _, _ := r.ReadRune(); next == '[' || next == 'O' {
					x, _, err := r.ReadRune()
					for ; err == nil && !(x >= 0x40 && x <= 0x7e); x, _, err = r.ReadRune() {
					}
					f.mu.Lock()
					if f.ask != nil && (x == 'A' || x == 'B') {
						d := 1
						if x == 'A' {
							d = -1
						}
						f.ask.arrow(d)
					}
					f.mu.Unlock()
					f.draw()
				}
				continue
			}
			f.mu.Lock()
			if f.ask != nil {
				f.ask.cancel()
			}
			f.mu.Unlock()
			continue
		}
		f.mu.Lock()
		if f.ask != nil {
			f.ask.key(c)
			f.mu.Unlock()
			f.draw()
			continue
		}
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
		if line == "/fake-ask" || line == "/fake-ask-multi" {
			f.finishTurn(n, f.waitAsk(line == "/fake-ask-multi"))
			continue
		}
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

// waitAsk 는 질문 대화상자를 띄우고 답(또는 취소)을 기다려 회신 글을 돌려준다.
func (f *fake) waitAsk(multi bool) string {
	a := newFakeAsk(multi)
	f.mu.Lock()
	f.ask = a
	f.mu.Unlock()
	f.writeStateWaiting("input needed")
	f.draw()
	answers := <-a.done
	f.mu.Lock()
	f.ask = nil
	f.mu.Unlock()
	f.writeState("busy")
	if answers == nil {
		return "declined"
	}
	return "answers: " + strings.Join(answers, " / ")
}

// startWithDialog 는 질문 대화상자를 띄우고, 답(또는 취소)이 오면 입력창으로 돌아간다. 턴은 만들지 않는다.
func (f *fake) startWithDialog() {
	a := newFakeAsk(false)
	f.ask = a
	f.writeStateWaiting("input needed")
	f.draw()
	go func() {
		<-a.done
		f.mu.Lock()
		f.ask = nil
		f.mu.Unlock()
		f.writeState("idle")
		f.draw()
	}()
}

// finishTurn 은 회신 글로 턴을 끝낸다(응답 그룹, turn_duration, idle).
func (f *fake) finishTurn(n int, text string) {
	f.appendTranscript(map[string]any{
		"type": "assistant", "timestamp": iso(time.Now()), "requestId": fmt.Sprintf("req_%d_b", n),
		"message": map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{
			map[string]any{"type": "text", "text": text},
		}},
	})
	f.appendTranscript(map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": iso(time.Now())})
	f.writeState("idle")
	f.mu.Lock()
	f.history = append(f.history, "● "+text)
	f.mu.Unlock()
	f.draw()
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
	if f.ask != nil {
		b.WriteString(strings.Join(f.ask.lines(), "\r\n"))
		os.Stdout.WriteString(b.String())
		return
	}
	b.WriteString(line + "\r\n❯\u00a0" + string(f.input) + "\x1b7\r\n" + line + "\r\n  ⏵⏵ fake mode\x1b8")
	os.Stdout.WriteString(b.String())
}

func (f *fake) writeState(status string) { f.writeStatus(status, "") }

// writeStateWaiting 은 대화상자에서 사람을 기다린다고 쓴다(진짜는 질문이 "input needed", 권한이 "permission prompt").
func (f *fake) writeStateWaiting(waitingFor string) { f.writeStatus("waiting", waitingFor) }

func (f *fake) writeStatus(status, waitingFor string) {
	now := time.Now().UnixMilli()
	st := map[string]any{
		"pid": f.pid, "sessionId": f.sid, "cwd": f.cwd, "startedAt": f.started, "procStart": f.procStart,
		"version": "2.1.312-fake", "kind": "interactive", "tmux": f.ref, "name": *name, "nameSource": "user",
		"status": status, "updatedAt": now, "statusUpdatedAt": now,
	}
	if waitingFor != "" {
		st["waitingFor"] = waitingFor
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
	path := transcriptFile(f.home, f.cwd, f.sid)
	os.MkdirAll(filepath.Dir(path), 0o700)
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
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
