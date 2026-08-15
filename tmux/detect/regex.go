package detect

import (
	"fmt"
	"regexp"
	"strings"
)

// The manifests are copied verbatim from herdr so they can be re-synced by
// overwriting the .toml files. herdr compiles them with Rust's regex crate,
// which is RE2-like — no backreferences, no lookaround, so every pattern *can*
// be expressed in Go. What differs is the vocabulary:
//
//   - Rust accepts \uXXXX and \u{XXXX}; Go accepts only \x{XXXX}.
//   - Rust supports Unicode property classes such as \p{Alphabetic}; RE2
//     exposes only general categories and scripts.
//   - Rust's \s \w \d \b are Unicode-aware; Go's are ASCII-only.
//
// The last one is the dangerous difference, because it is silent: `^\s*❯` still
// compiles under Go, it just stops matching when a terminal pads the line with
// a non-breaking or ideographic space, which agent UIs really do. So rather
// than editing the manifests (which would break re-syncing), patterns are
// translated on the way in.
//
// Translation is textual and therefore has to respect regex context: an escape
// only means what it means outside a character class, and a backslash only
// starts an escape when it is not itself escaped.

const (
	// Unicode whitespace, matching Rust's \s (\p{White_Space}).
	unicodeSpace    = `\t\n\v\f\r \x{0085}\p{Z}`
	unicodeNotSpace = `^` + unicodeSpace
	// Unicode word characters, matching Rust's \w.
	unicodeWord    = `\p{L}\p{M}\p{Nd}\p{Pc}`
	unicodeNotWord = `^` + unicodeWord
	// Rust's \p{Alphabetic} has no RE2 equivalent; letters plus the marks and
	// letter-numbers that Alphabetic also covers is the closest honest subset.
	alphabeticClass = `\p{L}\p{M}\p{Nl}`
)

// translatePattern rewrites a Rust-regex pattern into an equivalent Go one.
//
// It returns an error only for constructs that cannot be represented at all;
// everything else is either translated or passed through untouched.
func translatePattern(pattern string) (string, error) {
	var out strings.Builder
	out.Grow(len(pattern) * 2)

	runes := []rune(pattern)
	inClass := false

	for index := 0; index < len(runes); index++ {
		ch := runes[index]

		if ch != '\\' {
			// Track character-class nesting so escapes can be expanded with the
			// right syntax. A ']' immediately after '[' or '[^' is a literal.
			switch {
			case ch == '[' && !inClass:
				inClass = true
			case ch == ']' && inClass:
				inClass = false
			}
			out.WriteRune(ch)
			continue
		}

		if index+1 >= len(runes) {
			// A trailing backslash is invalid in both flavours; let Go report it.
			out.WriteRune(ch)
			continue
		}

		next := runes[index+1]
		switch next {
		case 'u':
			replacement, consumed, err := translateUnicodeEscape(runes, index)
			if err != nil {
				return "", err
			}
			out.WriteString(replacement)
			index += consumed - 1

		case 's', 'S', 'w', 'W', 'd', 'D':
			// A negated set cannot be spliced into an enclosing character
			// class — `[\S;]` would have to become `[[^...];]`, which means
			// something else. No shipped manifest does this; fail loudly if one
			// ever starts, rather than emitting a pattern that quietly differs.
			if inClass && (next == 'S' || next == 'W' || next == 'D') {
				return "", fmt.Errorf(`\%c inside a character class cannot be translated`, next)
			}
			out.WriteString(expandPerlClass(next, inClass))
			index++

		case 'p', 'P':
			replacement, consumed, ok := translateUnicodeProperty(runes, index, inClass)
			if !ok {
				// Not a property we rewrite; copy the escape through.
				out.WriteRune(ch)
				out.WriteRune(next)
				index++
				continue
			}
			out.WriteString(replacement)
			index += consumed - 1

		default:
			// Everything else is shared syntax: \. \\ \x{...} \A and so on.
			//
			// \b is the one knowing exception. Rust's is a Unicode word
			// boundary, Go's is ASCII, and RE2 has no way to spell the Unicode
			// one. It is left alone: the shipped uses are `yes\b`, `no\b`,
			// `tasks?\b` and similar, which only diverge when the next
			// character is a non-ASCII letter or digit — a localized UI writing
			// "yes" followed by a CJK character. Rare, and the alternative
			// (rewriting \b into explicit guards) risks changing more patterns
			// than it fixes.
			out.WriteRune(ch)
			out.WriteRune(next)
			index++
		}
	}

	translated := out.String()
	if _, err := regexp.Compile(translated); err != nil {
		return "", fmt.Errorf("pattern %q translated to %q: %w", pattern, translated, err)
	}
	return translated, nil
}

// translateUnicodeEscape rewrites `\uXXXX` and `\u{...}` into `\x{...}`.
//
// Returns the replacement and how many runes of input it consumed.
func translateUnicodeEscape(runes []rune, start int) (string, int, error) {
	// runes[start] == '\\', runes[start+1] == 'u'
	rest := runes[start+2:]

	if len(rest) > 0 && rest[0] == '{' {
		end := indexRune(rest, '}')
		if end < 0 {
			return "", 0, fmt.Errorf(`unterminated \u{...} escape`)
		}
		digits := string(rest[1:end])
		if !isHex(digits) {
			return "", 0, fmt.Errorf(`invalid \u{%s} escape`, digits)
		}
		return `\x{` + digits + `}`, 2 + end + 1, nil
	}

	if len(rest) < 4 {
		return "", 0, fmt.Errorf(`truncated \uXXXX escape`)
	}
	digits := string(rest[:4])
	if !isHex(digits) {
		return "", 0, fmt.Errorf(`invalid \u%s escape`, digits)
	}
	return `\x{` + digits + `}`, 2 + 4, nil
}

// translateUnicodeProperty rewrites the Unicode properties RE2 lacks.
//
// Only properties that have no RE2 spelling are touched; general categories
// and scripts pass through so `\p{L}` and `\p{Nd}` keep working.
func translateUnicodeProperty(runes []rune, start int, inClass bool) (string, int, bool) {
	// runes[start] == '\\', runes[start+1] == 'p' or 'P'
	negated := runes[start+1] == 'P'
	rest := runes[start+2:]
	if len(rest) == 0 || rest[0] != '{' {
		return "", 0, false
	}
	end := indexRune(rest, '}')
	if end < 0 {
		return "", 0, false
	}

	name := string(rest[1:end])
	var class string
	switch name {
	case "Alphabetic", "alphabetic", "Alpha", "alpha":
		class = alphabeticClass
	default:
		return "", 0, false
	}

	consumed := 2 + end + 1
	return wrapClass(class, negated, inClass), consumed, true
}

// expandPerlClass rewrites Rust's Unicode-aware \s \w \d and their negations.
//
// \d is Unicode-aware in Rust too, but Go spells the same set as \p{Nd}, which
// needs no bracket wrapping, so it is handled alongside the others.
func expandPerlClass(kind rune, inClass bool) string {
	switch kind {
	case 's':
		return wrapClass(unicodeSpace, false, inClass)
	case 'S':
		return wrapClass(unicodeSpace, true, inClass)
	case 'w':
		return wrapClass(unicodeWord, false, inClass)
	case 'W':
		return wrapClass(unicodeWord, true, inClass)
	case 'd':
		return `\p{Nd}`
	case 'D':
		return `\P{Nd}`
	}
	return string(kind)
}

// wrapClass renders a set of class members for use inside or outside a
// character class.
//
// Inside a class the members are spliced in bare, so `[\s;]` becomes
// `[\t\n...;]`. Negation is only representable outside a class; callers reject
// the inside-a-class case before reaching here.
func wrapClass(members string, negated, inClass bool) string {
	if inClass {
		return members
	}
	if negated {
		return "[^" + members + "]"
	}
	return "[" + members + "]"
}

func indexRune(runes []rune, target rune) int {
	for index, ch := range runes {
		if ch == target {
			return index
		}
	}
	return -1
}

func isHex(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'f':
		case ch >= 'A' && ch <= 'F':
		default:
			return false
		}
	}
	return true
}
