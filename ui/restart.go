package ui

import (
	"os"
	"syscall"
	"time"
)

// binaryStamp is what the file at the panel's own path looked like at one
// reading. Two readings that disagree mean mux was installed over.
//
// Size and time rather than a hash: the panel takes one of these every tick, and
// re-reading a 2.5 MB binary every two seconds to answer a question that is
// almost always "no" is not a trade worth making. The cost of the cheaper test
// is that reinstalling a byte-identical build still restarts the panel, which
// costs the cursor and nothing else.
type binaryStamp struct {
	size    int64
	modTime time.Time
}

// ok reports whether this stamp is a reading at all. A zero one means the file
// could not be read — which is a real state during an install, not an error to
// report.
func (b binaryStamp) ok() bool { return b.size > 0 }

func (b binaryStamp) same(other binaryStamp) bool {
	return b.size == other.size && b.modTime.Equal(other.modTime)
}

// statBinary reads the file now at path. Empty path, missing file and empty file
// all answer "no reading" rather than an error: nothing here is worth putting on
// a user's screen, and the caller's response to all three is to wait.
//
// Deliberately not cached, unlike everything else on the tick. A TTL on this
// would be a cache over the one question it exists to ask.
func statBinary(path string) binaryStamp {
	if path == "" {
		return binaryStamp{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return binaryStamp{}
	}
	return binaryStamp{size: fi.Size(), modTime: fi.ModTime()}
}

// relaunch replaces this process with the binary now at path, keeping os.Args
// and the environment.
//
// exec rather than exit-and-let-a-hook-reopen, for two reasons. The pane, its
// width and its focus all survive, because the pane is not going anywhere — only
// the process in it changes. And panelHooks fire on *window* events; installing
// a binary is not one, so a panel that closed itself would stay closed until
// something unrelated happened to the window.
//
// It returns only on failure — on success this process no longer exists.
func relaunch(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
