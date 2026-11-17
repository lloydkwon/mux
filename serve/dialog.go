package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/lloydkwon/mux/tmux"
)

// 대화상자에 답하기 — Claude 가 질문(AskUserQuestion)이나 권한 확인을 띄우고 기다릴 때, 폰에서 선택지를 고른다
// (docs/design/serve.md «대화상자에 답하기»). 화면 문구는 클라이언트가 정하고, 서버는 갈래와 이유만 낸다.

type dialogTabView struct {
	Label  string `json:"label"`
	Done   bool   `json:"done"`
	Submit bool   `json:"submit"`
}

type dialogOptionView struct {
	N        int    `json:"n"`
	Label    string `json:"label"`
	Detail   string `json:"detail,omitempty"`
	Chosen   bool   `json:"chosen"`
	FreeText bool   `json:"freeText"`
	// Draft 는 직접 쓰기 칸에 쓰다 만 글이다(PC 에서 쓰다 말았거나 앞서 실패한 답). 이 칸으로 답하면 지우고 새로 쓴다.
	Draft    string `json:"draft,omitempty"`
	Checkbox bool   `json:"checkbox"`
	Checked  bool   `json:"checked"`
}

type dialogView struct {
	// Key 는 이 대화상자의 열쇠다(tmux.Dialog.Key). 답할 때 그대로 돌려보낸다 — 그사이 다른 대화상자가 떴으면
	// 서버가 누르지 않는다(권한 확인은 어느 것이나 «1. Yes» 라 번호와 글로는 못 가린다).
	Key     string             `json:"key"`
	Kind    string             `json:"kind"` // question | permission | other
	Tabs    []dialogTabView    `json:"tabs"`
	Prompt  []string           `json:"prompt"`
	Options []dialogOptionView `json:"options"`
	Current int                `json:"current"`
	Hint    string             `json:"hint,omitempty"`
	// Multi 는 여러 개 고르기다 — 체크박스를 폰에서 고르고 한 번에 보낸다(answerRequest.Multi).
	Multi bool `json:"multi"`
}

type dialogResponse struct {
	// Result: ok | none | gone | error.
	Result string      `json:"result"`
	Detail string      `json:"detail,omitempty"`
	Dialog *dialogView `json:"dialog,omitempty"`
}

func dialogViewOf(d tmux.Dialog) *dialogView {
	v := &dialogView{Key: d.Key(), Kind: string(d.Kind), Tabs: []dialogTabView{}, Prompt: d.Prompt, Options: []dialogOptionView{},
		Current: d.Current, Hint: d.Hint, Multi: d.Multi}
	if v.Prompt == nil {
		v.Prompt = []string{}
	}
	for _, t := range d.Tabs {
		v.Tabs = append(v.Tabs, dialogTabView{Label: t.Label, Done: t.Done, Submit: t.Submit})
	}
	for _, o := range d.Options {
		v.Options = append(v.Options, dialogOptionView{N: o.N, Label: o.Label, Detail: o.Detail, Chosen: o.Chosen,
			FreeText: o.FreeText, Draft: o.Draft(), Checkbox: o.Checkbox, Checked: o.Checked})
	}
	return v
}

func (s *server) handleDialog(w http.ResponseWriter, r *http.Request) {
	a, err := s.resolve(r.URL.Query().Get("pane"), queryPID(r))
	if errors.Is(err, errBadTarget) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var d tmux.Dialog
	if err == nil {
		d, err = s.b.Dialog(a)
	}
	resp := dialogResponse{Result: dialogResult(err), Detail: errDetail(err)}
	if err == nil {
		resp.Dialog = dialogViewOf(d)
	}
	writeJSON(w, http.StatusOK, resp)
}

func dialogResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, tmux.ErrNoDialog):
		return "none"
	case errors.Is(err, tmux.ErrNoAgent), errors.Is(err, tmux.ErrAgentGone):
		return "gone"
	default:
		return "error"
	}
}

type answerRequest struct {
	Pane string `json:"pane"`
	PID  int    `json:"pid"`
	// Key 는 폰이 본 대화상자의 열쇠(dialogView.Key)다. 답하기·여러 개 고르기·Esc 모두 있어야 한다.
	Key string `json:"key"`
	// N 과 Label 은 폰이 본 선택지다. 서버는 누르기 직전에 화면을 다시 읽어 둘이 같을 때만 누른다.
	N     int    `json:"n"`
	Label string `json:"label"`
	// Text 는 직접 쓰기(«Type something.») 선택지의 글이다.
	Text string `json:"text"`
	// Cancel 이면 Esc 로 대화상자를 거둔다(N·Label 은 보지 않는다).
	Cancel bool `json:"cancel"`
	// Multi 면 여러 개 고르기다: Options 는 폰이 본 체크박스 전부와 바라는 체크다(N·Label 은 보지 않는다).
	Multi   bool                 `json:"multi"`
	Options []multiChoiceRequest `json:"options"`
}

type multiChoiceRequest struct {
	N       int    `json:"n"`
	Label   string `json:"label"`
	Checked bool   `json:"checked"`
}

func (s *server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPromptBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req answerRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "요청을 읽지 못했습니다")
		return
	}
	switch {
	case req.Key == "" || len(req.Key) > 64:
		writeError(w, http.StatusBadRequest, "폰이 본 대화상자의 열쇠가 있어야 한다")
		return
	case req.Cancel:
	case req.Multi:
		if len(req.Options) == 0 {
			writeError(w, http.StatusBadRequest, "여러 개 고르기는 폰이 본 선택지를 모두 보낸다")
			return
		}
		for _, o := range req.Options {
			if o.N < 1 || o.N > 9 || o.Label == "" {
				writeError(w, http.StatusBadRequest, "선택지 번호(1~9)와 폰이 본 글이 있어야 한다")
				return
			}
		}
	case req.N < 1 || req.Label == "":
		writeError(w, http.StatusBadRequest, "선택지 번호와 폰이 본 글이 있어야 한다")
		return
	}
	if req.Text != "" {
		if err := tmux.ValidatePromptText(req.Text); err != nil {
			writeJSON(w, http.StatusOK, promptView{Result: "invalid", Detail: err.Error()})
			return
		}
	}
	a, err := s.resolve(req.Pane, req.PID)
	if errors.Is(err, errBadTarget) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err == nil {
		switch {
		case req.Cancel:
			err = s.b.Cancel(a, req.Key)
		case req.Multi:
			want := make([]tmux.MultiChoice, len(req.Options))
			for i, o := range req.Options {
				want[i] = tmux.MultiChoice{N: o.N, Label: o.Label, Checked: o.Checked}
			}
			err = s.b.AnswerMulti(a, req.Key, want, req.Text)
		default:
			err = s.b.Answer(a, req.Key, req.N, req.Label, req.Text)
		}
	}
	result := answerResult(err, req.Cancel)
	// 선택지 글과 직접 쓴 글은 적지 않는다 — 보내기의 로그와 같은 이유다.
	switch {
	case req.Cancel:
		s.log.Printf("답하기 %s → %s (%s): 취소(Esc) → %s", requestLogin(r), req.Pane, a.Session, result)
	case req.Multi:
		checked := 0
		for _, o := range req.Options {
			if o.Checked {
				checked++
			}
		}
		s.log.Printf("답하기 %s → %s (%s): 여러 개 고르기 %d개, 직접 쓰기 %d자 → %s", requestLogin(r), req.Pane, a.Session,
			checked, utf8.RuneCountInString(req.Text), result)
	default:
		s.log.Printf("답하기 %s → %s (%s): %d번, 직접 쓰기 %d자 → %s", requestLogin(r), req.Pane, a.Session, req.N,
			utf8.RuneCountInString(req.Text), result)
	}
	writeJSON(w, http.StatusOK, promptView{Result: result, Detail: errDetail(err)})
}

// answerResult 는 답하기의 오류를 화면의 갈래로 바꾼다.
func answerResult(err error, cancel bool) string {
	switch {
	case err == nil && cancel:
		return "cancelled"
	case err == nil:
		return "answered"
	case errors.Is(err, tmux.ErrDialogChanged):
		return "changed"
	case errors.Is(err, tmux.ErrNoDialog):
		return "none"
	case errors.Is(err, tmux.ErrAnswerUnknown):
		return "unknown"
	case errors.Is(err, tmux.ErrAnswerText):
		return "invalid"
	case errors.Is(err, tmux.ErrAgentNotReady):
		return "not_ready"
	case errors.Is(err, tmux.ErrNoAgent), errors.Is(err, tmux.ErrAgentGone), errors.Is(err, tmux.ErrAmbiguous):
		return "gone"
	default:
		return "error"
	}
}
