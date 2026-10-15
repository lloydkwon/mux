package tmux

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// 어떤 프로젝트가 VS Code 창으로 열려 있는지는 tmux 로는 알 수 없다 — 그 창의 통합
// 터미널이 세션에 붙어 있을 때만 보인다. VS Code 자신은 안다: 열린 창의 폴더를
// windowsState.openedWindows 로 적어 두고, 창을 열고 닫을 때마다 갱신한다(실측:
// 닫은 창은 빠지고 열린 세 창만 남아 있었다). 그 파일을 읽는다.
//
// 네이티브 리눅스의 VS Code 도 같은 파일을 ~/.config/Code 아래에 file:// 폴더로 쓴다.
// 다만 VS Code 를 끄면 다음에 되살릴 창 목록으로 그대로 남으므로, VS Code 프로세스가
// 떠 있을 때만 믿는다(vscodeRunning).
//
// 이게 필요한 이유는 좌클릭이 **이미 열린 창만** 앞으로 내야 해서다. 붙은 터미널이
// 없다고 새 창을 띄우던 폴백은, 붙은 터미널 0개에 열린 창 3개인 날 아무 세션이나
// 눌러도 창을 하나씩 더 만들었다. 띄우는 것은 우클릭의 일이다.

// wslVSCodeStorageGlob finds the Windows VS Code's record of its windows from
// inside WSL. Globbed rather than derived from %APPDATA%: that would cost a
// Windows process per click, and there is one Windows user on such a machine.
// A variable so tests can point it at nothing.
var wslVSCodeStorageGlob = "/mnt/c/Users/*/AppData/Roaming/Code/User/globalStorage/storage.json"

// nativeVSCodeStorage is the same record for a VS Code running on this Linux
// itself. A variable so tests can point it at a fixture.
var nativeVSCodeStorage = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config/Code/User/globalStorage/storage.json")
}

// vscodeRunning reports whether a native VS Code is running. Replaceable in
// tests.
//
// It is the guard on the native record and the reason it can be trusted at
// all: VS Code keeps openedWindows when it quits — that list is what it
// restores next time — so with VS Code closed the file still names windows,
// and believing it would make a left click launch VS Code. The main IPC socket
// is no help: it outlives a crash. The process table is the one signal that
// goes when VS Code does. A WSL machine cannot see Windows processes, which is
// why the WSL record goes without this check, as it always has.
var vscodeRunning = func() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if c := e.Name()[0]; c < '0' || c > '9' {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(comm)) == "code" {
			return true
		}
	}
	return false
}

// OpenVSCodeFolders is the path of every folder VS Code currently has a window
// open on, from the Windows VS Code (seen from WSL) and the native one alike.
// Unreadable, missing or foreign answers empty — "do not know", which the
// caller reads as "do not raise anything". A variable so tests can say which
// windows are open without a VS Code profile on disk.
var OpenVSCodeFolders = openVSCodeFolders

func openVSCodeFolders() []string {
	var out []string
	matches, _ := filepath.Glob(wslVSCodeStorageGlob)
	for _, p := range matches {
		if b, err := os.ReadFile(p); err == nil {
			out = append(out, parseOpenedWindows(b, false)...)
		}
	}
	if p := nativeVSCodeStorage(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			if dirs := parseOpenedWindows(b, true); len(dirs) > 0 && vscodeRunning() {
				out = append(out, dirs...)
			}
		}
	}
	return out
}

// parseOpenedWindows pulls the folders out of storage.json. Pure for testing.
//
// Which URIs count depends on whose record it is. Read from WSL, only remote
// WSL folders do: a file:// window there is on a Windows path and cannot be a
// tmux session's. Read natively (local), file:// is exactly how a window on
// this machine is written. An entry without a folder is an empty or
// workspace-file window either way.
func parseOpenedWindows(b []byte, local bool) []string {
	var doc struct {
		WindowsState struct {
			LastActiveWindow struct {
				Folder string `json:"folder"`
			} `json:"lastActiveWindow"`
			OpenedWindows []struct {
				Folder string `json:"folder"`
			} `json:"openedWindows"`
		} `json:"windowsState"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil
	}
	var out []string
	folderOf := wslFolderFromURI
	if local {
		folderOf = localFolderFromURI
	}
	for _, w := range doc.WindowsState.OpenedWindows {
		if dir := folderOf(w.Folder); dir != "" {
			out = append(out, dir)
		}
	}
	// Natively the last active window counts too: with a single window VS Code
	// may record it there alone (suspected in review, not confirmed against
	// VS Code's source). Safe only because the native record is trusted while
	// VS Code runs — the WSL record has no such guard and keeps to
	// openedWindows, as it always did.
	if local {
		if dir := folderOf(doc.WindowsState.LastActiveWindow.Folder); dir != "" && !slices.Contains(out, dir) {
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

// localFolderFromURI turns "file:///home/u/p" into "/home/u/p". Anything that
// is not a local Unix folder is "" — a Windows drive path ("/c%3A/…") included,
// so a stray Windows record read as local still names nothing.
func localFolderFromURI(raw string) string {
	const scheme = "file://"
	if !strings.HasPrefix(raw, scheme) {
		return ""
	}
	p, err := url.PathUnescape(raw[len(scheme):])
	if err != nil {
		return ""
	}
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return ""
	}
	return p
}

// VSCodeWindowFor is the folder of the open VS Code window that owns a
// session's directory, or "" when no window is open on it. Same ownership rule
// as VSCodeDirFor: exact or containing, never the home directory.
func VSCodeWindowFor(sessionDir string) string {
	return VSCodeDirFor(sessionDir, OpenVSCodeFolders())
}
