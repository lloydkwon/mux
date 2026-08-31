package ui

import (
	"testing"

	"github.com/lloydkwon/mux/tmux"
)

// 프로젝트 스코프의 핵심: pane이 다른 프로젝트로 옮겨간 세션도 자기 프로젝트
// 필터에는 남아야 한다. Directory는 pane을 따라 움직이지만 @project_dir은 안
// 움직이므로, 이 대조가 빠지면 세션이 자기 목록에서 사라진다.
func TestFilterMatchesProjectDirWhenDirectoryMoved(t *testing.T) {
	m := Model{tree: newTreeState()}
	m.sessions = []tmux.Session{
		{Name: "my-mux", Directory: "/home/u/dev/my/erp", ProjectDir: "/home/u/dev/my/mux"},
		{Name: "my-erp", Directory: "/home/u/dev/my/erp", ProjectDir: "/home/u/dev/my/erp"},
		{Name: "stray", Directory: "/tmp"},
	}
	m.filterText = "/home/u/dev/my/mux"
	m.applyFilter()

	if len(m.filtered) != 1 || m.filtered[0].Name != "my-mux" {
		t.Fatalf("filtered = %v, want only my-mux", names(m.filtered))
	}
}

// 자동 시딩된 필터는 사용자가 친 필터가 아니므로 액션 행이 계속 보여야 한다.
func TestSeededProjectFilterKeepsActionRows(t *testing.T) {
	m := Model{tree: newTreeState(), projectDir: "/home/u/dev/my/mux"}
	m.filterText = m.projectDir
	m.sessions = []tmux.Session{{Name: "my-mux", ProjectDir: "/home/u/dev/my/mux"}}
	m.applyFilter()

	var actions int
	for _, it := range m.items {
		if it.kind == itemNewShell || it.kind == itemNewSession {
			actions++
		}
	}
	if actions == 0 {
		t.Error("시딩된 프로젝트 필터에서 액션 행이 사라졌다")
	}

	// 사용자가 직접 친 필터에서는 지금까지대로 감춘다
	m.filterText = "my"
	m.applyFilter()
	for _, it := range m.items {
		if it.kind == itemNewShell || it.kind == itemNewSession {
			t.Fatal("사용자 필터에서는 액션 행이 보이면 안 된다")
		}
	}
}

func names(ss []tmux.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
