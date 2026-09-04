package tmux

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// live and ghost are the two clients of the incident this feature came from:
// a 237x53 terminal in use, and a 269x69 one left attached overnight.
func live(t time.Time) Client {
	return Client{TTY: "/dev/pts/24", Width: 237, Height: 53, Session: "my-mux", Activity: t}
}
func ghost(t time.Time) Client {
	return Client{TTY: "/dev/pts/22", Width: 269, Height: 69, Session: "ebbt", Activity: t}
}

func TestStaleClientsFindsTheForgottenOne(t *testing.T) {
	now := time.Now()
	got := StaleClients([]Client{live(now), ghost(now.Add(-17 * time.Hour))}, 30*time.Minute, now)
	if len(got) != 1 || got[0].TTY != "/dev/pts/22" {
		t.Fatalf("expected the 17h-idle 269x69 client, got %+v", got)
	}
}

// The newest client is the user, by definition of the only signal there is.
// Detaching it is the one mistake this code must never make.
func TestStaleClientsNeverTakesTheNewest(t *testing.T) {
	now := time.Now()
	// Both idle far past the threshold, so only the newest-rule can save one.
	for _, got := range StaleClients(
		[]Client{live(now.Add(-2 * time.Hour)), ghost(now.Add(-17 * time.Hour))},
		30*time.Minute, now) {
		if got.TTY == "/dev/pts/24" {
			t.Fatal("detached the most recently active client")
		}
	}
}

// The harm is the size difference — that is what makes tmux re-lay out a window
// on every visit. A same-size client costs nothing, so disconnecting one would
// be tidying at the user's expense.
func TestStaleClientsLeavesASameSizedIdlerAlone(t *testing.T) {
	now := time.Now()
	twin := live(now.Add(-17 * time.Hour))
	twin.TTY = "/dev/pts/99"
	if got := StaleClients([]Client{live(now), twin}, 30*time.Minute, now); got != nil {
		t.Fatalf("same size is not a ghost, got %+v", got)
	}
}

func TestStaleClientsLeavesARecentClientAlone(t *testing.T) {
	now := time.Now()
	if got := StaleClients([]Client{live(now), ghost(now.Add(-5 * time.Minute))}, 30*time.Minute, now); got != nil {
		t.Fatalf("5m idle is not forgotten, got %+v", got)
	}
}

// A lone client is the user, however long they have been away from the keyboard.
func TestStaleClientsNeverTouchesTheOnlyClient(t *testing.T) {
	now := time.Now()
	if got := StaleClients([]Client{ghost(now.Add(-99 * time.Hour))}, 30*time.Minute, now); got != nil {
		t.Fatalf("the only client is never a ghost, got %+v", got)
	}
}

// Zero is the off switch and has to reach all the way through: a threshold of
// zero would otherwise make every other client instantly "idle longer than 0".
func TestStaleClientsZeroIdleDisablesIt(t *testing.T) {
	now := time.Now()
	if got := StaleClients([]Client{live(now), ghost(now.Add(-17 * time.Hour))}, 0, now); got != nil {
		t.Fatalf("zero disables, got %+v", got)
	}
}

func TestListClientsParsesTheFormat(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("/dev/pts/24|237|53|my-mux|1788490351\n/dev/pts/22|269|69|ebbt|1788429498\n"),
			nil, "tmux", "list-clients", "-F", clientListFormat)
		cs, err := ListClients()
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 2 {
			t.Fatalf("want 2 clients, got %d", len(cs))
		}
		if cs[0].Size() != "237x53" || cs[0].Session != "my-mux" {
			t.Errorf("first client wrong: %+v", cs[0])
		}
		if !cs[1].Activity.Equal(time.Unix(1788429498, 0)) {
			t.Errorf("activity not parsed: %v", cs[1].Activity)
		}
	})
}

// A half-parsed line would carry a zero Activity, which reads as epoch-old —
// precisely the shape that gets a client disconnected. Dropping it is the only
// safe failure.
func TestListClientsDropsAnUnparseableLine(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("/dev/pts/24|237|53|my-mux|1788490351\n/dev/pts/9|wide|69|x|nope\n"),
			nil, "tmux", "list-clients", "-F", clientListFormat)
		cs, _ := ListClients()
		if len(cs) != 1 {
			t.Fatalf("want the malformed line dropped, got %+v", cs)
		}
	})
}

// No server is not an error worth a line in anyone's log.
func TestListClientsIsEmptyWithNoServer(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput(nil, errors.New("exit status 1"), "tmux", "list-clients", "-F", clientListFormat)
		cs, err := ListClients()
		if err != nil || cs != nil {
			t.Fatalf("want (nil, nil), got (%+v, %v)", cs, err)
		}
	})
}

func TestDetachTTYNamesItsTarget(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput(nil, nil, "tmux", "detach-client", "-t", "/dev/pts/22")
		if err := DetachTTY("/dev/pts/22"); err != nil {
			t.Fatal(err)
		}
		if len(m.gets) != 1 || !strings.Contains(m.gets[0], "-t /dev/pts/22") {
			t.Fatalf("target not named: %v", m.gets)
		}
	})
}

// Without a target, detach-client disconnects whoever runs it — which from a
// panel is the user. An empty tty must never reach tmux.
func TestDetachTTYRefusesAnEmptyTarget(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		if err := DetachTTY("  "); err == nil {
			t.Fatal("want an error for an empty tty")
		}
		if len(m.gets) != 0 || len(m.runs) != 0 {
			t.Fatalf("nothing should have reached tmux: %v %v", m.gets, m.runs)
		}
	})
}

// Zero is honoured here, unlike MinWindowWidth's, because this switch turns off
// something that disconnects a terminal: "off" has to be reachable.
func TestGhostClientIdleHonoursZero(t *testing.T) {
	withMock(t, func(m *mockRunner) {
		m.OnOutput([]byte("0\n"), nil, "tmux", "show-options", "-gqv", ghostClientIdleOption)
		if got := GhostClientIdle(); got != 0 {
			t.Fatalf("want 0, got %v", got)
		}
	})
}

func TestGhostClientIdleFallsBackOnGarbage(t *testing.T) {
	for _, out := range []string{"", "soon", "-5"} {
		withMock(t, func(m *mockRunner) {
			m.OnOutput([]byte(out+"\n"), nil, "tmux", "show-options", "-gqv", ghostClientIdleOption)
			if got := GhostClientIdle(); got != DefaultGhostClientIdle {
				t.Fatalf("%q: want the default, got %v", out, got)
			}
		})
	}
}
