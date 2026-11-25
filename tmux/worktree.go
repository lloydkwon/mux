package tmux

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 새 worktree 세션 — 목록 맨 위의 «New worktree» 행이 부른다. 설계 이유는 docs/design/worktree.md.
//
// 여기서는 git 만 한다. 세션 생성과 attach 는 TUI 가 끝난 뒤 cmd/mux 가 한다(루트 CLAUDE.md 의 루프 규칙).

const (
	// worktreeReadLimit 는 읽기만 하는 git 호출의 제한 시간이다.
	worktreeReadLimit = 5 * time.Second
	// worktreeAddLimit 는 `git worktree add` 의 제한 시간이다. 체크아웃과 post-checkout 훅이 함께 돌아 읽기보다 길게 준다.
	// 끝이 없으면 입력 화면의 «creating worktree…» 가 영원히 남는다.
	worktreeAddLimit = 2 * time.Minute

	// worktreeTries 번까지, worktreeRetryDelay 간격으로 gitWorktree 가 다시 부른다.
	worktreeTries      = 10
	worktreeRetryDelay = 100 * time.Millisecond
)

// halfMadeWorktree 는 git 이 이웃 worktree 의 관리 파일을 읽지 못했다는 오류다. 문구는 로캘을 타고 파일 이름은 타지 않는다.
var halfMadeWorktree = regexp.MustCompile(`worktrees/[^/\n]+/(commondir|locked)\b`)

// gitWorktree 는 `git … worktree …` 하나를 부른다. 다른 git 이 같은 저장소의 worktree 를 만들거나 지우는 순간에는
// list·add·remove 가 모두 128 로 끝나므로(task.md «겹친 git worktree») 그 오류일 때만 잠깐 뒤 다시 부른다.
//
// 다시 불러도 되는 명령만 넘긴다 — `add -b` 는 실패해도 브랜치를 남겨 두 번째가 «이미 있다» 가 된다.
func gitWorktree(limit time.Duration, args ...string) ([]byte, error) {
	for try := 1; ; try++ {
		out, err := outputWithin(limit, "git", args...)
		if err == nil || try == worktreeTries || !halfMadeWorktree.MatchString(err.Error()) {
			return out, err
		}
		agentSleep(worktreeRetryDelay)
	}
}

// WorktreeSession 은 새 worktree 세션을 열 자리다.
type WorktreeSession struct {
	// Name 은 세션 이름 후보다(SafeSessionName 을 거침). 겹치면 만드는 쪽이 -2 를 붙인다.
	Name string
	// Dir 은 worktree 폴더(절대 경로)다.
	Dir string
	// Reused 는 그 브랜치의 worktree 가 이미 있어 만들지 않고 그대로 쓴다는 뜻이다.
	Reused bool
}

// worktreeEntry 는 `git worktree list --porcelain` 의 항목 하나다.
type worktreeEntry struct {
	path     string
	branch   string // refs/heads/... — detached 면 빈 값
	bare     bool
	prunable bool
	locked   bool
}

// PrepareWorktree 는 repo 저장소에 branch 의 worktree 를 마련하고 그 자리를 돌려준다.
//
// worktree 는 원본 저장소 **옆**(`<부모>/<저장소명>-<브랜치의 / 를 - 로>`)에 만든다. repo 가 linked worktree 여도
// 원본 옆이다 — 원본은 porcelain 목록의 첫 항목이다.
// 그 브랜치가 이미 linked worktree 에 있으면 만들지 않고 그 폴더를 돌려준다(`claude -w` 가 만든 것도).
// 원본 작업 트리에 체크아웃된 브랜치와, 이미 있는 폴더는 거절한다 — 덮지 않는다.
//
// 브랜치를 고르는 순서: 로컬 브랜치가 있으면 그것, 없고 원격 하나에만 있으면 그 원격을 추적하는 새 브랜치,
// 둘 다 아니면 repo 의 HEAD 에서 새 브랜치.
func PrepareWorktree(repo, branch string) (WorktreeSession, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return WorktreeSession{}, errors.New("브랜치 이름이 비었다")
	}
	dir, err := worktreeRepoDir(repo)
	if err != nil {
		return WorktreeSession{}, err
	}
	// 이름을 먼저 본다 — 저장소를 읽기 전에, 그리고 `-` 로 시작해 git 의 옵션으로 읽힐 이름을 여기서 막는다.
	if _, err := outputWithin(worktreeReadLimit, "git", "check-ref-format", "--branch", branch); err != nil {
		return WorktreeSession{}, fmt.Errorf("브랜치 이름으로 쓸 수 없다: %q", branch)
	}
	out, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return WorktreeSession{}, fmt.Errorf("git 저장소를 읽지 못했다: %w", err)
	}
	trees := parseWorktreeList(string(out))
	if len(trees) == 0 {
		return WorktreeSession{}, fmt.Errorf("git 저장소를 읽지 못했다: worktree 목록이 비었다 (%s)", dir)
	}
	main := trees[0]
	if main.bare {
		return WorktreeSession{}, fmt.Errorf("bare 저장소(%s)는 지원하지 않는다", main.path)
	}
	dirName := filepath.Base(main.path) + "-" + strings.ReplaceAll(branch, "/", "-")
	name := SafeSessionName(dirName)

	ref := "refs/heads/" + branch
	for i, t := range trees {
		if t.branch != ref {
			continue
		}
		if i == 0 {
			return WorktreeSession{}, fmt.Errorf("%s: 원본 작업 트리(%s)에 체크아웃된 브랜치라 worktree 를 만들 수 없다", branch, main.path)
		}
		if t.prunable || !isDir(t.path) {
			return WorktreeSession{}, fmt.Errorf("%s: worktree 폴더(%s)가 사라졌다. git worktree prune 뒤에 다시 하라", branch, t.path)
		}
		return WorktreeSession{Name: name, Dir: t.path, Reused: true}, nil
	}

	path := filepath.Join(filepath.Dir(main.path), dirName)
	if _, err := os.Lstat(path); err == nil {
		return WorktreeSession{}, fmt.Errorf("이미 있는 폴더라 덮지 않는다: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return WorktreeSession{}, fmt.Errorf("폴더를 확인하지 못했다 (%s): %w", path, err)
	}

	add, err := worktreeAddArgs(dir, branch, path)
	if err != nil {
		return WorktreeSession{}, err
	}
	if _, err := outputWithin(worktreeAddLimit, "git", add...); err != nil {
		return WorktreeSession{}, fmt.Errorf("git worktree add: %w", err)
	}
	return WorktreeSession{Name: name, Dir: path}, nil
}

// worktreeAddArgs 는 브랜치가 어디에 있느냐에 따라 `git worktree add` 의 인자를 고른다.
//
// 원격 브랜치는 git 의 DWIM 에 맡기지 않고 `--track -b` 로 적는다 — DWIM 은 원격 설정이 있어야 돌고(실측: 설정 없는
// 원격 ref 는 «잘못된 레퍼런스»), 어떤 원격을 골랐는지가 명령줄에 보이지 않는다.
func worktreeAddArgs(dir, branch, path string) ([]string, error) {
	local := "refs/heads/" + branch
	out, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "for-each-ref", "--format=%(refname)", local, "refs/remotes/")
	if err != nil {
		return nil, fmt.Errorf("브랜치 목록을 읽지 못했다: %w", err)
	}
	// for-each-ref 의 패턴은 앞부분 일치라 refs/heads/feat 이 refs/heads/feat/x 도 맞힌다 — 그래서 정확히 비교한다.
	refs := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			refs[line] = true
		}
	}
	base := []string{"-C", dir, "worktree", "add"}
	if refs[local] {
		return append(base, path, branch), nil
	}
	// 원격 이름은 `git remote` 로 받는다. refs/remotes/*/<b> 로 찾으면 * 가 / 를 넘어 origin/feat/x 가 x 에 맞는다(실측).
	remotesOut, err := outputWithin(worktreeReadLimit, "git", "-C", dir, "remote")
	if err != nil {
		return nil, fmt.Errorf("원격 목록을 읽지 못했다: %w", err)
	}
	var found []string
	for _, r := range strings.Fields(string(remotesOut)) {
		if refs["refs/remotes/"+r+"/"+branch] {
			found = append(found, r+"/"+branch)
		}
	}
	if len(found) == 1 {
		return append(base, "--track", "-b", branch, path, found[0]), nil
	}
	// 원격 여럿에 있으면 고르지 않는다 — HEAD 에서 새로 만든다. git 의 DWIM 도 이때는 고르지 않는다.
	return append(base, "-b", branch, path), nil
}

// parseWorktreeList 는 `git worktree list --porcelain -z` 를 읽는다. 줄은 NUL 로 끝나고, 빈 줄이 항목을 가른다.
func parseWorktreeList(out string) []worktreeEntry {
	var trees []worktreeEntry
	var cur *worktreeEntry
	for _, line := range strings.Split(out, "\x00") {
		switch {
		case line == "":
			cur = nil
		case strings.HasPrefix(line, "worktree "):
			trees = append(trees, worktreeEntry{path: strings.TrimPrefix(line, "worktree ")})
			cur = &trees[len(trees)-1]
		case cur == nil:
			// worktree 줄 앞에 온 줄은 모양을 모르는 것이니 버린다.
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(line, "branch ")
		case line == "bare":
			cur.bare = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.prunable = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.locked = true
		}
	}
	return trees
}

// worktreeRepoDir 는 입력한 저장소 경로를 절대 경로로 만든다. `~` 는 홈으로 푼다.
// 상대 경로는 거절한다 — mux 를 띄운 폴더 기준으로 풀리면 사람이 본 것과 다른 저장소가 된다.
func worktreeRepoDir(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", errors.New("저장소 경로가 비었다")
	}
	if repo == "~" || strings.HasPrefix(repo, "~/") {
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("홈 폴더를 알 수 없다: %w", err)
		}
		repo = filepath.Join(home, strings.TrimPrefix(repo, "~"))
	}
	if !filepath.IsAbs(repo) {
		return "", fmt.Errorf("저장소는 절대 경로로 적는다: %q", repo)
	}
	return filepath.Clean(repo), nil
}
