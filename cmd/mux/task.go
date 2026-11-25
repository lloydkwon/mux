package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
	"github.com/spf13/cobra"
)

// mux task — 작업마다 짧은 워커 세션(docs/design/task.md).

func newTaskCmd() *cobra.Command {
	taskCmd := &cobra.Command{
		Use:   "task",
		Short: "Run short-lived worker sessions: one task, one worktree, one Claude",
		Long: `One task = one git worktree = one tmux session t-<id> = one Claude.

  T=$(mux task start m123 --repo ~/dev/app --prompt "Read Mantis #123 and work on it")
  mux agent wait t-m123 --after "$T"
  mux task ls
  mux task done m123

The repositories to look in are listed in ~/.config/mux/task-repos.txt, one
absolute path per line. --repo and the current folder are looked in as well.

Exit codes: 0 ok, 1 error, 3 blocked on a dialog, 4 Claude did not get ready
(nothing was sent, the session stays), 5 refused (--force does not help),
6 the prompt was not submitted and rolled back (send it with mux agent prompt),
8 unpushed commits (--force goes on).`,
	}
	taskCmd.AddCommand(newTaskStartCmd(), newTaskLsCmd(), newTaskDoneCmd())
	return taskCmd
}

func newTaskStartCmd() *cobra.Command {
	var repo, title, prompt, promptFile string
	cmd := &cobra.Command{
		Use:   "start <id>",
		Short: "Make the worktree and session of a task and send its first prompt; prints a token",
		Long: `Makes the worktree <repo>-t-<id> on a new branch t-<id> (from the HEAD of the
repo folder), opens the session t-<id> in it and sends the first prompt. Prints
the token for mux agent wait/read --after.

Safe to run again: when the session is there it does nothing, and when only the
worktree is there it reopens the conversation without sending the prompt again.

The prompt is one line of at most 700 characters. A longer --prompt-file is sent
as "read this file", so the file has to stay where it is.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, cwd, err := taskDirs(repo)
			if err != nil {
				return err
			}
			tmux.ReviveServer()
			res, err := tmux.StartTask(tmux.TaskStartRequest{ID: args[0], Repo: dir, CWD: cwd, Title: title, Prompt: prompt, PromptFile: promptFile})
			if res.Base != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "worktree %s (%s @ %s)\n", res.Task.Worktree, res.Task.Branch, res.Base)
			}
			writeNotes(cmd.ErrOrStderr(), res.Notes)
			if err != nil {
				return err
			}
			if res.Token != nil {
				fmt.Fprintln(cmd.OutOrStdout(), res.Token.String())
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", res.Action, tmux.TaskSessionName(args[0]))
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "repository to make the worktree from (default: the repository of the current folder)")
	cmd.Flags().StringVar(&title, "title", "", "title shown in the panel (default: the usual generated title)")
	cmd.Flags().StringVar(&prompt, "prompt", "", "first prompt, one line of at most 700 characters")
	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "file holding the first prompt")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
	return cmd
}

func newTaskLsCmd() *cobra.Command {
	var repo string
	var asJSON bool
	cmd := &cobra.Command{
		Use:          "ls",
		Short:        "List tasks: id, repository, state and title",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, cwd, err := taskDirs(repo)
			if err != nil {
				return err
			}
			tmux.ReviveServer() // 기억(RememberServer)은 하지 않는다 — 다른 서버의 pane 에서 부른 조회가 기억을 바꾼다
			tasks, warnings, err := tmux.Tasks(dir, cwd)
			if err != nil {
				return err
			}
			if asJSON {
				b, err := tasksJSON(tasks)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
			} else {
				writeTaskTable(cmd.OutOrStdout(), tasks, time.Now())
			}
			// 읽은 것은 내고, 못 읽은 것이 있으면 실패다 — 빠진 목록을 다 읽은 것으로 알면 안 된다.
			return errors.Join(warnings...)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	cmd.Flags().StringVar(&repo, "repo", "", "also look in this repository")
	return cmd
}

func newTaskDoneCmd() *cobra.Command {
	var repo string
	var force bool
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Close a finished task: end its session and remove its worktree (the branch stays)",
		Long: `Ends the session t-<id> and removes its worktree. The branch is kept.

Refuses while Claude is working or waiting for an answer, and when there are
uncommitted changes - even with --force. With unpushed commits it stops unless
--force is given. Ignored files in the worktree (.env, node_modules) go with it.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, cwd, err := taskDirs(repo)
			if err != nil {
				return err
			}
			tmux.ReviveServer()
			res, err := tmux.FinishTask(tmux.TaskDoneRequest{
				ID: args[0], Repo: dir, CWD: cwd, Pane: os.Getenv("TMUX_PANE"), Force: force,
			})
			writeNotes(cmd.ErrOrStderr(), res.Notes)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "done %s", tmux.TaskSessionName(args[0]))
			if res.Task.Branch != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), " (branch %s stays)", res.Task.Branch)
			}
			fmt.Fprintln(cmd.ErrOrStderr())
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "repository the task is in, when the id is in more than one")
	cmd.Flags().BoolVar(&force, "force", false, "go on with unpushed commits")
	return cmd
}

// taskDirs 는 --repo 의 절대 경로와 지금 폴더다. tmux 패키지는 상대 경로를 받지 않는다 — 여기서는 사람이 친
// 셸의 폴더가 기준이라 푼다.
func taskDirs(repo string) (dir, cwd string, err error) {
	cwd, err = os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("지금 폴더를 알 수 없다: %w", err)
	}
	if repo == "" || repo == "~" || strings.HasPrefix(repo, "~/") {
		return repo, cwd, nil
	}
	dir, err = filepath.Abs(repo)
	if err != nil {
		return "", "", fmt.Errorf("저장소 경로: %w", err)
	}
	return dir, cwd, nil
}

func writeNotes(w io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintln(w, n)
	}
}

// taskJSON 은 ls --json 의 한 항목이다. 필드 이름은 TestTasksJSON 이 고정한다 — 스크립트와 오케스트레이터
// 세션이 읽는다(task.md 의 표). 빈 값도 늘 낸다.
type taskJSON struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Worktree  string `json:"worktree"`
	Branch    string `json:"branch"`
	Session   string `json:"session"`
	Pane      string `json:"pane"`
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Since     int64  `json:"since"` // unix ms, 모르면 0
	Title     string `json:"title"`
}

func tasksJSON(tasks []tmux.Task) ([]byte, error) {
	out := make([]taskJSON, 0, len(tasks)) // 빈 목록도 null 이 아닌 []
	for _, t := range tasks {
		j := taskJSON{
			ID: t.ID, Repo: t.Repo, Worktree: t.Worktree, Branch: t.Branch, Session: t.Session,
			State: string(t.State), Title: t.Title,
		}
		if t.Agent != nil {
			j.Pane, j.SessionID = t.Agent.PaneID, t.Agent.SessionID
		}
		if !t.Since.IsZero() {
			j.Since = t.Since.UnixMilli()
		}
		out = append(out, j)
	}
	return json.Marshal(out)
}

// taskStateLabel 은 표의 상태 칸이다. Claude 가 있으면 패널과 같은 배지를 쓴다.
func taskStateLabel(t tmux.Task) string {
	switch t.State {
	case tmux.TaskNoSession:
		return "세션 없음"
	case tmux.TaskNoWorktree:
		return "워크트리 없음"
	case tmux.TaskNoClaude:
		return "Claude 없음"
	}
	badges := map[tmux.TaskState]tmux.AIState{
		tmux.TaskState(tmux.AgentWorking): tmux.AIStateWorking,
		tmux.TaskState(tmux.AgentBlocked): tmux.AIStateApproval,
		tmux.TaskState(tmux.AgentIdle):    tmux.AIStateReady,
		tmux.TaskState(tmux.AgentShell):   tmux.AIStateShell,
	}
	if st, ok := badges[t.State]; ok {
		return st.Icon() + " " + string(t.State)
	}
	return string(t.State)
}

func writeTaskTable(w io.Writer, tasks []tmux.Task, now time.Time) {
	rows := [][]string{{"ID", "REPO", "STATE", "SINCE", "TITLE"}}
	for _, t := range tasks {
		repo, since := "-", "-"
		if t.Repo != "" {
			repo = filepath.Base(t.Repo)
		}
		if !t.Since.IsZero() {
			since = shortAge(now.Sub(t.Since))
		}
		rows = append(rows, []string{t.ID, repo, taskStateLabel(t), since, t.Title})
	}
	writeColumns(w, rows)
}

// writeColumns 는 열을 화면 폭으로 맞춘다 — tabwriter 는 글자 수로 세서 한글 뒤의 열이 밀린다.
func writeColumns(w io.Writer, rows [][]string) {
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
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}
