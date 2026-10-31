package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Session save/restore — tmux-resurrect plus tmux-continuum — as the third
// mux-owned region of the tmux config.
//
// A tmux server holds its sessions in memory and nowhere else, so anything that
// ends the server ends every session with it. On WSL that is every Windows
// restart, and what came back was one empty server and a list with nothing the
// user had been working in. The two plugins are the standard answer; this
// region installs them with the one option mux depends on already set.
//
// See docs/design/resurrect.md.

const (
	// resurrectBlockBegin and resurrectBlockEnd fence the region, for the same
	// reasons the panel's fence exists (see panelBlockBegin) — and as a separate
	// fence rather than more lines inside the panel's, because the panel block is
	// regenerated wholesale by the repair path and by setup-panel, and neither
	// should be what decides whether someone's sessions are saved.
	resurrectBlockBegin = "# mux resurrect {"
	resurrectBlockEnd   = "# mux resurrect }"

	// captureContentsLine is the one line mux cannot do without.
	//
	// resurrect brings the panel back as a bare shell, and findGhostPane
	// recognises that shell by the start command restore gives it —
	// `cat …/resurrect/restore/pane_contents/…; exec $SHELL` — together with the
	// panel header at the top of the painted-back screen. Both exist only when
	// pane contents are saved. Without this, the shell's prompt overwrites the
	// panel's title too, nothing marks the pane, and every restored window gets
	// a second panel beside the dead one. Reproduced on an isolated server:
	// three columns per window with it off, two with it on.
	captureContentsLine = "set -g @resurrect-capture-pane-contents 'on'"
)

// resurrectPlugin is one plugin the region loads, pinned to a revision.
//
// Pinned because the region runs the plugin's code on every config load, and
// "whatever the default branch says today" is not something to clone onto
// someone's machine unasked. A plugin directory that already exists is left
// exactly as it is — it is the user's, however it got there.
type resurrectPlugin struct {
	name  string // directory under the plugin dir, and the repo name
	entry string // the file tmux runs to load it
	rev   string
}

var resurrectPlugins = []resurrectPlugin{
	{name: "tmux-resurrect", entry: "resurrect.tmux", rev: "cff343cf9e81983d3da0c8562b01616f12e8d548"},
	// Continuum second: it reads resurrect's save/restore script paths, which
	// resurrect publishes as options when it loads.
	{name: "tmux-continuum", entry: "continuum.tmux", rev: "0698e8f4b17d6454c71bf5212895ec055c578da0"},
}

func (p resurrectPlugin) url() string { return "https://github.com/tmux-plugins/" + p.name }

// resurrectPluginDir is where the plugins live: tpm's own default, so a later
// switch to tpm finds them where it expects. That is $XDG_CONFIG_HOME/tmux/plugins
// for a config at …/tmux/tmux.conf and ~/.tmux/plugins otherwise.
func resurrectPluginDir(confPath string) (string, error) {
	if filepath.Base(confPath) == "tmux.conf" {
		return filepath.Join(filepath.Dir(confPath), "plugins"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to find home directory: %w", err)
	}
	return filepath.Join(home, ".tmux", "plugins"), nil
}

// resurrectBlockLines renders the region, fences included.
//
// With loadPlugins false it is the one option and nothing else: the user
// already loads resurrect themselves (tpm, a hand-written run-shell), and a
// second load would double continuum's autosave and override whatever they
// chose for restore. The option is still written because the panel's cleanup
// depends on it, not because of anyone's taste.
//
// The run-shell argument is double-quoted for tmux and the path inside it
// single-quoted for /bin/sh — the two-parser rule panelBlockLines explains.
func resurrectBlockLines(pluginDir string, loadPlugins bool) []string {
	lines := []string{resurrectBlockBegin, captureContentsLine}
	if loadPlugins {
		// Restore on server start is the point of installing continuum at all:
		// the server that died is the one being replaced.
		lines = append(lines, "set -g @continuum-restore 'on'")
		for _, p := range resurrectPlugins {
			lines = append(lines, fmt.Sprintf(`run-shell "%s"`,
				shellQuote(filepath.Join(pluginDir, p.name, p.entry))))
		}
	}
	return append(lines, resurrectBlockEnd)
}

// userLoadsResurrect reports whether the config already loads tmux-resurrect
// outside mux's own region — a tpm `@plugin` line or a run-shell of its entry
// point. Comments do not count: a commented-out plugin is one the user turned
// off, and that is not a reason to skip installing it here.
func userLoadsResurrect(contents ...string) bool {
	for _, content := range contents {
		lines := strings.Split(content, "\n")
		begin, end, fenced := findFenced(lines, resurrectBlockBegin, resurrectBlockEnd)
		for i, line := range lines {
			if fenced && i >= begin && i <= end {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(trimmed, "tmux-resurrect") {
				return true
			}
		}
	}
	return false
}

// ensureResurrectPlugins clones whichever plugin is missing, at its pinned
// revision. A directory that exists is never touched; one that exists without
// the plugin's entry file is reported rather than overwritten, since it is not
// ours to clear.
func ensureResurrectPlugins(pluginDir string) error {
	for _, p := range resurrectPlugins {
		dest := filepath.Join(pluginDir, p.name)
		entry := filepath.Join(dest, p.entry)
		if fileExists(entry) {
			continue
		}
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s exists but has no %s", dest, p.entry)
		}
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", pluginDir, err)
		}
		if err := runner.Run("git", "clone", "--quiet", p.url(), dest); err != nil {
			return fmt.Errorf("clone %s: %w", p.name, err)
		}
		if err := runner.Run("git", "-C", dest, "checkout", "--quiet", p.rev); err != nil {
			return fmt.Errorf("check out %s at %s: %w", p.name, p.rev[:7], err)
		}
		if !fileExists(entry) {
			return fmt.Errorf("%s was cloned but has no %s", dest, p.entry)
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ErrResurrectUnavailable wraps every reason the region could not be written,
// so a caller that installs it alongside the other two can say "the rest
// worked" rather than failing the whole setup on a missing git.
var ErrResurrectUnavailable = errors.New("session save/restore not set up")

// installResurrect writes the region into target and returns its lines, for
// the caller to apply to the running server. Nothing is written unless the
// plugins it loads are on disk: a run-shell of a missing file is an error on
// the status line at every config load, which is worse than no region.
func installResurrect(confPath, target string) ([]string, bool, error) {
	var contents []string
	for _, p := range []string{confPath, target} {
		if b, err := os.ReadFile(p); err == nil {
			contents = append(contents, string(b))
		}
	}
	loadPlugins := !userLoadsResurrect(contents...)

	pluginDir, err := resurrectPluginDir(confPath)
	if err != nil {
		return nil, loadPlugins, fmt.Errorf("%w: %v", ErrResurrectUnavailable, err)
	}
	if loadPlugins {
		if err := ensureResurrectPlugins(pluginDir); err != nil {
			return nil, loadPlugins, fmt.Errorf("%w: %v", ErrResurrectUnavailable, err)
		}
	}

	block := resurrectBlockLines(pluginDir, loadPlugins)
	if err := upsertFenced(target, resurrectBlockBegin, resurrectBlockEnd, block); err != nil {
		return nil, loadPlugins, err
	}
	return block, loadPlugins, nil
}

// SetupResurrect is the command-line path: write the region, apply it to the
// running server, and say what happened.
func SetupResurrect() error {
	confPath, err := findTmuxConf()
	if err != nil {
		return err
	}
	target := confPath
	if isOhMyTmux(confPath) {
		target = findTmuxConfLocal(confPath)
	}

	block, loadPlugins, err := installResurrect(confPath, target)
	if err != nil {
		return err
	}
	_ = applyToServer(block)

	fmt.Printf("Added the session save/restore block to %s\n\n", target)
	if !loadPlugins {
		fmt.Printf("tmux-resurrect is already loaded by your config, so the block only turns on\n")
		fmt.Printf("pane-contents capture — the mux panel needs it to clean up after a restore.\n")
		return nil
	}
	fmt.Printf("Sessions are saved every 15 minutes and restored when the tmux server starts.\n")
	fmt.Printf("By hand: prefix + Ctrl-s saves, prefix + Ctrl-r restores.\n")
	return nil
}
