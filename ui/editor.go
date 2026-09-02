package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// editorCommand is the only editor mux looks for. Not a list and not
// configurable: the panel opens the folder you are switching to, and "which of
// my four editors" is a question the person who wants that can answer by putting
// `code` on their PATH.
const editorCommand = "code"

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
func findEditor() string {
	if p, err := exec.LookPath(editorCommand); err == nil {
		return p
	}
	for _, c := range editorCandidates() {
		if isExecutableFile(c) {
			return c
		}
	}
	return ""
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

// firstLine keeps an error to one line. It is drawn into a panel row that is
// about forty cells wide, and a stack trace there costs the whole log.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
