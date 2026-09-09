package herdr

import "strings"

// CapturePane returns what the pane is showing, for the preview column.
//
// `--source visible` is the screen as drawn, which is what tmux's capture-pane
// gives; `--format ansi` keeps the colours, and ui/layout.go already clips ANSI
// to an exact cell width because tmux's `capture-pane -e` needed that.
//
// **This one does not go through call().** Measured: `herdr pane read` prints
// the pane's text to stdout raw, not the JSON envelope every other command
// returns — the envelope exists only on the socket, where the text sits in
// result.read.text. Decoding this as JSON fails on the first line of whatever
// the user happens to be running.
//
// Not cached, for the same reason the tmux capture is not: it is one call for
// the cursor row and its whole job is to be current.
func CapturePane(paneID string) (string, error) {
	bin, err := binPath()
	if err != nil {
		return "", err
	}
	out, err := runner.Output(bin, "pane", "read", paneID, "--source", "visible", "--format", "ansi")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}
