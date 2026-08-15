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
	//
	// The widths here moved out when the line gained the state's age (and the
	// pane's coordinates, when tmux reports them): the same rung now needs a few
	// more cells. What the numbers pin is the order, not the arithmetic.
	noBranch := BorderLine(s, 56)
	if strings.Contains(noBranch, "⌥") {
		t.Errorf("line %q kept a branch it had no room for", noBranch)
	}
	if !strings.Contains(noBranch, "claude") {
		t.Errorf("line %q dropped the tool before the branch", noBranch)
	}

	// Then the tool's name, then the age, leaving the glyph — which is the state.
	noTool := BorderLine(s, 48)
	if !strings.Contains(noTool, "1m") {
		t.Errorf("line %q dropped the age before the tool name", noTool)
	}
	noAge := BorderLine(s, 40)
	if strings.Contains(noAge, "1m") {
		t.Errorf("line %q kept an age it had no room for", noAge)
	}
	if strings.Contains(noAge, "claude") {
		t.Errorf("line %q kept a tool name it had no room for", noTool)
	}
	if !strings.Contains(noAge, tmux.AIStateReady.Icon()) {
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

// How long the state has held is the difference between "waiting on me" and
// "waiting on me since twelve minutes ago". The pane below cannot say it — a
// finished turn looks the same on screen after one second as after an hour.
func TestBorderLineShowsHowLongTheStateHasHeld(t *testing.T) {
	s := borderSession()
	s.AISince = time.Now().Add(-12 * time.Minute)

	if got := BorderLine(s, 120); !strings.Contains(got, "12m") {
		t.Errorf("line %q does not say how long the state has held", got)
	}

	// No state, no age: an idle session has nothing to have been idle *since*.
	plain := tmux.Session{Name: "shell", Directory: "/home/u/src"}
	if got := BorderLine(plain, 120); strings.Contains(got, "m ") {
		t.Errorf("line %q dated a state it does not have", got)
	}
}

// Two panes of one session are otherwise the same line twice, so the pane's own
// coordinates travel with the name rather than as a droppable extra.
func TestBorderLineCarriesPaneCoordinates(t *testing.T) {
	s := borderSession()
	s.WindowIndex, s.PaneIndex = "2", "1"

	got := BorderLine(s, 120)
	if !strings.Contains(got, "[ mux ] 2:1") {
		t.Errorf("line %q does not locate the pane", got)
	}

	// tmux may report neither, and half a coordinate says nothing.
	s.PaneIndex = ""
	if got := BorderLine(s, 120); strings.Contains(got, "2:") {
		t.Errorf("line %q printed half a coordinate", got)
	}
}
