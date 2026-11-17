package serve

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

func parkedFixture() []tmux.ParkedSession {
	home, _ := os.UserHomeDir()
	return []tmux.ParkedSession{
		{ID: "old-1", Name: "php85", ParkedAt: time.Date(2026, 11, 9, 10, 31, 0, 0, time.UTC),
			Conversations: []tmux.ParkedConversation{{SessionID: "40bcae92", CWD: "/srv/php85"}}},
		{ID: "new-1", Name: "sekihe-2", Note: "결제 고치던 중", ParkedAt: time.Date(2026, 11, 17, 6, 28, 0, 0, time.UTC),
			Conversations: []tmux.ParkedConversation{
				{SessionID: "8ad890b4", CWD: filepath.Join(home, "dev", "boseshop"), Task: "boseshop-a7"},
				{SessionID: "9be00001", CWD: filepath.Join(home, "dev", "boseshop")},
			}},
	}
}

func unparkPost(body string) *http.Request {
	return request(http.MethodPost, "/api/unpark", body, nil)
}

// 목록에 보관함이 따라온다 — 최근에 보관한 것이 먼저, 폴더는 집을 ~ 로, 대화마다 작업 이름.
func TestListCarriesTheParkedSessions(t *testing.T) {
	b := newFake()
	b.parked = parkedFixture()
	got := decode[listView](t, do(testServer(t, b), request("GET", "/api/list", "", nil)))
	if len(got.Parked) != 2 || got.Parked[0].ID != "new-1" || got.Parked[1].ID != "old-1" {
		t.Fatalf("parked = %+v — 최근에 보관한 것이 먼저", got.Parked)
	}
	p := got.Parked[0]
	if p.Name != "sekihe-2" || p.Note != "결제 고치던 중" || p.ParkedAt != time.Date(2026, 11, 17, 6, 28, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("항목: %+v", p)
	}
	if len(p.Conversations) != 2 || p.Conversations[0].Dir != "~/dev/boseshop" || p.Conversations[0].Task != "boseshop-a7" {
		t.Errorf("대화 요약: %+v", p.Conversations)
	}
	if got.ParkedError != "" {
		t.Errorf("parkedError = %q", got.ParkedError)
	}
}

// 깨진 보관함은 목록을 막지 않는다 — 카드는 그대로, 보관함 자리에 이유. 빈 목록으로 보이게 하지도 않는다(park.md).
func TestListSurvivesABrokenParkedFile(t *testing.T) {
	b := newFake()
	b.parkedErr = errors.New("parked.json: invalid character")
	w := do(testServer(t, b), request("GET", "/api/list", "", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("깨진 보관함이 목록을 막았다: %d", w.Code)
	}
	got := decode[listView](t, w)
	if len(got.AI) == 0 || !strings.Contains(got.ParkedError, "invalid character") || len(got.Parked) != 0 {
		t.Errorf("목록 %d장, parkedError %q, parked %d", len(got.AI), got.ParkedError, len(got.Parked))
	}
}

func TestUnparkResults(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*fakeBackend)
		id     string
		want   unparkView
		called bool
	}{
		{"되살림", nil, "new-1", unparkView{Result: "restored", Session: "sekihe-2"}, true},
		{"이미 살아 있는 대화 — 그 세션으로", func(b *fakeBackend) { b.unparkName = "sekihe-2-live" }, "new-1",
			unparkView{Result: "restored", Session: "sekihe-2-live"}, true},
		{"일부만", func(b *fakeBackend) { b.unparkErr = errors.New("restore 9be00001: its transcript is gone") }, "new-1",
			unparkView{Result: "partial", Session: "sekihe-2", Detail: "restore 9be00001: its transcript is gone"}, true},
		{"아무것도 못 띄움", func(b *fakeBackend) {
			b.unparkNone = true
			b.unparkErr = errors.New("restore 40bcae92: directory /srv/php85 is gone")
		}, "old-1", unparkView{Result: "failed", Detail: "restore 40bcae92: directory /srv/php85 is gone"}, true},
		{"보관함에 없는 id", nil, "nope", unparkView{Result: "gone"}, false},
		{"보관함을 못 읽음", func(b *fakeBackend) { b.parkedErr = errors.New("parked.json: invalid character") }, "new-1",
			unparkView{Result: "failed", Detail: "보관함을 읽지 못했습니다: parked.json: invalid character"}, false},
	}
	for _, c := range cases {
		b := newFake()
		b.parked = parkedFixture()
		if c.edit != nil {
			c.edit(b)
		}
		w := do(testServer(t, b), unparkPost(`{"id":"`+c.id+`"}`))
		if w.Code != http.StatusOK {
			t.Errorf("%s: %d — 결과는 늘 200 에 갈래로", c.name, w.Code)
			continue
		}
		if got := decode[unparkView](t, w); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if called := len(b.unparked) > 0; called != c.called {
			t.Errorf("%s: 되살리기를 불렀나 %v, want %v", c.name, called, c.called)
		}
	}
}

func TestUnparkRefusesBadRequests(t *testing.T) {
	b := newFake()
	b.parked = parkedFixture()
	h := testServer(t, b)
	for _, body := range []string{"", "{}", `{"id":""}`, `{"id":"new-1","extra":1}`, "not json"} {
		if w := do(h, unparkPost(body)); w.Code != http.StatusBadRequest {
			t.Errorf("본문 %q: %d, want 400", body, w.Code)
		}
	}
	// 다른 POST 와 같은 문 — 다른 출처, JSON 아닌 형식은 들어오지 못한다.
	if w := do(h, request(http.MethodPost, "/api/unpark", `{"id":"new-1"}`, func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
	})); w.Code != http.StatusForbidden {
		t.Errorf("다른 Origin: %d", w.Code)
	}
	if w := do(h, request(http.MethodPost, "/api/unpark", `{"id":"new-1"}`, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
	})); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", w.Code)
	}
	if len(b.unparked) != 0 {
		t.Errorf("거절한 요청으로 되살렸다: %v", b.unparked)
	}
}

// 두 손가락이 같은 항목을 겹쳐 눌러도 되살리기는 한 번에 하나이고, 뒤의 것은 그 순간의 보관함을 다시 읽어 «없음» 이다 —
// 같은 대화를 두 번 띄우지 않는다.
func TestUnparkOneAtATimeAndRereadsTheList(t *testing.T) {
	b := newFake()
	b.parked = parkedFixture()
	b.unparkIn = make(chan struct{}, 2)
	b.unparkBlock = make(chan struct{})
	h := testServer(t, b)
	first := make(chan unparkView)
	go func() { first <- decode[unparkView](t, do(h, unparkPost(`{"id":"new-1"}`))) }()
	<-b.unparkIn
	second := make(chan unparkView)
	go func() { second <- decode[unparkView](t, do(h, unparkPost(`{"id":"new-1"}`))) }()
	select {
	case <-b.unparkIn:
		t.Fatal("앞의 되살리기가 끝나기 전에 두 번째가 들어왔다")
	case <-time.After(200 * time.Millisecond):
	}
	close(b.unparkBlock)
	if got := <-first; got.Result != "restored" {
		t.Errorf("먼저 온 요청: %+v", got)
	}
	if got := <-second; got.Result != "gone" {
		t.Errorf("겹친 요청: %+v, want gone", got)
	}
	if len(b.unparked) != 1 {
		t.Errorf("되살리기를 %d번 불렀다, want 1", len(b.unparked))
	}
}

// 낡은 id 는 인용·잘라 로그에 남긴다 — 폰이 보낸 글로 로그 줄을 꾸며 넣지 못하게. 못 띄운 이유는 로그에도 남긴다.
func TestUnparkLogKeepsReasonsAndQuotesTheID(t *testing.T) {
	var logs bytes.Buffer
	b := newFake()
	b.parked = parkedFixture()
	b.unparkErr = errors.New("restore 9be00001: its transcript is gone")
	cfg, _ := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}}.normalized()
	h := newHandlerWith(cfg, b, nil, log.New(&logs, "", 0))
	do(h, unparkPost(`{"id":"new-1"}`))
	do(h, unparkPost(`{"id":"x\nmux serve: 가짜 줄`+strings.Repeat("y", 200)+`"}`))
	out := logs.String()
	if !strings.Contains(out, "partial: restore 9be00001: its transcript is gone") {
		t.Errorf("일부만의 이유가 로그에 없다: %q", out)
	}
	if strings.Count(out, "\n") != 2 || strings.Contains(out, "\nmux serve: 가짜 줄") || strings.Contains(out, strings.Repeat("y", 100)) {
		t.Errorf("요청 id 가 로그 줄을 꾸몄거나 잘리지 않았다: %q", out)
	}
}

func TestUnparkLogsWhoAndWhat(t *testing.T) {
	var logs bytes.Buffer
	b := newFake()
	b.parked = parkedFixture()
	cfg, _ := Config{Addr: "127.0.0.1:7690", Allow: []string{testLogin}}.normalized()
	h := newHandlerWith(cfg, b, nil, log.New(io.MultiWriter(&logs), "", 0))
	do(h, unparkPost(`{"id":"new-1"}`))
	if s := logs.String(); !strings.Contains(s, "되살리기") || !strings.Contains(s, testLogin) || !strings.Contains(s, "sekihe-2") ||
		!strings.Contains(s, "대화 2개") || !strings.Contains(s, "restored") {
		t.Errorf("로그: %q", s)
	}
}

// 연달아 되살려도 세션 생성은 같은 1초에 겹치지 않는다(resurrect 스냅숏이 섞이지 않게 — unpark.go 의 주석).
func TestUnparksAreASecondApart(t *testing.T) {
	b := newFake()
	b.parked = parkedFixture()
	h := testServer(t, b)
	do(h, unparkPost(`{"id":"new-1"}`))
	do(h, unparkPost(`{"id":"old-1"}`))
	if len(b.unparkAt) != 2 {
		t.Fatalf("되살리기 %d번", len(b.unparkAt))
	}
	if b.unparkAt[0].Unix() == b.unparkAt[1].Unix() {
		t.Errorf("두 되살리기가 같은 초에 세션을 만들었다: %v, %v", b.unparkAt[0], b.unparkAt[1])
	}
}
