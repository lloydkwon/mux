package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A Claude conversation is not a tmux session, and this file is what lets mux
// list one anyway.
//
// ClaudeStatuses (claude_status.go) reads the same directory and throws away
// every file with no `tmux` field, because its job is to put a badge on a tmux
// row and a session outside tmux has no row to sit on. Measured on this machine:
// twelve live Claude processes, three of them under tmux — so that filter drops
// three quarters of what is running the moment the user works anywhere else.
//
// What follows keeps all of them. The unit is the *conversation*, addressed by
// Claude's session id, and the terminal it is sitting in is a property of the
// row rather than the thing being listed.

// claudeRunTTL bounds one scan, on the same reasoning as claudeStatusTTL: this
// runs on the refresh tick and re-reads every file in the directory.
const claudeRunTTL = 1 * time.Second

// ClaudeRun is one live Claude conversation and where it is running.
type ClaudeRun struct {
	// Session is the row: name, directory, branch, badge, age.
	Session Session
	// SessionID is Claude's own id for the conversation — the argument
	// `claude --resume` takes.
	SessionID string
	// CWD is where the conversation was opened, which is where resuming it has
	// to happen for the transcript to be found again.
	CWD string
	// TmuxRef is Claude's verbatim "<session>:@<win>.%<pane>" when it runs under
	// tmux, empty otherwise. Empty is the interesting case: it means the
	// conversation is somewhere mux cannot attach to — a herdr pane, a bare
	// terminal, an editor — and continuing it here means resuming rather than
	// switching.
	TmuxRef string
	// TmuxSession is TmuxRef resolved to a live tmux session name, empty when
	// there is none. It is checked against the running server rather than taken
	// from the file, because a stale ref outlives the pane it names.
	TmuxSession string
	// Elsewhere reports that the process holding this conversation is alive and
	// is not reachable through tmux. Resuming it makes a second process on one
	// transcript, so it is the row that has to say so.
	Elsewhere bool
}

type cachedClaudeRuns struct {
	runs      []ClaudeRun
	expiresAt time.Time
}

var (
	claudeRunCache   cachedClaudeRuns
	claudeRunCacheMu sync.Mutex
)

// ClaudeRuns returns every live Claude conversation, wherever it is running,
// newest activity first.
func ClaudeRuns() []ClaudeRun {
	claudeRunCacheMu.Lock()
	defer claudeRunCacheMu.Unlock()
	if c := claudeRunCache; c.runs != nil && time.Now().Before(c.expiresAt) {
		return c.runs
	}
	runs := scanClaudeRuns()
	claudeRunCache = cachedClaudeRuns{runs: runs, expiresAt: time.Now().Add(claudeRunTTL)}
	return runs
}

// resetClaudeRunCache drops the scan. Tests use it.
func resetClaudeRunCache() {
	claudeRunCacheMu.Lock()
	claudeRunCache = cachedClaudeRuns{}
	claudeRunCacheMu.Unlock()
}

func scanClaudeRuns() []ClaudeRun {
	dir, err := claudeSessionsDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no Claude install, or nothing has run yet
	}

	// One list-panes for the whole scan, the same hoist ClaudeStatuses does.
	panes := paneSessions()

	// Keyed by session id: one conversation is one row however many processes
	// hold it. Measured, two did — the same id alive in a herdr pane and in a
	// tmux pane at once, because the user had already resumed it by hand.
	best := make(map[string]ClaudeRun)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // raced with the session exiting
		}
		run, ok := parseClaudeRun(data, panes)
		if !ok {
			continue
		}
		if prev, dup := best[run.SessionID]; dup && !preferRun(run, prev) {
			continue
		}
		best[run.SessionID] = run
	}

	runs := make([]ClaudeRun, 0, len(best))
	for _, r := range best {
		runs = append(runs, r)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := runs[i].Session, runs[j].Session
		if !a.AISince.Equal(b.AISince) {
			return a.AISince.After(b.AISince)
		}
		return a.Name < b.Name
	})
	return runs
}

// preferRun picks between two processes holding the same conversation.
//
// The one reachable through tmux wins, because that is the one the user can be
// put in front of without starting anything. Otherwise the most recently active
// wins — with two copies open, the one being typed into is the live one.
func preferRun(a, b ClaudeRun) bool {
	if (a.TmuxSession != "") != (b.TmuxSession != "") {
		return a.TmuxSession != ""
	}
	return a.Session.AISince.After(b.Session.AISince)
}

func parseClaudeRun(data []byte, panes map[string]paneInfo) (ClaudeRun, bool) {
	var sf claudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return ClaudeRun{}, false // torn write — skip the file, not the scan
	}
	if sf.SessionID == "" {
		return ClaudeRun{}, false
	}
	if !isAlive(sf.PID, sf.ProcStart) {
		return ClaudeRun{}, false // stale file from a crashed session
	}

	// Unlike parseClaudeStatus, a state we do not model is not a reason to drop
	// the row. There it would mean a badge with nothing to say; here it would
	// mean losing a running conversation from the only list that shows it.
	state := mapClaudeState(sf.Status)
	if state == AIStateShell {
		state = demoteServerShell(sf.PID)
	}

	run := ClaudeRun{
		SessionID: sf.SessionID,
		CWD:       sf.CWD,
		TmuxRef:   sf.Tmux,
	}
	if sf.Tmux != "" {
		run.TmuxSession = claudeSessionName(sf.Tmux, panes, sf.PID)
	}
	// A live process mux cannot reach through tmux is the case the row has to
	// warn about, because continuing it means starting a second one.
	run.Elsewhere = run.TmuxSession == ""

	s := Session{
		Name: sf.SessionID, // replaced by uniqueRunNames
		// Attached here means "reachable in tmux", which is what the list's
		// accent colour on a name has always meant — the row you can be put in
		// front of without starting anything. It is the cheapest place to say
		// which rows enter will *go* to and which it will resume, and it costs
		// no column.
		Attached:      run.TmuxSession != "",
		Directory:     sf.CWD,
		ProjectDir:    sf.CWD,
		ActiveCommand: "claude",
		AIState:       state,
		AITool:        "claude",
		// The panel drops a name the user set (claudeTaskName) because a fixed
		// label repeated on every logged transition says nothing. Here the name
		// *is* the row's identity — it is how the user knows which conversation
		// this is — so it is taken whoever chose it. Measured: nine of twelve
		// live sessions carried a user-set name.
		AITask:      sf.Name,
		AIPID:       sf.PID,
		AISessionID: sf.SessionID,
	}
	if state == AIStateApproval {
		s.AIWaitingFor = sf.WaitingFor
	}
	if sf.StatusUpdatedAt > 0 {
		s.AISince = time.UnixMilli(sf.StatusUpdatedAt)
	}
	if sf.StartedAt > 0 {
		s.Created = time.UnixMilli(sf.StartedAt)
	}
	if sf.CWD != "" {
		g := LookupGitInfo(sf.CWD)
		s.GitBranch = g.Branch
		s.IsWorktree = g.IsWorktree
	}
	run.Session = s
	return run, true
}

// uniqueRunNames settles what each row is called.
//
// Claude's own name for the conversation is what the user recognises, but it is
// not unique — two sessions named "mbl-plan" is the ordinary case for a
// plan/exec pair reused across projects. Session.Name is the key expansion
// state, the order map and the cursor all use, so a collision would make two
// rows behave as one. The short session id is appended only to the ones that
// actually collide, so the common row keeps the plain name.
func uniqueRunNames(runs []ClaudeRun) []ClaudeRun {
	count := make(map[string]int, len(runs))
	for _, r := range runs {
		count[runLabel(r)]++
	}
	taken := make(map[string]bool, len(runs))
	out := make([]ClaudeRun, 0, len(runs))
	for _, r := range runs {
		name := runLabel(r)
		if count[name] > 1 || taken[name] {
			name += " " + shortID(r.SessionID)
		}
		for taken[name] {
			name += "'"
		}
		taken[name] = true
		r.Session.Name = name
		out = append(out, r)
	}
	return out
}

func runLabel(r ClaudeRun) string {
	if r.Session.AITask != "" {
		return r.Session.AITask
	}
	if base := filepath.Base(r.CWD); base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return shortID(r.SessionID)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ClaudeSessions returns the live Claude conversations as rows, named.
func ClaudeSessions() []ClaudeRun {
	return uniqueRunNames(ClaudeRuns())
}

// ClaudeResumeArgs builds the command that continues a conversation in a new
// terminal.
//
// --fork-session is added when something is still holding the conversation,
// which is the whole safety of this feature: two Claude processes appending to
// one transcript is not a state either of them expects, and forking gives the
// new one its own id while keeping every message that came before. Where the
// original is gone, a plain --resume is the true continuation and keeps the id.
func ClaudeResumeArgs(sessionID string, stillRunning bool) []string {
	args := []string{"--resume", sessionID}
	if stillRunning {
		args = append(args, "--fork-session")
	}
	return args
}

// claudePaneTarget renders a run's tmux location as a capture-pane target, or
// "" when it is not in tmux.
func claudePaneTarget(r ClaudeRun) string {
	if r.TmuxSession == "" || r.TmuxRef == "" {
		return ""
	}
	_, rest, found := strings.Cut(r.TmuxRef, ":")
	if !found {
		return r.TmuxSession
	}
	return r.TmuxSession + ":" + rest
}

// ClaudePaneTarget is claudePaneTarget for callers outside the package.
func ClaudePaneTarget(r ClaudeRun) string { return claudePaneTarget(r) }
