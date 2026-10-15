package ui

import (
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

func TestParseActivateReply(t *testing.T) {
	if !parseActivateReply([]byte(`{"type":"b","data":[true]}`)) {
		t.Error("true reply read as false")
	}
	for _, bad := range []string{`{"type":"b","data":[false]}`, "", "junk", `{"type":"b","data":[]}`} {
		if parseActivateReply([]byte(bad)) {
			t.Errorf("%q read as true", bad)
		}
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
