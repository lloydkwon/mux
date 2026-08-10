package tmux

import "testing"

// Claude reports "shell" whenever a background shell is alive, and that
// outranks both "idle" and "busy" in its own precedence. A session that left a
// dev server running therefore never reaches "idle", so a turn ending there is
// invisible — which is exactly the case this demotion exists to recover. Get
// the pattern wrong in the other direction and a session that really is running
// a build gets reported as finished.
func TestDemoteServerShell(t *testing.T) {
	const snap = "/bin/bash /home/u/.claude/shell-snapshots/snapshot-bash.sh -c "

	tests := []struct {
		name string
		jobs []string
		want AIState
	}{
		{
			name: "nothing to inspect leaves the state alone",
			jobs: nil,
			want: AIStateShell,
		},
		{
			name: "a lone dev server means the turn is over",
			jobs: []string{snap + "npm run dev"},
			want: AIStateReady,
		},
		{
			name: "every launcher form counts",
			jobs: []string{
				snap + "yarn dev", snap + "pnpm run start", snap + "bun run serve",
				snap + "vite", snap + "nodemon index.js", snap + "uvicorn app:main",
				snap + "./gradlew bootRun", snap + "python manage.py runserver",
				snap + "next dev", snap + "nuxt dev", snap + "npm run watch",
			},
			want: AIStateReady,
		},
		{
			name: "a build is real work, not a parked server",
			jobs: []string{snap + "go build ./..."},
			want: AIStateShell,
		},
		{
			name: "a test run is not a server either",
			jobs: []string{snap + "npm test"},
			want: AIStateShell,
		},
		{
			name: "one real job among servers still means busy",
			jobs: []string{snap + "npm run dev", snap + "go test ./..."},
			want: AIStateShell,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := shellJobs
			shellJobs = func(int) []string { return tc.jobs }
			defer func() { shellJobs = old }()

			if got := demoteServerShell(1234); got != tc.want {
				t.Errorf("demoteServerShell = %v, want %v", got, tc.want)
			}
		})
	}
}

// The "=" prefix is the whole point: plain tmux target matching falls back to
// prefix and then glob, so "mux" would be ambiguous next to "mux-old".
func TestSwitchClientTargetsExactly(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := SwitchClient("mux"); err != nil {
			t.Fatalf("SwitchClient: %v", err)
		}
		want := "tmux switch-client -t =mux"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("ran %v, want [%q]", m.runs, want)
		}
	})
}
