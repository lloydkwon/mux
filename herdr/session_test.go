package herdr

import (
	"testing"
	"time"

	"github.com/lloydkwon/mux/tmux"
)

// withFixtures loads the captured snapshot and pins the worktree lookup, so the
// mapping is measured against the fixture alone and not against whatever
// directories happen to exist on the machine running the test.
func withFixtures(t *testing.T, fn func(ss []tmux.Session)) {
	t.Helper()
	old := isWorktree
	isWorktree = func(string) bool { return false }
	defer func() { isWorktree = old }()

	withMock(t, func(m *mockRunner) {
		seedSnapshot(t, m)
		ss, err := Sessions()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		fn(ss)
	})
}

func find(ss []tmux.Session, name string) (tmux.Session, bool) {
	for _, s := range ss {
		if s.Name == name {
			return s, true
		}
	}
	return tmux.Session{}, false
}

func TestSessionsFromFixtures(t *testing.T) {
	withFixtures(t, func(ss []tmux.Session) {
		if len(ss) != 6 {
			t.Fatalf("sessions = %d, want 6", len(ss))
		}

		// The focused workspace leads, the way an attached tmux session does.
		if ss[0].Name != "my-mux" {
			t.Errorf("first session = %q, want the focused one (my-mux)", ss[0].Name)
		}
		if !ss[0].Attached {
			t.Error("the focused workspace did not map to Attached")
		}

		mux, ok := find(ss, "my-mux")
		if !ok {
			t.Fatal("my-mux is missing")
		}
		if mux.GitBranch != "main" {
			t.Errorf("branch = %q, want %q", mux.GitBranch, "main")
		}
		if mux.Directory != "/home/lloydkwon/dev/projects/my/mux" {
			t.Errorf("directory = %q, want the pane's cwd", mux.Directory)
		}
		// The workspace carries no cwd of its own, so ProjectDir falls back to
		// the directory rather than staying empty and un-filterable.
		if mux.ProjectDir != mux.Directory {
			t.Errorf("project dir = %q, want it to fall back to %q", mux.ProjectDir, mux.Directory)
		}
		if mux.AITool != "claude" {
			t.Errorf("tool = %q, want %q", mux.AITool, "claude")
		}
		if mux.AIState != tmux.AIStateWorking {
			t.Errorf("state = %v, want working", mux.AIState)
		}
		if mux.AISince.IsZero() {
			t.Error("AISince is zero; the elapsed column would fall back to Created")
		}
	})
}

// A zero Created reaches compactAgo as time.Since(year 1) and renders
// "740049d" — seven cells in a four-cell column, and timeAgo's %4s pads without
// truncating, so every column after it shifts. herdr publishes no creation
// time, so the mapping has to supply one for every session, every time.
func TestNoSessionHasAZeroCreated(t *testing.T) {
	withFixtures(t, func(ss []tmux.Session) {
		for _, s := range ss {
			if s.Created.IsZero() {
				t.Errorf("%s has a zero Created — the elapsed column would render ~740000d", s.Name)
			}
		}
	})
}

func TestCreatedFallsBackToTheSnapshotClockWithNoPanes(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	if got := createdFor(nil, now); !got.Equal(now) {
		t.Errorf("createdFor(no panes) = %v, want the snapshot clock %v", got, now)
	}
	ps := []paneInfo{
		{StateChangedAtUnixMs: 1000},
		{StateChangedAtUnixMs: 5000},
		{StateChangedAtUnixMs: 3000},
	}
	if got := createdFor(ps, now); !got.Equal(time.UnixMilli(5000)) {
		t.Errorf("createdFor = %v, want the newest state change", got)
	}
}

// idle is the judgement call: mapping it to Ready as well would put ✅ on five
// of the nine live agents and the badge would stop distinguishing anything.
func TestMapState(t *testing.T) {
	cases := map[string]tmux.AIState{
		"working":                  tmux.AIStateWorking,
		"blocked":                  tmux.AIStateApproval,
		"done":                     tmux.AIStateReady,
		"idle":                     tmux.AIStateNone,
		"unknown":                  tmux.AIStateNone,
		"":                         tmux.AIStateNone,
		"whatever-herdr-adds-next": tmux.AIStateNone,
	}
	for status, want := range cases {
		if got := mapState(status); got != want {
			t.Errorf("mapState(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestWindowsCarryAgentNamesAndState(t *testing.T) {
	withFixtures(t, func(ss []tmux.Session) {
		gov, ok := find(ss, "govgen")
		if !ok {
			t.Fatal("govgen is missing")
		}
		if len(gov.Windows) != 2 {
			t.Fatalf("windows = %d, want 2", len(gov.Windows))
		}
		if gov.Windows[0].Index != 1 || gov.Windows[1].Index != 2 {
			t.Errorf("indices = %d,%d, want 1,2 from the pane ids",
				gov.Windows[0].Index, gov.Windows[1].Index)
		}
		if gov.Windows[1].Name != "govgen-plan" {
			t.Errorf("name = %q, want herdr's agent name", gov.Windows[1].Name)
		}
		if gov.Windows[1].AIState != tmux.AIStateReady {
			t.Errorf("state = %v, want ready (herdr said done)", gov.Windows[1].AIState)
		}
		if gov.Windows[1].AITool != "claude" {
			t.Errorf("tool = %q, want claude", gov.Windows[1].AITool)
		}
	})
}

// Measured: this repository's own pane carries no herdr name, so the fallback
// chain is load-bearing rather than defensive.
func TestWindowNameFallsBackWhenHerdrHasNoName(t *testing.T) {
	withFixtures(t, func(ss []tmux.Session) {
		mux, ok := find(ss, "my-mux")
		if !ok {
			t.Fatal("my-mux is missing")
		}
		if len(mux.Windows) != 1 {
			t.Fatalf("windows = %d, want 1", len(mux.Windows))
		}
		if got := mux.Windows[0].Name; got == "" {
			t.Error("an unnamed agent produced an empty row label")
		}
	})

	// The rest of the chain, which the fixture cannot reach.
	if got := windowName(paneInfo{PaneID: "w1:p3", Agent: "codex"}, ""); got != "codex" {
		t.Errorf("name = %q, want the tool name", got)
	}
	if got := windowName(paneInfo{PaneID: "w1:p3"}, ""); got != "p3" {
		t.Errorf("name = %q, want the pane number for a plain shell", got)
	}
}

// A pane hosting no agent is still a pane, and dropping it would lose a row the
// user can see in herdr. Measured: pane list 10, agent list 9.
func TestPaneWithNoAgentStillGetsARow(t *testing.T) {
	withFixtures(t, func(ss []tmux.Session) {
		dyfa, ok := find(ss, "dyfa-front")
		if !ok {
			t.Fatal("dyfa-front is missing")
		}
		if len(dyfa.Windows) != 1 {
			t.Fatalf("windows = %d, want 1 even with no agent", len(dyfa.Windows))
		}
		if dyfa.Windows[0].AIState != tmux.AIStateNone {
			t.Errorf("state = %v, want none", dyfa.Windows[0].AIState)
		}
		if dyfa.AITool != "" {
			t.Errorf("tool = %q, want empty for a workspace with no agent", dyfa.AITool)
		}
	})
}

// Session.Name is the key pruneCaches, preferences.Orders and focusSession all
// use, so two workspaces sharing a label must not share a name.
func TestDuplicateLabelsGetDistinctNames(t *testing.T) {
	snap := &snapshot{
		workspaces: []workspaceInfo{
			{WorkspaceID: "w1", Number: 1, Label: "api"},
			{WorkspaceID: "w2", Number: 2, Label: "api"},
			{WorkspaceID: "w3", Number: 3, Label: ""},
		},
	}
	names := sessionNames(snap.workspaces)
	if names["w1"] != "api" {
		t.Errorf("first = %q, want the plain label", names["w1"])
	}
	if names["w2"] != "api#2" {
		t.Errorf("second = %q, want a distinct name", names["w2"])
	}
	if names["w3"] != "w3" {
		t.Errorf("unlabelled = %q, want the workspace id", names["w3"])
	}
}

// The preview has to be asked for a herdr pane id, which is what the tree's
// (session, window) pair has to resolve back to.
func TestPaneIDResolvesBackFromTheTree(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		seedSnapshot(t, m)

		got, err := PaneID("govgen", 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "wE:p2" {
			t.Errorf("pane id = %q, want %q", got, "wE:p2")
		}
		// -1 means "the active one", the same as previewKey's.
		if got, _ = PaneID("govgen", -1); got == "" {
			t.Error("the session-level preview resolved to no pane")
		}
		if got, _ = PaneID("no-such-session", 1); got != "" {
			t.Errorf("a missing session resolved to %q", got)
		}
	})
}

// The lead pane is what supplies the session's directory, task and age, so its
// choice has to be deterministic rather than "whichever came first".
func TestLeadPanePrefersTheRowWorthActingOn(t *testing.T) {
	ps := []paneInfo{
		{PaneID: "w1:p1", AgentStatus: "working", StateChangedAtUnixMs: 900},
		{PaneID: "w1:p2", AgentStatus: "blocked", StateChangedAtUnixMs: 100},
		{PaneID: "w1:p3", AgentStatus: "done", StateChangedAtUnixMs: 999},
	}
	lead, ok := leadPane(ps)
	if !ok || lead.PaneID != "w1:p2" {
		t.Errorf("lead = %q, want the blocked pane", lead.PaneID)
	}

	tie := []paneInfo{
		{PaneID: "w1:p1", AgentStatus: "idle", StateChangedAtUnixMs: 100},
		{PaneID: "w1:p2", AgentStatus: "idle", StateChangedAtUnixMs: 500},
	}
	lead, _ = leadPane(tie)
	if lead.PaneID != "w1:p2" {
		t.Errorf("lead = %q, want the most recently changed of equals", lead.PaneID)
	}

	if _, ok := leadPane(nil); ok {
		t.Error("an empty workspace produced a lead pane")
	}
}
