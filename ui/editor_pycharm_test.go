package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withPythonProject 는 Python 판별을 python 으로 답하게 바꾸고, 물어본 폴더를 적는다.
func withPythonProject(t *testing.T, python bool) *[]string {
	t.Helper()
	var asked []string
	prev := pythonProject
	pythonProject = func(dir string) bool {
		asked = append(asked, dir)
		return python
	}
	t.Cleanup(func() { pythonProject = prev })
	return &asked
}

// pycharmTestModel 은 IntelliJ 와 PyCharm 이 둘 다 설치돼 있고 @mux_editor 가 idea 인 패널이다.
func pycharmTestModel() watchModel {
	m := intellijTestModel("idea")
	m.pycharmBin = "/opt/pycharm/bin/pycharm"
	return m
}

// PyCharm 도 IntelliJ 와 같은 규칙으로 찾는다. Toolbox 는 PATH 에 아무것도 올리지 않아(실측)
// 후보가 실제로 쓰인다.
func TestFindPyCharm(t *testing.T) {
	withCandidates := func(t *testing.T, paths ...string) {
		prev := pycharmCandidates
		pycharmCandidates = func() []string { return paths }
		t.Cleanup(func() { pycharmCandidates = prev })
	}

	t.Run("PATH first", func(t *testing.T) {
		pathDir := t.TempDir()
		onPath := writeFakeScript(t, pathDir, "pycharm", "")
		toolbox := writeFakeScript(t, t.TempDir(), "pycharm", "")
		t.Setenv("PATH", pathDir)
		withCandidates(t, toolbox)
		if got := findPyCharm(); got != onPath {
			t.Errorf("findPyCharm = %q, want the one on PATH (%q)", got, onPath)
		}
	})

	t.Run("falls back to the install locations", func(t *testing.T) {
		toolbox := writeFakeScript(t, t.TempDir(), "pycharm", "")
		t.Setenv("PATH", t.TempDir())
		withCandidates(t, "/nope/pycharm", toolbox)
		if got := findPyCharm(); got != toolbox {
			t.Errorf("findPyCharm = %q, want %q", got, toolbox)
		}
	})

	t.Run("empty when absent", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		withCandidates(t, "/nope/pycharm")
		if got := findPyCharm(); got != "" {
			t.Errorf("findPyCharm = %q, want empty", got)
		}
	})
}

// Python 프로젝트는 최상위의 표식 파일로 알아본다. JVM 빌드 파일이 함께 있으면 섞인 저장소라
// IntelliJ 에 둔다. 하위 폴더는 보지 않는다 — water_ai 처럼 ai/·backend/·frontend/ 를 품은
// 저장소는 IntelliJ 다.
func TestIsPythonProject(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string // dir 아래 상대 경로, 빈 파일로 만든다
		dirs  []string // dir 아래 상대 경로, 폴더로 만든다
		want  bool
	}{
		{"pyproject.toml", []string{"pyproject.toml"}, nil, true},
		{"setup.py", []string{"setup.py"}, nil, true},
		{"setup.cfg", []string{"setup.cfg"}, nil, true},
		{"requirements.txt", []string{"requirements.txt"}, nil, true},
		{"Pipfile", []string{"Pipfile"}, nil, true},
		{"uv.lock", []string{"uv.lock"}, nil, true},
		{"poetry.lock", []string{"poetry.lock"}, nil, true},
		{".python-version", []string{".python-version"}, nil, true},
		{"gradle beside a requirements file", []string{"build.gradle", "requirements.txt"}, nil, false},
		{"kotlin gradle beside pyproject", []string{"build.gradle.kts", "pyproject.toml"}, nil, false},
		{"gradle settings beside pyproject", []string{"settings.gradle", "pyproject.toml"}, nil, false},
		{"maven beside pyproject", []string{"pom.xml", "pyproject.toml"}, nil, false},
		{"scripts and a virtualenv, no build file", []string{"monitor.py", ".venv/pyvenv.cfg"}, nil, true},
		{"a script beside go.mod", []string{"go.mod", "tool.py"}, nil, false},
		{"nothing", nil, nil, false},
		{"python only below the top", []string{"ai/pyproject.toml"}, nil, false},
		{"a folder named like a marker", nil, []string{"pyproject.toml"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range c.files {
				p := filepath.Join(dir, f)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, d := range c.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if got := isPythonProject(dir); got != c.want {
				t.Errorf("isPythonProject = %v, want %v", got, c.want)
			}
		})
	}

	if isPythonProject("") || isPythonProject(filepath.Join(t.TempDir(), "gone")) {
		t.Error("a folder that is not there was read as a Python project")
	}
}

// @mux_editor=idea 이고 PyCharm 이 있으면 Python 프로젝트는 PyCharm 으로 연다. 기다리지 않는
// 실행이다 — PyCharm 의 런처도 IntelliJ 처럼 그 자체가 IDE 다.
func TestRightClickOpensAPythonProjectInPyCharm(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	asked := withPythonProject(t, true)

	m := pycharmTestModel()
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if msg := cmd(); msg != nil {
		t.Fatalf("unexpected message: %#v", msg)
	}

	want := "detached /opt/pycharm/bin/pycharm /home/u/projects/api"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("editor calls = %v, want [%q]", *calls, want)
	}
	if len(*asked) != 1 || (*asked)[0] != "/home/u/projects/api" {
		t.Errorf("asked about %v, want the project folder once", *asked)
	}
}

// Python 이 아니면 지금처럼 IntelliJ 다.
func TestRightClickKeepsOtherProjectsInIntelliJ(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withPythonProject(t, false)

	m := pycharmTestModel()
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if msg := cmd(); msg != nil {
		t.Fatalf("unexpected message: %#v", msg)
	}

	want := "detached /opt/idea/bin/idea /home/u/projects/api"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("editor calls = %v, want [%q]", *calls, want)
	}
}

// PyCharm 이 없으면(제로값) 폴더를 읽지도 않고 IntelliJ 다 — Python 프로젝트여도. 이 관문이
// 손으로 만든 watchModel 의 테스트들을 파일 시스템에서 떼어 놓는다.
func TestRightClickWithoutPyCharmNeverAsksWhatTheProjectIs(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	asked := withPythonProject(t, true)

	m := intellijTestModel("idea")
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	if msg := cmd(); msg != nil {
		t.Fatalf("unexpected message: %#v", msg)
	}

	want := "detached /opt/idea/bin/idea /home/u/projects/api"
	if len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("editor calls = %v, want [%q]", *calls, want)
	}
	if len(*asked) != 0 {
		t.Errorf("read the folder with no PyCharm to open it in: %v", *asked)
	}
}

// IntelliJ 가 없어도 PyCharm 으로 열 Python 프로젝트는 연다. 그 밖의 프로젝트는 예전처럼
// IntelliJ 가 없다고 말한다.
func TestRightClickWithPyCharmButNoIntelliJ(t *testing.T) {
	for _, tc := range []struct {
		name   string
		python bool
		want   string // 띄운 것, 아니면 로그 한 줄에 든 말
	}{
		{"a Python project opens in PyCharm", true, "detached /opt/pycharm/bin/pycharm /home/u/projects/api"},
		{"anything else reports IntelliJ missing", false, "IntelliJ 실행 실패"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeTmux(t, &fakeRunner{})
			calls := withFakeEditor(t, nil)
			withPythonProject(t, tc.python)

			m := pycharmTestModel()
			m.ideaBin = ""
			_, cmd := m.Update(rightPress(firstSessionRow(m)))
			msg := cmd()

			if tc.python {
				if msg != nil || len(*calls) != 1 || (*calls)[0] != tc.want {
					t.Errorf("msg %#v, calls %v — want only [%q]", msg, *calls, tc.want)
				}
				return
			}
			failed, ok := msg.(editorFailedMsg)
			if !ok || len(*calls) != 0 {
				t.Fatalf("msg %#v, calls %v — want editorFailedMsg and nothing launched", msg, *calls)
			}
			updated, _ := m.Update(failed)
			got := updated.(watchModel)
			if len(got.events) != 1 || !strings.Contains(got.events[0].text, tc.want) {
				t.Errorf("events = %v, want a line containing %q", got.events, tc.want)
			}
		})
	}
}

// PyCharm 이 실패하면 로그는 PyCharm 이라고 말한다 — IntelliJ 라고 적으면 엉뚱한 IDE 의 로그를 뒤지게 된다.
func TestRightClickReportsAPyCharmFailure(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	withFakeEditor(t, errors.New("exit status 3"))
	withPythonProject(t, true)

	m := pycharmTestModel()
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	failed, ok := cmd().(editorFailedMsg)
	if !ok {
		t.Fatal("a failed PyCharm launch was not reported")
	}
	updated, _ := m.Update(failed)
	got := updated.(watchModel)
	if len(got.events) != 1 || !strings.Contains(got.events[0].text, "PyCharm 실행 실패") {
		t.Errorf("events = %v, want a line naming PyCharm", got.events)
	}
}

// 이미 열린 프로젝트는 어느 IDE 로 열지 묻기 전에 그 창부터 올린다 — PyCharm 창도 같은
// 올리기(focusIdea)가 맡는다.
func TestRightClickRaisesAnOpenProjectBeforeChoosingAnIDE(t *testing.T) {
	withFakeTmux(t, &fakeRunner{})
	calls := withFakeEditor(t, nil)
	withOpenIntelliJProjects(t, "/home/u/projects/api")
	asked := withPythonProject(t, true)
	var raised []string

	m := pycharmTestModel()
	m.focusIdea = fakeFocus(focusRaised, &raised)
	_, cmd := m.Update(rightPress(firstSessionRow(m)))
	cmd()
	if len(raised) != 1 || len(*calls) != 0 || len(*asked) != 0 {
		t.Errorf("raised %v, launched %v, asked %v — want raised only", raised, *calls, *asked)
	}
}
