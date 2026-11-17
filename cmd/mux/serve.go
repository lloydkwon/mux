package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lloydkwon/mux/serve"
)

// newServeCmd 는 `mux serve` — 폰 브라우저용 원격 화면이다(docs/design/serve.md).
func newServeCmd() *cobra.Command {
	var cfg serve.Config
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a phone-friendly session list over tailscale serve (127.0.0.1 only)",
		Long: `Serve the session list, Claude prompts (voice or text) and each session's last
turn and screen to a phone browser, through tailscale serve.

  mux serve --allow you@example.com
  tailscale serve --bg --https=8443 127.0.0.1:7690     # once, by hand

It binds to loopback only and answers only requests whose Tailscale-User-Login
header is on the --allow list; direct requests without the header are refused.
Prompts go through the same checks as "mux agent prompt". Nothing runs while no
client is connected.

With --whisper the phone records and uploads, and the server transcribes with
that command (scripts/whisper/setup.sh installs ~/.local/opt/mux-whisper/transcribe).
Terms for the prompt and "wrong => right" fixes come from --dictation, read on
every request. Without --whisper the phone uses the browser's own dictation.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serve.Run(ctx, cfg, os.Stderr)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&cfg.Addr, "addr", "127.0.0.1:7690", "loopback address to listen on")
	cmd.Flags().StringArrayVar(&cfg.Allow, "allow", nil, "Tailscale login allowed in (repeatable; required)")
	cmd.Flags().StringVar(&cfg.HostSuffix, "host-suffix", serve.DefaultHostSuffix, "the Host name must end with this (DNS rebinding guard)")
	cmd.Flags().StringVar(&cfg.Whisper, "whisper", "", "command that transcribes a recording: <cmd> --prompt=<terms> <file> → {\"text\": …} (empty: the browser's dictation)")
	cmd.Flags().StringVar(&cfg.DictationFile, "dictation", defaultDictationFile(), "dictation terms and \"wrong => right\" fixes, one per line (used with --whisper)")
	return cmd
}

// defaultDictationFile 은 받아쓰기 용어 파일의 기본 자리다(~/.config/mux/dictation.txt). 없어도 된다.
func defaultDictationFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mux", "dictation.txt")
}
