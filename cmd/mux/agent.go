package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
	"github.com/spf13/cobra"
)

// mux agent — 에이전트 협업 API (docs/design/agent-api.md).
//
// 다른 Claude 에게 일을 보내고(prompt), 끝나기를 기다리고(wait), 회신을 읽는다(read).
// 스크립트가 종료 코드로 갈라 탈 수 있게 오류마다 코드가 정해져 있다(exitCode).

func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Prompt, wait for and read another Claude agent in this tmux server",
		Long: `Agent collaboration for Claude Code panes on this tmux server.

  T=$(mux agent prompt <target> "<one-line prompt>")
  mux agent wait <target> --after "$T"
  mux agent read <target> --after "$T"

A target is a pane id (%12), a session holding exactly one Claude pane, or a
Claude conversation name (claude -n / /rename). Ambiguous targets are refused.

Exit codes: 0 ok, 1 error, 2 no reply text, 3 blocked on a dialog,
4 wait timed out, 5 not ready (nothing was sent), 6 not submitted and rolled
back (safe to retry), 7 the prompt never showed up in the transcript (it may
have been submitted - do not resend).`,
	}
	agentCmd.AddCommand(newAgentLsCmd(), newAgentPromptCmd(), newAgentWaitCmd(), newAgentReadCmd())
	return agentCmd
}

func newAgentLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "ls",
		Short:        "List Claude agents running in tmux panes",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agents, err := tmux.Agents()
			if err != nil {
				return err
			}
			self := os.Getenv("TMUX_PANE")
			if !asJSON {
				writeAgentTable(cmd.OutOrStdout(), agents, self, time.Now())
				return nil
			}
			b, err := agentsJSON(agents, self)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newAgentPromptCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "prompt <target> <text|->",
		Short: "Type a one-line prompt into the target's input box and submit it; prints a token",
		Long: `Types the text once, presses Enter only when the input box shows exactly that
text, and confirms the submission. On success prints a token for wait/read --after.

The text must be one line of at most 700 characters ("-" reads it from stdin).
Write long bodies to a file and send its path. A text starting with "-" needs
"--" before it: mux agent prompt plan -- "-n ...".

--force also sends to a working agent (Claude queues it) and leaves copy mode.
It never answers a dialog and never clears what is already in the input box.`,
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := args[1]
			if text == "-" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimRight(string(b), "\r\n")
			}
			a, err := resolveTarget(args[0], true)
			if err != nil {
				return err
			}
			tok, err := tmux.PromptAgent(a, text, force)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), tok.String())
			fmt.Fprintf(cmd.ErrOrStderr(), "submitted → %s (%s · %s)\n", a.PaneID, a.Session, a.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "send even while the agent is working (queued), and leave copy mode")
	return cmd
}

func newAgentWaitCmd() *cobra.Command {
	var after string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait <target>",
		Short: "Wait until the target is idle, or until the turn a prompt started has ended",
		Long: `Without --after, waits until the target is not working. With the token from
prompt, waits until the turn that prompt started has ended. Local commands that
start no turn (/rename, /model) end in exit 7 after 15s - wait without --after.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := parseAfter(after)
			if err != nil {
				return err
			}
			a, err := resolveTarget(args[0], true)
			if err != nil {
				return err
			}
			_, err = tmux.WaitAgent(a, tok, timeout)
			return err
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "token printed by prompt: wait for that prompt's turn to end")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "give up after this long (0 waits forever)")
	return cmd
}

func newAgentReadCmd() *cobra.Command {
	var after string
	cmd := &cobra.Command{
		Use:          "read <target>",
		Short:        "Print the target's reply: the last response of the turn (or of the prompt given by --after)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := parseAfter(after)
			if err != nil {
				return err
			}
			a, err := resolveTarget(args[0], false)
			if err != nil {
				return err
			}
			text, err := tmux.ReadAgentReply(a, tok)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), text)
			return nil
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "token printed by prompt: read the reply to that prompt")
	return cmd
}

// resolveTarget 는 대상을 정한다. noSelf 면 자기 pane 을 거부한다 — 자기에게 보내면 지금
// 도는 턴의 입력창에 글이 들어가고, 자기를 기다리면 끝나지 않는다.
func resolveTarget(target string, noSelf bool) (tmux.Agent, error) {
	agents, err := tmux.Agents()
	if err != nil {
		return tmux.Agent{}, err
	}
	a, err := tmux.ResolveAgent(agents, target)
	if err != nil {
		return tmux.Agent{}, err
	}
	if noSelf && a.PaneID != "" && a.PaneID == os.Getenv("TMUX_PANE") {
		return tmux.Agent{}, fmt.Errorf("%s 는 이 명령을 부른 자기 pane 이다", a.PaneID)
	}
	return a, nil
}

func parseAfter(s string) (*tmux.AgentToken, error) {
	if s == "" {
		return nil, nil
	}
	tok, err := tmux.ParseAgentToken(s)
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

// agentJSON 은 ls --json 의 한 줄이다. 필드 이름은 TestAgentsJSON 이 고정한다 — 스크립트가
// 읽는다. status --json 의 sessionJSON 과 섞지 않는다(상태 단어가 다르다).
type agentJSON struct {
	Pane       string `json:"pane"`
	Session    string `json:"session"`
	Tool       string `json:"tool"`
	State      string `json:"state"`
	Since      int64  `json:"since,omitempty"` // 상태 시작 시각, unix ms
	WaitingFor string `json:"waitingFor,omitempty"`
	CWD        string `json:"cwd"`
	Name       string `json:"name"`
	SessionID  string `json:"sessionId"`
	PID        int    `json:"pid"`
	Self       bool   `json:"self,omitempty"` // 이 명령을 부른 pane
}

func agentsJSON(agents []tmux.Agent, self string) ([]byte, error) {
	out := make([]agentJSON, 0, len(agents)) // 빈 목록도 null 이 아닌 []
	for _, a := range agents {
		j := agentJSON{
			Pane:       a.PaneID,
			Session:    a.Session,
			Tool:       a.Tool,
			State:      string(a.State),
			WaitingFor: a.WaitingFor,
			CWD:        a.CWD,
			Name:       a.Name,
			SessionID:  a.SessionID,
			PID:        a.PID,
			Self:       self != "" && a.PaneID == self,
		}
		if !a.Since.IsZero() {
			j.Since = a.Since.UnixMilli()
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

// writeAgentTable 은 사람이 읽는 표다. 열은 화면 폭으로 맞춘다 — tabwriter 는 글자 수로
// 세서 한글 이름 뒤의 열이 밀린다.
func writeAgentTable(w io.Writer, agents []tmux.Agent, self string, now time.Time) {
	rows := [][]string{{"PANE", "SESSION", "STATE", "SINCE", "NAME", "CWD"}}
	for _, a := range agents {
		pane := a.PaneID
		if self != "" && a.PaneID == self {
			pane += "*"
		}
		since := "-"
		if !a.Since.IsZero() {
			since = shortAge(now.Sub(a.Since))
		}
		rows = append(rows, []string{pane, a.Session, string(a.State), since, a.Name, a.CWD})
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], ansi.StringWidth(c))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(c)+2))
			}
		}
		fmt.Fprintln(w, b.String())
	}
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// exitCode 는 오류를 종료 코드로 바꾼다. 매핑에 없는 오류는 지금까지처럼 1 이다.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tmux.ErrNoReply):
		return 2
	case errors.Is(err, tmux.ErrAgentBlocked):
		return 3
	case errors.Is(err, tmux.ErrWaitTimeout):
		return 4
	case errors.Is(err, tmux.ErrAgentNotReady), errors.Is(err, tmux.ErrTurnRunning):
		return 5
	case errors.Is(err, tmux.ErrNotSubmitted):
		return 6
	case errors.Is(err, tmux.ErrPromptNotSeen):
		// 6 과 떼어 둔다: 6 은 «되돌렸으니 다시 보내도 된다» 이고, 이것은 «제출은 됐을 수 있다» 다.
		// 둘을 섞으면 6 에서 다시 보내는 스크립트가 두 번 제출한다.
		return 7
	default:
		return 1
	}
}
