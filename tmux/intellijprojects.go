package tmux

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

// 어떤 프로젝트가 IntelliJ 창으로 열려 있는지 — vscodewindows.go 의 IntelliJ 판이다. 패널의
// 왼쪽 클릭은 @mux_editor 가 idea 면 그 세션의 프로젝트가 열린 IntelliJ 창을 앞으로 내는데,
// **이미 열린 창만** 내야 한다(띄우는 것은 오른쪽 클릭의 일). 창은 이름(프로젝트 폴더의 끝
// 이름)으로만 고를 수 있어서, 열린 것이 확인된 폴더에 대해서만 묻는다 — 같은 이름의 다른
// 프로젝트 창을 올리지 않으려고.
//
// IntelliJ 는 최근 프로젝트 기록(options/recentProjects.xml)에서 지금 열린 프로젝트에
// opened="true" 를 단다(2026-10-21 실측: 화면의 두 창과 정확히 맞았다). 끌 때는 다음에 다시 열
// 프로젝트로 그 표시를 남길 수 있으므로 — VS Code 의 openedWindows 와 같다 — IntelliJ 가 떠
// 있을 때만 믿는다.
//
// PyCharm 도 여기서 함께 읽는다(2026-10-30). @mux_editor=idea 의 오른쪽 클릭은 Python 프로젝트를
// PyCharm 으로 열고, 그 창도 같은 클릭이 올린다. 기록의 모양도 같다. 각 IDE 의 기록은 그 IDE
// 가 떠 있을 때만 믿는다 — IntelliJ 가 떠 있다고 PyCharm 이 남긴 «다시 열 프로젝트»를 믿지 않는다.

// jetbrainsIDEs 는 기록을 읽는 IDE 다: config 는 설정 폴더 이름의 앞부분(뒤에 버전이 붙는다,
// IntelliJIdea2026.2), comm 은 떠 있는 IDE 의 프로세스 이름이다. 런처가 IDE 를 자기 프로세스
// 안에서 돌리므로 런처 이름과 같다(실측: idea, pycharm). DataGrip 같은 다른 JetBrains IDE 는
// 프로젝트 폴더를 열지 않으므로 넣지 않는다.
var jetbrainsIDEs = []struct{ config, comm string }{
	{"IntelliJIdea", "idea"},
	{"PyCharm", "pycharm"},
}

// jetbrainsConfigDir is where every JetBrains IDE keeps its config folders, or
// "" when there is no home. A variable so tests can point it at a fixture.
var jetbrainsConfigDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config/JetBrains")
}

// processRunning reports whether a process named comm is running. Replaceable
// in tests, like vscodeRunning.
var processRunning = func(comm string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if c := e.Name()[0]; c < '0' || c > '9' {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == comm {
			return true
		}
	}
	return false
}

// OpenIntelliJProjects is the folder of every project IntelliJ or PyCharm has
// open now. Unreadable or not running answers empty — "do not know", which the
// caller reads as "do not raise anything". A variable so tests can say which
// projects are open.
var OpenIntelliJProjects = openIntelliJProjects

func openIntelliJProjects() []string {
	base := jetbrainsConfigDir()
	if base == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, ide := range jetbrainsIDEs {
		matches, _ := filepath.Glob(filepath.Join(base, ide.config+"*", "options", "recentProjects.xml"))
		if len(matches) == 0 || !processRunning(ide.comm) {
			continue
		}
		for _, p := range matches {
			if b, err := os.ReadFile(p); err == nil {
				out = append(out, parseIntelliJOpened(b, home)...)
			}
		}
	}
	return out
}

// parseIntelliJOpened pulls the opened projects out of recentProjects.xml.
// Pure for testing. Keys use IntelliJ's $USER_HOME$ macro; anything that is not
// an absolute path after expanding it is dropped.
func parseIntelliJOpened(b []byte, home string) []string {
	var doc struct {
		Components []struct {
			Options []struct {
				Entries []struct {
					Key  string `xml:"key,attr"`
					Info struct {
						// encoding/xml 은 경로 붙은 속성(a>b,attr)을 받지 않는다 — 요소 하나를 둔다.
						Opened string `xml:"opened,attr"`
					} `xml:"value>RecentProjectMetaInfo"`
				} `xml:"map>entry"`
			} `xml:"option"`
		} `xml:"component"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil
	}
	var out []string
	for _, c := range doc.Components {
		for _, o := range c.Options {
			for _, e := range o.Entries {
				if e.Info.Opened != "true" {
					continue
				}
				dir := strings.Replace(e.Key, "$USER_HOME$", home, 1)
				dir = strings.TrimRight(dir, "/")
				if strings.HasPrefix(dir, "/") {
					out = append(out, dir)
				}
			}
		}
	}
	return out
}

// IntelliJWindowFor is the folder of the open IntelliJ or PyCharm project that
// owns a session's directory, or "" when none does. Same ownership rule as
// VSCodeDirFor: exact or containing, never the home directory.
func IntelliJWindowFor(sessionDir string) string {
	return VSCodeDirFor(sessionDir, OpenIntelliJProjects())
}
