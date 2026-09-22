package tmux

import (
	"strconv"
	"strings"
)

// 창을 VS Code 클라이언트보다 넓게 고정해, 같은 세션을 보는 Windows Terminal 에만
// 패널이 보이게 하는 장치.
//
// tmux 에는 "이 클라이언트에게만 숨기기"가 없다 — pane 은 창 소유라 같은 창을 보는
// 클라이언트는 같은 것을 본다. 대신 실측(tmux 3.4)으로 확인한 성질이 하나 있다:
// 창보다 작은 클라이언트는 창을 잘라 보는 게 아니라 **활성 pane 을 따라가는 뷰포트**를
// 얻는다(상태줄에 [x,y] 오프셋이 뜬다). 그래서 창을
//
//	창 폭 = VS Code 폭 + 구분선 1 + 패널 폭
//
// 으로 고정하면, 작업 pane 이 활성인 한 오른쪽 끝의 패널은 VS Code 화면에 절대
// 들어오지 않는다. 창보다 큰 Windows Terminal 은 창 전체를 보므로 패널이 보인다.
//
// 높이도 반드시 맞춰야 한다. 창이 클라이언트가 그릴 수 있는 높이보다 높으면 뷰포트가
// 세로로도 움직여 프롬프트가 화면 밖으로 나간다 — 실측에서 재현됐다. 클라이언트의
// 높이에서 상태줄 줄 수를 뺀 것이 창에 허락된 높이다.
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

// sessionClients reports the single VS Code client watching session, and the
// largest of everything else attached to it.
//
// Exactly one VS Code client, or nothing: two VS Code windows on one session can
// disagree about size and there is only one window to fit, so no geometry
// satisfies both. Backing out there leaves the ordinary behaviour rather than
// picking a winner the other window would suffer for.
//
// The largest other client is what the window is really sized for — see
// windowFit. Both come from one list-clients because they are only ever wanted
// together, on a path tmux runs after every resize.
func sessionClients(session string) (vs, other vsClient, okVS bool) {
	if session == "" {
		return vsClient{}, vsClient{}, false
	}
	out, err := runner.Output("tmux", "list-clients", "-t", "="+session, "-F",
		"#{client_pid} #{client_width} #{client_height} #{client_tty}")
	if err != nil {
		return vsClient{}, vsClient{}, false
	}
	return parseClients(string(out), isVSCodeClient)
}

// parseClients splits list-clients output into the one VS Code client and the
// largest non-VS Code client. Pure for testing.
//
// "Largest" is per axis rather than per client: two terminals of different
// shapes both have to fit, and a window is one rectangle.
func parseClients(out string, isVS func(pid int) bool) (vs, other vsClient, okVS bool) {
	seen := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
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
		if isVS(pid) {
			seen++
			vs = vsClient{width: w, height: h, tty: f[3]}
			continue
		}
		other.width = maxInt(other.width, w)
		other.height = maxInt(other.height, h)
	}
	return vs, other, seen == 1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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

// windowFit is the window size that keeps the panel off a VS Code client's
// screen. Pure so the arithmetic is testable without a server.
//
//	폭  = max(VS Code 아닌 가장 큰 클라이언트, VS Code + 구분선 + 패널)
//	높이 = max(VS Code 아닌 가장 큰 클라이언트, VS Code)
//
// The first term is why a Windows Terminal on the same session still sees its
// whole screen: sizing the window to the editor instead left the rest of WT
// filled with tmux's dots. The second term is the floor that makes the panel
// invisible — it keeps the work pane at least as wide as the VS Code client, so
// that client's viewport never has to reach the window's right edge to keep its
// cursor in view. (A client smaller than the window does not get a cropped
// window but a viewport that follows the active pane's cursor; measured.)
//
// Heights arrive already net of the status line, so no axis needs the panel's
// width and the height is a plain max.
func windowFit(vs, other vsClient, panelWidth int) (width, height int) {
	width = maxInt(other.width, vs.width+paneDivider+panelWidth)
	height = maxInt(other.height, vs.height)
	return width, height
}

// syncVSCodeViewport fixes this window's size to hide the panel from the VS
// Code client watching it, or releases a fix mux made earlier. It reports
// whether the window is now fixed.
//
// Called from the panel's own hook path, so it runs on client-attached,
// client-resized and client-session-changed — every event that can change who
// is looking or how big they are.
func syncVSCodeViewport(ctx panelCtx, panelWidth int) bool {
	window, session := ctx.window, ctx.session
	c, other, ok := sessionClients(session)
	if !ok || panelWidth <= 0 {
		releaseWindowFit(window)
		return false
	}

	// A panel wider than the whole VS Code terminal would take more than half
	// the fixed window, which the panel itself refuses to stay at — it clamps to
	// half on its first frame and the work pane ends up wider than the client.
	// Back out instead of fighting it; that terminal is too narrow for this
	// arrangement.
	if panelWidth >= c.width {
		releaseWindowFit(window)
		return false
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
		return false
	}

	// Already the right size: say so and touch nothing. This is the guard that
	// keeps the hooks from eating themselves — resize-window fires
	// after-resize-window, whose run-shell is synchronous, which runs this same
	// command again. Without the comparison that is an unbounded chain: measured
	// as a `mux panel --auto` that never returned and a window left half
	// resized, with the panel squeezed to a single column.
	if ctx.width == width && ctx.height == height {
		return true
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
		return false
	}

	// Growing the window hands the new columns out proportionally, so the panel
	// has to be put back at its own width — otherwise the work pane ends up
	// wider than the VS Code client and long lines wrap off its screen.
	if pane, err := findPanelPane(window); err == nil && pane != "" {
		_ = ResizePaneWidth(pane, panelWidth)
	}
	return true
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
		fitted = append(fitted, panelCtx{window: f[0], session: f[1], width: w, height: h})
	}
	return fitted
}

// KeepBottomVisible keeps a VS Code client's viewport on the bottom of a window
// taller than it while the active pane's cursor is hidden.
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
	vs, _, ok := sessionClients(session)
	if !ok || vs.tty == "" {
		return
	}
	// Through the client, not the session: `-t =session` leaves pane formats
	// empty (measured — only session options resolve), and `-c` reads what
	// that client is looking at, which is the only view that matters here.
	out, err := runner.Output("tmux", "display-message", "-p", "-c", vs.tty, "#{cursor_flag}")
	if err != nil {
		return
	}
	pin, track := viewportAction(strings.TrimSpace(string(out)))
	switch {
	case pin:
		_ = runner.Run("tmux", "refresh-client", "-t", vs.tty, "-D", "99999")
	case track:
		_ = runner.Run("tmux", "refresh-client", "-t", vs.tty, "-c")
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
