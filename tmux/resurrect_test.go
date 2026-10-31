package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		block := resurrectBlockLines("/p", load)
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
	joined := strings.Join(resurrectBlockLines("/p", false), "\n")
	if strings.Contains(joined, "run-shell") || strings.Contains(joined, "@continuum-restore") {
		t.Errorf("option-only block still loads plugins or sets restore:\n%s", joined)
	}
}

// Continuum reads the script paths resurrect publishes when it loads.
func TestResurrectBlockLoadsResurrectBeforeContinuum(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/p", true), "\n")
	r := strings.Index(joined, "tmux-resurrect/resurrect.tmux")
	c := strings.Index(joined, "tmux-continuum/continuum.tmux")
	if r < 0 || c < 0 || r > c {
		t.Errorf("want resurrect loaded before continuum:\n%s", joined)
	}
}

// The two-parser rule: double quotes for tmux, single quotes for /bin/sh.
func TestResurrectBlockQuotesAPathWithSpaces(t *testing.T) {
	joined := strings.Join(resurrectBlockLines("/my plugins", true), "\n")
	want := `run-shell "'/my plugins/tmux-resurrect/resurrect.tmux'"`
	if !strings.Contains(joined, want) {
		t.Errorf("want %s in:\n%s", want, joined)
	}
}

func TestUserLoadsResurrect(t *testing.T) {
	ours := strings.Join(resurrectBlockLines("/p", true), "\n")
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
