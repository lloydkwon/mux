package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lloydkwon/mux/tmux"
)

// 화면 전체를 글자 그대로 고정하는 골든 테스트.
//
// 다른 레이아웃 테스트는 «줄 수가 맞다»·«폭이 맞다» 같은 성질만 본다. 그것으로는 컬럼이
// 한 칸 밀리거나, 배지가 브랜치 자리를 먹거나, 말줄임표가 엉뚱한 데서 생기는 것을
// 못 잡는다 — 지금까지 그건 사람이 화면을 보고서야 알았다. 여기서는 화면을 통째로
// testdata/golden 에 적어 두고 한 글자라도 바뀌면 실패한다. 의도한 변경이면
//
//	go test ./ui -run TestGolden -update
//
// 으로 다시 쓰고, 커밋 diff 에서 바뀐 화면을 눈으로 확인한다. 그 diff 가 이 테스트의
// 진짜 산출물이다.
//
// 결정적이어야 하므로 지키는 것 셋:
//   - NewModel 을 쓰지 않는다. 사용자의 preferences.json(정렬·순서)을 읽어 사람마다 다르게 그린다.
//   - 시각은 전부 제로값이다. 나이 컬럼이 time.Now 에 묶이면 매번 다르다(제로는 빈칸으로 그려진다).
//   - ANSI 는 걷어낸다. 색은 TestSelectedRowDropsSegmentColors 같은 테스트의 몫이고, 여기는 배치다.
//     줄 끝에 «|» 를 붙여 오른쪽 공백까지 diff 에 보이게 한다.
//   - 폭을 넘는 줄은 화면 폭에서 자른다. Bubble Tea 의 렌더러가 그렇게 그리므로(standard_renderer 의
//     ansi.Truncate) 골든도 «실제로 보이는 화면»이어야 한다 — 도움말 줄은 80칸에서 늘 잘려 있다.
var update = flag.Bool("update", false, "골든 파일을 지금 출력으로 다시 쓴다")

func goldenSessions() []tmux.Session {
	return []tmux.Session{
		{Name: "mux", WindowCount: 2, Attached: true, Directory: "/home/u/dev/mux",
			GitBranch: "main", ActiveCommand: "claude", AIState: tmux.AIStateWorking, AITool: "claude"},
		{Name: "api-server", WindowCount: 1, Directory: "/home/u/dev/api",
			GitBranch: "feat/long-branch-name-that-must-yield", ActiveCommand: "claude",
			AIState: tmux.AIStateApproval, AITool: "claude", AIWaitingFor: "Bash: git push"},
		{Name: "notes", WindowCount: 1, Directory: "/home/u/notes", ActiveCommand: "zsh",
			Note: "회의록 정리 중"},
		{Name: "a-session-with-a-rather-long-name", WindowCount: 3, Directory: "/tmp",
			ActiveCommand: "codex", AIState: tmux.AIStateReady, AITool: "codex"},
	}
}

func goldenModel(w, h int) Model {
	m := Model{tree: newTreeState(), width: w, height: h}
	m.sessions = goldenSessions()
	m.prefs.Sort = sortName // 이름순 — 최근순은 시각을 보고, 시각은 전부 제로다
	m.applyFilter()
	return m
}

func checkGolden(t *testing.T, name string, width int, got string) {
	t.Helper()
	var b strings.Builder
	for _, l := range strings.Split(ansi.Strip(got), "\n") {
		b.WriteString(ansi.Truncate(l, width, ""))
		b.WriteString("|\n")
	}
	path := filepath.Join("testdata", "golden", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v — 처음이면 go test ./ui -run TestGolden -update", err)
	}
	if string(want) != b.String() {
		t.Errorf("%s 의 화면이 바뀌었다. 의도한 것이면 -update 로 다시 쓰고 diff 를 확인한다.\n--- want\n%s--- got\n%s",
			path, want, b.String())
	}
}

func TestGoldenMainList(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"main-120x20", 120, 20},
		{"main-80x16", 80, 16},
		{"main-60x12", 60, 12}, // 좁아서 브랜치가 먼저 양보하는 폭
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := goldenModel(tc.w, tc.h)
			// 커서를 둘째 세션에 두고, 그 행의 프리뷰를 채운다 — 프리뷰는 키가 맞을 때만 그려진다
			m.focusItemSession("api-server")
			if it := m.currentItem(); it != nil {
				m.previewKey = previewKeyForItem(*it)
				m.previewContent = "$ git push\n? Allow Bash: git push  (y/n)"
			}
			checkGolden(t, tc.name, tc.w, m.View())
		})
	}
}

func TestGoldenFilteredList(t *testing.T) {
	m := goldenModel(100, 14)
	m.filterText = "api"
	m.applyFilter()
	checkGolden(t, "filtered-100x14", 100, m.View())
}

func TestGoldenHelp(t *testing.T) {
	m := goldenModel(68, 19) // mux popup 이 80x24 터미널에서 얻는 크기 (TestHelpBodyFitsPopup 과 같은 예산)
	m.mode = modeHelp
	checkGolden(t, "help-68x19", 68, m.View())
}

func TestGoldenPanel(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"panel-36x24", 36, 24}, // defaultPanelWidth
		{"panel-48x24", 48, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := watchTestModel(tc.w, tc.h)
			m.sessions = goldenSessions()
			m.ownSession = "mux"
			m.selected = "api-server"
			checkGolden(t, tc.name, tc.w, m.View())
		})
	}
}
