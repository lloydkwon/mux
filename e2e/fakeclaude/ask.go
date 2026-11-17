//go:build e2e

package main

import (
	"strings"
)

// 질문 대화상자(AskUserQuestion) 흉내 — `/fake-ask` 를 받으면 질문 둘과 Submit 탭을 띄운다. 모양과 키는 진짜
// Claude Code 2.1.312 에서 잰 것이다(tmux/testdata/dialog, docs/design/serve.md «대화상자에 답하기»):
//   - 번호 선택지에 ❯ 커서, 선택지 셋째는 «Type something.», 그 아래 선 너머에 «Chat about this».
//   - 숫자키는 그 선택지를 고르고 다음 질문으로, 마지막 질문 뒤에는 Submit 탭(검토)으로 간다.
//   - «Type something.» 에서는 숫자키가 커서만 옮기고, 친 글이 그 자리에 보이며 Enter 로 받는다. 커서가 그 칸에
//     있으면 숫자키도 글자로 들어간다. 화살표로 나가도 칸의 글은 남는다.
//   - Esc 는 대화상자를 거둔다(답하지 않음). 상태 파일은 waiting / input needed.
//
// 글 커서는 늘 칸의 끝이라고 친다 — 진짜는 칸에 다시 들어오면 맨 앞이지만 mux 는 End 뒤에 지우므로 같다(End 는
// main.go 의 readKeys 가 읽고 버린다).

const askFreeText = "Type something."

type askQuestion struct {
	header, prompt string
	options        []string
}

type fakeAsk struct {
	qs      []askQuestion
	answers []string
	tab     int // len(qs) 면 Submit 탭
	cursor  int // 1 부터. 여러 개 고르기에서 0 은 번호 없는 Submit 줄이다
	free    bool
	typing  []rune
	done    chan []string // nil 을 받으면 답하지 않음

	// 여러 개 고르기(`/fake-ask-multi`) — 실측: 선택지가 «[ ] 글» 체크박스이고 숫자키는 체크만 바꾼다(커서 그대로).
	// 직접 쓰기는 «[ ] Type something» 이고 화살표로 커서를 옮겨야 친 글이 들어간다. 번호 없는 «Submit» 줄에서
	// Enter 면 Submit 탭(검토)으로 간다.
	multi   bool
	checked map[int]bool
}

func newFakeAsk(multi bool) *fakeAsk {
	qs := []askQuestion{
		{"과일", "과일을 골라 주세요.", []string{"사과", "배"}},
		{"색", "색을 골라 주세요.", []string{"빨강", "파랑"}},
	}
	if multi {
		qs = []askQuestion{{"보강", "무엇을 보강할까요?(여러 개)", []string{"중단", "재시도"}}}
	}
	return &fakeAsk{qs: qs, answers: make([]string, len(qs)), cursor: 1, done: make(chan []string, 1), multi: multi, checked: map[int]bool{}}
}

func (a *fakeAsk) options() []string {
	if a.tab == len(a.qs) {
		return []string{"Submit answers", "Cancel"}
	}
	free := askFreeText
	if a.multi {
		free = strings.TrimSuffix(askFreeText, ".")
	}
	return append(append([]string{}, a.qs[a.tab].options...), free, "Chat about this")
}

// boxes 는 여러 개 고르기의 체크박스 수다(마지막 «Chat about this» 를 뺀 것).
func (a *fakeAsk) boxes() int { return len(a.options()) - 1 }

// arrow 는 화살표 하나다. 여러 개 고르기에서 커서는 체크박스 1..n, Submit 줄(0), Chat about this 순으로 선다.
func (a *fakeAsk) arrow(d int) {
	if a.tab == len(a.qs) {
		return
	}
	if !a.multi {
		opts := a.options()
		a.cursor = min(max(a.cursor+d, 1), len(opts))
		a.free = opts[a.cursor-1] == askFreeText
		return
	}
	stops := []int{}
	for n := 1; n <= a.boxes(); n++ {
		stops = append(stops, n)
	}
	stops = append(stops, 0, a.boxes()+1)
	at := 0
	for i, s := range stops {
		if s == a.cursor {
			at = i
		}
	}
	at += d
	if at < 0 {
		at = 0
	}
	if at >= len(stops) {
		at = len(stops) - 1
	}
	a.cursor = stops[at]
	a.free = a.cursor == a.boxes()
}

// key 는 키 하나를 받는다. 대화상자가 끝나면 true.
func (a *fakeAsk) key(c rune) bool {
	if a.multi && a.tab < len(a.qs) {
		return a.multiKey(c)
	}
	switch {
	case a.free && (c == '\r' || c == '\n'):
		if len(a.typing) > 0 {
			a.choose(string(a.typing))
		}
	case a.free && (c == 0x7f || c == 0x08):
		if len(a.typing) > 0 {
			a.typing = a.typing[:len(a.typing)-1]
		}
	case a.free && c >= 0x20:
		a.typing = append(a.typing, c)
	case c >= '1' && c <= '9':
		return a.pick(int(c - '0'))
	case c == '\r' || c == '\n':
		return a.pick(a.cursor)
	}
	return false
}

func (a *fakeAsk) pick(n int) bool {
	opts := a.options()
	if n < 1 || n > len(opts) {
		return false
	}
	if a.tab == len(a.qs) {
		if n == 1 {
			a.finish(a.answers)
		} else {
			a.finish(nil)
		}
		return true
	}
	if opts[n-1] == askFreeText {
		a.cursor, a.free = n, true
		return false
	}
	a.choose(opts[n-1])
	return false
}

func (a *fakeAsk) choose(answer string) {
	a.answers[a.tab] = answer
	a.tab++
	a.cursor, a.free, a.typing = 1, false, nil
}

func (a *fakeAsk) multiKey(c rune) bool {
	switch {
	case a.free && (c == 0x7f || c == 0x08):
		if len(a.typing) > 0 {
			a.typing = a.typing[:len(a.typing)-1]
		}
		if len(a.typing) == 0 {
			a.checked[a.boxes()] = false // 글을 다 지우면 체크도 꺼진다(실측)
		}
	case a.free && c >= 0x20:
		a.typing = append(a.typing, c)
		a.checked[a.boxes()] = true
	case c >= '1' && c <= '9':
		if n := int(c - '0'); n <= a.boxes() {
			a.checked[n] = !a.checked[n]
		}
	case (c == '\r' || c == '\n') && a.cursor == 0:
		var picked []string
		for n := 1; n <= a.boxes(); n++ {
			if !a.checked[n] {
				continue
			}
			if n == a.boxes() {
				picked = append(picked, string(a.typing))
			} else {
				picked = append(picked, a.options()[n-1])
			}
		}
		a.answers[a.tab] = strings.Join(picked, ", ")
		a.tab++
		a.cursor, a.free = 1, false
	}
	return false
}

func (a *fakeAsk) cancel() { a.finish(nil) }

// finish 는 답을 한 번만 넘긴다 — 끝난 뒤에 온 Esc 가 막히지 않게.
func (a *fakeAsk) finish(answers []string) {
	select {
	case a.done <- answers:
	default:
	}
}

// lines 는 대화상자를 그린다(입력창 자리).
func (a *fakeAsk) lines() []string {
	rule := strings.Repeat("─", 60)
	var tabs []string
	for i, q := range a.qs {
		mark := "☐"
		if i < a.tab {
			mark = "☒"
		}
		tabs = append(tabs, mark+" "+q.header)
	}
	out := []string{rule, "←  " + strings.Join(tabs, "  ") + "  ✔ Submit  →", ""}
	if a.tab == len(a.qs) {
		out = append(out, "Review your answers", "")
		for i, q := range a.qs {
			out = append(out, " ● "+q.prompt, "   → "+a.answers[i])
		}
		out = append(out, "", "Ready to submit your answers?", "")
	} else {
		out = append(out, a.qs[a.tab].prompt, "")
	}
	multi := a.multi && a.tab < len(a.qs)
	for i, o := range a.options() {
		n := i + 1
		mark := "  "
		if n == a.cursor {
			mark = "❯ "
		}
		label := o
		if strings.HasPrefix(o, "Type something") && len(a.typing) > 0 {
			label = string(a.typing)
		}
		if o == "Chat about this" {
			if multi {
				submit := "     Submit"
				if a.cursor == 0 {
					submit = "❯    Submit"
				}
				out = append(out, submit)
			}
			out = append(out, rule)
		}
		if multi && o != "Chat about this" {
			box := "[ ] "
			if a.checked[n] {
				box = "[✔] "
			}
			label = box + label
		}
		out = append(out, mark+itoa(n)+". "+label)
	}
	switch {
	case multi:
		out = append(out, "", "Enter to select · ↑/↓ to navigate · Esc to cancel")
	case a.tab < len(a.qs):
		out = append(out, "", "Enter to select · Tab/Arrow keys to navigate · Esc to cancel")
	}
	return out
}

func itoa(n int) string { return string(rune('0' + n)) }
