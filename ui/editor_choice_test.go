package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeScript 는 dir 안에 실행 가능한 셸 스크립트 name 을 만든다.
func writeFakeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// IntelliJ 도 VS Code 와 같은 규칙으로 찾는다 — PATH 가 먼저, 그다음 설치 위치.
// Toolbox 는 PATH 에 아무것도 올리지 않는 것이 기본이라(실측) 후보가 실제로 쓰인다.
func TestFindIntelliJ(t *testing.T) {
	withCandidates := func(t *testing.T, paths ...string) {
		prev := intellijCandidates
		intellijCandidates = func() []string { return paths }
		t.Cleanup(func() { intellijCandidates = prev })
	}

	t.Run("PATH first", func(t *testing.T) {
		pathDir := t.TempDir()
		onPath := writeFakeScript(t, pathDir, "idea", "")
		toolbox := writeFakeScript(t, t.TempDir(), "idea", "")
		t.Setenv("PATH", pathDir)
		withCandidates(t, toolbox)
		if got := findIntelliJ(); got != onPath {
			t.Errorf("findIntelliJ = %q, want the one on PATH (%q)", got, onPath)
		}
	})

	t.Run("falls back to the install locations", func(t *testing.T) {
		toolbox := writeFakeScript(t, t.TempDir(), "idea", "")
		t.Setenv("PATH", t.TempDir())
		withCandidates(t, "/nope/idea", toolbox)
		if got := findIntelliJ(); got != toolbox {
			t.Errorf("findIntelliJ = %q, want %q", got, toolbox)
		}
	})

	t.Run("empty when absent", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		withCandidates(t, "/nope/idea")
		if got := findIntelliJ(); got != "" {
			t.Errorf("findIntelliJ = %q, want empty", got)
		}
	})
}

// 이 테스트가 IntelliJ 를 startEditor 로 띄우지 않는 이유를 지킨다. IntelliJ 의 런처는
// 꺼져 있으면 그 자신이 IDE 가 되고, 떠 있으면 프로젝트 열기가 끝날 때까지(신뢰
// 대화상자 포함, 실측 131초) 돌아오지 않는다. 기다리다 죽이면 IDE 가 죽는다 — 그래서
// 유예 뒤에는 손만 떼고, 프로세스는 살아 있어야 한다.
func TestStartDetachedLeavesALongLaunchRunning(t *testing.T) {
	withDesktopSession(t)
	dir := t.TempDir()
	bin := writeFakeScript(t, dir, "idea", "sleep 0.3\ntouch \"$1/survived\"\n")

	start := time.Now()
	if err := startDetachedWithin(bin, dir, 50*time.Millisecond); err != nil {
		t.Fatalf("startDetachedWithin = %v, want nil for a launch still running", err)
	}
	if waited := time.Since(start); waited > 250*time.Millisecond {
		t.Errorf("waited %s for a launch that outlives the grace", waited)
	}

	marker := filepath.Join(dir, "survived")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the launch never finished — it was killed when the grace ran out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 유예 안에 실패로 끝난 런처는 보고한다 — 오른쪽 클릭은 사람이 누른 것이고, 말없이
// 끝나면 클릭이 아무 일도 안 한 것처럼 보인다(startEditor 가 기다리는 이유와 같다).
func TestStartDetachedReportsAFastFailure(t *testing.T) {
	withDesktopSession(t)
	bin := writeFakeScript(t, t.TempDir(), "idea", "exit 3\n")
	err := startDetachedWithin(bin, t.TempDir(), 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("startDetachedWithin = %v, want the exit status", err)
	}

	if err := startDetachedWithin("/nope/idea", t.TempDir(), time.Second); err == nil {
		t.Error("a binary that is not there was reported as launched")
	}
}

// intellijTestModel 은 IntelliJ 가 설치돼 있고 @mux_editor 가 choice 인 패널이다.
func intellijTestModel(choice string) watchModel {
	m := editorTestModel("/usr/bin/code")
	m.ideaBin = "/opt/idea/bin/idea"
	m.editorChoice = func() string { return choice }
	return m
}

// @mux_editor=idea 면 오른쪽 클릭은 IntelliJ 를 띄운다. 그 폴더에 VS Code 창이 열려
// 있어도 그 창을 올리지 않는다 — 사용자가 고른 것은 IntelliJ 다.
func TestRightClickOpensIntelliJWhenChosen(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenVSCodeWindows(t, "/home/u/projects/api")
	var asked []string

	m := intellijTestModel("idea")
	m.focusWindow = fakeFocus(focusRaised, &asked)
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if cmd == nil {
		t.Fatal("the right click issued no command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("unexpected message: %#v", msg)
	}

	want := "detached /opt/idea/bin/idea /home/u/projects/api"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("editor calls = %v, want [%q]", *calls, want)
	}
	if len(asked) != 0 {
		t.Errorf("asked the shell to raise a VS Code window: %v", asked)
	}
}

// 고른 편집기가 없거나 이름을 모르면 아무것도 띄우지 않고 그렇다고 말한다. 조용히
// VS Code 로 물러서면 사용자는 설정이 먹었는지 알 길이 없다.
func TestRightClickReportsAnEditorItCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name, choice, codeBin, ideaBin, want string
	}{
		{"IntelliJ chosen but not installed", "idea", "/usr/bin/code", "", "IntelliJ 실행 실패"},
		{"IntelliJ chosen and no editor at all", "idea", "", "", "IntelliJ 실행 실패"},
		{"a value that names no editor", "vim", "/usr/bin/code", "/opt/idea/bin/idea", "@mux_editor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeTmux(t, &fakeRunner{})
			calls := withFakeEditor(t, nil)

			m := intellijTestModel(tc.choice)
			m.editorBin, m.ideaBin = tc.codeBin, tc.ideaBin
			_, cmd := m.Update(rightPress(firstSessionRow(m)))
			msg := cmd()
			failed, ok := msg.(editorFailedMsg)
			if !ok {
				t.Fatalf("msg = %#v, want editorFailedMsg", msg)
			}
			if len(*calls) != 0 {
				t.Errorf("launched something anyway: %v", *calls)
			}

			updated, _ := m.Update(failed)
			got := updated.(watchModel)
			if len(got.events) != 1 || !strings.Contains(got.events[0].text, tc.want) {
				t.Errorf("events = %v, want a line containing %q", got.events, tc.want)
			}
		})
	}
}

// 설정이 없거나 code 면 지금까지의 VS Code 그대로다.
func TestRightClickDefaultsToVSCode(t *testing.T) {
	for _, choice := range []string{"", "code"} {
		withFakeTmux(t, &fakeRunner{})
		calls := withFakeEditor(t, nil)
		withOpenVSCodeWindows(t)

		m := intellijTestModel(choice)
		_, cmd := m.Update(rightPress(firstSessionRow(m)))
		if msg := cmd(); msg != nil {
			t.Fatalf("choice %q: unexpected message: %#v", choice, msg)
		}
		want := "/usr/bin/code /home/u/projects/api"
		if len(*calls) != 1 || (*calls)[0] != want {
			t.Errorf("choice %q: editor calls = %v, want [%q]", choice, *calls, want)
		}
	}
}

// 고르는 것은 오른쪽 클릭의 편집기뿐이다. 왼쪽 클릭은 전환하고 이미 열린 VS Code 창만
// 올린다 — IntelliJ 를 띄우지 않는다.
func TestLeftClickIgnoresTheEditorChoice(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenVSCodeWindows(t, "/home/u/projects/api")

	m := intellijTestModel("idea")
	m.selected = "demo"
	_, cmd := m.Update(leftPress(firstSessionRow(m)))
	cmd()
	want := "/usr/bin/code /home/u/projects/api"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("editor calls = %v, want only the VS Code raise [%q]", *calls, want)
	}
}

// 패널에서 띄운 편집기는 패널의 환경을 물려받는데, 패널은 며칠 사는 프로세스라 그 환경이
// 데스크톱보다 오래됐을 수 있다. 2026-10-21: 오늘 12:56 에 생긴 패널에는 그 뒤에 더한
// XMODIFIERS 가 없었고, 그 패널이 띄운 IntelliJ(X11)는 ibus 에 붙지 못해 한글이 안 들어갔다.
// 그래서 화면·입력기 변수만 데스크톱 세션의 값으로 덮는다. 나머지는 패널의 것 그대로다.
func TestWithDesktopEnv(t *testing.T) {
	pane := []string{"HOME=/h", "DISPLAY=:0", "GLFW_IM_MODULE=ibus", "PATH=/bin"}
	desktop := []string{
		"DISPLAY=:1",                // 덮는다
		"XMODIFIERS=@im=ibus",       // 패널에 없던 것 — 더한다
		"PATH=/desktop/bin",         // 목록 밖 — 패널 것을 둔다
		"GTK_IM_MODULE=$'weird\\n'", // systemctl 의 $'…' 인용은 풀지 않고 버린다
		"garbage",
	}
	got := withDesktopEnv(pane, desktop)
	want := []string{"HOME=/h", "DISPLAY=:1", "GLFW_IM_MODULE=ibus", "PATH=/bin", "XMODIFIERS=@im=ibus"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v\nwant %v", got, want)
	}

	// 데스크톱 세션을 물을 수 없으면(systemd 없음, WSL) 패널의 환경 그대로다.
	if got := withDesktopEnv(pane, nil); strings.Join(got, ",") != strings.Join(pane, ",") {
		t.Errorf("with no desktop env: got %v, want the pane's own", got)
	}
}

// startDetachedWithin 이 넘기는 환경에 데스크톱의 입력기 변수가 실제로 들어간다.
func TestStartDetachedPassesTheDesktopInputMethod(t *testing.T) {
	withDesktopSession(t, "XMODIFIERS=@im=ibus")
	t.Setenv("XMODIFIERS", "")
	dir := t.TempDir()
	bin := writeFakeScript(t, dir, "idea", "printf %s \"$XMODIFIERS\" > \"$1/env\"\n")

	if err := startDetachedWithin(bin, dir, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil || string(got) != "@im=ibus" {
		t.Errorf("child saw XMODIFIERS=%q (%v), want @im=ibus", got, err)
	}
}

// withDesktopSession 은 데스크톱 세션의 환경을 정해 준다 — 테스트가 개발자의 systemd 에
// 묻지 않게.
func withDesktopSession(t *testing.T, env ...string) {
	t.Helper()
	prev := desktopSessionEnv
	desktopSessionEnv = func() []string { return env }
	t.Cleanup(func() { desktopSessionEnv = prev })
}
