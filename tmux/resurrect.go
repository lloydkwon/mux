package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
// With loadPlugins false the plugins are not loaded and restore is not turned
// on: the user already loads resurrect themselves (tpm, a hand-written
// run-shell), and a second load would double continuum's autosave and override
// whatever they chose for restore. The option is still written because the
// panel's cleanup depends on it, not because of anyone's taste.
//
// hooks adds the two restore hooks that run `mux resurrect-hook`. They are left
// out only when the user's config sets those hooks itself — one option holds
// one command, and theirs is not ours to replace.
//
// The run-shell argument is double-quoted for tmux and the path inside it
// single-quoted for /bin/sh — the two-parser rule panelBlockLines explains.
// The hook values follow the same rule: resurrect evals them in bash.
func resurrectBlockLines(muxPath, pluginDir string, loadPlugins, hooks bool) []string {
	lines := []string{resurrectBlockBegin, captureContentsLine}
	if hooks {
		quoted := shellQuote(muxPath)
		lines = append(lines,
			fmt.Sprintf(`set -g %s "%s resurrect-hook pre"`, preRestoreHookOption, quoted),
			fmt.Sprintf(`set -g %s "%s resurrect-hook post"`, postRestoreHookOption, quoted))
	}
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

// userSetsRestoreHooks reports whether the config sets either restore hook
// outside mux's own region.
func userSetsRestoreHooks(contents ...string) bool {
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
			if strings.Contains(trimmed, preRestoreHookOption) || strings.Contains(trimmed, postRestoreHookOption) {
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
	muxPath, err := os.Executable()
	if err != nil {
		return nil, false, fmt.Errorf("%w: locate mux binary: %v", ErrResurrectUnavailable, err)
	}
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

	block := resurrectBlockLines(muxPath, pluginDir, loadPlugins, !userSetsRestoreHooks(contents...))
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

// ── After a restore ─────────────────────────────────────────────────────────
//
// Two things go wrong right after resurrect restores, and both put the user's
// first keystrokes in the wrong place.
//
// The active pane is not restored. resurrect picks it with
// `switch-client -t sess:win` and then `select-pane -t <index>` — a bare index,
// resolved against the client's current window. continuum restores when the
// server starts, before anyone is attached, so switch-client fails and the
// index lands in some other window. What stays active is whatever was split
// last, and the panel is always split last: the restored dead panel was the
// active pane of every window.
//
// And the dead panel is only cleaned up when a panel hook fires for its window
// (findGhostPane), which spares a pane with child processes — a shell somebody
// adopted. With the dead panel active, the first thing the user typed went into
// it; after a reboot that was `claude --continue`, and from then on the pane had
// a child and was never cleaned up. Three columns: an empty restored shell, the
// conversation squeezed into the panel's width, and the new panel.
//
// So mux takes both jobs at the moment restore finishes, through resurrect's own
// hooks: re-select every saved active pane by its full address, then close the
// panes this restore created where the panel used to be. Nobody can have
// adopted a pane that did not exist a moment ago, which is what lets this skip
// the child-process check findGhostPane needs — the pre hook records the
// highest pane id before restore starts, and only panes above it are touched.
//
// Both hooks are the hook path: they never report a failure (see
// cmd/mux runResurrectHook), and every step stands down on anything unexpected.

const (
	preRestoreHookOption  = "@resurrect-hook-pre-restore-all"
	postRestoreHookOption = "@resurrect-hook-post-restore-all"

	// restoreFloorOption carries the highest pane id from the pre hook to the
	// post hook. A tmux option rather than a file: it lives exactly as long as
	// the server the restore runs in.
	restoreFloorOption = "@mux_restore_floor"

	// restoredOption is set by the post hook when it finishes — the one signal
	// RestoreAtStartup can wait for, whoever ran the restore.
	restoredOption = "@mux_restored"
)

// ResurrectPreRestore records the highest pane id that exists before restore
// creates any.
func ResurrectPreRestore() error {
	out, err := runner.Output("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	return runner.Run("tmux", "set-option", "-g", restoreFloorOption, strconv.Itoa(highestPaneID(string(out))))
}

// highestPaneID is the largest %N in a list-panes output, or -1 for none.
func highestPaneID(out string) int {
	highest := -1
	for _, line := range strings.Split(out, "\n") {
		if n, ok := paneNumber(strings.TrimSpace(line)); ok && n > highest {
			highest = n
		}
	}
	return highest
}

func paneNumber(id string) (int, bool) {
	if !strings.HasPrefix(id, "%") {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	return n, err == nil
}

// ResurrectPostRestore re-selects the saved active panes and closes the dead
// panels this restore created. Without a floor from the pre hook it does
// nothing at all: it cannot then tell a restored pane from one the user had.
func ResurrectPostRestore() error {
	// Whatever happens below, the restore itself is over — say so for
	// RestoreAtStartup, which is waiting on exactly this.
	defer func() { _ = runner.Run("tmux", "set-option", "-g", restoredOption, "1") }()

	out, err := runner.Output("tmux", "show-options", "-gqv", restoreFloorOption)
	_ = runner.Run("tmux", "set-option", "-gu", restoreFloorOption)
	if err != nil {
		return err
	}
	floor, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return nil // no pre hook ran — stand down
	}

	dir, err := resurrectDir()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(dir, "last"))
	if err != nil {
		return err
	}
	plan := parseRestorePlan(string(content))

	// Active panes first. Closing a dead panel never moves the active pane of
	// its window unless it *is* the active pane, and then tmux hands the focus
	// to a neighbour — fine. The other order would shift the indices the save
	// file speaks in for any pane to the right of a closed one.
	for _, slot := range plan.active {
		_ = runner.Run("tmux", "select-pane", "-t", slot.target())
	}
	for _, slot := range plan.panels {
		out, err := runner.Output("tmux", "display-message", "-p", "-t", slot.target(),
			"#{pane_id}\t#{window_panes}\t#{pane_start_command}")
		if err != nil {
			continue
		}
		if id, ok := restoredDeadPanel(strings.TrimRight(string(out), "\n"), floor); ok {
			_ = runner.Run("tmux", "kill-pane", "-t", id)
		}
	}
	return nil
}

// restoreSlot is a pane position as the save file names it.
type restoreSlot struct{ session, window, pane string }

// target is the full address: `=` for an exact session match, and an explicit
// window — a bare pane index is what resurrect itself gets wrong.
func (s restoreSlot) target() string { return "=" + s.session + ":" + s.window + "." + s.pane }

type restorePlan struct {
	active []restoreSlot // the active pane of each saved window
	panels []restoreSlot // where a live mux panel was when the save was taken
}

// parseRestorePlan reads a resurrect save file. Pane lines are tab-separated:
//
//	pane  session  window  window_active  :flags  pane_index  title  :dir  pane_active  command  :full_command
//
// A panel is recognised by its title and its command together: the title alone
// is what the user's own shell can inherit after an earlier restore, and the
// command alone is any mux, the TUI included.
func parseRestorePlan(content string) restorePlan {
	var plan restorePlan
	for _, line := range strings.Split(content, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 10 || f[0] != "pane" {
			continue
		}
		slot := restoreSlot{session: f[1], window: f[2], pane: f[5]}
		if f[8] == "1" {
			plan.active = append(plan.active, slot)
		}
		if f[6] == panelTitle && f[9] == "mux" {
			plan.panels = append(plan.panels, slot)
		}
	}
	return plan
}

// restoredDeadPanel decides, from `#{pane_id}\t#{window_panes}\t#{pane_start_command}`,
// whether the pane at a saved panel slot is one this restore just created in
// the panel's place. Pure for testing.
//
// Three conditions, each a reason this is not someone's pane: its id is above
// the floor (it did not exist before this restore), its start command is
// resurrect's restore wrapper (it was not the live panel or anything else
// started since), and it is not alone — kill-pane on a window's only pane is
// kill-session, the rule panel.md is built on.
func restoredDeadPanel(info string, floor int) (string, bool) {
	f := strings.SplitN(info, "\t", 3)
	if len(f) < 3 {
		return "", false
	}
	n, ok := paneNumber(f[0])
	if !ok || n <= floor {
		return "", false
	}
	if panes, err := strconv.Atoi(f[1]); err != nil || panes < 2 {
		return "", false
	}
	if !strings.Contains(f[2], resurrectRestoreMark) {
		return "", false
	}
	return f[0], true
}

// resurrectDir resolves the save directory the way resurrect does: the
// @resurrect-dir option with ~, $HOME and $HOSTNAME expanded, else
// ~/.tmux/resurrect if it exists, else $XDG_DATA_HOME/tmux/resurrect.
func resurrectDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if out, err := runner.Output("tmux", "show-options", "-gqv", "@resurrect-dir"); err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			host, _ := os.Hostname()
			dir = strings.ReplaceAll(dir, "$HOME", home)
			dir = strings.ReplaceAll(dir, "$HOSTNAME", host)
			return strings.ReplaceAll(dir, "~", home), nil
		}
	}
	legacy := filepath.Join(home, ".tmux", "resurrect")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy, nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "tmux", "resurrect"), nil
}

// ── Restoring when mux starts ───────────────────────────────────────────────
//
// continuum restores when a tmux server starts, and after a reboot nothing
// starts one until the user does something in tmux. A bare `mux` at that point
// finds no server and draws an empty list. The first thing anyone does from an
// empty list is create a session — and that `new-session` is what starts the
// server, which loads the config, which restores. The restore then brings back
// a session of the very name just typed: the create fails as a duplicate and
// the old sessions appear behind it. That is the report this exists for.
//
// So mux starts the server itself, before drawing, and waits for the restore to
// finish. See docs/design/resurrect.md.

// restoreSeedSession is the session that exists only to start the server. It
// is closed once the restore is done. resurrect has its own version of this —
// it closes a session named "0" when it restores "from scratch" — but that only
// counts a server holding a single pane, and the panel hook puts a second one
// in every new session, so it never applies here.
//
// The name is a base, not the name: see seedSessionName.
const restoreSeedSession = "mux-restoring"

// seedSessionName picks a seed name that no saved session name is a prefix of.
//
// resurrect asks `has-session -t <name>` without `=`, and tmux falls back to a
// prefix match when there is no exact one. With a seed called mux-restoring, a
// saved session called mux "existed" — resurrect restored its windows into the
// seed, and closing the seed took them away. Reproduced on an isolated server:
// three sessions saved, two came back.
//
// Only a saved name that the seed *starts with* can clash, so the fix is the
// first character: the base name first, then the base behind each character of
// seedLeads in turn. Growing a prefix of one character instead (`_`, `__`, …)
// loops forever on a saved session called `_`.
func seedSessionName(saved []string) string {
	clashes := func(seed string) bool {
		for _, name := range saved {
			if name != "" && strings.HasPrefix(seed, name) {
				return true
			}
		}
		return false
	}
	if !clashes(restoreSeedSession) {
		return restoreSeedSession
	}
	for _, lead := range seedLeads {
		if seed := string(lead) + "-" + restoreSeedSession; !clashes(seed) {
			return seed
		}
	}
	// Every lead taken by a one-character session name: not a real server.
	return "~" + restoreSeedSession
}

// seedLeads are first characters tmux keeps as they are in a session name —
// not `.` or `:`, which it rewrites, and none of the target sigils `$@%=`.
const seedLeads = "_zyxwvutsrqponlkjihgfedcba9876543210"

// savedSessionNames lists the sessions a resurrect save file holds.
func savedSessionNames(content string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(content, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || f[0] != "pane" || seen[f[1]] {
			continue
		}
		seen[f[1]] = true
		names = append(names, f[1])
	}
	return names
}

// Timing for RestoreAtStartup. Variables so tests do not sleep.
var (
	restorePoll    = 200 * time.Millisecond
	restoreGrace   = 5 * time.Second  // how long continuum gets to start before mux runs the restore itself
	restoreTimeout = 30 * time.Second // the whole wait; a restore is a few seconds per dozen panes
	restoreSleep   = time.Sleep
)

// RestoreAtStartup brings saved sessions back when no tmux server is running,
// and reports whether it started one. say narrates, since this can take
// seconds and an unexplained pause looks like a hang.
//
// It acts only when the config carries mux's resurrect region with its hooks:
// the post hook is how it knows the restore is over, and without the region it
// has no business starting a server. Every other path stands down silently —
// the list opens exactly as it did before this existed.
//
// It also needs a save file to restore from, read before any server exists.
//
// Who restores: continuum, normally — it sees a server that just started. It
// stands down when another tmux server is running (a `-L` test server is
// enough), so if no restore has begun after restoreGrace, mux runs resurrect's
// restore script itself. The pre hook's floor is the sign one has begun; the
// two never both run, because mux only steps in when that sign is absent.
func RestoreAtStartup(say func(string)) bool {
	if runner.Run("tmux", "has-session") == nil {
		return false // a server with sessions: nothing to restore into
	}
	if !restoreHooksInstalled() {
		return false
	}
	// No server yet, so @resurrect-dir cannot be read and resurrectDir falls
	// back to resurrect's defaults — which is where mux's region saves.
	dir, err := resurrectDir()
	if err != nil {
		return false
	}
	content, err := os.ReadFile(filepath.Join(dir, "last"))
	if err != nil {
		return false // nothing saved: starting a server would only create the seed
	}
	seed := seedSessionName(savedSessionNames(string(content)))
	if err := runner.Run("tmux", "new-session", "-d", "-s", seed); err != nil {
		return false
	}
	say("Restoring saved tmux sessions…")

	ranItOurselves := false
	for waited := time.Duration(0); waited < restoreTimeout; waited += restorePoll {
		if globalOption(restoredOption) != "" {
			break
		}
		if !ranItOurselves && waited >= restoreGrace && globalOption(restoreFloorOption) == "" {
			script := globalOption("@resurrect-restore-script-path")
			if script == "" {
				break // resurrect is not loaded: nothing will ever restore
			}
			ranItOurselves = true
			// run-shell waits for the script, and the hooks run inside it.
			_ = runner.Run("tmux", "run-shell", shellQuote(script))
			continue
		}
		restoreSleep(restorePoll)
	}

	_ = runner.Run("tmux", "set-option", "-gu", restoredOption)
	// Closing the seed when nothing was restored leaves no sessions, and the
	// server exits with it — the same state mux found.
	_ = runner.Run("tmux", "kill-session", "-t", "="+seed)
	return true
}

// restoreHooksInstalled reports whether the tmux config carries mux's resurrect
// region with the post-restore hook in it.
func restoreHooksInstalled() bool {
	confPath, err := findTmuxConf()
	if err != nil {
		return false
	}
	target := confPath
	if isOhMyTmux(confPath) {
		target = findTmuxConfLocal(confPath)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	lines := strings.Split(string(content), "\n")
	begin, end, ok := findFenced(lines, resurrectBlockBegin, resurrectBlockEnd)
	if !ok {
		return false
	}
	for _, line := range lines[begin : end+1] {
		if strings.Contains(line, postRestoreHookOption) && strings.Contains(line, "resurrect-hook post") {
			return true
		}
	}
	return false
}
