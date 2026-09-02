package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestSetSessionNoteWritesAndClears(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := SetSessionNote("dev", "라벨링 후 진행"); err != nil {
			t.Fatalf("set: %v", err)
		}
		want := "tmux set-option -t dev @mux_note 라벨링 후 진행"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("runs = %v, want [%q]", m.runs, want)
		}
	})

	// Clearing must unset rather than store "", so a session that had a note
	// removed reads the same as one that never had one.
	withMock(t, func(m *mockRunner) {
		if err := SetSessionNote("dev", "   "); err != nil {
			t.Fatalf("clear: %v", err)
		}
		want := "tmux set-option -ut dev @mux_note"
		if len(m.runs) != 1 || m.runs[0] != want {
			t.Errorf("runs = %v, want [%q]", m.runs, want)
		}
	})
}

func TestSessionNoteReadsOption(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("라벨링 후 진행\n"), nil,
			"tmux", "show-options", "-qv", "-t", "dev", "@mux_note")
		if got := SessionNote("dev"); got != "라벨링 후 진행" {
			t.Errorf("SessionNote = %q", got)
		}
	})

	// Unreadable is no note, not an error: an absent option, a dead session and
	// a tmux that is not running all mean there is nothing to draw.
	withMock(t, func(m *mockRunner) {
		m.OnOutput(nil, fmt.Errorf("no server"),
			"tmux", "show-options", "-qv", "-t", "dev", "@mux_note")
		if got := SessionNote("dev"); got != "" {
			t.Errorf("SessionNote on error = %q, want empty", got)
		}
	})
}

func TestSanitizeNoteStripsControlCharacters(t *testing.T) {
	// The newline case is the load-bearing one: list-sessions emits one line per
	// session, so a note carrying one splits its session's line in two and
	// parseLine rejects both halves — the session vanishes from the list.
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"newline", "라벨링\n후 진행", "라벨링후 진행"},
		{"carriage return", "a\r\nb", "ab"},
		{"tab", "a\tb", "ab"},
		{"escape sequence", "a\x1b[31mb", "a[31mb"},
		{"surrounding space", "  note  ", "note"},
		{"pipe survives", "a | b", "a | b"},
		{"already clean", "라벨링 후 진행", "라벨링 후 진행"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeNote(tt.in); got != tt.want {
				t.Errorf("sanitizeNote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeNoteBounded(t *testing.T) {
	// Runes, so a long note is cut before the set-option argv gets anywhere near
	// the 16KB wall — and cells, because the row that draws it budgets in cells
	// and Korean draws two per rune.
	long := strings.Repeat("a", MaxNoteLen*2)
	if got := len([]rune(sanitizeNote(long))); got != MaxNoteLen {
		t.Errorf("ascii note = %d runes, want %d", got, MaxNoteLen)
	}
	korean := strings.Repeat("가", MaxNoteLen*2)
	if got := ansi.StringWidth(sanitizeNote(korean)); got > MaxNoteLen {
		t.Errorf("korean note = %d cells, want <= %d", got, MaxNoteLen)
	}
}

// TestParseLineNoteField pins both ends of the format change: a line with the
// note present carries it, and one written by an older format still parses.
func TestParseLineNoteField(t *testing.T) {
	now := time.Now().Unix()
	base := fmt.Sprintf("dev|2|%d|1|/home/user/dev|%d|bash|0|0|0|/proj", now-3600, now-60)

	tests := []struct {
		name string
		line string
		want string
	}{
		{"note present", base + "|라벨링 후 진행", "라벨링 후 진행"},
		{"note empty", base + "|", ""},
		// The floor stayed at 11 rather than rising with the format: one field
		// short must be a session with no note, never a session that disappears.
		{"eleven fields", base, ""},
		// The note is the last field, so SplitN leaves it as the remainder and a
		// separator inside it is safe. Nothing earlier has that property.
		{"pipe inside the note", base + "|a | b", "a | b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := parseLine(tt.line, nil, nil)
			if err != nil {
				t.Fatalf("parseLine: %v", err)
			}
			if s.Note != tt.want {
				t.Errorf("Note = %q, want %q", s.Note, tt.want)
			}
			if s.ProjectDir != "/proj" {
				t.Errorf("ProjectDir = %q, want /proj", s.ProjectDir)
			}
		})
	}
}
