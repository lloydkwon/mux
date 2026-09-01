package tmux

import (
	"fmt"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name    string
		line    string
		wantErr bool
		check   func(t *testing.T, s Session)
	}{
		{
			name: "valid line",
			line: "my-session|2|1711900000|1|/home/user/project|1711900100|bash|12345|0|1|/home/user/project",
			check: func(t *testing.T, s Session) {
				if s.Name != "my-session" {
					t.Errorf("Name = %q, want %q", s.Name, "my-session")
				}
				if s.WindowCount != 2 {
					t.Errorf("WindowCount = %d, want %d", s.WindowCount, 2)
				}
				if !s.Attached {
					t.Error("Attached = false, want true")
				}
				if s.Directory != "/home/user/project" {
					t.Errorf("Directory = %q, want %q", s.Directory, "/home/user/project")
				}
				if s.ProjectDir != "/home/user/project" {
					t.Errorf("ProjectDir = %q, want %q", s.ProjectDir, "/home/user/project")
				}
			},
		},
		{
			name: "not attached",
			line: "dev|1|1711900000|0|/tmp|1711900050|zsh|99999|0|0|",
			check: func(t *testing.T, s Session) {
				if s.Attached {
					t.Error("Attached = true, want false")
				}
			},
		},
		{
			name:    "too few fields",
			line:    "bad|line|only",
			wantErr: true,
		},
		{
			name: "session with pipe in path still works with SplitN",
			line: "test|1|" + itoa(now) + "|0|/home/user|" + itoa(now) + "|bash|123|0|0|",
			check: func(t *testing.T, s Session) {
				if s.Name != "test" {
					t.Errorf("Name = %q, want %q", s.Name, "test")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := parseLine(tt.line, nil, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}

// 좁힐지는 태그가 아니라 보고 있는 쪽이 정한다. 처음에는 태그만 보고 좁혔는데,
// 그러면 Windows Terminal 에서 연 목록까지 좁혀졌다 — 거기서 세션 매니저를 여는
// 것은 전체를 보려는 것이라 정반대다.
func TestProjectScope(t *testing.T) {
	const format = "#{client_pid}|#{@project_dir}"

	tests := map[string]struct {
		out      string
		vscode   bool
		wantDir  string
		wantOpen bool
	}{
		"VS Code 에서 보면 태그로 좁힌다": {
			out: "412|/home/u/proj\n", vscode: true,
			wantDir: "/home/u/proj", wantOpen: true,
		},
		"일반 터미널에서는 좁히지 않는다": {
			out: "412|/home/u/proj\n", vscode: false,
			wantDir: "/home/u/proj", wantOpen: false,
		},
		"VS Code 라도 태그가 없으면 좁힐 것이 없다": {
			out: "412|\n", vscode: true,
			wantDir: "", wantOpen: true,
		},
		"경로에 구분자가 들어 있어도 나머지로 받는다": {
			out: "412|/home/u/a|b\n", vscode: true,
			wantDir: "/home/u/a|b", wantOpen: true,
		},
		"클라이언트가 없으면 판단하지 않는다": {
			out: "|\n", vscode: true,
			wantDir: "", wantOpen: false,
		},
		"pid 가 숫자가 아니면 판단하지 않는다": {
			out: "none|/home/u/proj\n", vscode: true,
			wantDir: "", wantOpen: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			withMock(t, func(m *mockRunner) {
				m.OnOutput([]byte(tc.out), nil, "tmux", "display-message", "-p", format)
				old := clientEnvHas
				clientEnvHas = func(int, string) bool { return tc.vscode }
				defer func() { clientEnvHas = old }()

				dir, open := ProjectScope()
				if dir != tc.wantDir || open != tc.wantOpen {
					t.Errorf("ProjectScope() = (%q, %v), want (%q, %v)", dir, open, tc.wantDir, tc.wantOpen)
				}
			})
		})
	}

	// 조회 자체가 실패하면 좁히지 않는다 — 목록을 감추는 쪽이 늘 더 나쁘다.
	withMock(t, func(m *mockRunner) {
		if dir, open := ProjectScope(); dir != "" || open {
			t.Errorf("ProjectScope() = (%q, %v) on a failed lookup, want empty and false", dir, open)
		}
	})
}
