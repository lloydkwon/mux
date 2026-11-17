package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

const (
	testHost  = "box.tail1234.ts.net:8443"
	testLogin = "me@example.com"
)

// fakeBackend 는 tmux 대신 정해 둔 값을 돌려준다. 받은 보내기를 적어 둔다.
type fakeBackend struct {
	sessions    []tmux.Session
	agents      []tmux.Agent
	promptErr   error
	turn        tmux.LastTurn
	turnErr     error
	screen      string
	sent        []string // "pane|text|force"
	captured    []string
	sessionsErr error
	dialog      tmux.Dialog
	dialogErr   error
	answerErr   error
	answers     []string // "pane|key|n|label|text" 또는 "pane|key|cancel"
}

func (f *fakeBackend) Sessions() ([]tmux.Session, error) { return f.sessions, f.sessionsErr }
func (f *fakeBackend) Agents() ([]tmux.Agent, error)     { return f.agents, nil }
func (f *fakeBackend) Prompt(a tmux.Agent, text string, force bool) error {
	f.sent = append(f.sent, fmt.Sprintf("%s|%s|%v", a.PaneID, text, force))
	return f.promptErr
}
func (f *fakeBackend) LastTurn(tmux.Agent) (tmux.LastTurn, error) { return f.turn, f.turnErr }
func (f *fakeBackend) Dialog(tmux.Agent) (tmux.Dialog, error)     { return f.dialog, f.dialogErr }
func (f *fakeBackend) Answer(a tmux.Agent, key string, n int, label, text string) error {
	f.answers = append(f.answers, fmt.Sprintf("%s|%s|%d|%s|%s", a.PaneID, key, n, label, text))
	return f.answerErr
}
func (f *fakeBackend) AnswerMulti(a tmux.Agent, key string, want []tmux.MultiChoice, text string) error {
	f.answers = append(f.answers, fmt.Sprintf("%s|%s|multi|%v|%s", a.PaneID, key, want, text))
	return f.answerErr
}
func (f *fakeBackend) Cancel(a tmux.Agent, key string) error {
	f.answers = append(f.answers, a.PaneID+"|"+key+"|cancel")
	return f.answerErr
}
func (f *fakeBackend) Capture(target string) (string, error) {
	f.captured = append(f.captured, target)
	return f.screen, nil
}

func newFake() *fakeBackend {
	now := time.Now()
	return &fakeBackend{
		sessions: []tmux.Session{
			{Name: "mux", ActiveCommand: "claude", AITool: "claude", AIState: tmux.AIStateWorking, AISince: now.Add(-2 * time.Minute), AIWork: "인증을 쓰는 중"},
			{Name: "notes", Created: now.Add(-time.Hour)},
		},
		agents: []tmux.Agent{
			{PaneID: "%12", Session: "mux", PID: 4321, Name: "mux-exec", State: tmux.AgentWorking, Since: now.Add(-2 * time.Minute)},
			{PaneID: "%31", Session: "mux", PID: 4400, Name: "mux-plan", State: tmux.AgentIdle},
		},
		screen: "\x1b[32mok\x1b[0m",
	}
}

func testServer(t *testing.T, b backend) http.Handler {
	t.Helper()
	cfg, err := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(cfg, b, log.New(io.Discard, "", 0))
}

// request 는 tailscale serve 를 거쳐 온 요청을 흉내 낸다. edit 로 헤더를 바꾼다.
func request(method, path, body string, edit func(*http.Request)) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = testHost
	r.Header.Set("Tailscale-User-Login", testLogin)
	if method == http.MethodPost {
		r.Header.Set("Origin", "https://"+testHost)
		r.Header.Set("Content-Type", "application/json")
	}
	if edit != nil {
		edit(r)
	}
	return r
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("JSON 이 아니다: %v\n%s", err, w.Body.String())
	}
	return v
}

func TestConfigRefusesWhatWouldOpenItTooWide(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"모든 주소", Config{Addr: "0.0.0.0:7690", Allow: []string{testLogin}}},
		{"LAN 주소", Config{Addr: "192.168.0.10:7690", Allow: []string{testLogin}}},
		{"tailnet 주소", Config{Addr: "100.123.5.114:7690", Allow: []string{testLogin}}},
		{"호스트 없음", Config{Addr: ":7690", Allow: []string{testLogin}}},
		{"허용 목록 없음", Config{Addr: "127.0.0.1:7690"}},
		{"빈 로그인", Config{Addr: "127.0.0.1:7690", Allow: []string{" "}}},
	}
	for _, tt := range tests {
		if _, err := tt.cfg.normalized(); err == nil {
			t.Errorf("%s: %+v 를 받았다", tt.name, tt.cfg)
		}
	}
	for _, addr := range []string{"127.0.0.1:7690", "[::1]:7690", "localhost:7690"} {
		if _, err := (Config{Addr: addr, Allow: []string{testLogin}}).normalized(); err != nil {
			t.Errorf("%s 를 거절했다: %v", addr, err)
		}
	}
}

func TestGuardRefuses(t *testing.T) {
	h := testServer(t, newFake())
	tests := []struct {
		name   string
		method string
		path   string
		edit   func(*http.Request)
		want   int
	}{
		{"신원 헤더 없음 — 127.0.0.1 로 직접 들어왔다", "GET", "/api/list", func(r *http.Request) { r.Header.Del("Tailscale-User-Login") }, 403},
		{"허용 목록 밖", "GET", "/api/list", func(r *http.Request) { r.Header.Set("Tailscale-User-Login", "other@example.com") }, 403},
		{"funnel 요청", "GET", "/api/list", func(r *http.Request) { r.Header.Set("Tailscale-Funnel-Request", "?1") }, 403},
		{"DNS 리바인딩 — Host 가 tailnet 이름이 아니다", "GET", "/api/list", func(r *http.Request) { r.Host = "evil.example:7690" }, 403},
		{"ts.net 처럼 보이는 다른 이름", "GET", "/api/list", func(r *http.Request) { r.Host = "box.ts.net.evil.example" }, 403},
		{"정적 파일도 막는다", "GET", "/", func(r *http.Request) { r.Header.Del("Tailscale-User-Login") }, 403},
		{"POST 의 Origin 이 다르다", "POST", "/api/prompt", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"POST 의 Origin 이 없다", "POST", "/api/prompt", func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"POST 가 JSON 이 아니다(사전 요청 없는 폼)", "POST", "/api/prompt", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
	}
	for _, tt := range tests {
		w := do(h, request(tt.method, tt.path, `{"pane":"%12","pid":4321,"text":"hi"}`, tt.edit))
		if w.Code != tt.want {
			t.Errorf("%s: %d, want %d", tt.name, w.Code, tt.want)
		}
	}
	// 대소문자가 다른 같은 로그인은 받는다 — 이메일의 도메인 쪽은 대소문자를 가리지 않는다.
	w := do(h, request("GET", "/api/list", "", func(r *http.Request) { r.Header.Set("Tailscale-User-Login", "Me@Example.com") }))
	if w.Code != 200 {
		t.Errorf("대소문자만 다른 로그인: %d", w.Code)
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	h := testServer(t, newFake())
	for _, path := range []string{"/", "/app.js", "/api/list"} {
		w := do(h, request("GET", path, "", nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP = %q", path, csp)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff 가 없다", path)
		}
	}
	w := do(h, request("GET", "/api/list", "", nil))
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("API 응답이 캐시된다: %q", w.Header().Get("Cache-Control"))
	}
	// 거절도 헤더를 단다 — 거절 페이지가 다른 사이트의 iframe 에 들어가도 안 된다.
	w = do(h, request("GET", "/", "", func(r *http.Request) { r.Header.Del("Tailscale-User-Login") }))
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("거절 응답에 X-Frame-Options 가 없다")
	}
}

func TestStaticFiles(t *testing.T) {
	h := testServer(t, newFake())
	tests := []struct{ path, ctype, contains string }{
		{"/", "text/html", `src="/app.js"`},
		{"/", "text/html", `src="/record.js"`},
		{"/app.js", "text/javascript", "SpeechRecognition"},
		{"/record.js", "text/javascript", "MediaRecorder"},
		{"/app.css", "text/css", "prefers-color-scheme"},
	}
	for _, tt := range tests {
		w := do(h, request("GET", tt.path, "", nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tt.ctype) || !strings.Contains(w.Body.String(), tt.contains) {
			t.Errorf("%s: %d %q", tt.path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	// 인라인 스크립트가 없어야 CSP 의 script-src 'self' 가 지켜진다.
	w := do(h, request("GET", "/", "", nil))
	if strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "<style>") {
		t.Error("index.html 에 인라인 스크립트나 스타일이 있다")
	}
	if w := do(h, request("GET", "/nope", "", nil)); w.Code != 404 {
		t.Errorf("없는 경로: %d", w.Code)
	}
}

func TestListGroupsAgentsUnderTheirCards(t *testing.T) {
	h := testServer(t, newFake())
	w := do(h, request("GET", "/api/list", "", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got := decode[listView](t, w)
	if len(got.AI) != 1 || len(got.Other) != 1 {
		t.Fatalf("list = %+v", got)
	}
	mux := got.AI[0]
	if mux.Session != "mux" || mux.State != "working" || mux.Glyph != "⏳" || mux.Work != "인증을 쓰는 중" {
		t.Errorf("mux 카드 = %+v", mux.PhoneRow)
	}
	if len(mux.Agents) != 2 || mux.Agents[0].Pane != "%12" || mux.Agents[0].PID != 4321 || mux.Agents[0].State != "working" {
		t.Errorf("mux 의 Claude = %+v", mux.Agents)
	}
	if got.Other[0].Agents == nil {
		t.Error("Claude 없는 카드의 agents 는 null 이 아니라 [] 여야 한다")
	}
}

func TestListReportsAMissingServer(t *testing.T) {
	f := newFake()
	f.sessionsErr = tmux.ErrNoServer
	w := do(testServer(t, f), request("GET", "/api/list", "", nil))
	if w.Code != http.StatusServiceUnavailable || decode[errorView](t, w).Error == "" {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

func TestPromptResults(t *testing.T) {
	busy := fmt.Errorf("%w (%w): %%12 가 working 상태다", tmux.ErrAgentNotReady, tmux.ErrAgentBusy)
	tests := []struct {
		name  string
		err   error
		force bool
		want  string
	}{
		{"보냈다", nil, false, "sent"},
		{"대기열에 넣었다", nil, true, "queued"},
		{"승인 대기", fmt.Errorf("%w: %%12 (Bash)", tmux.ErrAgentBlocked), false, "blocked"},
		{"작업 중", busy, false, "busy"},
		{"입력창에 다른 글", fmt.Errorf("%w: %%12 의 입력창에 다른 글이 있어 친 글을 지웠다", tmux.ErrAgentNotReady), false, "not_ready"},
		{"되돌렸다", fmt.Errorf("%w: %%12", tmux.ErrNotSubmitted), false, "retry"},
		{"모른다", fmt.Errorf("%w: %%12", tmux.ErrPromptUnknown), false, "unknown"},
		{"끝났다", fmt.Errorf("%w: %%12", tmux.ErrAgentGone), false, "gone"},
		{"그 밖", errors.New("tmux 가 죽었다"), false, "error"},
	}
	for _, tt := range tests {
		f := newFake()
		f.promptErr = tt.err
		body := fmt.Sprintf(`{"pane":"%%12","pid":4321,"text":"테스트 돌려 줘","force":%v}`, tt.force)
		w := do(testServer(t, f), request("POST", "/api/prompt", body, nil))
		got := decode[promptView](t, w)
		if w.Code != 200 || got.Result != tt.want {
			t.Errorf("%s: %d %+v, want %s", tt.name, w.Code, got, tt.want)
		}
		if want := fmt.Sprintf("%%12|테스트 돌려 줘|%v", tt.force); len(f.sent) != 1 || f.sent[0] != want {
			t.Errorf("%s: 보낸 것 = %v", tt.name, f.sent)
		}
		if tt.err != nil && got.Detail == "" {
			t.Errorf("%s: 이유가 없다", tt.name)
		}
	}
}

func TestPromptSendsNothingWhenItShouldNot(t *testing.T) {
	tests := []struct {
		name, body, want string
		code             int
	}{
		// 목록을 그린 뒤 그 pane 에 다른 Claude 가 떴다.
		{"pid 가 바뀌었다", `{"pane":"%12","pid":999,"text":"hi"}`, "gone", 200},
		{"Claude 없는 pane", `{"pane":"%77","pid":1,"text":"hi"}`, "gone", 200},
		{"여러 줄", `{"pane":"%12","pid":4321,"text":"a\nb"}`, "invalid", 200},
		{"! 로 시작", `{"pane":"%12","pid":4321,"text":"!rm -rf ~"}`, "invalid", 200},
		{"700자 넘음", `{"pane":"%12","pid":4321,"text":"` + strings.Repeat("가", 701) + `"}`, "invalid", 200},
		{"pane id 가 아니다", `{"pane":"mux","pid":4321,"text":"hi"}`, "", 400},
		{"모르는 필드", `{"pane":"%12","pid":4321,"text":"hi","target":"x"}`, "", 400},
		{"JSON 이 아니다", `pane=%12`, "", 400},
	}
	for _, tt := range tests {
		f := newFake()
		w := do(testServer(t, f), request("POST", "/api/prompt", tt.body, nil))
		if w.Code != tt.code {
			t.Errorf("%s: %d, want %d (%s)", tt.name, w.Code, tt.code, w.Body.String())
		}
		if tt.want != "" {
			if got := decode[promptView](t, w); got.Result != tt.want || got.Detail == "" {
				t.Errorf("%s: %+v, want %s", tt.name, got, tt.want)
			}
		}
		if len(f.sent) != 0 {
			t.Errorf("%s: 보내지 말아야 하는데 %v", tt.name, f.sent)
		}
	}
	// 본문 상한: 아주 큰 본문은 읽지 않고 거절한다.
	f := newFake()
	w := do(testServer(t, f), request("POST", "/api/prompt", `{"pane":"%12","pid":4321,"text":"`+strings.Repeat("a", 20000)+`"}`, nil))
	if w.Code != 400 && w.Code != http.StatusRequestEntityTooLarge || len(f.sent) != 0 {
		t.Errorf("큰 본문: %d %v", w.Code, f.sent)
	}
}

func TestTurnResults(t *testing.T) {
	at := time.UnixMilli(1793589600000)
	tests := []struct {
		name   string
		turn   tmux.LastTurn
		err    error
		want   string
		prompt string
		reply  string
	}{
		{"회신 있음", tmux.LastTurn{Prompt: "검토해 줘", PromptAt: at, Human: true, Reply: "차단 0건", ReplyAt: at.Add(time.Minute)}, nil, "ok", "검토해 줘", "차단 0건"},
		{"작업 중", tmux.LastTurn{Prompt: "구현해 줘", PromptAt: at, Human: true}, fmt.Errorf("%w: %%12", tmux.ErrTurnRunning), "running", "구현해 줘", ""},
		{"승인 대기", tmux.LastTurn{Prompt: "푸시해 줘", Human: true}, fmt.Errorf("%w: %%12", tmux.ErrAgentBlocked), "blocked", "푸시해 줘", ""},
		{"회신 글 없음", tmux.LastTurn{Prompt: "응", Human: true}, fmt.Errorf("%w: %%12", tmux.ErrNoReply), "no_reply", "응", ""},
		{"끝났다", tmux.LastTurn{}, fmt.Errorf("%w: %%12", tmux.ErrAgentGone), "gone", "", ""},
	}
	for _, tt := range tests {
		f := newFake()
		f.turn, f.turnErr = tt.turn, tt.err
		w := do(testServer(t, f), request("GET", "/api/turn?pane=%2512&pid=4321", "", nil))
		got := decode[turnView](t, w)
		if w.Code != 200 || got.Result != tt.want {
			t.Errorf("%s: %d %+v", tt.name, w.Code, got)
			continue
		}
		if (tt.prompt == "") != (got.Prompt == nil) || got.Prompt != nil && got.Prompt.Text != tt.prompt {
			t.Errorf("%s: prompt = %+v", tt.name, got.Prompt)
		}
		if (tt.reply == "") != (got.Reply == nil) || got.Reply != nil && got.Reply.Text != tt.reply {
			t.Errorf("%s: reply = %+v", tt.name, got.Reply)
		}
	}
	f := newFake()
	f.turn = tmux.LastTurn{Prompt: "x", PromptAt: at, Human: true, Reply: "y", ReplyAt: at.Add(time.Minute)}
	got := decode[turnView](t, do(testServer(t, f), request("GET", "/api/turn?pane=%2512&pid=4321", "", nil)))
	if got.Prompt.At != at.UnixMilli() || got.Reply.At != at.Add(time.Minute).UnixMilli() || !got.Human {
		t.Errorf("시각·human = %+v %+v %v", got.Prompt, got.Reply, got.Human)
	}
}

func TestScreenTargets(t *testing.T) {
	f := newFake()
	h := testServer(t, f)
	if got := decode[screenView](t, do(h, request("GET", "/api/screen?pane=%2512&pid=4321", "", nil))); got.ANSI != f.screen {
		t.Errorf("pane 화면 = %q", got.ANSI)
	}
	// Claude 없는 세션은 세션 이름을 콜론까지 붙여 정확히 지목한다(접두사로 다른 세션을 잡지 않게).
	if w := do(h, request("GET", "/api/screen?session=notes", "", nil)); w.Code != 200 {
		t.Errorf("세션 화면: %d", w.Code)
	}
	if want := []string{"%12", "=notes:"}; strings.Join(f.captured, " ") != strings.Join(want, " ") {
		t.Errorf("capture 대상 = %v, want %v", f.captured, want)
	}
	for _, q := range []string{"session=nope", "pane=%2577&pid=1", "pane=%2512&pid=1", ""} {
		if w := do(h, request("GET", "/api/screen?"+q, "", nil)); w.Code == 200 {
			t.Errorf("%q 를 캡처했다", q)
		}
	}
	if len(f.captured) != 2 {
		t.Errorf("거절해야 할 대상을 캡처했다: %v", f.captured)
	}
}

// ── 대화상자에 답하기 ─────────────────────────────────────────────────────

func questionDialog() tmux.Dialog {
	return tmux.Dialog{
		Kind:   tmux.DialogQuestion,
		Tabs:   []tmux.DialogTab{{Label: "방식", Done: true}, {Label: "주기"}, {Label: "Submit", Submit: true}},
		Prompt: []string{"반영 주기를 골라 주세요."},
		Options: []tmux.DialogOption{
			{N: 1, Label: "5분", Detail: "5분마다 반영합니다."},
			{N: 2, Label: "10분", Chosen: true},
			{N: 3, Label: "Type something.", FreeText: true},
			{N: 4, Label: "Chat about this"},
		},
		Current: 1,
		Hint:    "Enter to select · Tab/Arrow keys to navigate · Esc to cancel",
	}
}

func TestDialogView(t *testing.T) {
	f := newFake()
	f.dialog = questionDialog()
	got := decode[dialogResponse](t, do(testServer(t, f), request("GET", "/api/dialog?pane=%2512&pid=4321", "", nil)))
	if got.Result != "ok" || got.Dialog == nil {
		t.Fatalf("%+v", got)
	}
	d := got.Dialog
	if d.Kind != "question" || d.Current != 1 || len(d.Tabs) != 3 || !d.Tabs[0].Done || !d.Tabs[2].Submit {
		t.Errorf("dialog = %+v", d)
	}
	if len(d.Options) != 4 || d.Options[0].Detail == "" || !d.Options[1].Chosen || !d.Options[2].FreeText || d.Options[2].Draft != "" {
		t.Errorf("options = %+v", d.Options)
	}
	if d.Key == "" || d.Key != f.dialog.Key() {
		t.Errorf("열쇠 = %q, want %q", d.Key, f.dialog.Key())
	}
	// 직접 쓰기 칸에 쓰다 만 글이 있으면 폰이 그 글을 보여 준다 — 그 칸으로 답하면 지우고 새로 쓴다.
	f.dialog.Options[2].Label = "PC 에서 쓰던 글"
	got = decode[dialogResponse](t, do(testServer(t, f), request("GET", "/api/dialog?pane=%2512&pid=4321", "", nil)))
	if o := got.Dialog.Options[2]; !o.FreeText || o.Draft != "PC 에서 쓰던 글" || got.Dialog.Key == d.Key {
		t.Errorf("쓰다 만 글 = %+v, 열쇠 %q", o, got.Dialog.Key)
	}

	for _, tt := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: %%12 가 idle 상태다", tmux.ErrNoDialog), "none"},
		{fmt.Errorf("%w: %%12", tmux.ErrAgentGone), "gone"},
		{errors.New("tmux 가 죽었다"), "error"},
	} {
		f := newFake()
		f.dialogErr = tt.err
		got := decode[dialogResponse](t, do(testServer(t, f), request("GET", "/api/dialog?pane=%2512&pid=4321", "", nil)))
		if got.Result != tt.want || got.Dialog != nil {
			t.Errorf("%v: %+v, want %s", tt.err, got, tt.want)
		}
	}
	// pid 가 다르면 읽지도 않는다.
	if got := decode[dialogResponse](t, do(testServer(t, newFake()), request("GET", "/api/dialog?pane=%2512&pid=1", "", nil))); got.Result != "gone" {
		t.Errorf("낡은 pid: %+v", got)
	}
}

func TestAnswerResults(t *testing.T) {
	tests := []struct {
		name, body string
		err        error
		want       string
		sent       string
	}{
		{"고른다", `{"pane":"%12","pid":4321,"key":"k1","n":2,"label":"10분"}`, nil, "answered", "%12|k1|2|10분|"},
		{"직접 쓴다", `{"pane":"%12","pid":4321,"key":"k1","n":3,"label":"Type something.","text":"7분"}`, nil, "answered", "%12|k1|3|Type something.|7분"},
		{"취소한다", `{"pane":"%12","pid":4321,"key":"k1","cancel":true}`, nil, "cancelled", "%12|k1|cancel"},
		{"바뀌었다", `{"pane":"%12","pid":4321,"key":"k1","n":2,"label":"이벤트"}`, fmt.Errorf("%w: …", tmux.ErrDialogChanged), "changed", "%12|k1|2|이벤트|"},
		{"닫혔다", `{"pane":"%12","pid":4321,"key":"k1","n":1,"label":"5분"}`, fmt.Errorf("%w: …", tmux.ErrNoDialog), "none", "%12|k1|1|5분|"},
		{"모른다", `{"pane":"%12","pid":4321,"key":"k1","n":1,"label":"5분"}`, fmt.Errorf("%w: …", tmux.ErrAnswerUnknown), "unknown", "%12|k1|1|5분|"},
		{"복사 모드", `{"pane":"%12","pid":4321,"key":"k1","n":1,"label":"5분"}`, fmt.Errorf("%w: 복사 모드", tmux.ErrAgentNotReady), "not_ready", "%12|k1|1|5분|"},
		{"글이 안 된다", `{"pane":"%12","pid":4321,"key":"k1","n":3,"label":"Type something.","text":"x"}`, fmt.Errorf("%w: 보낼 글이 비었다", tmux.ErrAnswerText), "invalid", "%12|k1|3|Type something.|x"},
	}
	for _, tt := range tests {
		f := newFake()
		f.answerErr = tt.err
		w := do(testServer(t, f), request("POST", "/api/answer", tt.body, nil))
		got := decode[promptView](t, w)
		if w.Code != 200 || got.Result != tt.want {
			t.Errorf("%s: %d %+v, want %s", tt.name, w.Code, got, tt.want)
		}
		if strings.Join(f.answers, ",") != tt.sent {
			t.Errorf("%s: backend 가 받은 것 = %v, want %s", tt.name, f.answers, tt.sent)
		}
	}
}

func TestAnswerSendsNothingWhenItShouldNot(t *testing.T) {
	tests := []struct {
		name, body, want string
		code             int
	}{
		{"pid 가 바뀌었다", `{"pane":"%12","pid":999,"key":"k1","n":1,"label":"5분"}`, "gone", 200},
		{"직접 쓰기 글이 여러 줄", `{"pane":"%12","pid":4321,"key":"k1","n":3,"label":"Type something.","text":"a\nb"}`, "invalid", 200},
		{"번호가 없다", `{"pane":"%12","pid":4321,"key":"k1","label":"5분"}`, "", 400},
		{"글을 안 봤다", `{"pane":"%12","pid":4321,"key":"k1","n":1}`, "", 400},
		{"pane id 가 아니다", `{"pane":"mux","pid":4321,"key":"k1","n":1,"label":"5분"}`, "", 400},
		{"모르는 필드", `{"pane":"%12","pid":4321,"key":"k1","n":1,"label":"5분","keys":"Enter"}`, "", 400},
		// 폰이 본 대화상자의 열쇠가 없으면 그사이 뜬 다른 대화상자(같은 «1. Yes»)를 가리지 못한다.
		{"열쇠가 없다", `{"pane":"%12","pid":4321,"n":1,"label":"Yes"}`, "", 400},
		{"Esc 도 열쇠가 있어야 한다", `{"pane":"%12","pid":4321,"cancel":true}`, "", 400},
		{"여러 개 고르기도", `{"pane":"%12","pid":4321,"multi":true,"options":[{"n":1,"label":"중단","checked":true}]}`, "", 400},
		{"열쇠가 너무 길다", `{"pane":"%12","pid":4321,"key":"` + strings.Repeat("k", 65) + `","n":1,"label":"Yes"}`, "", 400},
	}
	for _, tt := range tests {
		f := newFake()
		w := do(testServer(t, f), request("POST", "/api/answer", tt.body, nil))
		if w.Code != tt.code {
			t.Errorf("%s: %d, want %d (%s)", tt.name, w.Code, tt.code, w.Body.String())
		}
		if tt.want != "" {
			if got := decode[promptView](t, w); got.Result != tt.want {
				t.Errorf("%s: %+v, want %s", tt.name, got, tt.want)
			}
		}
		if len(f.answers) != 0 {
			t.Errorf("%s: 누르지 말아야 하는데 %v", tt.name, f.answers)
		}
	}
	// 답하기도 쓰는 요청이다 — 다른 출처에서는 막힌다.
	f := newFake()
	w := do(testServer(t, f), request("POST", "/api/answer", `{"pane":"%12","pid":4321,"key":"k1","n":1,"label":"5분"}`,
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }))
	if w.Code != 403 || len(f.answers) != 0 {
		t.Errorf("다른 출처의 답하기: %d %v", w.Code, f.answers)
	}
}

func TestAnswerLogNeverCarriesTheText(t *testing.T) {
	var buf strings.Builder
	f := newFake()
	cfg, _ := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}}.normalized()
	h := newHandler(cfg, f, log.New(&buf, "", 0))
	do(h, request("POST", "/api/answer", `{"pane":"%12","pid":4321,"key":"k1","n":3,"label":"Type something.","text":"비밀 계획"}`, nil))
	do(h, request("POST", "/api/prompt", `{"pane":"%12","pid":4321,"text":"비밀 프롬프트"}`, nil))
	if strings.Contains(buf.String(), "비밀") || !strings.Contains(buf.String(), "답하기") {
		t.Errorf("로그:\n%s", buf.String())
	}
}

func TestAnswerMultiSelect(t *testing.T) {
	f := newFake()
	f.dialog = tmux.Dialog{Kind: tmux.DialogQuestion, Multi: true, SubmitLine: true, Options: []tmux.DialogOption{
		{N: 1, Label: "중단", Checkbox: true, Checked: true}, {N: 2, Label: "재시도", Checkbox: true},
		{N: 3, Label: "Type something", Checkbox: true, FreeText: true}, {N: 4, Label: "Chat about this"},
	}}
	got := decode[dialogResponse](t, do(testServer(t, f), request("GET", "/api/dialog?pane=%2512&pid=4321", "", nil)))
	if !got.Dialog.Multi || !got.Dialog.Options[0].Checkbox || !got.Dialog.Options[0].Checked || got.Dialog.Options[3].Checkbox {
		t.Errorf("여러 개 고르기 = %+v", got.Dialog)
	}

	body := `{"pane":"%12","pid":4321,"key":"k1","multi":true,"options":[{"n":1,"label":"중단","checked":true},{"n":2,"label":"재시도","checked":false},{"n":3,"label":"Type something","checked":true}],"text":"로그"}`
	w := do(testServer(t, f), request("POST", "/api/answer", body, nil))
	if r := decode[promptView](t, w); r.Result != "answered" {
		t.Errorf("답 = %+v", r)
	}
	if want := "%12|k1|multi|[{1 중단 true} {2 재시도 false} {3 Type something true}]|로그"; strings.Join(f.answers, ",") != want {
		t.Errorf("backend 가 받은 것 = %v", f.answers)
	}

	for _, bad := range []string{
		`{"pane":"%12","pid":4321,"key":"k1","multi":true,"options":[]}`,
		`{"pane":"%12","pid":4321,"key":"k1","multi":true,"options":[{"n":0,"label":"x","checked":true}]}`,
		`{"pane":"%12","pid":4321,"key":"k1","multi":true,"options":[{"n":1,"label":"","checked":true}]}`,
	} {
		f := newFake()
		if w := do(testServer(t, f), request("POST", "/api/answer", bad, nil)); w.Code != 400 || len(f.answers) != 0 {
			t.Errorf("%s: %d %v", bad, w.Code, f.answers)
		}
	}
}
