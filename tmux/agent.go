package tmux

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 에이전트 협업 API — 한 pane 의 Claude 가 다른 pane 의 Claude 에게 일을 보내고,
// 끝나기를 기다리고, 회신을 읽는다(`mux agent ls/prompt/wait/read`).
// 설계와 실측은 docs/design/agent-api.md.
//
// v1 은 Claude 만 다룬다. prompt 는 Claude 의 ❯ 입력창에, wait 는 상태 파일
// (~/.claude/sessions/<pid>.json)에, read 는 대화 기록에 기댄다 — 다른 도구에는 셋 다 없다.

// AgentState 는 API 의 상태 단어다. AIState.String() 을 쓰지 않는다 — 그것은 Ready 를
// "waiting" 으로 내서(status --json 의 오래된 약속), «완료»와 «승인 대기»가 섞인다.
type AgentState string

const (
	AgentWorking AgentState = "working" // 턴 진행 중 (Claude "busy")
	AgentBlocked AgentState = "blocked" // 대화상자에서 사람을 기다림 (Claude "waiting")
	AgentIdle    AgentState = "idle"    // 입력을 받을 수 있음
	AgentShell   AgentState = "shell"   // 턴은 끝났지만 백그라운드 셸이 돈다
	AgentUnknown AgentState = "unknown" // 모르는 상태 문자열
)

// Agent 는 tmux pane 하나에서 도는 Claude 하나다.
type Agent struct {
	PaneID  string // "%12"
	Session string // 지금 그 pane 을 가진 tmux 세션 (Claude 가 적은 이름이 아니다)
	Tool    string // v1 은 늘 "claude"
	State   AgentState
	// Since 는 지금 상태가 시작된 때다(statusUpdatedAt). 모르면 제로값.
	Since      time.Time
	WaitingFor string
	CWD        string
	// Name 은 Claude 가 부르는 대화 이름이다 — `claude -n` 이나 /rename 으로 정한 것,
	// 아니면 Claude 가 지은 것.
	Name      string
	SessionID string
	PID       int

	procStart string
	startedAt int64
}

// 오류 값 — cmd/mux 가 errors.Is 로 종료 코드를 고른다(agent-api.md 의 표).
var (
	ErrNoServer      = errors.New("tmux 서버에 닿지 못했다")
	ErrNoAgent       = errors.New("대상 에이전트가 없다")
	ErrAmbiguous     = errors.New("대상이 여럿이다")
	ErrAgentGone     = errors.New("에이전트가 끝났다")
	ErrAgentBlocked  = errors.New("에이전트가 대화상자에서 사람을 기다린다")
	ErrAgentNotReady = errors.New("지금은 보낼 수 없다 — 아무것도 보내지 않았다")
	ErrNotSubmitted  = errors.New("제출되지 않았고 입력창을 되돌렸다 — 다시 보내도 된다")
	ErrPromptUnknown = errors.New("제출됐는지 알 수 없다 — pane 을 확인하고, 무작정 다시 보내지 마라")
	ErrPromptNotSeen = errors.New("프롬프트가 대화 기록에 나타나지 않았다 — 제출은 됐을 수 있으니 다시 보내지 마라")
	ErrTurnRunning   = errors.New("턴이 아직 진행 중이다")
	ErrNoReply       = errors.New("회신 본문이 없다")
	ErrWaitTimeout   = errors.New("기다리다 시간이 다 됐다")
	ErrTokenMismatch = errors.New("토큰이 이 에이전트의 대화와 맞지 않는다")
)

// agentStateOf 는 Claude 의 상태 문자열을 API 의 단어로 바꾼다. shell 은 개발 서버만
// 남은 경우 idle 로 내린다 — 목록·패널이 쓰는 demoteServerShell 과 같은 판단이다.
func agentStateOf(status string, pid int) AgentState {
	switch status {
	case "busy":
		return AgentWorking
	case "waiting":
		return AgentBlocked
	case "idle":
		return AgentIdle
	case "shell":
		if demoteServerShell(pid) == AIStateReady {
			return AgentIdle
		}
		return AgentShell
	default:
		return AgentUnknown
	}
}

// liveClaudeSessionFiles 는 살아 있는 Claude 의 상태 파일 전부다. 캐시하지 않는다 —
// prompt·wait 는 1초 묵은 상태로 움직이면 안 된다.
func liveClaudeSessionFiles() []claudeSessionFile {
	dir, err := claudeSessionsDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []claudeSessionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var sf claudeSessionFile
		if json.Unmarshal(data, &sf) != nil || sf.SessionID == "" {
			continue
		}
		if !isAlive(sf.PID, sf.ProcStart) {
			continue
		}
		out = append(out, sf)
	}
	return out
}

// readClaudeSessionFile 은 pid 하나의 상태 파일을 새로 읽는다.
func readClaudeSessionFile(pid int) (claudeSessionFile, error) {
	dir, err := claudeSessionsDir()
	if err != nil {
		return claudeSessionFile{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if err != nil {
		return claudeSessionFile{}, err
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return claudeSessionFile{}, err
	}
	return sf, nil
}

func agentFromFile(sf claudeSessionFile, paneID, session string) Agent {
	a := Agent{
		PaneID:     paneID,
		Session:    session,
		Tool:       "claude",
		State:      agentStateOf(sf.Status, sf.PID),
		CWD:        sf.CWD,
		Name:       sf.Name,
		SessionID:  sf.SessionID,
		PID:        sf.PID,
		procStart:  sf.ProcStart,
		startedAt:  sf.StartedAt,
		WaitingFor: "",
	}
	if a.State == AgentBlocked {
		a.WaitingFor = sf.WaitingFor
	}
	if sf.StatusUpdatedAt > 0 {
		a.Since = time.UnixMilli(sf.StatusUpdatedAt)
	}
	return a
}

// Agents 는 이 tmux 서버의 pane 에서 도는 Claude 전부다.
//
// Claude 가 적어 둔 tmux 위치("<session>:@win.%pane")는 그대로 믿지 않는다. pane id 는
// 서버가 바뀌면 다시 쓰이고, 실제로 다른 서버의 pane 을 가리키는 살아 있는 상태 파일이
// 흔하다. claudeSessionName 이 지금 서버의 pane 과 그 pane 의 프로세스 트리로 거른다.
func Agents() ([]Agent, error) {
	panes := paneSessions()
	if panes == nil {
		return nil, ErrNoServer
	}
	byPane := map[string]Agent{}
	for _, sf := range liveClaudeSessionFiles() {
		if sf.Tmux == "" {
			continue // tmux 밖(herdr·맨 터미널·편집기)의 Claude 는 지목할 수 없다
		}
		paneID := tmuxPaneID(sf.Tmux)
		if paneID == "" {
			continue
		}
		session := claudeSessionName(sf.Tmux, panes, sf.PID)
		if session == "" {
			continue
		}
		a := agentFromFile(sf, paneID, session)
		if prev, dup := byPane[paneID]; dup && !preferAgent(a, prev, panes[paneID].pid) {
			continue
		}
		byPane[paneID] = a
	}
	out := make([]Agent, 0, len(byPane))
	for _, a := range byPane {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		ni, _ := paneNumber(out[i].PaneID)
		nj, _ := paneNumber(out[j].PaneID)
		return ni < nj
	})
	return out, nil
}

// preferAgent 는 한 pane 을 가리키는 상태 파일이 둘일 때 고른다: pane 의 프로세스 트리
// 안에 있다고 확인된 쪽, 그다음 늦게 시작한 쪽, 그다음 pid 가 큰 쪽.
func preferAgent(a, b Agent, panePID int) bool {
	ua, ka := pidInPane(a.PID, panePID)
	ub, kb := pidInPane(b.PID, panePID)
	if (ka && ua) != (kb && ub) {
		return ka && ua
	}
	if a.startedAt != b.startedAt {
		return a.startedAt > b.startedAt
	}
	return a.PID > b.PID
}

var paneIDPattern = regexp.MustCompile(`^%\d+$`)

// ResolveAgent 는 대상을 하나로 정한다: pane id(%12), 그 세션에 Claude 가 하나인 tmux
// 세션 이름, 또는 Claude 이름.
//
// 세션과 이름 사이에 우선순위가 없다. 둘 이상이 맞으면 조용히 고르지 않고 후보를 늘어놓는
// 오류다 — 짝 합치기(pair.md) 뒤에는 plan Claude 가 exec 세션의 창에 살고, 같은 이름의
// 대화가 여러 프로젝트에 있는 것이 보통이라, 어느 규칙으로 골라도 언젠가 엉뚱한 Claude 에게
// 보낸다.
func ResolveAgent(agents []Agent, target string) (Agent, error) {
	if paneIDPattern.MatchString(target) {
		for _, a := range agents {
			if a.PaneID == target {
				return a, nil
			}
		}
		return Agent{}, fmt.Errorf("%w: pane %s 에 Claude 가 없다", ErrNoAgent, target)
	}
	type candidate struct {
		a   Agent
		why string
	}
	var cands []candidate
	for _, a := range agents {
		var why []string
		if a.Session == target {
			why = append(why, "session")
		}
		if a.Name == target {
			why = append(why, "name")
		}
		if len(why) > 0 {
			cands = append(cands, candidate{a, strings.Join(why, "+")})
		}
	}
	switch len(cands) {
	case 1:
		return cands[0].a, nil
	case 0:
		return Agent{}, fmt.Errorf("%w: %q 에 맞는 Claude 가 없다 — pane id(%%12), Claude 가 하나인 세션 이름, Claude 이름 중 하나로 지목한다 (mux agent ls)",
			ErrNoAgent, target)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q 에 맞는 Claude 가 %d개다 — pane id 로 지목한다:", target, len(cands))
	for _, c := range cands {
		fmt.Fprintf(&b, "\n  %-5s session=%s name=%s %s (%s)", c.a.PaneID, c.a.Session, c.a.Name, c.a.State, c.why)
	}
	return Agent{}, fmt.Errorf("%w: %s", ErrAmbiguous, b.String())
}

// refreshAgent 는 상태 파일을 새로 읽어 상태를 갱신한다. 프로세스가 끝났으면 ErrAgentGone.
//
// 읽기에 실패해도 프로세스가 살아 있으면 끝난 것이 아니다 — Claude 는 이 파일을 제자리에
// 덮어써서 반쯤 쓰인 내용이 읽힌다(claude_status_test.go 의 torn write). 그때는 이전 상태를
// 그대로 두고 다음 폴링을 기다린다. 한 시간 기다리는 wait 가 한 번의 찢어진 읽기로 끝나면 안 된다.
func refreshAgent(a Agent) (Agent, error) {
	sf, err := readClaudeSessionFile(a.PID)
	if err == nil && sf.PID == a.PID && isAlive(sf.PID, sf.ProcStart) {
		return agentFromFile(sf, a.PaneID, a.Session), nil
	}
	if isAlive(a.PID, a.procStart) {
		if _, statErr := os.Stat(claudeSessionPath(a.PID)); statErr == nil {
			return a, nil
		}
	}
	return a, fmt.Errorf("%w: %s (pid %d)", ErrAgentGone, a.PaneID, a.PID)
}

func claudeSessionPath(pid int) string {
	dir, err := claudeSessionsDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, strconv.Itoa(pid)+".json")
}

// AgentToken 은 prompt 가 돌려주는 «이 프롬프트» 의 표식이다: 대화 id, 보낸 시각(ms), 보낸
// 글의 해시. wait·read --after 는 이것으로 그 프롬프트가 연 턴을 찾는다.
//
// 대화 id 가 있어야 compact 로 기록이 다음 파일로 넘어가도 따라가고, /clear 로 대화가 바뀌면
// 다른 대화를 읽지 않는다. 글의 해시는 대기열 때문이다: 작업 중에 보낸(--force) 글은 꺼낼
// 때의 시각으로 기록되므로, 먼저 대기열에 있던 다른 글의 턴도 «보낸 시각 뒤의 첫 사람 턴» 이
// 된다. 본문이 같은 턴이 있으면 그것을 고른다(슬래시 명령처럼 본문이 바뀌어 기록되면 맞는
// 턴이 없고, 그때는 시각만으로 고른다).
type AgentToken struct {
	SessionID string
	SentAt    time.Time
	TextHash  uint32 // 0 이면 모른다
}

// promptHash 는 보낸 글의 해시다. 기록의 사람 턴 본문과 대조한다.
func promptHash(text string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(text))
	return h.Sum32()
}

func (t AgentToken) String() string {
	s := t.SessionID + "@" + strconv.FormatInt(t.SentAt.UnixMilli(), 10)
	if t.TextHash != 0 {
		s += "#" + strconv.FormatUint(uint64(t.TextHash), 16)
	}
	return s
}

// ParseAgentToken 은 "<sessionId>@<unix ms>[#<글 해시>]" 를 푼다.
func ParseAgentToken(s string) (AgentToken, error) {
	bad := fmt.Errorf("토큰 형식이 아니다 (<sessionId>@<ms>#<hash>): %q", s)
	rest, hash, hasHash := strings.Cut(strings.TrimSpace(s), "#")
	sid, ms, ok := strings.Cut(rest, "@")
	n, err := strconv.ParseInt(ms, 10, 64)
	if !ok || sid == "" || err != nil || n <= 0 {
		return AgentToken{}, bad
	}
	tok := AgentToken{SessionID: sid, SentAt: time.UnixMilli(n)}
	if hasHash {
		h, err := strconv.ParseUint(hash, 16, 32)
		if err != nil {
			return AgentToken{}, bad
		}
		tok.TextHash = uint32(h)
	}
	return tok, nil
}

// checkTokenChain 은 에이전트의 지금 대화가 토큰의 대화이거나 그 대화에서 compact 로
// 이어진 것인지 본다. ids 는 transcriptChain 이 거쳐 간 대화 id 들이다.
func checkTokenChain(a Agent, tok AgentToken, ids []string) error {
	for _, id := range ids {
		if id == a.SessionID {
			return nil
		}
	}
	return fmt.Errorf("%w: 토큰은 %s, 지금 대화는 %s", ErrTokenMismatch, shortID(tok.SessionID), shortID(a.SessionID))
}
