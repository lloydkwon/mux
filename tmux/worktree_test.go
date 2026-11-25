package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wtList 는 `git worktree list --porcelain -z` 의 출력을 만든다. 항목마다 줄을 NUL 로 끝내고, 빈 줄로 가른다.
func wtList(entries ...[]string) []byte {
	var b strings.Builder
	for _, e := range entries {
		for _, line := range e {
			b.WriteString(line + "\x00")
		}
		b.WriteString("\x00")
	}
	return []byte(b.String())
}

// worktreeFixture 는 원본 저장소 폴더 하나와 그 목 응답을 준비한다. 경로 확인이 진짜 디스크를 보므로 폴더는 실제로 만든다.
type worktreeFixture struct {
	root, repo string
}

func newWorktreeFixture(t *testing.T) worktreeFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "mux")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	return worktreeFixture{root: root, repo: repo}
}

// expect 는 이름 확인과 worktree 목록을 등록한다. dir 은 `-C` 로 줄 폴더다.
func (f worktreeFixture) expect(m *mockRunner, dir, branch string, list []byte) {
	m.OnOutput([]byte(branch+"\n"), nil, "git", "check-ref-format", "--branch", branch)
	m.OnOutput(list, nil, "git", "-C", dir, "worktree", "list", "--porcelain", "-z")
}

// expectRefs 는 브랜치 목록과 원격 목록을 등록한다.
func (f worktreeFixture) expectRefs(m *mockRunner, dir, branch, refs, remotes string) {
	m.OnOutput([]byte(refs), nil, "git", "-C", dir, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch, "refs/remotes/")
	m.OnOutput([]byte(remotes), nil, "git", "-C", dir, "remote")
}

func (f worktreeFixture) mainOnly() []byte {
	return wtList([]string{"worktree " + f.repo, "HEAD abc", "branch refs/heads/main"})
}

func addKey(args ...string) string {
	return "git " + strings.Join(args, " ")
}

func calledWith(m *mockRunner, prefix string) bool {
	return slices.ContainsFunc(m.gets, func(k string) bool { return strings.HasPrefix(k, prefix) })
}

// 브랜치 위치에 따라 `git worktree add` 의 인자가 갈린다 — 새 브랜치는 -b, 로컬은 그대로, 원격 하나는 --track.
func TestPrepareWorktreePicksTheBranchSource(t *testing.T) {
	cases := []struct {
		name    string
		branch  string
		refs    string
		remotes string
		want    func(repo, path string) []string
	}{
		{
			name:   "no such branch anywhere: new branch from HEAD",
			branch: "feat/x",
			refs:   "refs/remotes/origin/main\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", "-b", "feat/x", path}
			},
		},
		{
			name:   "local branch: check it out",
			branch: "feat/x",
			refs:   "refs/heads/feat/x\nrefs/remotes/origin/feat/x\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", path, "feat/x"}
			},
		},
		{
			name:    "on exactly one remote: track it",
			branch:  "feat/x",
			refs:    "refs/remotes/origin/feat/x\nrefs/remotes/upstream/main\n",
			remotes: "origin\nupstream\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", "--track", "-b", "feat/x", path, "origin/feat/x"}
			},
		},
		{
			name:    "on two remotes: pick neither",
			branch:  "feat/x",
			refs:    "refs/remotes/origin/feat/x\nrefs/remotes/upstream/feat/x\n",
			remotes: "origin\nupstream\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", "-b", "feat/x", path}
			},
		},
		{
			// refs/remotes/*/x 로 찾았다면 origin/feat/x 가 x 에 맞았을 것이다.
			name:    "a remote branch that merely ends in the name is not it",
			branch:  "x",
			refs:    "refs/remotes/origin/feat/x\n",
			remotes: "origin\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", "-b", "x", path}
			},
		},
		{
			// for-each-ref 는 앞부분이 맞으면 내놓는다 — refs/heads/feat 으로 refs/heads/feat/x 가 온다.
			name:    "a local branch under the name is not it",
			branch:  "feat",
			refs:    "refs/heads/feat/x\n",
			remotes: "origin\n",
			want: func(repo, path string) []string {
				return []string{"-C", repo, "worktree", "add", "-b", "feat", path}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				f := newWorktreeFixture(t)
				f.expect(m, f.repo, tc.branch, f.mainOnly())
				f.expectRefs(m, f.repo, tc.branch, tc.refs, tc.remotes)
				path := filepath.Join(f.root, "mux-"+strings.ReplaceAll(tc.branch, "/", "-"))
				add := tc.want(f.repo, path)
				m.OnOutput(nil, nil, "git", add...)

				got, err := PrepareWorktree(f.repo, tc.branch)
				if err != nil {
					t.Fatalf("PrepareWorktree: %v", err)
				}
				if got.Dir != path || got.Reused {
					t.Errorf("got %+v, want a new worktree at %s", got, path)
				}
				if !slices.Contains(m.gets, addKey(add...)) {
					t.Errorf("worktree add not called as %q; calls: %q", addKey(add...), m.gets)
				}
			})
		})
	}
}

// 이름은 저장소 이름 + 브랜치(/ 는 -). 세션 이름은 tmux 가 뜻을 주는 . 도 바꾼다.
func TestPrepareWorktreeNamesTheFolderAndTheSession(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		f := newWorktreeFixture(t)
		f.expect(m, f.repo, "feat/v1.2", f.mainOnly())
		f.expectRefs(m, f.repo, "feat/v1.2", "", "")
		path := filepath.Join(f.root, "mux-feat-v1.2")
		m.OnOutput(nil, nil, "git", "-C", f.repo, "worktree", "add", "-b", "feat/v1.2", path)

		got, err := PrepareWorktree(f.repo, " feat/v1.2 ")
		if err != nil {
			t.Fatalf("PrepareWorktree: %v", err)
		}
		if got.Dir != path {
			t.Errorf("Dir = %q, want %q", got.Dir, path)
		}
		if got.Name != "mux-feat-v1-2" {
			t.Errorf("Name = %q, want mux-feat-v1-2", got.Name)
		}
	})
}

// repo 가 linked worktree 여도 새 worktree 는 원본 옆이다. 시작점은 그 worktree 의 HEAD 라 -C 는 그 폴더다.
func TestPrepareWorktreeFromALinkedWorktreeGoesBesideTheOriginal(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		f := newWorktreeFixture(t)
		linked := filepath.Join(f.root, "mux-feat-a")
		if err := os.Mkdir(linked, 0o755); err != nil {
			t.Fatal(err)
		}
		list := wtList(
			[]string{"worktree " + f.repo, "HEAD abc", "branch refs/heads/main"},
			[]string{"worktree " + linked, "HEAD def", "branch refs/heads/feat/a"},
		)
		f.expect(m, linked, "feat/b", list)
		f.expectRefs(m, linked, "feat/b", "", "")
		path := filepath.Join(f.root, "mux-feat-b")
		m.OnOutput(nil, nil, "git", "-C", linked, "worktree", "add", "-b", "feat/b", path)

		got, err := PrepareWorktree(linked, "feat/b")
		if err != nil {
			t.Fatalf("PrepareWorktree: %v", err)
		}
		if got.Dir != path || got.Name != "mux-feat-b" {
			t.Errorf("got %+v, want %s named mux-feat-b", got, path)
		}
	})
}

// 그 브랜치의 worktree 가 이미 있으면(claude -w 가 만든 것도) 만들지 않고 그 폴더로 간다.
func TestPrepareWorktreeReusesTheBranchsWorktree(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		f := newWorktreeFixture(t)
		existing := filepath.Join(f.repo, ".claude", "worktrees", "x")
		if err := os.MkdirAll(existing, 0o755); err != nil {
			t.Fatal(err)
		}
		list := wtList(
			[]string{"worktree " + f.repo, "HEAD abc", "branch refs/heads/main"},
			[]string{"worktree " + existing, "HEAD def", "branch refs/heads/feat/x"},
		)
		f.expect(m, f.repo, "feat/x", list)

		got, err := PrepareWorktree(f.repo, "feat/x")
		if err != nil {
			t.Fatalf("PrepareWorktree: %v", err)
		}
		if got.Dir != existing || !got.Reused || got.Name != "mux-feat-x" {
			t.Errorf("got %+v, want the existing worktree %s reused as mux-feat-x", got, existing)
		}
		if calledWith(m, "git -C "+f.repo+" worktree add") || calledWith(m, "git -C "+f.repo+" for-each-ref") {
			t.Errorf("reusing must not create anything; calls: %q", m.gets)
		}
	})
}

// 거절하는 경우 — 무엇도 만들지 않는다.
func TestPrepareWorktreeRefuses(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, m *mockRunner, f worktreeFixture) (repo, branch string)
		want  string
	}{
		{
			name: "the branch the original checkout is on",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				f.expect(m, f.repo, "main", f.mainOnly())
				return f.repo, "main"
			},
			want: "원본 작업 트리",
		},
		{
			name: "a worktree whose folder is gone",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				gone := filepath.Join(f.root, "mux-feat-x")
				f.expect(m, f.repo, "feat/x", wtList(
					[]string{"worktree " + f.repo, "HEAD abc", "branch refs/heads/main"},
					[]string{"worktree " + gone, "HEAD def", "branch refs/heads/feat/x", "prunable gitdir file points to non-existent location"},
				))
				return f.repo, "feat/x"
			},
			want: "git worktree prune",
		},
		{
			name: "a folder already in the way",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				if err := os.Mkdir(filepath.Join(f.root, "mux-feat-x"), 0o755); err != nil {
					t.Fatal(err)
				}
				f.expect(m, f.repo, "feat/x", f.mainOnly())
				return f.repo, "feat/x"
			},
			want: "이미 있는 폴더",
		},
		{
			name: "a bare repository",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				f.expect(m, f.repo, "feat/x", wtList([]string{"worktree " + f.repo, "bare"}))
				return f.repo, "feat/x"
			},
			want: "bare",
		},
		{
			name: "not a repository",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				m.OnOutput([]byte("feat/x\n"), nil, "git", "check-ref-format", "--branch", "feat/x")
				m.OnOutput(nil, errors.New("exit status 128: fatal: not a git repository"), "git", "-C", f.repo, "worktree", "list", "--porcelain", "-z")
				return f.repo, "feat/x"
			},
			want: "not a git repository",
		},
		{
			name: "a name git will not take",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				m.OnOutput(nil, errors.New("exit status 128"), "git", "check-ref-format", "--branch", "-x")
				return f.repo, "-x"
			},
			want: "브랜치 이름으로 쓸 수 없다",
		},
		{
			name: "git refusing the add",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				f.expect(m, f.repo, "feat/x", f.mainOnly())
				f.expectRefs(m, f.repo, "feat/x", "", "")
				m.OnOutput(nil, errors.New("exit status 128: fatal: boom"), "git", "-C", f.repo, "worktree", "add", "-b", "feat/x", filepath.Join(f.root, "mux-feat-x"))
				return f.repo, "feat/x"
			},
			want: "fatal: boom",
		},
		{
			name: "an empty branch",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				return f.repo, "  "
			},
			want: "브랜치 이름이 비었다",
		},
		{
			name: "an empty repository path",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				return " ", "feat/x"
			},
			want: "저장소 경로가 비었다",
		},
		{
			// mux 를 띄운 폴더 기준으로 풀리면 사람이 본 것과 다른 저장소다.
			name: "a relative repository path",
			setup: func(t *testing.T, m *mockRunner, f worktreeFixture) (string, string) {
				return "mux", "feat/x"
			},
			want: "절대 경로",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				f := newWorktreeFixture(t)
				repo, branch := tc.setup(t, m, f)
				got, err := PrepareWorktree(repo, branch)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("PrepareWorktree = %+v, %v; want an error containing %q", got, err, tc.want)
				}
				if tc.name != "git refusing the add" && calledWith(m, "git -C "+f.repo+" worktree add") {
					t.Errorf("a refusal must not run worktree add; calls: %q", m.gets)
				}
			})
		})
	}
}

// ~ 는 홈으로 푼다 — 입력 칸의 안내 «~/…» 대로 쳐도 된다.
func TestPrepareWorktreeExpandsTilde(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		home := t.TempDir()
		homeDir = func() (string, error) { return home, nil } // withMock 이 끝에 되돌린다
		repo := filepath.Join(home, "mux")
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		f := worktreeFixture{root: home, repo: repo}
		f.expect(m, repo, "feat/x", f.mainOnly())
		f.expectRefs(m, repo, "feat/x", "", "")
		m.OnOutput(nil, nil, "git", "-C", repo, "worktree", "add", "-b", "feat/x", filepath.Join(home, "mux-feat-x"))

		if _, err := PrepareWorktree("~/mux/", "feat/x"); err != nil {
			t.Fatalf("PrepareWorktree: %v", err)
		}
	})
}

func TestParseWorktreeList(t *testing.T) {
	out := wtList(
		[]string{"worktree /r", "HEAD a", "branch refs/heads/main"},
		[]string{"worktree /r-d", "HEAD b", "detached"},
		[]string{"worktree /r-l", "HEAD c", "branch refs/heads/l", "locked reason"},
		[]string{"worktree /r-p", "HEAD d", "branch refs/heads/p", "prunable gitdir file points to non-existent location"},
		[]string{"worktree /b", "bare"},
	)
	got := parseWorktreeList(string(out))
	want := []worktreeEntry{
		{path: "/r", branch: "refs/heads/main"},
		{path: "/r-d"},
		{path: "/r-l", branch: "refs/heads/l", locked: true},
		{path: "/r-p", branch: "refs/heads/p", prunable: true},
		{path: "/b", bare: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseWorktreeList =\n%+v\nwant\n%+v", got, want)
	}
}

// New worktree 의 Repo 칸은 mux 를 띄운 세션에서 온다. 클라이언트가 없으면 고르지 않는다 — 클라이언트 없이
// 물으면 tmux 가 다른 세션을 골라 답할 수 있다.
func TestCurrentSessionName(t *testing.T) {
	const format = "#{client_pid}|#{session_name}"
	for name, tc := range map[string]struct{ out, want string }{
		"클라이언트의 세션":         {"412|mux\n", "mux"},
		"이름에 구분자가 있어도 나머지로": {"412|a|b\n", "a|b"},
		"클라이언트가 없으면 빈 값":    {"|mux\n", ""},
		"pid 가 숫자가 아니면 빈 값": {"none|mux\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte(tc.out), nil, "tmux", "display-message", "-p", format)
				if got := CurrentSessionName(); got != tc.want {
					t.Errorf("CurrentSessionName() = %q, want %q", got, tc.want)
				}
			})
		})
	}
	withMock(t, func(m *mockRunner) {
		if got := CurrentSessionName(); got != "" {
			t.Errorf("CurrentSessionName() = %q on a failed lookup, want empty", got)
		}
	})
}
