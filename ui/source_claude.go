package ui

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/lloydkwon/mux/tmux"
)

// claudeSource lists Claude conversations rather than terminals.
//
// It is the only source whose rows are not places: a conversation is a
// transcript plus, sometimes, a process holding it. That is the whole reason
// this source exists — the row survives the terminal, so the work can be picked
// up somewhere else — and it is why Activate has two answers instead of one.
type claudeSource struct {
	// mu guards runs, which Sessions fills and Activate reads. They run on
	// different goroutines: the loaders are tea.Cmds and Activate is called
	// from Update.
	mu   sync.Mutex
	runs []tmux.ClaudeRun
}

func (c *claudeSource) Sessions() ([]tmux.Session, error) {
	runs := tmux.ClaudeSessions()
	c.mu.Lock()
	c.runs = runs
	c.mu.Unlock()

	sessions := make([]tmux.Session, 0, len(runs))
	for _, r := range runs {
		sessions = append(sessions, r.Session)
	}
	return sessions, nil
}

// Windows and Panes are empty: a conversation has no subtree. Expanding a row
// draws nothing, which is what the tree already does for a session with no
// windows cached.
func (c *claudeSource) Windows(string) ([]tmux.Window, error)  { return nil, nil }
func (c *claudeSource) Panes(string, int) ([]tmux.Pane, error) { return nil, nil }

func (c *claudeSource) find(session string) (tmux.ClaudeRun, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.runs {
		if r.Session.Name == session {
			return r, true
		}
	}
	return tmux.ClaudeRun{}, false
}

// Capture previews the pane a conversation is running in — and, when it is not
// running in one mux can read, says so instead.
//
// A blank right-hand column was the first version and it was the wrong answer.
// Seven of ten rows here are held by panes that are not tmux's, so the pane the
// list is *about* would have been empty most of the time, and empty reads as
// broken rather than as "elsewhere". This column is the thing the user reads
// before committing, so it is where the consequence of enter belongs.
func (c *claudeSource) Capture(key previewKey) (string, error) {
	r, ok := c.find(key.session)
	if !ok {
		return "", nil
	}
	if target := tmux.ClaudePaneTarget(r); target != "" {
		return tmux.CapturePaneTarget(target)
	}
	return elsewhereNotice(r), nil
}

// elsewhereNotice explains a conversation mux cannot show, and what enter will
// do about it.
//
// It spells out the exact command, because the two cases differ in a way a row
// cannot carry: an idle conversation is resumed under its own id, which is what
// lets the two copies collapse back into one row afterwards, while one that is
// actively working is forked so the two never interleave.
func elsewhereNotice(r tmux.ClaudeRun) string {
	fork := tmux.ClaudeBusyElsewhere(r)

	var b strings.Builder
	b.WriteString("This conversation is not running in tmux.\n\n")
	if r.CWD != "" {
		b.WriteString("  directory  " + r.CWD + "\n")
	}
	b.WriteString("  session    " + r.SessionID + "\n")
	if r.Session.AIPID > 0 {
		b.WriteString("  process    pid " + strconv.Itoa(r.Session.AIPID) + ", still running\n")
	}
	b.WriteString("\nEnter starts it here with:\n\n")
	b.WriteString("  claude " + strings.Join(tmux.ClaudeResumeArgs(r.SessionID, fork), " ") + "\n")
	if fork {
		b.WriteString("\nSomething is working in this conversation right now, so the resume\n")
		b.WriteString("is forked: the new one gets its own id and every message so far,\n")
		b.WriteString("and the two never append to one transcript. The copy you leave\n")
		b.WriteString("behind is untouched.\n")
	} else {
		b.WriteString("\nSame id, not a fork — so once this is running the two copies show\n")
		b.WriteString("as one row again, and mux stops offering to bring it over.\n")
	}
	return b.String()
}

// Activate says what `enter` means for a conversation, and the split is the
// point of the whole feature.
//
// Held in tmux → go there. Nothing is started, and the conversation the user is
// looking at is the one they land in.
//
// Held anywhere else, or nowhere → start it here. That is a resume, and it
// keeps the conversation's own id — which is what makes the two processes
// collapse back into one row on the next scan, and what stops mux offering to
// bring the same conversation over again for ever.
//
// The exception is a conversation something else is *actively working in*.
// There the two would genuinely interleave, so that one forks instead.
func (c *claudeSource) Activate(session string) (previewKey, *resumeRequest, bool) {
	r, ok := c.find(session)
	if !ok {
		return previewKey{}, nil, false
	}
	if r.TmuxSession != "" {
		// The session, not the pane. AttachToSession addresses windows and
		// panes by index while Claude records them by id (@2, %4), and the
		// pane holding the conversation is the active one in all but the
		// contrived case — landing in the session is the answer that needs no
		// translation.
		return previewKey{session: r.TmuxSession, window: -1, pane: -1}, nil, true
	}
	name := tmux.SafeSessionName(r.Session.Name)
	if name == "" || r.CWD == "" || r.SessionID == "" {
		return previewKey{}, nil, false
	}
	fork := tmux.ClaudeBusyElsewhere(r)
	return previewKey{}, &resumeRequest{
		Name:   name,
		Dir:    r.CWD,
		Args:   append([]string{"claude"}, tmux.ClaudeResumeArgs(r.SessionID, fork)...),
		Forked: fork,
	}, true
}

func (*claudeSource) Title(rows int) string {
	return fmt.Sprintf("⚡ claude sessions %d", rows)
}

// Bar names what enter does, because here it does two different things and the
// difference matters: one of them starts a process.
func (*claudeSource) Bar() string {
	return "claude — enter attaches where it runs, or resumes it here · new/kill/rename/memo are off"
}

// NewClaudeModel returns a Model that lists Claude conversations.
//
// Not read-only: enter is the reason to open it. What it does block is
// everything that would change *tmux* — creating, killing, renaming, a note —
// because none of those are operations on a conversation, and the rows here are
// conversations. That gate is m.src being set, not m.readOnly.
//
// It skips NewModel for the reason NewHerdrModel does: the @project_dir filter
// seed is a tmux answer that matches no row here.
func NewClaudeModel() Model {
	prefs, err := loadPreferences()
	return Model{
		tree:  newTreeState(),
		prefs: prefs,
		err:   err,
		src:   &claudeSource{},
	}
}
