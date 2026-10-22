package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 이 기계의 recentProjects.xml 에서 줄인 것(2026-10-21). 열린 두 프로젝트에만 opened="true"
// 가 있고, my-memo 는 닫힌 최근 프로젝트다.
const intellijRecentFixture = `<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="$USER_HOME$/dev/projects/my/my-memo">
          <value>
            <RecentProjectMetaInfo frameTitle="my-memo" projectWorkspaceId="a" />
          </value>
        </entry>
        <entry key="$USER_HOME$/dev/projects/my/mux">
          <value>
            <RecentProjectMetaInfo frameTitle="mux – Chat" opened="true" projectWorkspaceId="b">
              <option name="activationTimestamp" value="1792577505273" />
            </RecentProjectMetaInfo>
          </value>
        </entry>
        <entry key="/srv/work/matchingByJev/">
          <value>
            <RecentProjectMetaInfo opened="true" projectWorkspaceId="c" />
          </value>
        </entry>
      </map>
    </option>
    <option name="lastOpenedProject" value="$USER_HOME$/dev/projects/my/mux" />
  </component>
</application>`

func TestParseIntelliJOpened(t *testing.T) {
	got := parseIntelliJOpened([]byte(intellijRecentFixture), "/home/u")
	want := []string{"/home/u/dev/projects/my/mux", "/srv/work/matchingByJev"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := parseIntelliJOpened([]byte("<application><broken"), "/home/u"); got != nil {
		t.Errorf("깨진 XML 에서 폴더가 나왔다: %v", got)
	}
}

// IntelliJ 를 끄면 «다시 열 프로젝트»로 opened 가 남을 수 있다 — VS Code 의 openedWindows 와
// 같다. 그 기록을 믿으면 왼쪽 클릭이 없는 창을 찾으러 간다. 떠 있을 때만 읽는다.
func TestOpenIntelliJProjectsTrustsTheRecordOnlyWhileRunning(t *testing.T) {
	dir := t.TempDir()
	opts := filepath.Join(dir, "IntelliJIdea2026.2", "options")
	if err := os.MkdirAll(opts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts, "recentProjects.xml"), []byte(intellijRecentFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	prevGlob, prevRun := intellijRecentGlob, intellijRunning
	t.Cleanup(func() { intellijRecentGlob, intellijRunning = prevGlob, prevRun })
	intellijRecentGlob = func() string { return filepath.Join(dir, "IntelliJIdea*", "options", "recentProjects.xml") }

	intellijRunning = func() bool { return false }
	if got := openIntelliJProjects(); len(got) != 0 {
		t.Errorf("IntelliJ 가 꺼져 있는데 열린 프로젝트 %v", got)
	}
	intellijRunning = func() bool { return true }
	if got := openIntelliJProjects(); len(got) != 2 {
		t.Errorf("떠 있을 때 열린 프로젝트 %v, want 2개", got)
	}
}

// IntelliJ 터미널(JediTerm)이 붙은 세션에서 그 터미널의 폴더를 모은다. VS Code 터미널은 섞이지
// 않는다 — 이 폴더로 IntelliJ 창을 찾는다.
func TestIntelliJClientDirs(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("10 api\n11 api\n12 web\n"), nil, "tmux", "list-clients", "-F", "#{client_pid} #{client_session}")
		prevIs, prevPWD := isJetBrainsClient, clientPWD
		t.Cleanup(func() { isJetBrainsClient, clientPWD = prevIs, prevPWD })
		isJetBrainsClient = func(pid int) bool { return pid != 11 }
		clientPWD = func(pid int) string { return map[int]string{10: "/p/api", 11: "/p/vscode", 12: "/p/web"}[pid] }

		got := IntelliJClientDirs()
		if strings.Join(got["api"], ",") != "/p/api" || strings.Join(got["web"], ",") != "/p/web" {
			t.Errorf("got %v", got)
		}
	})
}
