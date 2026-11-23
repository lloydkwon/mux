package tmux

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 보관 — 지금은 필요 없는 Claude·opencode 세션을 닫되, 나중에 같은 대화로 되살릴 수 있게 기억해 둔다.
//
// 닫는 순간 ~/.claude/sessions 의 상태 파일이 사라지므로 --claude 목록에도 다시 나오지 않는다.
// 대화로 가는 길은 대화 id 와 그것을 연 디렉터리뿐이고, 이 파일이 그 둘을 적어 둔다. 되살리기는
// 같은 id 로 --resume 한다 — 포크하지 않는 이유는 docs/design/claude-mode.md, 나머지 결정은
// docs/design/park.md. opencode 쪽(대화 id 를 알아내는 법, -s 로 되살리기)은 park_opencode.go.

// ErrNothingToPark 는 세션에 되살릴 수 있는 대화가 없다는 뜻이다. 그때는 세션을 닫지 않는다.
var ErrNothingToPark = errors.New("no Claude or opencode conversation to resume in this session")

const (
	toolClaude   = "claude"
	toolOpencode = "opencode"
)

// ParkableTool 은 보관할 수 있는 도구인지 — 대화 id 를 알아내 같은 대화로 되살릴 수 있는 도구다.
func ParkableTool(name string) bool { return name == toolClaude || name == toolOpencode }

// ParkedConversation 은 보관한 세션 안의 대화 하나다.
type ParkedConversation struct {
	SessionID string `json:"session_id"`
	// CWD 는 대화를 연 곳이다. --resume 은 거기서 해야 기록을 다시 찾는다.
	CWD string `json:"cwd"`
	// Task 는 도구가 붙인 대화 이름이다(Claude 의 작업 이름, opencode 의 제목). 보관함에 보여 주는 용도뿐이다.
	Task string `json:"task,omitempty"`
	// PID 는 보관할 때 대화를 들고 있던 프로세스다. 복원 때 «아직 살아 있는 사본» 판정에서 뺀다 —
	// SIGHUP 을 받고 끝나는 중인 그 프로세스를 사본으로 보면 포크하거나 죽은 세션으로 간다.
	PID int `json:"pid,omitempty"`
	// Tool 은 대화를 연 도구다. 빈 값은 claude 다 — 이 필드가 생기기 전의 보관함이 그대로 읽힌다.
	Tool string `json:"tool,omitempty"`
	// Command 는 되살릴 때 띄울 opencode 실행 파일이다. tmux 서버의 PATH 에 없을 수 있어 보관할 때 돌던
	// 경로를 적는다. claude 는 비운다.
	Command string `json:"command,omitempty"`
}

// ParkedSession 은 보관함의 항목 하나 — 닫은 tmux 세션 하나다.
type ParkedSession struct {
	// ID 는 보관할 때마다 새로 만드는 항목의 열쇠다(newParkedID). 대화 id 를 그대로 쓰면, 일부만 복원돼
	// 남은 항목과 그 세션을 다시 보관한 항목이 같은 열쇠를 갖게 되고, 지우기 한 번에 둘 다 사라진다.
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ProjectDir string    `json:"project_dir,omitempty"`
	Note       string    `json:"note,omitempty"`
	ParkedAt   time.Time `json:"parked_at"`
	// Conversations 는 창·pane 순서다. 첫 대화가 세션을 열고, 나머지는 창을 하나씩 연다.
	Conversations []ParkedConversation `json:"conversations"`
}

type parkedState struct {
	Sessions []ParkedSession `json:"sessions"`
}

// parkedFile 은 보관함 파일의 경로다. 테스트가 바꾼다.
var parkedFile = defaultParkedPath

// parkExitTimeout 은 보관한 Claude 가 끝나기를 기다리는 한도다. 테스트는 0 으로 둔다.
var parkExitTimeout = 3 * time.Second

func defaultParkedPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}
	return filepath.Join(dir, "mux", "parked.json"), nil
}

// LoadParked 는 보관함을 최신순으로 읽는다. 파일이 없으면 비어 있는 것이다.
//
// 깨진 파일은 빈 목록이 아니라 오류다. 빈 목록으로 읽고 다음에 쓰면 그 안의 항목이 전부 사라지고,
// 보관은 그 쓰기 뒤에 세션을 닫는다 — 대화로 가는 길을 한꺼번에 잃는다.
func LoadParked() ([]ParkedSession, error) {
	path, err := parkedFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read parked sessions: %w", err)
	}
	var st parkedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse parked sessions %s: %w", path, err)
	}
	return normalizeParked(st.Sessions), nil
}

// normalizeParked 는 이름이나 대화가 없는 항목을 버리고 최신순으로 세운다.
func normalizeParked(in []ParkedSession) []ParkedSession {
	var out []ParkedSession
	for _, p := range in {
		var convs []ParkedConversation
		for _, c := range p.Conversations {
			if c.SessionID == "" {
				continue
			}
			// tool 을 모르는 옛 mux 가 보관함을 다시 쓰면 그 필드가 지워지고, 그 대화는 claude --resume 으로 가
			// 영영 돌아오지 않는다. opencode 의 대화 id 는 «ses_» 로 시작하고 Claude 의 것은 UUID 라 겹치지 않는다.
			if c.Tool == "" && strings.HasPrefix(c.SessionID, "ses_") {
				c.Tool = toolOpencode
			}
			convs = append(convs, c)
		}
		if strings.TrimSpace(p.Name) == "" || len(convs) == 0 {
			continue
		}
		p.Conversations = convs
		if p.ID == "" {
			p.ID = convs[0].SessionID
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ParkedAt.After(out[j].ParkedAt) })
	// 열쇠가 겹치면 최신 것만 남긴다. 손으로 고친 파일이 아니면 생기지 않지만, 겹친 채 두면 지우기 한 번이
	// 둘을 함께 지운다.
	seen := make(map[string]bool, len(out))
	unique := out[:0:0]
	for _, p := range out {
		if !seen[p.ID] {
			seen[p.ID] = true
			unique = append(unique, p)
		}
	}
	return unique
}

// newParkedID 는 항목의 새 열쇠다 — 보관한 시각과 첫 대화 id 의 앞머리.
func newParkedID(at time.Time, convs []ParkedConversation) string {
	return strconv.FormatInt(at.UnixNano(), 36) + "-" + shortID(convs[0].SessionID)
}

// saveParked 는 보관함을 원자적으로 쓴다 — temp 파일, 0600, rename. panelwidth.go 와 같은 모양이다.
func saveParked(list []ParkedSession) error {
	path, err := parkedFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parked directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".parked-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary parked list: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set parked list permissions: %w", err)
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(parkedState{Sessions: normalizeParked(list)}); err != nil {
		return fmt.Errorf("write parked list: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close parked list: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace parked list: %w", err)
	}
	ok = true
	return nil
}

// ParkSession 은 세션의 Claude·opencode 대화를 보관함에 적고 세션을 닫는다. 갱신된 보관함을 돌려준다.
//
// **순서가 load-bearing 이다.** 적고, 적힌 것을 다시 읽어 확인하고, 그다음에 닫는다. 거꾸로면 닫기는
// 됐는데 쓰기가 실패한 경우 대화로 가는 길이 없다. 사람이 누르는 속도라 파일 잠금은 두지 않았고,
// 다시 읽는 확인이 그 대신이다.
func ParkSession(s Session) ([]ParkedSession, error) {
	if s.Name == "" {
		return nil, fmt.Errorf("park: empty session name")
	}
	list, err := LoadParked()
	if err != nil {
		return nil, err
	}

	resetClaudeRunCache()
	convs, err := sessionConversations(ClaudeRuns(), s.Name, parkedIDs(list))
	if err != nil {
		return list, err
	}
	if len(convs) == 0 {
		return list, ErrNothingToPark
	}
	now := time.Now()
	entry := ParkedSession{
		ID:            newParkedID(now, convs),
		Name:          s.Name,
		ProjectDir:    s.ProjectDir,
		Note:          s.Note,
		ParkedAt:      now,
		Conversations: convs,
	}
	if err := saveParked(append([]ParkedSession{entry}, stripConversations(list, convs)...)); err != nil {
		return list, err
	}
	saved, err := LoadParked()
	if err != nil {
		return list, err
	}
	if !containsParked(saved, entry.ID) {
		return saved, fmt.Errorf("park %q: the parked list did not keep it, so the session stays open", s.Name)
	}

	if err := closeParkedSession(s.Name, list); err != nil {
		return list, err
	}
	waitParkedExit(convs)
	resetClaudeRunCache()
	return saved, nil
}

// closeParkedSession 은 보관함에 적은 뒤 세션을 닫는다. 닫지 못했는데 세션이 살아 있으면 보관함을 before 로
// 되돌린다 — 새 항목을 빼는 것만으로는 stripConversations 가 옛 항목에서 옮겨 온 대화가 돌아오지 않는다.
//
// = 로 정확히 일치시킨다. 그 이름이 이미 없으면 tmux 는 접두사로 찾아 «name-2» 를 죽인다(17절 실측).
// 복원이 바로 그 «name-2» 를 만드는 기능이다. AttachToSession 이 = 를 쓰는 것과 같은 이유다.
func closeParkedSession(name string, before []ParkedSession) error {
	err := runner.Run("tmux", "kill-session", "-t", "="+name)
	if err == nil {
		return nil
	}
	if runner.Run("tmux", "has-session", "-t", "="+name) != nil {
		return nil // 이미 닫혔다. 항목이 대화로 가는 유일한 길이니 남긴다.
	}
	if rbErr := saveParked(before); rbErr != nil {
		return errors.Join(fmt.Errorf("park %q: %w", name, err), rbErr)
	}
	return fmt.Errorf("park %q: %w", name, err)
}

// placedConversation 은 대화와 그것이 도는 pane 의 "<session>:@<window>.%<pane>" 이다 — 순서를 세우는 데만 쓴다.
type placedConversation struct {
	ref  string
	conv ParkedConversation
}

// sessionConversations 는 tmux 세션 하나에 든 대화 중 되살릴 수 있는 것을 창·pane 순서로 고른다.
// Claude 와 opencode 가 섞여 있으면 둘 다, 한 순서로 세운다. opencode 의 대화를 정할 수 없으면 오류다
// (park_opencode.go) — 그때는 아무것도 적지 않고 닫지도 않는다. parked 는 이미 보관함에 든 대화 id 로,
// opencode 쪽만 쓴다(Claude 는 같은 대화를 다시 보관하면 새 항목으로 옮긴다 — stripConversations).
func sessionConversations(runs []ClaudeRun, session string, parked map[string]bool) ([]ParkedConversation, error) {
	opencode, err := opencodeConversations(session, parked)
	if err != nil {
		return nil, err
	}
	placed := append(claudeConversations(runs, session), opencode...)
	sort.SliceStable(placed, func(i, j int) bool {
		wi, pi := refOrder(placed[i].ref)
		wj, pj := refOrder(placed[j].ref)
		if wi != wj {
			return wi < wj
		}
		return pi < pj
	})
	convs := make([]ParkedConversation, 0, len(placed))
	for _, p := range placed {
		convs = append(convs, p.conv)
	}
	return convs, nil
}

// claudeConversations 는 세션에 든 Claude 대화 중 되살릴 수 있는 것이다.
//
// TmuxSession 은 pane id 로 푼 이름이라, Claude 가 적어 둔 이름과 달리 세션 이름을 바꾼 뒤에도 맞다.
// 기록 파일이 없는 대화는 뺀다 — 갓 띄우고 아직 아무것도 안 시킨 Claude 는 --resume 할 것이 없어,
// 되살리면 pane 이 곧바로 끝나고 세션째 사라진다.
func claudeConversations(runs []ClaudeRun, session string) []placedConversation {
	var placed []placedConversation
	for _, r := range runs {
		if r.TmuxSession != session || r.SessionID == "" || r.CWD == "" {
			continue
		}
		if _, err := transcriptPath(r.SessionID, r.CWD); err != nil {
			continue
		}
		placed = append(placed, placedConversation{ref: r.TmuxRef, conv: ParkedConversation{
			SessionID: r.SessionID,
			CWD:       r.CWD,
			Task:      r.Session.AITask,
			PID:       r.Session.AIPID,
		}})
	}
	return placed
}

// refOrder 는 "<session>:@<window>.%<pane>" 의 창·pane id 다. id 는 만든 순서라 보관할 때마다 같은
// 순서가 나온다(AISince 순이면 상태가 바뀔 때마다 달라진다). 못 읽으면 0 이다.
func refOrder(ref string) (window, pane int) {
	rest := ref
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		rest = ref[i+1:]
	}
	w, p, _ := strings.Cut(rest, ".")
	window, _ = strconv.Atoi(strings.TrimPrefix(w, "@"))
	pane, _ = strconv.Atoi(strings.TrimPrefix(p, "%"))
	return window, pane
}

// stripConversations 는 각 항목에서 convs 의 대화만 뺀 새 목록이다. 같은 대화를 다시 보관하면 그 대화는
// 새 항목으로 옮겨 가고, 옛 항목의 나머지 대화는 남는다 — 항목을 통째로 버리면 손으로 하나만 이어 쓰던
// 대화의 짝까지 잃는다. 대화가 다 빠진 항목은 normalizeParked 가 버린다.
func stripConversations(list []ParkedSession, convs []ParkedConversation) []ParkedSession {
	ids := make(map[string]bool, len(convs))
	for _, c := range convs {
		ids[c.SessionID] = true
	}
	out := make([]ParkedSession, 0, len(list))
	for _, p := range list {
		p.Conversations = slices.DeleteFunc(slices.Clone(p.Conversations),
			func(c ParkedConversation) bool { return ids[c.SessionID] })
		out = append(out, p)
	}
	return out
}

// parkedIDs 는 보관함에 든 대화 id 들이다.
func parkedIDs(list []ParkedSession) map[string]bool {
	ids := make(map[string]bool)
	for _, p := range list {
		for _, c := range p.Conversations {
			ids[c.SessionID] = true
		}
	}
	return ids
}

func containsParked(list []ParkedSession, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}

// waitParkedExit 는 보관한 Claude 가 끝나기를 parkExitTimeout 까지 기다린다. 끝나기 전에 복원하면
// 그 프로세스가 «tmux 밖에서 살아 있는 사본» 으로 보인다.
func waitParkedExit(convs []ParkedConversation) {
	deadline := time.Now().Add(parkExitTimeout)
	for {
		alive := false
		for _, c := range convs {
			if c.PID > 0 && isAlive(c.PID, "") {
				alive = true
				break
			}
		}
		if !alive || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// RestoreParked 는 보관한 세션을 되살리고 attach 할 세션 이름을 돌려준다.
//
// 이름이 있으면 오류가 있어도 그리로 간다 — 띄우지 못한 대화만 보관함에 남아 다음에 Parked 행으로
// 다시 보인다. TUI 가 닫힌 뒤라 경고를 보여 줄 화면이 없으므로, 그 남은 항목이 실패의 보고다.
// 이름이 빈 것은 아무것도 띄우지 못했다는 뜻이고, 그때 항목은 그대로다.
func RestoreParked(p ParkedSession) (string, error) {
	resetClaudeRunCache()
	plan := planUnpark(p, ClaudeRuns())
	name, launchErrs := launchUnpark(p, &plan)
	errs := append(plan.errs, launchErrs...)
	if name != "" {
		if err := keepParked(p.ID, plan.remaining); err != nil {
			errs = append(errs, err)
		}
	}
	return name, errors.Join(errs...)
}

// unparkLaunch 는 띄울 대화 하나와 그 명령이다.
type unparkLaunch struct {
	conv ParkedConversation
	argv []string
}

// unparkPlan 은 대화마다 정한 것이다: 띄울 것, 못 띄워 남길 것, 이미 살아 있는 세션.
type unparkPlan struct {
	launches  []unparkLaunch
	remaining []ParkedConversation
	errs      []error
	live      string
}

// planUnpark 는 대화마다 무엇을 할지 정한다. tmux 는 부르지 않는다 — opencode 대화는 opencode 에 아직
// 있는지만 묻는다(planOpencode).
func planUnpark(p ParkedSession, runs []ClaudeRun) unparkPlan {
	var plan unparkPlan
	for _, c := range p.Conversations {
		if c.Tool == toolOpencode {
			plan.planOpencode(c)
			continue
		}
		run, alive := liveCopy(runs, c)
		switch {
		case alive && run.TmuxSession != "":
			// 이미 tmux 에 있다. 또 띄우면 한 기록에 두 프로세스가 붙는다 — 그리로 간다.
			if plan.live == "" {
				plan.live = run.TmuxSession
			}
		case !isDir(c.CWD):
			plan.keep(c, fmt.Errorf("restore %s: directory %s is gone", shortID(c.SessionID), c.CWD))
		case !hasTranscript(c):
			// Claude 는 오래된 기록을 스스로 지운다. 기록 없이 --resume 하면 pane 이 곧바로 끝나는데,
			// 그것을 «띄움» 으로 세면 항목까지 지워진다.
			plan.keep(c, fmt.Errorf("restore %s: its transcript is gone", shortID(c.SessionID)))
		default:
			fork := alive && ClaudeBusyElsewhere(run)
			plan.launches = append(plan.launches, unparkLaunch{
				conv: c,
				argv: append([]string{"claude"}, ClaudeResumeArgs(c.SessionID, fork)...),
			})
		}
	}
	return plan
}

func (plan *unparkPlan) keep(c ParkedConversation, err error) {
	plan.remaining = append(plan.remaining, c)
	plan.errs = append(plan.errs, err)
}

func hasTranscript(c ParkedConversation) bool {
	_, err := transcriptPath(c.SessionID, c.CWD)
	return err == nil
}

// launchUnpark 는 계획대로 띄우고 attach 할 이름을 돌려준다. 못 띄운 대화는 plan.remaining 에 더한다.
//
// 첫 대화가 세션을 열고 나머지는 창을 하나씩 연다. 대상은 전부 «=name:» 이다 — 콜론 없는 이름은 그 세션이
// 없을 때 접두사로 «name-2» 를 고른다(17절 실측, set-option 도 같다).
func launchUnpark(p ParkedSession, plan *unparkPlan) (string, []error) {
	if len(plan.launches) == 0 {
		return plan.live, nil
	}
	first := plan.launches[0]
	name, err := CreateSessionWithCommand(p.Name, first.conv.CWD, first.argv)
	if err != nil {
		for _, l := range plan.launches {
			plan.remaining = append(plan.remaining, l.conv)
		}
		return plan.live, []error{err}
	}
	target := "=" + name + ":"
	var errs []error
	for _, l := range plan.launches[1:] {
		args := append([]string{"new-window", "-d", "-t", target, "-c", tmuxDirArg(l.conv.CWD)}, l.argv...)
		if err := runner.Run("tmux", args...); err != nil {
			plan.remaining = append(plan.remaining, l.conv)
			errs = append(errs, fmt.Errorf("restore %s: %w", shortID(l.conv.SessionID), err))
		}
	}
	// 둘 다 best-effort 다. 태그나 메모가 빠진 세션은 실패가 아니라 옛 동작이다.
	if p.ProjectDir != "" {
		_ = runner.Run("tmux", "set-option", "-t", target, projectDirOption, p.ProjectDir)
	}
	if note := sanitizeNote(p.Note); note != "" {
		_ = runner.Run("tmux", "set-option", "-t", target, noteOption, note)
	}
	return name, errs
}

// liveCopy 는 같은 대화를 들고 있는 살아 있는 프로세스를 찾는다. 보관할 때 닫은 그 프로세스는 뺀다.
func liveCopy(runs []ClaudeRun, c ParkedConversation) (ClaudeRun, bool) {
	for _, r := range runs {
		if r.SessionID != c.SessionID {
			continue
		}
		if c.PID > 0 && r.Session.AIPID == c.PID {
			continue
		}
		return r, true
	}
	return ClaudeRun{}, false
}

// keepParked 는 항목 id 의 대화를 remaining 으로 줄이고, 비면 항목을 지운다.
func keepParked(id string, remaining []ParkedConversation) error {
	if len(remaining) == 0 {
		return ForgetParked(id)
	}
	list, err := LoadParked()
	if err != nil {
		return err
	}
	next := make([]ParkedSession, 0, len(list))
	for _, p := range list {
		if p.ID == id {
			p.Conversations = remaining
		}
		next = append(next, p)
	}
	return saveParked(next)
}

// ForgetParked 는 항목을 보관함에서 지운다. 대화 기록은 건드리지 않는다 — claude --resume 의 목록에서
// 여전히 찾을 수 있다. 없는 id 는 할 일이 없는 것이지 오류가 아니다.
func ForgetParked(id string) error {
	list, err := LoadParked()
	if err != nil {
		return err
	}
	next := make([]ParkedSession, 0, len(list))
	for _, p := range list {
		if p.ID != id {
			next = append(next, p)
		}
	}
	if len(next) == len(list) {
		return nil
	}
	return saveParked(next)
}
