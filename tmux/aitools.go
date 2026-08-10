package tmux

// AITool describes a known AI CLI tool with its display metadata.
type AITool struct {
	Name  string
	Icon  string
	Color string // hex color code
}

// aiToolMap is the single source of truth for known AI CLI tools.
var aiToolMap = map[string]AITool{
	"claude": {Name: "claude", Icon: "✦", Color: "#F59E0B"},
	"codex":  {Name: "codex", Icon: "◈", Color: "#60A5FA"},
	"aider":  {Name: "aider", Icon: "⬡", Color: "#34D399"},
	"gemini": {Name: "gemini", Icon: "✧", Color: "#A78BFA"},
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

// SessionAITool returns the AI tool to display for a session.
//
// Live state wins over the active-pane command, because ActiveCommand only
// reflects the active pane of the active window while the state file covers
// the whole session — so a tool running in a background window is still
// surfaced.
func SessionAITool(s Session) (AITool, bool) {
	if s.AIState != AIStateNone {
		// ponytail: Claude is the only tool publishing live state today. A
		// second provider must carry its own tool name onto Session.
		t, ok := aiToolMap["claude"]
		return t, ok
	}
	return LookupAITool(s.ActiveCommand)
}
