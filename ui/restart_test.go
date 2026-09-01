package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stamp(size int64, min int) binaryStamp {
	return binaryStamp{size: size, modTime: time.Date(2026, 9, 1, 12, min, 0, 0, time.UTC)}
}

// The check is off unless RunWatch handed the model a baseline. That is what
// keeps `go test ./ui` — which drives Update directly — from stat'ing anything
// or reaching requestRestart's tmux call.
func TestCheckBinaryStandsDownWithoutABaseline(t *testing.T) {
	// No path at all.
	if _, stale := (watchModel{exeStamp: stamp(10, 0)}).checkBinary(stamp(20, 1)); stale {
		t.Error("restarted with no exePath")
	}
	// A path, but the file could not be read at startup — nothing to compare to.
	if _, stale := (watchModel{exePath: "/bin/mux"}).checkBinary(stamp(20, 1)); stale {
		t.Error("restarted with no baseline reading")
	}
}

func TestCheckBinaryIgnoresTheFileItStartedFrom(t *testing.T) {
	m := watchModel{exePath: "/bin/mux", exeStamp: stamp(10, 0)}
	if _, stale := m.checkBinary(stamp(10, 0)); stale {
		t.Error("restarted on an unchanged binary")
	}
}

// `install` cannot write over a running binary — measured, that is ETXTBSY and
// why `cp` fails there — so it unlinks and writes a new file, and the path names
// a partial one while that happens. One disagreeing reading is not evidence.
func TestCheckBinaryNeedsTheNewReadingTwice(t *testing.T) {
	m := watchModel{exePath: "/bin/mux", exeStamp: stamp(10, 0)}

	m, stale := m.checkBinary(stamp(20, 1))
	if stale {
		t.Fatal("restarted on the first disagreeing reading")
	}
	if _, stale := m.checkBinary(stamp(20, 1)); !stale {
		t.Error("did not restart once the new file settled")
	}
}

// A copy still in progress moves the size under us, so the two readings have to
// agree with each other and not merely differ from the baseline.
func TestCheckBinaryWaitsOutAFileStillBeingWritten(t *testing.T) {
	m := watchModel{exePath: "/bin/mux", exeStamp: stamp(10, 0)}
	for _, size := range []int64{5, 12, 19} {
		var stale bool
		if m, stale = m.checkBinary(stamp(size, 1)); stale {
			t.Fatalf("restarted mid-copy at size %d", size)
		}
	}
	if _, stale := m.checkBinary(stamp(19, 1)); !stale {
		t.Error("did not restart once the size stopped moving")
	}
}

// Between the unlink and the create there is no file at all. That is a state to
// wait out, and it must not be mistaken for the second sighting of a new one.
func TestCheckBinaryTreatsAMissingFileAsNoReading(t *testing.T) {
	m := watchModel{exePath: "/bin/mux", exeStamp: stamp(10, 0)}

	m, _ = m.checkBinary(stamp(20, 1))
	m, stale := m.checkBinary(binaryStamp{})
	if stale {
		t.Fatal("restarted on an unreadable path")
	}
	if _, stale := m.checkBinary(stamp(20, 1)); stale {
		t.Error("a gap did not reset the count")
	}
}

func TestStatBinary(t *testing.T) {
	if got := statBinary(""); got.ok() {
		t.Error("an empty path produced a reading")
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	if got := statBinary(missing); got.ok() {
		t.Error("a missing file produced a reading")
	}

	// An empty file is not a binary to exec into, and reads as no reading.
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := statBinary(empty); got.ok() {
		t.Error("an empty file produced a reading")
	}

	real := filepath.Join(dir, "mux")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := statBinary(real)
	if !got.ok() || got.size != 10 {
		t.Errorf("statBinary = %+v, want a reading of 10 bytes", got)
	}
	if !got.same(statBinary(real)) {
		t.Error("two readings of one untouched file disagreed")
	}
}
