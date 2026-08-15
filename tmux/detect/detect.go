package detect

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Detection is what the engine concludes about one pane.
type Detection struct {
	State State
	// RuleID names the rule that decided it, empty when none matched.
	RuleID string
	// SkipStateUpdate marks a screen that is showing the agent's own transcript
	// or a menu rather than live prompt state. The caller should keep whatever
	// it already believed instead of trusting State.
	SkipStateUpdate bool
}

// compiledGate is a Gate with its needles lowered and its patterns compiled.
type compiledGate struct {
	contains  []string
	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
	all       []compiledGate
	any       []compiledGate
	not       []compiledGate
}

type compiledRule struct {
	rule Rule
	gate compiledGate
}

type compiledManifest struct {
	rules []compiledRule
}

var (
	engineOnce sync.Once
	engine     map[string]*compiledManifest
	// byAlias maps every id and alias to its manifest, so a pane's command name
	// can be looked up directly.
	byAlias   map[string]*compiledManifest
	engineErr error
)

// load compiles the embedded manifests once.
func load() (map[string]*compiledManifest, error) {
	engineOnce.Do(func() {
		manifests, err := loadBundledManifests()
		if err != nil {
			engineErr = err
			return
		}

		engine = make(map[string]*compiledManifest, len(manifests))
		byAlias = make(map[string]*compiledManifest, len(manifests)*2)
		for id, manifest := range manifests {
			compiled, err := compileManifest(manifest)
			if err != nil {
				engineErr = fmt.Errorf("%s: %w", id, err)
				return
			}
			engine[id] = compiled
			byAlias[strings.ToLower(id)] = compiled
			for _, alias := range manifest.Aliases {
				byAlias[strings.ToLower(alias)] = compiled
			}
		}
	})
	return engine, engineErr
}

func compileManifest(manifest Manifest) (*compiledManifest, error) {
	compiled := &compiledManifest{
		rules: make([]compiledRule, 0, len(manifest.Rules)),
	}
	for _, rule := range manifest.Rules {
		if !knownRegion(rule.Region) {
			return nil, fmt.Errorf("rule %s names region %q, which this engine does not implement",
				rule.ID, rule.Region)
		}
		gate, err := compileGate(rule.Gate())
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
		}
		compiled.rules = append(compiled.rules, compiledRule{rule: rule, gate: gate})
	}
	return compiled, nil
}

func compileGate(gate Gate) (compiledGate, error) {
	out := compiledGate{}

	// Needles are lowered once here; the region text is lowered once per
	// evaluation, so `contains` is case-insensitive on both sides.
	for _, needle := range gate.Contains {
		out.contains = append(out.contains, strings.ToLower(needle))
	}

	compileAll := func(patterns []string) ([]*regexp.Regexp, error) {
		compiled := make([]*regexp.Regexp, 0, len(patterns))
		for _, pattern := range patterns {
			translated, err := translatePattern(pattern)
			if err != nil {
				return nil, err
			}
			re, err := regexp.Compile(translated)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", pattern, err)
			}
			compiled = append(compiled, re)
		}
		return compiled, nil
	}

	var err error
	if out.regex, err = compileAll(gate.Regex); err != nil {
		return compiledGate{}, err
	}
	if out.lineRegex, err = compileAll(gate.LineRegex); err != nil {
		return compiledGate{}, err
	}

	for _, nested := range gate.All {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		out.all = append(out.all, child)
	}
	for _, nested := range gate.Any {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		out.any = append(out.any, child)
	}
	for _, nested := range gate.Not {
		child, err := compileGate(nested)
		if err != nil {
			return compiledGate{}, err
		}
		out.not = append(out.not, child)
	}
	return out, nil
}

// matches evaluates a gate against a region.
//
// The shape is herdr's: everything listed must hold, `any` is a disjunction
// only when non-empty, and `not` fails the gate if anything under it matches.
func (g compiledGate) matches(text, lowerText string) bool {
	for _, needle := range g.contains {
		if !strings.Contains(lowerText, needle) {
			return false
		}
	}
	for _, re := range g.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range g.lineRegex {
		if !matchesSomeLine(re, text) {
			return false
		}
	}
	for _, nested := range g.all {
		if !nested.matches(text, lowerText) {
			return false
		}
	}
	if len(g.any) > 0 {
		matched := false
		for _, nested := range g.any {
			if nested.matches(text, lowerText) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, nested := range g.not {
		if nested.matches(text, lowerText) {
			return false
		}
	}
	return true
}

func matchesSomeLine(re *regexp.Regexp, text string) bool {
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if re.MatchString(strings.TrimSuffix(line, "\r")) {
			return true
		}
	}
	return false
}

// Supported reports whether an agent name has a manifest, matching on the
// manifest id or any alias.
func Supported(agent string) bool {
	if _, err := load(); err != nil {
		return false
	}
	_, ok := byAlias[strings.ToLower(strings.TrimSpace(agent))]
	return ok
}

// Detect runs an agent's rules against a screen.
//
// A known agent whose rules all miss reports StateIdle, not StateUnknown: the
// manifests describe what working and blocked look like, so "none of those"
// means the agent is sitting at its prompt. An agent with no manifest reports
// StateUnknown.
func Detect(agent string, input Input) Detection {
	if _, err := load(); err != nil {
		return Detection{State: StateUnknown}
	}
	manifest, ok := byAlias[strings.ToLower(strings.TrimSpace(agent))]
	if !ok {
		return Detection{State: StateUnknown}
	}

	// Region text is reused across rules that name the same region, and each is
	// lowered once — 109 rules over 20 manifests would otherwise lower the same
	// screen many times per tick.
	regionCache := make(map[string]regionText, 4)
	regionFor := func(spec string) regionText {
		if cached, ok := regionCache[spec]; ok {
			return cached
		}
		text := region(input, spec)
		cached := regionText{text: text, lower: strings.ToLower(text)}
		regionCache[spec] = cached
		return cached
	}

	var winner *compiledRule
	for index := range manifest.rules {
		candidate := &manifest.rules[index]
		// Highest priority wins; on a tie the earliest-declared rule keeps it.
		if winner != nil && winner.rule.Priority >= candidate.rule.Priority {
			continue
		}
		text := regionFor(candidate.rule.Region)
		if candidate.gate.matches(text.text, text.lower) {
			winner = candidate
		}
	}

	if winner == nil {
		return Detection{State: StateIdle}
	}

	state := parseState(winner.rule.State)
	return Detection{
		State:           state,
		RuleID:          winner.rule.ID,
		SkipStateUpdate: winner.rule.SkipStateUpdate,
	}
}

type regionText struct {
	text  string
	lower string
}
