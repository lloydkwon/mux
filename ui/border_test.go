package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

func borderSession() tmux.Session {
	s := sess("mux", tmux.AIStateReady)
	s.Directory = "/home/u/Projects/temp/mux"
	s.GitBranch = "main"
	s.AISince = time.Now().Add(-time.Minute)
	return s
}

// tmux clips the border itself, and clipping lands mid-glyph. Whatever it
// carries, the line has to fit what the pane can show.
func TestBorderLineFitsTheWidth(t *testing.T) {
	long := borderSession()
	long.Name = "a-fairly-long-session-name-indeed"
	long.GitBranch = "feature/a-very-long-branch-name-here"
	long.Directory = "/home/u/Projects/very/deeply/nested/thing"
	plain := tmux.Session{Name: "shell", Directory: "/tmp", ActiveCommand: "zsh"}

	for _, s := range []tmux.Session{borderSession(), long, plain} {
		for _, width := range []int{4, 8, 16, 30, 60, 120, 200} {
			if got := ansi.StringWidth(BorderLine(s, width)); got > width {
				t.Errorf("name=%q width=%d: line measures %d cells (%q)",
					s.Name, width, got, BorderLine(s, width))
			}
		}
	}
}

// tmux styles formats with its own #[…] syntax and paints raw escapes as text.
// A colour set here would appear as literal gibberish along the top of the pane.
func TestBorderLineCarriesNoANSI(t *testing.T) {
	line := BorderLine(borderSession(), 120)
	if stripped := ansi.Strip(line); stripped != line {
		t.Errorf("line carries escape sequences: %q", line)
	}
}

// The order things are given up in, from a full line down to a bare name.
func TestBorderLineDropsDetailToFit(t *testing.T) {
	s := borderSession()

	full := BorderLine(s, 120)
	for _, want := range []string{"[ mux ]", "Projects/temp/mux", "claude", "main",
		tmux.AIStateReady.Icon()} {
		if !strings.Contains(full, want) {
			t.Errorf("full line %q is missing %q", full, want)
		}
	}

	// The branch goes before the tool's name does.
	noBranch := BorderLine(s, 48)
	if strings.Contains(noBranch, "⌥") {
		t.Errorf("line %q kept a branch it had no room for", noBranch)
	}
	if !strings.Contains(noBranch, "claude") {
		t.Errorf("line %q dropped the tool before the branch", noBranch)
	}

	// Then the tool's name, leaving the glyph, which is the state.
	noTool := BorderLine(s, 36)
	if strings.Contains(noTool, "claude") {
		t.Errorf("line %q kept a tool name it had no room for", noTool)
	}
	if !strings.Contains(noTool, tmux.AIStateReady.Icon()) {
		t.Errorf("line %q dropped the state glyph before the tool name", noTool)
	}

	// The directory is next, and the name is the last thing standing.
	for _, width := range []int{16, 10} {
		got := BorderLine(s, width)
		if !strings.Contains(got, "mux") {
			t.Errorf("width=%d: line %q lost the session name", width, got)
		}
	}
}

// A session running no AI CLI still gets a line — which session and where it is
// are the half of this that does not depend on a tool being there.
func TestBorderLineWithoutAI(t *testing.T) {
	plain := tmux.Session{Name: "shell", Directory: "/home/u/src", GitBranch: "main",
		ActiveCommand: "zsh"}

	got := BorderLine(plain, 120)
	for _, want := range []string{"[ shell ]", "/home/u/src", "main"} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q is missing %q", got, want)
		}
	}
	for _, st := range allAIStates {
		if g := st.Icon(); g != "" && strings.Contains(got, g) {
			t.Errorf("line %q shows the %v glyph for a plain shell", got, st)
		}
	}
}

// Nothing to describe means nothing drawn: tmux fills the rest of the border
// with its own line, so an empty format is the correct blank.
func TestBorderLineNeedsASession(t *testing.T) {
	if got := BorderLine(tmux.Session{}, 80); got != "" {
		t.Errorf("nameless session rendered %q", got)
	}
	if got := BorderLine(borderSession(), 0); got != "" {
		t.Errorf("zero width rendered %q", got)
	}
}
