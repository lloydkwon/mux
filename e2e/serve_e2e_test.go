//go:build e2e

// `mux serve` 를 격리 tmux 위의 가짜 Claude 로 끝까지 돌린다(docs/design/serve.md). tailscale serve 대신 시험이
// 신원 헤더·Host·Origin 을 붙인다 — 그 헤더를 tailscale 이 정말 그렇게 붙이는지는 폰 실측의 일이다.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	serveHost  = "box.e2e.ts.net:8443"
	serveLogin = "e2e@example.com"
)

type serveClient struct {
	t    *testing.T
	base string
}

// startServe 는 빈 포트에 mux serve 를 띄우고, 듣기 시작할 때까지 기다린다. args 는 serve 에 더 줄 플래그다.
func startServe(t *testing.T, args ...string) *serveClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(muxBin, append([]string{"serve", "--addr", addr, "--allow", serveLogin}, args...)...)
	cmd.Env = append(os.Environ(), "TMUX_PANE=")
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("mux serve 로그:\n%s", log.String())
		}
	})
	c := &serveClient{t: t, base: "http://" + addr}
	eventually(t, "mux serve 가 듣는다", 10*time.Second, func() bool {
		code, _ := c.get("/api/list", true)
		return code == 200
	})
	return c
}

func (c *serveClient) do(method, path, body string, signed bool) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Host = serveHost
	if signed {
		req.Header.Set("Tailscale-User-Login", serveLogin)
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", "https://"+serveHost)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 70 * time.Second}).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (c *serveClient) get(path string, signed bool) (int, []byte) {
	return c.do(http.MethodGet, path, "", signed)
}

// postAudio 는 폰의 녹음 올리기다 — 본문은 녹음 그대로, Content-Type 은 녹음 형식.
func (c *serveClient) postAudio(body, contentType string) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/transcribe", strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Host = serveHost
	req.Header.Set("Tailscale-User-Login", serveLogin)
	req.Header.Set("Origin", "https://"+serveHost)
	req.Header.Set("Content-Type", contentType)
	resp, err := (&http.Client{Timeout: 70 * time.Second}).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

type serveAgent struct {
	Pane  string `json:"pane"`
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type serveCard struct {
	Session string       `json:"session"`
	State   string       `json:"state"`
	Agents  []serveAgent `json:"agents"`
}

func (c *serveClient) card(session string) *serveCard {
	code, data := c.get("/api/list", true)
	if code != 200 {
		return nil
	}
	var list struct{ AI, Other []serveCard }
	if err := json.Unmarshal(data, &list); err != nil {
		c.t.Fatalf("list: %v\n%s", err, data)
	}
	for _, card := range append(list.AI, list.Other...) {
		if card.Session == session {
			return &card
		}
	}
	return nil
}

func TestServeRefusesWithoutTheTailscaleIdentity(t *testing.T) {
	c := startServe(t)
	if code, _ := c.get("/api/list", false); code != http.StatusForbidden {
		t.Errorf("신원 헤더 없는 요청: %d, want 403", code)
	}
	if code, _ := c.get("/", false); code != http.StatusForbidden {
		t.Errorf("신원 헤더 없는 정적 파일: %d, want 403", code)
	}
}

func TestServePromptRoundTrip(t *testing.T) {
	pane := startFake(t, "e2e-serve", "sv-plan")
	c := startServe(t)

	var agent serveAgent
	eventually(t, "목록에 가짜 Claude 의 카드가 있다", 10*time.Second, func() bool {
		card := c.card("e2e-serve")
		if card == nil || len(card.Agents) != 1 {
			return false
		}
		agent = card.Agents[0]
		return true
	})
	if agent.Pane != pane || agent.State != "idle" || agent.Name != "sv-plan" {
		t.Fatalf("카드의 Claude = %+v, pane %s", agent, pane)
	}

	body := fmt.Sprintf(`{"pane":%q,"pid":%d,"text":"hello from phone"}`, agent.Pane, agent.PID)
	code, data := c.do(http.MethodPost, "/api/prompt", body, true)
	var sent struct{ Result, Detail string }
	json.Unmarshal(data, &sent)
	if code != 200 || sent.Result != "sent" {
		t.Fatalf("보내기: %d %s", code, data)
	}
	if fakeLog("sv-plan") != "hello from phone\n" {
		t.Errorf("가짜 Claude 가 받은 것 = %q", fakeLog("sv-plan"))
	}

	// 목록을 그린 뒤 그 pane 에 다른 Claude 가 떴다면(pid 가 다르다) 보내지 않는다.
	stale := fmt.Sprintf(`{"pane":%q,"pid":%d,"text":"stale"}`, agent.Pane, agent.PID+100000)
	if _, data := c.do(http.MethodPost, "/api/prompt", stale, true); !strings.Contains(string(data), `"gone"`) {
		t.Errorf("낡은 pid 로 보낸 결과 = %s", data)
	}

	turnPath := "/api/turn?pane=" + url.QueryEscape(agent.Pane) + fmt.Sprintf("&pid=%d", agent.PID)
	var turn struct {
		Result string
		Human  bool
		Prompt *struct{ Text string }
		Reply  *struct{ Text string }
	}
	eventually(t, "대화 탭이 보낸 말과 회신을 짝지어 읽는다", 20*time.Second, func() bool {
		code, data := c.get(turnPath, true)
		if code != 200 || json.Unmarshal(data, &turn) != nil {
			return false
		}
		return turn.Result == "ok"
	})
	if !turn.Human || turn.Prompt == nil || turn.Prompt.Text != "hello from phone" || turn.Reply == nil || turn.Reply.Text != "echo: hello from phone" {
		t.Errorf("대화 = %+v prompt=%+v reply=%+v", turn, turn.Prompt, turn.Reply)
	}
	if fakeLog("sv-plan") != "hello from phone\n" {
		t.Errorf("낡은 pid 의 보내기가 닿았다: %q", fakeLog("sv-plan"))
	}

	screenPath := "/api/screen?pane=" + url.QueryEscape(agent.Pane) + fmt.Sprintf("&pid=%d", agent.PID)
	var screen struct{ ANSI string }
	if code, data := c.get(screenPath, true); code != 200 || json.Unmarshal(data, &screen) != nil || !strings.Contains(screen.ANSI, "❯") {
		t.Errorf("pane 화면: %d %s", code, data)
	}
	if code, data := c.get("/api/screen?session=e2e-serve", true); code != 200 || json.Unmarshal(data, &screen) != nil || screen.ANSI == "" {
		t.Errorf("세션 화면: %d %s", code, data)
	}
}

type serveDialog struct {
	Result string
	Dialog *struct {
		Key  string
		Kind string
		Tabs []struct {
			Label        string
			Done, Submit bool
		}
		Prompt  []string
		Options []struct {
			N        int
			Label    string
			FreeText bool
			Draft    string
		}
		Current int
	}
}

func (c *serveClient) post(path string, body any) map[string]string {
	c.t.Helper()
	data, _ := json.Marshal(body)
	code, out := c.do(http.MethodPost, path, string(data), true)
	var res map[string]string
	if code != 200 || json.Unmarshal(out, &res) != nil {
		c.t.Fatalf("POST %s %s: %d %s", path, data, code, out)
	}
	return res
}

func (c *serveClient) dialog(a serveAgent) serveDialog {
	c.t.Helper()
	_, out := c.get(fmt.Sprintf("/api/dialog?pane=%s&pid=%d", url.QueryEscape(a.Pane), a.PID), true)
	var d serveDialog
	json.Unmarshal(out, &d)
	return d
}

func (c *serveClient) agentState(session string) string {
	if card := c.card(session); card != nil && len(card.Agents) == 1 {
		return card.Agents[0].State
	}
	return ""
}

func (c *serveClient) reply(a serveAgent) string {
	_, out := c.get(fmt.Sprintf("/api/turn?pane=%s&pid=%d", url.QueryEscape(a.Pane), a.PID), true)
	var turn struct {
		Result string
		Reply  *struct{ Text string }
	}
	if json.Unmarshal(out, &turn) != nil || turn.Result != "ok" || turn.Reply == nil {
		return ""
	}
	return turn.Reply.Text
}

// TestServeAnswersADialog 는 폰에서 Claude 의 질문 대화상자에 답한다(docs/design/serve.md «대화상자에 답하기»).
// 가짜 Claude 의 대화상자는 진짜 2.1.312 에서 잰 모양과 키를 흉내 낸다(e2e/fakeclaude/ask.go).
func TestServeAnswersADialog(t *testing.T) {
	startFake(t, "e2e-dlg", "dlg")
	c := startServe(t)
	var a serveAgent
	eventually(t, "카드에 가짜 Claude 가 있다", 10*time.Second, func() bool {
		card := c.card("e2e-dlg")
		if card == nil || len(card.Agents) != 1 {
			return false
		}
		a = card.Agents[0]
		return true
	})

	ask := func() {
		t.Helper()
		if res := c.post("/api/prompt", map[string]any{"pane": a.Pane, "pid": a.PID, "text": "/fake-ask"}); res["result"] != "sent" {
			t.Fatalf("질문 띄우기: %v", res)
		}
		eventually(t, "Claude 가 질문을 띄우고 기다린다", 10*time.Second, func() bool { return c.agentState("e2e-dlg") == "blocked" })
	}
	// 상태 파일의 waiting 이 화면보다 먼저 쓰일 수 있다 — 폰처럼 다시 읽는다. 끝내 못 읽으면 그때의 화면을 남긴다.
	readDialog := func() serveDialog {
		t.Helper()
		var d serveDialog
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if d = c.dialog(a); d.Result == "ok" {
				return d
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("대화상자를 읽지 못했다: %+v\n화면:\n%s", d, tmq("capture-pane", "-p", "-t", a.Pane))
		return d
	}

	// PC 에서 직접 쓰기 칸으로 가서 1 을 쳐 두었다 — 리뷰에서 재현한 화면 «❯ 3. 1». 이 칸에서는 숫자키가 글자로 들어간다.
	// readDraft 는 칸의 글이 draft 가 될 때까지 다시 읽는다.
	readDraft := func(draft string) serveDialog {
		t.Helper()
		var d serveDialog
		eventually(t, "직접 쓰기 칸에 «"+draft+"»", 5*time.Second, func() bool {
			d = c.dialog(a)
			return d.Result == "ok" && len(d.Dialog.Options) > 2 && d.Dialog.Options[2].Draft == draft
		})
		return d
	}

	ask()
	d := readDialog()
	if d.Result != "ok" || d.Dialog.Kind != "question" || len(d.Dialog.Options) != 4 || d.Dialog.Options[1].Label != "배" ||
		!d.Dialog.Options[2].FreeText || len(d.Dialog.Tabs) != 3 || d.Dialog.Key == "" {
		t.Fatalf("첫 질문 = %+v %+v", d, d.Dialog)
	}
	tmq("send-keys", "-t", a.Pane, "3")
	tmq("send-keys", "-t", a.Pane, "1")
	d = readDraft("1")
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": d.Dialog.Key, "n": 2, "label": "배"}); res["result"] != "answered" {
		t.Fatalf("배 고르기: %v\n화면:\n%s", res, tmq("capture-pane", "-p", "-t", a.Pane))
	}
	first := d.Dialog.Key
	d = readDialog()
	if d.Result != "ok" || strings.Join(d.Dialog.Prompt, "") != "색을 골라 주세요." || !d.Dialog.Tabs[0].Done {
		t.Fatalf("둘째 질문 = %+v", d.Dialog)
	}
	// 폰이 첫 질문을 보던 채로 «2. 배» 를 한 번 더 눌렀다 — 다른 대화상자라 누르지 않는다.
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": first, "n": 2, "label": "배"}); res["result"] != "changed" {
		t.Fatalf("낡은 화면의 답: %v", res)
	}
	// 둘째 질문에도 PC 에서 쓰다 만 글이 있다. 폰은 그 글을 보고 직접 쓰기를 골랐다 — 지우고 새로 쓴다.
	tmq("send-keys", "-t", a.Pane, "3")
	tmq("send-keys", "-t", a.Pane, "-l", "초록")
	d = readDraft("초록")
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": d.Dialog.Key, "n": 3, "label": "초록", "text": "보라"}); res["result"] != "answered" {
		t.Fatalf("직접 쓰기: %v\n화면:\n%s", res, tmq("capture-pane", "-p", "-t", a.Pane))
	}
	d = readDialog()
	if d.Result != "ok" || len(d.Dialog.Options) != 2 || d.Dialog.Options[0].Label != "Submit answers" ||
		!strings.Contains(strings.Join(d.Dialog.Prompt, "\n"), "→ 보라") {
		t.Fatalf("Submit 탭 = %+v", d.Dialog)
	}
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": d.Dialog.Key, "n": 1, "label": "Submit answers"}); res["result"] != "answered" {
		t.Fatalf("제출: %v", res)
	}
	eventually(t, "답이 Claude 에게 닿아 회신이 된다", 15*time.Second, func() bool { return c.reply(a) == "answers: 배 / 보라" })

	// Esc 로 거두면 «답하지 않음» 이다. 폰이 본 대화상자가 아니면 거두지 않는다.
	ask()
	d = readDialog()
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": first, "cancel": true}); res["result"] != "changed" {
		t.Fatalf("다른 대화상자의 Esc: %v", res)
	}
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": d.Dialog.Key, "cancel": true}); res["result"] != "cancelled" {
		t.Fatalf("취소: %v", res)
	}
	eventually(t, "거둔 질문은 답하지 않음으로 끝난다", 15*time.Second, func() bool { return c.reply(a) == "declined" })
	if d := c.dialog(a); d.Result == "ok" {
		t.Errorf("닫힌 뒤에도 대화상자가 있다: %+v", d.Dialog)
	}
}

// TestServeAnswersAMultiSelect 는 여러 개 고르기에 답한다 — 체크를 맞추고, 직접 쓰기 칸에 글을 치고, Submit 줄에서
// Enter 한 뒤, 검토 탭에서 제출한다(tmux.AnswerMulti, 가짜는 e2e/fakeclaude/ask.go).
func TestServeAnswersAMultiSelect(t *testing.T) {
	startFake(t, "e2e-multi", "multi")
	c := startServe(t)
	var a serveAgent
	eventually(t, "카드에 가짜 Claude 가 있다", 10*time.Second, func() bool {
		card := c.card("e2e-multi")
		if card == nil || len(card.Agents) != 1 {
			return false
		}
		a = card.Agents[0]
		return true
	})
	if res := c.post("/api/prompt", map[string]any{"pane": a.Pane, "pid": a.PID, "text": "/fake-ask-multi"}); res["result"] != "sent" {
		t.Fatalf("질문 띄우기: %v", res)
	}
	var raw struct {
		Result string
		Dialog *struct {
			Key     string
			Multi   bool
			Prompt  []string
			Options []struct {
				N        int
				Label    string
				Checkbox bool
				FreeText bool
			}
		}
	}
	readDialog := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			_, out := c.get(fmt.Sprintf("/api/dialog?pane=%s&pid=%d", url.QueryEscape(a.Pane), a.PID), true)
			if json.Unmarshal(out, &raw) == nil && raw.Result == "ok" {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("대화상자를 읽지 못했다: %+v\n화면:\n%s", raw, tmq("capture-pane", "-p", "-t", a.Pane))
	}

	readDialog()
	if !raw.Dialog.Multi || len(raw.Dialog.Options) != 4 || !raw.Dialog.Options[2].FreeText || raw.Dialog.Options[3].Checkbox {
		t.Fatalf("여러 개 고르기 = %+v", raw.Dialog)
	}
	// PC 에서 직접 쓰기 칸으로 가서 1 을 쳐 두었다(«❯ 3. [✔] 1») — 이 칸에서 1 을 누르면 «중단» 이 아니라 글자가 된다.
	tmq("send-keys", "-t", a.Pane, "-N", "2", "Down")
	tmq("send-keys", "-t", a.Pane, "1")
	eventually(t, "직접 쓰기 칸에 «1»", 5*time.Second, func() bool {
		readDialog()
		return raw.Dialog.Options[2].Label == "1"
	})
	options := []map[string]any{
		{"n": 1, "label": "중단", "checked": true},
		{"n": 2, "label": "재시도", "checked": false},
		{"n": 3, "label": "1", "checked": true},
	}
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": raw.Dialog.Key, "multi": true, "options": options, "text": "로그 보강"}); res["result"] != "answered" {
		t.Fatalf("여러 개 고르기 답: %v\n화면:\n%s", res, tmq("capture-pane", "-p", "-t", a.Pane))
	}
	readDialog()
	if raw.Dialog.Multi || len(raw.Dialog.Options) != 2 || raw.Dialog.Options[0].Label != "Submit answers" ||
		!strings.Contains(strings.Join(raw.Dialog.Prompt, "\n"), "→ 중단, 로그 보강") {
		t.Fatalf("검토 탭 = %+v", raw.Dialog)
	}
	if res := c.post("/api/answer", map[string]any{"pane": a.Pane, "pid": a.PID, "key": raw.Dialog.Key, "n": 1, "label": "Submit answers"}); res["result"] != "answered" {
		t.Fatalf("제출: %v", res)
	}
	eventually(t, "고른 것이 Claude 에게 닿는다", 15*time.Second, func() bool { return c.reply(a) == "answers: 중단, 로그 보강" })
}

// 받아쓰기를 가짜 명령으로 끝까지 돌린다(docs/design/serve.md «받아쓰기 — 서버 Whisper»): 플래그 → 녹음 올리기 →
// 계약대로 부른 명령 → 세션 이름과 용어가 든 프롬프트 → 바꿔 쓰기 → 임시 파일 지우기. 진짜 모델은
// scripts/whisper/check.sh 의 일이다.
func TestServeTranscribes(t *testing.T) {
	startFake(t, "e2e-dict", "dict")
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-whisper")
	script := "#!/bin/sh\n" +
		"# 가짜 받아쓰기: 계약(--prompt=<글> <파일>)대로 받았는지 글로 돌려준다\n" +
		"case $1 in --prompt=*) ;; *) exit 2 ;; esac\n" +
		"printf '{\"text\": \"허드 %s %s\"}\\n' \"$(wc -c <\"$2\" | tr -d ' ')\" \"${1#--prompt=}\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dict := filepath.Join(dir, "dictation.txt")
	if err := os.WriteFile(dict, []byte("golangci-lint\n허드 => herdr\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leftovers := func() []string {
		m, _ := filepath.Glob(filepath.Join(os.TempDir(), "mux-dictation-*"))
		return m
	}
	before := len(leftovers())
	logPath := filepath.Join(dir, "dictation-log.jsonl")
	c := startServe(t, "--whisper", fake, "--dictation", dict, "--dictation-log", logPath)

	if code, data := c.get("/api/features", true); code != 200 || !strings.Contains(string(data), `"whisper"`) {
		t.Fatalf("features: %d %s", code, data)
	}
	code, data := c.postAudio("webm-bytes", "audio/webm;codecs=opus")
	var v struct{ Result, Text, Detail string }
	if err := json.Unmarshal(data, &v); err != nil || code != 200 || v.Result != "ok" {
		t.Fatalf("받아쓰기: %d %s", code, data)
	}
	if !strings.HasPrefix(v.Text, "herdr 10 ") || !strings.Contains(v.Text, "e2e-dict") ||
		!strings.HasSuffix(v.Text, ", golangci-lint.") {
		t.Errorf("글 %q — 바꿔 쓴 글, 녹음 크기, 세션 이름이 앞이고 파일 용어가 뒤인 프롬프트여야 한다", v.Text)
	}
	if n := len(leftovers()); n != before {
		t.Errorf("받아쓰기 뒤 임시 녹음 파일이 남았다: %v", leftovers())
	}
	// 받아쓴 글은 기록 파일에 남는다 — Whisper 가 낸 그대로와 바꿔 쓴 뒤의 글을 둘 다.
	if data, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(data), `"heard":"허드 10 `) ||
		!strings.Contains(string(data), `"text":"herdr 10 `) || strings.Count(string(data), "\n") != 1 {
		t.Errorf("받아쓰기 기록: %v\n%s", err, data)
	}
	if code, _ := c.postAudio("x", "multipart/form-data; boundary=x"); code != http.StatusUnsupportedMediaType {
		t.Errorf("multipart 녹음: %d, want 415", code)
	}
}
