package tmux

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	claudeSessionsTTL = 10 * time.Second
	claudeDir         = ".claude"
	sessionsDir       = "sessions"
	projectsDir       = "projects"
)

// TokenUsage holds aggregated token counts and estimated cost for a Claude session.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	TotalCost    float64 // estimated USD
}

// claudeSessionFile represents the JSON structure of ~/.claude/sessions/{PID}.json.
//
// This is Claude Code's private, unversioned format (it carries its own
// "peerProtocol" number). Every field is optional as far as we are concerned:
// a renamed or removed field decodes to its zero value, which callers must
// treat as "state unknown" rather than as an error.
type claudeSessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`

	// Tmux is "<session>:@<window_id>.%<pane_id>" when Claude runs under tmux.
	Tmux string `json:"tmux"`
	// ProcStart is field 22 of /proc/<pid>/stat, stored as a string.
	ProcStart string `json:"procStart"`
	// Status is one of "busy", "shell", "idle", "waiting".
	Status string `json:"status"`
	// WaitingFor explains the block; present only when Status == "waiting".
	WaitingFor string `json:"waitingFor"`
	// Name is what Claude calls the work in hand, e.g.
	// "panel-session-last-notification". Present on every live session file
	// observed, but optional like everything else here.
	Name string `json:"name"`
	// StatusUpdatedAt is when the current status began, in epoch milliseconds.
	StatusUpdatedAt int64 `json:"statusUpdatedAt"`
}

// homeDir resolves the user's home directory. Replaceable in tests.
var homeDir = os.UserHomeDir

// claudeSessionsDir returns ~/.claude/sessions.
func claudeSessionsDir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, claudeDir, sessionsDir), nil
}

// jsonlMessage is a minimal representation of a JSONL line with usage data.
type jsonlMessage struct {
	Type    string `json:"type"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type cachedUsage struct {
	usage     *TokenUsage
	expiresAt time.Time
}

var (
	usageCache   = make(map[string]cachedUsage) // sessionID → cached usage
	usageCacheMu sync.Mutex
)

// FindClaudeSession locates a Claude Code session file for a given tmux pane PID.
// It scans child processes to find the Claude PID, then reads its session file.
func FindClaudeSession(panePID int) (sessionID string, cwd string, err error) {
	sessDir, err := claudeSessionsDir()
	if err != nil {
		return "", "", err
	}

	// Get child PIDs of the pane shell
	out, err := runner.Output("pgrep", "-P", fmt.Sprintf("%d", panePID))
	if err != nil {
		return "", "", fmt.Errorf("no child processes for pane %d", panePID)
	}

	for _, pidStr := range strings.Fields(string(out)) {
		path := filepath.Join(sessDir, pidStr+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sf claudeSessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		return sf.SessionID, sf.CWD, nil
	}

	return "", "", fmt.Errorf("no claude session found for pane %d", panePID)
}

// LoadTokenUsage reads and aggregates token usage from a Claude session's JSONL log.
// Results are cached with a TTL to avoid re-reading large files on every tick.
func LoadTokenUsage(sessionID, cwd string) (*TokenUsage, error) {
	usageCacheMu.Lock()
	if cached, ok := usageCache[sessionID]; ok && time.Now().Before(cached.expiresAt) {
		usageCacheMu.Unlock()
		return cached.usage, nil
	}
	usageCacheMu.Unlock()

	home, err := homeDir()
	if err != nil {
		return nil, err
	}

	encoded := encodePath(cwd)
	jsonlPath := filepath.Join(home, claudeDir, projectsDir, encoded, sessionID+".jsonl")

	usage, err := parseTokenUsage(jsonlPath)
	if err != nil {
		return nil, err
	}

	usageCacheMu.Lock()
	usageCache[sessionID] = cachedUsage{
		usage:     usage,
		expiresAt: time.Now().Add(claudeSessionsTTL),
	}
	usageCacheMu.Unlock()

	return usage, nil
}

func parseTokenUsage(path string) (*TokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	usage := &TokenUsage{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024) // handle large lines

	for scanner.Scan() {
		line := scanner.Bytes()

		// Quick filter: skip lines without "usage"
		if !containsBytes(line, []byte(`"usage"`)) {
			continue
		}

		var msg jsonlMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Type != "assistant" {
			continue
		}

		u := msg.Message.Usage
		usage.InputTokens += u.InputTokens
		usage.OutputTokens += u.OutputTokens
		usage.CacheRead += u.CacheReadInputTokens
		usage.CacheWrite += u.CacheCreationInputTokens
	}

	usage.TotalCost = estimateCost(usage)
	return usage, nil
}

// estimateCost calculates an approximate USD cost from token counts.
// Uses Claude Opus 4.6 pricing as default.
func estimateCost(u *TokenUsage) float64 {
	const (
		inputPer1M      = 15.0
		outputPer1M     = 75.0
		cacheReadPer1M  = 1.5
		cacheWritePer1M = 18.75
	)
	cost := float64(u.InputTokens) / 1_000_000 * inputPer1M
	cost += float64(u.OutputTokens) / 1_000_000 * outputPer1M
	cost += float64(u.CacheRead) / 1_000_000 * cacheReadPer1M
	cost += float64(u.CacheWrite) / 1_000_000 * cacheWritePer1M
	return cost
}

// encodePath converts a filesystem path to the Claude projects directory encoding.
// "/Users/foo/bar" → "-Users-foo-bar"
func encodePath(path string) string {
	return strings.ReplaceAll(path, string(os.PathSeparator), "-")
}

// FormatTokens formats a token count into a short human-readable string.
func FormatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func containsBytes(haystack, needle []byte) bool {
	return strings.Contains(string(haystack), string(needle))
}
