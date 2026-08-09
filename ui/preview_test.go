package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/xguru/mux/tmux"
)

func TestShortenPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{home + "/projects/foo", "~/projects/foo"},
		{home, "~"},
		{"/tmp/other", "/tmp/other"},
		{"", ""},
	}

	for _, tt := range tests {
		got := shortenPath(tt.input)
		if got != tt.want {
			t.Errorf("shortenPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestShortenPathTruncatesLong(t *testing.T) {
	long := "/very/long/path/that/exceeds/thirty/five/characters/definitely"
	got := shortenPath(long)
	if len(got) > 35 {
		t.Errorf("shortenPath should truncate to 35 chars, got len=%d: %q", len(got), got)
	}
	if !strings.HasPrefix(got, "...") {
		t.Errorf("truncated path should start with '...', got %q", got)
	}
}

func TestAiLabelPlain(t *testing.T) {
	// Known commands should return non-empty
	for _, cmd := range []string{"claude", "codex", "aider", "gemini"} {
		info := aiLabelPlain(tmux.Session{ActiveCommand: cmd})
		if info.styled == "" {
			t.Errorf("aiLabelPlain(%q) returned empty styled", cmd)
		}
		if info.text == "" {
			t.Errorf("aiLabelPlain(%q) returned empty text", cmd)
		}
	}
	// Unknown commands should return empty
	info := aiLabelPlain(tmux.Session{ActiveCommand: "bash"})
	if info.styled != "" {
		t.Errorf("aiLabelPlain(%q) styled = %q, want empty", "bash", info.styled)
	}

	// A live Claude session is surfaced even when the active pane is a shell,
	// because Claude may be running in another window.
	info = aiLabelPlain(tmux.Session{
		ActiveCommand: "bash",
		ClaudeState:   tmux.ClaudeStateWorking,
	})
	if info.styled == "" {
		t.Error("a live Claude state should produce a badge regardless of ActiveCommand")
	}
}

func TestFormatStatusLine(t *testing.T) {
	now := time.Now()
	blocked := tmux.Session{
		ClaudeState:      tmux.ClaudeStateApproval,
		ClaudeWaitingFor: "permission prompt",
		ClaudeSince:      now.Add(-3 * time.Minute),
	}
	usage := &tmux.TokenUsage{InputTokens: 1200, OutputTokens: 890, TotalCost: 0.12}

	if got := formatStatusLine(tmux.Session{}, nil, 70); got != "" {
		t.Errorf("a session with neither state nor usage should render nothing, got %q", got)
	}

	// Wide preview: state, reason, elapsed and cost all fit.
	wide := ansi.Strip(formatStatusLine(blocked, usage, 70))
	if ansi.StringWidth(wide) != 70 {
		t.Errorf("status line measures %d cells, want 70: %q", ansi.StringWidth(wide), wide)
	}
	for _, want := range []string{"approval", "permission prompt", "3m", "~$0.12"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide status line missing %q: %q", want, wide)
		}
	}

	// Narrow preview: the blocking reason outranks the token cost.
	narrow := ansi.Strip(formatStatusLine(blocked, usage, 46))
	if ansi.StringWidth(narrow) != 46 {
		t.Errorf("narrow status line measures %d cells, want 46: %q", ansi.StringWidth(narrow), narrow)
	}
	if !strings.Contains(narrow, "permission prompt") {
		t.Errorf("narrow status line dropped the reason: %q", narrow)
	}
	if strings.Contains(narrow, "~$") {
		t.Errorf("narrow status line should drop token cost before the reason: %q", narrow)
	}

	// Token usage alone still renders, as it did before states existed.
	only := ansi.Strip(formatStatusLine(tmux.Session{}, usage, 70))
	if !strings.Contains(only, "~$0.12") {
		t.Errorf("token-only status line missing cost: %q", only)
	}
}

// The header must keep its exact line budget whether or not a status row is
// present, in both the 2-line and 3-line branches.
func TestRenderPreviewLineCountWithClaudeState(t *testing.T) {
	session := tmux.Session{
		Name:        "mux",
		Directory:   "/home/user/mux",
		ClaudeState: tmux.ClaudeStateWorking,
		ClaudeSince: time.Now().Add(-time.Minute),
	}
	item := &listItem{kind: itemSession, session: &session}

	for _, usage := range []*tmux.TokenUsage{nil, {InputTokens: 10, OutputTokens: 5}} {
		for _, height := range []int{10, 20, 40} {
			out := renderPreview(item, "line one\nline two", 60, height, usage)
			if got := len(strings.Split(out, "\n")); got != height {
				t.Errorf("usage=%v height=%d: got %d lines", usage != nil, height, got)
			}
		}
	}
}

func TestClaudeStatusTextDegrades(t *testing.T) {
	s := tmux.Session{
		ClaudeState:      tmux.ClaudeStateApproval,
		ClaudeWaitingFor: "permission prompt",
		ClaudeSince:      time.Now().Add(-3 * time.Minute),
	}

	for _, width := range []int{70, 46, 30, 20, 12, 8, 4, 1} {
		got := claudeStatusText(s, width)
		if w := ansi.StringWidth(got); w > width {
			t.Errorf("width=%d produced %d cells: %q", width, w, got)
		}
	}

	if got := claudeStatusText(tmux.Session{}, 70); got != "" {
		t.Errorf("no Claude state should render nothing, got %q", got)
	}
}

func TestRenderPreviewNilSession(t *testing.T) {
	output := renderPreview(nil, "", 40, 10, nil)
	if !strings.Contains(output, "No session selected") {
		t.Error("nil session should show 'No session selected'")
	}
}
