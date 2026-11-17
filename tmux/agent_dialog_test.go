package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 대화상자 해석과 답하기 — mux serve 가 폰에서 Claude 의 질문·권한 확인에 답할 때 쓴다(docs/design/serve.md).
// 픽스처는 2026-11-17 Claude Code 2.1.312 의 진짜 화면이다(격리 tmux 서버의 haiku, 경로만 바꿈).

func dialogFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "dialog", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// dialogKey 는 폰이 그 화면에서 받은 대화상자 열쇠다.
func dialogKey(t *testing.T, screen string) string {
	t.Helper()
	d, ok := parseDialog(screen)
	if !ok {
		t.Fatalf("대화상자가 아니다:\n%s", screen)
	}
	return d.Key()
}

func optionLabels(d Dialog) []string {
	out := make([]string, len(d.Options))
	for i, o := range d.Options {
		out[i] = o.Label
	}
	return out
}

func TestParseDialogQuestion(t *testing.T) {
	d, ok := parseDialog(dialogFixture(t, "question-1"))
	if !ok {
		t.Fatal("질문 대화상자를 못 찾았다")
	}
	if d.Kind != DialogQuestion || d.Current != 1 {
		t.Errorf("kind=%s current=%d", d.Kind, d.Current)
	}
	if got := fmt.Sprint(d.Tabs); got != "[{방식 false false} {주기 false false} {Submit false true}]" {
		t.Errorf("tabs = %s", got)
	}
	if strings.Join(d.Prompt, "|") != "eBay 반영 방식을 골라 주세요." {
		t.Errorf("prompt = %q", d.Prompt)
	}
	if got := strings.Join(optionLabels(d), "|"); got != "스케줄러|이벤트|Type something.|Chat about this" {
		t.Errorf("options = %s", got)
	}
	if d.Options[0].Detail != "정해진 간격마다 주기적으로 eBay 데이터를 가져와 반영합니다." {
		t.Errorf("detail = %q", d.Options[0].Detail)
	}
	if !d.Options[2].FreeText || d.Options[0].FreeText || d.Options[3].FreeText {
		t.Error("직접 쓰기는 «Type something.» 하나뿐이다")
	}
	if d.Hint == "" {
		t.Error("안내 줄을 놓쳤다")
	}

	d2, ok := parseDialog(dialogFixture(t, "question-2"))
	if !ok || !d2.Tabs[0].Done || d2.Tabs[1].Done || strings.Join(d2.Prompt, "") != "반영 주기를 골라 주세요." {
		t.Errorf("둘째 질문 = %+v", d2)
	}
	if d.signature() == d2.signature() {
		t.Error("다른 질문인데 서명이 같다 — 답이 들어갔는지 못 가린다")
	}

	// nse 에서 본 모양(2026-11-17): 탭 이름에 띄어쓰기가 있고, 안내 줄 아래에 이름이 박힌 선이 있다.
	nse := strings.Replace(dialogFixture(t, "question-1"), "←  ☐ 방식  ☐ 주기  ✔ Submit  →", "←  ☐ 반영 방식  ☐ 반영 주기  ✔ Submit  →", 1)
	nse = strings.TrimRight(nse, "\n") + "\n" + strings.Repeat("─", 60) + " shopify-ebay-listing-sync ─\n"
	d3, ok := parseDialog(nse)
	if !ok || len(d3.Tabs) != 3 || d3.Tabs[0].Label != "반영 방식" || d3.Tabs[1].Label != "반영 주기" || len(d3.Options) != 4 {
		t.Errorf("nse 모양 = %+v", d3)
	}
}

func TestParseDialogSubmitTabWithoutHint(t *testing.T) {
	// Submit 탭에는 안내 줄(Esc to cancel)이 없다(실측). 탭 줄로 질문 대화상자임을 안다.
	d, ok := parseDialog(dialogFixture(t, "question-submit"))
	if !ok {
		t.Fatal("Submit 탭을 못 찾았다")
	}
	if got := strings.Join(optionLabels(d), "|"); got != "Submit answers|Cancel" || d.Kind != DialogQuestion {
		t.Errorf("kind=%s options=%s", d.Kind, got)
	}
	if !strings.Contains(strings.Join(d.Prompt, "\n"), "→ 사과") || !d.Tabs[0].Done || !d.Tabs[1].Done {
		t.Errorf("검토 글·탭 = %q %+v", d.Prompt, d.Tabs)
	}
}

func TestParseDialogPermission(t *testing.T) {
	d, ok := parseDialog(dialogFixture(t, "permission"))
	if !ok {
		t.Fatal("권한 확인을 못 찾았다")
	}
	if d.Kind != DialogPermission || d.Current != 1 || len(d.Tabs) != 0 {
		t.Errorf("kind=%s current=%d tabs=%v", d.Kind, d.Current, d.Tabs)
	}
	prompt := strings.Join(d.Prompt, "\n")
	for _, want := range []string{"Bash command", "date > probe2.txt", "Do you want to proceed?"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt 에 %q 가 없다:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "╌") {
		t.Error("점선은 글이 아니다")
	}
	if got := strings.Join(optionLabels(d), "|"); !strings.HasPrefix(got, "Yes|Yes, and always allow access to /home/u/proj from|Yes, and switch to auto mode") || !strings.HasSuffix(got, "|No") {
		t.Errorf("options = %s", got)
	}
	if d.Options[1].Detail != "this project" {
		t.Errorf("접힌 줄 = %q", d.Options[1].Detail)
	}
}

func TestParseDialogTypedFreeText(t *testing.T) {
	// 직접 쓰기에 글을 치면 «Type something.» 자리에 그 글이 보이고 ✔ 는 앞서 고른 답이다(실측).
	screen := strings.Replace(dialogFixture(t, "question-2"), "❯ 1. 5분", "  1. 5분", 1)
	screen = strings.Replace(screen, "  2. 10분", "  2. 10분 ✔", 1)
	screen = strings.Replace(screen, "  3. Type something.", "❯ 3. 7분으로 해 줘", 1)
	d, ok := parseDialog(screen)
	if !ok || d.Current != 3 || d.Options[2].Label != "7분으로 해 줘" || !d.Options[1].Chosen || d.Options[1].Label != "10분" {
		t.Errorf("d = %+v", d)
	}
}

func TestParseDialogIgnoresWhatIsNotADialog(t *testing.T) {
	tests := map[string]string{
		"입력창": (&fakePane{box: "고쳐 줘"}).screen(),
		"입력창에 사람이 친 번호 목록": strings.Repeat("─", 40) + "\n❯ 1. 첫째\n  2. 둘째\n" + strings.Repeat("─", 40) + "\n  ⏵⏵ auto mode on\n",
		"회신 속 번호 목록":       "● 할 일\n  1. 하나\n  2. 둘\n" + strings.Repeat("─", 40) + "\n❯ \n" + strings.Repeat("─", 40) + "\n",
		// 폴더 신뢰 대화상자에는 번호가 없다(실측) — 숫자키로 답할 수 없으니 대화상자로 내지 않는다.
		"번호 없는 메뉴": strings.Repeat("─", 40) + "\n Quick safety check\n ❯ No, exit\n   Yes, I trust this folder\n Enter to confirm · Esc to cancel\n",
	}
	for name, screen := range tests {
		if d, ok := parseDialog(screen); ok {
			t.Errorf("%s 를 대화상자로 읽었다: %+v", name, d)
		}
	}
}

// fakeDialogPane 은 화면 하나와 키에 따른 다음 화면을 흉내 내는 러너다.
type fakeDialogPane struct {
	pane   string
	screen string
	next   map[string]string // 키 → 다음 화면. 없으면 화면이 그대로다(키가 먹혔다)
	typed  func(screen, text string) string
	keys   func(keys []string) // 있으면 모든 키를 이것이 받는다(여러 개 고르기의 상태 기계)
	runs   []string
	inMode bool
}

func (f *fakeDialogPane) Output(name string, args ...string) ([]byte, error) {
	k := strings.Join(args, " ")
	switch k {
	case "capture-pane -p -t " + f.pane:
		return []byte(f.screen), nil
	case "display-message -p #{socket_path}":
		return []byte("/tmp/tmux-test/default\n"), nil
	case "display-message -p -t " + f.pane + " #{pane_in_mode} #{pane_synchronized}":
		return []byte(fmt.Sprintf("%d 0\n", b2i(f.inMode))), nil
	}
	return nil, fmt.Errorf("fakeDialogPane: unexpected Output %s", k)
}

func (f *fakeDialogPane) Run(name string, args ...string) error {
	f.runs = append(f.runs, strings.Join(args, " "))
	if len(args) < 4 || args[0] != "send-keys" || args[2] != f.pane {
		return fmt.Errorf("fakeDialogPane: unexpected Run %v", args)
	}
	if f.keys != nil {
		f.keys(args[3:])
		return nil
	}
	key := args[3]
	if key == "-l" {
		if f.typed != nil {
			f.screen = f.typed(f.screen, args[len(args)-1])
		}
		return nil
	}
	if s, ok := f.next[key]; ok {
		f.screen = s
	}
	return nil
}

func withDialogPane(t *testing.T, status string, f *fakeDialogPane, fn func(a Agent)) {
	t.Helper()
	if f.pane == "" {
		f.pane = "%5"
	}
	files := map[string]string{"300.json": sessionFile(300, "s-300", "w:@1."+f.pane, status, "target", 1)}
	withClaudeSessions(t, files, nil, func() {
		SetRunner(f)
		oldNow, oldSleep, oldLock := agentNow, agentSleep, agentLockDir
		now := agentNow()
		agentNow = func() time.Time { return now }
		agentSleep = func(d time.Duration) { now = now.Add(d) }
		lockDir := t.TempDir()
		agentLockDir = func() string { return lockDir }
		defer func() { agentNow, agentSleep, agentLockDir = oldNow, oldSleep, oldLock }()
		fn(Agent{PaneID: f.pane, Session: "w", PID: 300, SessionID: "s-300", CWD: "/home/u/proj", State: AgentBlocked})
	})
}

func TestAnswerDialogPressesTheDigitWhenTheScreenStillShowsIt(t *testing.T) {
	f := &fakeDialogPane{screen: dialogFixture(t, "question-1"), next: map[string]string{"1": dialogFixture(t, "question-2")}}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, dialogFixture(t, "question-1")), 1, "스케줄러", ""); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Join(f.runs, "\n") != "send-keys -t %5 1" {
		t.Errorf("보낸 키 = %q", f.runs)
	}
}

func TestAnswerDialogRefusesWhatThePhoneDidNotSee(t *testing.T) {
	// 폰은 첫 질문의 «2. 이벤트» 를 봤는데 그사이 둘째 질문으로 넘어갔다 — 2 를 누르면 «10분» 이 들어간다.
	// 2026-11-17 측정 중에 낡은 화면을 보고 2 를 눌러 Submit 탭의 Cancel 이 눌린 일이 있다.
	tests := []struct {
		name, seen, screen, status, label string
		n                                 int
		want                              error
	}{
		{"다른 질문으로 넘어갔다", "question-1", "question-2", "waiting", "이벤트", 2, ErrDialogChanged},
		{"번호와 글은 같아도 다른 질문이다", "question-1", "question-2", "waiting", "5분", 1, ErrDialogChanged},
		{"없는 번호", "question-1", "question-1", "waiting", "?", 7, ErrDialogChanged},
		{"10 이상은 숫자키 하나가 아니다", "question-1", "question-1", "waiting", "x", 10, ErrDialogChanged},
		{"대화상자가 닫혔다(idle)", "question-1", "question-1", "idle", "스케줄러", 1, ErrNoDialog},
		{"작업 중", "question-1", "question-1", "busy", "스케줄러", 1, ErrNoDialog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeDialogPane{screen: dialogFixture(t, tt.screen)}
			withDialogPane(t, tt.status, f, func(a Agent) {
				if err := AnswerDialog(a, dialogKey(t, dialogFixture(t, tt.seen)), tt.n, tt.label, ""); !errors.Is(err, tt.want) {
					t.Errorf("err = %v, want %v", err, tt.want)
				}
			})
			if len(f.runs) != 0 {
				t.Errorf("아무것도 누르지 말아야 하는데 %v", f.runs)
			}
		})
	}
	// 화면이 대화상자가 아니면(입력창) 숫자는 입력창에 글자로 들어간다 — 누르지 않는다.
	f := &fakeDialogPane{screen: (&fakePane{}).screen()}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, dialogFixture(t, "permission")), 1, "Yes", ""); !errors.Is(err, ErrNoDialog) || len(f.runs) != 0 {
			t.Errorf("err = %v, runs = %v", err, f.runs)
		}
	})
	// 복사 모드면 키가 모드로 간다.
	f = &fakeDialogPane{screen: dialogFixture(t, "question-1"), inMode: true}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, dialogFixture(t, "question-1")), 1, "스케줄러", ""); !errors.Is(err, ErrAgentNotReady) || len(f.runs) != 0 {
			t.Errorf("err = %v, runs = %v", err, f.runs)
		}
	})
}

func TestAnswerDialogUnknownWhenNothingChanged(t *testing.T) {
	// 키를 눌렀는데 화면이 그대로다. 먹혔는지 모른다 — 같은 모양의 다음 대화상자일 수도 있다.
	f := &fakeDialogPane{screen: dialogFixture(t, "permission")}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, dialogFixture(t, "permission")), 4, "No", ""); !errors.Is(err, ErrAnswerUnknown) {
			t.Errorf("err = %v", err)
		}
	})
	if len(f.runs) != 1 {
		t.Errorf("한 번만 눌러야 한다: %v", f.runs)
	}
}

func TestAnswerDialogFreeText(t *testing.T) {
	q2 := dialogFixture(t, "question-2")
	focused := strings.Replace(strings.Replace(q2, "❯ 1. 5분", "  1. 5분", 1), "  3. Type something.", "❯ 3. Type something.", 1)
	f := &fakeDialogPane{
		screen: q2,
		next:   map[string]string{"3": focused, "Enter": dialogFixture(t, "question-submit")},
		typed: func(screen, text string) string {
			return strings.Replace(screen, "❯ 3. Type something.", "❯ 3. "+text, 1)
		},
	}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, q2), 3, "Type something.", "7분으로 해 줘"); err != nil {
			t.Fatal(err)
		}
	})
	want := "send-keys -t %5 3\nsend-keys -t %5 -l -- 7분으로 해 줘\nsend-keys -t %5 Enter"
	if strings.Join(f.runs, "\n") != want {
		t.Errorf("보낸 키:\n%s\nwant:\n%s", strings.Join(f.runs, "\n"), want)
	}
}

func TestAnswerDialogFreeTextChecks(t *testing.T) {
	q2 := dialogFixture(t, "question-2")
	// 글이 없거나 보낼 수 없는 글이면 아무것도 누르지 않는다.
	for _, text := range []string{"", "두 줄\n글", "!rm"} {
		f := &fakeDialogPane{screen: q2}
		withDialogPane(t, "waiting", f, func(a Agent) {
			if err := AnswerDialog(a, dialogKey(t, q2), 3, "Type something.", text); err == nil || len(f.runs) != 0 {
				t.Errorf("%q: err = %v, runs = %v", text, err, f.runs)
			}
		})
	}
	// 친 글이 칸에 그대로 보이지 않으면 Enter 를 누르지 않는다.
	focused := strings.Replace(q2, "  3. Type something.", "❯ 3. Type something.", 1)
	f := &fakeDialogPane{
		screen: q2,
		next:   map[string]string{"3": focused},
		typed: func(screen, text string) string {
			return strings.Replace(screen, "❯ 3. Type something.", "❯ 3. 7분으로 ㅎ", 1)
		},
	}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, q2), 3, "Type something.", "7분으로 해 줘"); err == nil {
			t.Error("어긋났는데 성공이라고 했다")
		}
	})
	for _, r := range f.runs {
		if r == "send-keys -t %5 Enter" {
			t.Errorf("어긋났는데 Enter 를 눌렀다: %v", f.runs)
		}
	}
}

func TestCancelDialogSendsEscapeOnce(t *testing.T) {
	f := &fakeDialogPane{screen: dialogFixture(t, "permission"), next: map[string]string{"Escape": (&fakePane{}).screen()}}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := CancelDialog(a, dialogKey(t, dialogFixture(t, "permission"))); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Join(f.runs, "\n") != "send-keys -t %5 Escape" {
		t.Errorf("보낸 키 = %v", f.runs)
	}
	f = &fakeDialogPane{screen: (&fakePane{}).screen()}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := CancelDialog(a, dialogKey(t, dialogFixture(t, "permission"))); !errors.Is(err, ErrNoDialog) || len(f.runs) != 0 {
			t.Errorf("대화상자 없이 Esc: err = %v, runs = %v", err, f.runs)
		}
	})
}

// ── 여러 개 고르기(multiSelect) ─────────────────────────────────────────────
// 실측(2026-11-17): 선택지가 «[ ] 글» 체크박스이고, 숫자키는 체크만 켜고 끈다(커서도 그대로, 넘어가지 않는다).
// 끝에 번호 없는 «Submit» 줄이 있어 화살표로만 가고, 거기서 Enter 면 Submit 탭(검토)으로 간다. 직접 쓰기는
// «[ ] Type something»(마침표 없음)이고, 화살표로 커서를 옮겨야 친 글이 들어간다(숫자키는 체크만 한다).

func TestParseDialogMultiSelect(t *testing.T) {
	d, ok := parseDialog(dialogFixture(t, "multi-1"))
	if !ok || !d.Multi || d.Kind != DialogQuestion || !d.SubmitLine || d.OnSubmit || d.Current != 1 {
		t.Fatalf("d = %+v", d)
	}
	if got := strings.Join(optionLabels(d), "|"); got != "중단|재시도|알림|Type something|Chat about this" {
		t.Errorf("options = %s", got)
	}
	if !d.Options[0].Checkbox || d.Options[0].Checked || !d.Options[3].Checkbox || !d.Options[3].FreeText || d.Options[4].Checkbox {
		t.Errorf("체크박스 = %+v", d.Options)
	}
	if d.Options[0].Detail != "작업을 중단하는 기능을 보강합니다." || d.Options[3].Detail != "" {
		t.Errorf("Submit 줄이 설명으로 들어갔다: %q / %q", d.Options[0].Detail, d.Options[3].Detail)
	}

	d2, _ := parseDialog(dialogFixture(t, "multi-2"))
	if !d2.Options[0].Checked || d2.Options[1].Checked || !d2.Options[2].Checked || d.signature() == d2.signature() {
		t.Errorf("체크 = %+v", d2.Options)
	}
	d3, _ := parseDialog(dialogFixture(t, "multi-3"))
	if !d3.OnSubmit || d3.Current != 0 {
		t.Errorf("Submit 줄의 커서 = %+v", d3)
	}
	dt, _ := parseDialog(dialogFixture(t, "multi-typed"))
	if dt.Current != 3 || dt.Options[2].Label != "로그 보강" || !dt.Options[2].Checked {
		t.Errorf("직접 쓴 칸 = %+v", dt.Options)
	}
	ds, ok := parseDialog(dialogFixture(t, "multi-submit"))
	if !ok || ds.Multi || strings.Join(optionLabels(ds), "|") != "Submit answers|Cancel" {
		t.Errorf("Submit 탭 = %+v", ds)
	}
}

// multiFake 는 측정한 여러 개 고르기의 키 동작을 흉내 낸다. 화면은 multi-1 픽스처와 같은 모양으로 그린다.
// 직접 쓰기 칸(4)은 freeSlot 이 흉내 낸다 — 커서가 그 칸에 있으면 숫자도 글자로 들어가고 체크가 켜진다(실측).
type multiFake struct {
	cursor  int // 1..4, 0 은 Submit 줄
	checked map[int]bool
	free    freeSlot
	done    bool
}

var multiLabels = []string{"중단", "재시도", "알림", "Type something"}

func (m *multiFake) screen() string {
	if m.done {
		return "● 앞 대화\n" + strings.Repeat("─", 60) + "\n←  ☒ 보강  ✔ Submit  →\n\nReview your answers\n\n❯ 1. Submit answers\n  2. Cancel\n"
	}
	var b strings.Builder
	b.WriteString("● 앞 대화\n" + strings.Repeat("─", 60) + "\n←  ☐ 보강  ✔ Submit  →\n\n무엇을 보강할까요?(여러 개)\n\n")
	order := []int{1, 2, 3, 4, 0}
	for _, n := range order {
		mark := "  "
		if m.cursor == n {
			mark = "❯ "
		}
		if n == 0 {
			b.WriteString(mark + "   Submit\n")
			continue
		}
		box := "[ ]"
		if m.checked[n] {
			box = "[✔]"
		}
		label := multiLabels[n-1]
		if n == 4 && len(m.free.text) > 0 {
			label = string(m.free.text)
		}
		fmt.Fprintf(&b, "%s%d. %s %s\n", mark, n, box, label)
		if n < 4 {
			b.WriteString("         설명입니다.\n")
		}
	}
	b.WriteString(strings.Repeat("─", 60) + "\n  5. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n")
	return b.String()
}

func (m *multiFake) key(f *fakeDialogPane) func([]string) {
	pos := []int{1, 2, 3, 4, 0}
	move := func(d int) {
		i := 0
		for j, p := range pos {
			if p == m.cursor {
				i = j
			}
		}
		i = min(max(i+d, 0), len(pos)-1)
		m.cursor = pos[i]
		if m.cursor == 4 {
			m.free.enter()
		}
	}
	press := func(k string) {
		switch {
		case m.cursor == 4 && m.free.key(k):
			// 숫자는 칸에 글자로 들어가며 체크를 켜고, 글을 다 지우면 체크가 꺼진다(실측).
			if k != "End" && k != "BSpace" {
				m.checked[4] = true
			} else if len(m.free.text) == 0 && k == "BSpace" {
				m.checked[4] = false
			}
		case len(k) == 1 && k >= "1" && k <= "4":
			n := int(k[0] - '0')
			m.checked[n] = !m.checked[n]
		case k == "Up":
			move(-1)
		case k == "Down":
			move(1)
		case k == "Enter" && m.cursor == 0:
			m.done = true
		}
	}
	return func(keys []string) {
		runFakeKeys(keys, press, func(text string) {
			if m.cursor == 4 {
				m.free.typeText(text)
				m.checked[4] = true
			}
		})
		f.screen = m.screen()
	}
}

// freeSlot 은 직접 쓰기 칸의 글과 글 커서다(실측 2026-11-17): 칸에 들어오면 글 커서가 맨 앞이고, 숫자도 글자로
// 들어가며, End 로 끝에 간 뒤 BSpace 로 지운다. 빈 칸의 BSpace 는 아무것도 안 한다.
type freeSlot struct {
	text   []rune
	at     int
	mangle bool // 친 글의 마지막 글자를 잃는다 — 칸에 보낸 글과 다르게 보이는 경우
}

func (s *freeSlot) enter() { s.at = 0 }

func (s *freeSlot) insert(text string) {
	r := []rune(text)
	s.text = append(append(append([]rune{}, s.text[:s.at]...), r...), s.text[s.at:]...)
	s.at += len(r)
}

// key 는 칸에 커서가 있을 때 칸이 먹는 키를 처리한다. 먹지 않는 키(화살표, Enter)면 false.
func (s *freeSlot) key(k string) bool {
	switch {
	case len(k) == 1 && k >= "0" && k <= "9":
		s.insert(k)
	case k == "End":
		s.at = len(s.text)
	case k == "BSpace":
		if s.at > 0 {
			s.text = append(append([]rune{}, s.text[:s.at-1]...), s.text[s.at:]...)
			s.at--
		}
	default:
		return false
	}
	return true
}

func (s *freeSlot) typeText(text string) {
	if s.mangle {
		r := []rune(text)
		text = string(r[:len(r)-1])
	}
	s.insert(text)
}

// runFakeKeys 는 send-keys 의 인자(-t 뒤)를 키 하나씩 press 로, -l 글은 literal 로 나눈다.
func runFakeKeys(keys []string, press func(string), literal func(string)) {
	switch {
	case keys[0] == "-l":
		literal(keys[len(keys)-1])
	case keys[0] == "-N":
		count, _ := strconv.Atoi(keys[1])
		for range count {
			press(keys[2])
		}
	default:
		for _, k := range keys {
			press(k)
		}
	}
}

func newMultiPane(m *multiFake) *fakeDialogPane {
	f := &fakeDialogPane{}
	f.screen = m.screen()
	f.keys = m.key(f)
	return f
}

func choices(checked ...bool) []MultiChoice {
	out := make([]MultiChoice, len(checked))
	for i, c := range checked {
		out[i] = MultiChoice{N: i + 1, Label: multiLabels[i], Checked: c}
	}
	return out
}

func TestAnswerMultiTogglesOnlyWhatDiffersThenSubmits(t *testing.T) {
	m := &multiFake{cursor: 1, checked: map[int]bool{3: true}}
	f := newMultiPane(m)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		// 중단·알림을 원한다. 알림은 이미 켜져 있으니 누르지 않는다.
		if err := AnswerMulti(a, key, choices(true, false, true, false), ""); err != nil {
			t.Fatal(err)
		}
	})
	want := "send-keys -t %5 1\nsend-keys -t %5 -N 4 Down\nsend-keys -t %5 Enter"
	if strings.Join(f.runs, "\n") != want || !m.done || !m.checked[1] || !m.checked[3] {
		t.Errorf("보낸 키:\n%s\nwant:\n%s\nstate=%+v", strings.Join(f.runs, "\n"), want, m)
	}
}

func TestAnswerMultiTypesTheFreeTextWithTheCursorOnIt(t *testing.T) {
	m := &multiFake{cursor: 1, checked: map[int]bool{}}
	f := newMultiPane(m)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, choices(false, false, false, true), "로그 보강"); err != nil {
			t.Fatal(err)
		}
	})
	want := "send-keys -t %5 -N 3 Down\nsend-keys -t %5 -l -- 로그 보강\nsend-keys -t %5 -N 1 Down\nsend-keys -t %5 Enter"
	if strings.Join(f.runs, "\n") != want || string(m.free.text) != "로그 보강" || !m.done {
		t.Errorf("보낸 키:\n%s\nwant:\n%s", strings.Join(f.runs, "\n"), want)
	}
}

func TestAnswerMultiRefuses(t *testing.T) {
	tests := []struct {
		name   string
		screen func() *fakeDialogPane
		want   []MultiChoice
		text   string
		err    error
	}{
		{"폰이 본 글과 다르다", func() *fakeDialogPane { return newMultiPane(&multiFake{cursor: 1, checked: map[int]bool{}}) },
			[]MultiChoice{{1, "중단", true}, {2, "재시도", false}, {3, "알람", false}, {4, "Type something", false}}, "", ErrDialogChanged},
		{"선택지 수가 다르다", func() *fakeDialogPane { return newMultiPane(&multiFake{cursor: 1, checked: map[int]bool{}}) },
			choices(true, false, true), "", ErrDialogChanged},
		{"여러 개 고르기가 아니다", func() *fakeDialogPane { return &fakeDialogPane{screen: dialogFixture(t, "question-1")} },
			choices(true, false, false, false), "", ErrDialogChanged},
		{"직접 쓰기를 골랐는데 글이 없다", func() *fakeDialogPane { return newMultiPane(&multiFake{cursor: 1, checked: map[int]bool{}}) },
			choices(false, false, false, true), "", ErrAnswerText},
		{"글은 있는데 직접 쓰기를 안 골랐다", func() *fakeDialogPane { return newMultiPane(&multiFake{cursor: 1, checked: map[int]bool{}}) },
			choices(true, false, false, false), "글", ErrAnswerText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.screen()
			key := dialogKey(t, f.screen)
			withDialogPane(t, "waiting", f, func(a Agent) {
				if err := AnswerMulti(a, key, tt.want, tt.text); !errors.Is(err, tt.err) {
					t.Errorf("err = %v, want %v", err, tt.err)
				}
			})
			if len(f.runs) != 0 {
				t.Errorf("아무것도 누르지 말아야 하는데 %v", f.runs)
			}
		})
	}
	// 여러 개 고르기에 숫자키 하나로 답하면 체크만 바뀌고 «답했다» 고 잘못 말한다 — 한 개 답하기는 거절한다.
	f := newMultiPane(&multiFake{cursor: 1, checked: map[int]bool{}})
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, dialogKey(t, f.screen), 1, "중단", ""); !errors.Is(err, ErrDialogChanged) || len(f.runs) != 0 {
			t.Errorf("err = %v, runs = %v", err, f.runs)
		}
	})
}

// ── 직접 쓰기 칸 위의 커서, 폰이 본 대화상자 (2026-11-17 리뷰) ─────────────────
// 커서가 직접 쓰기 칸에 있으면 숫자키도 그 칸에 글자로 들어간다(실측: «❯ 3. 1», 여러 개 고르기는 «[✔] 1»).
// 번호와 글만 맞춰 보면 그사이 뜬 다음 권한 확인의 «1. Yes» 를 누른다.

// singleFake 는 측정한 한 개 고르기(질문 하나)의 키 동작을 흉내 낸다. 3 이 직접 쓰기 칸, 4 가 «Chat about this».
type singleFake struct {
	cursor   int // 1..4
	free     freeSlot
	answered string // 고른 답 — 정해지면 대화상자가 닫힌다
}

var singleLabels = []string{"사과", "배", "Type something.", "Chat about this"}

func (s *singleFake) screen() string {
	if s.answered != "" {
		return (&fakePane{}).screen()
	}
	var b strings.Builder
	b.WriteString("● 앞 대화\n" + strings.Repeat("─", 60) + "\n ☐ 가\n고르세요\n")
	for n := 1; n <= 4; n++ {
		if n == 4 {
			b.WriteString(strings.Repeat("─", 60) + "\n")
		}
		mark := "  "
		if s.cursor == n {
			mark = "❯ "
		}
		label := singleLabels[n-1]
		if n == 3 && len(s.free.text) > 0 {
			label = string(s.free.text)
		}
		fmt.Fprintf(&b, "%s%d. %s\n", mark, n, label)
		if n < 3 {
			b.WriteString("     설명입니다.\n")
		}
	}
	b.WriteString("Enter to select · ↑/↓ to navigate · Esc to cancel\n")
	return b.String()
}

func (s *singleFake) key(f *fakeDialogPane) func([]string) {
	move := func(d int) {
		s.cursor = min(max(s.cursor+d, 1), 4)
		if s.cursor == 3 {
			s.free.enter()
		}
	}
	press := func(k string) {
		switch {
		case s.cursor == 3 && s.free.key(k):
		case len(k) == 1 && k >= "1" && k <= "4":
			if n := int(k[0] - '0'); n == 3 {
				move(3 - s.cursor) // 직접 쓰기 칸의 숫자는 커서만 옮긴다
			} else {
				s.answered = singleLabels[n-1]
			}
		case k == "Up":
			move(-1)
		case k == "Down":
			move(1)
		case k == "Enter" && s.cursor == 3:
			if len(s.free.text) > 0 {
				s.answered = string(s.free.text)
			}
		case k == "Enter":
			s.answered = singleLabels[s.cursor-1]
		}
	}
	return func(keys []string) {
		runFakeKeys(keys, press, func(text string) {
			if s.cursor == 3 {
				s.free.typeText(text)
			}
		})
		f.screen = s.screen()
	}
}

func newSinglePane(s *singleFake) *fakeDialogPane {
	f := &fakeDialogPane{}
	f.screen = s.screen()
	f.keys = s.key(f)
	return f
}

func TestParseDialogFindsTheFreeSlotByPlace(t *testing.T) {
	// 칸에 글이 들면 «Type something» 이 사라진다 — 선택지를 가르는 «─» 선 바로 위 번호라서 직접 쓰기 칸이다.
	s := &singleFake{cursor: 3, free: freeSlot{text: []rune("1")}}
	d, ok := parseDialog(s.screen())
	if !ok || !d.Options[2].FreeText || d.Options[2].Draft() != "1" || d.Options[3].FreeText || d.Options[0].FreeText {
		t.Errorf("글이 든 직접 쓰기 칸 = %+v", d.Options)
	}
	dt, _ := parseDialog(dialogFixture(t, "multi-typed"))
	if !dt.Options[2].FreeText || dt.Options[2].Draft() != "로그 보강" || dt.Options[0].FreeText {
		t.Errorf("여러 개 고르기의 글이 든 칸 = %+v", dt.Options)
	}
	q1, _ := parseDialog(dialogFixture(t, "question-1"))
	if q1.Options[2].Draft() != "" || q1.Options[0].Draft() != "" {
		t.Errorf("빈 칸·보통 선택지의 Draft = %q %q", q1.Options[2].Draft(), q1.Options[0].Draft())
	}
	// 권한 확인과 Submit 탭에는 선택지 사이에 선이 없다 — 직접 쓰기 칸이 없다.
	for _, name := range []string{"permission", "question-submit"} {
		d, _ := parseDialog(dialogFixture(t, name))
		for _, o := range d.Options {
			if o.FreeText {
				t.Errorf("%s 의 %d «%s» 를 직접 쓰기로 읽었다", name, o.N, o.Label)
			}
		}
	}
}

func TestDialogKeyIgnoresTheCursorButNotTheContent(t *testing.T) {
	q2 := dialogFixture(t, "question-2")
	moved := strings.Replace(strings.Replace(q2, "❯ 1. 5분", "  1. 5분", 1), "  2. 10분", "❯ 2. 10분", 1)
	if dialogKey(t, q2) != dialogKey(t, moved) {
		t.Error("커서만 옮겼는데 다른 대화상자가 됐다")
	}
	m1 := &multiFake{cursor: 1, checked: map[int]bool{}}
	m2 := &multiFake{cursor: 0, checked: map[int]bool{1: true, 3: true}}
	if dialogKey(t, m1.screen()) != dialogKey(t, m2.screen()) {
		t.Error("체크와 커서만 바뀌었는데 다른 대화상자가 됐다")
	}
	perm := dialogFixture(t, "permission")
	other := otherPermission(t)
	if dialogKey(t, perm) == dialogKey(t, other) || dialogKey(t, dialogFixture(t, "question-1")) == dialogKey(t, q2) {
		t.Error("다른 대화상자인데 열쇠가 같다")
	}
	drafted := &singleFake{cursor: 3, free: freeSlot{text: []rune("1")}}
	if dialogKey(t, (&singleFake{cursor: 3}).screen()) == dialogKey(t, drafted.screen()) {
		t.Error("직접 쓰기 칸에 글이 들었는데 열쇠가 같다")
	}
}

func TestAnswerDialogLeavesTheFreeSlotBeforeTheDigit(t *testing.T) {
	// 리뷰에서 가짜 Claude 로 재현: 화면이 «❯ 3. Type something.» 일 때 1 을 누르면 «❯ 3. 1» 이 되고 «답했다» 고 했다.
	for _, draft := range []string{"", "1"} {
		s := &singleFake{cursor: 3, free: freeSlot{text: []rune(draft)}}
		f := newSinglePane(s)
		key := dialogKey(t, f.screen)
		withDialogPane(t, "waiting", f, func(a Agent) {
			if err := AnswerDialog(a, key, 1, "사과", ""); err != nil {
				t.Fatalf("칸의 글 %q: %v", draft, err)
			}
		})
		if got := strings.Join(f.runs, "\n"); got != "send-keys -t %5 Up\nsend-keys -t %5 1" || s.answered != "사과" {
			t.Errorf("칸의 글 %q: 보낸 키 %q, 답 %q", draft, got, s.answered)
		}
	}
}

func TestAnswerDialogFreeTextReplacesTheDraftThePhoneSaw(t *testing.T) {
	// 칸에 «1» 이 남아 있고 커서도 그 칸이다. 폰은 그 글을 보고 직접 쓰기를 골랐다 — 숫자는 누르지 않고(글자가 된다),
	// End 로 끝에 가서 지운 뒤 친다(다시 들어온 칸은 글 커서가 맨 앞이라 BSpace 만으로는 안 지워진다).
	s := &singleFake{cursor: 3, free: freeSlot{text: []rune("1")}}
	f := newSinglePane(s)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, key, 3, "1", "보라"); err != nil {
			t.Fatal(err)
		}
	})
	want := "send-keys -t %5 End\nsend-keys -t %5 -N 2 BSpace\nsend-keys -t %5 -l -- 보라\nsend-keys -t %5 Enter"
	if got := strings.Join(f.runs, "\n"); got != want || s.answered != "보라" {
		t.Errorf("보낸 키:\n%s\nwant:\n%s\n답 %q", got, want, s.answered)
	}

	// 폰은 빈 칸을 봤는데 그사이 PC 에서 글을 썼다 — 남의 글을 지우지 않는다.
	s = &singleFake{cursor: 1}
	key = dialogKey(t, s.screen())
	s.free.text = []rune("PC 에서 쓰던 글")
	f = newSinglePane(s)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerDialog(a, key, 3, "Type something.", "보라"); !errors.Is(err, ErrDialogChanged) || len(f.runs) != 0 {
			t.Errorf("err = %v, runs = %v", err, f.runs)
		}
	})
}

func TestAnswerDialogFreeTextUndoesWhatItTyped(t *testing.T) {
	// 친 글이 칸에 다르게 보인다 — Enter 를 누르지 않고, 친 만큼 지워 칸을 비운다.
	s := &singleFake{cursor: 1, free: freeSlot{mangle: true}}
	f := newSinglePane(s)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		err := AnswerDialog(a, key, 3, "Type something.", "보라색")
		if !errors.Is(err, ErrAnswerUnknown) || !strings.Contains(err.Error(), "지웠다") {
			t.Errorf("err = %v", err)
		}
	})
	if len(s.free.text) != 0 || s.answered != "" || strings.Contains(strings.Join(f.runs, "\n"), "Enter") {
		t.Errorf("칸 %q, 답 %q, 보낸 키 %v", string(s.free.text), s.answered, f.runs)
	}
}

// otherPermission 은 실측 권한 확인에서 대화상자 안의 명령 줄만 바꾼 화면이다(위 대화 기록의 같은 글은 그대로).
func otherPermission(t *testing.T) string {
	t.Helper()
	screen := dialogFixture(t, "permission")
	const line = "\n date > probe2.txt\n"
	if strings.Count(screen, line) != 1 {
		t.Fatalf("권한 확인의 명령 줄을 못 찾았다")
	}
	return strings.Replace(screen, line, "\n rm -rf dist\n", 1)
}

func TestAnswerRefusesADialogThePhoneDidNotSee(t *testing.T) {
	// 폰은 «date > probe2.txt» 권한 확인의 1. Yes 를 골랐는데, 그사이 PC 에서 답했고 «rm -rf dist» 권한 확인이 떴다.
	// 번호와 글은 같다 — 열쇠가 다르니 누르지 않는다. Esc 도 마찬가지다(보지 않은 실행을 거절하지 않는다).
	seen := dialogKey(t, dialogFixture(t, "permission"))
	other := otherPermission(t)
	for _, key := range []string{seen, ""} {
		f := &fakeDialogPane{screen: other}
		withDialogPane(t, "waiting", f, func(a Agent) {
			if err := AnswerDialog(a, key, 1, "Yes", ""); !errors.Is(err, ErrDialogChanged) {
				t.Errorf("답하기 열쇠 %q: err = %v", key, err)
			}
			if err := CancelDialog(a, key); !errors.Is(err, ErrDialogChanged) {
				t.Errorf("Esc 열쇠 %q: err = %v", key, err)
			}
		})
		if len(f.runs) != 0 {
			t.Errorf("열쇠 %q: 아무것도 누르지 말아야 하는데 %v", key, f.runs)
		}
	}
	m := &multiFake{cursor: 1, checked: map[int]bool{}}
	key := dialogKey(t, strings.Replace(m.screen(), "무엇을 보강할까요?(여러 개)", "무엇을 지울까요?(여러 개)", 1))
	f := newMultiPane(m)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, choices(true, false, false, false), ""); !errors.Is(err, ErrDialogChanged) || len(f.runs) != 0 {
			t.Errorf("여러 개 고르기: err = %v, runs = %v", err, f.runs)
		}
	})
}

func TestAnswerMultiLeavesTheFreeBoxBeforeToggling(t *testing.T) {
	// 커서가 직접 쓰기 칸에 있다 — 1 을 누르면 칸에 «1» 이 들어가고 체크된다(실측). 먼저 한 칸 위로 옮긴다.
	m := &multiFake{cursor: 4, checked: map[int]bool{}}
	f := newMultiPane(m)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, choices(true, false, false, false), ""); err != nil {
			t.Fatal(err)
		}
	})
	want := "send-keys -t %5 Up\nsend-keys -t %5 1\nsend-keys -t %5 -N 2 Down\nsend-keys -t %5 Enter"
	if got := strings.Join(f.runs, "\n"); got != want || len(m.free.text) != 0 || !m.checked[1] || m.checked[4] || !m.done {
		t.Errorf("보낸 키:\n%s\nwant:\n%s\nstate=%+v", got, want, m)
	}
}

func TestAnswerMultiReplacesOrDropsTheDraft(t *testing.T) {
	// 리뷰의 재현 화면 «❯ 4. [✔] 1» — 폰은 그 글을 봤다. 직접 쓰기를 바라면 지우고 다시 친다.
	m := &multiFake{cursor: 4, checked: map[int]bool{4: true}, free: freeSlot{text: []rune("1"), at: 1}}
	f := newMultiPane(m)
	key := dialogKey(t, f.screen)
	want := []MultiChoice{{1, "중단", true}, {2, "재시도", false}, {3, "알림", false}, {4, "1", true}}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, want, "로그"); err != nil {
			t.Fatal(err)
		}
	})
	keys := "send-keys -t %5 Up\nsend-keys -t %5 1\nsend-keys -t %5 -N 1 Down\nsend-keys -t %5 End\nsend-keys -t %5 -N 2 BSpace\n" +
		"send-keys -t %5 -l -- 로그\nsend-keys -t %5 -N 1 Down\nsend-keys -t %5 Enter"
	if got := strings.Join(f.runs, "\n"); got != keys || string(m.free.text) != "로그" || !m.checked[1] || !m.done {
		t.Errorf("보낸 키:\n%s\nwant:\n%s\nstate=%+v", got, keys, m)
	}

	// 바라지 않으면 체크만 끈다 — 체크를 끈 칸의 글은 제출되지 않는다(실측).
	m = &multiFake{cursor: 1, checked: map[int]bool{4: true}, free: freeSlot{text: []rune("abc")}}
	f = newMultiPane(m)
	key = dialogKey(t, f.screen)
	want = []MultiChoice{{1, "중단", true}, {2, "재시도", false}, {3, "알림", false}, {4, "abc", false}}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, want, ""); err != nil {
			t.Fatal(err)
		}
	})
	keys = "send-keys -t %5 1\nsend-keys -t %5 4\nsend-keys -t %5 -N 4 Down\nsend-keys -t %5 Enter"
	if got := strings.Join(f.runs, "\n"); got != keys || m.checked[4] || !m.done {
		t.Errorf("보낸 키:\n%s\nwant:\n%s\nstate=%+v", got, keys, m)
	}
}

func TestAnswerMultiStopsBeforeSubmit(t *testing.T) {
	// 친 글이 칸에 다르게 보인다 — 친 만큼 지우고 Submit 은 누르지 않는다.
	m := &multiFake{cursor: 1, checked: map[int]bool{}, free: freeSlot{mangle: true}}
	f := newMultiPane(m)
	key := dialogKey(t, f.screen)
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, choices(false, false, false, true), "로그 보강"); !errors.Is(err, ErrAnswerUnknown) {
			t.Errorf("err = %v", err)
		}
	})
	if len(m.free.text) != 0 || m.checked[4] || m.done || strings.Contains(strings.Join(f.runs, "\n"), "Enter") {
		t.Errorf("칸 %q, state=%+v, 보낸 키 %v", string(m.free.text), m, f.runs)
	}

	// Submit 줄로 가는 사이 체크가 바뀌었다(PC 에서 누름) — Enter 직전에 한 번 더 보고 누르지 않는다.
	m = &multiFake{cursor: 1, checked: map[int]bool{}}
	f = newMultiPane(m)
	key = dialogKey(t, f.screen)
	inner := f.keys
	f.keys = func(keys []string) {
		inner(keys)
		if keys[0] == "-N" && keys[2] == "Down" {
			m.checked[1] = false
			f.screen = m.screen()
		}
	}
	withDialogPane(t, "waiting", f, func(a Agent) {
		if err := AnswerMulti(a, key, choices(true, false, false, false), ""); !errors.Is(err, ErrAnswerUnknown) {
			t.Errorf("err = %v", err)
		}
	})
	if m.done || strings.Contains(strings.Join(f.runs, "\n"), "Enter") {
		t.Errorf("체크가 어긋났는데 Submit 했다: %v", f.runs)
	}
}
