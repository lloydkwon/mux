package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The offer must not appear for a user who already ran either setup command —
// whichever region is present is proof they know the commands exist.
func TestIntegrationInstalledFindsEitherRegion(t *testing.T) {
	cases := map[string]string{
		"popup bind": "bind m run-shell 'mux popup'  " + muxKeybindMarker + "\n",
		"panel block": panelBlockBegin + "\nbind a run-shell 'mux panel'\n" +
			panelBlockEnd + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if !IntegrationInstalled() {
				t.Errorf("IntegrationInstalled() = false with %s present", name)
			}
		})
	}
}

func TestIntegrationInstalledIsFalseOnAFreshMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if IntegrationInstalled() {
		t.Error("IntegrationInstalled() = true with no config at all")
	}

	// A config that exists but says nothing about mux is still a fresh machine.
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g mouse on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if IntegrationInstalled() {
		t.Error("IntegrationInstalled() = true for a config with no mux region")
	}
}

// Accepting the offer must leave the machine exactly where the two setup
// commands would: both regions present, and recognised as installed.
func TestInstallIntegrationWritesBothRegions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TMUX", "") // no server to apply to; the files are the test

	if err := InstallIntegration(); err != nil {
		t.Fatalf("InstallIntegration: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(home, ".tmux.conf"))
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, muxKeybindMarker) {
		t.Errorf("conf lacks the popup bind:\n%s", s)
	}
	if !strings.Contains(s, panelBlockBegin) || !strings.Contains(s, panelBlockEnd) {
		t.Errorf("conf lacks the panel block:\n%s", s)
	}
	if !IntegrationInstalled() {
		t.Error("IntegrationInstalled() = false right after InstallIntegration")
	}
}
