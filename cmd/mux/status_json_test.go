package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// 공개 JSON 스키마 핀: 필드명이 바뀌면 외부 위젯(my-mux)이 깨진다.
func TestSessionsJSON(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		b, err := sessionsJSON(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "[]" {
			t.Errorf("empty sessions = %s, want []", b)
		}
	})

	t.Run("session with AI state", func(t *testing.T) {
		since := time.UnixMilli(1754800000000)
		b, err := sessionsJSON([]tmux.Session{{
			Name:         "work",
			Attached:     true,
			Directory:    "/home/u/proj",
			GitBranch:    "main",
			IsWorktree:   true,
			AIState:      tmux.AIStateApproval,
			AIWaitingFor: "Bash: rm -rf",
			AISince:      since,
			AIPID:        4242,
		}}, map[string][]string{"work": {"/other/peek", "/home/u/proj"}})
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		want := map[string]any{
			"name":       "work",
			"attached":   true,
			"dir":        "/home/u/proj",
			"branch":     "main",
			"worktree":   true,
			"tool":       "claude",
			"state":      "approval",
			"waitingFor": "Bash: rm -rf",
			"since":      float64(1754800000000),
			"pid":        float64(4242),
			"vscodeDir":  "/home/u/proj",
		}
		for k, w := range want {
			if got[0][k] != w {
				t.Errorf("%s = %v, want %v", k, got[0][k], w)
			}
		}
	})

	t.Run("plain session omits AI fields", func(t *testing.T) {
		b, err := sessionsJSON([]tmux.Session{{Name: "shell", Directory: "/tmp"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"tool", "state", "waitingFor", "since", "branch", "pid", "worktree", "vscodeDir"} {
			if _, ok := got[0][k]; ok {
				t.Errorf("field %s should be omitted, got %v", k, got[0][k])
			}
		}
	})

	t.Run("vscodeDir dropped when session dir is outside the window folder", func(t *testing.T) {
		b, err := sessionsJSON([]tmux.Session{{Name: "peek", Directory: "/home/u/other"}},
			map[string][]string{"peek": {"/home/u/proj"}})
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if v, ok := got[0]["vscodeDir"]; ok {
			t.Errorf("vscodeDir = %v, want omitted (다른 프로젝트 창에서의 attach)", v)
		}
	})
}
