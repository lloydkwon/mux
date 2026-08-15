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
			line: "my-session|2|1711900000|1|/home/user/project|1711900100|bash|12345|0|1",
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
			},
		},
		{
			name: "not attached",
			line: "dev|1|1711900000|0|/tmp|1711900050|zsh|99999|0|0",
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
			line: "test|1|" + itoa(now) + "|0|/home/user|" + itoa(now) + "|bash|123|0|0",
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
