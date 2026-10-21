package tmux

import (
	"strconv"
	"strings"
)

// VS Code 클라이언트가 붙은 창의 크기를 mux 가 직접 고정하는 장치. 목적은 둘이다:
// 지금 쓰고 있는 터미널은 창 전체를 보고, VS Code 화면에서는 패널이 빠진다.
//
// tmux 에는 "이 클라이언트에게만 숨기기"가 없다 — pane 은 창 소유라 같은 창을 보는
// 클라이언트는 같은 것을 본다. 대신 실측(tmux 3.4)으로 확인한 성질이 하나 있다:
// 창보다 작은 클라이언트는 창을 잘라 보는 게 아니라 **활성 pane 을 따라가는 뷰포트**를
// 얻는다(상태줄에 [x,y] 오프셋이 뜬다). 그래서 창을
//
//	터미널이 있으면    창 = 가장 최근에 쓴 터미널의 크기 그대로
//	VS Code 만 있으면  창 = (VS Code 폭 + 구분선 1 + 패널 폭) × VS Code 높이
//
// 로 고정한다. 터미널이 VS Code 폭 + 구분선 + 패널 폭 이상이면 작업 pane 이 VS Code
// 보다 넓으므로, 작업 pane 이 활성인 한 오른쪽 끝의 패널은 VS Code 화면에 들어오지 않는다.
//
// 터미널이 그보다 좁으면 VS Code 화면 오른쪽 끝에 패널 일부가 보인다. 이것은 고른 대가다.
// 예전에는 패널을 줄여 둘 다 맞췄는데, 그러면 «패널은 어디서나 마지막으로 드래그한 폭
// 하나»(docs/design/panel-width.md)가 깨졌다: 밖에서 ssh(236칸)로 볼 때 집의 VS Code
// (195칸)가 붙은 세션만 패널이 40칸이었다. 2026-10-19 사용자 결정으로 패널 폭이 이긴다.
//
// «가장 최근에 쓴» 터미널이다, «가장 큰» 이 아니다. 축마다 가장 큰 값을 쓰던 때는 집의
// kitty(273x64)가 붙은 세션에서 밖의 ssh(236x51) 화면이 위 13줄과 오른쪽이 잘렸다
// (2026-10-19). 쓰지 않는 터미널은 남는 칸이 점으로 차거나 뷰포트로 본다. 쓰는 터미널이
// 바뀌면 client-active 훅이, 터미널이 떠나면 client-detached 훅이 다시 맞춘다.
//
// 높이도 반드시 맞춰야 한다. 창이 클라이언트가 그릴 수 있는 높이보다 높으면 뷰포트가
// 세로로도 움직여 프롬프트가 화면 밖으로 나간다 — 실측에서 재현됐다. 클라이언트의
// 높이에서 상태줄 줄 수를 뺀 것이 창에 허락된 높이다. 높이는 패널을 숨기는 데 아무 역할이
// 없으므로 터미널이 있으면 터미널의 것이다. VS Code 를 바닥으로 두었더니, 밖의 ssh(236x51)
// 에 집의 VS Code(195x55)가 겹친 세션에서 창이 54줄이 되어 ssh 화면의 위 네 줄이 잘렸다
// (2026-10-15). VS Code 가 더 크면 아래에 ··· 줄이 남을 뿐이고, 더 작으면 KeepBottomVisible 이
// 바닥을 붙잡는 뷰포트로 본다.
//
// 이 파일의 «VS Code» 는 편집기의 통합 터미널이다 — 2026-10-21 부터 JetBrains IDE(IntelliJ)의
// 터미널도 든다(isEditorClient, 표식은 jetbrainsEnvMarker). 패널의 오른쪽 클릭이 IntelliJ 로
// 열게 되면서 그 터미널도 한 프로젝트를 보는 편집기 창이 됐고, 패널을 그 화면에서 빼고 창을
// 쓰는 터미널에 맞춰야 하는 이유가 VS Code 와 같다.
const (
	// vsFitOption marks a window whose size mux fixed, so the release path can
	// tell "we set this" from "the user set window-size themselves".
	vsFitOption = "@mux_vsfit"

	// paneDivider is the column tmux spends on the border between two panes in
	// a horizontal split. Without it the panel would be one cell narrower than
	// asked and the work pane one cell wider than the VS Code client.
	paneDivider = 1
)

// vsClient is the VS Code terminal a session is being watched from: its size,
// and its tty, which is how refresh-client addresses it.
type vsClient struct {
	width, height int
	tty           string
}

// sessionClients reports the VS Code client the window is fitted to, and the
// terminal in use among everything else attached to it (see parseClients).
//
// With several VS Code clients the most recently active one wins. Two of them
// can disagree about size and there is only one window, so one has to lose —
// and backing out instead, which this used to do, made the loser everyone: a
// hidden terminal tab (80x30, idle for hours) was enough to release the fit,
// so the moment the big terminal switched to another session the window shrank
// to the editor's width and the panel walked into the editor's view (measured
// 2026-10-14). The one that loses now is a terminal nobody is looking at; a
// smaller one gets a viewport that follows the cursor, a larger one sees the
// panel.
//
// The terminal is what the window is really sized for — see windowFit. Both
// come from one list-clients because they are only ever wanted together, on a
// path tmux runs after every resize.
func sessionClients(session string) (vs, other vsClient, okVS bool) {
	if session == "" {
		return vsClient{}, vsClient{}, false
	}
	out, err := runner.Output("tmux", "list-clients", "-t", "="+session, "-F",
		"#{client_pid} #{client_width} #{client_height} #{client_tty} #{client_activity}")
	if err != nil {
		return vsClient{}, vsClient{}, false
	}
	return parseClients(string(out), isEditorClient)
}

// clientInfo is one attached client as list-clients reports it.
type clientInfo struct {
	tty           string
	width, height int
	vscode        bool
	// activity is client_activity in unix seconds, or 0 when the format did
	// not ask for it — KeepBottomVisible has no use for it.
	activity int64
}

// parseClientList reads list-clients output: pid, width, height, tty and an
// optional activity. Pure for testing.
func parseClientList(out string, isVS func(pid int) bool) []clientInfo {
	var list []clientInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 && len(f) != 5 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		w, errW := strconv.Atoi(f[1])
		h, errH := strconv.Atoi(f[2])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue
		}
		c := clientInfo{tty: f[3], width: w, height: h, vscode: isVS(pid)}
		if len(f) == 5 {
			// An unreadable activity reads as 0, the oldest: it can lose the
			// pick but never drops a client from the list.
			c.activity, _ = strconv.ParseInt(f[4], 10, 64)
		}
		list = append(list, c)
	}
	return list
}

// parseClients splits list-clients output into the VS Code client to fit and
// the terminal the window is for — each the most recently active of its kind.
// Pure for testing.
//
// The terminal is the one in use, not the largest. Taking the largest per axis
// sized the window to a kitty left attached at home (273x64) while the user
// typed into a 236x51 ssh from outside, which lost the ssh its top thirteen
// rows and its right edge (2026-10-19). One terminal has to lose when two
// differ, and the one nobody is typing into is the cheap loser.
func parseClients(out string, isVS func(pid int) bool) (vs, other vsClient, okVS bool) {
	var bestVS, bestTerm *clientInfo
	list := parseClientList(out, isVS)
	for i := range list {
		c := &list[i]
		if c.vscode {
			if bestVS == nil || preferClient(*c, *bestVS) {
				bestVS = c
			}
			continue
		}
		if bestTerm == nil || preferClient(*c, *bestTerm) {
			bestTerm = c
		}
	}
	if bestTerm != nil {
		other = vsClient{width: bestTerm.width, height: bestTerm.height}
	}
	if bestVS == nil {
		return vsClient{}, other, false
	}
	return vsClient{width: bestVS.width, height: bestVS.height, tty: bestVS.tty}, other, true
}

// preferClient reports whether a is the client to go by rather than b: the
// more recently active, and on a tie the larger. Activity is in whole seconds,
// so two terminals attached together tie, and a hidden terminal tab sits at a
// small default size while the one in view fills the editor.
func preferClient(a, b clientInfo) bool {
	if a.activity != b.activity {
		return a.activity > b.activity
	}
	return a.width*a.height > b.width*b.height
}

// panelWidthFor is the width the panel is held at in a fitted window of the
// given width: the saved one, capped at half the window like everywhere else
// (MaxPanelWidth), and nothing when even that half is below MinPanelWidth.
// Pure for testing.
//
// The fit no longer narrows the panel to keep it off a VS Code screen. It used
// to, and that made the panel the one thing that is supposed to be one width
// everywhere a different width per session: from a 236-column ssh, the session
// a 195-column VS Code client was on had a 40-column panel and every other one
// had 57 (2026-10-19). The user chose the width.
//
// The cap is applied here because the panel process leaves a fitted window's
// widths to this file — pinPanelWidth is what holds it.
func panelWidthFor(windowWidth, want int) (int, bool) {
	w := want
	if limit := MaxPanelWidth(windowWidth); w > limit {
		w = limit
	}
	if w < MinPanelWidth {
		return 0, false
	}
	return w, true
}

// statusLines is how many rows the status line takes from a client, read from
// the session's own `status` option.
//
// Unreadable answers 1, the default: guessing too many shrinks the window by a
// row the client could have used, which is the cheaper mistake — guessing too
// few pushes the window past what the client can draw and the viewport starts
// scrolling, which is the failure this whole file exists to avoid.
func statusLines(session string) int {
	// Read through a format, not show-options: `status` is a session option that
	// is almost always set globally, and a session-scoped read of one nobody set
	// on that session comes back empty. Measured on a live server — both
	// `-t name` and `-t =name` printed nothing while the status line was on.
	// The format resolves the effective value ("on", or "2".."5").
	out, err := runner.Output("tmux", "display-message", "-p", "-t", "="+session, "#{status}")
	if err != nil {
		return 1
	}
	return parseStatusLines(strings.TrimSpace(string(out)))
}

// parseStatusLines maps the `status` option's value onto a row count.
func parseStatusLines(v string) int {
	switch v {
	case "off", "0":
		return 0
	case "", "on":
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 5 {
		return 1
	}
	return n
}

// windowFit is the window size mux fixes: the terminal in use exactly, or —
// with only VS Code attached — that client plus the panel. Pure so the
// arithmetic is testable without a server.
//
//	터미널이 있으면    폭 × 높이 = 터미널
//	VS Code 만 있으면  폭 = VS Code + 구분선 + 패널, 높이 = VS Code
//
// The terminal gets its own size on both axes because a window larger than a
// client is not cropped for it but panned: the client gets a viewport that
// follows the active pane's cursor (measured), and with Claude's cursor at the
// bottom — tty.c, tty_window_offset1 — that costs the top rows, while a window
// too wide pans the panel off the right edge. A larger terminal used to win
// the width (WT beside VS Code): it still does, since the one in use is the
// one sized for.
//
// The VS Code client is the one that adapts. When the terminal is at least VS
// Code + divider + panel wide, the work pane is wider than the editor's view
// and its viewport never has to reach the panel to follow the cursor. When the
// terminal is narrower, part of the panel shows on the editor's right edge —
// the price of keeping the panel at its one saved width (see panelWidthFor).
//
// "No terminal" is other.width == 0. Heights arrive already net of the status
// line.
func windowFit(vs, other vsClient, panelWidth int) (width, height int) {
	if other.width > 0 {
		return other.width, other.height
	}
	return vs.width + paneDivider + panelWidth, vs.height
}

// syncVSCodeViewport fixes this window's size to hide the panel from the VS
// Code client watching it, or releases a fix mux made earlier. It reports
// whether the window is now fixed.
//
// Called from the panel's own hook path, so it runs on client-attached,
// client-detached, client-active, client-resized and client-session-changed —
// every event that can change who is looking or how big they are.
//
// The width returned is the one the panel must have in this window — the
// saved width, or half the window when that is less (panelWidthFor). The
// caller opens the panel at exactly that, or the fit and the freshly opened
// panel disagree on the first frame.
func syncVSCodeViewport(ctx panelCtx, panelWidth int) (fitted bool, width int) {
	window, session := ctx.window, ctx.session
	c, other, ok := sessionClients(session)
	if !ok || panelWidth <= 0 {
		releaseWindowFit(window)
		return false, panelWidth
	}

	// With only VS Code attached, a panel wider than the whole VS Code terminal
	// would take more than half the fixed window, which the panel itself
	// refuses to stay at — it clamps to half on its first frame and the work
	// pane ends up wider than the client. Back out instead of fighting it; that
	// terminal is too narrow for this arrangement. With a terminal the window is
	// the terminal's and panelWidthFor caps the panel at half of it.
	if other.width == 0 && panelWidth >= c.width {
		releaseWindowFit(window)
		return false, panelWidth
	}

	// The status line is the session's, so it costs every client the same row.
	status := statusLines(session)
	c.height -= status
	if other.height > 0 {
		other.height -= status
	}

	width, height := windowFit(c, other, panelWidth)
	if height < 1 {
		releaseWindowFit(window)
		return false, panelWidth
	}
	if panelWidth, ok = panelWidthFor(width, panelWidth); !ok {
		releaseWindowFit(window)
		return false, panelWidth
	}

	// Already the right size: say so and touch nothing. This is the guard that
	// keeps the hooks from eating themselves — resize-window fires
	// after-resize-window, whose run-shell is synchronous, which runs this same
	// command again. Without the comparison that is an unbounded chain: measured
	// as a `mux panel --auto` that never returned and a window left half
	// resized, with the panel squeezed to a single column.
	//
	// The panel inside is checked even so. The panel process leaves a fitted
	// window's widths to this function, so returning here without looking
	// meant a panel knocked off its width stayed off it: measured 2026-10-14,
	// a 239-column window split 74 / 164 and held there with every hook
	// passing through this branch. Resizing a pane fires no window hook, so
	// the guard above still holds.
	//
	// Right size is not the same as fixed, though. A window tmux sized itself —
	// window-size largest, a big terminal just switched in — already measures
	// the target, and returning here left it without the tag and without a
	// manual size. When that terminal left, tmux shrank the window to the
	// editor's width and the sweep skipped it for want of the tag, so the
	// panel walked into the editor's view (measured 2026-10-14). A resize to
	// the size it already has is what turns window-size to manual; the run it
	// triggers finds the tag and stops.
	if ctx.width == width && ctx.height == height {
		if !ctx.fitted && !WindowFitted(window) {
			_ = runner.Run("tmux", "set-option", "-w", "-t", window, vsFitOption, "1")
			_ = runner.Run("tmux", "resize-window", "-t", window,
				"-x", strconv.Itoa(width), "-y", strconv.Itoa(height))
		}
		pinPanelWidth(window, panelWidth)
		return true, panelWidth
	}

	// The tag goes on first so the nested run this resize triggers already sees
	// it, and so a failure half way through leaves something the release path
	// can clean up.
	_ = runner.Run("tmux", "set-option", "-w", "-t", window, vsFitOption, "1")

	// resize-window sets this window's window-size option to manual, which is
	// the point: tmux must stop resizing the window to whichever client was
	// last active, or the next keystroke in VS Code undoes this.
	if err := runner.Run("tmux", "resize-window", "-t", window,
		"-x", strconv.Itoa(width), "-y", strconv.Itoa(height)); err != nil {
		return false, panelWidth
	}

	// Growing the window hands the new columns out proportionally, so the panel
	// has to be put back at its own width — otherwise the work pane ends up
	// wider than the VS Code client and long lines wrap off its screen.
	if pane, err := findPanelPane(window); err == nil && pane != "" {
		_ = ResizePaneWidth(pane, panelWidth)
	}
	return true, panelWidth
}

// pinPanelWidth puts a fitted window's panel back at width when it is not
// there. One list-panes, and a resize only when it is off.
//
// Its own list-panes rather than findPanelPane: that one's format is shared by
// every mock key that finds a panel, and this needs the width beside the id.
func pinPanelWidth(window string, width int) {
	out, err := runner.Output("tmux", "list-panes", "-t", window,
		"-F", "#{pane_id} #{pane_width} #{pane_start_command}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(f) != 3 || !strings.Contains(f[2], panelCommand) {
			continue
		}
		cur, err := strconv.Atoi(f[1])
		if err != nil || cur == width {
			return
		}
		_ = ResizePaneWidth(f[0], width)
		return
	}
}

// releaseWindowFit undoes a fix mux made, and does nothing otherwise — a user
// who set window-size manually on their own window keeps it.
func releaseWindowFit(window string) {
	out, err := runner.Output("tmux", "show-options", "-wqv", "-t", window, vsFitOption)
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		return
	}
	_ = runner.Run("tmux", "set-option", "-wu", "-t", window, "window-size")
	_ = runner.Run("tmux", "set-option", "-wu", "-t", window, vsFitOption)
}

// SyncFittedWindows re-runs the fit on every window mux has fixed, not just the
// one the hook fired for.
//
// A window is fixed to the clients watching it, and the event that invalidates
// it often happens *elsewhere*: Windows Terminal switching to another session
// leaves the one it left still sized for a client that is gone, and no hook
// fires for the session nobody is looking at any more. Measured — a session WT
// had left kept a 269-column window while its VS Code terminal was 165 wide.
//
// One list-windows for the sweep. Windows tmux never fixed are skipped without
// a second call, so the usual case costs exactly that one.
func SyncFittedWindows(panelWidth int) {
	out, err := runner.Output("tmux", "list-windows", "-a", "-F",
		"#{window_id} #{session_name} #{"+vsFitOption+"} #{window_width} #{window_height}")
	if err != nil {
		return
	}
	for _, ctx := range parseFittedWindows(string(out)) {
		syncVSCodeViewport(ctx, panelWidth)
	}
}

// parseFittedWindows keeps the windows mux fixed. Pure for testing.
func parseFittedWindows(out string) []panelCtx {
	var fitted []panelCtx
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 || f[2] != "1" {
			continue
		}
		w, errW := strconv.Atoi(f[3])
		h, errH := strconv.Atoi(f[4])
		if errW != nil || errH != nil {
			continue
		}
		fitted = append(fitted, panelCtx{window: f[0], session: f[1], width: w, height: h, fitted: true})
	}
	return fitted
}

// KeepBottomVisible keeps every viewport on a window — any client the window
// is larger than — on the bottom while the active pane's cursor is hidden.
//
// tmux pans a small client's viewport to follow the cursor, which for Claude
// Code sits at the bottom — but only while the cursor is *shown*. Hidden, tmux
// falls back to the top-left corner (tty.c, tty_window_offset1: `if
// (~wp->screen->mode & MODE_CURSOR) { *ox = 0; *oy = 0; }`), and Claude hides
// its cursor while it streams. Measured: cursor hidden → offset [0,0], the
// newest rows gone; `refresh-client -D` past the end → bottom again.
//
// So: cursor hidden → pin the bottom; cursor shown → hand the viewport back to
// tmux's own tracking. The second half is what keeps this safe for a shell —
// a pinned viewport stops following the cursor, and `clear` puts the prompt on
// row 1. Both commands are idempotent (measured), so this is stateless and runs
// every tick with nothing to remember.
//
// ponytail: list-clients + display-message + refresh-client, 2초마다, VS Code
// 클라이언트가 붙은 세션의 패널에서만. 눈에 띄면 cursor_flag 가 바뀔 때만
// refresh-client 를 보내도록 직전 값을 들면 된다.
func KeepBottomVisible(session string) {
	if session == "" {
		return
	}
	out, err := runner.Output("tmux", "list-clients", "-t", "="+session, "-F",
		"#{client_pid} #{client_width} #{client_height} #{client_tty}")
	if err != nil {
		return
	}
	clients := parseClientList(string(out), isEditorClient)
	if len(clients) == 0 {
		return
	}
	// Through a client, not the session: `-t =session` leaves pane formats
	// empty (measured — only session options resolve), and `-c` reads what
	// that client is looking at. Every client of the session shares the
	// window, so any one of them answers for all.
	out, err = runner.Output("tmux", "display-message", "-p", "-c", clients[0].tty,
		"#{window_width} #{window_height} #{cursor_flag} #{status}")
	if err != nil {
		return
	}
	f := strings.Fields(string(out))
	if len(f) != 4 {
		return
	}
	winW, errW := strconv.Atoi(f[0])
	winH, errH := strconv.Atoi(f[1])
	if errW != nil || errH != nil {
		return
	}
	status := parseStatusLines(f[3])
	pin, track := viewportAction(f[2])
	for _, c := range clients {
		// Only a client the window is larger than has a viewport to keep.
		// Not just VS Code: a phone, or a Windows Terminal shorter than the
		// editor's terminal, pans the same way.
		if c.width >= winW && c.height-status >= winH {
			continue
		}
		switch {
		case pin:
			_ = runner.Run("tmux", "refresh-client", "-t", c.tty, "-D", "99999")
		case track:
			_ = runner.Run("tmux", "refresh-client", "-t", c.tty, "-c")
		}
	}
}

// viewportAction maps the active pane's cursor flag onto what to do with the
// viewport: hidden pins the bottom, shown returns to tracking, anything else
// (unreadable) does nothing. Pure for testing.
func viewportAction(cursorFlag string) (pin, track bool) {
	switch cursorFlag {
	case "0":
		return true, false
	case "1":
		return false, true
	}
	return false, false
}

// WindowFitted reports whether mux fixed this window's size (see
// syncVSCodeViewport). The panel asks before treating a width change as the
// user dragging its border: growing a fitted window hands the new columns out
// proportionally, and that widening looked exactly like a drag — measured as
// the saved panel width creeping from 60 to 107 in one afternoon, which then
// pushed every fitted window past Windows Terminal's width.
func WindowFitted(target string) bool {
	args := []string{"show-options", "-wqv"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, vsFitOption)
	out, err := runner.Output("tmux", args...)
	return err == nil && strings.TrimSpace(string(out)) == "1"
}
