package tmux

import "sort"

// AITool describes a known AI CLI tool with its display metadata.
type AITool struct {
	Name  string
	Icon  string
	Color string // hex color code
}

// aiToolMap is the single source of truth for known AI CLI tools.
//
// The colours are ANSI palette indices rather than hex, for the reason spelled
// out on ui's palette: the terminal's own scheme knows what reads on its own
// background. As hex these were amber, blue, mint and lilac — all of them picked
// against a dark terminal, and all of them near-invisible on a light one (the
// worst reached 1.7:1). What matters here is only that they are told apart.
//
// The list outgrew the four tools mux could name by process alone once screen
// detection arrived: tmux/detect ships rules for twenty agents, and a session
// missing from this map gets no badge at all, because aiGlyph gives up when
// SessionAITool cannot name the tool. The keys past the first four are the
// manifest ids in tmux/detect/manifests — keep the two in step.
//
// Every icon measures one cell, which TestGlyphWidthsAreStable pins. Only the
// originals are recognisable shapes; the rest just have to differ from each
// other, since an icon is shown only when a tool has no live state to show
// instead.
var aiToolMap = map[string]AITool{
	"claude": {Name: "claude", Icon: "✦", Color: "3"}, // yellow
	"codex":  {Name: "codex", Icon: "◈", Color: "4"},  // blue
	"aider":  {Name: "aider", Icon: "⬡", Color: "2"},  // green
	"gemini": {Name: "gemini", Icon: "✧", Color: "5"}, // magenta

	"agy":      {Name: "agy", Icon: "◇", Color: "6"},
	"amp":      {Name: "amp", Icon: "◆", Color: "1"},
	"cline":    {Name: "cline", Icon: "○", Color: "2"},
	"copilot":  {Name: "copilot", Icon: "●", Color: "4"},
	"cursor":   {Name: "cursor", Icon: "▲", Color: "6"},
	"devin":    {Name: "devin", Icon: "△", Color: "3"},
	"droid":    {Name: "droid", Icon: "■", Color: "2"},
	"grok":     {Name: "grok", Icon: "□", Color: "1"},
	"hermes":   {Name: "hermes", Icon: "▣", Color: "5"},
	"kilo":     {Name: "kilo", Icon: "▽", Color: "4"},
	"kimi":     {Name: "kimi", Icon: "▼", Color: "6"},
	"kiro":     {Name: "kiro", Icon: "◐", Color: "3"},
	"maki":     {Name: "maki", Icon: "◑", Color: "2"},
	"opencode": {Name: "opencode", Icon: "◒", Color: "5"},
	"pi":       {Name: "pi", Icon: "◓", Color: "1"},
	"qodercli": {Name: "qodercli", Icon: "◔", Color: "4"},
	"qwen":     {Name: "qwen", Icon: "◕", Color: "6"},
}

// AIState is the coarse, display-oriented state of an AI CLI session. Only a
// tool that publishes its own state ever reports anything but AIStateNone —
// today that is Claude Code alone (see claude_status.go).
type AIState int

const (
	// AIStateNone means no live AI state, or a state we don't model.
	AIStateNone AIState = iota
	// AIStateWorking means the tool is processing.
	AIStateWorking
	// AIStateApproval means the tool is blocked waiting on the user.
	AIStateApproval
	// AIStateReady means the turn finished and the tool awaits input.
	AIStateReady
	// AIStateShell means the tool handed the terminal to a shell — a command
	// the user (or the tool) left running in the foreground, e.g. a download.
	AIStateShell
)

func (s AIState) String() string {
	switch s {
	case AIStateWorking:
		return "working"
	case AIStateApproval:
		return "approval"
	case AIStateReady:
		return "waiting"
	case AIStateShell:
		return "shell"
	default:
		return ""
	}
}

// Icon returns the badge glyph for a live state, or "" for AIStateNone so the
// caller falls back to the tool's own icon. These glyphs replace the tool icon
// rather than sitting beside it — one badge answers both "which tool" and
// "what is it doing".
//
// Every glyph here is Emoji_Presentation: it measures and draws 2 cells, where
// the tool icons measure and draw 1. Any replacement must keep that property,
// because the manual renderer in ui/layout.go cannot compensate for a
// mis-measured glyph.
func (s AIState) Icon() string {
	switch s {
	case AIStateWorking:
		return "⏳"
	case AIStateApproval:
		return "❗"
	case AIStateReady:
		return "✅"
	case AIStateShell:
		return "💻"
	default:
		return ""
	}
}

// IsAICommand reports whether cmd is a known AI CLI process.
func IsAICommand(cmd string) bool {
	_, ok := aiToolMap[cmd]
	return ok
}

// LookupAITool returns the AITool for the given command name, if known.
func LookupAITool(cmd string) (AITool, bool) {
	t, ok := aiToolMap[cmd]
	return t, ok
}

// AITools returns every known tool, in name order.
//
// It exists so callers can check a property of the whole set rather than a
// hand-copied list of it — the glyph-width test being the one that matters,
// since a tool whose icon measures two cells would shift the branch column on
// its row alone.
func AITools() []AITool {
	tools := make([]AITool, 0, len(aiToolMap))
	for _, tool := range aiToolMap {
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// SessionAITool returns the AI tool to display for a session.
//
// Live state wins over the active-pane command, because ActiveCommand only
// reflects the active pane of the active window while the state file covers
// the whole session — so a tool running in a background window is still
// surfaced.
func SessionAITool(s Session) (AITool, bool) {
	// Whoever produced the state names the tool, so a second provider no longer
	// has to be guessed at. Screen detection sets this from the manifest it
	// matched; the Claude state file sets it to claude.
	if s.AITool != "" {
		if t, ok := aiToolMap[s.AITool]; ok {
			return t, true
		}
	}
	if t, ok := LookupAITool(s.ActiveCommand); ok {
		return t, true
	}
	if s.AIState != AIStateNone {
		// A live state must always produce a badge, even from a provider that
		// forgot to name its tool — otherwise the session would render as if it
		// ran no AI CLI at all. Which tool is named here is invisible anyway:
		// the state glyph replaces the tool icon whenever there is a state.
		if t, ok := aiToolMap["claude"]; ok {
			return t, true
		}
	}
	return AITool{}, false
}
