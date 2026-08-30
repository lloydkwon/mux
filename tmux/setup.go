package tmux

import (
	"fmt"
	"os"
	"strings"
)

// IntegrationInstalled reports whether the tmux config carries any mux-owned
// region — the popup bind line or the panel block, in the main conf or, for
// oh-my-tmux, the .local file beside it.
//
// This is what decides whether the TUI's first-run offer appears, so the
// failure direction matters: when the config cannot even be located, the offer
// could not install anything either, and "installed" is the answer that keeps
// a broken environment from being nagged about it.
func IntegrationInstalled() bool {
	confPath, err := findTmuxConf()
	if err != nil {
		return true
	}

	paths := []string{confPath}
	if isOhMyTmux(confPath) {
		paths = append(paths, findTmuxConfLocal(confPath))
	}
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(content)
		if strings.Contains(s, muxKeybindMarker) || strings.Contains(s, panelBlockBegin) {
			return true
		}
	}
	return false
}

// InstallIntegration writes both mux-owned regions with the default keys and
// applies them to the running server — the quiet path behind the TUI's
// first-run offer, and what `mux setup` bundles for the command line.
//
// It reuses the same write helpers `setup-keybind` and `setup-panel` go
// through rather than those commands themselves, because they narrate to
// stdout and this runs with a TUI holding the screen. Applying both regions in
// one source-file is what makes the keys work the moment the offer is
// accepted, without a config reload the user would have to know about.
func InstallIntegration() error {
	muxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate mux binary: %w", err)
	}
	confPath, err := findTmuxConf()
	if err != nil {
		return err
	}

	bindLine := popupBindLine(DefaultBindKey, muxPath)
	target := confPath
	if isOhMyTmux(confPath) {
		target = findTmuxConfLocal(confPath)
		if err := writeBindToLocal(target, bindLine); err != nil {
			return err
		}
		// Same best-effort cleanup SetupKeybind does: a corrupt line an older
		// mux may have appended to the main conf breaks oh-my-tmux's heredoc.
		_, _ = stripMarkerLines(confPath)
	} else {
		if err := upsertBindLine(confPath, bindLine, true); err != nil {
			return err
		}
	}

	block := panelBlockLines(muxPath, DefaultPanelKey, DefaultFocusKey)
	if err := upsertBlock(target, block); err != nil {
		return err
	}

	_ = applyToServer(append([]string{bindLine}, block...))
	return nil
}
