package tmux

import (
	"os"
	"path/filepath"
	"strings"
)

// 프로젝트의 언어. `mux border` 가 작업 pane 위의 한 줄에서 폴더 옆에 적고(ui/border.go), 패널의
// 오른쪽 클릭이 Python 프로젝트를 PyCharm 으로 열지 정할 때 쓴다(ui/editor.go).
//
// 빌드·의존성 파일의 **이름만** 본다. 소스 파일을 세지 않는 것은 `mux border` 가 pane 마다,
// 갱신마다 새로 뜨는 프로세스라 캐시할 곳이 없어서다 — 폴더 하나에 ReadDir 한 번이면 끝나야
// 한다. 그리고 프로젝트가 스스로 밝힌 것이 확장자를 센 것보다 정확하다: 스크립트 몇 개가 든
// Gradle 저장소는 Python 프로젝트가 아니다.

// LangPython is the label of a Python project — the one label code compares
// against (the panel's PyCharm rule).
const LangPython = "Python"

// langJava is what a JVM build file names, unless the sources say Kotlin.
const langJava = "Java"

// languageRule names a language by the files a project of it carries at its
// top: any of names, or any file ending in one of suffixes.
type languageRule struct {
	label    string
	names    []string
	suffixes []string
}

// languageRules are tried in order, and the first that matches names the
// language. The order is the decision for a folder that carries more than one:
// a JVM build file beats a Python script's requirements.txt, and a language's
// own build file beats package.json, which many projects carry only for their
// tooling. TypeScript is asked before JavaScript because its projects carry
// package.json too.
var languageRules = []languageRule{
	{label: langJava, names: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}},
	{label: "Go", names: []string{"go.mod"}},
	{label: "Rust", names: []string{"Cargo.toml"}},
	{label: LangPython, names: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile", "uv.lock", "poetry.lock", ".python-version"}},
	{label: "C#", suffixes: []string{".sln", ".csproj"}},
	{label: "Swift", names: []string{"Package.swift"}},
	{label: "Dart", names: []string{"pubspec.yaml"}},
	{label: "Ruby", names: []string{"Gemfile"}},
	{label: "PHP", names: []string{"composer.json"}},
	{label: "Elixir", names: []string{"mix.exs"}},
	{label: "TypeScript", names: []string{"tsconfig.json"}},
	{label: "JavaScript", names: []string{"package.json"}},
	{label: "C/C++", names: []string{"CMakeLists.txt"}},
}

// 빌드 파일 없는 Python 도 흔하다 — 스크립트 몇 개와 virtualenv 로 된 도구(2026-11-02
// infra-monitor: monitor.py, monitor_app/*.py, .venv). 그래서 빌드 파일 규칙이 모두 빗나간 뒤에만
// 약한 표식을 본다: 맨 위의 *.py·*.pyw 파일, 또는 virtualenv(.venv·venv 안의 pyvenv.cfg — 이름만
// 같은 폴더가 아니라 venv·uv 가 만든 것). 약한 표식은 어떤 빌드 파일에도 진다 — Go 저장소의
// 도우미 스크립트 하나가 그 저장소를 Python 으로 만들지 않는다.

// pythonScriptSuffixes are the loose Python files that are a weak sign at a
// folder's top.
var pythonScriptSuffixes = []string{".py", ".pyw"}

// venvDirs are where a project keeps its virtualenv; one holding pyvenv.cfg is
// a weak sign of Python.
var venvDirs = []string{".venv", "venv"}

// LanguageAt is the language of the project whose top is dir, or "" when dir
// carries no sign mux knows. Only dir itself is read — see ProjectLanguage for
// a folder inside a project.
func LanguageAt(dir string) string {
	lang, _, _ := scanLanguage(dir)
	return lang
}

// ProjectLanguage is the language of the project dir is in: dir's own, else
// the nearest parent's, climbing no further than the repository's top (the
// folder holding .git — a directory, or a file in a worktree). A pane's folder
// is often below its project's top, because the shell wandered into ui/; a
// folder outside the repository is somebody else's project.
//
// The home directory is never read, and the climb ends there: a global
// .python-version (pyenv) or a stray package.json in $HOME would otherwise name
// every shell on the machine. Relative paths answer "" — tmux reports absolute
// ones, and anything else is not a place to start climbing from.
func ProjectLanguage(dir string) string {
	if !filepath.IsAbs(dir) {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home) // $HOME may end in "/", and d never will
	}
	// A weak sign does not end the climb: a build file further up still wins,
	// so the scripts/ folder of a Go repository stays Go. The nearest weak sign
	// is the answer only when no build file turns up before the climb ends.
	nearestWeak := ""
	for d := filepath.Clean(dir); d != "/" && d != home; {
		lang, weak, repoTop := scanLanguage(d)
		if lang != "" && !weak {
			return lang
		}
		if lang != "" && nearestWeak == "" {
			nearestWeak = lang
		}
		if repoTop {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return nearestWeak
}

// scanLanguage reads dir once and reports the language its build files name —
// failing that, the one its weak signs suggest (weak true) — and whether it is
// a repository's top. Folders named like a build file are not build files.
func scanLanguage(dir string) (lang string, weak, repoTop bool) {
	if dir == "" {
		return "", false, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, false
	}
	files := make(map[string]bool, len(entries))
	dirs := make(map[string]bool)
	for _, e := range entries {
		if e.Name() == ".git" {
			repoTop = true
		}
		if e.IsDir() {
			dirs[e.Name()] = true
		} else {
			files[e.Name()] = true
		}
	}
	for _, r := range languageRules {
		if r.matches(files) {
			if r.label == langJava && isDir(filepath.Join(dir, "src", "main", "kotlin")) {
				return "Kotlin", false, repoTop
			}
			return r.label, false, repoTop
		}
	}
	if looksLikePython(dir, files, dirs) {
		return LangPython, true, repoTop
	}
	return "", false, repoTop
}

// looksLikePython reports the weak signs of a Python project at dir's top: a
// loose script, or a virtualenv.
func looksLikePython(dir string, files, dirs map[string]bool) bool {
	if (languageRule{suffixes: pythonScriptSuffixes}).matches(files) {
		return true
	}
	for _, v := range venvDirs {
		if dirs[v] && fileExists(filepath.Join(dir, v, "pyvenv.cfg")) {
			return true
		}
	}
	return false
}

func (r languageRule) matches(files map[string]bool) bool {
	for _, n := range r.names {
		if files[n] {
			return true
		}
	}
	if len(r.suffixes) == 0 {
		return false
	}
	for f := range files {
		for _, suf := range r.suffixes {
			if strings.HasSuffix(f, suf) {
				return true
			}
		}
	}
	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
