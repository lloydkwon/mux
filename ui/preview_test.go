package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/lloydkwon/mux/tmux"
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
	live := tmux.Session{ActiveCommand: "bash", AIState: tmux.AIStateWorking}
	info = aiLabelPlain(live)
	if info.styled == "" {
		t.Error("a live AI state should produce a badge regardless of ActiveCommand")
	}
	// The header follows the same merge rule as the list: state glyph, not ✦.
	if !strings.Contains(info.text, tmux.AIStateWorking.Icon()) {
		t.Errorf("header badge = %q, want the state glyph", info.text)
	}
	if strings.Contains(info.text, "✦") {
		t.Errorf("header badge shows both state and tool icon: %q", info.text)
	}
}

func TestFormatStatusLine(t *testing.T) {
	now := time.Now()
	blocked := tmux.Session{
		AIState:      tmux.AIStateApproval,
		AIWaitingFor: "permission prompt",
		AISince:      now.Add(-3 * time.Minute),
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
func TestRenderPreviewLineCountWithAIState(t *testing.T) {
	session := tmux.Session{
		Name:      "mux",
		Directory: "/home/user/mux",
		AIState:   tmux.AIStateWorking,
		AISince:   time.Now().Add(-time.Minute),
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

func TestAIStatusTextDegrades(t *testing.T) {
	s := tmux.Session{
		AIState:      tmux.AIStateApproval,
		AIWaitingFor: "permission prompt",
		AISince:      time.Now().Add(-3 * time.Minute),
	}

	for _, width := range []int{70, 46, 30, 20, 12, 8, 4, 1} {
		got := aiStatusText(s, width)
		if w := ansi.StringWidth(got); w > width {
			t.Errorf("width=%d produced %d cells: %q", width, w, got)
		}
	}

	if got := aiStatusText(tmux.Session{}, 70); got != "" {
		t.Errorf("no live AI state should render nothing, got %q", got)
	}
}

func TestRenderPreviewNilSession(t *testing.T) {
	output := renderPreview(nil, "", 40, 10, nil)
	if !strings.Contains(output, "No session selected") {
		t.Error("nil session should show 'No session selected'")
	}
}
