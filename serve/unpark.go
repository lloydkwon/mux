package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// 보관함 되살리기(docs/design/serve.md «보관함 되살리기»). TUI 의 `p` 로 보관한 세션을 폰에서 보고 되살린다.
// 보관 코드(tmux/park.go)는 그대로 부르기만 한다 — 같은 대화 id 로 --resume, 살아 있는 대화는 다시 띄우지 않음,
// 못 띄운 대화는 항목에 남김. 다른 점은 attach 가 없다는 것뿐이다.

type parkedConvView struct {
	Task string `json:"task,omitempty"` // Claude 가 붙인 작업 이름
	Dir  string `json:"dir"`            // 대화를 연 폴더, 집은 ~ 로
}

type parkedView struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Note          string           `json:"note,omitempty"`
	ParkedAt      int64            `json:"parkedAt"` // ms — 폰이 «며칠 전» 과 오래된 항목 경고를 계산한다
	Conversations []parkedConvView `json:"conversations"`
}

// parkedViews 는 최근에 보관한 것이 먼저다 — 방금 보관한 세션을 되살리는 일이 가장 흔하다.
func parkedViews(list []tmux.ParkedSession) []parkedView {
	home, _ := os.UserHomeDir()
	out := make([]parkedView, 0, len(list))
	for _, p := range list {
		v := parkedView{ID: p.ID, Name: p.Name, Note: p.Note, ParkedAt: p.ParkedAt.UnixMilli(), Conversations: []parkedConvView{}}
		for _, c := range p.Conversations {
			v.Conversations = append(v.Conversations, parkedConvView{Task: c.Task, Dir: tildePath(c.CWD, home)})
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ParkedAt > out[j].ParkedAt })
	return out
}

func tildePath(p, home string) string {
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return p
}

type unparkRequest struct {
	ID string `json:"id"`
}

// unparkView 의 result: restored(되살림 — 이미 살아 있던 대화면 그 세션) | partial(일부 대화만, 나머지는 보관함에)
// | failed(아무것도 못 띄움, 항목 그대로) | gone(그 id 가 보관함에 없다 — 다른 곳에서 되살렸거나 지웠다).
type unparkView struct {
	Result  string `json:"result"`
	Session string `json:"session,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func (s *server) handleUnpark(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPromptBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req unparkRequest
	if err := dec.Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	s.unparkMu.Lock()
	defer s.unparkMu.Unlock()
	// 폰이 본 목록은 낡았을 수 있다(그사이 TUI 가 되살렸거나 지웠다) — 그 순간의 보관함에서 찾는다.
	list, err := s.b.Parked()
	if err != nil {
		v := unparkView{Result: "failed", Detail: "보관함을 읽지 못했습니다: " + err.Error()}
		s.log.Printf("되살리기 %s: %s", requestLogin(r), v.Detail)
		writeJSON(w, http.StatusOK, v)
		return
	}
	var p tmux.ParkedSession
	found := false
	for _, q := range list {
		if q.ID == req.ID {
			p, found = q, true
			break
		}
	}
	if !found {
		// id 는 폰이 보낸 글이다 — 인용하고 잘라 로그 줄을 꾸며 넣지 못하게 한다.
		s.log.Printf("되살리기 %s → %q: gone", requestLogin(r), truncateRunes(req.ID, 64))
		writeJSON(w, http.StatusOK, unparkView{Result: "gone"})
		return
	}
	// 같은 1초 안에 세션을 둘 만들지 않는다 — session-created 훅의 resurrect 스냅숏은 초 단위 파일 이름에 원자적이지 않게 써서,
	// 한 초에 둘이 생기면 한 파일에 섞이고 그것으로 복원하면 pane 이 두 배가 된다(tmux/claude_sessions.go 의 waitForNextSecond,
	// --claude 입양이 초 경계를 기다리는 이유).
	if now := time.Now(); now.Unix() == s.lastUnpark.Unix() {
		time.Sleep(now.Truncate(time.Second).Add(time.Second).Sub(now))
	}
	name, err := s.b.Unpark(p)
	s.lastUnpark = time.Now()
	v := unparkView{Session: name}
	switch {
	case name == "":
		v.Result = "failed"
	case err != nil:
		v.Result = "partial"
	default:
		v.Result = "restored"
	}
	if err != nil {
		v.Detail = err.Error()
	}
	// 이유(못 띄운 대화의 짧은 id·폴더, tmux 오류)도 남긴다 — 폰의 알림은 닫히면 사라진다. 사용자 글은 없다.
	if v.Detail != "" {
		s.log.Printf("되살리기 %s → %s (대화 %d개): %s: %s", requestLogin(r), p.Name, len(p.Conversations), v.Result, v.Detail)
	} else {
		s.log.Printf("되살리기 %s → %s (대화 %d개): %s", requestLogin(r), p.Name, len(p.Conversations), v.Result)
	}
	writeJSON(w, http.StatusOK, v)
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
