//go:build e2e

// 패널의 세션 아래 줄이 대화 기록에서 읽은 «지금 하는 일» 인지 진짜 패널 화면으로 본다. 단위 테스트는 기록을
// 읽는 쪽과 그리는 쪽을 따로 지키고, 상태 파일 → 기록 → ListSessions → 화면으로 이어지는 길은 여기서만 지나간다.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// underSession 은 패널 화면에서 세션 행 바로 아래 줄이다. 없으면 빈 문자열.
func underSession(t *testing.T, panel, session string) string {
	t.Helper()
	lines := strings.Split(screen(t, panel), "\n")
	for i, l := range lines {
		if strings.Contains(l, session) && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	return ""
}

func TestPanelShowsTheWorkUnderTheSession(t *testing.T) {
	const session = "e2e-work"
	// 상태 파일의 대화 이름 — /clear 뒤의 실제 세션처럼 지금 하는 일과 상관없는 값이다.
	const staleName = "stale-conversation-name"
	fake := startFake(t, session, staleName)
	attach(t, session, 200, 40, false)
	w := eventuallyWindow(t, session, "패널이 열린다", 8*time.Second, func(w window) bool { return w.panel > 0 })
	panel := panelPane(t, w.id)

	send := func(text string) {
		t.Helper()
		token, errOut, code := muxAgent(t, "prompt", fake, text)
		if code != 0 {
			t.Fatalf("prompt %q: exit %d: %s", text, code, errOut)
		}
		if _, errOut, code := muxAgent(t, "wait", fake, "--after", token, "--timeout", "10s"); code != 0 {
			t.Fatalf("wait %q: exit %d: %s", text, code, errOut)
		}
	}
	showing := func(what, want string) {
		t.Helper()
		var got string
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			got = underSession(t, panel, session)
			if strings.Contains(got, want) {
				if strings.Contains(got, staleName) {
					t.Fatalf("%s: 대화 이름으로 돌아갔다: %q", what, got)
				}
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s: 세션 아래 줄 %q, want %q\n화면:\n%s", what, got, want, screen(t, panel))
	}

	send("패널에 세션별 현재 작업을 적어 주세요")
	showing("프롬프트", "패널에 세션별")

	// 일을 말하지 않는 입력은 줄을 바꾸지 않는다. 다음 일이 바꾸는 것까지 봐야 패널 틱이 돈다는 것도 확인된다.
	send("응.")
	send("[mantis-progress] 지난 문의 뒤로 진행된 일을 JSON 한 줄로 답해 주세요")
	time.Sleep(2500 * time.Millisecond) // 패널 틱(2초)이 지날 시간
	showing("짧은 응답과 문의 뒤", "패널에 세션별")

	send("kitty 를 최신 버전으로 올려 주세요")
	showing("새 프롬프트", "kitty 를 최신")
}

// 패널이 가짜 claude -p 로 제목을 짓고, 그 제목이 세션 아래 줄이 된다. 패널이 여럿이어도(창마다 하나) 대화 하나에
// 글 하나당 호출은 하나다 — 틱은 저장소만 읽어야 한다.
func TestPanelTitlesTheWorkOnce(t *testing.T) {
	const session = "e2e-title"
	fake := startFake(t, session, "stale-conversation-name")
	attach(t, session, 200, 40, false)
	w := eventuallyWindow(t, session, "패널이 열린다", 8*time.Second, func(w window) bool { return w.panel > 0 })
	panel := panelPane(t, w.id)

	const work = "제목 보완을 시험하는 프롬프트입니다"
	token, errOut, code := muxAgent(t, "prompt", fake, work)
	if code != 0 {
		t.Fatalf("prompt: exit %d: %s", code, errOut)
	}
	if _, errOut, code := muxAgent(t, "wait", fake, "--after", token, "--timeout", "10s"); code != 0 {
		t.Fatalf("wait: exit %d: %s", code, errOut)
	}

	eventually(t, "세션 아래 줄이 제목이 된다", 15*time.Second, func() bool {
		return strings.Contains(underSession(t, panel, session), "제목: "+work)
	})

	time.Sleep(5 * time.Second) // 패널 틱 두어 번 — 또 부르면 여기서 늘어난다
	calls := 0
	data, err := os.ReadFile(filepath.Join(home, "fake-titles.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == work {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("이 글의 제목 호출 %d번, want 1", calls)
	}
}
