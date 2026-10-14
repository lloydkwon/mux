package tmux

import "testing"

// VS Code 클라이언트가 여럿이면 가장 최근에 쓴 것에 맞춘다. 물러나던 시절에는 숨은
// 터미널 탭 하나(80x30, 몇 시간째 유휴)가 붙어 있는 것만으로 fit 이 풀려, 큰 터미널이
// 다른 세션으로 떠나는 순간 창이 VS Code 폭으로 줄고 패널이 VS Code 화면에 들어왔다
// (2026-10-14 실측). 다섯째 칸은 client_activity 다.
func TestParseClients(t *testing.T) {
	isVS := func(pid int) bool { return pid == 200 || pid == 300 }

	cases := []struct {
		name      string
		out       string
		wantVS    vsClient
		wantOther vsClient
		wantOk    bool
	}{
		{"VS Code 하나 + WT 하나", "100 269 69 /dev/pts/6\n200 120 24 /dev/pts/9\n",
			vsClient{120, 24, "/dev/pts/9"}, vsClient{269, 69, ""}, true},
		{"VS Code 없음", "100 269 69 /dev/pts/6\n", vsClient{}, vsClient{269, 69, ""}, false},
		{"VS Code 둘이면 최근 것", "200 120 24 /dev/pts/9 2000\n300 80 30 /dev/pts/8 1000\n",
			vsClient{120, 24, "/dev/pts/9"}, vsClient{}, true},
		{"VS Code 둘, 순서가 바뀌어도 최근 것", "300 80 30 /dev/pts/8 1000\n200 183 55 /dev/pts/9 2000\n",
			vsClient{183, 55, "/dev/pts/9"}, vsClient{}, true},
		// 같은 초에 붙으면 활동 시각으로 못 가린다. 숨은 탭은 대개 기본 크기라 큰 쪽이 보는 쪽이다.
		{"활동 시각이 같으면 큰 것", "300 80 30 /dev/pts/8 1000\n200 120 24 /dev/pts/9 1000\n",
			vsClient{120, 24, "/dev/pts/9"}, vsClient{}, true},
		{"VS Code 만", "200 120 24 /dev/pts/9\n", vsClient{120, 24, "/dev/pts/9"}, vsClient{}, true},
		{"클라이언트 없음", "", vsClient{}, vsClient{}, false},
		{"깨진 줄은 건너뛴다", "garbage\n200 120 24 /dev/pts/9\n", vsClient{120, 24, "/dev/pts/9"}, vsClient{}, true},
		{"크기가 0이면 무시", "200 0 24 /dev/pts/9\n", vsClient{}, vsClient{}, false},
		// 창은 사각형 하나라 축별로 가장 큰 값을 받아야 둘 다 들어간다.
		{"다른 클라이언트 둘은 축별 최대", "100 269 40 /dev/pts/6\n101 200 69 /dev/pts/7\n200 120 24 /dev/pts/9\n",
			vsClient{120, 24, "/dev/pts/9"}, vsClient{269, 69, ""}, true},
	}
	for _, c := range cases {
		vs, other, ok := parseClients(c.out, isVS)
		if ok != c.wantOk || vs != c.wantVS || other != c.wantOther {
			t.Errorf("%s: got vs=%+v other=%+v ok=%v, want vs=%+v other=%+v ok=%v",
				c.name, vs, other, ok, c.wantVS, c.wantOther, c.wantOk)
		}
	}
}

// 상태줄 줄 수만큼 창이 낮아야 한다. 읽지 못하면 1 — 너무 많이 잡는 쪽이 싸다.
func TestParseStatusLines(t *testing.T) {
	cases := map[string]int{"off": 0, "0": 0, "on": 1, "": 1, "2": 2, "5": 5, "9": 1, "huh": 1}
	for in, want := range cases {
		if got := parseStatusLines(in); got != want {
			t.Errorf("parseStatusLines(%q) = %d, want %d", in, got, want)
		}
	}
}

// 창 = VS Code 폭 + 구분선 + 패널 폭, 높이 = VS Code 높이 - 상태줄.
//
// 높이를 빼지 않으면 뷰포트가 세로로 밀려 프롬프트가 화면 밖으로 나간다 — 실측으로
// 확인한 실패 모드라 여기서 못 박는다.
func TestWindowFit(t *testing.T) {
	cases := []struct {
		name         string
		vs, other    vsClient
		panelWidth   int
		wantW, wantH int
	}{
		// WT 가 같이 붙어 있으면 창은 WT 것이다 — 그래야 WT 화면에 ··· 가 없다.
		// VS Code 는 커서를 따라가는 뷰포트로 보고, 작업 pane(269-1-60=208)이
		// 자기 폭(178)보다 넓으므로 패널은 화면 밖에 남는다.
		{"WT 가 더 크다", vsClient{width: 178, height: 49}, vsClient{width: 269, height: 68}, 60, 269, 68},
		// VS Code 만 있으면 딱 맞춰 준다 — 패닝조차 없다.
		{"VS Code 만", vsClient{width: 120, height: 23}, vsClient{}, 70, 191, 23},
		// WT 가 작아도 작업 pane 은 VS Code 폭 이상이어야 한다. 이게 깨지면
		// 뷰포트가 오른쪽 끝까지 밀려 패널이 보인다.
		{"WT 가 작으면 바닥이 이긴다", vsClient{width: 178, height: 49}, vsClient{width: 200, height: 30}, 60, 239, 49},
		{"축이 서로 엇갈릴 때", vsClient{width: 178, height: 60}, vsClient{width: 300, height: 40}, 60, 300, 60},
	}
	for _, c := range cases {
		w, h := windowFit(c.vs, c.other, c.panelWidth)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}

// 작업 pane 이 VS Code 클라이언트보다 좁아지면 패널이 뷰포트에 들어온다 — 어떤
// 조합에서도 그 일이 없어야 한다는 것을 성질로 못 박는다.
func TestWindowFitKeepsWorkPaneWideEnough(t *testing.T) {
	for _, vsW := range []int{60, 120, 178, 300} {
		for _, otherW := range []int{0, 80, 200, 269, 400} {
			for _, panel := range []int{30, 60, 100} {
				w, _ := windowFit(vsClient{width: vsW, height: 50}, vsClient{width: otherW, height: 50}, panel)
				if work := w - paneDivider - panel; work < vsW {
					t.Fatalf("vs=%d other=%d panel=%d: 작업 pane %d < VS Code %d",
						vsW, otherW, panel, work, vsW)
				}
			}
		}
	}
}

// 일괄 재계산은 mux 가 고정한 창만 건드린다 — 사용자가 직접 manual 로 둔 창까지
// 손대면 남의 설정을 되돌리는 셈이다.
func TestParseFittedWindows(t *testing.T) {
	out := "@1 work 1 269 68\n@2 other  200 50\n@3 third 1 190 24\ngarbage\n@4 bad 1 x y\n"
	got := parseFittedWindows(out)
	if len(got) != 2 {
		t.Fatalf("got %d windows (%+v), want 2", len(got), got)
	}
	if got[0] != (panelCtx{window: "@1", session: "work", width: 269, height: 68}) {
		t.Errorf("first = %+v", got[0])
	}
	if got[1] != (panelCtx{window: "@3", session: "third", width: 190, height: 24}) {
		t.Errorf("second = %+v", got[1])
	}
	if len(parseFittedWindows("")) != 0 {
		t.Error("빈 출력에서 창이 나왔다")
	}
}

// 커서가 숨겨진 동안만 바닥으로 — tmux 는 그때 뷰포트를 왼쪽 위로 보낸다.
// 커서가 보이면 tmux 추적으로 돌려줘야 셸의 clear 뒤 프롬프트를 놓치지 않는다.
func TestViewportAction(t *testing.T) {
	cases := map[string][2]bool{"0": {true, false}, "1": {false, true}, "": {false, false}, "x": {false, false}}
	for in, want := range cases {
		pin, track := viewportAction(in)
		if pin != want[0] || track != want[1] {
			t.Errorf("viewportAction(%q) = pin %v track %v, want pin %v track %v", in, pin, track, want[0], want[1])
		}
	}
}

// 창이 WT 보다 넓어지면 WT 도 뷰포트를 얻고, 커서가 숨겨지는 순간 입력창이 화면 밖으로
// 나간다(실측). 패널은 WT 안에 들어가는 만큼만, 그마저 안 되면 물러난다.
func TestFitPanelWidth(t *testing.T) {
	cases := []struct {
		name      string
		vs, other vsClient
		want      int
		got       int
		ok        bool
	}{
		{"넉넉하면 원하는 폭 그대로", vsClient{width: 174}, vsClient{width: 269}, 60, 60, true},
		{"모자라면 남는 만큼", vsClient{width: 174}, vsClient{width: 269}, 107, 94, true},
		{"바닥(MinPanelWidth)도 안 들어가면 물러난다", vsClient{width: 250}, vsClient{width: 269}, 60, 0, false},
		{"WT 가 없으면 캡 없음", vsClient{width: 174}, vsClient{}, 107, 107, true},
	}
	for _, c := range cases {
		got, ok := fitPanelWidth(c.vs, c.other, c.want)
		if got != c.got || ok != c.ok {
			t.Errorf("%s: got %d,%v want %d,%v", c.name, got, ok, c.got, c.ok)
		}
	}
}

func TestParseClientList(t *testing.T) {
	isVS := func(pid int) bool { return pid == 200 }
	got := parseClientList("100 269 69 /dev/pts/6\n200 174 50 /dev/pts/10\nbad\n", isVS)
	if len(got) != 2 || got[0].vscode || !got[1].vscode || got[1].tty != "/dev/pts/10" || got[0].width != 269 {
		t.Errorf("got %+v", got)
	}
}

// 창 크기가 이미 맞아도 패널 폭은 바로잡는다. 크기가 맞으면 아무것도 안 하던 가드 때문에
// 고정된 창 안에서 패널이 164칸으로 틀어진 채 머물렀다(2026-10-14 실측: 작업 74 / 패널 164).
// 패널 프로세스는 고정된 창의 폭을 fit 에 맡기므로, fit 이 안 고치면 아무도 안 고친다.
func TestPinPanelWidth(t *testing.T) {
	listArgs := []string{"list-panes", "-t", "@1", "-F", "#{pane_id} #{pane_width} #{pane_start_command}"}
	cases := []struct {
		name     string
		out      string
		wantRuns []string
	}{
		{"틀어졌으면 되돌린다", "%0 74 zsh\n%1 164 /bin/mux watch\n", []string{"tmux resize-pane -t %1 -x 45"}},
		{"맞으면 아무것도 안 한다", "%0 193 zsh\n%1 45 /bin/mux watch\n", nil},
		{"패널이 없으면 아무것도 안 한다", "%0 239 zsh\n", nil},
		{"폭을 못 읽으면 아무것도 안 한다", "%0 74 zsh\n%1 ?? /bin/mux watch\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte(c.out), nil, "tmux", listArgs...)
				pinPanelWidth("@1", 45)
				if len(m.runs) != len(c.wantRuns) || (len(c.wantRuns) > 0 && m.runs[0] != c.wantRuns[0]) {
					t.Errorf("runs = %v, want %v", m.runs, c.wantRuns)
				}
			})
		})
	}
}
