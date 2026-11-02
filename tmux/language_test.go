package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// makeTree 는 root 아래에 files(빈 파일)와 dirs(폴더)를 만든다. 경로는 root 기준 상대 경로다.
func makeTree(t *testing.T, root string, files, dirs []string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 언어는 프로젝트가 스스로 밝힌 빌드·의존성 파일로 정한다. 섞인 폴더는 표의 순서가 정한다:
// JVM 빌드 파일이 Python 의 requirements.txt 를 이기고(패널의 PyCharm 규칙과 같다), 언어 자신의
// 빌드 파일이 도구용으로 흔히 딸려 오는 package.json 을 이긴다.
func TestLanguageAt(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		dirs  []string
		want  string
	}{
		{"go.mod", []string{"go.mod"}, nil, "Go"},
		{"maven", []string{"pom.xml"}, nil, "Java"},
		{"gradle", []string{"build.gradle", "settings.gradle"}, nil, "Java"},
		{"gradle kts without kotlin sources", []string{"build.gradle.kts"}, []string{"src/main/java"}, "Java"},
		{"gradle with kotlin sources", []string{"build.gradle.kts"}, []string{"src/main/kotlin"}, "Kotlin"},
		{"gradle beside a requirements file", []string{"build.gradle", "requirements.txt"}, nil, "Java"},
		{"cargo", []string{"Cargo.toml"}, nil, "Rust"},
		{"pyproject", []string{"pyproject.toml"}, nil, "Python"},
		{"requirements", []string{"requirements.txt"}, nil, "Python"},
		{"uv lock", []string{"uv.lock"}, nil, "Python"},
		{"python version", []string{".python-version"}, nil, "Python"},
		{"python beside package.json", []string{"pyproject.toml", "package.json"}, nil, "Python"},
		{"go beside package.json", []string{"go.mod", "package.json"}, nil, "Go"},
		{"csproj", []string{"App.csproj"}, nil, "C#"},
		{"solution", []string{"Mux.sln"}, nil, "C#"},
		{"swift", []string{"Package.swift"}, nil, "Swift"},
		{"dart", []string{"pubspec.yaml"}, nil, "Dart"},
		{"ruby", []string{"Gemfile"}, nil, "Ruby"},
		{"php", []string{"composer.json"}, nil, "PHP"},
		{"elixir", []string{"mix.exs"}, nil, "Elixir"},
		{"typescript", []string{"package.json", "tsconfig.json"}, nil, "TypeScript"},
		{"javascript", []string{"package.json"}, nil, "JavaScript"},
		{"cmake", []string{"CMakeLists.txt"}, nil, "C/C++"},
		// 빌드 파일 없는 Python — 스크립트와 virtualenv 만 있는 프로젝트(infra-monitor). 약한 표식이라
		// 빌드 파일 규칙이 모두 빗나간 뒤에만 본다.
		{"a script at the top", []string{"monitor.py", "README.md"}, nil, "Python"},
		{"a pyw at the top", []string{"run-monitor.pyw"}, nil, "Python"},
		{"a virtualenv", []string{".venv/pyvenv.cfg"}, nil, "Python"},
		{"a virtualenv named venv", []string{"venv/pyvenv.cfg"}, nil, "Python"},
		{"a folder named .venv that is no virtualenv", nil, []string{".venv"}, ""},
		{"a script beside go.mod", []string{"go.mod", "tool.py"}, nil, "Go"},
		{"a script beside package.json", []string{"package.json", "gen.py"}, nil, "JavaScript"},
		{"a virtualenv beside gradle", []string{"build.gradle", ".venv/pyvenv.cfg"}, nil, "Java"},
		{"a folder named like a script", nil, []string{"tool.py"}, ""},
		{"nothing", []string{"README.md"}, nil, ""},
		{"a folder named like a marker", nil, []string{"go.mod"}, ""},
		{"markers only below the top", []string{"ai/pyproject.toml", "backend/build.gradle"}, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			makeTree(t, dir, c.files, c.dirs)
			if got := LanguageAt(dir); got != c.want {
				t.Errorf("LanguageAt = %q, want %q", got, c.want)
			}
		})
	}

	if got := LanguageAt(""); got != "" {
		t.Errorf(`LanguageAt("") = %q`, got)
	}
	if got := LanguageAt(filepath.Join(t.TempDir(), "gone")); got != "" {
		t.Errorf("a folder that is not there = %q", got)
	}
}

// pane 의 폴더는 프로젝트의 하위 폴더일 때가 많다(셸이 ui/ 로 들어가 있다). 그래서 위로 올라가며
// 찾되, 저장소 맨 위(.git 이 있는 곳)에서 멈춘다 — 저장소 밖의 폴더는 그 프로젝트가 아니다.
// 홈은 읽지 않는다: pyenv 의 전역 .python-version 같은 것이 홈의 모든 셸을 Python 으로 만든다.
func TestProjectLanguage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	makeTree(t, home,
		[]string{
			".python-version",               // 홈의 것 — 읽지 않는다
			"mux/go.mod", "mux/ui/watch.go", // 하위 폴더에서 올라가 찾는다
			"outer/go.mod", "outer/repo/README", // repo 는 저장소 — 그 밖의 go.mod 는 남의 것
			"loose/sub/notes.md",                // 저장소도 표식도 없이 홈까지 — 없음
			"wtouter/go.mod", "wtouter/wt/.git", // worktree 의 .git 은 파일이다 — 그래도 저장소 맨 위
			"mux/scripts/gen.py",                 // 위의 go.mod 가 아래의 스크립트를 이긴다
			"pyrepo/app.py", "pyrepo/pkg/mod.py", // 빌드 파일 없는 저장소 — 가장 가까운 약한 표식
			"loosepy/tool/run.py", // 저장소 밖에서도 약한 표식은 답이 된다
		},
		[]string{"mux/.git", "outer/repo/.git", "outer/repo/src", "wtouter/wt/sub", "pyrepo/.git"},
	)

	for _, c := range []struct {
		dir, want string
	}{
		{filepath.Join(home, "mux"), "Go"},
		{filepath.Join(home, "mux", "ui"), "Go"},
		{filepath.Join(home, "mux", "ui") + "/", "Go"},
		{filepath.Join(home, "outer", "repo", "src"), ""},
		{filepath.Join(home, "loose", "sub"), ""},
		{filepath.Join(home, "wtouter", "wt", "sub"), ""},
		{filepath.Join(home, "mux", "scripts"), "Go"},
		{filepath.Join(home, "pyrepo", "pkg"), "Python"},
		{filepath.Join(home, "loosepy", "tool"), "Python"},
		{home, ""},
		{"", ""},
		{"relative/path", ""},
		{"/", ""},
	} {
		if got := ProjectLanguage(c.dir); got != c.want {
			t.Errorf("ProjectLanguage(%q) = %q, want %q", c.dir, got, c.want)
		}
	}

	// $HOME 이 "/" 로 끝나도 홈에서 멈춘다.
	t.Setenv("HOME", home+"/")
	if got := ProjectLanguage(filepath.Join(home, "loose", "sub")); got != "" {
		t.Errorf("climbed into a home written with a trailing slash: %q", got)
	}
}
