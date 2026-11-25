package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func TestTaskExitCodes(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{tmux.ErrNoTask, 1},
		{fmt.Errorf("%w: 작업 중", tmux.ErrTaskRefused), 5},
		// 5 는 --force 로도 안 되고 8 은 --force 면 된다. 섞으면 스크립트가 커밋 안 된 변경에 --force 를 준다.
		{fmt.Errorf("%w: 2개", tmux.ErrTaskUnpushed), 8},
		// 세션을 닫은 뒤 worktree 를 남긴 것도 5 다.
		{fmt.Errorf("세션은 닫았고: %w", tmux.ErrTaskRefused), 5},
	}
	for _, tt := range tests {
		if got := exitCode(tt.err); got != tt.want {
			t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

// 필드 이름과 빈 값은 스크립트와의 약속이다.
func TestTasksJSON(t *testing.T) {
	agent := tmux.Agent{PaneID: "%5", SessionID: "conv"}
	b, err := tasksJSON([]tmux.Task{
		{ID: "m1", Repo: "/p/repo", Worktree: "/p/repo-t-m1", Branch: "t-m1", Session: "t-m1",
			State: "working", Since: time.UnixMilli(1793589532061), Title: "로그인", Agent: &agent, Commits: 2},
		{ID: "m2", Repo: "/p/repo", Worktree: "/p/repo-t-m2", State: tmux.TaskNoSession, Commits: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"id":"m1","repo":"/p/repo","worktree":"/p/repo-t-m1","branch":"t-m1","session":"t-m1","pane":"%5","sessionId":"conv","state":"working","since":1793589532061,"commits":2,"title":"로그인"},` +
		`{"id":"m2","repo":"/p/repo","worktree":"/p/repo-t-m2","branch":"","session":"","pane":"","sessionId":"","state":"no-session","since":0,"commits":-1,"title":""}]`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	if b, _ := tasksJSON(nil); string(b) != "[]" {
		t.Errorf("빈 목록 = %s", b)
	}
}

func TestTaskTableAlignsWideText(t *testing.T) {
	now := time.UnixMilli(1793589600000)
	var b strings.Builder
	writeTaskTable(&b, []tmux.Task{
		{ID: "m1", Repo: "/p/repo", State: tmux.TaskState(tmux.AgentWorking), Since: now.Add(-5 * time.Minute), Title: "제목"},
		{ID: "wms-45", Repo: "/p/wms", State: tmux.TaskNoSession, Title: "제목"},
		{ID: "x", State: tmux.TaskNoWorktree, Title: "제목"},
		{ID: "y", Repo: "/p/repo", State: tmux.TaskNoClaude, Title: "제목"},
	}, now)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("줄 = %d\n%s", len(lines), b.String())
	}
	for _, want := range []string{"⏳ working", "세션 없음", "워크트리 없음", "Claude 없음", "5m"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("%q 가 없다:\n%s", want, b.String())
		}
	}
	col := ansi.StringWidth(strings.SplitN(lines[0], "TITLE", 2)[0])
	for _, l := range lines[1:] {
		if got := ansi.StringWidth(strings.SplitN(l, "제목", 2)[0]); got != col {
			t.Errorf("제목 열 = %d, want %d: %q", got, col, l)
		}
	}
}

func TestTaskDirsResolvesWhatThePersonTyped(t *testing.T) {
	t.Chdir(t.TempDir())
	for in, wantAbs := range map[string]bool{"": false, "~/x": false, "/abs": true, "rel": true, ".": true} {
		dir, cwd, err := taskDirs(in)
		if err != nil || cwd == "" {
			t.Fatal(err)
		}
		if wantAbs != strings.HasPrefix(dir, "/") {
			t.Errorf("taskDirs(%q) = %q", in, dir)
		}
		if in == "rel" && dir != cwd+"/rel" {
			t.Errorf("rel = %q, cwd %q", dir, cwd)
		}
	}
}

func TestTaskStartRefusesTwoPrompts(t *testing.T) {
	cmd := newTaskCmd()
	cmd.SetArgs([]string{"start", "m1", "--prompt", "a", "--prompt-file", "b"})
	cmd.SilenceErrors = true
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil || errors.Is(err, tmux.ErrNoServer) {
		t.Errorf("err = %v", err)
	}
}
