package tmux

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// 어떤 프로젝트가 VS Code 창으로 열려 있는지는 tmux 로는 알 수 없다 — 그 창의 통합
// 터미널이 세션에 붙어 있을 때만 보인다. VS Code 자신은 안다: 열린 창의 폴더를
// windowsState.openedWindows 로 적어 두고, 창을 열고 닫을 때마다 갱신한다(실측:
// 닫은 창은 빠지고 열린 세 창만 남아 있었다). 그 파일을 읽는다.
//
// 이게 필요한 이유는 좌클릭이 **이미 열린 창만** 앞으로 내야 해서다. 붙은 터미널이
// 없다고 새 창을 띄우던 폴백은, 붙은 터미널 0개에 열린 창 3개인 날 아무 세션이나
// 눌러도 창을 하나씩 더 만들었다. 띄우는 것은 우클릭의 일이다.

// vscodeStorageGlob finds VS Code's own record of its windows. Globbed rather
// than derived from %APPDATA%: that would cost a Windows process per click,
// and there is one Windows user on this machine.
const vscodeStorageGlob = "/mnt/c/Users/*/AppData/Roaming/Code/User/globalStorage/storage.json"

// OpenVSCodeFolders is the WSL path of every folder VS Code currently has a
// window open on. Unreadable, missing or local-only answers empty — "do not
// know", which the caller reads as "do not raise anything". A variable so
// tests can say which windows are open without a Windows profile on disk.
var OpenVSCodeFolders = openVSCodeFolders

func openVSCodeFolders() []string {
	matches, _ := filepath.Glob(vscodeStorageGlob)
	var out []string
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		out = append(out, parseOpenedWindows(b)...)
	}
	return out
}

// parseOpenedWindows pulls the WSL folders out of storage.json. Pure for
// testing.
//
// Only remote WSL folders count: a window on a Windows path cannot be a tmux
// session's, and an entry without a folder is an empty or workspace-file
// window. The URI's authority is "wsl+<distro>" percent-encoded; the path
// follows it verbatim.
func parseOpenedWindows(b []byte) []string {
	var doc struct {
		WindowsState struct {
			OpenedWindows []struct {
				Folder string `json:"folder"`
			} `json:"openedWindows"`
		} `json:"windowsState"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil
	}
	var out []string
	for _, w := range doc.WindowsState.OpenedWindows {
		if dir := wslFolderFromURI(w.Folder); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}

// wslFolderFromURI turns "vscode-remote://wsl%2Bubuntu-24.04/home/u/p" into
// "/home/u/p". Anything that is not a WSL remote folder is "".
//
// Split by hand rather than with net/url: Go's parser refuses the
// percent-encoded "+" in the authority, which is exactly how VS Code writes
// the distro name.
func wslFolderFromURI(raw string) string {
	const scheme = "vscode-remote://"
	if !strings.HasPrefix(raw, scheme) {
		return ""
	}
	rest := raw[len(scheme):]
	authority, path, _ := strings.Cut(rest, "/")
	host, err := url.PathUnescape(authority)
	if err != nil || !strings.HasPrefix(strings.ToLower(host), "wsl+") {
		return ""
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	if p, err := url.PathUnescape(path); err == nil {
		path = p
	}
	return "/" + path
}

// VSCodeWindowFor is the folder of the open VS Code window that owns a
// session's directory, or "" when no window is open on it. Same ownership rule
// as VSCodeDirFor: exact or containing, never the home directory.
func VSCodeWindowFor(sessionDir string) string {
	return VSCodeDirFor(sessionDir, OpenVSCodeFolders())
}
