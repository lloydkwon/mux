package ui

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 네이티브 Wayland(GNOME)에서는 `code <폴더>` 가 이미 열린 창을 앞으로 내지 못한다.
// VS Code 는 그 창에 포커스를 요청하지만, 컴포저가 활성화 토큰 없는 요청을 버리고 —
// 클릭은 터미널 → tmux → 패널을 거쳐 오므로 토큰이 남아 있을 수가 없다. WSL 에서는
// Windows 가 그 요청을 들어줬기 때문에 이 기능이 원래 동작했다.
//
// 그래서 창은 GNOME Shell 안에서 올린다. dotfiles 의 window-focus@local 확장이 세션
// 버스에 VS Code 창 목록(List)과 활성화(Activate)를 내놓고, 여기는 그 목록에서 폴더의
// 창을 고른다. 고르는 규칙을 Go 에 둔 것은 테스트할 수 있어서다. 확장이 없거나(WSL,
// 다른 데스크톱) 맞는 창이 없으면 false 이고, 부르는 쪽은 `code <폴더>` 로 물러선다.

const (
	windowFocusPath  = "/io/github/lloydkwon/WindowFocus"
	windowFocusIface = "io.github.lloydkwon.WindowFocus"

	// windowFocusTimeout bounds each bus call. The shell answers in a few
	// milliseconds; a bus that does not answer at all is the case this is for,
	// and the click must still fall through to `code` promptly.
	windowFocusTimeout = time.Second

	// vscodeAppName is the last segment of every VS Code window title.
	vscodeAppName = "Visual Studio Code"
)

// focusResult is what asking the shell to raise a window came to. The three
// are kept apart because the callers must treat them differently: only
// "could not ask" may fall back to `code <folder>` on a left click. "No such
// window" is an answer — the record that said one was open is stale — and
// falling back there would launch a window, which a left click never does.
type focusResult int

const (
	// focusUnavailable: the extension could not be reached (WSL, another
	// desktop, not loaded yet). The caller falls back to `code`.
	focusUnavailable focusResult = iota
	// focusNoWindow: the shell answered, and no single VS Code window is on
	// that folder — none, or several sharing its name.
	focusNoWindow
	// focusRaised: the window came forward.
	focusRaised
)

// shellWindow is one VS Code window as the extension lists it.
type shellWindow struct {
	ID    uint64 `json:"id"`
	Title string `json:"title"`
}

// focusShellWindow raises the VS Code window open on dir through the GNOME
// Shell extension, and reports whether one came forward. It is what the real
// panel and TUI pass as their focus function; a watchModel built by hand has
// none, which keeps `go test ./ui` off the developer's desktop.
func focusShellWindow(dir string) focusResult {
	out, err := busctlCall("List")
	if err != nil {
		return focusUnavailable
	}
	id, ok := pickEditorWindow(parseWindowList(out), dir)
	if !ok {
		return focusNoWindow
	}
	out, err = busctlCall("Activate", "t", strconv.FormatUint(id, 10))
	if err != nil {
		return focusUnavailable
	}
	if !parseActivateReply(out) {
		// The window went between List and Activate.
		return focusNoWindow
	}
	return focusRaised
}

// busctlCall calls a method on the extension's object. busctl rather than
// gdbus because --json gives an answer that needs no GVariant text parser.
func busctlCall(method string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), windowFocusTimeout)
	defer cancel()
	argv := append([]string{"--user", "--json=short", "call",
		"org.gnome.Shell", windowFocusPath, windowFocusIface, method}, args...)
	return exec.CommandContext(ctx, "busctl", argv...).Output()
}

// parseWindowList reads List's reply: busctl's {"type":"s","data":["…"]}
// around the extension's own JSON array. Pure for testing; anything malformed
// is "no windows".
func parseWindowList(out []byte) []shellWindow {
	var reply struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Data) != 1 {
		return nil
	}
	var wins []shellWindow
	if err := json.Unmarshal([]byte(reply.Data[0]), &wins); err != nil {
		return nil
	}
	return wins
}

// parseActivateReply reads Activate's {"type":"b","data":[true]}. Pure for
// testing.
func parseActivateReply(out []byte) bool {
	var reply struct {
		Data []bool `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Data) != 1 {
		return false
	}
	return reply.Data[0]
}

// pickEditorWindow finds the one window whose workspace is dir. Pure for
// testing.
//
// VS Code's default title is `${dirty}${activeEditorShort} - ${rootName} -
// ${appName}`, and rootName is the folder's base name — the segment just
// before the app name. Only that segment is looked at: the others are the open
// file, and a file named like a project must not stand for it. A title with a
// profile name wedged in therefore matches nothing and falls back to `code`.
// Equality, never a substring — "mux" must not raise the "my-mux" window. A
// remote suffix like " [WSL: Ubuntu]" is not part of the name.
//
// The title carries only the base name, so two windows on same-named folders
// cannot be told apart. Then there is no answer: raising the wrong project
// would be worse than raising none.
func pickEditorWindow(wins []shellWindow, dir string) (uint64, bool) {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return 0, false
	}
	base := filepath.Base(dir)
	var found uint64
	n := 0
	for _, w := range wins {
		segs := titleSegments(w.Title)
		if len(segs) > 0 && segs[len(segs)-1] == base {
			found = w.ID
			n++
		}
	}
	if n != 1 {
		return 0, false
	}
	return found, true
}

// titleSegments splits a VS Code title into its segments, without the app name
// and with remote suffixes dropped. A title that does not end in the app name
// is not a VS Code window and has none.
func titleSegments(title string) []string {
	parts := strings.Split(title, " - ")
	if len(parts) < 2 || strings.TrimSpace(parts[len(parts)-1]) != vscodeAppName {
		return nil
	}
	segs := make([]string, 0, len(parts)-1)
	for _, p := range parts[:len(parts)-1] {
		p = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "●"))
		if i := strings.Index(p, " ["); i >= 0 && strings.HasSuffix(p, "]") {
			p = p[:i]
		}
		segs = append(segs, p)
	}
	return segs
}
