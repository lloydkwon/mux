//go:build e2e

// «New worktree» 행을 격리 tmux 와 진짜 git 위에서 끝까지 돌린다. 행에서 브랜치를 치면 원본 저장소 옆에 worktree 가
// 생기고, TUI 가 끝난 뒤 cmd/mux 가 그 폴더의 셸 세션을 만들어 붙은 클라이언트를 그리로 옮긴다.
// git 의 응답 모양(worktree list --porcelain -z, for-each-ref, 원격 추적)도 여기서 진짜로 잰다 — 단위 시험은 목이다.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// gitRepo 는 커밋 하나짜리 저장소를 만든다. 사용자의 git 설정과 무관하게 이름·기본 브랜치를 못 박는다.
func gitRepo(t *testing.T, parent, name string) string {
	t.Helper()
	repo := filepath.Join(parent, name)
	git(t, parent, "-c", "init.defaultBranch=main", "init", "-q", name)
	git(t, repo, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	return repo
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// startTUIIn 은 startTUI 와 같되 TUI 세션이 dir 에서 시작한다 — 그 세션이 «mux 를 띄운 세션» 이 된다.
func startTUIIn(t *testing.T, session, dir string) string {
	t.Helper()
	prefs := filepath.Join(home, ".config", "mux", "preferences.json")
	if err := os.WriteFile(prefs, []byte(`{"sort":"name","setup_offered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(prefs) })
	pane := tm(t, "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session, "-x", "160", "-y", "40", "-c", dir,
		"MUX_NO_BOOTSTRAP=1 exec '"+muxBin+"'")
	t.Cleanup(func() { tmq("kill-session", "-t", "="+session) })
	return pane
}

// repoField 는 입력 화면의 Repo 줄이다. 칸이 40칸이라 긴 경로는 끝부분만 보인다.
func repoField(t *testing.T, pane string) string {
	t.Helper()
	for _, line := range strings.Split(screen(t, pane), "\n") {
		if strings.Contains(line, "Repo:") {
			return line
		}
	}
	return ""
}

// openWorktreePrompt 는 TUI 의 New worktree 행(맨 위에서 셋째)에서 enter 를 누른다.
func openWorktreePrompt(t *testing.T, tui string) {
	t.Helper()
	eventually(t, "TUI 가 뜬다", 10*time.Second, func() bool { return strings.Contains(screen(t, tui), "New worktree") })
	for _, k := range []string{"g", "j", "j", "Enter"} {
		tm(t, "send-keys", "-t", tui, k)
	}
	eventually(t, "입력 화면", 5*time.Second, func() bool { return strings.Contains(screen(t, tui), "New Worktree") })
}

func TestNewWorktreeSession(t *testing.T) {
	parent := t.TempDir()
	repo := gitRepo(t, parent, "wtrepo")
	wt := filepath.Join(parent, "wtrepo-feat-x")

	tui := startTUIIn(t, "tui-wt", repo)
	// mux popup 처럼 붙은 클라이언트가 있어야 «mux 를 띄운 세션» 을 안다(CurrentSessionName 은 client_pid 를 본다).
	c := attach(t, "tui-wt", 160, 40, false)
	openWorktreePrompt(t, tui)
	eventually(t, "Repo 칸이 mux 를 띄운 세션의 폴더로 채워진다", 5*time.Second, func() bool {
		return strings.HasSuffix(strings.TrimRight(repoField(t, tui), " │"), "/wtrepo")
	})
	tm(t, "send-keys", "-t", tui, "-l", "feat/x")
	tm(t, "send-keys", "-t", tui, "Enter")

	eventually(t, "worktree 세션이 생기고 클라이언트가 그리로 간다", 15*time.Second, func() bool {
		return c.session() == "wtrepo-feat-x"
	})
	t.Cleanup(func() { tmq("kill-session", "-t", "=wtrepo-feat-x") })
	if got := tmq("display-message", "-p", "-t", "=wtrepo-feat-x:", "#{session_path}"); got != wt {
		t.Errorf("session_path = %q, want the worktree %q", got, wt)
	}
	if got := git(t, wt, "branch", "--show-current"); got != "feat/x" {
		t.Errorf("worktree branch = %q, want feat/x", got)
	}
	// 태그를 달면 패널 자동 삽입이 물러난다 — 달지 않았는지 본다.
	if got := tmq("show-options", "-qv", "-t", "=wtrepo-feat-x:", "@project_dir"); got != "" {
		t.Errorf("@project_dir = %q, want none", got)
	}

	// 같은 브랜치로 다시: worktree 는 그대로 쓰고 세션은 -2 로 하나 더. 먼저 원본의 브랜치를 쳐서 거절을 본다.
	tui2 := startTUIIn(t, "tui-wt2", repo)
	c2 := attach(t, "tui-wt2", 150, 40, false)
	openWorktreePrompt(t, tui2)
	eventually(t, "Repo 칸", 5*time.Second, func() bool {
		return strings.HasSuffix(strings.TrimRight(repoField(t, tui2), " │"), "/wtrepo")
	})
	tm(t, "send-keys", "-t", tui2, "-l", "main")
	tm(t, "send-keys", "-t", tui2, "Enter")
	eventually(t, "원본 작업 트리의 브랜치는 입력 화면에서 거절한다", 10*time.Second, func() bool {
		return strings.Contains(screen(t, tui2), "원본 작업 트리")
	})
	tm(t, "send-keys", "-t", tui2, "C-u")
	tm(t, "send-keys", "-t", tui2, "-l", "feat/x")
	tm(t, "send-keys", "-t", tui2, "Enter")
	eventually(t, "같은 worktree 의 두 번째 세션", 15*time.Second, func() bool {
		return c2.session() == "wtrepo-feat-x-2"
	})
	t.Cleanup(func() { tmq("kill-session", "-t", "=wtrepo-feat-x-2") })
	if got := tmq("display-message", "-p", "-t", "=wtrepo-feat-x-2:", "#{session_path}"); got != wt {
		t.Errorf("second session_path = %q, want the same worktree %q", got, wt)
	}
	if out := git(t, repo, "worktree", "list"); strings.Count(out, "\n")+1 != 2 {
		t.Errorf("want exactly one linked worktree, got:\n%s", out)
	}
}

// git 쪽 분기를 진짜 git 으로 — 로컬 브랜치, 원격 하나에만 있는 브랜치, linked worktree 에서 부른 경우.
func TestPrepareWorktreeAgainstRealGit(t *testing.T) {
	parent := t.TempDir()
	origin := gitRepo(t, parent, "origin")
	git(t, origin, "branch", "feat/remote")
	git(t, parent, "clone", "-q", "origin", "clone")
	clone := filepath.Join(parent, "clone")
	git(t, clone, "branch", "feat/local")

	local, err := tmux.PrepareWorktree(clone, "feat/local")
	if err != nil {
		t.Fatalf("local branch: %v", err)
	}
	if got := git(t, local.Dir, "branch", "--show-current"); local.Dir != filepath.Join(parent, "clone-feat-local") || got != "feat/local" {
		t.Errorf("local branch: %+v on %q", local, got)
	}

	remote, err := tmux.PrepareWorktree(clone, "feat/remote")
	if err != nil {
		t.Fatalf("remote-only branch: %v", err)
	}
	if got := git(t, remote.Dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); got != "origin/feat/remote" {
		t.Errorf("remote-only branch tracks %q, want origin/feat/remote", got)
	}

	// linked worktree 에서 불러도 원본 옆이고, 시작점은 그 worktree 의 HEAD 다.
	git(t, local.Dir, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "commit", "-q", "--allow-empty", "-m", "on local")
	fromLinked, err := tmux.PrepareWorktree(local.Dir, "feat/next")
	if err != nil {
		t.Fatalf("from a linked worktree: %v", err)
	}
	if fromLinked.Dir != filepath.Join(parent, "clone-feat-next") {
		t.Errorf("from a linked worktree: Dir = %q, want beside the original", fromLinked.Dir)
	}
	if got := git(t, fromLinked.Dir, "log", "-1", "--format=%s"); got != "on local" {
		t.Errorf("from a linked worktree: started at %q, want that worktree's HEAD", got)
	}

	// 폴더를 지운 worktree 는 prune 하라고 한다.
	if err := os.RemoveAll(remote.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := tmux.PrepareWorktree(clone, "feat/remote"); err == nil || !strings.Contains(err.Error(), "git worktree prune") {
		t.Errorf("a removed worktree: err = %v, want the prune hint", err)
	}
}
