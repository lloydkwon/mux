package tmux

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// maxPromptRunes 는 한 번에 보내는 글의 상한(글자 수)이다.
//
// 실측(Claude Code 2.1.312, 2026-11-02): 한 번에 들어온 글이 800자까지는 입력창에 펼쳐지고
// 801자부터 «[Pasted text #n]» 으로 접힌다. 바이트가 아니라 글자로 센다(한글도 같다).
// 접히면 입력창이 우리 글과 같은지 확인할 수 없고, 되돌릴 때 BSpace 수도 맞지 않는다.
// 여유를 두고 700 이다. 긴 본문은 파일에 쓰고 한 줄짜리 경로를 보낸다 — 회차 루프가 이미
// 그렇게 한다.
const maxPromptRunes = 700

const (
	// promptTypeSettle 은 글을 친 뒤 입력창을 처음 읽기까지 기다리는 시간이다. 글과 Enter 를
	// 붙여 보내면 TUI 가 Enter 를 삼킨다 — 회차 루프가 0.6초 간격을 재서 정했다.
	promptTypeSettle = 600 * time.Millisecond
	// promptTypeWait 은 입력창에 우리 글이 다 보이기까지 기다리는 상한이다.
	promptTypeWait = 2 * time.Second
	// promptConfirmWait 는 Enter 뒤 제출을 확인하는 상한이다.
	promptConfirmWait = 5 * time.Second
	promptPoll        = 100 * time.Millisecond
	promptConfirmPoll = 250 * time.Millisecond
	// promptLockWait 는 같은 pane 에 보내는 다른 mux 를 기다리는 상한이다.
	promptLockWait = 10 * time.Second
)

// 테스트가 바꾸는 이음매 — 진짜로 자지 않고 시계를 돌린다.
var (
	agentNow   = time.Now
	agentSleep = time.Sleep
	// agentLockDir 은 pane 별 잠금 파일을 두는 곳이다.
	agentLockDir = func() string {
		return filepath.Join(os.TempDir(), "mux-"+strconv.Itoa(os.Getuid()))
	}
)

// ValidatePromptText 는 보낼 수 있는 글인지 본다.
//
// 한 줄이어야 한다: 줄바꿈은 Claude 에서 제출이고, 탭·ESC 는 키다. 화면에서 한 글자로 합쳐지는
// 글자열(ZWJ·피부색·국기 이모지, 결합 부호, NFD 로 분해된 한글 자모)도 받지 않는다 — 되돌릴 때
// 친 글자(rune) 수만큼 BSpace 를 누르는데, 합쳐진 것은 한 번에 지워져 사람의 초안까지 지울 수
// 있다. 그래서 rune 수와 grapheme 수가 같아야 한다. "!" 로 시작하면 Claude 가 bash 모드로 바뀐다.
func ValidatePromptText(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("보낼 글이 비었다")
	}
	if !utf8.ValidString(text) {
		return errors.New("UTF-8 이 아닌 글이다")
	}
	n := utf8.RuneCountInString(text)
	if n > maxPromptRunes {
		return fmt.Errorf("글이 %d자다 (상한 %d자) — 긴 본문은 파일에 쓰고 그 경로를 한 줄로 보낸다", n, maxPromptRunes)
	}
	if strings.HasPrefix(text, "!") {
		return errors.New(`"!" 로 시작하는 글은 Claude 를 bash 모드로 바꾼다`)
	}
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r':
			return errors.New("여러 줄은 보낼 수 없다 — 본문은 파일에 쓰고 그 경로를 한 줄로 보낸다")
		case unicode.IsControl(r):
			return fmt.Errorf("제어 문자 %U 는 보낼 수 없다", r)
		case unicode.Is(unicode.Cf, r):
			return fmt.Errorf("보이지 않는 문자 %U 는 보낼 수 없다", r)
		}
	}
	if uniseg.GraphemeClusterCount(text) != n {
		return errors.New("화면에서 한 글자로 합쳐지는 문자(결합 문자·이모지 조합·분해된 한글)는 보낼 수 없다 — 되돌리기가 정확하지 않다")
	}
	return nil
}

// tmuxLiteral 은 send-keys -l 에 넘길 인자를 만든다. tmux 는 «;» 로 끝나는 인자를 명령
// 구분자로 읽으므로(3.4 실측: 뒤 인자가 새 명령이 된다) 끝의 «;» 를 «\;» 로 넘긴다.
func tmuxLiteral(text string) string {
	if strings.HasSuffix(text, ";") {
		return text[:len(text)-1] + `\;`
	}
	return text
}

// parseInputBox 는 capture-pane -p 화면에서 Claude 의 입력창 글을 찾는다.
//
// 입력창은 «─» 선 바로 아래, 0번 칸에서 «❯» 로 시작하는 줄이고, 다음 «─» 선까지 이어진다
// (긴 글은 줄바꿈된다). 그런 줄 중 마지막 것을 쓴다 — 위쪽 대화 기록과 대기열에도 «❯ 글»
// 줄이 있지만 그 위는 «─» 선이 아니다. 대화상자의 «❯ 1. Yes» 는 들여쓰여 있어 걸리지
// 않는다. 실측(2.1.312): 빈 입력창은 «❯» + U+00A0 + 커서 칸 하나다.
func parseInputBox(screen string) (string, bool) {
	lines := strings.Split(screen, "\n")
	at := -1
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "❯") && strings.HasPrefix(lines[i-1], "─") {
			at = i
		}
	}
	if at < 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(strings.TrimPrefix(lines[at], "❯"))
	for j := at + 1; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], "─") {
			return b.String(), true
		}
		b.WriteString(lines[j])
	}
	return "", false // 아래 선이 없다 — 입력창이 다 보이지 않는다
}

// normalizeBox 는 비교용으로 공백을 모두 지운다. U+00A0 구분자, 커서 칸, 줄바꿈된 곳의
// 들여쓰기가 다 공백이라, 남는 것은 글 자체다.
func normalizeBox(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func readBox(pane string) (string, bool) {
	out, err := runner.Output("tmux", "capture-pane", "-p", "-t", pane)
	if err != nil {
		return "", false
	}
	return parseInputBox(string(out))
}

// lockPane 은 pane 별 잠금을 잡는다. 두 mux 가 한 pane 에 동시에 치면 글자가 섞인다.
// flock 이라 프로세스가 죽으면 커널이 풀어 준다.
//
// 열쇠는 tmux 가 답하는 소켓 경로다 — $TMUX 로 만들면 tmux 밖(herdr 등)에서 부른 mux 와 안에서
// 부른 mux 가 같은 서버의 같은 pane 에 다른 잠금을 잡는다.
func lockPane(pane string) (func(), error) {
	dir := agentLockDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("잠금 디렉터리: %w", err)
	}
	server := "default"
	if out, err := runner.Output("tmux", "display-message", "-p", "#{socket_path}"); err == nil {
		server = strings.TrimSpace(string(out))
	}
	h := fnv.New32a()
	h.Write([]byte(server))
	name := fmt.Sprintf("agent-%08x-%s.lock", h.Sum32(), strings.TrimPrefix(pane, "%"))
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("잠금 파일: %w", err)
	}
	deadline := agentNow().Add(promptLockWait)
	for {
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !agentNow().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w: 다른 mux agent prompt 가 %s 에 보내는 중이다", ErrAgentNotReady, pane)
		}
		agentSleep(promptPoll)
	}
}

// checkPaneMode 는 키가 Claude 에게 가는 pane 인지 본다. 복사 모드면 키가 모드로 가고,
// synchronize-panes 면 같은 창의 모든 pane 에 간다.
func checkPaneMode(pane string, force bool) error {
	out, err := runner.Output("tmux", "display-message", "-p", "-t", pane, "#{pane_in_mode} #{pane_synchronized}")
	if err != nil {
		return fmt.Errorf("%w: pane %s 를 읽지 못했다: %v", ErrAgentNotReady, pane, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 2 && f[1] == "1" {
		return fmt.Errorf("%w: %s 의 창에 synchronize-panes 가 켜져 있다", ErrAgentNotReady, pane)
	}
	if len(f) >= 1 && f[0] == "1" {
		if !force {
			return fmt.Errorf("%w: %s 가 복사 모드다 (--force 로 빠져나와 보낸다)", ErrAgentNotReady, pane)
		}
		if err := runner.Run("tmux", "send-keys", "-t", pane, "-X", "cancel"); err != nil {
			return fmt.Errorf("%w: 복사 모드를 빠져나오지 못했다: %v", ErrAgentNotReady, err)
		}
	}
	return nil
}

// waitBox 는 settle 만큼 기다린 뒤 limit 안에서 입력창이 cond 를 만족하기를 기다린다.
func waitBox(pane string, cond func(box string, ok bool) bool, settle, limit time.Duration) (string, bool, bool) {
	agentSleep(settle)
	deadline := agentNow().Add(limit)
	for {
		box, ok := readBox(pane)
		if cond(box, ok) {
			return box, ok, true
		}
		if !agentNow().Before(deadline) {
			return box, ok, false
		}
		agentSleep(promptPoll)
	}
}

// rollback 은 친 글을 친 글자 수만큼 BSpace 로 지우고, 입력창이 치기 전(또는 빈 것)으로
// 돌아왔는지 본다.
//
// 지우기 전에 다시 읽어, 입력창이 «치기 전의 글 + 우리 글» 이거나 «우리 글» 일 때만 지운다.
// 그 사이 사람이 이어 쳤다면 BSpace 는 커서 앞의 사람 글부터 지운다 — 그때는 건드리지 않고
// 물러선다(false).
func rollback(pane, text, before string) bool {
	box, ok := readBox(pane)
	norm, want, prev := normalizeBox(box), normalizeBox(text), normalizeBox(before)
	if !ok || (norm != want && norm != prev+want) {
		return false
	}
	n := utf8.RuneCountInString(text)
	if err := runner.Run("tmux", "send-keys", "-t", pane, "-N", strconv.Itoa(n), "BSpace"); err != nil {
		return false
	}
	_, _, restored := waitBox(pane, func(box string, ok bool) bool {
		norm := normalizeBox(box)
		return ok && (norm == prev || norm == "")
	}, promptPoll, promptTypeWait)
	return restored
}

type submitResult int

const (
	submitConfirmed submitResult = iota
	submitSwallowed              // 입력창에 아직 우리 글만 있다 — Enter 가 먹혔다
	submitUnknown
)

// confirmSubmit 은 Enter 뒤 제출을 확인한다. 셋 중 하나면 제출이다: 입력창이 비었다, 입력창이
// 사라졌는데(대화상자) 상태가 Enter 뒤에 바뀌었다, 대화 기록에 Enter 뒤의 사람 턴이 생겼다.
//
// 상태 변화는 대상이 idle 이었을 때만 믿는다. --force 로 작업 중에 보냈다면 앞 턴이 끝나거나
// 앞 턴의 권한 대화상자가 떠도 상태가 바뀐다 — Enter 가 먹혔는데 성공으로 읽게 된다.
func confirmSubmit(a Agent, enterAt time.Time, want string, wasIdle bool, hash uint32) submitResult {
	deadline := agentNow().Add(promptConfirmWait)
	lastSwallowed := false
	for {
		box, ok := readBox(a.PaneID)
		norm := normalizeBox(box)
		lastSwallowed = ok && norm == want
		switch {
		case ok && norm == "":
			return submitConfirmed
		case !ok && wasIdle && stateChangedSince(a, enterAt):
			return submitConfirmed
		}
		if !agentNow().Before(deadline) {
			break
		}
		agentSleep(promptConfirmPoll)
	}
	if humanTurnSince(a, enterAt, hash) {
		return submitConfirmed
	}
	if lastSwallowed {
		return submitSwallowed
	}
	return submitUnknown
}

func stateChangedSince(a Agent, at time.Time) bool {
	sf, err := readClaudeSessionFile(a.PID)
	return err == nil && sf.StatusUpdatedAt >= at.UnixMilli()
}

// humanTurnSince 는 대화 기록에 at 이후의 사람 턴이 있는지 본다.
func humanTurnSince(a Agent, at time.Time, hash uint32) bool {
	rows, _, err := transcriptChain(a.SessionID, a.CWD)
	if err != nil {
		return false
	}
	_, ok := findTurn(rows, &at, hash)
	return ok
}

// preparePrompt 는 보내기 전의 확인이다: 상태, pane 모드, 입력창. 아무것도 보내지 않는다
// (--force 의 복사 모드 빠져나오기만 예외). 새로 읽은 에이전트와 치기 전의 입력창을 돌려준다.
func preparePrompt(a Agent, force bool) (Agent, string, error) {
	a, err := refreshAgent(a)
	if err != nil {
		return a, "", err
	}
	switch a.State {
	case AgentIdle:
	case AgentBlocked:
		// 보내면 대화상자에 답하는 셈이다 — --force 로도 넘지 않는다.
		return a, "", fmt.Errorf("%w: %s (%s)", ErrAgentBlocked, a.PaneID, a.WaitingFor)
	default:
		if !force {
			return a, "", fmt.Errorf("%w: %s 가 %s 상태다 (--force 면 대기열에 넣는다)", ErrAgentNotReady, a.PaneID, a.State)
		}
	}
	if err := checkPaneMode(a.PaneID, force); err != nil {
		return a, "", err
	}
	before, ok := readBox(a.PaneID)
	if !ok {
		return a, "", fmt.Errorf("%w: %s 에 입력창이 보이지 않는다 — 대화상자나 메뉴가 열려 있다", ErrAgentNotReady, a.PaneID)
	}
	return a, before, nil
}

// pressEnter 는 Enter 를 따로, 많아야 두 번 누른다. 다시 눌러도 두 번 제출될 수 없다 — Enter 는
// 입력창에 있는 것만 제출하고, 입력창은 제출되면 빈다. 다시 누르기 전에 기록과 상태를 한 번 더
// 본다: Claude 가 잠시 멈춰 화면이 늦게 따라온 것이면 첫 Enter 가 이미 제출됐고, 두 번째 Enter 는
// 빈 입력창에 떨어진다.
func pressEnter(a Agent, want string, wasIdle bool, hash uint32) (submitResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		enterAt := agentNow()
		if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "Enter"); err != nil {
			return submitUnknown, fmt.Errorf("%w: Enter: %v", ErrPromptUnknown, err)
		}
		res := confirmSubmit(a, enterAt, want, wasIdle, hash)
		if res != submitSwallowed {
			return res, nil
		}
		if wasIdle && stateChangedSince(a, enterAt) {
			return submitUnknown, nil
		}
	}
	return submitSwallowed, nil
}

// PromptAgent 는 대상 Claude 의 입력창에 글을 치고 제출한다. 성공하면 토큰을 돌려준다.
//
// 지키는 것 셋: 글은 한 번만 친다. Enter 는 입력창이 정확히 우리 글일 때만 누른다 — 빈
// 입력창에서 누르면 Claude 의 제안 문구가 제출될 수 있다. 어긋나면 친 만큼만 지운다 —
// 사람이 쓰던 초안과 사람이 그 사이 친 글은 그대로 둔다.
func PromptAgent(a Agent, text string, force bool) (AgentToken, error) {
	if err := ValidatePromptText(text); err != nil {
		return AgentToken{}, err
	}
	unlock, err := lockPane(a.PaneID)
	if err != nil {
		return AgentToken{}, err
	}
	defer unlock()

	a, before, err := preparePrompt(a, force)
	if err != nil {
		return AgentToken{}, err
	}
	wasIdle := a.State == AgentIdle
	tok := AgentToken{SessionID: a.SessionID, SentAt: agentNow(), TextHash: promptHash(text)}
	if err := runner.Run("tmux", "send-keys", "-t", a.PaneID, "-l", "--", tmuxLiteral(text)); err != nil {
		return AgentToken{}, fmt.Errorf("%w: send-keys: %v", ErrAgentNotReady, err)
	}

	want := normalizeBox(text)
	if _, _, shown := waitBox(a.PaneID, func(box string, ok bool) bool {
		return ok && normalizeBox(box) == want
	}, promptTypeSettle, promptTypeWait); !shown {
		if rollback(a.PaneID, text, before) {
			return AgentToken{}, fmt.Errorf("%w: %s 의 입력창에 다른 글이 있어 친 글을 지웠다", ErrAgentNotReady, a.PaneID)
		}
		return AgentToken{}, fmt.Errorf("%w: 입력창이 우리 글과 다르고, 되돌리지도 않았다", ErrPromptUnknown)
	}

	res, err := pressEnter(a, want, wasIdle, tok.TextHash)
	switch {
	case err != nil:
		return AgentToken{}, err
	case res == submitConfirmed:
		return tok, nil
	case res == submitSwallowed && rollback(a.PaneID, text, before):
		return AgentToken{}, fmt.Errorf("%w: %s", ErrNotSubmitted, a.PaneID)
	}
	return AgentToken{}, fmt.Errorf("%w: %s", ErrPromptUnknown, a.PaneID)
}
