package detect

import (
	"strconv"
	"strings"
)

// Input is what the engine reads. Empty strings are valid and mean "not
// available" — a rule whose region is empty simply cannot match.
type Input struct {
	// Screen is the pane's visible text, bottom-anchored, plain (no ANSI),
	// wrapped as displayed, with trailing blank lines removed.
	Screen string
	// OSCTitle is the pane's current terminal title. tmux exposes it as
	// #{pane_title}; six manifests key their strongest rules on it.
	OSCTitle string
	// OSCProgress is the payload of the agent's last OSC 9 progress sequence.
	// tmux does not expose this, so it is always empty here — see the package
	// docs for which rules that costs.
	OSCProgress string
}

// region extracts the slice of the input a rule reads.
//
// The names and their semantics are herdr's; only the ones its bundled
// manifests actually use are implemented. An unimplemented name yields the
// empty string, which cannot match — the same outcome herdr produces for a
// region that is not present.
func region(input Input, spec string) string {
	spec = strings.TrimSpace(spec)

	// OSC regions read their own fields and never touch the screen.
	switch spec {
	case "osc_title":
		return input.OSCTitle
	case "osc_progress":
		return input.OSCProgress
	}

	content := input.Screen
	switch spec {
	case "whole_recent":
		return content
	case "after_last_horizontal_rule":
		return afterLastHorizontalRule(content)
	case "prompt_box_body":
		return promptBoxBody(content)
	case "after_last_prompt_marker":
		return afterLastPromptMarker(content)
	}

	if count, ok := regionCount(spec, "bottom_non_empty_lines"); ok {
		return bottomNonEmptyLines(content, count)
	}
	if count, ok := regionCount(spec, "top_non_empty_lines"); ok {
		return topNonEmptyLines(content, count)
	}
	if count, ok := regionCount(spec, "bottom_lines"); ok {
		return bottomLines(content, count)
	}
	return ""
}

// knownRegion reports whether a spec names a region this engine implements, so
// a re-synced manifest using something new fails a test rather than silently
// producing a rule that can never fire.
func knownRegion(spec string) bool {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "osc_title", "osc_progress", "whole_recent",
		"after_last_horizontal_rule", "prompt_box_body", "after_last_prompt_marker":
		return true
	}
	for _, name := range []string{"bottom_non_empty_lines", "top_non_empty_lines", "bottom_lines"} {
		if _, ok := regionCount(spec, name); ok {
			return true
		}
	}
	return false
}

// regionCount parses `name(N)`.
func regionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	count, err := strconv.Atoi(digits)
	if err != nil || count < 0 {
		return 0, false
	}
	return count, true
}

// lineStartOffset returns the byte offset where line `index` begins.
//
// It assumes \n endings, which is what a tmux capture produces.
func lineStartOffset(content string, lines []string, index int) int {
	if index > len(lines) {
		index = len(lines)
	}
	offset := 0
	for _, line := range lines[:index] {
		offset += len(line) + 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return offset
}

func sliceFromLine(content string, lines []string, index int) string {
	return content[lineStartOffset(content, lines, index):]
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(content, "\n")
	return strings.Split(trimmed, "\n")
}

// bottomLines returns the last N physical lines, blanks included.
func bottomLines(content string, count int) string {
	lines := splitLines(content)
	start := len(lines) - count
	if start < 0 {
		start = 0
	}
	return sliceFromLine(content, lines, start)
}

// bottomNonEmptyLines returns everything from the topmost of the last N
// non-blank lines to the end.
//
// Blank lines interleaved between those non-blank ones are included — the
// region is a contiguous tail, not a filtered set.
func bottomNonEmptyLines(content string, count int) string {
	lines := splitLines(content)
	if count == 0 {
		return ""
	}

	seen := 0
	start := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		seen++
		start = index
		if seen == count {
			break
		}
	}
	if start < 0 {
		return ""
	}
	return sliceFromLine(content, lines, start)
}

// topNonEmptyLines returns the prefix through the Nth non-blank line
// inclusive, keeping any leading blank lines.
func topNonEmptyLines(content string, count int) string {
	lines := splitLines(content)
	if count == 0 {
		return ""
	}

	seen := 0
	end := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		seen++
		end = index
		if seen == count {
			break
		}
	}
	if end < 0 {
		return ""
	}
	return content[:lineStartOffset(content, lines, end+1)]
}

// isHorizontalRule reports whether a line is one of the box-drawing rules
// agent UIs use to fence their prompt.
//
// A line qualifies when it starts with a run of U+2500 and either nothing
// follows, or the run is long enough to read as a rule with a title on it
// (`─── Title ───`).
func isHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	ruleChars := 0
	offset := 0
	for _, ch := range trimmed {
		if ch != '─' {
			break
		}
		ruleChars++
		offset += len(string(ch))
	}
	if ruleChars == 0 {
		return false
	}
	return strings.TrimSpace(trimmed[offset:]) == "" || ruleChars >= 3
}

// afterLastHorizontalRule returns everything below the last rule line, or the
// whole content when there is none.
func afterLastHorizontalRule(content string) string {
	lines := splitLines(content)
	last := 0
	offset := 0
	for _, line := range lines {
		next := offset + len(line) + 1
		if isHorizontalRule(line) {
			last = min(next, len(content))
		}
		offset = next
	}
	return content[last:]
}

// promptBoxTopBorder finds the second-from-bottom rule line, which is the top
// border of the prompt box.
func promptBoxTopBorder(lines []string) (int, bool) {
	borders := 0
	for index := len(lines) - 1; index >= 0; index-- {
		if !isHorizontalRule(lines[index]) {
			continue
		}
		borders++
		if borders == 2 {
			return index, true
		}
	}
	return 0, false
}

// promptBoxBody returns the lines between the prompt box's top border and the
// next rule below it.
func promptBoxBody(content string) string {
	lines := splitLines(content)
	top, ok := promptBoxTopBorder(lines)
	if !ok {
		return ""
	}
	start := lineStartOffset(content, lines, top+1)

	endIndex := len(lines)
	for index := top + 1; index < len(lines); index++ {
		if isHorizontalRule(lines[index]) {
			endIndex = index
			break
		}
	}
	end := lineStartOffset(content, lines, endIndex)
	if end < start {
		return ""
	}
	return content[start:end]
}

// afterLastPromptMarker returns everything below codex's last `›` prompt line.
func afterLastPromptMarker(content string) string {
	lines := splitLines(content)
	last := -1
	for index, line := range lines {
		if line == "›" || strings.HasPrefix(line, "› ") {
			last = index
		}
	}
	if last < 0 {
		return content
	}
	return sliceFromLine(content, lines, last+1)
}
