package tmux

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// running 은 worktree 와 세션과 Claude 가 다 있는 작업이다.
func (w *taskWorld) running(status string) {
	w.watch(w.main)
	w.addTree(w.worktree("m1"), "refs/heads/t-m1")
	w.sessions["t-m1"] = w.worktree("m1")
	w.writeClaude("t-m1", "conv", w.worktree("m1"), status)
}

func (w *taskWorld) done(force bool) (TaskDone, error) {
	return FinishTask(TaskDoneRequest{ID: "m1", Force: force})
}

func TestDoneClosesTheSessionThenRemovesTheWorktree(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		script := filepath.Join(t.TempDir(), "sa#ve.sh")
		os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
		w.options["@resurrect-save-script-path"] = script + "\n"
		res, err := w.done(false)
		if err != nil || len(res.Notes) != 0 {
			t.Fatalf("res = %+v, %v", res, err)
		}
		want := []string{
			"tmux kill-session -t =t-m1",
			"git -C " + w.main + " -c status.showUntrackedFiles=normal worktree remove " + w.worktree("m1"),
			"tmux run-shell '" + strings.ReplaceAll(script, "#", "##") + "' quiet", // run-shell 은 포맷을 펼친다
		}
		if got := w.mutated(); !slices.Equal(got, want) {
			t.Errorf("한 일:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		// 브랜치는 지우지 않는다.
		for _, c := range w.calls {
			if strings.Contains(c, "git") && strings.Contains(c, " branch ") {
				t.Errorf("브랜치를 건드렸다: %q", c)
			}
		}
	})
}

// --force 로도 못 넘는 것과 넘는 것.
func TestDoneRefusals(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(w *taskWorld)
		want     error
		forceToo bool // --force 여도 거절한다
	}{
		{"작업 중", func(w *taskWorld) { w.running("busy") }, ErrTaskRefused, true},
		{"백그라운드 셸", func(w *taskWorld) { w.running("shell") }, ErrTaskRefused, true},
		{"모르는 상태", func(w *taskWorld) { w.running("compacting") }, ErrTaskRefused, true},
		{"승인 대기", func(w *taskWorld) { w.running("waiting") }, ErrAgentBlocked, true},
		{"커밋 안 된 변경", func(w *taskWorld) {
			w.running("idle")
			w.dirty[w.worktree("m1")] = "?? new.txt\n"
		}, ErrTaskRefused, true},
		{"아무도 가리키지 않는 커밋", func(w *taskWorld) {
			w.running("idle")
			w.trees[w.worktree("m1")] = ""
			w.contains = ""
		}, ErrTaskRefused, true},
		{"푸시 안 된 커밋", func(w *taskWorld) {
			w.running("idle")
			w.unpushed = "2\n"
		}, ErrTaskUnpushed, false},
		{"셀 수 없는 답", func(w *taskWorld) {
			w.running("idle")
			w.unpushed = "?\n"
		}, nil, false},
		{"worktree 폴더의 다른 Claude 가 일한다", func(w *taskWorld) {
			w.running("idle")
			b, _ := json.Marshal(map[string]any{"pid": 777, "sessionId": "s", "cwd": w.worktree("m1") + "/sub", "procStart": "1", "status": "busy"})
			os.WriteFile(w.claudeFile(777), b, 0o600)
		}, ErrTaskRefused, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				tc.setup(w)
				_, err := w.done(false)
				if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
					t.Errorf("err = %v, want %v", err, tc.want)
				}
				if m := w.mutated(); len(m) != 0 {
					t.Fatalf("거절했는데 무언가 했다: %q", m)
				}
				_, err = w.done(true)
				if tc.forceToo && !errors.Is(err, tc.want) {
					t.Errorf("--force: err = %v, want %v", err, tc.want)
				}
				if !tc.forceToo && err != nil {
					t.Errorf("--force: err = %v", err)
				}
				if m := w.mutated(); tc.forceToo && len(m) != 0 {
					t.Errorf("--force 로 거절했는데 무언가 했다: %q", m)
				}
			})
		})
	}
}

// 이름이 앞부분만 같은 이웃 작업의 Claude 는 이 작업을 막지 않는다.
func TestDoneIgnoresTheNeighbourWithALongerID(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		b, _ := json.Marshal(map[string]any{"pid": 777, "sessionId": "s", "cwd": w.worktree("m12"), "procStart": "1", "status": "busy"})
		os.WriteFile(w.claudeFile(777), b, 0o600)
		if _, err := w.done(false); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUnderDir(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/p/repo-t-m1", "/p/repo-t-m1", true},
		{"/p/repo-t-m1/a/b", "/p/repo-t-m1", true},
		{"/p/repo-t-m12", "/p/repo-t-m1", false},
		{"/p", "/p/repo-t-m1", false},
		{"/p/repo-t-m1/..x", "/p/repo-t-m1", true},
		{"", "/p/repo-t-m1", false},
	}
	for _, tc := range cases {
		if got := underDir(tc.path, tc.dir); got != tc.want {
			t.Errorf("underDir(%q, %q) = %v", tc.path, tc.dir, got)
		}
	}
}

// 자기 세션 안에서는 닫지 않는다. 다른 세션의 pane 은 괜찮다.
func TestDoneRefusesFromInsideItsOwnSession(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		if _, err := FinishTask(TaskDoneRequest{ID: "m1", Pane: "%9", Force: true}); !errors.Is(err, ErrTaskRefused) {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Fatalf("무언가 했다: %q", m)
		}
		if _, err := FinishTask(TaskDoneRequest{ID: "m1", Pane: "%8"}); err != nil {
			t.Errorf("다른 세션에서: %v", err)
		}
	})
}

// 세션을 닫는 사이 파일이 생기면 worktree 를 지우지 않는다.
func TestDoneKeepsAWorktreeThatGotDirtyWhileClosing(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.onKill = func() { w.dirty[w.worktree("m1")] = " M a.txt\n" }
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) {
			t.Errorf("err = %v", err)
		}
		if !isDir(w.worktree("m1")) || w.called("git -C "+w.main+" -c") != 0 {
			t.Error("worktree 를 지웠다")
		}
	})
}

func TestDoneStopsWhenTheSessionWillNotClose(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.killErr = errors.New("exit status 1")
		if _, err := w.done(true); err == nil {
			t.Error("닫지 못했는데 성공이다")
		}
		if !isDir(w.worktree("m1")) {
			t.Error("세션이 살아 있는데 worktree 를 지웠다")
		}
	})
}

// 세션만, worktree 만 있어도 정리한다. 둘 다 없으면 그런 작업이 없다.
func TestDoneHandlesHalfATask(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		if _, err := w.done(false); !errors.Is(err, ErrNoTask) {
			t.Errorf("err = %v", err)
		}
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		if _, err := w.done(false); err != nil || isDir(w.worktree("m1")) || w.called("tmux kill-session") != 0 {
			t.Errorf("worktree 만: %v, %q", err, w.mutated())
		}
		w.sessions["t-m1"] = w.worktree("m1") // 이미 지운 작업이 복구로 되살아난 모양: 폴더는 없다
		if _, err := w.done(false); err != nil || len(w.sessions) != 0 {
			t.Errorf("세션만: %v, %v", err, w.sessions)
		}
	})
}

// 서버가 없어도 worktree 는 정리한다. 답하지 못하는 서버면 손대지 않는다.
func TestDoneWithoutAServer(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		w.listErr = errors.New("exit status 1: protocol version mismatch (client 8, server 7)")
		if _, err := w.done(true); err == nil || !isDir(w.worktree("m1")) {
			t.Errorf("답하지 못하는 서버인데 지웠다: %v", err)
		}
		w.listErr = errors.New("exit status 1: no server running on /tmp/x")
		if _, err := w.done(false); err != nil || isDir(w.worktree("m1")) {
			t.Errorf("서버 없음: %v", err)
		}
	})
}

// 원격이 있으면 원격에 없는 커밋을, 없으면 다른 로컬 브랜치에 없는 커밋을 센다.
func TestUnpushedCommitsWithoutARemote(t *testing.T) {
	cases := []struct{ remotes, branch, want string }{
		{"origin\n", "t-m1", "HEAD --not --remotes"},
		{"", "t-m1", "HEAD --not --exclude=t-m1 --branches"},
		{"", "", "HEAD --not --branches"},
	}
	for _, tc := range cases {
		withTaskWorld(t, func(w *taskWorld) {
			w.remotes = tc.remotes
			if _, err := unpushedCommits(Task{Worktree: w.worktree("m1"), Branch: tc.branch}); err != nil {
				t.Fatal(err)
			}
			if w.called("git -C "+w.worktree("m1")+" rev-list --count "+tc.want) != 1 {
				t.Errorf("remotes=%q branch=%q: %q", tc.remotes, tc.branch, w.calls)
			}
		})
	}
}

// 상태 파일은 제자리에 덮어써서 반쯤 쓰인 채 읽힐 때가 있다. 그 순간의 «Claude 없음» 을 믿고 닫으면 안 된다.
func TestDoneReadsAgainWhenTheStateFileIsTorn(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("busy")
		whole, _ := os.ReadFile(w.claudeFile(taskPID))
		os.WriteFile(w.claudeFile(taskPID), whole[:len(whole)/2], 0o600)
		sleep := agentSleep
		agentSleep = func(d time.Duration) {
			sleep(d)
			os.WriteFile(w.claudeFile(taskPID), whole, 0o600)
		}
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("일하는 Claude 를 닫았다: %q", m)
		}
	})
}

// 세션은 있는데 pane 목록을 못 읽으면 Claude 가 없는 것이 아니라 모르는 것이다.
func TestDoneStopsWhenThePanesCannotBeRead(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.sessions["t-m1"] = "/nowhere"
		w.panesErr = errors.New("exit status 1: server busy")
		if _, err := w.done(true); err == nil {
			t.Error("pane 을 못 읽었는데 성공이다")
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// 이름만 같고 폴더가 다른 세션은 사용자의 것이다 — 닫지 않는다.
func TestDoneLeavesASessionThatIsNotTheTasks(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.stopClaude()
		w.sessions["t-m1"] = w.worktree("m12")
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
		// worktree 의 하위 폴더에서 연 세션은 이 작업의 것이다.
		w.sessions["t-m1"] = w.worktree("m1") + "/sub"
		if _, err := w.done(false); err != nil {
			t.Errorf("하위 폴더: %v", err)
		}
	})
}

// 잠긴 worktree 는 git 이 지우기를 거절한다. 세션을 닫은 뒤에 알면 늦다.
func TestDoneRefusesALockedWorktreeBeforeClosing(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.locked = w.worktree("m1")
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) || !strings.Contains(err.Error(), "unlock") {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// 같은 id 가 두 저장소에 있으면 고르지 않는다. --repo 로 고르면 그것만 지운다.
func TestDoneDoesNotGuessBetweenTwoRepos(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		other := filepath.Join(t.TempDir(), "other")
		otherTree := other + "-t-m1"
		os.MkdirAll(otherTree, 0o755)
		SetRunner(&otherRepo{taskWorld: w, other: other, trees: []string{otherTree}})

		_, err := FinishTask(TaskDoneRequest{ID: "m1", CWD: other, Force: true})
		if err == nil || !strings.Contains(err.Error(), w.main) || !strings.Contains(err.Error(), other) {
			t.Errorf("err = %v, want both repos", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Fatalf("고르지 말아야 하는데 %q", m)
		}
		res, err := FinishTask(TaskDoneRequest{ID: "m1", Repo: w.main, CWD: other})
		if err != nil || res.Task.Repo != w.main || isDir(w.worktree("m1")) || !isDir(otherTree) {
			t.Errorf("--repo: %+v, %v", res.Task, err)
		}
	})
}

// 세션 밖에서 이 worktree 를 열고 있는 Claude 가 있으면 idle 이어도 지우지 않는다 — 발밑의 폴더가 사라진다. 다른 tmux
// 서버의 작업 세션이나 herdr 의 Claude 가 이 모양이다.
func TestDoneRefusesWhileAnotherClaudeHoldsTheFolder(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		b, _ := json.Marshal(map[string]any{"pid": 777, "sessionId": "s", "cwd": w.worktree("m1"), "procStart": "1", "status": "idle"})
		os.WriteFile(w.claudeFile(777), b, 0o600)
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) || !strings.Contains(err.Error(), "777") {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// closing 은 세션은 이미 없고 그 세션의 Claude 만 아직 살아 있는 작업이다 — 사람이 방금 닫았다.
func (w *taskWorld) closing(status string) {
	w.watch(w.main)
	w.addTree(w.worktree("m1"), "refs/heads/t-m1")
	w.writeClaude("t-m1", "conv", w.worktree("m1"), status)
	taskExitTimeout = 10 * time.Second
}

// 세션을 막 닫고 부른 done 은 그 Claude 가 끝나기를 기다렸다가 지운다. 닫힐 때 무엇을 하던 중이었는지는 따지지 않는다.
func TestDoneWaitsForTheClaudeOfAJustClosedSession(t *testing.T) {
	for _, status := range []string{"idle", "busy", "waiting"} {
		t.Run(status, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				w.closing(status)
				begin, sleep := agentNow(), agentSleep
				agentSleep = func(d time.Duration) {
					if sleep(d); agentNow().Sub(begin) >= 3*time.Second {
						w.stopClaude()
					}
				}
				removed := 0
				w.onGit = func(k string) {
					if !strings.Contains(k, "worktree remove") {
						return
					}
					if removed++; isAlive(taskPID, "") {
						t.Error("Claude 가 살아 있는데 worktree 를 지웠다")
					}
				}
				if _, err := w.done(false); err != nil || removed != 1 {
					t.Errorf("err = %v, 지운 횟수 = %d", err, removed)
				}
				if took := agentNow().Sub(begin); took < 3*time.Second || took > 4*time.Second {
					t.Errorf("기다린 시간 = %v", took)
				}
			})
		})
	}
}

// 끝나지 않으면 거절하고, 방금 닫은 세션이면 다시 해 보라고 알린다.
func TestDoneGivesUpOnAClaudeThatStays(t *testing.T) {
	for status, want := range map[string]error{"idle": ErrTaskRefused, "busy": ErrTaskRefused, "waiting": ErrAgentBlocked} {
		t.Run(status, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				w.closing(status)
				begin := agentNow()
				_, err := w.done(true)
				if !errors.Is(err, want) || !strings.Contains(err.Error(), "방금 닫은 세션") {
					t.Errorf("err = %v, want %v", err, want)
				}
				if took := agentNow().Sub(begin); took < taskExitTimeout || took > taskExitTimeout+time.Second {
					t.Errorf("기다린 시간 = %v", took)
				}
				if m := w.mutated(); len(m) != 0 {
					t.Errorf("무언가 했다: %q", m)
				}
			})
		})
	}
}

// 끝나는 중이 아닌 것이 분명하면 기다리지 않는다: pane 에 사는 Claude(리뷰어·다른 세션)와, 작업 세션이 아직 있을 때.
func TestDoneDoesNotWaitForAClaudeThatIsNotClosing(t *testing.T) {
	cases := map[string]func(w *taskWorld){
		"리뷰어": func(w *taskWorld) {
			w.closing("idle")
			w.sessions["r-m1"], w.paneSession = w.worktree("m1"), "r-m1"
			w.writeClaude("r-m1", "rev", w.worktree("m1"), "idle")
		},
		"작업 세션이 있다(Claude 는 꺼졌다)": func(w *taskWorld) {
			w.running("idle")
			w.stopClaude()
			taskExitTimeout = 10 * time.Second
			b, _ := json.Marshal(map[string]any{"pid": 777, "sessionId": "s", "cwd": w.worktree("m1"), "procStart": "1", "status": "idle"})
			os.WriteFile(w.claudeFile(777), b, 0o600)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				setup(w)
				begin := agentNow()
				if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) || strings.Contains(err.Error(), "방금 닫은 세션") {
					t.Errorf("err = %v", err)
				}
				if took := agentNow().Sub(begin); took > time.Second {
					t.Errorf("기다렸다: %v", took)
				}
			})
		})
	}
}

// 기다리는 사이 누가 그 작업을 다시 열었으면 닫지 않는다 — 없던 세션이라 닫아도 되는지 물은 적이 없다.
func TestDoneLeavesASessionThatOpenedWhileWaiting(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.closing("idle")
		sleep := agentSleep
		agentSleep = func(d time.Duration) {
			sleep(d)
			w.sessions["t-m1"] = w.worktree("m1")
			w.stopClaude()
		}
		if _, err := w.done(true); err == nil || !strings.Contains(err.Error(), "다시 하라") {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// 리뷰어가 열고 있으면 거절하되, 어떤 상태든 무엇을 닫아야 하는지 말한다. 막혀 있으면 워커와 같이 3 이다.
func TestDoneNamesTheReviewerHoldingTheFolder(t *testing.T) {
	for status, want := range map[string]error{
		"idle": ErrTaskRefused, "busy": ErrTaskRefused, "shell": ErrTaskRefused, "compacting": ErrTaskRefused, "waiting": ErrAgentBlocked,
	} {
		t.Run(status, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				w.watch(w.main)
				w.addTree(w.worktree("m1"), "refs/heads/t-m1")
				w.sessions["r-m1"] = w.worktree("m1")
				w.paneSession = "r-m1"
				w.writeClaude("r-m1", "rev", w.worktree("m1"), status)
				_, err := w.done(true)
				if !errors.Is(err, want) || !strings.Contains(err.Error(), "리뷰어 세션 r-m1") || !strings.Contains(err.Error(), "kill-session -t =r-m1") {
					t.Errorf("err = %v, want %v", err, want)
				}
				if m := w.mutated(); len(m) != 0 {
					t.Errorf("무언가 했다: %q", m)
				}
			})
		})
	}
}

// worktree 가 안 보이는 세션은 «이미 지운 작업이 되살아난 것» 일 때만 닫는다. 저장소를 못 읽은 것과 사람의 세션은 닫지
// 않는다 — 커밋 안 된 변경을 보지 않은 채 닫게 된다.
func TestDoneClosesALoneSessionOnlyWhenItsFolderIsGone(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *taskWorld)
		ok    bool
	}{
		{"지운 작업이 되살아났다", func(w *taskWorld) { w.sessions["t-m1"] = w.worktree("m1") }, true},
		{"폴더는 있는데 저장소를 못 읽는다", func(w *taskWorld) {
			os.MkdirAll(w.worktree("m1"), 0o755)
			w.sessions["t-m1"] = w.worktree("m1")
			w.gitErr = errors.New("git did not answer within 5s")
		}, false},
		{"폴더는 있는데 등록된 worktree 가 아니다", func(w *taskWorld) {
			os.MkdirAll(w.worktree("m1"), 0o755)
			w.sessions["t-m1"] = w.worktree("m1")
		}, false},
		{"설정의 저장소를 못 읽는다", func(w *taskWorld) {
			w.watch("/not/a/repo")
			w.sessions["t-m1"] = w.worktree("m1")
		}, false},
		{"하위 폴더에서 열린 세션이 되살아났다", func(w *taskWorld) { w.sessions["t-m1"] = w.worktree("m1") + "/sub/dir" }, true},
		{"하위 폴더만 사라졌다", func(w *taskWorld) {
			os.MkdirAll(w.worktree("m1"), 0o755)
			w.sessions["t-m1"] = w.worktree("m1") + "/sub"
		}, false},
		{"이름이 더 긴 작업의 자리", func(w *taskWorld) { w.sessions["t-m1"] = w.worktree("m12") }, false},
		{"사람이 만든 세션", func(w *taskWorld) { w.sessions["t-m1"] = w.home }, false},
		{"폴더를 모르는 세션", func(w *taskWorld) { w.sessions["t-m1"] = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				tc.setup(w)
				_, err := w.done(true)
				if (err == nil) != tc.ok || (w.called("tmux kill-session") == 1) != tc.ok {
					t.Errorf("err = %v, kill %d번, want ok=%v", err, w.called("tmux kill-session"), tc.ok)
				}
			})
		})
	}
}

// 확인하는 동안 Claude 가 일을 시작하면 닫지 않는다.
func TestDoneLooksAgainRightBeforeClosing(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.onGit = func(k string) {
			if strings.HasPrefix(k, "status") {
				w.writeClaude("t-m1", "conv", w.worktree("m1"), "busy")
			}
		}
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// 다시 본 작업이 처음 확인한 작업이 아니면 닫지 않는다 — 확인하지 않은 worktree 를 지우게 된다.
func TestDoneStopsWhenTheTaskChangedUnderneath(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		other := filepath.Join(t.TempDir(), "other")
		otherTree := other + "-t-m1"
		os.MkdirAll(otherTree, 0o755)
		os.MkdirAll(other, 0o755)
		r := &otherRepo{taskWorld: w, other: other}
		SetRunner(r)
		w.onGit = func(k string) {
			if strings.HasPrefix(k, "status") { // 첫 확인 뒤에 저장소 사정이 바뀐다
				delete(w.trees, w.worktree("m1"))
				r.trees = []string{otherTree}
			}
		}
		if _, err := FinishTask(TaskDoneRequest{ID: "m1", CWD: other, Force: true}); err == nil || !strings.Contains(err.Error(), "바뀌었다") {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 했다: %q", m)
		}
	})
}

// 닫는 사이 detached 로 커밋했으면 그 커밋을 지키려고 worktree 를 남긴다.
func TestDoneKeepsAWorktreeThatGotAFloatingCommitWhileClosing(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.onKill = func() { w.contains = "" }
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) || !isDir(w.worktree("m1")) {
			t.Errorf("err = %v, 남음=%v", err, isDir(w.worktree("m1")))
		}
	})
}

// 세션을 닫았는데 Claude 가 아직 끝나지 않았으면 그 밑의 폴더를 지우지 않는다.
func TestDoneKeepsTheWorktreeUntilClaudeIsGone(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.survives = true
		if _, err := w.done(true); !errors.Is(err, ErrTaskRefused) || !isDir(w.worktree("m1")) {
			t.Errorf("err = %v, 남음=%v", err, isDir(w.worktree("m1")))
		}
	})
}

// 저장은 사용자의 저장 훅과 같은 잠금을 쥐고, 끝난 뒤 1초를 더 쥔다. 저장본 이름이 초 단위라 같은 초의 둘째 저장은
// 첫째의 파일을 지운다.
func TestSaveHoldsTheSaveLockAndASecondMore(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		script := filepath.Join(t.TempDir(), "save.sh")
		os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
		w.options["@resurrect-save-script-path"] = script
		lock := filepath.Join(strings.TrimSpace(w.options["@resurrect-dir"]), ".save.lock")

		begin := agentNow()
		sleep, held := agentSleep, false
		agentSleep = func(d time.Duration) {
			held = held || (w.called("tmux run-shell") == 1 && !flockFree(t, lock))
			sleep(d)
		}
		if err := saveSessionsNow(); err != nil {
			t.Fatal(err)
		}
		if !held || agentNow().Sub(begin) < time.Second {
			t.Errorf("저장 뒤 잠금을 쥐고 있지 않았다(held=%v, %v)", held, agentNow().Sub(begin))
		}
		if !flockFree(t, lock) {
			t.Error("잠금을 풀지 않았다")
		}

		// 다른 저장이 쥐고 있으면 기다리다 그만둔다.
		unlock, err := flockWait(lock, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		if err := saveSessionsNow(); err == nil || w.called("tmux run-shell") != 1 {
			t.Errorf("쥐고 있는데 저장했다: %v", err)
		}
	})
}

func flockFree(t *testing.T, path string) bool {
	t.Helper()
	unlock, err := flockWait(path, 0)
	if err != nil {
		return false
	}
	unlock()
	return true
}

// 서버가 뜬 지 30초 안에는 저장하지 않는다 — 복원 중인 일부 목록으로 저장본을 덮는다.
func TestSaveWaitsOutTheRestore(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		script := filepath.Join(t.TempDir(), "save.sh")
		os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
		w.options["@resurrect-save-script-path"] = script
		for _, start := range []string{strconv.FormatInt(agentNow().Unix()-29, 10), "?", ""} {
			w.startTime = start
			if err := saveSessionsNow(); err == nil || w.called("tmux run-shell") != 0 {
				t.Errorf("start_time %q: %v", start, err)
			}
		}
		w.startTime = strconv.FormatInt(agentNow().Unix()-31, 10)
		if err := saveSessionsNow(); err != nil {
			t.Errorf("31초: %v", err)
		}
	})
}

// 저장을 못 해도 정리는 성공이다 — 알리기만 한다.
func TestDoneOnlyWarnsWhenTheSaveFails(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		w.options["@resurrect-save-script-path"] = "/not/there/save.sh"
		res, err := w.done(false)
		if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "저장") {
			t.Errorf("res = %+v, %v", res, err)
		}
	})
	// resurrect 를 쓰지 않으면 할 말이 없고, 서버가 없어 부르지 못한 것은 말한다.
	withTaskWorld(t, func(w *taskWorld) {
		w.running("idle")
		if res, err := w.done(false); err != nil || len(res.Notes) != 0 {
			t.Errorf("resurrect 없음: %+v, %v", res, err)
		}
	})
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		w.listErr = errors.New("exit status 1: no server running on /tmp/x")
		if res, err := w.done(false); err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "저장") {
			t.Errorf("서버 없음: %+v, %v", res, err)
		}
	})
}
