package tmux

import (
	"os"
	"testing"
)

// WT 클라이언트 선택 규칙: Windows Terminal 클라이언트만, 최근 활동 우선.
func TestMostRecentWTClient(t *testing.T) {
	out := "100 1000 /dev/pts/1\n200 3000 /dev/pts/2\n300 2000 /dev/pts/3\n"

	t.Run("picks most recent WT client", func(t *testing.T) {
		isWT := func(pid int) bool { return pid == 100 || pid == 300 }
		if got := mostRecentWTClient(out, isWT); got != "/dev/pts/3" {
			t.Errorf("got %q, want /dev/pts/3", got)
		}
	})

	t.Run("ignores non-WT clients entirely", func(t *testing.T) {
		isWT := func(pid int) bool { return false }
		if got := mostRecentWTClient(out, isWT); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("skips malformed lines", func(t *testing.T) {
		isWT := func(int) bool { return true }
		if got := mostRecentWTClient("garbage\n100 500 /dev/pts/9\n", isWT); got != "/dev/pts/9" {
			t.Errorf("got %q, want /dev/pts/9", got)
		}
	})
}

// WT 판별: WT_SESSION 만으로는 부족하다 — WT 에서 띄운 VS Code 가 그 값을 물려받으므로
// 그 클라이언트까지 WT 로 세면 SwitchWTClient 가 VS Code 창을 끌고 간다.
func TestIsWTClientExcludesVSCode(t *testing.T) {
	origWT, origVS := hasWTEnv, isVSCodeClient
	t.Cleanup(func() { hasWTEnv, isVSCodeClient = origWT, origVS })

	// 100: 순수 WT, 200: WT_SESSION 을 물려받은 VS Code, 300: 둘 다 아님
	hasWTEnv = func(pid int) bool { return pid == 100 || pid == 200 }
	isVSCodeClient = func(pid int) bool { return pid == 200 }

	cases := map[int]bool{100: true, 200: false, 300: false}
	for pid, want := range cases {
		if got := isWTClient(pid); got != want {
			t.Errorf("isWTClient(%d) = %v, want %v", pid, got, want)
		}
	}
}

// VS Code 클라이언트가 붙은 세션인지: 붙어 있으면 무자격 switch-client 를 쓰면 안 된다.
func TestAnyVSCodeClient(t *testing.T) {
	isVS := func(pid int) bool { return pid == 200 }

	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"VS Code 클라이언트 있음", "100\n200\n", true},
		{"WT 뿐", "100\n300\n", false},
		{"클라이언트 없음", "", false},
		{"깨진 줄은 건너뛴다", "garbage\n200\n", true},
	}
	for _, c := range cases {
		if got := anyVSCodeClient(c.out, isVS); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// pane ID 추출: 세션 이름이 바뀌어도 %N으로 현재 세션을 역추적하기 위한 파서.

// 세션의 VS Code 창 고르기: 워크스페이스 폴더가 세션 디렉터리를 품은 창만 주인이다.
// 구경하러 attach 한 다른 프로젝트 창을 고르면 엉뚱한 창이 앞으로 나온다.
func TestVSCodeDirFor(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("홈 디렉터리를 모르면 이 규칙은 적용되지 않는다")
	}

	cases := []struct {
		name       string
		sessionDir string
		candidates []string
		want       string
	}{
		{"정확히 같은 폴더", "/p/water", []string{"/p/water"}, "/p/water"},
		{"하위 디렉터리", "/p/water/back", []string{"/p/water"}, "/p/water"},
		{"구경 창은 제외", "/p/water", []string{"/p/govgen"}, ""},
		{"주인과 구경 창이 섞임", "/p/water", []string{"/p/govgen", "/p/water"}, "/p/water"},
		{"접두사만 같은 남", "/p/water2", []string{"/p/water"}, ""},
		// 홈에서 연 창은 모든 프로젝트를 품는다 — 담기만 해서는 주인이 아니다.
		{"홈 창은 주인이 아니다", "/p/water", []string{home}, ""},
		{"홈 자체를 보는 세션은 예외", home, []string{home}, home},
		{"홈 창보다 진짜 주인이 뒤에 있어도 고른다", "/p/water", []string{home, "/p/water"}, "/p/water"},
		{"후보 없음", "/p/water", nil, ""},
		{"세션 디렉터리를 모름", "", []string{"/p/water"}, ""},
	}
	for _, c := range cases {
		if got := VSCodeDirFor(c.sessionDir, c.candidates); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
