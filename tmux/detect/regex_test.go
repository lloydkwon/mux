package detect

import (
	"regexp"
	"strings"
	"testing"
)

// The manifests are herdr's, written for Rust's regex crate. These tests pin
// the translation to Go's syntax, because every failure mode here is silent:
// a pattern that still compiles but stops matching costs a wrong badge, not an
// error.

func TestTranslateRewritesUnicodeEscapes(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		want    string
	}{
		// Go has no \uXXXX. Braille spinners are written this way by
		// antigravity, cursor, droid, kimi and qodercli.
		{"four digit", "[\\u2800-\\u28FF]", `[\x{2800}-\x{28FF}]`},
		// Go has no \u{...} either. hermes uses it for variation selectors.
		{"braced", `^⚠[\u{fe0e}\u{fe0f}]?`, `^⚠[\x{fe0e}\x{fe0f}]?`},
		// \x{...} is already shared syntax and must survive untouched.
		{"already hex", `^[\x{2800}-\x{28FF}] `, `^[\x{2800}-\x{28FF}] `},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := translatePattern(tc.pattern)
			if err != nil {
				t.Fatalf("translate %q: %v", tc.pattern, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTranslateReplacesPropertiesRE2Lacks(t *testing.T) {
	// \p{Alphabetic} is a Unicode property; RE2 exposes only categories and
	// scripts. kiro, cursor, antigravity and qodercli all use it.
	got, err := translatePattern(`\p{Alphabetic}+`)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if !strings.Contains(got, `\p{L}`) {
		t.Errorf("got %q, want a letter class", got)
	}
	if strings.Contains(got, "Alphabetic") {
		t.Errorf("got %q, still names a property RE2 cannot compile", got)
	}

	// Categories RE2 does know must pass through unchanged.
	for _, pattern := range []string{`\p{L}`, `\p{Nd}`, `\p{Z}`} {
		got, err := translatePattern(pattern)
		if err != nil {
			t.Fatalf("translate %q: %v", pattern, err)
		}
		if got != pattern {
			t.Errorf("got %q, want %q unchanged", got, pattern)
		}
	}
}

func TestTranslateMakesWhitespaceUnicodeAware(t *testing.T) {
	// This is the difference that would have been silent. Rust's \s is
	// \p{White_Space}; Go's is ASCII-only. Agent UIs pad with NBSP and
	// ideographic spaces, so `^\s*❯` has to keep matching them.
	translated, err := translatePattern(`^\s*❯`)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	re := regexp.MustCompile(translated)

	for _, line := range []string{
		"❯",
		"  ❯",
		" ❯",  // no-break space
		"　❯",  // ideographic space
		"  ❯", // narrow no-break, figure space
	} {
		if !re.MatchString(line) {
			t.Errorf("translated %q did not match %q", translated, line)
		}
	}

	// The untranslated pattern is what we are protecting against; prove it
	// really does fail, so this test cannot silently stop testing anything.
	if regexp.MustCompile(`^\s*❯`).MatchString("　❯") {
		t.Error("Go's ASCII \\s matched an ideographic space; this test is moot")
	}
}

func TestTranslateSplicesClassesInsideCharacterClasses(t *testing.T) {
	// `[\s;]` must become a class containing whitespace *and* a semicolon, not
	// a nested class.
	translated, err := translatePattern(`^[\s;]+$`)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	re := regexp.MustCompile(translated)
	for _, value := range []string{" ;", " ", ";;"} {
		if !re.MatchString(value) {
			t.Errorf("translated %q did not match %q", translated, value)
		}
	}
	if re.MatchString("x") {
		t.Errorf("translated %q matched a letter", translated)
	}
}

func TestTranslateRejectsNegatedClassesItCannotExpress(t *testing.T) {
	// `[\S;]` has no faithful Go spelling. Failing loudly beats emitting a
	// pattern that quietly means something else.
	if _, err := translatePattern(`[\S;]`); err == nil {
		t.Error("expected an error for a negated class inside a character class")
	}
}

func TestTranslateKeepsInlineFlagsAndGroups(t *testing.T) {
	for _, pattern := range []string{
		`(?i)esc to close`,
		`(?m)^\s*❯\s*$`,
		`(?s).+`,
		`^\s*/btw(?:\s|$)`,
		`\A`,
	} {
		translated, err := translatePattern(pattern)
		if err != nil {
			t.Fatalf("translate %q: %v", pattern, err)
		}
		if _, err := regexp.Compile(translated); err != nil {
			t.Errorf("translated %q to %q which does not compile: %v", pattern, translated, err)
		}
	}
}

func TestTranslateLeavesEscapedBackslashesAlone(t *testing.T) {
	// `\\s` is a literal backslash followed by an 's', not a whitespace class.
	got, err := translatePattern(`\\s`)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got != `\\s` {
		t.Errorf("got %q, want %q", got, `\\s`)
	}
}

// TestEveryShippedPatternTranslates is the real guard: it walks the embedded
// manifests and translates every regex they contain. A manifest re-synced from
// herdr that uses syntax we cannot handle fails here rather than at runtime.
func TestEveryShippedPatternTranslates(t *testing.T) {
	manifests, err := loadBundledManifests()
	if err != nil {
		t.Fatalf("load bundled manifests: %v", err)
	}
	if len(manifests) == 0 {
		t.Fatal("no bundled manifests found")
	}

	patterns := 0
	for _, manifest := range manifests {
		for _, rule := range manifest.Rules {
			walkGates(rule.Gate(), func(gate Gate) {
				for _, pattern := range append(append([]string{}, gate.Regex...), gate.LineRegex...) {
					patterns++
					if _, err := translatePattern(pattern); err != nil {
						t.Errorf("%s rule %s: %v", manifest.ID, rule.ID, err)
					}
				}
			})
		}
	}
	if patterns == 0 {
		t.Fatal("walked the manifests but found no patterns")
	}
	t.Logf("translated %d patterns across %d manifests", patterns, len(manifests))
}

// walkGates visits a gate and every gate nested under it.
func walkGates(gate Gate, visit func(Gate)) {
	visit(gate)
	for _, nested := range gate.All {
		walkGates(nested, visit)
	}
	for _, nested := range gate.Any {
		walkGates(nested, visit)
	}
	for _, nested := range gate.Not {
		walkGates(nested, visit)
	}
}
