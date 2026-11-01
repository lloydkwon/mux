package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakePlugins puts both plugins' entry files where installResurrect looks, so
// nothing is cloned — the mock runner would accept the clone and leave no file.
func fakePlugins(t *testing.T, pluginDir string) {
	t.Helper()
	for _, p := range resurrectPlugins {
		dir := filepath.Join(pluginDir, p.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p.entry), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TMUX", "") // no server to apply to; the files are the test
	return home
}

// The option is the point: without it the panel's ghost cleanup cannot
// recognise a restored panel, whichever way resurrect is loaded.
func TestResurrectBlockAlwaysCapturesPaneContents(t *testing.T) {
	for _, load := range []bool{true, false} {
		block := resurrectBlockLines("/bin/mux", "/p", load, true)
		if !strings.Contains(strings.Join(block, "\n"), captureContentsLine) {
			t.Errorf("loadPlugins=%v: block lacks %q:\n%s", load, captureContentsLine, strings.Join(block, "\n"))
		}
		if block[0] != resurrectBlockBegin || block[len(block)-1] != resurrectBlockEnd {
			t.Errorf("loadPlugins=%v: block is not fenced:\n%s", load, strings.Join(block, "\n"))
		}
	}
}

// Someone who already loads resurrect gets the option and nothing else: a
// second load doubles continuum's autosave and overrides their restore choice.
func TestResurrectBlockOptionOnlyWhenUserLoadsIt(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/bin/mux", "/p", false, true), "\n")
	if strings.Contains(joined, "run-shell") || strings.Contains(joined, "@continuum-restore") {
		t.Errorf("option-only block still loads plugins or sets restore:\n%s", joined)
	}
}

// Continuum reads the script paths resurrect publishes when it loads.
func TestResurrectBlockLoadsResurrectBeforeContinuum(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/bin/mux", "/p", true, true), "\n")
	r := strings.Index(joined, "tmux-resurrect/resurrect.tmux")
	c := strings.Index(joined, "tmux-continuum/continuum.tmux")
	if r < 0 || c < 0 || r > c {
		t.Errorf("want resurrect loaded before continuum:\n%s", joined)
	}
}

// The two-parser rule: double quotes for tmux, single quotes for /bin/sh.
func TestResurrectBlockQuotesAPathWithSpaces(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/bin/mux", "/my plugins", true, true), "\n")
	want := `run-shell "'/my plugins/tmux-resurrect/resurrect.tmux'"`
	if !strings.Contains(joined, want) {
		t.Errorf("want %s in:\n%s", want, joined)
	}
}

func TestUserLoadsResurrect(t *testing.T) {
	ours := strings.Join(resurrectBlockLines("/bin/mux", "/p", true, true), "\n")
	cases := []struct {
		name, conf string
		want       bool
	}{
		{"tpm plugin line", "set -g @plugin 'tmux-plugins/tmux-resurrect'\n", true},
		{"hand-written run-shell", "run-shell ~/x/tmux-resurrect/resurrect.tmux\n", true},
		{"commented out", "# set -g @plugin 'tmux-plugins/tmux-resurrect'\n", false},
		{"only mux's own region", "set -g mouse on\n" + ours + "\n", false},
		{"nothing", "set -g mouse on\n", false},
	}
	for _, c := range cases {
		if got := userLoadsResurrect(c.conf); got != c.want {
			t.Errorf("%s: userLoadsResurrect = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResurrectPluginDirFollowsTheConfig(t *testing.T) {
	home := isolatedHome(t)
	got, _ := resurrectPluginDir(filepath.Join(home, ".tmux.conf"))
	if want := filepath.Join(home, ".tmux", "plugins"); got != want {
		t.Errorf("~/.tmux.conf: plugin dir = %s, want %s", got, want)
	}
	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	got, _ = resurrectPluginDir(xdg)
	if want := filepath.Join(home, ".config", "tmux", "plugins"); got != want {
		t.Errorf("XDG config: plugin dir = %s, want %s", got, want)
	}
}

// A missing plugin is cloned and checked out at the pinned revision — never
// whatever the default branch says that day.
func TestEnsureResurrectPluginsClonesAtThePinnedRevision(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		dir := t.TempDir()
		err := ensureResurrectPlugins(dir)
		// The mock clone writes nothing, so the post-clone check must notice.
		if err == nil || !strings.Contains(err.Error(), "was cloned but has no") {
			t.Fatalf("want the missing entry file reported, got %v", err)
		}
		p := resurrectPlugins[0]
		dest := filepath.Join(dir, p.name)
		want := []string{
			"git clone --quiet " + p.url() + " " + dest,
			"git -C " + dest + " checkout --quiet " + p.rev,
		}
		if len(m.runs) != 2 || m.runs[0] != want[0] || m.runs[1] != want[1] {
			t.Errorf("runs = %q\nwant %q", m.runs, want)
		}
	})
}

// A plugin that is already there is the user's, however it got there.
func TestEnsureResurrectPluginsLeavesExistingPluginsAlone(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		dir := t.TempDir()
		fakePlugins(t, dir)
		if err := ensureResurrectPlugins(dir); err != nil {
			t.Fatal(err)
		}
		if len(m.runs) != 0 {
			t.Errorf("touched existing plugins: %q", m.runs)
		}
	})
}

// A directory without the entry file is not ours to clear.
func TestEnsureResurrectPluginsRefusesAForeignDirectory(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "tmux-resurrect"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ensureResurrectPlugins(dir); err == nil {
			t.Error("a directory without the plugin's entry file was accepted")
		}
		if len(m.runs) != 0 {
			t.Errorf("ran commands over a foreign directory: %q", m.runs)
		}
	})
}

// Writing a run-shell of a file that is not there puts an error on the status
// line at every config load — no region is better than that one.
func TestInstallResurrectWritesNothingWhenThePluginsAreMissing(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		home := isolatedHome(t)
		conf := filepath.Join(home, ".tmux.conf")
		if err := os.WriteFile(conf, []byte("set -g mouse on\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := installResurrect(conf, conf)
		if !errors.Is(err, ErrResurrectUnavailable) {
			t.Fatalf("want ErrResurrectUnavailable, got %v", err)
		}
		b, _ := os.ReadFile(conf)
		if strings.Contains(string(b), resurrectBlockBegin) {
			t.Errorf("region written without its plugins:\n%s", b)
		}
	})
}

func TestInstallResurrectIsIdempotent(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		home := isolatedHome(t)
		fakePlugins(t, filepath.Join(home, ".tmux", "plugins"))
		conf := filepath.Join(home, ".tmux.conf")
		if err := os.WriteFile(conf, []byte("set -g mouse on\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, _, err := installResurrect(conf, conf); err != nil {
				t.Fatal(err)
			}
		}
		b, _ := os.ReadFile(conf)
		if n := strings.Count(string(b), resurrectBlockBegin); n != 1 {
			t.Errorf("region appears %d times after two installs:\n%s", n, b)
		}
		if !strings.Contains(string(b), "@continuum-restore 'on'") {
			t.Errorf("a fresh config should load the plugins:\n%s", b)
		}
	})
}

// The panel region and this one are separate fences; writing one must not
// disturb the other.
func TestResurrectAndPanelRegionsCoexist(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		home := isolatedHome(t)
		fakePlugins(t, filepath.Join(home, ".tmux", "plugins"))
		conf := filepath.Join(home, ".tmux.conf")
		if err := upsertBlock(conf, panelBlockLines("/bin/mux", "a", "Tab")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := installResurrect(conf, conf); err != nil {
			t.Fatal(err)
		}
		if err := upsertBlock(conf, panelBlockLines("/new/mux", "a", "Tab")); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(conf)
		s := string(b)
		if strings.Count(s, panelBlockBegin) != 1 || strings.Count(s, resurrectBlockBegin) != 1 {
			t.Errorf("want one of each region:\n%s", s)
		}
		if strings.Contains(s, "/bin/mux") {
			t.Errorf("panel block not replaced in place:\n%s", s)
		}
	})
}

// The hooks name this binary and are quoted for the two parsers: tmux's double
// quotes outside, the shell's single quotes around the path inside.
func TestResurrectBlockCarriesTheRestoreHooks(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/my tools/mux", "/p", true, true), "\n")
	for _, want := range []string{
		`set -g @resurrect-hook-pre-restore-all "'/my tools/mux' resurrect-hook pre"`,
		`set -g @resurrect-hook-post-restore-all "'/my tools/mux' resurrect-hook post"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("want %s in:\n%s", want, joined)
		}
	}
	if without := strings.Join(resurrectBlockLines("/bin/mux", "/p", true, false), "\n"); strings.Contains(without, "resurrect-hook") {
		t.Errorf("hooks written although the user sets them:\n%s", without)
	}
}

func TestUserSetsRestoreHooks(t *testing.T) {
	ours := strings.Join(resurrectBlockLines("/bin/mux", "/p", true, true), "\n")
	if userSetsRestoreHooks("set -g mouse on\n" + ours) {
		t.Error("mux's own hooks counted as the user's")
	}
	if !userSetsRestoreHooks("set -g @resurrect-hook-post-restore-all 'echo hi'\n") {
		t.Error("the user's own post-restore hook went unnoticed")
	}
	if userSetsRestoreHooks("# set -g @resurrect-hook-post-restore-all 'echo hi'\n") {
		t.Error("a commented-out hook counted")
	}
}

const sampleSave = "pane\tmux\t0\t1\t:*\t0\t✳ work\t:/w\t1\tclaude\t:\n" +
	"pane\tmux\t0\t1\t:*\t1\tmux panel\t:/w\t0\tmux\t:\n" +
	"pane\tsys\t2\t1\t:*\t0\tzsh\t:/s\t0\tzsh\t:\n" +
	"pane\tsys\t2\t1\t:*\t1\tvim\t:/s\t1\tvim\t:\n" +
	"pane\tsys\t2\t1\t:*\t2\tmux panel\t:/s\t0\tmux\t:\n" +
	// The title alone is not a panel: an adopted shell can inherit it.
	"pane\told\t0\t1\t:*\t0\tmux panel\t:/o\t1\tzsh\t:\n" +
	"window\tmux\t0\t:claude\t1\t:*\tlayout\t:\n" +
	"state\tmux\tsys\n"

func TestParseRestorePlan(t *testing.T) {
	plan := parseRestorePlan(sampleSave)
	wantActive := []string{"=mux:0.0", "=sys:2.1", "=old:0.0"}
	wantPanels := []string{"=mux:0.1", "=sys:2.2"}
	var gotActive, gotPanels []string
	for _, s := range plan.active {
		gotActive = append(gotActive, s.target())
	}
	for _, s := range plan.panels {
		gotPanels = append(gotPanels, s.target())
	}
	if strings.Join(gotActive, " ") != strings.Join(wantActive, " ") {
		t.Errorf("active = %v, want %v", gotActive, wantActive)
	}
	if strings.Join(gotPanels, " ") != strings.Join(wantPanels, " ") {
		t.Errorf("panels = %v, want %v", gotPanels, wantPanels)
	}
}

func TestRestoredDeadPanel(t *testing.T) {
	restored := "cat '/h/.local/share/tmux/resurrect/restore/pane_contents//pane-mux:0.1'; exec /usr/bin/zsh"
	cases := []struct {
		name, info string
		floor      int
		want       bool
	}{
		{"created by this restore", "%7\t2\t" + restored, 5, true},
		{"existed before the restore", "%3\t2\t" + restored, 5, false},
		{"the window's only pane", "%7\t1\t" + restored, 5, false},
		{"not a restored pane", "%7\t2\t/bin/mux watch", 5, false},
		{"plain shell", "%7\t2\t", 5, false},
		{"garbage", "", 5, false},
	}
	for _, c := range cases {
		id, ok := restoredDeadPanel(c.info, c.floor)
		if ok != c.want {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.want)
		}
		if ok && id != "%7" {
			t.Errorf("%s: id = %q", c.name, id)
		}
	}
}

func TestHighestPaneID(t *testing.T) {
	if got := highestPaneID("%2\n%10\n%9\n"); got != 10 {
		t.Errorf("highestPaneID = %d, want 10", got)
	}
	if got := highestPaneID(""); got != -1 {
		t.Errorf("no panes: highestPaneID = %d, want -1", got)
	}
}

func TestResurrectPreRestoreRecordsTheFloor(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%0\n%4\n%2\n"), nil, "tmux", "list-panes", "-a", "-F", "#{pane_id}")
		if err := ResurrectPreRestore(); err != nil {
			t.Fatal(err)
		}
		want := "tmux set-option -g " + restoreFloorOption + " 4"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("runs = %q, want %q", m.runs, want)
		}
	})
}

// postRestoreMock answers the post hook's reads: the floor, the save dir, and
// one display-message per panel slot.
func postRestoreMock(t *testing.T, m *mockRunner, floor string, slots map[string]string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "last"), []byte(sampleSave), 0o644); err != nil {
		t.Fatal(err)
	}
	m.OnOutput([]byte(floor), nil, "tmux", "show-options", "-gqv", restoreFloorOption)
	m.OnOutput([]byte(dir+"\n"), nil, "tmux", "show-options", "-gqv", "@resurrect-dir")
	for target, info := range slots {
		m.OnOutput([]byte(info+"\n"), nil, "tmux", "display-message", "-p", "-t", target,
			"#{pane_id}\t#{window_panes}\t#{pane_start_command}")
	}
}

// The whole fix: the saved active panes are selected by full address before
// anything is closed, and only the restored dead panels are closed.
func TestResurrectPostRestoreSelectsThenCloses(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		restored := "cat '/x/resurrect/restore/pane_contents//p'; exec zsh"
		postRestoreMock(t, m, "5\n", map[string]string{
			"=mux:0.1": "%7\t2\t" + restored,
			"=sys:2.2": "%3\t3\t" + restored, // older than the restore: someone's pane
		})
		if err := ResurrectPostRestore(); err != nil {
			t.Fatal(err)
		}
		want := []string{
			"tmux set-option -gu " + restoreFloorOption,
			"tmux select-pane -t =mux:0.0",
			"tmux select-pane -t =sys:2.1",
			"tmux select-pane -t =old:0.0",
			"tmux kill-pane -t %7",
			// Last, and whatever happened: RestoreAtStartup waits on this.
			"tmux set-option -g " + restoredOption + " 1",
		}
		if strings.Join(m.runs, "\n") != strings.Join(want, "\n") {
			t.Errorf("runs:\n%s\nwant:\n%s", strings.Join(m.runs, "\n"), strings.Join(want, "\n"))
		}
	})
}

// Without the pre hook's floor nothing proves a pane is new, so nothing is
// touched.
func TestResurrectPostRestoreStandsDownWithoutAFloor(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		postRestoreMock(t, m, "\n", nil)
		_ = ResurrectPostRestore()
		want := []string{
			"tmux set-option -gu " + restoreFloorOption,
			"tmux set-option -g " + restoredOption + " 1", // still says it is over
		}
		if strings.Join(m.runs, "\n") != strings.Join(want, "\n") {
			t.Errorf("acted without a floor: %q", m.runs)
		}
	})
}

func TestResurrectDirExpandsLikeResurrect(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		home := isolatedHome(t)
		m.OnOutput([]byte("~/saves\n"), nil, "tmux", "show-options", "-gqv", "@resurrect-dir")
		if got, _ := resurrectDir(); got != filepath.Join(home, "saves") {
			t.Errorf("~ not expanded: %s", got)
		}
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", "@resurrect-dir")
		t.Setenv("XDG_DATA_HOME", "")
		if got, _ := resurrectDir(); got != filepath.Join(home, ".local", "share", "tmux", "resurrect") {
			t.Errorf("default dir = %s", got)
		}
	})
}

// failingRunner is the mock with Run able to fail: RestoreAtStartup's first
// question — is a server up? — is answered by whether has-session fails.
type failingRunner struct {
	*mockRunner
	fail map[string]bool
}

func (f failingRunner) Run(name string, args ...string) error {
	err := f.mockRunner.Run(name, args...)
	if f.fail[f.key(name, args...)] {
		return errors.New("no server running")
	}
	return err
}

// withRestoreTiming makes the startup wait instant and counts the polls.
func withRestoreTiming(t *testing.T) {
	t.Helper()
	oldPoll, oldGrace, oldTimeout, oldSleep := restorePoll, restoreGrace, restoreTimeout, restoreSleep
	restorePoll, restoreGrace, restoreTimeout = time.Millisecond, 3*time.Millisecond, 10*time.Millisecond
	restoreSleep = func(time.Duration) {}
	t.Cleanup(func() {
		restorePoll, restoreGrace, restoreTimeout, restoreSleep = oldPoll, oldGrace, oldTimeout, oldSleep
	})
}

// confWithHooks writes a tmux config carrying mux's resurrect region, and a
// save file where resurrect's default directory is — the server is down, so
// that default is all RestoreAtStartup can read.
func confWithHooks(t *testing.T, hooks bool) {
	t.Helper()
	home := isolatedHome(t)
	t.Setenv("XDG_DATA_HOME", "")
	body := strings.Join(resurrectBlockLines("/bin/mux", "/p", true, hooks), "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local", "share", "tmux", "resurrect")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last"), []byte(sampleSave), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runStartup(t *testing.T, m *mockRunner, serverUp bool) (bool, []string) {
	t.Helper()
	fr := failingRunner{mockRunner: m, fail: map[string]bool{}}
	if !serverUp {
		fr.fail["tmux has-session"] = true
	}
	SetRunner(fr)
	var said []string
	started := RestoreAtStartup(func(s string) { said = append(said, s) })
	return started, said
}

// A server that is already up is never touched.
func TestRestoreAtStartupLeavesARunningServerAlone(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, true)
		started, _ := runStartup(t, m, true)
		if started || len(m.runs) != 1 {
			t.Errorf("acted on a running server: started=%v runs=%q", started, m.runs)
		}
	})
}

// Without mux's hooks there is no way to know the restore finished, and no
// business starting a server.
func TestRestoreAtStartupNeedsTheHooks(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, false)
		if started, _ := runStartup(t, m, false); started {
			t.Errorf("started a server without the hooks: %q", m.runs)
		}
	})
}

// The normal path: continuum restores, the post hook says so, the seed goes.
func TestRestoreAtStartupWaitsForContinuum(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, true)
		m.OnOutput([]byte("1\n"), nil, "tmux", "show-options", "-gqv", restoredOption)
		started, said := runStartup(t, m, false)
		if !started || len(said) != 1 {
			t.Fatalf("started=%v said=%q", started, said)
		}
		want := []string{
			"tmux has-session",
			// sampleSave holds a session called "mux", a prefix of the base name.
			"tmux new-session -d -s _-" + restoreSeedSession,
			"tmux set-option -gu " + restoredOption,
			"tmux kill-session -t =_-" + restoreSeedSession,
		}
		if strings.Join(m.runs, "\n") != strings.Join(want, "\n") {
			t.Errorf("runs:\n%s\nwant:\n%s", strings.Join(m.runs, "\n"), strings.Join(want, "\n"))
		}
	})
}

// continuum stands down when another tmux server runs; mux restores itself,
// once, and only because no restore had begun.
func TestRestoreAtStartupRunsTheRestoreWhenContinuumDoesNot(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, true)
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", restoredOption)
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", restoreFloorOption)
		m.OnOutput([]byte("/p/tmux-resurrect/scripts/restore.sh\n"), nil,
			"tmux", "show-options", "-gqv", "@resurrect-restore-script-path")
		runStartup(t, m, false)
		n := 0
		for _, r := range m.runs {
			if r == "tmux run-shell '/p/tmux-resurrect/scripts/restore.sh'" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("restore script ran %d times, want 1: %q", n, m.runs)
		}
		if last := m.runs[len(m.runs)-1]; last != "tmux kill-session -t =_-"+restoreSeedSession {
			t.Errorf("seed not closed last: %q", m.runs)
		}
	})
}

// A restore continuum already began is never run a second time.
func TestRestoreAtStartupDoesNotDoubleARestoreInProgress(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, true)
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", restoredOption)
		m.OnOutput([]byte("7\n"), nil, "tmux", "show-options", "-gqv", restoreFloorOption)
		m.OnOutput([]byte("/p/restore.sh\n"), nil, "tmux", "show-options", "-gqv", "@resurrect-restore-script-path")
		runStartup(t, m, false)
		for _, r := range m.runs {
			if strings.HasPrefix(r, "tmux run-shell") {
				t.Errorf("ran the restore beside continuum's: %q", m.runs)
			}
		}
	})
}

// The seed must not be a prefix match for any saved session: resurrect looks
// sessions up without `=`, and a saved "mux" restored into "mux-restoring".
func TestSeedSessionNameAvoidsPrefixMatches(t *testing.T) {
	cases := map[string][]string{
		"mux-restoring":   {"sys", "ppt-lyric"},
		"_-mux-restoring": {"mux", "sys"},
		// A session called "_" takes the first lead; this used to loop forever.
		"z-mux-restoring": {"m", "_"},
	}
	for want, saved := range cases {
		if got := seedSessionName(saved); got != want {
			t.Errorf("saved %q: seed = %q, want %q", saved, got, want)
		}
		for _, name := range saved {
			if strings.HasPrefix(seedSessionName(saved), name) {
				t.Errorf("seed %q still starts with saved %q", seedSessionName(saved), name)
			}
		}
	}
}

func TestSavedSessionNames(t *testing.T) {
	got := strings.Join(savedSessionNames(sampleSave), " ")
	if got != "mux sys old" {
		t.Errorf("savedSessionNames = %q", got)
	}
}

// Nothing saved, nothing started: a server would only hold the seed.
func TestRestoreAtStartupNeedsASave(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		withRestoreTiming(t)
		confWithHooks(t, true)
		home, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(home, ".local", "share", "tmux", "resurrect", "last"))
		if started, _ := runStartup(t, m, false); started {
			t.Errorf("started a server with nothing saved: %q", m.runs)
		}
	})
}
