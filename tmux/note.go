package tmux

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// noteOption is the tmux session option holding the user's own one-line note
// about a session.
//
// A session option rather than a file keyed by name, for the reason
// @project_dir is one: the option belongs to the *session*, so a rename carries
// it along and there is no map to fix up the way preferences.json's Orders needs
// fixing up on rename and kill. It rides listFormat, so the TUI, the panel and
// `mux border` all see it without a single extra tmux call.
//
// The price is that it dies with the server. That has happened — see
// eventarchive.go — and when it does the notes go with it. There is no archive
// here on purpose: if one is ever wanted, events.json is the shape to copy.
const noteOption = "@mux_note"

// MaxNoteLen caps a note in runes.
//
// A set-option argv over 16KB comes back "command too long" (MAX_IMSGSIZE,
// measured on tmux 3.4 in eventlog.go), and that failure is not reported
// anywhere a user could act on it. This is far below the wall because a note is
// a label, not a document: what does not fit on a list row is not a note the
// list can use.
const MaxNoteLen = 120

// SessionNote reads a session's note. Anything unreadable is no note — an
// absent option, a dead session and a tmux that is not running all mean the same
// thing here, which is that there is nothing to draw.
func SessionNote(session string) string {
	if session == "" {
		return ""
	}
	out, err := runner.Output("tmux", "show-options", "-qv", "-t", session, noteOption)
	if err != nil {
		return ""
	}
	return sanitizeNote(string(out))
}

// SetSessionNote writes a session's note, or clears it when note is empty.
//
// Clearing unsets the option rather than storing "" so that a session with no
// note reads the same whether it never had one or had one removed.
func SetSessionNote(session, note string) error {
	if session == "" {
		return nil
	}
	note = sanitizeNote(note)
	if note == "" {
		return runner.Run("tmux", "set-option", "-ut", session, noteOption)
	}
	return runner.Run("tmux", "set-option", "-t", session, noteOption, note)
}

// sanitizeNote makes a string safe to put in a tmux option that listFormat reads
// back.
//
// **Stripping newlines is load-bearing.** `list-sessions -F` emits one line per
// session, so a note containing a newline splits its session's line in two and
// parseLine rejects both halves — the session disappears from the list entirely.
// Every other control character is dropped for the same family of reasons: the
// value is painted into a fixed-width row, and an escape sequence there would be
// drawn as cells nothing measured.
//
// Truncation is by rune and then by cell, because the row arithmetic counts
// cells and a note is as likely to be Korean as ASCII.
func sanitizeNote(s string) string {
	var b strings.Builder
	for _, r := range s {
		// Tab and the rest of the C0/C1 range are all cut; a note is one line of
		// text and none of them belong in one.
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	note := strings.TrimSpace(b.String())

	if runes := []rune(note); len(runes) > MaxNoteLen {
		note = strings.TrimSpace(string(runes[:MaxNoteLen]))
	}
	// A second bound in cells: MaxNoteLen runes of Korean draw twice that many
	// columns, and the option is read back into a row that budgets in cells.
	if ansi.StringWidth(note) > MaxNoteLen {
		note = strings.TrimSpace(ansi.Truncate(note, MaxNoteLen, ""))
	}
	return note
}
