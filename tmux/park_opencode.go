package tmux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// opencode 대화의 보관 — park.go 가 Claude 대화에 하는 일을 opencode 에 한다.
//
// Claude 는 «어느 pane 이 어느 대화를 열고 있는지» 를 상태 파일에 적지만 opencode 는 어디에도 적지 않는다
// (2.0.29 실측: 상태 디렉터리·로그·서버 API 어디에도 없다). 그래서 TUI 가 뜬 뒤 그 폴더에서 갱신된 최상위
// 대화를 전부 그 pane 의 대화로 본다. 같은 폴더에 opencode 가 또 있으면 고르지 않는다. 근거와 한계는
// docs/design/park.md.

// opencodeStartSlackMs 는 «TUI 가 뜬 뒤» 를 셀 때의 여유다. 프로세스 시작 시각은 부팅 시각(초 단위)에
// 틱을 더해 얻으므로 1초까지 어긋난다. 늦게 잡으면 TUI 가 뜨자마자 만든 대화를 놓친다.
const opencodeStartSlackMs = 1000

// userHZ 는 /proc/<pid>/stat 의 시각 단위다. 리눅스는 사용자 공간에 늘 100 으로 보인다(USER_HZ).
const userHZ = 100

// opencodeCallTimeout 은 opencode API 호출 하나의 한도다. 보통 60ms 안팎이지만 서비스가 멎으면 끝나지 않는다 —
// 그러면 보관 Cmd 가 돌아오지 않아 TUI 의 p 가 전부 «still parking» 으로 막힌다. 서비스를 새로 띄우는 첫
// 호출도 견디게 넉넉히 잡는다.
const opencodeCallTimeout = 10 * time.Second

// opencodeProc 는 살아 있는 opencode 클라이언트 프로세스 하나다 — TUI, 또는 대화를 만드는 run·mini·acp.
// 백그라운드 서비스(serve)와 관리 명령, 원격 서버(--server)에 붙은 것은 들지 않는다.
type opencodeProc struct {
	PID int
	// Lineage 는 PID 에서 init 쪽으로 올라간 조상들이다(자신 포함). pane 의 첫 프로세스가 이 안에 있으면
	// 그 pane 에서 돈다 — pane 명령으로 띄웠든, 셸에서 띄웠든, npm 래퍼 밑이든.
	Lineage []int
	// Exe 는 API 를 묻고 되살릴 때 쓰는 실행 파일이다. 설치 스크립트는 ~/.opencode/bin 에 두는데, tmux
	// 서버의 PATH 에는 그 폴더가 없을 수 있다.
	Exe string
	// Dir 은 대화가 열리는 폴더다. opencode 는 폴더 인자를 받으면 그리로 chdir 하므로 cwd 하나로 된다(실측).
	Dir string
	// StartMs 는 프로세스가 뜬 시각(Unix ms)이다.
	StartMs int64
	// TUI 는 대화형 화면인지다. 보관하는 것은 TUI 뿐이고, 나머지는 같은 폴더를 모호하게 만들 뿐이다.
	TUI bool
	// Session 은 -s/--session 으로 연 대화 id, Continuing 은 -c/--continue 다.
	Session    string
	Continuing bool
}

// opencodeInvocation 은 opencode 명령줄에서 읽은 것이다.
type opencodeInvocation struct {
	tui        bool
	session    string
	continuing bool
}

// 값을 받는 플래그 — 그 값을 위치 인자로 읽지 않으려고 안다. 모르는 플래그의 값은 위치 인자로 읽히지만,
// 폴더는 cwd 로 읽고 첫 위치 인자 뒤의 위치 인자는 넘기므로 해가 없다.
var opencodeValueFlags = map[string]bool{
	"-s": true, "--session": true, "--prompt": true, "--log-level": true,
}

// opencodeClientCommands 는 대화를 만들 수 있는 하위 명령이다. 보관하지는 않지만 같은 폴더에 있으면
// 그 폴더의 새 대화가 누구 것인지 모르게 한다.
var opencodeClientCommands = map[string]bool{"run": true, "mini": true, "acp": true}

// opencodeOtherCommands 는 대화를 들고 있지 않은 하위 명령이다(2.0.29·2.2.7 의 --help, update 는 2.0.29 뿐).
var opencodeOtherCommands = map[string]bool{
	"upgrade": true, "update": true, "uninstall": true, "api": true, "debug": true, "auth": true, "mcp": true,
	"plugin": true, "models": true, "stats": true, "session": true, "service": true, "reload": true,
	"pair": true, "serve": true, "help": true,
}

// parseOpencodeArgs 는 opencode 의 argv[1:] 를 읽는다. ok 가 거짓이면 이 기계의 대화를 들고 있지 않은 프로세스다.
func parseOpencodeArgs(args []string) (opencodeInvocation, bool) {
	inv := opencodeInvocation{tui: true}
	positional := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			name, val, hasVal := strings.Cut(a, "=")
			switch name {
			case "-s", "--session":
				if !hasVal && i+1 < len(args) {
					i++
					val = args[i]
				}
				inv.session = val
			case "-c", "--continue":
				inv.continuing = true
			case "--server":
				// 원격 서버의 대화는 이 기계의 서비스 목록에 없다. 같은 폴더의 로컬 대화를 고르면 틀린 대화다.
				return opencodeInvocation{}, false
			case "-h", "--help", "-v", "--version", "--completions":
				return opencodeInvocation{}, false
			default:
				if opencodeValueFlags[name] && !hasVal {
					i++
				}
			}
			continue
		}
		// 첫 위치 인자가 하위 명령이면 그것이 정한다. 아니면 폴더 인자이고, 그 뒤의 플래그도 읽는다
		// («opencode . -s <id>»). 뒤의 위치 인자는 모르는 플래그의 값일 수 있어 넘긴다.
		if positional {
			continue
		}
		positional = true
		if opencodeClientCommands[a] {
			return opencodeInvocation{}, true
		}
		if opencodeOtherCommands[a] {
			return opencodeInvocation{}, false
		}
	}
	return inv, true
}

// scanOpencodeProcs 는 살아 있는 opencode 클라이언트를 모은다. 테스트가 바꾼다.
var scanOpencodeProcs = readOpencodeProcs

// readOpencodeProcs 는 /proc 를 훑는다. /proc 가 없으면(macOS) 아무것도 없다 — opencode 는 보관할 수 없다.
func readOpencodeProcs() []opencodeProc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	bootMs, ok := bootTimeMs()
	if !ok {
		return nil
	}
	var procs []opencodeProc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if p, ok := readOpencodeProc(pid, bootMs); ok {
			procs = append(procs, p)
		}
	}
	return procs
}

func readOpencodeProc(pid int, bootMs int64) (opencodeProc, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(raw) == 0 {
		return opencodeProc{}, false
	}
	argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if filepath.Base(argv[0]) != "opencode" {
		return opencodeProc{}, false
	}
	inv, ok := parseOpencodeArgs(argv[1:])
	if !ok {
		return opencodeProc{}, false
	}
	dir := processCwd(pid)
	ticks, ok := readProcStart(pid)
	if !ok || dir == "" {
		return opencodeProc{}, false // 그사이 끝났다
	}
	t, err := strconv.ParseInt(ticks, 10, 64)
	if err != nil {
		return opencodeProc{}, false
	}
	return opencodeProc{
		PID:        pid,
		Lineage:    procLineage(pid),
		Exe:        opencodeExe(pid, argv[0]),
		Dir:        dir,
		StartMs:    bootMs + t*1000/userHZ,
		TUI:        inv.tui,
		Session:    inv.session,
		Continuing: inv.continuing,
	}, true
}

// opencodeExe 는 그 프로세스의 실행 파일 경로다. 절대 경로로 띄웠으면 그 경로를 쓴다 — 심링크라도 그쪽이
// 업그레이드를 견딘다. 아니면 /proc 의 exe 이고, 업그레이드로 지워진 파일이면 « (deleted)» 를 뗀다.
func opencodeExe(pid int, argv0 string) string {
	if filepath.IsAbs(argv0) {
		return argv0
	}
	if exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil {
		return strings.TrimSuffix(exe, " (deleted)")
	}
	return argv0
}

// bootTimeMs 는 부팅 시각(Unix ms)이다 — /proc/stat 의 btime.
func bootTimeMs() (int64, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if rest, ok := bytes.CutPrefix(line, []byte("btime ")); ok {
			s, err := strconv.ParseInt(strings.TrimSpace(string(rest)), 10, 64)
			return s * 1000, err == nil
		}
	}
	return 0, false
}

// procLineage 는 pid 와 그 조상들이다. pidUnder 와 같은 한도로 걷는다.
func procLineage(pid int) []int {
	lineage := []int{pid}
	for i := 0; i < 32 && pid > 1; i++ {
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if err != nil {
			break
		}
		ppid := 0
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "PPid:"); ok {
				ppid, _ = strconv.Atoi(strings.TrimSpace(rest))
				break
			}
		}
		if ppid <= 0 {
			break
		}
		lineage = append(lineage, ppid)
		pid = ppid
	}
	return lineage
}

// opencodeConversations 는 tmux 세션 하나에서 도는 opencode TUI 들의 대화를 고른다. parked 는 이미 보관함에
// 든 대화 id 다 — 그 대화는 다른 항목의 것이라 옮기지 않는다.
//
// 대화를 아직 안 연 TUI(첫 화면)는 건너뛴다 — Claude 의 기록 없는 대화와 같다. 대화를 정할 수 없으면
// (같은 폴더의 다른 opencode, 답하지 않는 opencode, 읽히지 않는 pane 목록) 오류다. 그때 보관은 세션을 닫지 않는다.
func opencodeConversations(session string, parked map[string]bool) ([]placedConversation, error) {
	procs := scanOpencodeProcs()
	if len(procs) == 0 {
		return nil, nil // opencode 가 하나도 없으면 tmux 에 묻지도 않는다
	}
	panes, err := sessionPanes(session)
	if err != nil {
		// 삼키면 섞인 세션에서 Claude 대화만 적고 opencode 의 대화를 둔 채 세션을 닫는다.
		return nil, fmt.Errorf("list panes to find opencode: %w", err)
	}
	var placed []placedConversation
	for _, pane := range panes {
		p, ok := opencodeInPane(procs, pane.pid)
		if !ok {
			continue
		}
		convs, err := opencodePaneConversations(p, procs, parked)
		if err != nil {
			return nil, err
		}
		ref := session + ":" + pane.window + "." + pane.id
		for _, c := range convs {
			placed = append(placed, placedConversation{ref: ref, conv: c})
		}
	}
	return placed, nil
}

type tmuxPane struct {
	window, id string
	pid        int
}

// sessionPanes 는 세션 하나의 pane 들이다. 세션 이름은 공백을 담을 수 있어 마지막 필드로 둔다.
func sessionPanes(session string) ([]tmuxPane, error) {
	out, err := runner.Output("tmux", "list-panes", "-a", "-F", "#{window_id} #{pane_id} #{pane_pid} #{session_name}")
	if err != nil {
		return nil, err
	}
	var panes []tmuxPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, " ", 4)
		if len(f) != 4 || f[3] != session {
			continue
		}
		pid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		panes = append(panes, tmuxPane{window: f[0], id: f[1], pid: pid})
	}
	return panes, nil
}

// opencodeInPane 은 pane 의 첫 프로세스 밑에서 도는 opencode TUI 다. 여럿이면 가장 깊은 것 — 래퍼가
// opencode 이름으로 진짜 opencode 를 띄운 경우 진짜 쪽이다.
func opencodeInPane(procs []opencodeProc, panePID int) (opencodeProc, bool) {
	var best opencodeProc
	found := false
	for _, p := range procs {
		if !p.TUI || !containsInt(p.Lineage, panePID) {
			continue
		}
		if !found || len(p.Lineage) > len(best.Lineage) {
			best, found = p, true
		}
	}
	return best, found
}

// sharesFolder 는 p 와 같은 폴더에서 도는 다른 opencode 클라이언트를 찾는다. p 의 조상·자손(래퍼)은 남이 아니다.
func sharesFolder(procs []opencodeProc, p opencodeProc) (opencodeProc, bool) {
	for _, o := range procs {
		if o.PID == p.PID || o.Dir != p.Dir || containsInt(o.Lineage, p.PID) || containsInt(p.Lineage, o.PID) {
			continue
		}
		return o, true
	}
	return opencodeProc{}, false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// opencodePaneConversations 는 TUI 하나의 대화를 고른다. 비었으면 아직 대화가 없다.
//
//  1. TUI 가 뜬 뒤 그 폴더에서 갱신된 최상위 대화 전부(최신 먼저). 사람이 친 대화는 여기서 잡힌다. 하나만
//     고르지 않는다 — TUI 는 탭으로 여러 대화를 오가고, 이미 닫힌 다른 클라이언트가 남긴 갱신과 가릴 길이 없다.
//     전부 적으면 잃는 대화가 없다(되살리면 창이 하나 더 열릴 뿐이다). 같은 폴더에 다른 opencode 가 살아
//     있으면 그 갱신이 지금도 섞이므로 고르지 않는다.
//  2. 없으면 -s 로 연 대화 — 되살린 뒤 아직 아무것도 안 친 경우다.
//  3. 없으면 -c 가 이은 대화, 곧 그 폴더의 가장 최근 대화. 1이 비었으니 TUI 가 뜬 뒤 바뀐 것이 없다.
//
// 보관함에 이미 든 대화는 어느 단계에서도 고르지 않는다.
func opencodePaneConversations(p opencodeProc, procs []opencodeProc, parked map[string]bool) ([]ParkedConversation, error) {
	list, err := opencodeSessions(p.Exe, p.Dir)
	if err != nil {
		return nil, fmt.Errorf("opencode in %s: list its conversations: %w", p.Dir, err)
	}
	conv := func(s opencodeSession) ParkedConversation {
		return ParkedConversation{SessionID: s.ID, CWD: p.Dir, Task: s.Title, PID: p.PID, Tool: toolOpencode, Command: p.Exe}
	}

	since := p.StartMs - opencodeStartSlackMs
	var touched []ParkedConversation
	for _, s := range list {
		if s.Time.Updated >= since && !parked[s.ID] {
			touched = append(touched, conv(s))
		}
	}
	if len(touched) > 0 {
		if other, shared := sharesFolder(procs, p); shared {
			return nil, fmt.Errorf("another opencode (pid %d) works in %s too — can't tell which conversation is this session's",
				other.PID, p.Dir)
		}
		return touched, nil
	}

	if p.Session != "" && !parked[p.Session] {
		for _, s := range list {
			if s.ID == p.Session {
				return []ParkedConversation{conv(s)}, nil
			}
		}
		s, ok, err := opencodeSessionInfo(p.Exe, p.Session)
		if err != nil {
			return nil, fmt.Errorf("opencode in %s: look up %s: %w", p.Dir, p.Session, err)
		}
		if ok {
			return []ParkedConversation{conv(s)}, nil
		}
	}
	if p.Continuing && len(list) > 0 && !parked[list[0].ID] {
		return []ParkedConversation{conv(list[0])}, nil
	}
	return nil, nil
}

// opencodeSession 은 opencode API 가 돌려주는 대화 하나(Session.Info)에서 쓰는 것만이다.
type opencodeSession struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID"`
	Title    string `json:"title"`
	Time     struct {
		Updated  int64 `json:"updated"`
		Archived int64 `json:"archived"`
	} `json:"time"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

// opencodeSessions 는 폴더 하나의 최상위 대화를 갱신순(최신 먼저)으로 묻는다. 보관(archived)된 것은 뺀다.
//
// SQLite 를 직접 읽지 않고 opencode 의 API 를 거친다. 스키마(session_v2)는 버전마다 바뀌고, Go 에서 읽으려면
// 드라이버가 든다. «opencode session list» 는 «현재 프로젝트» 를 cwd 로 정해 폴더를 지목할 수 없다.
//
// 폴더는 답에서도 다시 맞춘다. 필드가 비었으면 다른 것으로 본다 — 다음 버전에서 이름이 바뀌고 서버가
// directory 를 무시하게 되면, 너그러운 비교는 다른 폴더의 최신 대화를 고른다.
func opencodeSessions(bin, dir string) ([]opencodeSession, error) {
	out, err := outputWithin(opencodeCallTimeout, bin, "api", "session.list", "--param", "directory="+dir,
		"--param", "parentID=null", "--param", "order=desc", "--param", "limit=50")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []opencodeSession `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("unexpected answer from opencode: %w", err)
	}
	var list []opencodeSession
	for _, s := range resp.Data {
		if s.ID == "" || s.ParentID != "" || s.Time.Archived > 0 || s.Location.Directory != dir {
			continue
		}
		list = append(list, s)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.Updated > list[j].Time.Updated })
	return list, nil
}

// opencodeSessionInfo 는 대화 하나를 묻는다. 없으면 ok 가 거짓이고 오류는 없다. 묻는 폴더와 상관없이 답한다
// (2.2.7 실측: 다른 프로젝트의 대화도 /tmp 에서 같은 답).
//
// 없는 대화의 답은 종료 코드 1 에, stdout 의 SessionNotFoundError 본문과 stderr 의 «HTTP 404 Not Found» 다
// (2.2.7 실측, 서비스·--standalone 모두). 종료 코드만 보면 «서비스가 죽었다» 와 같아 보이므로 본문으로 먼저
// 가린다 — 없는 대화는 «물을 수 없음» 이 아니라 «없음» 이다.
func opencodeSessionInfo(bin, id string) (opencodeSession, bool, error) {
	out, err := outputWithin(opencodeCallTimeout, bin, "api", "session.get", "--param", "sessionID="+id)
	var resp struct {
		Tag  string           `json:"_tag"`
		Data *opencodeSession `json:"data"`
	}
	// 첫 JSON 값만 읽는다 — 두 흐름이 한데 모이면 본문 뒤에 404 줄이 붙는다.
	decodeErr := json.NewDecoder(bytes.NewReader(out)).Decode(&resp)
	if decodeErr == nil && resp.Tag == "SessionNotFoundError" {
		return opencodeSession{}, false, nil
	}
	if err != nil {
		return opencodeSession{}, false, err
	}
	if decodeErr != nil {
		return opencodeSession{}, false, fmt.Errorf("unexpected answer from opencode: %w", decodeErr)
	}
	if resp.Data == nil || resp.Data.ID != id {
		return opencodeSession{}, false, nil
	}
	return *resp.Data, true, nil
}

// 되살릴 실행 파일을 고르는 데 쓰는 것들이다. 테스트가 바꾼다.
var (
	opencodeLookPath  = exec.LookPath
	opencodeBinUsable = isExecutableFile
)

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// opencodeBin 은 되살릴 때 띄울 opencode 다 — 보관할 때 돌던 그 실행 파일. 그 경로가 사라졌으면(버전마다
// 폴더를 따로 쓰는 설치의 업그레이드) mux 의 PATH 에서 찾는다. 거기도 없으면 적은 경로 그대로 두고, 묻기가
// 실패해 항목이 남는 것이 보고다.
func opencodeBin(c ParkedConversation) string {
	if c.Command != "" && opencodeBinUsable(c.Command) {
		return c.Command
	}
	if p, err := opencodeLookPath("opencode"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs // tmux 서버의 PATH 는 mux 의 것과 다를 수 있다
		}
		return p
	}
	if c.Command != "" {
		return c.Command
	}
	return "opencode"
}

// planOpencode 는 opencode 대화 하나를 띄울지 정한다. 이미 열린 사본이 있어도 또 띄운다 — opencode 의 TUI 는
// 서버의 클라이언트라 한 대화에 둘이 붙어도 된다(Claude 처럼 한 기록 파일에 두 프로세스가 쓰지 않는다).
func (plan *unparkPlan) planOpencode(c ParkedConversation) {
	if !isDir(c.CWD) {
		plan.keep(c, fmt.Errorf("restore %s: directory %s is gone", shortID(c.SessionID), c.CWD))
		return
	}
	bin := opencodeBin(c)
	// opencode 는 없는 id 의 -s 를 새 대화로 받는다. 그렇게 띄우면 빈 대화가 «되살림» 으로 세어져 항목이 지워진다.
	_, ok, err := opencodeSessionInfo(bin, c.SessionID)
	if err != nil {
		plan.keep(c, fmt.Errorf("restore %s: ask opencode: %w", shortID(c.SessionID), err))
		return
	}
	if !ok {
		plan.keep(c, fmt.Errorf("restore %s: opencode no longer has this conversation", shortID(c.SessionID)))
		return
	}
	plan.launches = append(plan.launches, unparkLaunch{conv: c, argv: []string{bin, "-s", c.SessionID}})
}
