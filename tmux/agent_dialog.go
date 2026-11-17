package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 대화상자 — Claude 가 사람의 답을 기다리며 띄운 번호 선택지: AskUserQuestion 의 질문(과 그 Submit 탭), 권한 확인.
// mux serve 가 폰에서 답할 때 쓴다(docs/design/serve.md «대화상자에 답하기»).
//
// 실측(2026-11-17, Claude Code 2.1.312, 격리 tmux 서버의 haiku — 화면은 testdata/dialog):
//   - 질문과 권한 확인 모두 «❯ 1. 글» 같은 번호 목록이고 ❯ 가 커서다. 권한 확인은 한 칸 들여 쓰고, 긴 선택지는
//     더 들여 쓴 다음 줄로 접힌다. 질문의 선택지 아래 줄은 설명이다.
//   - 숫자키 하나가 그 선택지를 고르고 바로 확정한다. 질문이 여럿이면 다음 질문 탭으로, 마지막 질문 뒤에는
//     Submit 탭(검토, «1. Submit answers / 2. Cancel»)으로 간다 — 마지막 질문에서 눌러도 바로 제출되지 않는다.
//   - «Type something.» 에서는 숫자키가 커서만 옮긴다. 그대로 글을 치면 그 자리에 보이고 Enter 로 확정된다.
//   - 상태 파일은 waiting 이다(질문은 waitingFor "input needed", 권한은 "permission prompt"). Esc 는 대화상자를 거둔다
//     (권한은 거절, 질문은 «User declined to answer questions»).
//   - 폴더 신뢰처럼 번호 없는 메뉴도 있다. 숫자키로 답할 수 없어 대화상자로 내지 않는다.
//   - 커서가 직접 쓰기 칸에 있으면 숫자키도 그 칸에 글자로 들어간다(«❯ 3. 1», 여러 개 고르기는 «[✔] 1» 로 체크까지).
//     화살표로 한 칸 옮기면 숫자키가 다시 고르기다. 칸에 글이 들면 «Type something» 이 사라지므로 칸은 글이 아니라
//     자리로 안다 — 질문의 선택지 덩어리를 가르는 «─» 선 바로 위 번호(«Chat about this» 앞)다.
//   - 칸을 나갔다 들어오면 글 커서가 맨 앞이라 BSpace 가 먹지 않는다. End 뒤 BSpace 로 지운다. 빈 칸의 BSpace 는
//     아무것도 하지 않고, 여러 개 고르기에서 글을 다 지우면 체크도 꺼진다. 체크를 끈 칸의 글은 제출되지 않는다.
//
// 낡은 화면을 보고 숫자를 누르면 엉뚱한 답이 들어간다 — 측정하다 Submit 탭이 떠 있는 줄 모르고 2 를 눌러 질문 전체가
// 취소됐다. 그래서 누르기 직전에 화면을 다시 읽어, 폰이 본 대화상자(Key)와 선택지(번호와 글)일 때만 누른다. 번호와
// 글만으로는 모자란다 — 권한 확인은 어느 것이나 «1. Yes» 라, 그사이 다음 권한 확인이 뜨면 보지 않은 실행을 승인한다.

// DialogKind 는 대화상자의 종류다.
type DialogKind string

const (
	DialogQuestion   DialogKind = "question"   // AskUserQuestion (Submit 탭 포함)
	DialogPermission DialogKind = "permission" // 도구 실행 허락
	DialogOther      DialogKind = "other"      // 번호 선택지이지만 둘 다 아닌 것
)

// DialogTab 은 질문이 여럿일 때 맨 위 탭 하나다(«☐ 방식», «☒ 주기», «✔ Submit»).
type DialogTab struct {
	Label  string
	Done   bool // ☒ — 답했다
	Submit bool // 마지막의 Submit 탭
}

// DialogOption 은 번호 선택지 하나다.
type DialogOption struct {
	N      int
	Label  string
	Detail string // 아래 들여 쓴 줄들(질문의 설명, 권한 선택지의 접힌 뒷부분)
	// Chosen 은 앞서 고른 답이다(라벨 끝의 ✔).
	Chosen bool
	// FreeText 는 직접 쓰기 칸(«Type something.») — 숫자키가 커서만 옮기고, 글을 쳐서 Enter 로 답한다. 여러 개
	// 고르기에서는 «[ ] Type something»(마침표 없음)이고 숫자키는 체크만 한다. 칸에 글이 들면 라벨이 그 글이 되므로
	// 자리로도 안다(dialogOptions). 쓰다 만 글은 Draft.
	FreeText bool
	// Checkbox 는 여러 개 고르기의 «[ ] 글» 선택지다. Checked 는 «[✔]».
	Checkbox bool
	Checked  bool
}

// Dialog 는 화면에서 읽은 대화상자 하나다.
type Dialog struct {
	Kind DialogKind
	Tabs []DialogTab
	// Prompt 는 시작 선과 첫 선택지 사이의 글이다(질문, 실행할 명령, 검토). 공통 들여쓰기만 걷고 빈 줄과 점선은 뺀다.
	Prompt  []string
	Options []DialogOption
	Current int    // ❯ 가 있는 선택지 번호. 모르거나 Submit 줄이면 0
	Hint    string // 맨 아래 안내 줄(«Esc to cancel» 이 든 줄). 없을 수 있다
	// Multi 는 여러 개 고르기(체크박스)다. 숫자키는 체크만 켜고 끄고, 번호 없는 «Submit» 줄(SubmitLine)에서
	// Enter 를 눌러야 넘어간다. OnSubmit 은 커서가 그 줄에 있다는 뜻이다.
	Multi      bool
	SubmitLine bool
	OnSubmit   bool
}

// MultiChoice 는 여러 개 고르기에서 폰이 본 선택지 하나와 바라는 체크다.
type MultiChoice struct {
	N       int
	Label   string
	Checked bool
}

// 대화상자 오류 — mux serve 가 errors.Is 로 화면 문구를 고른다.
var (
	ErrNoDialog      = errors.New("답을 기다리는 대화상자가 없다")
	ErrDialogChanged = errors.New("대화상자가 바뀌었다 — 아무것도 누르지 않았다")
	ErrAnswerUnknown = errors.New("답이 들어갔는지 알 수 없다 — 화면을 확인하라")
	// ErrAnswerText 는 직접 쓰기 글이 보낼 수 없는 글이라는 뜻이다(ValidatePromptText). 아무것도 누르지 않았다.
	ErrAnswerText = errors.New("직접 쓰기 글을 보낼 수 없다")
)

const (
	// dialogSettle 은 키를 누른 뒤 화면을 처음 읽기까지 기다리는 시간이다. 숫자키는 바로 반영됐다(실측).
	dialogSettle = 300 * time.Millisecond
	// dialogMoveWait 은 키를 누른 뒤 대화상자가 넘어가기를 기다리는 상한이다.
	dialogMoveWait = 3 * time.Second
	// dialogTypeWait 은 직접 쓰기 칸이 커서를 받거나 친 글을 보이기까지 기다리는 상한이다.
	dialogTypeWait = 2 * time.Second
	// dialogFreeText 는 직접 쓰기 선택지의 라벨이다(실측).
	dialogFreeText = "Type something."
)

var (
	dialogOptionRe = regexp.MustCompile(`^(\s*)(?:❯\s+)?(\d+)\.\s+(.*?)\s*$`)
	dialogRuleRe   = regexp.MustCompile(`^\s*─{10,}\s*$`)
	dialogDashRe   = regexp.MustCompile(`^\s*╌{10,}\s*$`)
	dialogTabsRe   = regexp.MustCompile(`^\s*←\s+(.*?)\s+→\s*$`)
	dialogTabSepRe = regexp.MustCompile(`\s{2,}`)
	dialogCheckRe  = regexp.MustCompile(`^\[( |✔|x|X)\]\s+(.*)$`)
)

// parseDialog 는 pane 화면에서 대화상자를 읽는다. 없으면 false.
//
// 아래에서 위로 번호 목록의 마지막 덩어리(1 부터 이어지는 번호)를 찾고, 그 위의 가장 가까운 «─» 선을 시작으로
// 본다(권한 확인 안의 «╌» 점선은 시작이 아니다). 시작 선 바로 아래에 질문 탭 줄이 있거나, 선택지 아래에
// «Esc to cancel» 안내 줄이 있어야 대화상자다 — 사람이 입력창에 친 «1. …» 이나 회신 속 번호 목록을 걸러 낸다.
// Submit 탭에는 안내 줄이 없다(빈 줄이 남는 화면에서도, 실측) — 탭 줄로 안다.
func parseDialog(screen string) (Dialog, bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	first, last, ok := dialogOptionBlock(lines)
	if !ok {
		return Dialog{}, false
	}
	start := -1
	for i := first - 1; i >= 0; i-- {
		if dialogRuleRe.MatchString(lines[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return Dialog{}, false
	}

	d := Dialog{}
	end := len(lines)
	for i := last + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "Esc to cancel") {
			d.Hint = strings.TrimSpace(lines[i])
			end = i
			break
		}
	}
	var body []string
	for i := start + 1; i < first; i++ {
		if len(d.Tabs) == 0 && len(body) == 0 && dialogTabsRe.MatchString(lines[i]) {
			d.Tabs = parseDialogTabs(lines[i])
			continue
		}
		if strings.TrimSpace(lines[i]) == "" || dialogDashRe.MatchString(lines[i]) {
			continue
		}
		body = append(body, strings.TrimRight(lines[i], " "))
	}
	if len(d.Tabs) == 0 && d.Hint == "" {
		return Dialog{}, false
	}
	d.Prompt = dedent(body)
	dialogOptions(&d, lines[first:end])
	d.Kind = dialogKind(d)
	return d, true
}

// dialogOptionBlock 은 1 부터 이어지는 번호 목록 중 화면 맨 아래 것의 첫 줄과 마지막 선택지 줄을 찾는다.
func dialogOptionBlock(lines []string) (first, last int, ok bool) {
	last = -1
	want := 0
	for i := len(lines) - 1; i >= 0; i-- {
		m := dialogOptionRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if last < 0 {
			last, want = i, n
		}
		if n != want {
			// 이어지지 않는 번호다 — 이 덩어리는 대화상자가 아니다(맨 아래 덩어리만 본다).
			return 0, 0, false
		}
		if n == 1 {
			return i, last, true
		}
		want--
	}
	return 0, 0, false
}

// dialogOptions 는 선택지 줄들을 읽는다. 번호 줄 아래의 들여 쓴 줄은 그 선택지의 설명(또는 접힌 뒷부분)이다.
// 여러 개 고르기의 번호 없는 «Submit» 줄은 설명이 아니라 따로 센다.
//
// 직접 쓰기 칸은 «─» 선이 선택지 덩어리를 가르는 바로 위 번호다(질문만 그렇다 — 권한 확인과 Submit 탭에는 선택지
// 사이에 선이 없다, 실측). 여러 개 고르기는 마지막 체크박스이기도 하다. 라벨이 «Type something» 인 것도 그렇다.
func dialogOptions(d *Dialog, lines []string) {
	split := -1 // 선 위의 선택지 자리 — 선 아래에 선택지가 또 나오면 직접 쓰기 칸이다
	for _, line := range lines {
		if m := dialogOptionRe.FindStringSubmatch(line); m != nil {
			if split >= 0 {
				d.Options[split].FreeText = true
				split = -1
			}
			n, _ := strconv.Atoi(m[2])
			label := m[3]
			o := DialogOption{N: n}
			if c := dialogCheckRe.FindStringSubmatch(label); c != nil {
				o.Checkbox, o.Checked, label = true, c[1] != " ", c[2]
				d.Multi = true
			}
			if strings.HasSuffix(label, " ✔") {
				o.Chosen = true
				label = strings.TrimSuffix(label, " ✔")
			}
			o.Label = strings.TrimSpace(label)
			o.FreeText = o.Label == dialogFreeText || (o.Checkbox && o.Label == strings.TrimSuffix(dialogFreeText, "."))
			if strings.HasPrefix(strings.TrimLeft(line, " "), "❯") {
				d.Current = n
			}
			d.Options = append(d.Options, o)
			continue
		}
		t := strings.TrimSpace(line)
		if d.Multi && strings.TrimSpace(strings.TrimPrefix(t, "❯")) == "Submit" {
			d.SubmitLine = true
			if strings.HasPrefix(t, "❯") {
				d.OnSubmit, d.Current = true, 0
			}
			continue
		}
		if dialogRuleRe.MatchString(line) && len(d.Options) > 0 {
			split = len(d.Options) - 1
			continue
		}
		if t == "" || len(d.Options) == 0 {
			continue
		}
		o := &d.Options[len(d.Options)-1]
		if o.Detail != "" {
			o.Detail += " "
		}
		o.Detail += t
	}
	if boxes := checkboxOptions(*d); len(boxes) > 0 {
		last := boxes[len(boxes)-1].N
		for i := range d.Options {
			if d.Options[i].N == last {
				d.Options[i].FreeText = true
			}
		}
	}
}

func parseDialogTabs(line string) []DialogTab {
	m := dialogTabsRe.FindStringSubmatch(line)
	var tabs []DialogTab
	for _, tok := range dialogTabSepRe.Split(strings.TrimSpace(m[1]), -1) {
		switch {
		case strings.HasPrefix(tok, "☒"):
			tabs = append(tabs, DialogTab{Label: strings.TrimSpace(strings.TrimPrefix(tok, "☒")), Done: true})
		case strings.HasPrefix(tok, "☐"):
			tabs = append(tabs, DialogTab{Label: strings.TrimSpace(strings.TrimPrefix(tok, "☐"))})
		case strings.HasPrefix(tok, "✔"):
			tabs = append(tabs, DialogTab{Label: strings.TrimSpace(strings.TrimPrefix(tok, "✔")), Submit: true})
		case tok != "":
			tabs = append(tabs, DialogTab{Label: tok})
		}
	}
	return tabs
}

func dialogKind(d Dialog) DialogKind {
	prompt := strings.Join(d.Prompt, "\n")
	switch {
	case len(d.Tabs) > 0, strings.Contains(d.Hint, "Enter to select"):
		return DialogQuestion
	case strings.Contains(d.Hint, "Tab to amend"), strings.Contains(prompt, "Do you want to"):
		return DialogPermission
	default:
		return DialogOther
	}
}

// dedent 는 줄들의 공통 들여쓰기를 걷는다. 검토 화면의 «● 질문 / → 답» 같은 상대 들여쓰기는 남는다.
func dedent(lines []string) []string {
	min := -1
	for _, l := range lines {
		n := len(l) - len(strings.TrimLeft(l, " "))
		if min < 0 || n < min {
			min = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l[min:]
	}
	return out
}

// signature 는 대화상자가 넘어갔는지 가리는 값이다 — 탭·글·선택지·커서 중 하나라도 바뀌면 다르다.
func (d Dialog) signature() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%s|%d|%v", d.Kind, d.Tabs, strings.Join(d.Prompt, "\n"), d.Current, d.OnSubmit)
	for _, o := range d.Options {
		fmt.Fprintf(&b, "|%d.%s.%v.%v", o.N, o.Label, o.Chosen, o.Checked)
	}
	return b.String()
}

// Key 는 폰이 본 대화상자를 가리는 열쇠다 — 종류·탭·글·선택지(번호·글·설명·직접 쓰기 칸의 글)가 같으면 같다.
// 커서와 체크는 넣지 않는다: 답하는 동안 우리가 바꾸는 것이고, 바뀐다고 다른 대화상자가 되지 않는다.
func (d Dialog) Key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%s", d.Kind, d.Tabs, strings.Join(d.Prompt, "\n"))
	for _, o := range d.Options {
		fmt.Fprintf(&b, "|%d.%s.%s.%v.%v.%v", o.N, o.Label, o.Detail, o.Chosen, o.Checkbox, o.FreeText)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// Draft 는 직접 쓰기 칸에 쓰다 만 글이다. 비었거나 직접 쓰기 칸이 아니면 "".
func (o DialogOption) Draft() string {
	if !o.FreeText || o.Label == dialogFreeText || o.Label == strings.TrimSuffix(dialogFreeText, ".") {
		return ""
	}
	if o.Detail == "" {
		return o.Label
	}
	return o.Label + " " + o.Detail
}

func (d Dialog) option(n int) (DialogOption, bool) {
	for _, o := range d.Options {
		if o.N == n {
			return o, true
		}
	}
	return DialogOption{}, false
}

func readDialog(pane string) (Dialog, bool) {
	out, err := runner.Output("tmux", "capture-pane", "-p", "-t", pane)
	if err != nil {
		return Dialog{}, false
	}
	return parseDialog(string(out))
}

// ReadDialog 는 대상이 띄운 대화상자를 읽는다. 대화상자에서 막혀 있지 않거나, 번호로 답할 수 있는 대화상자가
// 화면에 없으면 ErrNoDialog 다.
func ReadDialog(a Agent) (Dialog, error) {
	a, err := refreshAgent(a)
	if err != nil {
		return Dialog{}, err
	}
	if a.State != AgentBlocked {
		return Dialog{}, fmt.Errorf("%w: %s 가 %s 상태다", ErrNoDialog, a.PaneID, a.State)
	}
	d, ok := readDialog(a.PaneID)
	if !ok {
		return Dialog{}, fmt.Errorf("%w: %s 의 화면에 번호 선택지가 없다", ErrNoDialog, a.PaneID)
	}
	return d, nil
}

// prepareDialog 는 키를 누르기 전의 확인이다: 막혀 있는가, 키가 Claude 에게 가는가(복사 모드 아님), 대화상자가 보이는가.
func prepareDialog(a Agent) (Dialog, error) {
	a, err := refreshAgent(a)
	if err != nil {
		return Dialog{}, err
	}
	if a.State != AgentBlocked {
		return Dialog{}, fmt.Errorf("%w: %s 가 %s 상태다", ErrNoDialog, a.PaneID, a.State)
	}
	if err := checkPaneMode(a.PaneID, false); err != nil {
		return Dialog{}, err
	}
	d, ok := readDialog(a.PaneID)
	if !ok {
		// 입력창이 보이는데 숫자를 누르면 글자로 들어간다 — 누르지 않는다.
		return Dialog{}, fmt.Errorf("%w: %s 의 화면에 번호 선택지가 없다", ErrNoDialog, a.PaneID)
	}
	return d, nil
}

// AnswerDialog 는 대화상자의 선택지 n 을 고른다. key 와 label 은 폰이 본 대화상자(Dialog.Key)와 그 선택지의 글이다
// — 지금 화면이 그 대화상자가 아니거나 n 번이 그 글이 아니면(그사이 다음 질문·다음 권한 확인이 떴다) 누르지 않고
// ErrDialogChanged 다. 직접 쓰기 칸은 text 를 쳐서 답한다.
//
// 숫자키 하나로 고른다(9 까지). 커서가 직접 쓰기 칸에 있으면 먼저 빼낸다(그 칸에서는 숫자가 글자로 들어간다). 누른 뒤
// 대화상자가 넘어갔는지(닫혔거나, 탭·글·선택지가 바뀌었거나, 상태가 막힘에서 벗어났거나) 확인한다. 그대로면
// ErrAnswerUnknown — 같은 모양의 다음 대화상자일 수도 있어 «안 들어갔다» 고 하지 않는다.
func AnswerDialog(a Agent, key string, n int, label, text string) error {
	if n < 1 || n > 9 {
		return fmt.Errorf("%w: 선택지 %d 는 숫자키 하나로 고를 수 없다", ErrDialogChanged, n)
	}
	unlock, err := lockPane(a.PaneID)
	if err != nil {
		return err
	}
	defer unlock()

	d, err := prepareDialog(a)
	if err != nil {
		return err
	}
	if err := sameDialog(d, key); err != nil {
		return err
	}
	if d.Multi {
		// 숫자키는 체크만 바꾼다 — «답했다» 고 잘못 말하게 된다. 여러 개 고르기는 AnswerMulti 로 답한다.
		return fmt.Errorf("%w: 여러 개 고르기다 — 고를 것을 모두 정해 한 번에 답한다", ErrDialogChanged)
	}
	opt, ok := d.option(n)
	if !ok || opt.Label != label {
		return fmt.Errorf("%w: 폰이 본 %d «%s» 가 지금 화면에는 «%s» 다", ErrDialogChanged, n, label, opt.Label)
	}
	if opt.FreeText {
		return answerFreeText(a, d, opt, text)
	}
	d, err = leaveFreeSlot(a.PaneID, d)
	if err != nil {
		return err
	}
	before := d.signature()
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, strconv.Itoa(n)); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	if dialogMoved(a, before) {
		return nil
	}
	return fmt.Errorf("%w: %s 의 대화상자가 그대로다", ErrAnswerUnknown, a.PaneID)
}

// sameDialog 는 지금 화면이 폰이 본 대화상자인지 본다.
func sameDialog(d Dialog, key string) error {
	if key == "" || d.Key() != key {
		return fmt.Errorf("%w: 폰이 본 대화상자가 아니다(글·선택지가 바뀌었다)", ErrDialogChanged)
	}
	return nil
}

// answerFreeText 는 직접 쓰기 칸에 글을 쳐서 답한다. 커서를 그 칸에 두고 → 쓰다 만 글이 있으면 지우고(폰이 그 글을
// 보고 골랐다) → 칸이 빈 것을 보고 → 글을 한 번 치고 → 칸에 정확히 그 글이 보일 때만 Enter 를 누른다(mux agent
// prompt 와 같은 원칙, agent-api.md). 글이 다르게 보이면 친 만큼 되돌린다.
func answerFreeText(a Agent, d Dialog, opt DialogOption, text string) error {
	if err := ValidatePromptText(text); err != nil {
		return fmt.Errorf("%w: %v", ErrAnswerText, err)
	}
	n := opt.N
	if d.Current != n {
		// 다른 선택지에 있을 때 이 칸의 숫자키는 커서만 옮긴다(실측). 이미 칸에 있으면 누르지 않는다 — 글자가 된다.
		if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, strconv.Itoa(n)); err != nil {
			return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
		}
		if _, ok := waitDialog(a.PaneID, dialogTypeWait, func(d Dialog) bool {
			o, ok := d.option(n)
			return ok && d.Current == n && o.FreeText
		}); !ok {
			return fmt.Errorf("%w: %s 의 직접 쓰기 칸으로 옮겨지지 않았다 — 글은 치지 않았다", ErrAnswerUnknown, a.PaneID)
		}
	}
	if err := clearFreeSlot(a.PaneID, opt); err != nil {
		return err
	}
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "-l", "--", tmuxLiteral(text)); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	want := normalizeBox(text)
	typed, ok := waitDialog(a.PaneID, dialogTypeWait, func(d Dialog) bool {
		o, ok := d.option(n)
		return ok && d.Current == n && normalizeBox(o.Label+o.Detail) == want
	})
	if !ok {
		return undoFreeText(a.PaneID, n, text, "Enter 는 누르지 않았다")
	}
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "Enter"); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAnswerUnknown, err)
	}
	if dialogMoved(a, typed.signature()) {
		return nil
	}
	return fmt.Errorf("%w: %s 의 대화상자가 그대로다", ErrAnswerUnknown, a.PaneID)
}

// leaveFreeSlot 은 커서가 직접 쓰기 칸에 있으면 한 칸 옮긴다(위로, 첫 칸이면 아래로) — 그 칸에서는 숫자키가 고르기가
// 아니라 글자로 들어간다(실측). 칸의 글은 그대로 남는다. 옮긴 뒤의 화면을 돌려준다.
func leaveFreeSlot(pane string, d Dialog) (Dialog, error) {
	slot, ok := d.option(d.Current)
	if !ok || !slot.FreeText {
		return d, nil
	}
	key := "Up"
	if slot.N == 1 {
		key = "Down"
	}
	if err := runner.Run("tmux", "send-keys", "-t", pane, key); err != nil {
		return d, fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	moved, ok := waitDialog(pane, dialogTypeWait, func(nd Dialog) bool {
		return nd.Current != slot.N && (nd.Current > 0 || nd.OnSubmit)
	})
	if !ok {
		return d, fmt.Errorf("%w: %s 의 커서가 직접 쓰기 칸에서 나오지 않았다 — 숫자는 누르지 않았다", ErrAnswerUnknown, pane)
	}
	return moved, nil
}

// clearFreeSlot 은 커서가 있는 직접 쓰기 칸 opt 의 쓰다 만 글을 지운다. 칸에 다시 들어오면 글 커서가 맨 앞이라 End 로
// 끝에 간 뒤 BSpace 를 글자 수만큼 누른다(실측, 남는 BSpace 는 빈 칸에서 아무것도 안 한다). 비었는지 보고 돌아온다.
func clearFreeSlot(pane string, opt DialogOption) error {
	draft := opt.Draft()
	if draft == "" {
		return nil
	}
	if err := runner.Run("tmux", "send-keys", "-t", pane, "End"); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	count := strconv.Itoa(utf8.RuneCountInString(draft) + 1)
	if err := runner.Run("tmux", "send-keys", "-t", pane, "-N", count, "BSpace"); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	n := opt.N
	if _, ok := waitDialog(pane, dialogTypeWait, func(d Dialog) bool {
		o, ok := d.option(n)
		return ok && d.Current == n && o.FreeText && o.Draft() == ""
	}); !ok {
		return fmt.Errorf("%w: %s 의 직접 쓰기 칸에 쓰던 글을 다 지우지 못했다 — 글은 치지 않았다", ErrAnswerUnknown, pane)
	}
	return nil
}

// undoFreeText 는 친 글이 칸에 그대로 보이지 않을 때 친 만큼 BSpace 로 되돌린다. 치기 직전에 칸이 빈 것을 봤고 글
// 커서는 친 글 끝에 있다 — 커서가 아직 그 칸에 있을 때만 누른다. 되돌렸든 못 했든 ErrAnswerUnknown 이다.
func undoFreeText(pane string, n int, text, what string) error {
	d, ok := readDialog(pane)
	if !ok || d.Current != n {
		return fmt.Errorf("%w: %s 의 직접 쓰기 칸이 보낸 글과 다르다 — %s", ErrAnswerUnknown, pane, what)
	}
	count := strconv.Itoa(utf8.RuneCountInString(text))
	if err := runner.Run("tmux", "send-keys", "-t", pane, "-N", count, "BSpace"); err != nil {
		return fmt.Errorf("%w: %s 의 직접 쓰기 칸이 보낸 글과 다르다 — %s, 친 글을 되돌리지 못했다: %v", ErrAnswerUnknown, pane, what, err)
	}
	if _, ok := waitDialog(pane, dialogTypeWait, func(d Dialog) bool {
		o, ok := d.option(n)
		return ok && o.FreeText && o.Draft() == ""
	}); !ok {
		return fmt.Errorf("%w: %s 의 직접 쓰기 칸이 보낸 글과 다르다 — %s, 친 글을 다 되돌리지 못했다", ErrAnswerUnknown, pane, what)
	}
	return fmt.Errorf("%w: %s 의 직접 쓰기 칸이 보낸 글과 달라 친 글을 지웠다 — %s", ErrAnswerUnknown, pane, what)
}

// CancelDialog 는 Esc 로 대화상자를 거둔다(권한은 거절, 질문은 답하지 않음). key 는 폰이 본 대화상자다 — 그사이 다른
// 대화상자가 떴으면 거두지 않는다.
func CancelDialog(a Agent, key string) error {
	unlock, err := lockPane(a.PaneID)
	if err != nil {
		return err
	}
	defer unlock()
	d, err := prepareDialog(a)
	if err != nil {
		return err
	}
	if err := sameDialog(d, key); err != nil {
		return err
	}
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "Escape"); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}
	if dialogMoved(a, d.signature()) {
		return nil
	}
	return fmt.Errorf("%w: %s 의 대화상자가 그대로다", ErrAnswerUnknown, a.PaneID)
}

// waitDialog 는 dialogSettle 뒤 limit 안에서 화면의 대화상자가 cond 를 만족하기를 기다린다.
func waitDialog(pane string, limit time.Duration, cond func(Dialog) bool) (Dialog, bool) {
	agentSleep(dialogSettle)
	deadline := agentNow().Add(limit)
	for {
		if d, ok := readDialog(pane); ok && cond(d) {
			return d, true
		}
		if !agentNow().Before(deadline) {
			return Dialog{}, false
		}
		agentSleep(promptPoll)
	}
}

// dialogMoved 는 키를 누른 뒤 대화상자가 넘어갔는지 본다: 닫혔거나(번호 선택지가 화면에 없다), 바뀌었거나,
// 상태가 막힘에서 벗어났다.
func dialogMoved(a Agent, before string) bool {
	agentSleep(dialogSettle)
	deadline := agentNow().Add(dialogMoveWait)
	for {
		d, ok := readDialog(a.PaneID)
		if !ok || d.signature() != before {
			return true
		}
		if fresh, err := refreshAgent(a); err == nil && fresh.State != AgentBlocked {
			return true
		}
		if !agentNow().Before(deadline) {
			return false
		}
		agentSleep(promptPoll)
	}
}

// AnswerMulti 는 여러 개 고르기에 답한다. key 는 폰이 본 대화상자(Dialog.Key), want 는 폰이 본 체크박스 선택지
// 전부와 바라는 체크다 — 지금 화면이 다르면 누르지 않고 ErrDialogChanged 다. text 는 직접 쓰기 칸의 글이고, 그 칸을
// 바랄 때만 있어야 한다.
//
// 실측한 키 동작대로, 단계마다 화면을 다시 읽어 확인한다:
//  1. 커서가 직접 쓰기 칸에 있으면 빼낸 뒤(그 칸에서는 숫자가 글자로 들어간다), 직접 쓰기 글을 칠 칸이 아닌 것 중 지금과
//     다른 것만 숫자키로 켜고 끈다(숫자키는 체크만 바꾸고 커서는 그대로다). 체크를 끈 칸의 쓰다 만 글은 제출되지 않는다.
//  2. 직접 쓰기 글이 있으면 화살표로 커서를 그 칸에 옮겨, 쓰다 만 글이 있으면 지우고 친다(숫자키로는 글이 들어가지
//     않는다). 칸에 그 글이 보이고 체크가 켜져야 다음으로 간다. 다르게 보이면 친 만큼 되돌린다.
//  3. 화살표로 번호 없는 «Submit» 줄까지 가서, 커서가 거기 있고 체크가 모두 바란 대로인 것을 보고 Enter → Submit
//     탭(검토). 제출은 그 탭에서 한 번 더 고른다(AnswerDialog).
func AnswerMulti(a Agent, key string, want []MultiChoice, text string) error {
	unlock, err := lockPane(a.PaneID)
	if err != nil {
		return err
	}
	defer unlock()

	d, err := prepareDialog(a)
	if err != nil {
		return err
	}
	if err := sameDialog(d, key); err != nil {
		return err
	}
	if !d.Multi || !d.SubmitLine {
		return fmt.Errorf("%w: 여러 개 고르기가 아니다", ErrDialogChanged)
	}
	boxes := checkboxOptions(d)
	if len(want) != len(boxes) {
		return fmt.Errorf("%w: 폰이 본 선택지 %d개가 지금은 %d개다", ErrDialogChanged, len(want), len(boxes))
	}
	var free *MultiChoice
	for i, w := range want {
		if o := boxes[i]; o.N != w.N || o.Label != w.Label {
			return fmt.Errorf("%w: 폰이 본 %d «%s» 가 지금 화면에는 %d «%s» 다", ErrDialogChanged, w.N, w.Label, o.N, o.Label)
		}
		if boxes[i].FreeText {
			free = &want[i]
		}
	}
	wantFree := free != nil && free.Checked
	switch {
	case text != "" && !wantFree:
		return fmt.Errorf("%w: 직접 쓰기 칸을 고르지 않았는데 글이 있다", ErrAnswerText)
	case text == "" && wantFree:
		return fmt.Errorf("%w: 직접 쓰기 칸을 골랐는데 글이 없다", ErrAnswerText)
	case text != "":
		if err := ValidatePromptText(text); err != nil {
			return fmt.Errorf("%w: %v", ErrAnswerText, err)
		}
	}

	// 1. 체크 맞추기 — 직접 쓰기 칸은 글을 치면 켜진다.
	if d, err = leaveFreeSlot(a.PaneID, d); err != nil {
		return err
	}
	for _, w := range want {
		o, _ := d.option(w.N)
		if o.Checked == w.Checked || (o.FreeText && w.Checked) {
			continue
		}
		if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, strconv.Itoa(w.N)); err != nil {
			return fmt.Errorf("%w: send-keys: %v", ErrAnswerUnknown, err)
		}
		n, checked := w.N, w.Checked
		if _, ok := waitDialog(a.PaneID, dialogTypeWait, func(d Dialog) bool {
			o, ok := d.option(n)
			return ok && o.Checked == checked
		}); !ok {
			return fmt.Errorf("%w: %s 의 %d번 체크가 바뀌지 않았다 — Submit 은 누르지 않았다", ErrAnswerUnknown, a.PaneID, n)
		}
	}

	// 2. 직접 쓰기 — 커서를 그 칸에 옮겨 친다.
	if wantFree {
		n := free.N
		at, err := moveDialogCursor(a.PaneID, len(boxes), n, func(d Dialog) bool { return d.Current == n })
		if err != nil {
			return err
		}
		slot, _ := at.option(n)
		if err := clearFreeSlot(a.PaneID, slot); err != nil {
			return err
		}
		if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "-l", "--", tmuxLiteral(text)); err != nil {
			return fmt.Errorf("%w: send-keys: %v", ErrAnswerUnknown, err)
		}
		want := normalizeBox(text)
		if _, ok := waitDialog(a.PaneID, dialogTypeWait, func(d Dialog) bool {
			o, ok := d.option(n)
			return ok && o.Checked && normalizeBox(o.Label+o.Detail) == want
		}); !ok {
			return undoFreeText(a.PaneID, n, text, "Submit 은 누르지 않았다")
		}
	}

	// 3. Submit 줄에서 Enter — 그 앞에 체크가 모두 바란 대로인지 한 번 더 본다.
	at, err := moveDialogCursor(a.PaneID, len(boxes), 0, func(d Dialog) bool { return d.OnSubmit })
	if err != nil {
		return err
	}
	if !at.OnSubmit {
		return fmt.Errorf("%w: %s 의 커서가 Submit 줄에 없다 — Enter 는 누르지 않았다", ErrAnswerUnknown, a.PaneID)
	}
	for _, w := range want {
		if o, ok := at.option(w.N); !ok || (o.Label != w.Label && !o.FreeText) || o.Checked != w.Checked {
			return fmt.Errorf("%w: %s 의 %d번 체크가 바란 대로가 아니다 — Submit 은 누르지 않았다", ErrAnswerUnknown, a.PaneID, w.N)
		}
	}
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "Enter"); err != nil {
		return fmt.Errorf("%w: send-keys: %v", ErrAnswerUnknown, err)
	}
	if dialogMoved(a, at.signature()) {
		return nil
	}
	return fmt.Errorf("%w: %s 의 대화상자가 그대로다", ErrAnswerUnknown, a.PaneID)
}

func checkboxOptions(d Dialog) []DialogOption {
	var out []DialogOption
	for _, o := range d.Options {
		if o.Checkbox {
			out = append(out, o)
		}
	}
	return out
}

// moveDialogCursor 는 여러 개 고르기에서 화살표로 커서를 옮긴다. 커서가 서는 곳은 체크박스 1..boxes, 그다음
// Submit 줄, 그 아래의 나머지 선택지(Chat about this) 순이다. to 가 0 이면 Submit 줄이다. 옮긴 뒤 arrived 를 확인하고
// 그 화면을 돌려준다.
func moveDialogCursor(pane string, boxes, to int, arrived func(Dialog) bool) (Dialog, error) {
	d, ok := readDialog(pane)
	if !ok {
		return Dialog{}, fmt.Errorf("%w: %s 의 대화상자가 사라졌다", ErrAnswerUnknown, pane)
	}
	if d.Current == 0 && !d.OnSubmit {
		return Dialog{}, fmt.Errorf("%w: %s 의 커서가 어디 있는지 모른다", ErrAnswerUnknown, pane)
	}
	stop := func(n int, onSubmit bool) int {
		switch {
		case onSubmit:
			return boxes
		case n <= boxes:
			return n - 1
		default:
			return n
		}
	}
	from := stop(d.Current, d.OnSubmit)
	target := stop(to, to == 0)
	if from == target {
		return d, nil
	}
	key, steps := "Down", target-from
	if steps < 0 {
		key, steps = "Up", -steps
	}
	if err := runner.Run("tmux", "send-keys", "-t", pane, "-N", strconv.Itoa(steps), key); err != nil {
		return Dialog{}, fmt.Errorf("%w: send-keys: %v", ErrAnswerUnknown, err)
	}
	at, ok := waitDialog(pane, dialogTypeWait, arrived)
	if !ok {
		return Dialog{}, fmt.Errorf("%w: %s 의 커서가 가야 할 곳에 가지 않았다", ErrAnswerUnknown, pane)
	}
	return at, nil
}
