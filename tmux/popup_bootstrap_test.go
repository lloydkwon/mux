package tmux

import (
	"strings"
	"testing"
)

// The command list is the part worth pinning: a semicolon in the wrong place
// attaches without ever opening the popup, and nothing would say so.
func TestBootstrapArgs(t *testing.T) {
	got := bootstrapArgs("/home/u/go/bin/mux")

	want := []string{
		"attach-session", ";",
		"display-popup", "-E", "-w", "85%", "-h", "80%",
		"MUX_NO_BOOTSTRAP=1 exec '/home/u/go/bin/mux'",
	}
	if len(got) != len(want) {
		t.Fatalf("built %d args, want %d:\n  %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The popup runs mux again. Without the guard in the command it would bootstrap
// in turn, and the popups would never stop.
func TestBootstrapArgsCarryTheGuard(t *testing.T) {
	joined := strings.Join(bootstrapArgs("/bin/mux"), " ")
	if !strings.Contains(joined, BootstrapGuardEnv+"=1") {
		t.Errorf("no recursion guard in the popup command:\n  %s", joined)
	}
}

// The popup's command is read by a shell, and a path with spaces is ordinary.
func TestBootstrapArgsQuoteThePath(t *testing.T) {
	args := bootstrapArgs("/Applications/My Tools/mux")
	last := args[len(args)-1]
	if !strings.Contains(last, `'/Applications/My Tools/mux'`) {
		t.Errorf("path was not quoted as one word: %q", last)
	}

	// An apostrophe must not end the quoting early. POSIX has no escape inside
	// single quotes, so the only way is to close, emit an escaped quote, reopen.
	odd := bootstrapArgs("/tmp/it's/mux")
	wantOdd := BootstrapGuardEnv + `=1 exec '/tmp/it'\''s/mux'`
	if got := odd[len(odd)-1]; got != wantOdd {
		t.Errorf("apostrophe quoting:\n  got  %s\n  want %s", got, wantOdd)
	}
}

// The width and height come from the same constants prefix + m uses, so the two
// cannot drift into different shapes.
func TestBootstrapArgsUseThePopupSize(t *testing.T) {
	joined := strings.Join(bootstrapArgs("/bin/mux"), " ")
	if !strings.Contains(joined, "-w "+popupWidth) || !strings.Contains(joined, "-h "+popupHeight) {
		t.Errorf("bootstrap popup is not the size prefix + m opens:\n  %s", joined)
	}
}
