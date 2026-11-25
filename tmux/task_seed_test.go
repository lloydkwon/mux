package tmux

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runTaskSeed 는 러너를 거치지 않으므로(task.md «실행 모양») 목 대신 PATH 맨 앞에 가짜 claude 를 두고 진짜로 실행한다.

const seedTestSessionID = "6f1c2a9e-3b7d-4c1e-9a2f-0d8e7b6c5a41"

// seedSuccessReply 는 씨앗이 성공했을 때 `claude -p --output-format json` 이 내는 모양이다(모델 호출 없음).
const seedSuccessReply = `printf '%s' '{"type":"result","subtype":"success","is_error":false,"num_turns":0,"result":"","total_cost_usd":0}'`

// fakeSeedClaude 는 PATH 맨 앞에 가짜 claude 를 두고, 부른 쪽의 환경에 TMUX·TMUX_PANE 을 심는다.
// 스크립트는 받은 폴더·환경·인자를 seen 에 적은 뒤 body 를 돈다.
func fakeSeedClaude(t *testing.T, body string) (seen string) {
	t.Helper()
	bin := t.TempDir()
	seen = filepath.Join(t.TempDir(), "seen")
	// ${X+set} 은 «있음» 만 본다 — TMUX= 처럼 빈 값으로 남은 것도 tmux 안으로 읽히므로 «없음» 과 갈라야 한다.
	script := "#!/bin/sh\n" +
		"{\n" +
		"  echo \"pwd=$(pwd -P)\"\n" +
		"  echo \"tmux=${TMUX+set}\"\n" +
		"  echo \"pane=${TMUX_PANE+set}\"\n" +
		"  echo \"home=${HOME-}\"\n" +
		"  for a in \"$@\"; do printf 'arg=[%s]\\n' \"$a\"; done\n" +
		"} > '" + seen + "'\n" +
		body + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-1000/default,4242,0")
	t.Setenv("TMUX_PANE", "%9")
	return seen
}

type seedSeen struct {
	pwd, tmux, pane, home string
	args                  []string
}

func readSeedSeen(t *testing.T, path string) seedSeen {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("가짜 claude 가 돌지 않았다: %v", err)
	}
	var s seedSeen
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		key, val, _ := strings.Cut(line, "=")
		switch key {
		case "pwd":
			s.pwd = val
		case "tmux":
			s.tmux = val
		case "pane":
			s.pane = val
		case "home":
			s.home = val
		case "arg":
			s.args = append(s.args, strings.TrimSuffix(strings.TrimPrefix(val, "["), "]"))
		}
	}
	return s
}

// seedWorktree 는 씨앗을 돌릴 폴더다. 빈칸과 한글을 넣어 폴더가 셸을 거치지 않고 그대로 가는지도 본다.
func seedWorktree(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "저장소 t-m1")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 씨앗은 그 worktree 폴더에서, 인자를 쪼개거나 버리지 않고, tmux 를 모르는 채로 돈다.
// 빈 인자가 빠지면 `--setting-sources` 가 다음 옵션을 값으로 읽어 훅·CLAUDE.md 가 돈다. TMUX 가 남으면 부른 pane 이
// 그동안 Claude 세션으로 보인다(workTitleEnv). HOME 까지 빠지면 claude 가 자기 설정·로그인 폴더를 못 찾는다.
func TestRunTaskSeedRunsInTheWorktreeWithoutTmux(t *testing.T) {
	seen := fakeSeedClaude(t, seedSuccessReply)
	work := seedWorktree(t)
	title := `로그인 "오류" $(touch x); y`

	if err := runTaskSeed(work, seedTestSessionID, title); err != nil {
		t.Fatalf("runTaskSeed: %v", err)
	}
	got := readSeedSeen(t, seen)

	want := []string{
		"-p",
		"--output-format", "json",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--tools", "",
		"--session-id", seedTestSessionID,
		"/rename " + title,
	}
	if !slices.Equal(got.args, want) {
		t.Errorf("인자:\n got %q\nwant %q", got.args, want)
	}
	wantDir, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	if got.pwd != wantDir {
		t.Errorf("폴더 = %q, want %q", got.pwd, wantDir)
	}
	if got.tmux != "" || got.pane != "" {
		t.Errorf("tmux 환경이 남았다: TMUX %q, TMUX_PANE %q", got.tmux, got.pane)
	}
	if home := os.Getenv("HOME"); got.home != home {
		t.Errorf("HOME = %q, want %q — 나머지 환경은 그대로 가야 한다", got.home, home)
	}
	if _, err := os.Stat(filepath.Join(work, "x")); err == nil {
		t.Error("제목의 $(…) 가 셸에서 돌았다")
	}
}

// 종료 코드 0 이라도 is_error 면 대화가 없다. Claude 가 준 까닭을 그대로 보인다.
func TestRunTaskSeedReportsAnErrorReply(t *testing.T) {
	fakeSeedClaude(t, `printf '%s' '{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}'`)

	err := runTaskSeed(seedWorktree(t), seedTestSessionID, "t-m1")
	if err == nil {
		t.Fatal("is_error 답을 성공으로 읽었다")
	}
	if !strings.Contains(err.Error(), "Not logged in · Please run /login") {
		t.Errorf("err = %v — Claude 가 준 까닭이 없다", err)
	}
}

// JSON 이 아니면 대화가 만들어졌는지 모른다 — 성공으로 넘기지 않는다.
func TestRunTaskSeedRejectsAReplyThatIsNotJSON(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"글", `echo 'Error: Input must be provided either through stdin or as a prompt argument when using --print'`},
		{"빈 답", `:`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSeedClaude(t, tc.body)

			err := runTaskSeed(seedWorktree(t), seedTestSessionID, "t-m1")
			if err == nil || !strings.Contains(err.Error(), "claude 답을 읽지 못했다") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// 종료 코드가 JSON 보다 먼저다 — stdout 이 성공처럼 보여도 1 이면 실패이고, stderr 의 까닭과 ExitError 가 함께 온다.
// 같은 id 로 다시 만들면 «already in use» 로 1 이다(task.md 의 표).
func TestRunTaskSeedReportsTheExitCodeWithStderr(t *testing.T) {
	fakeSeedClaude(t, seedSuccessReply+"\n"+
		`echo 'Error: Session ID `+seedTestSessionID+` is already in use.' >&2`+"\n"+
		`exit 1`)

	err := runTaskSeed(seedWorktree(t), seedTestSessionID, "t-m1")
	if err == nil {
		t.Fatal("종료 코드 1 을 성공으로 읽었다")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Errorf("err = %v — 종료 코드 1 의 ExitError 가 아니다", err)
	}
	if !strings.Contains(err.Error(), "is already in use") {
		t.Errorf("err = %v — stderr 의 까닭이 없다", err)
	}
}

// PATH 에도 설치 폴더(~/.local/bin)에도 없으면 아무것도 돌리지 않고 그렇게 말한다.
func TestRunTaskSeedWithoutClaude(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })

	err := runTaskSeed(seedWorktree(t), seedTestSessionID, "t-m1")
	if err == nil || !strings.Contains(err.Error(), "claude 를 찾지 못했다") {
		t.Errorf("err = %v", err)
	}
}
