package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
every request; each transcription is appended to --dictation-log (the audio is
never kept). Without --whisper the phone uses the browser's own dictation.

Each device must also unlock the page (named Muxtap on the phone) with a passkey
— Face ID or a security key. Register a device with a one-time code from
"mux serve enroll". Passkeys live in --passkeys; an empty --passkeys turns the
passkey check off and trusts the Tailscale identity alone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.Passkeys == "" && !cmd.Flags().Changed("passkeys") {
				return errors.New("패스키 파일 자리를 정하지 못했다(설정 폴더 없음) — --passkeys 로 준다. 끄려면 --passkeys=")
			}
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
	cmd.Flags().StringVar(&cfg.DictationFile, "dictation", muxConfigFile("dictation.txt"), "dictation terms and \"wrong => right\" fixes, one per line (used with --whisper)")
	cmd.Flags().StringVar(&cfg.DictationLog, "dictation-log", muxConfigFile("dictation-log.jsonl"), "append each transcription (heard text, fixed text, durations) as a JSON line; empty: keep none")
	cmd.Flags().StringVar(&cfg.Passkeys, "passkeys", muxConfigFile("passkeys.json"), "registered passkeys (public keys only); empty: no passkey check, Tailscale identity alone")
	cmd.AddCommand(newServeEnrollCmd())
	return cmd
}

// newServeEnrollCmd 는 `mux serve enroll` — 폰 하나를 패스키로 등록할 일회용 코드를 낸다(docs/design/serve.md
// «패스키 로그인»). 코드는 해시로만 파일에 남고, 돌고 있는 mux serve 가 요청마다 그 파일을 읽는다.
func newServeEnrollCmd() *cobra.Command {
	var passkeys string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Print a one-time code that registers a phone's passkey with mux serve",
		Long: `Print a one-time code for registering a device's passkey (Face ID or a
security key). Open Muxtap on the phone, tap "새 기기 등록" and type the code.

The code works once, for --ttl, and a new code replaces the old one. Five wrong
tries delete it. The running mux serve reads it from the file next to
--passkeys, so this does not need to talk to the server.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if passkeys == "" {
				return errors.New("패스키 파일 자리를 정하지 못했다 — --passkeys 로 준다(mux serve 와 같은 값)")
			}
			if ttl <= 0 || ttl > time.Hour {
				return fmt.Errorf("--ttl %s: 0 보다 길고 1시간 이하로 준다", ttl)
			}
			code, err := serve.NewEnrollCode(passkeys, ttl, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Muxtap 등록 코드: %s\n", serve.FormatEnrollCode(code))
			fmt.Fprintf(out, "폰에서 Muxtap 을 열고 «새 기기 등록» 에 넣으세요. %s 동안, 한 번만 씁니다.\n", ttl)
			return nil
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&passkeys, "passkeys", muxConfigFile("passkeys.json"), "the same --passkeys file mux serve uses")
	cmd.Flags().DurationVar(&ttl, "ttl", serve.DefaultEnrollTTL, "how long the code stays valid (at most 1h)")
	return cmd
}

// muxConfigFile 은 ~/.config/mux 안의 파일 자리다 — 받아쓰기 용어 파일(dictation.txt, 없어도 된다)과 받아쓰기
// 기록(dictation-log.jsonl).
func muxConfigFile(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mux", name)
}
