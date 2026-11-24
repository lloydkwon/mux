package herdr

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// herdr 가 지금 쥐고 있는 Claude 대화 — `mux --claude` 가 입양하거나 여기서 이어가지 않게.
// 설계 이유는 docs/design/claude-mode.md «herdr 가 쥔 대화».

// holdsLimit 는 agent list 한 번의 제한 시간이다. 이 답은 --claude 의 스캔 안에서 기다린다 — 서버가 멎어 답이
// 없으면 목록이 멈춘다. 끊기면 «못 읽음» 이고, 그때는 tmux 밖 대화를 모두 쥔 것으로 본다.
var holdsLimit = 2 * time.Second

// heldBy 는 미리보기와 상태 줄에 보이는 쥔 쪽의 이름이다.
const heldBy = "herdr"

// ClaudeHolds 는 herdr 의 pane 에서 도는 Claude 대화를 tmux.ExternalHolds 로 돌려준다. tmux.SetExternalHolds 에
// 그대로 넘긴다.
//
//   - herdr 가 없거나 서버가 꺼져 있으면 쥔 것이 없다. 서버가 없을 때 herdr 는 stdout 을 비우고 오류 봉투를 stderr 로
//     낸다(종료 코드 1, 0.9.5 실측 — scripts/herdr-assumptions.sh 9절). 러너가 stderr 를 오류에 붙이므로 거기서
//     server_not_running 을 찾는다.
//   - 대화 id 를 알리면(agent_session, kind "id") 그 id 를 쥔 것이다. 알리지 않은 Claude 에이전트는 그 폴더의
//     tmux 밖 대화를 모두 쥔 것으로 본다 — herdr 의 Claude 통합이 빠졌거나 아직 보고하기 전이다.
//   - 그 밖의 실패는 «못 읽음» 이다.
func ClaudeHolds() tmux.ExternalHolds {
	h := tmux.ExternalHolds{By: heldBy, IDs: map[string]bool{}, Dirs: map[string]bool{}}
	bin, err := binPath()
	if err != nil {
		return h // herdr 가 없으면 herdr 가 쥔 것도 없다
	}
	out, err := outputWithin(holdsLimit, bin, "agent", "list")
	if err != nil {
		if strings.Contains(err.Error(), "server_not_running") {
			return h
		}
		h.Unknown = true
		return h
	}
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil || env.Error != nil {
		h.Unknown = true
		return h
	}
	for _, a := range env.Result.Agents {
		if a.Agent != "claude" {
			continue
		}
		if s := a.AgentSession; s != nil && s.Kind == "id" && s.Value != "" {
			h.IDs[s.Value] = true
			continue
		}
		// Claude 가 적는 cwd 는 그 프로세스의 폴더(물리 경로)다. herdr 의 cwd 는 pane 의 폴더, foreground_cwd 는
		// 앞에서 도는 프로세스의 폴더라 둘 다 넣고, 끝 / 를 걷고 심링크를 푼 꼴도 넣는다 — 문자열이 정확히 같을 때만
		// 맞으므로, 놓치는 쪽(같은 id 를 두 프로세스가 잡는다)을 줄인다.
		for _, dir := range []string{a.CWD, a.ForegroundCWD} {
			addDir(h.Dirs, dir)
		}
	}
	return h
}

// addDir 는 폴더를 그대로, 정리한 꼴로, 심링크를 푼 꼴로 넣는다.
func addDir(dirs map[string]bool, dir string) {
	if dir == "" {
		return
	}
	dirs[dir] = true
	clean := filepath.Clean(dir)
	dirs[clean] = true
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		dirs[real] = true
	}
}
