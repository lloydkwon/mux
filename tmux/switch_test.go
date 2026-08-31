package tmux

import "testing"

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

// pane ID 추출: 세션 이름이 바뀌어도 %N으로 현재 세션을 역추적하기 위한 파서.
