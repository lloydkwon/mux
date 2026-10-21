package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// 오른쪽 클릭이 띄울 편집기는 둘 중 하나이고, tmux 전역 옵션 @mux_editor 가 고른다
// (tmux.EditorChoice). 없으면 VS Code 다. 목록이 아니라 둘인 이유는 띄우는 방식이
// 편집기마다 다르기 때문이다 — 이름 하나 바꾸는 설정이었다면 IntelliJ 는 이렇게 됐다:
//
//   - VS Code 의 `code` 는 런처다. 떠 있는 인스턴스에 폴더를 넘기고 1초 안에 돌아온다.
//     그래서 startEditor 는 끝날 때까지 기다려 실패를 보고한다.
//   - IntelliJ 의 `idea` 는 돌아오지 않는다. 떠 있는 인스턴스에 넘기면 프로젝트 열기가
//     끝날 때까지 기다리고, 처음 여는 폴더는 «Trust and Open Project?» 대화상자가 현재
//     창 뒤에 떠서 그것을 닫을 때까지 131초가 걸렸다(2026-10-21 실측). 꺼져 있으면 그
//     프로세스가 곧 IDE 본체다(JVM 이 런처 안에서 돈다). startEditor 의 10초 타임아웃은
//     거기서 IDE 를 죽인다. 그래서 IntelliJ 는 startDetached 로 띄운다.
const (
	choiceVSCode   = "code"
	choiceIntelliJ = "idea"
)

// editorCommand 와 intellijCommand 는 PATH 에서 찾는 이름이다.
const (
	editorCommand   = "code"
	intellijCommand = "idea"
)

// editorTimeout bounds one launch.
//
// `code` is a launcher: it hands the folder to a running instance or starts one
// and returns, so the normal path finishes in well under a second. This is the
// ceiling on a shim that hangs, not part of how it works — without it a wedged
// launch would hold a tea.Cmd's goroutine for the life of the panel, and the
// panel lives for days.
const editorTimeout = 10 * time.Second

// openEditor is the seam. This package has no mock runner the way tmux does, so
// the launcher is a package-level var the tests replace — the same shape as
// tmux's clientEnvHas and panelStateFile. Without it `go test ./ui` would open
// windows on the developer's desktop.
var openEditor = startEditor

// findEditor resolves the editor binary, or "" when there is none to run.
//
// PATH first, because that is where an install that wants to be found puts
// itself — including WSL, where the Windows install arrives over interop
// (measured on this machine: the shim sits under
// "/mnt/c/.../Microsoft VS Code/bin" and is on the tmux server's PATH, which is
// what the panel inherits).
//
// The candidates below exist because PATH is not enough. A snap, a flatpak, a
// tarball unpacked into /opt and a macOS app bundle can all be installed without
// anything named `code` being on PATH, and mux would report "not installed" to
// someone looking at the running application.
func findEditor() string { return findBinary(editorCommand, editorCandidates) }

// findIntelliJ 는 IntelliJ IDEA 의 런처를 찾는다. 없으면 "". 규칙은 findEditor 와 같다.
func findIntelliJ() string { return findBinary(intellijCommand, intellijCandidates) }

// findBinary 는 PATH 에서 name 을, 없으면 candidates 를 순서대로 본다.
func findBinary(name string, candidates func() []string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, c := range candidates() {
		if isExecutableFile(c) {
			return c
		}
	}
	return ""
}

// intellijCandidates 는 editorCandidates 와 같은 이유로 var 다.
var intellijCandidates = systemIntelliJCandidates

// systemIntelliJCandidates 는 IntelliJ 가 PATH 밖에 설치되는 곳이다.
//
// Toolbox 는 기본으로 PATH 에 아무것도 올리지 않는다(이 기계 실측). 앱 경로가 먼저인
// 것은 Toolbox 의 scripts/idea 가 `exec` 없이 그 런처를 부르는 bash 래퍼라서다 — 쓸 수는
// 있지만 프로세스가 하나 더 낀다. snap 두 줄은 문서의 경로이고 여기서 재 보지 못했다.
func systemIntelliJCandidates() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		toolbox := filepath.Join(home, ".local/share/JetBrains/Toolbox")
		paths = append(paths,
			filepath.Join(toolbox, "apps/intellij-idea/bin/idea"), // Toolbox 3.x
			filepath.Join(toolbox, "scripts/idea"),                // Toolbox 가 만든 셸 스크립트
		)
	}
	return append(paths,
		"/snap/bin/intellij-idea-ultimate",
		"/snap/bin/intellij-idea-community",
	)
}

// editorCandidates lists the standard install locations, most common first. A
// var for the same reason openEditor is one: the paths are absolute and real, so
// a test cannot reach the fallback without replacing them.
var editorCandidates = systemEditorCandidates

// systemEditorCandidates is where VS Code installs itself when it is not on
// PATH.
//
// The Linux entries are checkable here; the macOS ones are the documented bundle
// paths and could not be measured on this machine, which is why they are last —
// a wrong guess there costs a stat, not a wrong window.
func systemEditorCandidates() []string {
	paths := []string{
		"/usr/bin/code",                                      // deb / rpm
		"/usr/local/bin/code",                                // hand-made link
		"/usr/share/code/bin/code",                           // what the deb/rpm link points at
		"/snap/bin/code",                                     // snap
		"/opt/visual-studio-code/bin/code",                   // AUR, unpacked tarball
		"/var/lib/flatpak/exports/bin/com.visualstudio.code", // flatpak, system
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".local/share/flatpak/exports/bin/com.visualstudio.code"),
			filepath.Join(home, ".local/bin/code"),
			filepath.Join(home, "Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"),
		)
	}
	return append(paths,
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
	)
}

// isExecutableFile reports whether path is a regular file anyone may execute.
// A directory with the right name is not a candidate, and neither is a file the
// user cannot run.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// startEditor opens dir in the editor at bin.
//
// WSL needs no special case: the shim reads $WSL_DISTRO_NAME itself and rewrites
// the argument into a `--remote wsl+<distro>` invocation, so a plain Linux path
// is what it wants.
func startEditor(bin, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), editorTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, dir)

	// The panel is an alt-screen TUI sharing this terminal. One line written by
	// a child lands on top of the list and stays there until the next redraw, so
	// the child gets no terminal at all. nil is /dev/null.
	cmd.Stdin, cmd.Stdout = nil, nil

	// stderr is kept, because it is the only thing that can say *why*. See the
	// error path below and tmux/runner.go's withStderr, which exists for the
	// same reason.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Its own session, so closing the panel's pane cannot take a half-started
	// editor with it: tmux signals the pane's process group, and until the shim
	// has handed off to the real application it is still in ours.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// And not our tmux. A VS Code server launched from a pane inherits TMUX,
	// hands it to every integrated terminal, and `tmux attach` in those then
	// refuses to nest — the tmux-project terminal tab closes the moment it
	// opens. Measured: two vscode-server processes carrying TMUX=…,4.
	// 화면·입력기 변수는 데스크톱 세션의 것으로 — editorEnv.
	cmd.Env = editorEnv()

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s did not return within %s", editorCommand, editorTimeout)
	}
	if err == nil {
		return nil
	}
	// Waiting for the exit code rather than firing and forgetting buys exactly
	// this: under WSL the panel inherits WSL_INTEROP from the tmux server, and
	// that socket dies with the WSL session while the tmux server outlives it —
	// after which launching anything under /mnt/c fails. Started and forgotten,
	// that failure reaches nobody and the click just appears to do nothing.
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return fmt.Errorf("%w: %s", err, firstLine(msg))
	}
	return err
}

// detachGrace 는 기다리지 않는 실행을 지켜보는 시간이다. 그 안에 끝난 런처의 실패만
// 보고한다 — DISPLAY 가 없는 콜드 스타트처럼 바로 죽는 경우다(실측 0.39초, 종료 코드 3,
// 별도 config·system 경로로 띄운 IntelliJ 2026.2.3). 그보다 오래 사는 것은 IDE 가 됐거나
// 대화상자를 기다리는 중이라, 기다릴 것이 없다.
const detachGrace = 2 * time.Second

// openDetached 는 openEditor 와 같은 이음새다.
var openDetached = startDetached

// startDetached 는 bin 으로 dir 을 열고, 오래 걸리면 기다리지 않는다. IntelliJ 용.
// 이유는 파일 머리의 choiceIntelliJ 설명.
func startDetached(bin, dir string) error { return startDetachedWithin(bin, dir, detachGrace) }

// startDetachedWithin 은 startDetached 의 몸체다. grace 는 테스트가 짧게 주려고 받는다.
//
// startEditor 와 같은 것: 자기 세션(Setsid), 이 tmux 를 뺀 환경, 터미널 없음.
// 다른 것 셋:
//   - context 가 없다. 유예가 지나도 죽이지 않는다 — 그 프로세스가 IDE 일 수 있다.
//   - stderr 도 받지 않는다. 버퍼는 며칠 사는 IDE 의 출력을 패널 메모리에 쌓고, 파이프는
//     IDE 의 stderr 를 패널의 수명에 묶는다. 실패 이유는 IDE 자신의 로그(idea.log)에 있다.
//   - Wait 는 고루틴이 맡아 좀비를 거둔다. 그 고루틴은 패널이 `syscall.Exec` 로 스스로
//     재시작하면 사라지므로, 그 뒤에 IDE 가 끝나면 패널이 끝날 때까지 좀비가 하나
//     남는다. pid 표 항목 하나라 받아들인다.
func startDetachedWithin(bin, dir string, grace time.Duration) error {
	cmd := exec.Command(bin, dir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = editorEnv()
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w — 이유는 IDE 로그에", err)
		}
		return nil
	case <-timer.C:
		return nil
	}
}

// firstLine keeps an error to one line. It is drawn into a panel row that is
// about forty cells wide, and a stack trace there costs the whole log.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// editorEnv 는 편집기에 넘길 환경이다: 패널의 환경에서 tmux 를 빼고(withoutTmux), 화면·입력기
// 변수는 데스크톱 세션의 값으로 덮는다(withDesktopEnv).
//
// 패널의 환경은 패널이 생길 때의 tmux 서버 환경이고, 패널은 며칠 산다. 그 사이 데스크톱
// 설정이 바뀌면 패널이 띄운 편집기만 옛 값을 갖는다. 2026-10-21 실측: 12:56 에 생긴 my-mux
// 패널에는 그 뒤 environment.d 에 더한 XMODIFIERS=@im=ibus 가 없었고, 그 패널이 콜드 스타트한
// IntelliJ(X11, XIM 으로만 ibus 에 붙는다)는 한글이 한 글자도 안 들어갔다. GNOME 메뉴로 띄운
// 앱이 받는 값(systemd 사용자 매니저, gnome-shell 의 환경)에는 있었다.
func editorEnv() []string {
	return withDesktopEnv(withoutTmux(os.Environ()), desktopSessionEnv())
}

// desktopEnvVars 는 데스크톱 세션의 값을 따르는 변수다. 화면에 붙는 법과 입력기에 붙는 법
// 뿐이다 — PATH 같은 나머지는 패널의 것을 둔다(WSL 의 code 셤은 패널의 WSL_* 가 있어야 돈다).
var desktopEnvVars = []string{
	"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP",
	"DBUS_SESSION_BUS_ADDRESS",
	"XMODIFIERS", "GTK_IM_MODULE", "QT_IM_MODULE", "GLFW_IM_MODULE", "SDL_IM_MODULE",
}

// desktopSessionEnv 는 데스크톱이 앱을 띄울 때 넘기는 환경을 `systemctl --user
// show-environment` 로 읽는다. 물을 수 없으면(systemd 사용자 매니저 없음) nil — 그러면 패널의
// 환경 그대로다. var 인 것은 openEditor 와 같은 이유다: 테스트가 개발자의 systemd 에 묻지 않게.
var desktopSessionEnv = systemdUserEnv

func systemdUserEnv() []string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// withDesktopEnv 는 environ 의 desktopEnvVars 를 desktop 의 값으로 덮고, environ 에 없던 것은
// 뒤에 더한다. 나머지는 손대지 않는다. Pure for testing.
//
// systemctl 은 특수 문자가 든 값을 $'…' 로 인용해 찍는다. 그런 값은 풀지 않고 버린다 — 이
// 목록의 변수에 그런 값이 올 일이 없고, 잘못 푼 DISPLAY 는 없는 것보다 나쁘다.
func withDesktopEnv(environ, desktop []string) []string {
	want := make(map[string]string)
	for _, kv := range desktop {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(v, "$'") || !slices.Contains(desktopEnvVars, k) {
			continue
		}
		want[k] = v
	}
	out := make([]string, 0, len(environ)+len(want))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if v, ok := want[k]; ok {
			out = append(out, k+"="+v)
			delete(want, k)
			continue
		}
		out = append(out, kv)
	}
	for _, k := range desktopEnvVars { // 더하는 순서를 정해 둔다
		if v, ok := want[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// withoutTmux drops the variables that would make a child believe it lives
// inside this tmux. Pure for testing.
func withoutTmux(environ []string) []string {
	out := environ[:0:0]
	for _, kv := range environ {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
