package tmux

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Fields 9 and 10 are the pane's own coordinates. From list-sessions they
// name the active pane of the active window; read through display-message by
// SessionForTarget they name the target pane, which is what a border drawn above
// that pane should say.
//
// @mux_note goes last because it is the only field whose value a user types.
// parseLine's SplitN leaves the final field as the remainder, so a note
// containing the separator survives intact; anywhere earlier it would corrupt
// every field after it.
const listFormat = "#{session_name}|#{session_windows}|#{session_created}|#{session_attached}|#{pane_current_path}|#{session_activity}|#{pane_current_command}|#{pane_pid}|#{window_index}|#{pane_index}|#{@project_dir}|#{@mux_note}"

// projectDirOption is the tmux session option the tmux-project VS Code profile
// writes when it opens a session for a folder. Its presence is what marks a
// session as belonging to one project — and, unlike the clients attached to it,
// it is there from the moment the session exists.
const projectDirOption = "@project_dir"

// ProjectScope answers, in one round trip, both halves of "should this list open
// narrowed to a project": the session's projectDirOption, and whether the client
// looking at it is a VS Code integrated terminal.
//
// **vscode is what decides, not the tag.** Narrowing was keyed off the tag alone
// at first, and that narrowed the list in Windows Terminal too — where a session
// manager is opened precisely to see everything. The one audience it helps is an
// editor window that already opened this folder.
//
// Asking who is looking is allowed here, unlike on the panel's hook path: this
// runs from a key the user just pressed, so a current client exists.
// `after-new-session` fires before any client attaches, which is why the panel
// keys off the tag instead.
//
// One call because the two are only ever wanted together, at startup, before the
// first frame — and two would cost twice over, including in every test that
// builds a Model against the developer's own tmux server.
//
// Anything unreadable answers false. Guessing "VS Code" wrongly hides the
// sessions the user opened the list to find.
//
// The path goes last and takes the remainder: a pid cannot contain the
// separator and a path can.
func ProjectScope() (dir string, vscode bool) {
	out, err := runner.Output("tmux", "display-message", "-p",
		"#{client_pid}|#{"+projectDirOption+"}")
	if err != nil {
		return "", false
	}
	pidStr, dir, found := strings.Cut(strings.TrimSpace(string(out)), "|")
	if !found {
		return "", false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		return "", false
	}
	return dir, clientEnvHas(pid, vscodeEnvMarker)
}

// SessionProjectDir reports projectDirOption for a named session. Empty when
// the session carries no tag, or does not exist.
func SessionProjectDir(session string) string {
	if session == "" {
		return ""
	}
	out, err := runner.Output("tmux", "show-options", "-qv", "-t", session, projectDirOption)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ListSessions returns all tmux sessions sorted by attached status and recent activity.
func ListSessions() ([]Session, error) {
	out, err := runner.Output("tmux", "list-sessions", "-F", listFormat)
	if err != nil {
		// tmux returns error when no server is running
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	// One scan covers every session, so it is hoisted out of the loop. Screen
	// detection is hoisted for the same reason and costs one more round trip:
	// it batches every pane's capture into a single tmux invocation.
	statuses := ClaudeStatuses()
	screens := ScreenStates()

	sessions := make([]Session, 0, len(lines))
	for _, line := range lines {
		s, err := parseLine(line, statuses, screens)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Attached != sessions[j].Attached {
			return sessions[i].Attached
		}
		return sessions[i].Activity.After(sessions[j].Activity)
	})

	return sessions, nil
}

// SessionForTarget builds the Session owning target, in one round trip. Pass ""
// for the current pane.
//
// Every field of listFormat resolves in a pane's context, so this is
// list-sessions' format read through display-message and handed to the same
// parser — no second format string to drift, and no second place deciding what a
// Session is.
//
// It exists for `mux border`, which runs once per pane per refresh as a fresh
// process. ListSessions there would walk every session and pay for every one of
// their git and Claude lookups to render a line about one of them.
//
// The pane fields (path, command, pid) describe *target* rather than its
// session's active pane, which is what a line drawn above that pane should say.
func SessionForTarget(target string) (Session, error) {
	args := []string{"display-message", "-p"}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, listFormat)

	out, err := runner.Output("tmux", args...)
	if err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	// No screen states, deliberately. ScreenStates costs two tmux spawns and
	// captures every pane on the server; tmux runs this once per pane and
	// re-runs them on redraw, so paying that here is quadratic in the number of
	// panes on screen. Claude's statuses are file reads and cost nothing.
	//
	// The price is that an agent only screen detection recognises reaches the
	// border with no live state — its tool icon, not a glyph. That is the
	// budget for a renderer with no process to cache in, not an oversight.
	return parseLine(strings.TrimSpace(string(out)), ClaudeStatuses(), nil)
}

// parseLine builds a Session from one list-sessions line. statuses maps tmux
// session names to live Claude state and screens to screen-detected state;
// either may be nil.
func parseLine(line string, statuses map[string]ClaudeStatus, screens map[string]ScreenState) (Session, error) {
	// The path may contain the separator, so it is not split off by count — but
	// it is field 5 of a fixed layout, and everything after it is separator-free
	// except the note, which is last and therefore takes the remainder.
	//
	// The floor stays 11 rather than rising to 12 with the format. A line one
	// field short is a session that vanishes from the list entirely, and the
	// note is not worth that: a missing note reads as "no note", which is the
	// state most sessions are in anyway.
	parts := strings.SplitN(line, "|", 12)
	if len(parts) < 11 {
		return Session{}, fmt.Errorf("unexpected format: %s", line)
	}
	note := ""
	if len(parts) > 11 {
		note = sanitizeNote(parts[11])
	}

	windows, _ := strconv.Atoi(parts[1])
	createdUnix, _ := strconv.ParseInt(parts[2], 10, 64)
	attached, _ := strconv.Atoi(parts[3])
	activityUnix, _ := strconv.ParseInt(parts[5], 10, 64)
	panePID, _ := strconv.Atoi(parts[7])

	activeCommand := resolveCommand(panePID, parts[6])
	gitInfo := LookupGitInfo(parts[4])

	s := Session{
		Name:            parts[0],
		WindowCount:     windows,
		Created:         time.Unix(createdUnix, 0),
		Activity:        time.Unix(activityUnix, 0),
		Attached:        attached > 0,
		AttachedClients: attached,
		Directory:       parts[4],
		ActiveCommand:   activeCommand,
		PanePID:         panePID,
		GitBranch:       gitInfo.Branch,
		IsWorktree:      gitInfo.IsWorktree,
		WindowIndex:     parts[8],
		PaneIndex:       parts[9],
		ProjectDir:      parts[10],
		Note:            note,
	}

	// Screen detection first, then the state file over the top of it. A tool
	// reporting its own state is better evidence than reading its screen: it
	// knows the difference between thinking and waiting for a tool call, and it
	// does not go blind when the user opens a transcript. Screen detection is
	// what covers the other nineteen agents, and Claude in the moments before
	// its state file exists.
	if sc, ok := screens[s.Name]; ok && sc.State != AIStateNone {
		s.AIState = sc.State
		s.AITool = sc.Tool
	}

	if st, ok := statuses[s.Name]; ok {
		s.AIState = st.State
		s.AITool = "claude"
		s.AIWaitingFor = st.WaitingFor
		s.AITask = st.Name
		s.AISince = st.Since
		s.AIPID = st.PID

		// tmux's session path follows the *active pane*, which may be looking
		// somewhere unrelated (a split shell, another repo). The AI's own cwd
		// is what the session is about, so dir/branch follow it when they
		// disagree. No-op where /proc is unavailable.
		if cwd := processCwd(st.PID); cwd != "" && cwd != s.Directory {
			s.Directory = cwd
			gi := LookupGitInfo(cwd)
			s.GitBranch = gi.Branch
			s.IsWorktree = gi.IsWorktree
		}
	}

	return s, nil
}

// CreateSession creates a new detached tmux session with the given name.
func CreateSession(name string) error {
	return runner.Run("tmux", "new-session", "-d", "-s", name)
}

// CreateSessionWithDir creates a new detached tmux session starting in the
// given directory, tagged with it as its project.
//
// The tag is what the VS Code tmux-project profile matches on. A session made
// here used to carry none, so opening that project's terminal in VS Code made
// a twin (water_quality → si-water_quality): the profile could not tell the
// two apart, and session_path was no help either — it records the directory
// mux itself was started in, not the one asked for. Tagging at birth closes
// that; the profile still falls back to the pane's cwd for sessions made
// without a directory.
func CreateSessionWithDir(name, dir string) error {
	if err := runner.Run("tmux", "new-session", "-d", "-s", name, "-c", dir); err != nil {
		return err
	}
	// Best-effort: a session without its tag is the old behaviour, not a
	// failure worth refusing the session over.
	_ = runner.Run("tmux", "set-option", "-t", name, projectDirOption, dir)
	return nil
}

// CreateSessionWithCommand makes a detached session running argv, and returns
// the name it actually got.
//
// The name may not be the one asked for. A conversation's own label is what the
// row is called, so resuming the same one twice would collide — and tmux
// refuses a duplicate rather than picking for you, which from the TUI reads as
// enter simply failing. Suffixing is the smaller surprise, and the returned
// name is what the caller must attach to.
//
// The command is passed as separate argv words rather than a string: tmux runs
// a bare shell-command through /bin/sh, and a conversation label carrying a
// quote or a space would be re-split by it.
func CreateSessionWithCommand(name, dir string, argv []string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("create session: empty name")
	}
	name = freeSessionName(name)
	args := []string{"new-session", "-d", "-s", name}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, argv...)
	if err := runner.Run("tmux", args...); err != nil {
		return "", fmt.Errorf("create session %q: %w", name, err)
	}
	return name, nil
}

// freeSessionName finds a name tmux will accept, starting from the one asked
// for.
//
// One list-sessions rather than a has-session per candidate. Asking tmux once
// and searching the answer is both cheaper and honest about what it can know:
// has-session reports existence through its exit status, so every candidate
// would cost a round trip whose failure mode — a tmux that cannot answer at all
// — is indistinguishable from "this name is free". Reading the list makes "no
// server" mean "no sessions", which is the right answer here.
//
// The bound is a guard against a pathological list, not a normal path; the last
// candidate is returned either way, and a duplicate error the caller reports is
// better than a loop.
func freeSessionName(name string) string {
	taken := make(map[string]bool)
	if out, err := runner.Output("tmux", "list-sessions", "-F", "#{session_name}"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if s := strings.TrimSpace(line); s != "" {
				taken[s] = true
			}
		}
	}
	if !taken[name] {
		return name
	}
	candidate := name
	for i := 2; i < 100; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
		if !taken[candidate] {
			return candidate
		}
	}
	return candidate
}

// KillSession destroys the tmux session with the given name.
func KillSession(name string) error {
	return runner.Run("tmux", "kill-session", "-t", name)
}

// RenameSession renames a tmux session from oldName to newName.
func RenameSession(oldName, newName string) error {
	return runner.Run("tmux", "rename-session", "-t", oldName, newName)
}

// SwitchClient points the current tmux client at the named session. Only valid
// from inside tmux, where the client is implied by $TMUX.
//
// The "=" prefix forces an exact match. Without it tmux falls back to prefix
// and then glob matching, so "mux" would be ambiguous next to "mux-old" and a
// name containing * or [ would match something else entirely.
//
// Unlike AttachToSession this returns normally instead of replacing the
// process, so it has no reason to bypass the runner.
func SwitchClient(name string) error {
	return runner.Run("tmux", "switch-client", "-t", "="+name)
}

// isVSCodeClient reports whether a tmux client process is a VS Code integrated
// terminal. Replaceable in tests.
var isVSCodeClient = func(pid int) bool { return procEnvHas(pid, "TERM_PROGRAM=vscode") }

// hasWTEnv reports whether a process carries Windows Terminal's own marker.
// Replaceable in tests.
var hasWTEnv = func(pid int) bool { return procEnvHasPrefix(pid, "WT_SESSION=") }

// isWTClient reports whether a tmux client process was launched from Windows
// Terminal *and is not* a VS Code terminal. Replaceable in tests.
//
// Both halves are needed. A VS Code window opened from Windows Terminal
// inherits WT_SESSION, so the marker alone names VS Code clients too — and
// those are exactly the ones SwitchWTClient must never move.
var isWTClient = func(pid int) bool { return hasWTEnv(pid) && !isVSCodeClient(pid) }

// isTerminalClient reports whether a tmux client is anything but a VS Code
// integrated terminal — SwitchWTClient's fallback when no Windows Terminal is
// attached. A func rather than a var: it is defined by isVSCodeClient, which
// tests already replace.
func isTerminalClient(pid int) bool { return !isVSCodeClient(pid) }

// SwitchWTClient points the most recently active Windows Terminal client at
// the named session (exact match, see SwitchClient for the "=" rationale).
//
// VS Code integrated-terminal clients are never touched: each watches a single
// session on purpose, and yanking them somewhere else would be hostile. Windows
// Terminal is identified by the WT_SESSION environment variable on the client
// process.
//
// Without a Windows Terminal client the most recent client that is not VS Code
// moves instead. The name predates that: on native Linux the terminal beside
// VS Code is WezTerm or GNOME Terminal, nothing carries WT_SESSION, and every
// panel click failed with the error below (measured 2026-10-14). Windows
// Terminal still goes first so a WSL setup moves exactly what it did before.
func SwitchWTClient(name string) error {
	out, err := runner.Output("tmux", "list-clients", "-F",
		"#{client_pid} #{client_activity} #{client_name}")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return errors.New("no attached tmux clients")
	}
	client := mostRecentWTClient(string(out), isWTClient)
	if client == "" {
		client = mostRecentWTClient(string(out), isTerminalClient)
	}
	if client == "" {
		return errors.New("no tmux client attached outside VS Code")
	}
	return runner.Run("tmux", "switch-client", "-c", client, "-t", "="+name)
}

// SessionHasVSCodeClient reports whether any VS Code integrated terminal is
// attached to the named session.
//
// It is the guard on an unqualified switch-client: that command moves "the
// current client", which for a command run from a pane is whichever client is
// watching that pane's session. Where that is VS Code, the switch drags a
// window that is supposed to watch one project only.
//
// Unknown reads as false — a failed list-clients or a process whose environment
// cannot be read must not turn into "this is VS Code".
func SessionHasVSCodeClient(session string) bool {
	if session == "" {
		return false
	}
	out, err := runner.Output("tmux", "list-clients", "-t", "="+session, "-F",
		"#{client_pid}")
	if err != nil {
		return false
	}
	return anyVSCodeClient(string(out), isVSCodeClient)
}

// anyVSCodeClient reports whether any pid in list-clients output is a VS Code
// client. Pure for testing.
func anyVSCodeClient(out string, isVS func(pid int) bool) bool {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		if isVS(pid) {
			return true
		}
	}
	return false
}

// VSCodeClientDirs maps each session name to the workspace folders of every
// VS Code window whose integrated terminal is attached to it. A session can
// collect several — its own project window plus windows that merely attached
// to peek — so callers pick the folder that actually matches the session.
// The folder comes from the client process's PWD: VS Code starts terminals at
// the workspace root, so it names the open window even when that differs from
// the session's own path. Sessions with no VS Code client are absent.
func VSCodeClientDirs() map[string][]string {
	out, err := runner.Output("tmux", "list-clients", "-F",
		"#{client_pid} #{client_session}")
	if err != nil {
		return nil
	}
	dirs := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || !isVSCodeClient(pid) {
			continue
		}
		if dir := procEnvValue(pid, "PWD"); dir != "" {
			dirs[fields[1]] = append(dirs[fields[1]], dir)
		}
	}
	return dirs
}

// VSCodeDirFor picks the VS Code window that owns a session from the folders of
// every VS Code client attached to it, or "" when none of them do.
//
// A session collects candidates two ways: its own project window, and windows
// whose terminal merely attached to peek. Only a window whose workspace folder
// contains the session's directory is the owner — the rest are visitors, and
// focusing one of those would pull the user into an unrelated project.
//
// Pure, so the rule lives in one place: the widget's JSON decides "which VS
// Code window is this session's" with these lines.
func VSCodeDirFor(sessionDir string, candidates []string) string {
	if sessionDir == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	for _, dir := range candidates {
		if sessionDir == dir {
			return dir
		}
		// A window opened on the home directory contains every project, so by
		// the containment rule it would claim every session — click any row and
		// that one window owns them all. Measured: a client with PWD=$HOME was
		// attached while sessions lived under ~/dev/projects. Only an exact
		// match counts for it.
		if home != "" && dir == home {
			continue
		}
		if strings.HasPrefix(sessionDir, dir+"/") {
			return dir
		}
	}
	return ""
}

// mostRecentWTClient picks the client to switch from list-clients output:
// Windows Terminal clients only, most recent activity first. Pure for testing.
func mostRecentWTClient(out string, isWT func(pid int) bool) string {
	best := ""
	bestActivity := int64(-1)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || !isWT(pid) {
			continue
		}
		activity, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		if activity > bestActivity {
			bestActivity = activity
			best = fields[2]
		}
	}
	return best
}
