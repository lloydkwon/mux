package ui

import (
	"errors"
	"testing"
)

// VS Code 의 기본 창 제목은 `${dirty}${activeEditorShort} - ${rootName} - ${appName}` 이고,
// 편집기가 없으면 앞 칸이, 원격이면 rootName 뒤에 « [WSL: …]» 가 붙는다. 폴더는 rootName
// 칸과 «같아야» 한다 — 부분 문자열이면 mux 를 찾다 my-mux 창을 올린다.
func TestPickEditorWindow(t *testing.T) {
	wins := []shellWindow{
		{ID: 1, Title: "main.go - my-mux - Visual Studio Code"},
		{ID: 2, Title: "● watch.go - mux - Visual Studio Code"},
		{ID: 3, Title: "water_ai - Visual Studio Code"},
		{ID: 4, Title: "README.md - api [WSL: Ubuntu] - Visual Studio Code"},
		{ID: 5, Title: "doublecmd - Settings - Visual Studio Code"}, // 열린 파일 이름이 우연히 doublecmd
		{ID: 6, Title: "x.go - dup - Visual Studio Code"},
		{ID: 7, Title: "y.go - dup - Visual Studio Code"},
	}
	cases := []struct {
		dir    string
		want   uint64
		wantOK bool
	}{
		{"/home/u/dev/projects/my/mux", 2, true},
		{"/home/u/dev/projects/my/my-mux", 1, true},
		{"/home/u/dev/projects/si/water_ai/", 3, true},
		{"/home/u/projects/api", 4, true},
		{"/home/u/dev/projects/si/water_quality", 0, false},
		// 열린 파일 이름은 폴더가 아니다 — 폴더 칸만 본다.
		{"/home/u/bin/doublecmd", 0, false},
		// 같은 이름의 폴더 창이 둘이면 어느 쪽인지 모른다. 엉뚱한 창을 올리느니 올리지 않는다.
		{"/home/u/a/dup", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := pickEditorWindow(wins, c.dir)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%q → (%d, %v), want (%d, %v)", c.dir, got, ok, c.want, c.wantOK)
		}
	}
}

// busctl --json=short 는 문자열 하나를 {"type":"s","data":["…"]} 로 낸다. 그 안이 확장이
// 만든 JSON 배열이다. 어느 겹이든 깨지면 «창 없음» — 그러면 `code <폴더>` 로 물러선다.
func TestParseWindowList(t *testing.T) {
	out := `{"type":"s","data":["[{\"id\":7,\"title\":\"x - mux - Visual Studio Code\"}]"]}`
	got := parseWindowList([]byte(out))
	if len(got) != 1 || got[0].ID != 7 || got[0].Title != "x - mux - Visual Studio Code" {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []string{"", "not json", `{"type":"s","data":[]}`, `{"type":"s","data":["nope"]}`} {
		if got := parseWindowList([]byte(bad)); len(got) != 0 {
			t.Errorf("%q → %+v, want nothing", bad, got)
		}
	}
}

func TestParseBoolReply(t *testing.T) {
	if !parseBoolReply([]byte(`{"type":"b","data":[true]}`)) {
		t.Error("true reply read as false")
	}
	for _, bad := range []string{`{"type":"b","data":[false]}`, "", "junk", `{"type":"b","data":[]}`} {
		if parseBoolReply([]byte(bad)) {
			t.Errorf("%q read as true", bad)
		}
	}
}

// withFakeBus stands in for the session bus: what List answers (an error means
// the extension's object is not there) and whether GNOME Shell owns its name.
func withFakeBus(t *testing.T, list string, listErr error, shellRunning bool) *[]string {
	t.Helper()
	var calls []string
	prevCall, prevShell := busctlCall, gnomeShellRunning
	busctlCall = func(method string, args ...string) ([]byte, error) {
		calls = append(calls, method)
		switch method {
		case "List":
			return []byte(list), listErr
		case "Activate":
			return []byte(`{"type":"b","data":[true]}`), nil
		}
		return nil, errors.New("unexpected method " + method)
	}
	gnomeShellRunning = func() bool { return shellRunning }
	t.Cleanup(func() { busctlCall, gnomeShellRunning = prevCall, prevShell })
	return &calls
}

// 화면이 잠기면 GNOME 은 확장을 끈다. 그때 List 는 «그런 객체가 없다»로 실패하는데,
// 이것을 «물을 수 없다»로 읽으면 `code <폴더>` 로 물러서고 — 실측 1.04초 — 그동안
// prefix+m 팝업이 빈 사각형으로 남았다(2026-10-16, 밖에서 ssh 로 쓰던 중). Wayland 에서는
// `code` 로 창이 올라오지도 않으므로, GNOME 이 떠 있는데 확장만 없으면 «창 없음»이다.
// GNOME 자체가 없을 때(WSL, 다른 데스크톱)만 `code` 가 대안이다.
func TestFocusShellWindowWithoutExtension(t *testing.T) {
	gone := errors.New("exit status 1")
	list := `{"type":"s","data":["[{\"id\":7,\"title\":\"x - api - Visual Studio Code\"}]"]}`
	cases := []struct {
		name    string
		list    string
		listErr error
		shell   bool
		want    focusResult
	}{
		{"GNOME 은 있고 확장이 없다(잠금 화면)", "", gone, true, focusNoWindow},
		{"GNOME 이 없다", "", gone, false, focusUnavailable},
		{"확장이 답한다", list, nil, true, focusRaised},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFakeBus(t, c.list, c.listErr, c.shell)
			if got := focusShellWindow("/home/u/projects/api"); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// 끝까지 이어 보면: 잠금 화면에서 팝업·왼쪽 클릭의 창 올리기는 `code` 를 부르지 않는다.
func TestRaiseOpenEditorSkipsCodeWhenTheShellHasNoExtension(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenVSCodeWindows(t, "/home/u/projects/api")
	withFakeBus(t, "", errors.New("exit status 1"), true)

	raiseOpenEditor("api", "/usr/bin/code", "/home/u/projects/api", focusShellWindow)
	if len(*calls) != 0 {
		t.Errorf("ran the editor while GNOME had no extension to raise with: %v", *calls)
	}
}

// fakeFocus stands in for the GNOME Shell extension: it says whether a window
// came forward and records what it was asked for.
func fakeFocus(r focusResult, asked *[]string) func(string) focusResult {
	return func(dir string) focusResult {
		*asked = append(*asked, dir)
		return r
	}
}

// On native Wayland `code <folder>` cannot bring an open window forward — the
// compositor drops a focus request that carries no activation token, and none
// survives the trip through tmux. So the window is raised through the shell
// first, and `code` is only the fallback for when that cannot be done (no
// extension, WSL, no matching window).
func TestRaiseOpenEditorFocusesBeforeLaunching(t *testing.T) {
	t.Run("focused: code is not run", func(t *testing.T) {
		withFakeTmux(t, &fakeRunner{})
		calls := withFakeEditor(t, nil)
		withOpenVSCodeWindows(t, "/home/u/projects/api")
		var asked []string

		raiseOpenEditor("api", "/usr/bin/code", "/home/u/projects/api", fakeFocus(focusRaised, &asked))
		if len(asked) != 1 || asked[0] != "/home/u/projects/api" {
			t.Errorf("focus asked for %v", asked)
		}
		if len(*calls) != 0 {
			t.Errorf("ran the editor after the window was already raised: %v", *calls)
		}
	})

	t.Run("shell unreachable: falls back to code", func(t *testing.T) {
		withFakeTmux(t, &fakeRunner{})
		calls := withFakeEditor(t, nil)
		withOpenVSCodeWindows(t, "/home/u/projects/api")
		var asked []string

		raiseOpenEditor("api", "/usr/bin/code", "/home/u/projects/api", fakeFocus(focusUnavailable, &asked))
		if len(*calls) != 1 {
			t.Errorf("editor calls = %v, want the code fallback once", *calls)
		}
	})

	// The shell answering "no such window" is an answer, not a failure: the
	// record that said a window was open is stale. Falling back to `code` there
	// would launch one, which a left click must never do.
	t.Run("shell has no such window: nothing", func(t *testing.T) {
		withFakeTmux(t, &fakeRunner{})
		calls := withFakeEditor(t, nil)
		withOpenVSCodeWindows(t, "/home/u/projects/api")
		var asked []string

		raiseOpenEditor("api", "/usr/bin/code", "/home/u/projects/api", fakeFocus(focusNoWindow, &asked))
		if len(*calls) != 0 {
			t.Errorf("a left click launched an editor on a stale record: %v", *calls)
		}
	})

	t.Run("no window open: neither", func(t *testing.T) {
		withFakeTmux(t, &fakeRunner{})
		calls := withFakeEditor(t, nil)
		withOpenVSCodeWindows(t)
		var asked []string

		raiseOpenEditor("api", "/usr/bin/code", "/home/u/projects/api", fakeFocus(focusRaised, &asked))
		if len(asked) != 0 || len(*calls) != 0 {
			t.Errorf("focus %v / editor %v with no window open", asked, *calls)
		}
	})
}

// A right click on a session whose window is already open hits the same wall:
// `code` would leave the window where it is. Raising first makes the click do
// something visible; launching stays the fallback, so a session without a
// window still gets one.
func TestRightClickFocusesAnOpenWindowFirst(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenVSCodeWindows(t, "/home/u/projects/api")
	var asked []string

	m := editorTestModel("/usr/bin/code")
	m.focusWindow = fakeFocus(focusRaised, &asked)
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if cmd == nil {
		t.Fatal("the right click issued no command")
	}
	cmd()
	if len(asked) != 1 {
		t.Errorf("focus asked %v, want once", asked)
	}
	if len(*calls) != 0 {
		t.Errorf("launched an editor for a window that came forward: %v", *calls)
	}

	asked = nil
	m.focusWindow = fakeFocus(focusNoWindow, &asked)
	_, cmd = m.Update(rightPress(firstSessionRow(m)))
	cmd()
	if len(*calls) != 1 {
		t.Errorf("editor calls = %v, want the launch fallback", *calls)
	}
}

// A right click on a folder with no window of its own must launch, even when
// some other window shares its base name — the shell can only match by name,
// so asking it there would raise ~/work/api for a click on ~/personal/api and
// open nothing.
func TestRightClickOnAFolderWithoutAWindowDoesNotAskTheShell(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenVSCodeWindows(t, "/home/u/work/api")
	var asked []string

	m := editorTestModel("/usr/bin/code") // 세션 폴더는 /home/u/projects/api
	m.focusWindow = fakeFocus(focusRaised, &asked)
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	cmd()
	if len(asked) != 0 {
		t.Errorf("asked the shell for a folder with no window: %v", asked)
	}
	if len(*calls) != 1 {
		t.Errorf("editor calls = %v, want a launch", *calls)
	}
}

// IntelliJ 창 제목은 «프로젝트이름 – 무엇»이다(구분자 en dash, 실측 2026-10-21: `mux – Claude Code`,
// `matchingByJev – Local`). 첫 토막만 본다 — 뒤 토막은 열린 파일·도구 창이라 그 이름이 프로젝트와
// 같아도 프로젝트가 아니다. 같음 비교라 mux 를 찾다 my-mux 를 올리지 않고, 같은 이름 둘이면 답하지
// 않는다. VS Code 창(app 이 vscode 이거나 비어 있음)은 제목이 무엇이든 보지 않는다.
func TestPickIntelliJWindow(t *testing.T) {
	wins := []shellWindow{
		{ID: 1, Title: "mux – Claude Code", App: "idea"},
		{ID: 2, Title: "my-mux – watch.go", App: "idea"},
		{ID: 3, Title: "matchingByJev", App: "idea"},
		{ID: 4, Title: "web – mux", App: "idea"}, // 뒤 토막의 mux 는 프로젝트가 아니다
		{ID: 5, Title: "dup – a", App: "idea"},
		{ID: 6, Title: "dup – b", App: "idea"},
		{ID: 7, Title: "api – x", App: "vscode"},
		{ID: 8, Title: "api – y"}, // 옛 확장: app 없음 = vscode
	}
	for _, c := range []struct {
		dir    string
		want   uint64
		wantOK bool
	}{
		{"/home/u/dev/projects/my/mux", 1, true},
		{"/home/u/dev/projects/my/my-mux/", 2, true},
		{"/srv/work/matchingByJev", 3, true},
		{"/home/u/a/dup", 0, false},
		{"/home/u/api", 0, false},
		{"", 0, false},
	} {
		got, ok := pickIntelliJWindow(wins, c.dir)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%q → (%d, %v), want (%d, %v)", c.dir, got, ok, c.want, c.wantOK)
		}
	}
}

// VS Code 규칙은 IntelliJ 창을 보지 않는다 — 제목이 우연히 VS Code 모양이어도.
func TestPickEditorWindowIgnoresIntelliJ(t *testing.T) {
	wins := []shellWindow{{ID: 9, Title: "x - mux - Visual Studio Code", App: "idea"}}
	if _, ok := pickEditorWindow(wins, "/p/mux"); ok {
		t.Error("an IntelliJ window was picked by the VS Code rule")
	}
}

func TestParseWindowListReadsApp(t *testing.T) {
	out := `{"type":"s","data":["[{\"id\":7,\"title\":\"mux – Chat\",\"app\":\"idea\"}]"]}`
	got := parseWindowList([]byte(out))
	if len(got) != 1 || got[0].App != "idea" {
		t.Errorf("got %+v", got)
	}
}

// IntelliJ 도 같은 shell 경로로 올린다 — 목록에서 고르고 Activate.
func TestFocusShellIntelliJ(t *testing.T) {
	list := `{"type":"s","data":["[{\"id\":5,\"title\":\"x - mux - Visual Studio Code\",\"app\":\"vscode\"},{\"id\":6,\"title\":\"mux – Chat\",\"app\":\"idea\"}]"]}`
	calls := withFakeBus(t, list, nil, true)
	if r := focusShellIntelliJ("/p/mux"); r != focusRaised {
		t.Errorf("focusShellIntelliJ = %v, want raised", r)
	}
	if len(*calls) != 2 || (*calls)[1] != "Activate" {
		t.Errorf("bus calls %v, want List then Activate", *calls)
	}
	if r := focusShellIntelliJ("/p/web"); r != focusNoWindow {
		t.Errorf("no IntelliJ window on web: %v, want no window", r)
	}
}
