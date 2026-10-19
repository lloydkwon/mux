package tmux

import (
	"strings"
	"testing"
)

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
		// 터미널이 둘이면 지금 쓰는 쪽(가장 최근)이다. 축별 최대를 쓰던 때는 집의
		// kitty(273x64)가 붙은 세션에서 밖의 ssh(236x51) 화면이 위 13줄과 오른쪽이
		// 잘렸다(2026-10-19).
		{"터미널 둘이면 최근 것", "100 273 64 /dev/pts/0 1000\n101 236 51 /dev/pts/23 3000\n200 155 55 /dev/pts/27 2000\n",
			vsClient{155, 55, "/dev/pts/27"}, vsClient{236, 51, ""}, true},
		{"터미널 둘, 같은 초면 큰 것", "100 269 40 /dev/pts/6 1000\n101 200 69 /dev/pts/7 1000\n200 120 24 /dev/pts/9\n",
			vsClient{120, 24, "/dev/pts/9"}, vsClient{200, 69, ""}, true},
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

// 터미널이 있으면 창 = 그 터미널 크기 그대로, VS Code 만 있으면 VS Code 폭 + 구분선 +
// 패널 폭 × VS Code 높이(높이는 상태줄을 뺀 값).
//
// 지금 쓰는 터미널은 창 전체를 봐야 한다. 창이 터미널보다 높으면 위가 잘리고(뷰포트가
// 바닥의 커서를 따라 내려간다), 넓으면 오른쪽 패널이 잘린다.
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
		// 터미널이 VS Code + 패널보다 좁아도 창은 터미널을 넘지 않는다. VS Code 화면
		// 오른쪽 끝에 패널 일부가 보이는 것이 대가다(2026-10-19 사용자 결정 — 패널
		// 폭을 줄여 맞추던 것을 그만뒀다).
		{"터미널이 좁아도 창은 터미널 것", vsClient{width: 178, height: 49}, vsClient{width: 200, height: 30}, 60, 200, 30},
		{"축이 서로 엇갈릴 때", vsClient{width: 178, height: 60}, vsClient{width: 300, height: 40}, 60, 300, 40},
		// 2026-10-15 의 사례 그대로: 밖에서 ssh(236x51)로 붙은 세션에 집의 VS Code
		// 터미널(195x55)이 남아 있었다. 높이를 VS Code 에 맞춘 창(54줄)은 ssh 가 그릴 수
		// 있는 50줄보다 4줄 높아서 패널의 위 네 줄이 잘렸다.
		{"ssh 가 VS Code 보다 낮고 좁다", vsClient{width: 195, height: 54}, vsClient{width: 236, height: 50}, 57, 236, 50},
	}
	for _, c := range cases {
		w, h := windowFit(c.vs, c.other, c.panelWidth)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}

// 두 성질을 모든 조합에서 못 박는다.
//   - 터미널이 있으면 창은 그 터미널과 같다. 넘으면 터미널이 뷰포트를 얻어 잘린다.
//   - 패널이 VS Code 화면 밖에 남는 것(작업 pane ≥ VS Code 폭)은 터미널이 없거나,
//     VS Code + 구분선 + 패널이 들어갈 만큼 넓을 때다. 그보다 좁은 터미널에서는 VS Code 에
//     패널이 보인다 — 패널 폭을 지키려고 고른 대가다.
func TestWindowFitKeepsWorkPaneWideEnough(t *testing.T) {
	for _, vsW := range []int{60, 120, 178, 300} {
		for _, otherW := range []int{0, 80, 200, 269, 400} {
			for _, panel := range []int{30, 60, 100} {
				w, _ := windowFit(vsClient{width: vsW, height: 50}, vsClient{width: otherW, height: 40}, panel)
				if otherW > 0 && w != otherW {
					t.Fatalf("vs=%d other=%d panel=%d: 창 %d ≠ 터미널 %d", vsW, otherW, panel, w, otherW)
				}
				if otherW > 0 && otherW < vsW+paneDivider+panel {
					continue
				}
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
	if got[0] != (panelCtx{window: "@1", session: "work", width: 269, height: 68, fitted: true}) {
		t.Errorf("first = %+v", got[0])
	}
	if got[1] != (panelCtx{window: "@3", session: "third", width: 190, height: 24, fitted: true}) {
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

// 맞춘 창의 패널은 저장된 폭이다 — VS Code 화면에서 숨기려고 줄이지 않는다. 줄이던
// 때는 밖에서 ssh(236칸)로 볼 때 집의 VS Code(195칸)가 붙은 세션만 패널이 40칸이었다
// (2026-10-19). 상한은 패널이 스스로 지키는 창의 반(MaxPanelWidth) 하나뿐이고, 그
// 반마저 바닥(MinPanelWidth)에 못 미치면 물러난다.
func TestPanelWidthFor(t *testing.T) {
	cases := []struct {
		name         string
		window, want int
		got          int
		ok           bool
	}{
		{"넉넉하면 저장된 폭 그대로", 236, 57, 57, true},
		{"VS Code 가 넓어도 줄이지 않는다", 200, 60, 60, true},
		{"창의 반을 넘으면 반", 100, 57, 50, true},
		{"반이 바닥에 못 미치면 물러난다", 40, 57, 0, false},
	}
	for _, c := range cases {
		got, ok := panelWidthFor(c.window, c.want)
		if got != c.got || ok != c.ok {
			t.Errorf("%s: got %d,%v want %d,%v", c.name, got, ok, c.got, c.ok)
		}
	}
}

// 2026-10-19 의 사례를 끝까지: my-computer 에는 집의 kitty(273x64, 어제 이후 입력 없음)와
// VS Code 터미널(155x55)이 붙어 있고, 밖의 ssh(236x51)가 방금 들어왔다. 창은 ssh 크기,
// 패널은 저장된 폭 57 이어야 한다.
func TestSyncFitsTheTerminalInUse(t *testing.T) {
	isVS := isVSCodeClient
	t.Cleanup(func() { isVSCodeClient = isVS })
	isVSCodeClient = func(pid int) bool { return pid == 20 }

	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("10 273 64 /dev/pts/0 1000\n20 155 55 /dev/pts/27 2000\n30 236 51 /dev/pts/23 3000\n"), nil,
			"tmux", "list-clients", "-t", "=my-computer", "-F",
			"#{client_pid} #{client_width} #{client_height} #{client_tty} #{client_activity}")
		m.OnOutput([]byte("on\n"), nil, "tmux", "display-message", "-p", "-t", "=my-computer", "#{status}")
		m.OnOutput([]byte("%13 215 zsh\n%14 57 /bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@8", "-F", "#{pane_id} #{pane_start_command}")

		ctx := panelCtx{window: "@8", session: "my-computer", width: 273, height: 63, fitted: true}
		fitted, width := syncVSCodeViewport(ctx, 57)
		want := []string{
			"tmux set-option -w -t @8 " + vsFitOption + " 1",
			"tmux resize-window -t @8 -x 236 -y 50",
			"tmux resize-pane -t %14 -x 57",
		}
		if !fitted || width != 57 || strings.Join(m.runs, "\n") != strings.Join(want, "\n") {
			t.Errorf("fitted=%v width=%d runs=%v, want 57 and %v", fitted, width, m.runs, want)
		}
	})
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

// 크기가 이미 맞아도 고정 표시가 없는 창은 고정 상태로 만든다. 큰 터미널이 들어오면
// tmux 가 훅보다 먼저 창을 그 크기로 키우므로, 목표 크기와 현재 크기가 같아 아무것도 안
// 하고 돌아갔다 — 표시도 수동 크기도 없이. 그 터미널이 떠나면 tmux 가 창을 VS Code 폭으로
// 줄이고, 표시가 없어 재정렬 대상에서도 빠져 패널이 편집기 화면에 들어왔다(2026-10-14).
func TestSyncMarksARightSizedUnfittedWindow(t *testing.T) {
	setup := func(m *mockRunner, tag string) {
		m.OnOutput([]byte("10 188 55 /dev/pts/15 2000\n11 80 30 /dev/pts/17 1000\n20 239 57 /dev/pts/19 3000\n"), nil,
			"tmux", "list-clients", "-t", "=water", "-F",
			"#{client_pid} #{client_width} #{client_height} #{client_tty} #{client_activity}")
		m.OnOutput([]byte("on\n"), nil, "tmux", "display-message", "-p", "-t", "=water", "#{status}")
		m.OnOutput([]byte(tag+"\n"), nil, "tmux", "show-options", "-wqv", "-t", "@2", vsFitOption)
		m.OnOutput([]byte("%5 193 zsh\n%6 45 /bin/mux watch\n"), nil,
			"tmux", "list-panes", "-t", "@2", "-F", "#{pane_id} #{pane_width} #{pane_start_command}")
	}
	origVS := isVSCodeClient
	t.Cleanup(func() { isVSCodeClient = origVS })
	isVSCodeClient = func(pid int) bool { return pid == 10 || pid == 11 }

	// tmux 가 이미 239x56 으로 키워 둔 창 — 목표와 같다.
	ctx := panelCtx{window: "@2", session: "water", width: 239, height: 56}

	t.Run("표시가 없으면 고정 상태로 만든다", func(t *testing.T) {
		withMock(t, func(m *mockRunner) {
			setup(m, "")
			fitted, _ := syncVSCodeViewport(ctx, 45)
			want := []string{
				"tmux set-option -w -t @2 " + vsFitOption + " 1",
				"tmux resize-window -t @2 -x 239 -y 56",
			}
			if !fitted || len(m.runs) != 2 || m.runs[0] != want[0] || m.runs[1] != want[1] {
				t.Errorf("fitted=%v runs=%v, want %v", fitted, m.runs, want)
			}
		})
	})
	t.Run("이미 표시돼 있으면 아무것도 안 한다", func(t *testing.T) {
		withMock(t, func(m *mockRunner) {
			setup(m, "1")
			if fitted, _ := syncVSCodeViewport(ctx, 45); !fitted || len(m.runs) != 0 {
				t.Errorf("fitted=%v runs=%v, want 아무 변경 없음", fitted, m.runs)
			}
		})
	})
	t.Run("정렬 경로는 표시를 다시 묻지 않는다", func(t *testing.T) {
		withMock(t, func(m *mockRunner) {
			setup(m, "")
			c := ctx
			c.fitted = true
			if fitted, _ := syncVSCodeViewport(c, 45); !fitted || len(m.runs) != 0 {
				t.Errorf("fitted=%v runs=%v, want 아무 변경 없음", fitted, m.runs)
			}
			for _, g := range m.gets {
				if g == "tmux show-options -wqv -t @2 "+vsFitOption {
					t.Errorf("이미 아는 표시를 다시 물었다")
				}
			}
		})
	})
}
