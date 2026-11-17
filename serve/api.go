package serve

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/lloydkwon/mux/tmux"
	"github.com/lloydkwon/mux/ui"
)

//go:embed static
var staticFiles embed.FS

// maxPromptBody 는 보내기 요청 본문의 상한이다. 글은 700자(한글 3바이트, JSON 이스케이프면 6바이트)이고
// 나머지는 pane·pid 뿐이라 8KB 면 넉넉하다.
const maxPromptBody = 8 << 10

var paneIDPattern = regexp.MustCompile(`^%\d+$`)

// errBadTarget 은 pane·pid 가 모양부터 틀렸다는 뜻이다 — 화면이 보낼 일이 없는 요청이라 400 이다.
var errBadTarget = errors.New("대상은 pane id(%12)와 pid 로 지목한다")

// serveStatic 은 빌드에 묻힌 정적 파일 하나를 낸다. 바이너리를 바꿔 띄우면 바로 새 화면이 나오게 no-cache.
func serveStatic(name, contentType string) http.HandlerFunc {
	data, err := fs.ReadFile(staticFiles, "static/"+name)
	if err != nil {
		panic(fmt.Sprintf("serve: static/%s 가 빌드에 없다: %v", name, err))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	}
}

type errorView struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorView{Error: msg})
}

// ── 목록 ─────────────────────────────────────────────────────────────

type agentView struct {
	Pane       string `json:"pane"`
	PID        int    `json:"pid"`
	Name       string `json:"name"`
	State      string `json:"state"` // tmux.AgentState: working | blocked | idle | shell | unknown
	WaitingFor string `json:"waitingFor,omitempty"`
	// Since 는 지금 상태가 시작된 때(ms)다. 화면은 (state, since) 가 바뀌면 대화를 다시 읽는다.
	Since int64 `json:"since,omitempty"`
}

type cardView struct {
	ui.PhoneRow
	Agents []agentView `json:"agents"`
}

type listView struct {
	AI    []cardView `json:"ai"`
	Other []cardView `json:"other"`
}

func agentViewOf(a tmux.Agent) agentView {
	v := agentView{Pane: a.PaneID, PID: a.PID, Name: a.Name, State: string(a.State), WaitingFor: a.WaitingFor}
	if !a.Since.IsZero() {
		v.Since = a.Since.UnixMilli()
	}
	return v
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	sessions, err := s.b.Sessions()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tmux 세션을 읽지 못했습니다: "+err.Error())
		return
	}
	agents, err := s.b.Agents()
	if err != nil {
		// 목록은 Claude 없이도 쓸모가 있다 — 보내기 버튼만 사라진다.
		s.log.Printf("Claude 목록을 읽지 못했다: %v", err)
		agents = nil
	}
	bySession := map[string][]agentView{}
	for _, a := range agents {
		bySession[a.Session] = append(bySession[a.Session], agentViewOf(a))
	}
	ai, other := ui.PhoneRows(sessions)
	writeJSON(w, http.StatusOK, listView{AI: cardsOf(ai, bySession), Other: cardsOf(other, bySession)})
}

func cardsOf(rows []ui.PhoneRow, bySession map[string][]agentView) []cardView {
	cards := make([]cardView, 0, len(rows))
	for _, row := range rows {
		agents := bySession[row.Session]
		if agents == nil {
			agents = []agentView{}
		}
		cards = append(cards, cardView{PhoneRow: row, Agents: agents})
	}
	return cards
}

// ── 대상 지목 ───────────────────────────────────────────────────────────

// resolve 는 pane id 와 pid 로 지금의 Claude 를 찾는다. pid 가 다르면 목록을 그린 뒤 그 pane 에 다른 Claude 가
// 뜬 것이다 — 보내지 않는다. 대화 id 가 아니라 pid 인 이유: /clear 는 대화 id 를 바꾸지만 같은 Claude 다.
func (s *server) resolve(pane string, pid int) (tmux.Agent, error) {
	if !paneIDPattern.MatchString(pane) || pid <= 0 {
		return tmux.Agent{}, errBadTarget
	}
	agents, err := s.b.Agents()
	if err != nil {
		return tmux.Agent{}, err
	}
	a, err := tmux.ResolveAgent(agents, pane)
	if err != nil {
		return tmux.Agent{}, err
	}
	if a.PID != pid {
		return tmux.Agent{}, fmt.Errorf("%w: %s 의 Claude 가 바뀌었다 (pid %d → %d)", tmux.ErrNoAgent, pane, pid, a.PID)
	}
	return a, nil
}

func queryPID(r *http.Request) int {
	pid, err := strconv.Atoi(r.URL.Query().Get("pid"))
	if err != nil {
		return 0
	}
	return pid
}

// ── 보내기 ─────────────────────────────────────────────────────────────

type promptRequest struct {
	Pane  string `json:"pane"`
	PID   int    `json:"pid"`
	Text  string `json:"text"`
	Force bool   `json:"force"`
}

type promptView struct {
	// Result: sent | queued | blocked | busy | not_ready | retry | unknown | not_seen | gone | invalid | error.
	// 화면 문구는 클라이언트가 정한다(app.js 의 RESULTS) — 서버는 갈래와 이유만 낸다.
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

func (s *server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPromptBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req promptRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	if err := tmux.ValidatePromptText(req.Text); err != nil {
		writeJSON(w, http.StatusOK, promptView{Result: "invalid", Detail: err.Error()})
		return
	}
	a, err := s.resolve(req.Pane, req.PID)
	if errors.Is(err, errBadTarget) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err == nil {
		err = s.b.Prompt(a, req.Text, req.Force)
	}
	result := promptResult(err, req.Force)
	// 글은 적지 않는다 — 프롬프트는 붙여 넣은 무엇이든 담는다(이벤트 기록이 프롬프트를 담지 않는 것과 같다).
	s.log.Printf("보내기 %s → %s (%s, %d자, force=%v): %s", requestLogin(r), req.Pane, a.Session,
		utf8.RuneCountInString(req.Text), req.Force, result)
	writeJSON(w, http.StatusOK, promptView{Result: result, Detail: errDetail(err)})
}

// promptResult 는 PromptAgent 의 오류를 화면의 갈래로 바꾼다. 순서가 뜻이다: ErrAgentBusy 는 늘
// ErrAgentNotReady 와 함께 감싸이므로 먼저 본다.
func promptResult(err error, force bool) string {
	switch {
	case err == nil && force:
		return "queued"
	case err == nil:
		return "sent"
	case errors.Is(err, tmux.ErrAgentBlocked):
		return "blocked"
	case errors.Is(err, tmux.ErrAgentBusy):
		return "busy"
	case errors.Is(err, tmux.ErrAgentNotReady):
		return "not_ready"
	case errors.Is(err, tmux.ErrNotSubmitted):
		return "retry"
	case errors.Is(err, tmux.ErrPromptUnknown):
		return "unknown"
	case errors.Is(err, tmux.ErrPromptNotSeen):
		return "not_seen"
	case errors.Is(err, tmux.ErrNoAgent), errors.Is(err, tmux.ErrAgentGone), errors.Is(err, tmux.ErrAmbiguous):
		return "gone"
	default:
		return "error"
	}
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ── 대화 ─────────────────────────────────────────────────────────────

type stampedText struct {
	Text string `json:"text"`
	At   int64  `json:"at,omitempty"` // ms. 모르면 0
}

type turnView struct {
	// Result: ok | running | blocked | no_reply | gone | error.
	Result string       `json:"result"`
	Detail string       `json:"detail,omitempty"`
	Human  bool         `json:"human"`
	Prompt *stampedText `json:"prompt,omitempty"`
	Reply  *stampedText `json:"reply,omitempty"`
}

func stamped(text string, at time.Time) *stampedText {
	if text == "" {
		return nil
	}
	v := &stampedText{Text: text}
	if !at.IsZero() {
		v.At = at.UnixMilli()
	}
	return v
}

func (s *server) handleTurn(w http.ResponseWriter, r *http.Request) {
	a, err := s.resolve(r.URL.Query().Get("pane"), queryPID(r))
	if errors.Is(err, errBadTarget) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var turn tmux.LastTurn
	if err == nil {
		turn, err = s.b.LastTurn(a)
	}
	writeJSON(w, http.StatusOK, turnView{
		Result: turnResult(err),
		Detail: errDetail(err),
		Human:  turn.Human,
		Prompt: stamped(turn.Prompt, turn.PromptAt),
		Reply:  stamped(turn.Reply, turn.ReplyAt),
	})
}

// turnResult 는 ReadLastTurn 의 오류를 화면의 갈래로 바꾼다. 회신이 없는 갈래에서도 보낸 말은 함께 간다.
func turnResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, tmux.ErrTurnRunning):
		return "running"
	case errors.Is(err, tmux.ErrAgentBlocked):
		return "blocked"
	case errors.Is(err, tmux.ErrNoReply):
		return "no_reply"
	case errors.Is(err, tmux.ErrNoAgent), errors.Is(err, tmux.ErrAgentGone):
		return "gone"
	default:
		return "error"
	}
}

// ── 화면 ─────────────────────────────────────────────────────────────

type screenView struct {
	ANSI string `json:"ansi"`
}

// handleScreen 은 pane 하나의 화면을 색 그대로 낸다. 대상은 지금 있는 Claude pane(pane+pid), 또는 지금 있는
// 세션의 활성 pane(session) 뿐이다 — 아무 tmux 대상이나 받지 않는다.
func (s *server) handleScreen(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var target string
	switch {
	case q.Get("pane") != "":
		a, err := s.resolve(q.Get("pane"), queryPID(r))
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		target = a.PaneID
	case q.Get("session") != "":
		name := q.Get("session")
		sessions, err := s.b.Sessions()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tmux 세션을 읽지 못했습니다: "+err.Error())
			return
		}
		if !slices.ContainsFunc(sessions, func(ss tmux.Session) bool { return ss.Name == name }) {
			writeError(w, http.StatusNotFound, "그런 세션이 없습니다: "+name)
			return
		}
		// 콜론까지 써야 그 세션만 잡는다 — 맨 이름은 접두사로 다른 세션을 잡을 수 있다(park.md, tmux-assumptions 19절).
		target = "=" + name + ":"
	default:
		writeError(w, http.StatusBadRequest, "pane(+pid) 나 session 으로 지목한다")
		return
	}
	out, err := s.b.Capture(target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "화면을 읽지 못했습니다: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, screenView{ANSI: out})
}
