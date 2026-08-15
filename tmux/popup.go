package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	popupWidth     = "85%"
	popupHeight    = "80%"
	minTmuxVersion = 3.2
	DefaultBindKey = "m"

	// muxKeybindMarker tags lines we own so we can replace/remove them idempotently.
	muxKeybindMarker = "# mux popup keybinding"

	// ohMyTmuxSignature is the first line of gpakosz/.tmux's bundled .tmux.conf.
	// It opens a heredoc that lets the file double as a shell script when
	// processed via `cut -c3- | sh -s ...`.
	ohMyTmuxSignature = "# : << 'EOF'"

	// ohMyTmuxSentinel marks the end of user-editable territory in
	// .tmux.conf.local. oh-my-tmux explicitly warns against writing past it.
	ohMyTmuxSentinel = `# "$@"`
)

// OpenPopup opens mux inside a tmux display-popup overlay.
// Must be called from inside a tmux session.
// BootstrapGuardEnv marks a mux that must not bootstrap again.
//
// The popup runs mux, so without a guard the child would bootstrap in turn and
// open popups forever. Measured: $TMUX *is* set inside display-popup -E (it
// reads back as the server socket and a pane id), so checking that would work
// today — but the cost of being wrong is an unbounded loop, and an env var mux
// sets itself depends on nothing.
const BootstrapGuardEnv = "MUX_NO_BOOTSTRAP"

// AttachAndPopup attaches this terminal to tmux and opens mux as a popup on it.
//
// It exists because a popup has to be drawn on a client, and outside tmux there
// is none. Measured: with a server up and a client attached elsewhere,
// display-popup succeeds with $TMUX unset — and draws on *that* client, in a
// different terminal from the one you typed in. So attaching has to come first;
// then the popup lands where you are looking.
//
// attach-session with no -t takes tmux's own most-recently-used session. Which
// session to attach to is the question mux exists to answer, but asking it needs
// a client, so tmux's default gets us on screen and the popup that follows is
// where the answer is given.
//
// One exec of one tmux command list. attach-session replaces this process — the
// same reason AttachToSession uses syscall.Exec outside tmux — so a second call
// afterwards would never run.
func AttachAndPopup() error {
	version, err := getTmuxVersion()
	if err != nil {
		return fmt.Errorf("failed to detect tmux version: %w", err)
	}
	if version < minTmuxVersion {
		return fmt.Errorf("tmux %.1f+ required for popup (current: %.1f)", minTmuxVersion, version)
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}
	muxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find mux executable: %w", err)
	}

	argv := append([]string{"tmux"}, bootstrapArgs(muxPath)...)
	return syscall.Exec(tmuxPath, argv, os.Environ())
}

// bootstrapArgs builds the tmux command list. Split out because it is the part
// worth pinning: a semicolon in the wrong place attaches without ever opening
// the popup, and nothing would say so.
//
// The popup's command is one string, not an argv. tmux runs a shell-command
// through a shell, which is what makes the VAR=1 prefix take effect — verified
// on an isolated server rather than assumed.
func bootstrapArgs(muxPath string) []string {
	return []string{
		"attach-session", ";",
		"display-popup", "-E", "-w", popupWidth, "-h", popupHeight,
		BootstrapGuardEnv + "=1 exec " + shellQuote(muxPath),
	}
}

// shellQuote makes one shell word of a path. Paths with spaces are ordinary on
// macOS, and the popup's command is read by a shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func OpenPopup() error {
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("mux popup must be run inside a tmux session")
	}

	version, err := getTmuxVersion()
	if err != nil {
		return fmt.Errorf("failed to detect tmux version: %w", err)
	}
	if version < minTmuxVersion {
		return fmt.Errorf("tmux %.1f+ required for popup (current: %.1f)", minTmuxVersion, version)
	}

	muxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find mux executable: %w", err)
	}

	cmd := exec.Command("tmux", "display-popup",
		"-E",
		"-w", popupWidth,
		"-h", popupHeight,
		muxPath,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func findTmuxConf() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to find home directory: %w", err)
	}

	// Candidate paths in priority order:
	//   $XDG_CONFIG_HOME/tmux/tmux.conf → ~/.config/tmux/tmux.conf → ~/.tmux.conf
	var candidates []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "tmux", "tmux.conf"))
	}
	candidates = append(candidates,
		filepath.Join(home, ".config", "tmux", "tmux.conf"),
		filepath.Join(home, ".tmux.conf"),
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return filepath.Join(home, ".tmux.conf"), nil
}

// findTmuxConfLocal returns the path where oh-my-tmux user customizations live,
// derived from the matched tmux.conf path so XDG and home variants stay paired.
func findTmuxConfLocal(confPath string) string {
	dir := filepath.Dir(confPath)
	base := filepath.Base(confPath)
	switch base {
	case "tmux.conf":
		return filepath.Join(dir, "tmux.conf.local")
	case ".tmux.conf":
		return filepath.Join(dir, ".tmux.conf.local")
	default:
		return confPath + ".local"
	}
}

// isOhMyTmux detects gpakosz/.tmux installs using a hybrid of two signals:
//
//   - Strategy A (symlink): confPath is a symlink whose target lives under a
//     `.tmux/` directory — matches the upstream installer's layout.
//   - Strategy B (signature): confPath's first line is `# : << 'EOF'` — the
//     heredoc opener oh-my-tmux uses to make the conf file double as a shell
//     script. Catches users who copied files instead of symlinking.
//
// Either signal alone is sufficient.
func isOhMyTmux(confPath string) bool {
	if info, err := os.Lstat(confPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(confPath); err == nil {
			// Resolve relative symlinks against the symlink's directory.
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(confPath), target)
			}
			if strings.Contains(filepath.ToSlash(target), "/.tmux/") {
				return true
			}
		}
	}

	f, err := os.Open(confPath)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len(ohMyTmuxSignature))
	n, _ := f.Read(buf)
	return strings.TrimSpace(string(buf[:n])) == ohMyTmuxSignature
}

// SetupKeybind adds a popup keybinding to the user's tmux config file.
//
// For oh-my-tmux installs the bind line is routed to .tmux.conf.local and
// inserted before the `# "$@"` sentinel (oh-my-tmux marks everything below
// that line as off-limits). Any prior corrupt entry left in .tmux.conf by
// older mux versions is removed in the same pass.
func SetupKeybind(key string) error {
	muxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find mux executable: %w", err)
	}

	confPath, err := findTmuxConf()
	if err != nil {
		return err
	}
	bindLine := fmt.Sprintf(`bind %s display-popup -E -w %s -h %s "%s"`, key, popupWidth, popupHeight, muxPath)

	if isOhMyTmux(confPath) {
		localPath := findTmuxConfLocal(confPath)
		if err := writeBindToLocal(localPath, bindLine); err != nil {
			return err
		}
		// Best-effort cleanup of any corrupt line older mux versions may have
		// appended to the main conf. Ignore failures — we already succeeded
		// on the file that matters.
		removed, _ := stripMarkerLines(confPath)

		fmt.Printf("Detected oh-my-tmux. Added to %s:\n  %s\n\n", localPath, bindLine)
		if removed {
			fmt.Printf("Removed prior mux entry from %s (was breaking oh-my-tmux's heredoc).\n\n", confPath)
		}
		fmt.Printf("Reload tmux config:\n  tmux source-file %s\n\n", localPath)
		fmt.Printf("Then press: prefix + %s (default prefix: Ctrl+b)\n", key)
		return nil
	}

	if err := upsertBindLine(confPath, bindLine, true); err != nil {
		return err
	}
	fmt.Printf("Added to %s:\n  %s\n\n", confPath, bindLine)
	fmt.Printf("Reload tmux config:\n  tmux source-file %s\n\n", confPath)
	fmt.Printf("Then press: prefix + %s (default prefix: Ctrl+b)\n", key)
	return nil
}

// upsertBindLine writes bindLine into path. If a line tagged with
// muxKeybindMarker already exists it is replaced in place; otherwise the line
// is appended (createIfMissing controls whether a missing file is allowed).
func upsertBindLine(path, bindLine string, createIfMissing bool) error {
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
		if !createIfMissing {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}
	}

	tagged := bindLine + "  " + muxKeybindMarker
	lines := strings.Split(string(content), "\n")
	replaced := false
	for i, line := range lines {
		if strings.Contains(line, muxKeybindMarker) {
			lines[i] = tagged
			replaced = true
		}
	}
	if !replaced {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, tagged)
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// writeBindToLocal upserts bindLine into an oh-my-tmux .tmux.conf.local,
// inserting before the `# "$@"` sentinel. Falls back to append if the sentinel
// is missing (user may have stripped it). Creates the file if absent.
func writeBindToLocal(path, bindLine string) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	tagged := bindLine + "  " + muxKeybindMarker
	lines := strings.Split(string(content), "\n")

	// Replace existing marker line in place.
	for i, line := range lines {
		if strings.Contains(line, muxKeybindMarker) {
			lines[i] = tagged
			result := strings.Join(lines, "\n")
			if !strings.HasSuffix(result, "\n") {
				result += "\n"
			}
			return os.WriteFile(path, []byte(result), 0644)
		}
	}

	// Insert before the sentinel if present.
	sentinelIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == ohMyTmuxSentinel {
			sentinelIdx = i
			break
		}
	}

	if sentinelIdx >= 0 {
		insertion := []string{"", tagged, ""}
		newLines := make([]string, 0, len(lines)+len(insertion))
		newLines = append(newLines, lines[:sentinelIdx]...)
		newLines = append(newLines, insertion...)
		newLines = append(newLines, lines[sentinelIdx:]...)
		lines = newLines
	} else {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, tagged)
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// legacyInstallerBindFragment matches the exact bind line older install.sh
// fallbacks appended without a marker comment. Pre-fix install.sh wrote:
//
//	bind-key m display-popup -E -w 80% -h 80% "mux"
//
// We strip lines containing this fragment so users corrupted via the installer
// (not via `mux setup-keybind`) get cleaned up too. Substring match is narrow
// enough to avoid eating user-authored bindings that wrap mux differently.
const legacyInstallerBindFragment = `display-popup -E -w 80% -h 80% "mux"`

// stripMarkerLines removes any mux-owned line from path: lines tagged with
// muxKeybindMarker AND legacy untagged installer binds. Returns whether any
// line was removed. Missing file returns (false, nil).
func stripMarkerLines(path string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lines := strings.Split(string(content), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if strings.Contains(line, muxKeybindMarker) ||
			strings.Contains(line, legacyInstallerBindFragment) {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	result := strings.Join(kept, "\n")
	if !strings.HasSuffix(result, "\n") && len(result) > 0 {
		result += "\n"
	}
	return true, os.WriteFile(path, []byte(result), 0644)
}

func getTmuxVersion() (float64, error) {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return 0, err
	}
	// Output: "tmux 3.4" or "tmux 3.2a"
	s := strings.TrimSpace(string(out))
	s = strings.TrimPrefix(s, "tmux ")
	// Strip trailing letter (e.g. "3.2a" -> "3.2")
	var version float64
	fmt.Sscanf(s, "%f", &version)
	return version, nil
}
