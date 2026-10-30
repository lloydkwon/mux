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

// PyCharm 의 기록은 IntelliJ 의 것과 모양이 같다. 열린 프로젝트 하나.
const pycharmRecentFixture = `<application>
  <component name="RecentProjectsManager">
    <option name="additionalInfo">
      <map>
        <entry key="$USER_HOME$/dev/projects/ebbt/matchingByJev">
          <value>
            <RecentProjectMetaInfo frameTitle="matchingByJev" opened="true" projectWorkspaceId="d" />
          </value>
        </entry>
      </map>
    </option>
  </component>
</application>`

// writeRecent 는 dir 아래 IDE 설정 폴더 config 에 recentProjects.xml 을 둔다.
func writeRecent(t *testing.T, dir, config, body string) {
	t.Helper()
	opts := filepath.Join(dir, config, "options")
	if err := os.MkdirAll(opts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts, "recentProjects.xml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// IntelliJ 를 끄면 «다시 열 프로젝트»로 opened 가 남을 수 있다 — VS Code 의 openedWindows 와
// 같다. 그 기록을 믿으면 왼쪽 클릭이 없는 창을 찾으러 간다. 그래서 각 IDE 의 기록은 그 IDE 가
// 떠 있을 때만 읽는다 — IntelliJ 가 떠 있다고 PyCharm 의 기록을 믿지 않는다.
func TestOpenIntelliJProjectsTrustsEachRecordOnlyWhileItsIDERuns(t *testing.T) {
	dir := t.TempDir()
	writeRecent(t, dir, "IntelliJIdea2026.2", intellijRecentFixture)
	writeRecent(t, dir, "PyCharm2026.2", pycharmRecentFixture)
	writeRecent(t, dir, "DataGrip2026.2", pycharmRecentFixture) // 다른 JetBrains IDE 는 읽지 않는다
	t.Setenv("HOME", "/home/u")

	prevDir, prevRun := jetbrainsConfigDir, processRunning
	t.Cleanup(func() { jetbrainsConfigDir, processRunning = prevDir, prevRun })
	jetbrainsConfigDir = func() string { return dir }

	const mux, jev, byPath = "/home/u/dev/projects/my/mux", "/home/u/dev/projects/ebbt/matchingByJev", "/srv/work/matchingByJev"
	for _, c := range []struct {
		running []string
		want    []string
	}{
		{nil, nil},
		{[]string{"idea"}, []string{mux, byPath}},
		{[]string{"pycharm"}, []string{jev}},
		{[]string{"idea", "pycharm"}, []string{mux, byPath, jev}},
		{[]string{"datagrip"}, nil},
	} {
		processRunning = func(comm string) bool {
			for _, r := range c.running {
				if r == comm {
					return true
				}
			}
			return false
		}
		if got := openIntelliJProjects(); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("running %v: 열린 프로젝트 %v, want %v", c.running, got, c.want)
		}
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
