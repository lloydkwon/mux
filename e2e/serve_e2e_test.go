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

// startServe 는 빈 포트에 mux serve 를 띄우고, 듣기 시작할 때까지 기다린다.
func startServe(t *testing.T) *serveClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(muxBin, "serve", "--addr", addr, "--allow", serveLogin)
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
