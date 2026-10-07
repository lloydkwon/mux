package tmux

import "testing"

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
	got := parseOpenedWindows([]byte(storageFixture))
	want := []string{"/home/u/dev/projects/si/water_ai", "/home/u/dev/projects/my/my-mux"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
	if parseOpenedWindows([]byte("not json")) != nil {
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
