package tmux

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// clientListFormat is one line per attached client. Activity is a unix stamp
// because that is the only form tmux gives that does not depend on the server's
// locale — `#{t:client_activity}` renders "Thu Sep 3 18:58:18 2026", which is
// unparseable without knowing which language the server was started in.
const clientListFormat = "#{client_tty}|#{client_width}|#{client_height}|#{client_session}|#{client_activity}"

// Client is one terminal attached to the tmux server.
type Client struct {
	TTY      string
	Width    int
	Height   int
	Session  string
	Activity time.Time
}

// Size is what makes a client cost something. Two clients of different sizes
// are the whole reason a window's panes move on their own: tmux's default
// `window-size latest` resizes a window to whichever client last used it, and
// then hands the difference out among that window's panes.
func (c Client) Size() string { return strconv.Itoa(c.Width) + "x" + strconv.Itoa(c.Height) }

// ListClients returns every client attached to the server.
//
// No clients is not an error: `list-clients` exits non-zero with no server, and
// a panel asking this question when there is nothing to answer it should get an
// empty list rather than a line in the log.
func ListClients() ([]Client, error) {
	out, err := runner.Output("tmux", "list-clients", "-F", clientListFormat)
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			return nil, nil
		}
		return nil, fmt.Errorf("list clients: %w", err)
	}
	var cs []Client
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if c, ok := parseClientLine(line); ok {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

// parseClientLine turns one formatted line into a Client. A line that does not
// carry all five fields is dropped rather than half-filled: this list decides
// what to detach, and a client whose activity failed to parse would read as
// epoch-old — which is exactly the shape that gets it disconnected.
func parseClientLine(line string) (Client, bool) {
	f := strings.Split(line, "|")
	if len(f) < 5 || strings.TrimSpace(f[0]) == "" {
		return Client{}, false
	}
	w, err := strconv.Atoi(strings.TrimSpace(f[1]))
	if err != nil {
		return Client{}, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(f[2]))
	if err != nil {
		return Client{}, false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(f[4]), 10, 64)
	if err != nil {
		return Client{}, false
	}
	return Client{
		TTY:      strings.TrimSpace(f[0]),
		Width:    w,
		Height:   h,
		Session:  strings.TrimSpace(f[3]),
		Activity: time.Unix(sec, 0),
	}, true
}

// StaleClients names the clients that are costing the user a stable layout, and
// nothing else.
//
// A ghost client is one mux itself tends to leave behind: `mux` typed in a
// terminal outside tmux attaches and then draws a popup (AttachAndPopup), and a
// terminal closed without quitting that popup leaves the attachment. Observed
// 2026-09-04: a 269x69 client idle for seventeen hours held two windows at 269
// while the live 237x53 client re-laid them out on every visit, which is what
// the panel's width correction was fighting.
//
// Four rules, and every one of them has to hold:
//
//  1. More than one client. A lone client is the user, however long they have
//     been away from the keyboard.
//  2. Its size differs from the newest client's. This is the harm — a same-size
//     idle client changes no layout, so disconnecting one would be tidying at
//     the user's expense. Fixing exactly the symptom is the point.
//  3. It has been idle longer than idle. Detaching costs whoever comes back to
//     that terminal their attachment.
//  4. It is not the newest client. That one is the user, by definition of the
//     only signal there is.
//
// Pure, and takes now, so the decision can be tested without a clock or a
// server — the impure half is ListClients above.
func StaleClients(cs []Client, idle time.Duration, now time.Time) []Client {
	if len(cs) < 2 || idle <= 0 {
		return nil
	}
	newest := cs[0]
	for _, c := range cs[1:] {
		if c.Activity.After(newest.Activity) {
			newest = c
		}
	}
	var out []Client
	for _, c := range cs {
		if c.TTY == newest.TTY || c.Size() == newest.Size() {
			continue
		}
		if now.Sub(c.Activity) <= idle {
			continue
		}
		out = append(out, c)
	}
	return out
}

// DetachTTY disconnects one client by its tty.
//
// Distinct from ui.DetachClient, which detaches whoever is running mux and
// bypasses the runner to reach the real terminal. This one names its target, so
// it goes through the runner like everything else in this package.
//
// Detaching destroys nothing: the session, its windows and every process in
// them keep running, and the terminal can attach again.
func DetachTTY(tty string) error {
	if strings.TrimSpace(tty) == "" {
		return fmt.Errorf("detach client: no tty")
	}
	if _, err := runner.Output("tmux", "detach-client", "-t", tty); err != nil {
		return fmt.Errorf("detach client %s: %w", tty, err)
	}
	return nil
}

// DefaultGhostClientIdle is how long a differently-sized client must sit idle
// before it is treated as forgotten rather than merely quiet. Half an hour is
// well past a coffee break and well short of the seventeen hours that prompted
// this.
const DefaultGhostClientIdle = 30 * time.Minute

// ghostClientIdleOption sets that threshold, in minutes. Zero turns the whole
// check off — the same shape as every other switch here, where the value that
// disables is the one you can reach by accident least often.
const ghostClientIdleOption = "@mux_ghost_client_idle"

// GhostClientIdle is the threshold in force. Unlike MinWindowWidth, a zero here
// is honoured rather than replaced by the default: this one disconnects
// something, so "off" has to be reachable. Only a missing or unparseable option
// falls back.
func GhostClientIdle() time.Duration {
	out, err := runner.Output("tmux", "show-options", "-gqv", ghostClientIdleOption)
	if err != nil {
		return DefaultGhostClientIdle
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return DefaultGhostClientIdle
	}
	m, err := strconv.Atoi(s)
	if err != nil || m < 0 {
		return DefaultGhostClientIdle
	}
	return time.Duration(m) * time.Minute
}
