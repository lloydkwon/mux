package tmux

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func (w *taskWorld) start(prompt string) (TaskStarted, error) {
	return StartTask(TaskStartRequest{ID: "m1", Repo: w.main, Prompt: prompt})
}

// 새 작업: worktree → 씨앗 → 세션 → 프롬프트 한 번.
func TestStartCreatesEverythingAndSendsThePromptOnce(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		res, err := StartTask(TaskStartRequest{ID: "m1", Repo: w.main, Title: "로그인 오류 $(x)", Prompt: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Action != TaskCreated || res.Base != "abc1234" || res.Token == nil || res.Token.SessionID != "conv-1" {
			t.Errorf("res = %+v", res)
		}
		if !slices.Equal(w.pane.submitted, []string{"go"}) {
			t.Errorf("제출 = %q", w.pane.submitted)
		}
		wt := w.worktree("m1")
		if !slices.Equal(w.seeds, []string{wt + "|conv-1|로그인 오류 $(x)"}) {
			t.Errorf("씨앗 = %q", w.seeds)
		}
		want := "tmux new-session -d -s t-m1 -c " + wt + " -x 120 -y 50 "
		var got string
		for _, c := range w.calls {
			if strings.HasPrefix(c, "tmux new-session") {
				got = c
			}
		}
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, `claude --resume conv-1; exec "$SHELL"`) {
			t.Errorf("new-session = %q", got)
		}
		if !w.afterBoundary("tmux new-session") {
			t.Error("초 경계를 기다리지 않고 세션을 만들었다")
		}
		// 사람이 쓴 글은 세션의 명령줄에 없다 — 복구 때 셸에 그대로 다시 쳐진다.
		if strings.Contains(got, "로그인") || strings.Contains(got, "go ") {
			t.Errorf("명령줄에 사람의 글이 있다: %q", got)
		}
		if len(res.Notes) != 0 {
			t.Errorf("notes = %q", res.Notes)
		}
	})
}

// 같은 명령을 다시 부르면 아무것도 바뀌지 않는다.
func TestStartTwiceDoesNothing(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		if _, err := w.start("go"); err != nil {
			t.Fatal(err)
		}
		before := len(w.mutated())
		res, err := w.start("go")
		if err != nil || res.Action != TaskExists || res.Token != nil {
			t.Errorf("res = %+v, %v", res, err)
		}
		if now := w.mutated(); len(now) != before {
			t.Errorf("두 번째 start 가 무언가 했다: %q", now[before:])
		}
		if len(w.pane.submitted) != 1 {
			t.Errorf("제출 = %q", w.pane.submitted)
		}
	})
}

// 세션이 사라졌으면 대화를 잇고, 첫 프롬프트는 다시 보내지 않는다.
func TestStartReopensWithoutResendingThePrompt(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		w.transcript(wt, "old-conv", trCustomTitle, trHumanLine)
		res, err := w.start("go")
		if err != nil || res.Action != TaskResumed || res.Token != nil {
			t.Fatalf("res = %+v, %v", res, err)
		}
		if len(w.pane.submitted) != 0 || len(w.seeds) != 0 {
			t.Errorf("보낸 것 %q, 씨앗 %q — 둘 다 없어야 한다", w.pane.submitted, w.seeds)
		}
		if w.called("tmux new-session -d -s t-m1 -c "+wt) != 1 || !strings.Contains(strings.Join(w.calls, "\n"), "--resume old-conv;") {
			t.Errorf("calls = %q", w.mutated())
		}
	})
}

// worktree 만 있고 프롬프트를 보낸 적이 없으면 이번에 보낸다 — 만들다 죽은 경우다.
func TestStartSendsThePromptThatWasNeverSent(t *testing.T) {
	cases := map[string]func(w *taskWorld){
		"대화가 없다":            func(w *taskWorld) {},
		"씨앗만 있다":            func(w *taskWorld) { w.transcript(w.worktree("m1"), "seed", trCustomTitle) },
		"claude -p 의 기록뿐이다": func(w *taskWorld) { w.transcript(w.worktree("m1"), "sdk", trSDKLine) },
		"지난 작업의 기록뿐이다": func(w *taskWorld) {
			p := w.transcript(w.worktree("m1"), "old", trCustomTitle, trHumanLine)
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(p, past, past); err != nil {
				w.t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				w.watch(w.main)
				w.addTree(w.worktree("m1"), "refs/heads/t-m1")
				setup(w)
				res, err := w.start("go")
				if err != nil || res.Action != TaskResumed || res.Token == nil {
					t.Fatalf("res = %+v, %v", res, err)
				}
				if !slices.Equal(w.pane.submitted, []string{"go"}) {
					t.Errorf("제출 = %q", w.pane.submitted)
				}
			})
		})
	}
}

// 이어 갈 대화는 가장 최근의 것이다. `claude -p` 의 기록이 더 새것이어도 고르지 않는다.
func TestLatestTaskConversationPicksTheNewestRealOne(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		born, _ := os.Stat(filepath.Join(wt, ".git"))
		for i, f := range []struct{ id, line string }{{"a", trHumanLine}, {"b", trHumanLine}, {"c", trSDKLine}} {
			p := w.transcript(wt, f.id, f.line)
			at := born.ModTime().Add(time.Duration(i+1) * time.Minute)
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
		conv, ok, err := latestTaskConversation(wt)
		if err != nil || !ok || conv.id != "b" || !conv.prompted {
			t.Errorf("conv = %+v, %v, %v, want b", conv, ok, err)
		}
	})
}

// /clear 뒤의 새 대화에는 사람 턴이 없다. 그것이 가장 최근이어도 프롬프트는 이미 보냈다 — 다시 보내면 안 된다.
func TestStartDoesNotResendAfterClear(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		born, _ := os.Stat(filepath.Join(wt, ".git"))
		for i, f := range []struct{ id, line string }{{"first", trHumanLine}, {"cleared", trCustomTitle}} {
			p := w.transcript(wt, f.id, f.line)
			at := born.ModTime().Add(time.Duration(i+1) * time.Minute)
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
		res, err := w.start("go")
		if err != nil || res.Action != TaskResumed || res.Token != nil || len(w.pane.submitted) != 0 {
			t.Errorf("res = %+v, %v, 제출 %q", res, err, w.pane.submitted)
		}
		if !strings.Contains(strings.Join(w.calls, "\n"), "--resume cleared;") {
			t.Errorf("가장 최근 대화를 이어야 한다: %q", w.mutated())
		}
	})
}

// Claude 의 기록 폴더 이름은 경로가 길면 200자에서 잘려 해시가 붙고, 이모지는 두 글자로 센다(2.1.332 에서 잰 것). 못
// 찾으면 이미 보낸 프롬프트를 다시 보낸다. 잘린 이름은 이웃 작업(t-m1 과 t-m12)과 같을 수 있어 cwd 로 가른다.
func TestLatestTaskConversationFindsUnusualPaths(t *testing.T) {
	long := "/" + strings.Repeat("a", 100) + "/R&D/" + strings.Repeat("b", 100) + "/repo-t-m1"
	cases := []struct{ name, worktree, dir string }{
		{"보통", "/p/repo-t-m1", "-p-repo-t-m1"},
		{"한글", "/p/저장소-t-m1", "-p-----t-m1"},
		{"이모지", "/p/😀-t-m1", "-p----t-m1"},
		{"200자 넘음", long, strings.NewReplacer("/", "-", "&", "-").Replace(long)[:200] + "-4crkqw"},
	}
	for _, tc := range cases {
		withTaskWorld(t, func(w *taskWorld) {
			dir := filepath.Join(w.home, claudeDir, "projects", tc.dir)
			os.MkdirAll(dir, 0o700)
			write := func(id, cwd string) {
				// Claude 는 & 를 이스케이프하지 않는다(JSON.stringify).
				line := `{"type":"user","cwd":"` + cwd + `","entrypoint":"cli","origin":{"kind":"human"}}`
				os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line+"\n"), 0o600)
			}
			write("mine", tc.worktree)
			if tc.name == "200자 넘음" {
				write("neighbour", tc.worktree+"2")
				future := time.Now().Add(time.Hour)
				os.Chtimes(filepath.Join(dir, "neighbour.jsonl"), future, future)
			}
			convs, err := taskConversations(tc.worktree, time.Time{})
			if err != nil || len(convs) != 1 || convs[0].id != "mine" || !convs[0].prompted {
				t.Errorf("%s: %+v, want mine", tc.name, convs)
			}
		})
	}
}

// 프롬프트 파일은 보낼 때만 읽는다 — 시작한 뒤에 파일을 지우고 같은 명령을 다시 불러도 아무 변화가 없어야 한다.
func TestStartReadsThePromptFileOnlyWhenItSends(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		file := filepath.Join(t.TempDir(), "p.md")
		os.WriteFile(file, []byte("go\n"), 0o600)
		req := TaskStartRequest{ID: "m1", Repo: w.main, PromptFile: file}
		if _, err := StartTask(req); err != nil || !slices.Equal(w.pane.submitted, []string{"go"}) {
			t.Fatalf("%v, 제출 %q", err, w.pane.submitted)
		}
		os.Remove(file)
		if res, err := StartTask(req); err != nil || res.Action != TaskExists {
			t.Errorf("다시: %+v, %v", res, err)
		}
		req.ID = "m2"
		if _, err := StartTask(req); err == nil || isDir(w.worktree("m2")) {
			t.Errorf("없는 파일로 새 작업을 만들었다: %v", err)
		}
	})
}

// 대화 id 는 파일 이름에서 오고 셸 명령에 들어간다. 글자를 벗어난 이름은 대화가 아니다.
func TestLatestTaskConversationRefusesNamesThatWouldReachTheShell(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		for _, id := range []string{"x; touch pwned", "$(id)", "a b", "-n", "`id`"} {
			w.transcript(wt, id, trHumanLine)
		}
		if conv, ok, err := latestTaskConversation(wt); ok || err != nil {
			t.Errorf("conv = %+v, %v", conv, err)
		}
	})
}

// 그 대화를 다른 곳에서 열어 두었으면 또 열지 않는다 — 한 기록에 두 Claude 가 쓴다.
func TestStartDoesNotOpenAConversationHeldElsewhere(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		w.transcript(wt, "held", trHumanLine)
		b, _ := json.Marshal(map[string]any{"pid": 777, "sessionId": "held", "cwd": wt, "procStart": "1", "status": "idle"})
		os.WriteFile(w.claudeFile(777), b, 0o600)
		if _, err := w.start("go"); err == nil || !strings.Contains(err.Error(), "777") {
			t.Errorf("err = %v", err)
		}
		if len(w.sessions) != 0 {
			t.Errorf("세션을 열었다: %v", w.sessions)
		}
	})
}

// 이름만 같고 폴더가 다른 세션은 이 작업의 것이 아니다.
func TestStartRefusesASessionThatIsNotTheTasks(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		w.sessions["t-m1"] = "/somewhere/else"
		if _, err := w.start("go"); err == nil || !strings.Contains(err.Error(), "/somewhere/else") {
			t.Errorf("err = %v", err)
		}
	})
}

// 아무것도 만들기 전에 거절하는 것들.
func TestStartRefusesBeforeMakingAnything(t *testing.T) {
	ok := func(w *taskWorld) TaskStartRequest {
		return TaskStartRequest{ID: "m1", Repo: w.main, Prompt: "go"}
	}
	cases := []struct {
		name  string
		setup func(w *taskWorld)
		req   func(w *taskWorld) TaskStartRequest
		want  string
	}{
		{"id 가 틀렸다", nil, func(w *taskWorld) TaskStartRequest { r := ok(w); r.ID = "M1"; return r }, "작업 id"},
		{"프롬프트가 여러 줄이다", nil, func(w *taskWorld) TaskStartRequest { r := ok(w); r.Prompt = "a\nb"; return r }, "여러 줄"},
		{"새 작업인데 프롬프트가 없다", nil, func(w *taskWorld) TaskStartRequest { r := ok(w); r.Prompt = ""; return r }, "--prompt"},
		{"저장소가 아니다", nil, func(w *taskWorld) TaskStartRequest { r := ok(w); r.Repo = "/not/a/repo"; return r }, "git 저장소"},
		{"--repo 도 지금 폴더의 저장소도 없다", nil, func(w *taskWorld) TaskStartRequest { r := ok(w); r.Repo, r.CWD = "", "/tmp"; return r }, "--repo"},
		{"로컬 브랜치가 남아 있다", func(w *taskWorld) { w.refs = append(w.refs, "refs/heads/t-m1") }, ok, "브랜치가 이미 있다"},
		{"원격에 브랜치가 있다", func(w *taskWorld) { w.refs = append(w.refs, "refs/remotes/origin/t-m1") }, ok, "브랜치가 이미 있다"},
		{"폴더가 이미 있다", func(w *taskWorld) { os.Mkdir(w.worktree("m1"), 0o755) }, ok, "덮지 않는다"},
		{"손으로 지운 worktree 가 등록돼 있다", func(w *taskWorld) {
			w.trees[w.worktree("m1")] = "refs/heads/t-m1"
			w.prunable[w.worktree("m1")] = true
		}, ok, "prune"},
		{"세션만 있다", func(w *taskWorld) { w.sessions["t-m1"] = "/nowhere" }, ok, "worktree 가 없다"},
		{"서버가 없다 — 띄우지 않는다", func(w *taskWorld) { w.listErr = errors.New("exit status 1: no server running on /tmp/x") }, ok, "서버가 없다"},
		{"답하지 못하는 서버", func(w *taskWorld) { w.listErr = errors.New("exit status 1: protocol version mismatch") }, ok, "세션 목록"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				if tc.setup != nil {
					tc.setup(w)
				}
				_, err := StartTask(tc.req(w))
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %v, want %q", err, tc.want)
				}
				if m := w.mutated(); len(m) != 0 || len(w.seeds) != 0 {
					t.Errorf("무언가 만들었다: %q %q", m, w.seeds)
				}
			})
		})
	}
}

// 이름이 앞부분만 같은 브랜치는 그 브랜치가 아니다(for-each-ref 는 앞부분 일치다).
func TestStartIsNotStoppedByALookalikeBranch(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.refs = append(w.refs, "refs/heads/t-m1/x", "refs/heads/t-m12", "refs/remotes/origin/feat/t-m1")
		if _, err := w.start("go"); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

// 같은 id 가 다른 저장소에 있으면 거절한다.
func TestStartRefusesTheSameIDInAnotherRepo(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		other := filepath.Join(t.TempDir(), "other")
		os.Mkdir(other, 0o755)
		oldMain := w.main
		// 두 번째 저장소는 git 이 답하는 유일한 «다른» 저장소다 — 목록 읽기만 흉내 낸다.
		r := &otherRepo{taskWorld: w, other: other}
		SetRunner(r)
		_, err := StartTask(TaskStartRequest{ID: "m1", Repo: other, Prompt: "go"})
		if err == nil || !strings.Contains(err.Error(), oldMain) {
			t.Errorf("err = %v, want the repo that holds it", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Errorf("무언가 만들었다: %q", m)
		}
	})
}

// --repo 없이 다른 저장소 안에서 부르면 그 저장소의 작업을 말하는 것일 수 있다 — 다른 저장소의 Claude 에 보내지 않는다.
func TestStartDoesNotResumeAnotherReposTaskFromHere(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		other := filepath.Join(t.TempDir(), "other")
		os.Mkdir(other, 0o755)
		SetRunner(&otherRepo{taskWorld: w, other: other})
		if _, err := StartTask(TaskStartRequest{ID: "m1", CWD: other, Prompt: "B 작업"}); err == nil || !strings.Contains(err.Error(), w.main) {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 {
			t.Fatalf("무언가 했다: %q", m)
		}
		// 지금 폴더의 저장소를 못 읽은 것(시간 초과)은 «저장소 밖» 이 아니다.
		w.gitErr = errors.New("git did not answer within 5s")
		r := &otherRepo{taskWorld: w, other: "/nowhere"}
		SetRunner(r)
		if _, err := existingTask(taskScan{}, TaskStartRequest{ID: "m1", CWD: other}); err != nil {
			t.Errorf("없는 작업: %v", err)
		}
		t1 := Task{ID: "m1", Repo: w.main, Worktree: w.worktree("m1")}
		if err := checkStartRepo(t1, TaskStartRequest{ID: "m1", CWD: other}); err == nil {
			t.Error("못 읽은 저장소를 저장소 밖으로 읽었다")
		}
		w.gitErr = nil
		SetRunner(&otherRepo{taskWorld: w, other: other})
		// 저장소 밖에서, 그리고 그 저장소의 다른 worktree 에서 부르는 것은 된다.
		if _, err := StartTask(TaskStartRequest{ID: "m1", CWD: "/", Prompt: "go"}); err != nil {
			t.Errorf("저장소 밖: %v", err)
		}
	})
}

// 만들다 끊긴 worktree 는 git 이 잠근 채(initializing) 남는다. 그 위에서 일을 시작하지 않는다.
func TestStartRefusesALockedWorktree(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		w.locked = w.worktree("m1")
		if _, err := w.start("go"); err == nil || !strings.Contains(err.Error(), "잠겨") {
			t.Errorf("err = %v", err)
		}
		if m := w.mutated(); len(m) != 0 || len(w.seeds) != 0 {
			t.Errorf("무언가 했다: %q %q", m, w.seeds)
		}
	})
}

// 기록을 읽지 못한 것은 «안 보냄» 이 아니다.
func TestStartStopsWhenTheTranscriptCannotBeRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 는 권한에 막히지 않는다")
	}
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		w.addTree(w.worktree("m1"), "refs/heads/t-m1")
		p := w.transcript(w.worktree("m1"), "conv", trHumanLine)
		os.Chmod(p, 0)
		defer os.Chmod(p, 0o600)
		if _, err := w.start("go"); err == nil {
			t.Error("기록을 못 읽었는데 성공이다")
		}
		if len(w.sessions) != 0 || len(w.pane.submitted) != 0 {
			t.Errorf("세션 %v, 제출 %q", w.sessions, w.pane.submitted)
		}
	})
}

// 씨앗의 기록을 다음 start 가 찾는 길로 확인한다. 못 찾으면 다음에 프롬프트를 다시 보내게 되므로 여기서 멈춘다.
func TestStartChecksTheSeedTheWayTheNextStartWill(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		seedTaskConversation = func(dir, id, title string) error {
			w.transcript("/some/other/folder", id, trCustomTitle) // Claude 의 폴더 이름 규칙이 우리와 어긋난 경우
			return nil
		}
		if _, err := w.start("go"); err == nil || len(w.sessions) != 0 {
			t.Errorf("err = %v, 세션 %v", err, w.sessions)
		}
	})
}

// 오래된 worktree 에 기록이 없으면 지워진 것일 수 있다(Claude 는 오래된 기록을 스스로 지운다). 다시 보내지 않고 알린다.
func TestStartDoesNotSendIntoAnOldWorktreeWithoutARecord(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.watch(w.main)
		wt := w.worktree("m1")
		w.addTree(wt, "refs/heads/t-m1")
		old := agentNow().Add(-25 * time.Hour)
		os.Chtimes(filepath.Join(wt, ".git"), old, old)
		res, err := w.start("go")
		if err != nil || res.Action != TaskResumed || res.Token != nil || len(w.pane.submitted) != 0 {
			t.Errorf("res = %+v, %v, 제출 %q", res, err, w.pane.submitted)
		}
		if len(res.Notes) == 0 || !strings.Contains(strings.Join(res.Notes, "\n"), "mux agent prompt t-m1") {
			t.Errorf("notes = %q", res.Notes)
		}
		// 첫 번째 부름이 만든 씨앗이 다음 부름의 «대화 있음» 이 되어 보내게 되면 안 된다.
		delete(w.sessions, "t-m1")
		w.stopClaude()
		if res, err := w.start("go"); err != nil || res.Token != nil || len(w.pane.submitted) != 0 {
			t.Errorf("두 번째: %+v, %v, 제출 %q", res, err, w.pane.submitted)
		}
	})
}

// 다른 작업의 세션과 같은 초에 만들지 않는다 — 초 경계를 기다리는 동안 잠금을 쥔다.
func TestStartSerializesSessionCreation(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		held := false
		waitForNextSecond = func() {
			w.calls = append(w.calls, secondBoundary)
			held = !flockFree(t, filepath.Join(agentLockDir(), "task-session.lock"))
		}
		if _, err := w.start("go"); err != nil || !held {
			t.Errorf("err = %v, held = %v", err, held)
		}
	})
}

type otherRepo struct {
	*taskWorld
	other string
	trees []string
}

func (r *otherRepo) Output(name string, args ...string) ([]byte, error) {
	if name == "git" && args[1] == r.other && strings.Join(args[2:], " ") == "worktree list --porcelain -z" {
		list := [][]string{{"worktree " + r.other, "HEAD abc", "branch refs/heads/main"}}
		for _, p := range r.trees {
			list = append(list, []string{"worktree " + p, "HEAD abc", "branch refs/heads/t-m1"})
		}
		return wtList(list...), nil
	}
	return r.taskWorld.Output(name, args...)
}

// 설정에 없는 저장소는 만들되 알린다.
func TestStartWarnsAboutAnUnwatchedRepo(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		res, err := w.start("go")
		if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "task-repos.txt") {
			t.Errorf("notes = %q, %v", res.Notes, err)
		}
	})
}

// --repo 없이 부르면 지금 폴더의 저장소다. linked worktree 안이면 그 원본 옆에 만든다.
func TestStartUsesTheRepoOfTheCurrentFolder(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.addTree(w.main+"-feat-x", "refs/heads/feat/x")
		res, err := StartTask(TaskStartRequest{ID: "m1", CWD: w.main + "-feat-x", Prompt: "go"})
		if err != nil || res.Task.Worktree != w.worktree("m1") || res.Task.Repo != w.main {
			t.Errorf("task = %+v, %v", res.Task, err)
		}
		// 시작점은 사람이 보던 checkout 의 HEAD 다.
		if w.called("git -C "+w.main+"-feat-x worktree add -b t-m1 ") != 1 {
			t.Errorf("calls = %q", w.mutated())
		}
	})
}

// 대화상자에서 멈춘 Claude 에게는 보내지 않는다. 세션은 남는다.
func TestStartDoesNotSendIntoADialog(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.state = "waiting"
		_, err := w.start("go")
		if !errors.Is(err, ErrAgentBlocked) {
			t.Errorf("err = %v", err)
		}
		if len(w.pane.runs) != 0 {
			t.Errorf("키를 보냈다: %q", w.pane.runs)
		}
		if _, ok := w.sessions["t-m1"]; !ok {
			t.Error("세션을 지웠다")
		}
	})
}

// 준비되지 않으면 시간이 다 됐다고 말하고 아무것도 보내지 않는다.
func TestStartTimesOutWhenClaudeNeverGetsReady(t *testing.T) {
	cases := map[string]func(w *taskWorld){
		"Claude 가 뜨지 않는다": func(w *taskWorld) { w.noClaude = true },
		"입력창이 없다":         func(w *taskWorld) { w.pane.noBox = true },
		"계속 일하는 중이다":      func(w *taskWorld) { w.state = "busy" },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			withTaskWorld(t, func(w *taskWorld) {
				setup(w)
				_, err := w.start("go")
				if !errors.Is(err, ErrWaitTimeout) {
					t.Errorf("err = %v", err)
				}
				if len(w.pane.runs) != 0 {
					t.Errorf("키를 보냈다: %q", w.pane.runs)
				}
			})
		})
	}
}

// 입력창이 보였다 사라지면(시작 훅, 다시 그리기) 다시 나타난 때부터 센다. 그 사이에 친 글은 사라질 수 있다.
func TestStartWaitsForTheBoxToSettle(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		begin := agentNow()
		const gone, back = 600 * time.Millisecond, 1400 * time.Millisecond
		sleep := agentSleep
		agentSleep = func(d time.Duration) {
			sleep(d)
			at := agentNow().Sub(begin)
			w.pane.noBox = at >= gone && at < back
		}
		var typedAt time.Duration
		w.pane.onSubmit = func() { typedAt = agentNow().Sub(begin) }
		if _, err := w.start("go"); err != nil {
			t.Fatal(err)
		}
		if typedAt < back+taskReadySettle {
			t.Errorf("%v 에 보냈다 — 입력창이 다시 나타난 %v 부터 %v 이어져야 한다", typedAt, back, taskReadySettle)
		}
	})
}

// 만드는 동안 같은 id 를 부르면 거절한다 — 잠금은 끝까지 쥔다.
func TestStartHoldsTheLockUntilItIsDone(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		seed := seedTaskConversation
		var inner error
		seedTaskConversation = func(dir, id, title string) error {
			_, inner = w.start("go")
			return seed(dir, id, title)
		}
		if _, err := w.start("go"); err != nil {
			t.Fatal(err)
		}
		if inner == nil || !strings.Contains(inner.Error(), "다루는 중") {
			t.Errorf("겹친 start = %v", inner)
		}
		if len(w.pane.submitted) != 1 {
			t.Errorf("제출 = %q", w.pane.submitted)
		}
	})
}

// 씨앗을 만들지 못하면 세션을 열지 않는다. 다시 부르면 이어서 만든다.
func TestStartStopsWhenTheSeedFailsAndPicksUpLater(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		w.seedErr = errors.New("claude: not logged in")
		if _, err := w.start("go"); err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Fatalf("err = %v", err)
		}
		if len(w.sessions) != 0 {
			t.Errorf("세션을 열었다: %v", w.sessions)
		}
		w.seedErr = nil
		res, err := w.start("go")
		if err != nil || res.Action != TaskResumed || !slices.Equal(w.pane.submitted, []string{"go"}) {
			t.Errorf("res = %+v, %v, 제출 %q", res, err, w.pane.submitted)
		}
	})
}

// 같은 id 를 둘이 다루지 못한다.
func TestStartIsLockedPerID(t *testing.T) {
	withTaskWorld(t, func(w *taskWorld) {
		unlock, err := lockTask("m1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.start("go"); err == nil || !strings.Contains(err.Error(), "다루는 중") {
			t.Errorf("err = %v", err)
		}
		unlock()
		if _, err := w.start("go"); err != nil {
			t.Errorf("푼 뒤: %v", err)
		}
	})
}

func TestTaskSeedArgsKeepTheTitleInOneWordAndLoadNoSettings(t *testing.T) {
	args := taskSeedArgs("id-1", `제목 "a" $(x)`)
	if args[len(args)-1] != `/rename 제목 "a" $(x)` {
		t.Errorf("args = %q", args)
	}
	for _, pair := range [][2]string{{"--setting-sources", ""}, {"--tools", ""}, {"--session-id", "id-1"}} {
		i := slices.Index(args, pair[0])
		if i < 0 || args[i+1] != pair[1] {
			t.Errorf("%s 가 %q 가 아니다: %q", pair[0], pair[1], args)
		}
	}
	if slices.Contains(args, "--disable-slash-commands") {
		t.Error("/rename 을 쓰므로 슬래시 명령을 끄면 안 된다")
	}
}

func TestRandomUUIDIsV4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, err := randomUUID()
	b, _ := randomUUID()
	if err != nil || !re.MatchString(a) || a == b {
		t.Errorf("%q %q %v", a, b, err)
	}
}
