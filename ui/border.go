package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/lloydkwon/mux/tmux"
)

// BorderLine renders the session summary tmux draws above a pane, through
// `pane-border-status top` and `pane-border-format`.
//
// It says what the pane below it cannot: which session this is, where it is,
// which AI CLI is in it and what that CLI is doing, and the git branch. The pane
// itself is the user's shell, so this border is the only row of that window mux
// gets to write in.
//
// Three things make this unlike every other renderer here:
//
//   - It emits **no ANSI**. tmux styles formats with its own `#[fg=…]` syntax
//     and passes raw escapes through as text. Colour is left to
//     pane-border-style / pane-active-border-style, which already tell the
//     active pane from the rest, and the state glyph (⏳❗✅) carries what a
//     colour would have said — the same rule the list and the panel follow.
//   - It does not right-align. tmux fills whatever the format does not use with
//     the border character, so padding out to the full width would replace that
//     run of ─ with blanks and leave the border looking broken.
//   - It is a single left-aligned cluster that gives up its parts from the right
//     as the pane narrows: branch, then the tool's name, then the directory. The
//     session name is never dropped — a border that cannot say which session it
//     belongs to has nothing left worth drawing.
func BorderLine(s tmux.Session, width int) string {
	if s.Name == "" || width <= 0 {
		return ""
	}

	glyph, hasAI := aiGlyph(s)
	tool := ""
	if t, ok := tmux.SessionAITool(s); ok {
		tool = t.Name
	}
	branch := ""
	if s.GitBranch != "" {
		branch = branchGlyph(s) + " " + s.GitBranch
	}

	// Most complete first: the ladder returns the first line that fits, so each
	// entry is one detail poorer than the one above it.
	// How long the state has held. It is the difference between "waiting on me"
	// and "waiting on me since twelve minutes ago", and the pane below cannot
	// say it — a finished turn looks the same on screen after one second as
	// after an hour.
	elapsed := ""
	if s.AIState != tmux.AIStateNone && !s.AISince.IsZero() {
		elapsed = compactAgo(s.AISince)
	}

	badgeFull, badgeMid, badgeShort := "", "", ""
	if hasAI {
		badgeFull = borderJoinInner(glyph, elapsed, tool)
		badgeMid = borderJoinInner(glyph, elapsed)
		badgeShort = glyph
	}
	// The pane's own coordinates, kept with the name because together they are
	// the identity: two panes of one session are otherwise the same line twice.
	name := "[ " + s.Name + " ]"
	if s.WindowIndex != "" && s.PaneIndex != "" {
		name += " " + s.WindowIndex + ":" + s.PaneIndex
	}
	path := shortenPath(s.Directory)

	for _, parts := range [][]string{
		{name, path, badgeFull, branch},
		{name, path, badgeFull},  // the branch is context, and the panel repeats it
		{name, path, badgeMid},   // the tool's name goes before its state does
		{name, path, badgeShort}, // the glyph is the state; the name is its label
		{name, badgeShort},
		{name},
	} {
		line := borderJoin(parts)
		if ansi.StringWidth(line) <= width {
			return line
		}
	}
	// Even the name alone overflows: cut it rather than let tmux clip mid-glyph.
	return strings.TrimRight(fitCells(borderJoin([]string{name}), width), " ")
}

// borderJoin puts the line together, dropping the parts that turned out empty.
//
// Two spaces between clusters and one leading space: the border's corner sits
// immediately left of this, and a run of ─ immediately right of it.
func borderJoin(parts []string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return " " + strings.Join(kept, "  ") + " "
}

// borderJoinInner joins the parts of one cluster with a single space, so an
// absent one leaves no gap behind it. Clusters are separated by two spaces in
// borderJoin; this is the space inside them.
func borderJoinInner(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}
