package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/lloydkwon/mux/tmux"
	"github.com/lloydkwon/mux/ui"
)

// version is injected at build time via -ldflags by the Makefile and by
// goreleaser. Builds that skip those — notably `go install <module>@<version>`
// — leave it at defaultVersion and fall back to resolveVersion.
var version = defaultVersion

const defaultVersion = "dev"

// resolveVersion reports the version to display.
//
// An injected value always wins: release builds stamp the exact tag, and
// nothing Go records is more authoritative than that. Otherwise the module
// version Go recorded is used — since Go 1.24 that covers plain local builds
// too, derived from the nearest VCS tag. The bare revision is a last resort
// for a build with no reachable tag. Only a build with none of these stays
// "dev".
//
// The leading "v" is trimmed from every source so that all build paths agree:
// goreleaser passes a bare "0.2.0" while git describe and the module version
// both yield "v0.2.0".
func resolveVersion(injected string, info *debug.BuildInfo, ok bool) string {
	return strings.TrimPrefix(rawVersion(injected, info, ok), "v")
}

func rawVersion(injected string, info *debug.BuildInfo, ok bool) string {
	if injected != "" && injected != defaultVersion {
		return injected
	}
	if !ok || info == nil {
		return defaultVersion
	}

	// "(devel)" is what Go records when the build maps to no module version
	// and no VCS tag is reachable — fall through to the revision.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return defaultVersion
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

func main() {
	info, ok := debug.ReadBuildInfo()

	rootCmd := &cobra.Command{
		Use:     "mux",
		Short:   "TUI tmux session manager",
		Version: resolveVersion(version, info, ok),
		RunE:    runTUI,
		// Suppress cobra's default completion and help subcommands
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	rootCmd.SetVersionTemplate("mux {{.Version}}\n")

	popupCmd := &cobra.Command{
		Use:   "popup",
		Short: "Open mux as a tmux popup overlay",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tmux.OpenPopup()
		},
	}

	setupKeybindCmd := &cobra.Command{
		Use:   "setup-keybind [key]",
		Short: fmt.Sprintf("Add popup keybinding to tmux config (default: %s)", tmux.DefaultBindKey),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := tmux.DefaultBindKey
			if len(args) > 0 {
				key = args[0]
			}
			return tmux.SetupKeybind(key)
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show AI session summary for tmux statusbar",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}

	rootCmd.AddCommand(popupCmd, setupKeybindCmd, statusCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runStatus() error {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return err
	}

	var parts []string
	for _, s := range sessions {
		tool, ok := tmux.SessionAITool(s)
		if !ok {
			continue
		}
		parts = append(parts, tool.Icon)
	}

	if len(parts) == 0 {
		return nil // no AI sessions, output nothing
	}

	fmt.Print(fmt.Sprintf(" %s ", joinWith(parts, " ")))
	return nil
}

func joinWith(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

func runTUI(cmd *cobra.Command, args []string) error {
	p := tea.NewProgram(ui.NewModel(), tea.WithAltScreen())

	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	if m, ok := result.(ui.Model); ok {
		if m.DetachRequested() {
			if err := ui.DetachClient(); err != nil {
				return fmt.Errorf("failed to detach tmux client: %w", err)
			}
			return nil
		}
		if name := m.AttachName(); name != "" {
			if err := ui.AttachToSession(name, m.AttachWindowIndex(), m.AttachPaneIndex()); err != nil {
				return fmt.Errorf("failed to attach: %w", err)
			}
		}
	}
	return nil
}
