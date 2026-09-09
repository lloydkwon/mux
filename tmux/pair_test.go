package tmux

import (
	"strings"
	"testing"
)

func pairSess(names ...string) []Session {
	ss := make([]Session, 0, len(names))
	for _, n := range names {
		ss = append(ss, Session{Name: n})
	}
	return ss
}

func TestFindPairs(t *testing.T) {
	got := FindPairs(pairSess(
		"matching-exec", "matching-plan",
		"govgen-exec", "govgen-plan",
		"wai-exec",    // no partner
		"lonely-plan", // no partner
		"my-mux",
	), "-exec", "-plan")

	if len(got) != 2 {
		t.Fatalf("pairs = %v, want the two complete ones", got)
	}
	// Sorted by base, so the order is something a caller can rely on.
	if got[0].Base != "govgen" || got[1].Base != "matching" {
		t.Errorf("pairs = %v, want them sorted by base", got)
	}
	if got[1].Exec != "matching-exec" || got[1].Plan != "matching-plan" {
		t.Errorf("pair = %+v, want both halves named", got[1])
	}
}

// A suffix that is a whole name would make "-exec" pair with "-plan", which are
// not a pair — they are two sessions someone named badly.
func TestFindPairsIgnoresAnEmptyBase(t *testing.T) {
	if got := FindPairs(pairSess("-exec", "-plan"), "-exec", "-plan"); len(got) != 0 {
		t.Errorf("pairs = %v, want none", got)
	}
}

func TestPairSuffixesFallBackRatherThanTurningOff(t *testing.T) {
	cases := map[string]string{
		"":            "default", // nobody set it
		"garbage":     "default", // no comma
		",-plan":      "default", // half missing
		"-same,-same": "default", // both halves identical would pair a session with itself
		"-a,-b":       "-a/-b",
	}
	for raw, want := range cases {
		withMock(t, func(m *mockRunner) {
			m.OnOutput([]byte(raw+"\n"), nil, "tmux", "show-options", "-gqv", pairSuffixOption)
			e, p := PairSuffixes()
			got := e + "/" + p
			if want == "default" {
				got = "default"
				if e != defaultExecSuffix || p != defaultPlanSuffix {
					got = e + "/" + p
				}
			}
			if got != want {
				t.Errorf("PairSuffixes(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}

// Pulling a pair apart is something a person does on purpose. mux joining them
// again on its next launch would be arguing with them.
func TestPairablePairsSkipsWhatWasAlreadyJoined(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", pairSuffixOption)
		m.OnOutput([]byte("matching\n"), nil, "tmux", "show-options", "-gqv", pairedOption)

		got := PairablePairs(pairSess(
			"matching-exec", "matching-plan",
			"govgen-exec", "govgen-plan",
		))
		if len(got) != 1 || got[0].Base != "govgen" {
			t.Errorf("pairable = %v, want only the one mux has not joined", got)
		}
	})
}

func TestJoinPairPutsTheReviewPaneBesideTheWork(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%2 claude --resume abc\n%3 mux watch\n"), nil,
			"tmux", "list-panes", "-t", "matching-plan", "-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("%0 claude --resume def\n%1 mux watch\n"), nil,
			"tmux", "list-panes", "-t", "matching-exec", "-F", "#{pane_id} #{pane_start_command}")
		m.OnOutput([]byte("\n"), nil, "tmux", "show-options", "-gqv", pairedOption)

		if err := JoinPair(Pair{Base: "matching", Exec: "matching-exec", Plan: "matching-plan"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := "tmux join-pane -d -h -s %2 -t %0"
		var joined, marked bool
		for _, r := range m.runs {
			if r == want {
				joined = true
			}
			if strings.HasPrefix(r, "tmux set-option -g "+pairedOption) {
				marked = true
			}
		}
		if !joined {
			t.Errorf("runs = %v, want %q — the work pane, not the panel, and without stealing focus", m.runs, want)
		}
		if !marked {
			t.Error("the pair was joined but not remembered; the next launch would join it again")
		}
	})
}

// Already joined: the review session has nothing but its panel left.
func TestJoinPairIsANoOpWhenThePaneHasAlreadyMoved(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%3 mux watch\n"), nil,
			"tmux", "list-panes", "-t", "matching-plan", "-F", "#{pane_id} #{pane_start_command}")

		if err := JoinPair(Pair{Base: "matching", Exec: "matching-exec", Plan: "matching-plan"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, r := range m.runs {
			if strings.Contains(r, "join-pane") {
				t.Errorf("joined anyway: %q", r)
			}
		}
	})
}

// Moving the only pane out of a session ends it, and keeping the review session
// alive is the whole shape the user asked for.
func TestJoinPairRefusesToEndTheReviewSession(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("%2 claude --resume abc\n"), nil,
			"tmux", "list-panes", "-t", "matching-plan", "-F", "#{pane_id} #{pane_start_command}")

		err := JoinPair(Pair{Base: "matching", Exec: "matching-exec", Plan: "matching-plan"})
		if err == nil {
			t.Fatal("moved the review session's only pane, which would have killed it")
		}
		if !strings.Contains(err.Error(), "panel") {
			t.Errorf("error = %v, want it to say why", err)
		}
	})
}
