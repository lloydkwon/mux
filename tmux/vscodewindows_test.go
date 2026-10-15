package tmux

import (
	"os"
	"testing"
)

// 지금 storage.json 모양 그대로: 원격 폴더 창, 폴더 없는 창(복원 백업만), 로컬 창.
const storageFixture = `{
  "windowsState": {
    "lastActiveWindow": { "folder": "vscode-remote://wsl%2Bubuntu-24.04/home/u/dev/projects/my/my-mux" },
    "openedWindows": [
      { "folder": "vscode-remote://wsl%2Bubuntu-24.04/home/u/dev/projects/si/water_ai", "uiState": {} },
      { "backupPath": "C:\\Users\\u\\AppData\\Roaming\\Code\\Backups\\1", "uiState": {} },
      { "folder": "file:///c%3A/Users/u/dev/local-only" },
      { "folder": "vscode-remote://wsl%2Bubuntu-24.04/home/u/dev/projects/my/my-mux/" }
    ]
  }
}`

func TestParseOpenedWindows(t *testing.T) {
	got := parseOpenedWindows([]byte(storageFixture), false)
	want := []string{"/home/u/dev/projects/si/water_ai", "/home/u/dev/projects/my/my-mux"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
	if parseOpenedWindows([]byte("not json"), false) != nil {
		t.Error("깨진 JSON 에서 폴더가 나왔다")
	}
}

func TestWSLFolderFromURI(t *testing.T) {
	cases := map[string]string{
		"vscode-remote://wsl%2Bubuntu-24.04/home/u/p": "/home/u/p",
		"vscode-remote://wsl%2BUbuntu/home/u/p/":      "/home/u/p",
		"vscode-remote://ssh-remote%2Bhost/home/u/p":  "",
		"file:///c%3A/Users/u/dev":                    "",
		"vscode-remote://wsl%2Bubuntu-24.04/":         "",
		"":                                            "",
	}
	for in, want := range cases {
		if got := wslFolderFromURI(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// 네이티브 리눅스의 storage.json 은 창 폴더를 file:// 로 적는다(2026-10-15 이 기계에서 확인).
// 같은 파일 모양을 WSL 쪽에서 읽으면 file:// 는 Windows 경로라 버려야 하므로, 어느 쪽에서
// 읽었는지가 판단을 가른다.
const nativeStorageFixture = `{
  "windowsState": {
    "lastActiveWindow": { "folder": "file:///home/u/dev/projects/my/mux" },
    "openedWindows": [
      { "folder": "file:///home/u/dev/projects/si/water_ai" },
      { "folder": "file:///home/u/dev/projects/my/mux/" },
      { "folder": "file:///home/u/dev/%ED%95%9C%EA%B8%80" },
      { "folder": "vscode-remote://ssh-remote%2Bhost/home/u/p" },
      { "backupPath": "/home/u/.config/Code/Backups/1" }
    ]
  }
}`

func TestParseOpenedWindowsNative(t *testing.T) {
	got := parseOpenedWindows([]byte(nativeStorageFixture), true)
	want := []string{"/home/u/dev/projects/si/water_ai", "/home/u/dev/projects/my/mux", "/home/u/dev/한글"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
	if got := parseOpenedWindows([]byte(nativeStorageFixture), false); len(got) != 0 {
		t.Errorf("WSL 쪽에서 읽은 file:// 가 폴더로 나왔다: %v", got)
	}
}

func TestLocalFolderFromURI(t *testing.T) {
	cases := map[string]string{
		"file:///home/u/p":          "/home/u/p",
		"file:///home/u/p/":         "/home/u/p",
		"file:///c%3A/Users/u/dev":  "",
		"file:///":                  "",
		"vscode-remote://wsl%2Bx/p": "",
		"":                          "",
	}
	for in, want := range cases {
		if got := localFolderFromURI(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// VS Code 는 끝날 때 다음에 되살릴 창을 openedWindows 에 남겨 둔다. 그 목록을 «열려 있다»로
// 읽으면, VS Code 가 꺼진 동안의 좌클릭이 `code <폴더>` 로 VS Code 를 새로 띄운다 —
// 좌클릭은 열린 창을 앞으로 낼 뿐 띄우지 않는다는 원칙이 깨진다.
// 창이 하나뿐일 때 VS Code 가 그 창을 lastActiveWindow 에만 적을 수 있다(검토에서 나온 의심,
// 소스로 확인하지 못함). 네이티브 기록에서는 그 칸도 읽는다 — 이미 목록에 있으면 한 번만.
func TestParseOpenedWindowsReadsLastActiveNatively(t *testing.T) {
	only := `{"windowsState":{"lastActiveWindow":{"folder":"file:///home/u/p"},"openedWindows":[]}}`
	if got := parseOpenedWindows([]byte(only), true); len(got) != 1 || got[0] != "/home/u/p" {
		t.Errorf("got %v, want [/home/u/p]", got)
	}
	if got := parseOpenedWindows([]byte(nativeStorageFixture), true); len(got) != 3 {
		t.Errorf("lastActiveWindow 가 중복으로 들어갔다: %v", got)
	}
}

func TestOpenVSCodeFoldersIgnoresStorageWhenVSCodeIsNotRunning(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/storage.json"
	if err := os.WriteFile(path, []byte(nativeStorageFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	origPath, origRunning, origGlob := nativeVSCodeStorage, vscodeRunning, wslVSCodeStorageGlob
	t.Cleanup(func() { nativeVSCodeStorage, vscodeRunning, wslVSCodeStorageGlob = origPath, origRunning, origGlob })
	nativeVSCodeStorage = func() string { return path }
	wslVSCodeStorageGlob = dir + "/no-such-*/storage.json"

	vscodeRunning = func() bool { return false }
	if got := openVSCodeFolders(); len(got) != 0 {
		t.Errorf("VS Code 가 꺼져 있는데 열린 창이 있다고 했다: %v", got)
	}
	vscodeRunning = func() bool { return true }
	if got := openVSCodeFolders(); len(got) != 3 {
		t.Errorf("VS Code 가 떠 있을 때 got %v, want 3 folders", got)
	}
}
