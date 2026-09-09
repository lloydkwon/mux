package tmux

import (
	"fmt"
	"sort"
	"strings"
)

// An exec/review pair is two sessions that are one piece of work: the session
// doing it and the session reviewing it. They are separate tmux sessions on
// purpose — that is what puts each of them in mux's list with its own AI badge —
// but the person driving them wants both on screen at once.
//
// Pairing joins the review session's work pane into the exec session's window,
// so the window reads [exec | review | panel]. The review session survives
// holding only its panel: closing that last pane would take the session with it,
// and its name, options and note with that.

const (
	// pairSuffixes name the two halves. This is the fork author's own
	// convention (session-loop's EXEC_NAME/PLAN_NAME), not something tmux or
	// mux imposes, so @mux_pair_suffixes overrides it globally.
	defaultExecSuffix = "-exec"
	defaultPlanSuffix = "-plan"

	pairSuffixOption = "@mux_pair_suffixes"
	// pairedOption records the bases mux has already joined, so a pair a person
	// pulled apart by hand stays apart. It lives on the tmux server and dies
	// with it, which is the right lifetime: a restored server rebuilds its
	// layout from scratch anyway.
	pairedOption = "@mux_paired"
)

// globalOption reads a global user option, empty for anything unreadable.
// -gqv is the same form PanelHeaderEnabled and MinWindowWidth use: quiet, so an
// option nobody set is an empty string rather than an error.
func globalOption(name string) string {
	out, err := runner.Output("tmux", "show-options", "-gqv", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Pair is two sessions that belong together.
type Pair struct {
	Base string // what they share, e.g. "matching"
	Exec string // the session doing the work
	Plan string // the session reviewing it
}

// PairSuffixes returns the two name endings that mark a pair.
//
// @mux_pair_suffixes is "<exec>,<plan>"; anything unparseable falls back rather
// than turning pairing off, because a typo in an option should not silently
// change what mux does to panes.
func PairSuffixes() (exec, plan string) {
	raw := strings.TrimSpace(globalOption(pairSuffixOption))
	e, p, found := strings.Cut(raw, ",")
	e, p = strings.TrimSpace(e), strings.TrimSpace(p)
	if !found || e == "" || p == "" || e == p {
		return defaultExecSuffix, defaultPlanSuffix
	}
	return e, p
}

// FindPairs picks the exec/review pairs out of a session list. Pure.
func FindPairs(sessions []Session, execSuffix, planSuffix string) []Pair {
	byName := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		byName[s.Name] = true
	}

	var pairs []Pair
	for _, s := range sessions {
		base, ok := strings.CutSuffix(s.Name, execSuffix)
		if !ok || base == "" {
			continue
		}
		plan := base + planSuffix
		if !byName[plan] {
			continue
		}
		pairs = append(pairs, Pair{Base: base, Exec: s.Name, Plan: plan})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Base < pairs[j].Base })
	return pairs
}

// workPane returns the session's pane that is not the panel, and how many panes
// it has in all.
//
// "Not the panel" rather than "pane 0": the panel is created by splitting, so
// which index it lands on is a layout detail, while what it runs is not.
func workPane(session string) (pane string, panes int, err error) {
	out, err := runner.Output("tmux", "list-panes", "-t", session,
		"-F", "#{pane_id} #{pane_start_command}")
	if err != nil {
		return "", 0, fmt.Errorf("list panes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		panes++
		id, cmd, _ := strings.Cut(line, " ")
		if strings.Contains(cmd, panelCommand) {
			continue
		}
		if pane == "" {
			pane = id
		}
	}
	return pane, panes, nil
}

// PairedBases reports the pairs mux has already joined on this server.
func PairedBases() map[string]bool {
	done := make(map[string]bool)
	for _, b := range strings.Split(globalOption(pairedOption), ",") {
		if b = strings.TrimSpace(b); b != "" {
			done[b] = true
		}
	}
	return done
}

// markPaired records a base as joined.
func markPaired(base string) error {
	done := PairedBases()
	done[base] = true
	names := make([]string, 0, len(done))
	for b := range done {
		names = append(names, b)
	}
	sort.Strings(names)
	return runner.Run("tmux", "set-option", "-g", pairedOption, strings.Join(names, ","))
}

// JoinPair puts the review session's work pane beside the exec session's.
//
// -d keeps the focus where it was: the person is looking at the exec pane, and
// a join that stole the cursor would make an automatic action feel like one it
// had no business taking.
//
// -h puts the review pane to the *right* of the exec pane, which lands it
// between the work and the panel — [exec | review | panel]. Measured: a 200
// column window became 81 | 80 | 37.
func JoinPair(p Pair) error {
	planPane, planPanes, err := workPane(p.Plan)
	if err != nil {
		return err
	}
	if planPane == "" {
		// Nothing but a panel: already joined, by us or by hand.
		return nil
	}
	if planPanes < 2 {
		// The review session is its work pane and nothing else. Moving it would
		// end the session, which is the one outcome the caller asked to avoid.
		return fmt.Errorf("pair %s: %s has no panel to keep it alive", p.Base, p.Plan)
	}
	execPane, _, err := workPane(p.Exec)
	if err != nil {
		return err
	}
	if execPane == "" {
		return fmt.Errorf("pair %s: no work pane in %s to join onto", p.Base, p.Exec)
	}
	if err := runner.Run("tmux", "join-pane", "-d", "-h", "-s", planPane, "-t", execPane); err != nil {
		return fmt.Errorf("pair %s: %w", p.Base, err)
	}
	return markPaired(p.Base)
}

// PairablePairs returns the pairs that should be joined now: both halves
// present, not joined already, and not one mux has joined before.
//
// The last rule is what makes this safe to run automatically. Pulling the two
// apart again is a thing a person does deliberately, and a mux that re-joined
// them on its next launch would be arguing with them.
func PairablePairs(sessions []Session) []Pair {
	execSuffix, planSuffix := PairSuffixes()
	done := PairedBases()

	var out []Pair
	for _, p := range FindPairs(sessions, execSuffix, planSuffix) {
		if done[p.Base] {
			continue
		}
		out = append(out, p)
	}
	return out
}
