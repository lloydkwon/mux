// Package detect decides what an AI CLI is doing from the text on its screen.
//
// The rules are not ours. They are herdr's agent-detection manifests, copied
// verbatim into manifests/ so they can be re-synced by overwriting the .toml
// files — those rules track twenty agents' UIs as they change, and rewriting
// them by hand would mean re-learning every one. What lives here is the engine
// that evaluates them, ported to Go, plus the translation layer that reconciles
// Rust's regex dialect with Go's (see regex.go).
//
// The engine is pure: it takes text and returns a state. Everything that
// shells out to tmux to *get* that text lives in the parent package.
package detect

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed manifests/*.toml
var manifestFS embed.FS

// State is what a rule concludes about the agent. It mirrors herdr's
// AgentState; the parent package maps it onto mux's AIState.
type State int

const (
	// StateUnknown means no rule matched and the agent is unrecognized.
	StateUnknown State = iota
	// StateIdle means the agent finished its turn and awaits input.
	StateIdle
	// StateWorking means the agent is processing.
	StateWorking
	// StateBlocked means the agent needs an answer from the user.
	StateBlocked
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateWorking:
		return "working"
	case StateBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

func parseState(value string) State {
	switch strings.TrimSpace(value) {
	case "idle":
		return StateIdle
	case "working":
		return StateWorking
	case "blocked":
		return StateBlocked
	default:
		return StateUnknown
	}
}

// Gate is one matcher node. A rule is a gate with metadata attached, and a gate
// may nest further gates, so the same evaluation function serves both.
type Gate struct {
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
	All       []Gate   `toml:"all"`
	Any       []Gate   `toml:"any"`
	Not       []Gate   `toml:"not"`
}

// Rule is one `[[rules]]` entry.
//
// The matcher fields sit alongside the metadata in the TOML rather than under a
// nested table, so they are declared flat here and gathered by Gate().
type Rule struct {
	ID       string `toml:"id"`
	State    string `toml:"state"`
	Priority int    `toml:"priority"`
	Region   string `toml:"region"`

	VisibleIdle     bool `toml:"visible_idle"`
	VisibleBlocker  bool `toml:"visible_blocker"`
	VisibleWorking  bool `toml:"visible_working"`
	SkipStateUpdate bool `toml:"skip_state_update"`

	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
	All       []Gate   `toml:"all"`
	Any       []Gate   `toml:"any"`
	Not       []Gate   `toml:"not"`
}

// Gate returns the rule's matcher tree.
func (r Rule) Gate() Gate {
	return Gate{
		Contains:  r.Contains,
		Regex:     r.Regex,
		LineRegex: r.LineRegex,
		All:       r.All,
		Any:       r.Any,
		Not:       r.Not,
	}
}

// Manifest is one agent's rule set.
type Manifest struct {
	ID      string   `toml:"id"`
	Version string   `toml:"version"`
	Aliases []string `toml:"aliases"`
	Rules   []Rule   `toml:"rules"`

	// Parsed but unused: herdr enforces MinEngineVersion only for manifests it
	// fetches at runtime. We ship exactly what we embed, so there is nothing to
	// reject — but the field must be declared or strict decoding rejects it.
	MinEngineVersion int    `toml:"min_engine_version"`
	UpdatedAt        string `toml:"updated_at"`
}

// defaultRegion matches herdr's: a rule that names no region reads the whole
// captured screen.
const defaultRegion = "whole_recent"

// parseManifest decodes one manifest, rejecting unknown keys the way herdr's
// serde(deny_unknown_fields) does. A typo in a re-synced manifest should be an
// error, not a rule that silently never fires.
func parseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	meta, err := toml.Decode(string(data), &manifest)
	if err != nil {
		return Manifest{}, err
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Manifest{}, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return Manifest{}, fmt.Errorf("manifest has no id")
	}
	if len(manifest.Rules) == 0 {
		return Manifest{}, fmt.Errorf("manifest %s has no rules", manifest.ID)
	}
	for index := range manifest.Rules {
		if strings.TrimSpace(manifest.Rules[index].ID) == "" {
			return Manifest{}, fmt.Errorf("manifest %s has a rule with no id", manifest.ID)
		}
		if strings.TrimSpace(manifest.Rules[index].Region) == "" {
			manifest.Rules[index].Region = defaultRegion
		}
	}
	return manifest, nil
}

// loadBundledManifests decodes every embedded manifest, keyed by manifest id.
func loadBundledManifests() (map[string]Manifest, error) {
	entries, err := manifestFS.ReadDir("manifests")
	if err != nil {
		return nil, err
	}

	manifests := make(map[string]Manifest, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		data, err := manifestFS.ReadFile(path.Join("manifests", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		manifest, err := parseManifest(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		manifests[manifest.ID] = manifest
	}
	return manifests, nil
}
