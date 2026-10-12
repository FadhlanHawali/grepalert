package rules

import (
	"bytes"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// Prefilter is a cheap necessary condition for a regexp to match: a set of
// literals of which at least one must appear in any matching line. Checking it
// with a substring search before running the regexp makes rules like
// `(?i)panic|fatal` (which Go's regexp engine cannot accelerate) roughly 50x
// faster on the lines that do not match, which is nearly all of them.
//
// A Prefilter never rejects a line the regexp would match.
type Prefilter struct {
	lits [][]byte
	fold bool // compare ASCII case-insensitively; lits are lower-case
	// unicodeFold: some folded literal contains 'k' or 's', which Go's (?i)
	// also matches as the non-ASCII 'K' (U+212A) and 'ſ' (U+017F). Lines with
	// non-ASCII bytes are then passed to the regexp unconditionally.
	unicodeFold bool
}

// minPrefilterLen: shorter literals match too often to be worth checking.
const minPrefilterLen = 3

// NewPrefilter derives a prefilter from a pattern, or returns nil if the
// pattern has no usable required literals.
func NewPrefilter(pattern string) *Prefilter {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil
	}
	lits, fold, ok := required(re.Simplify())
	if !ok || len(lits) == 0 {
		return nil
	}
	p := &Prefilter{fold: fold}
	for _, l := range lits {
		if len(l) < minPrefilterLen || strings.ContainsRune(l, utf8.RuneError) {
			// Too unselective, or U+FFFD, which the regexp also matches
			// against invalid UTF-8 bytes that a byte search would not.
			return nil
		}
		if fold {
			for i := 0; i < len(l); i++ {
				if l[i] >= utf8.RuneSelf {
					return nil // only ASCII folding is handled
				}
			}
			l = strings.ToLower(l)
			if strings.ContainsAny(l, "ks") {
				p.unicodeFold = true
			}
		}
		p.lits = append(p.lits, []byte(l))
	}
	return p
}

// MayMatch reports whether line could match the regexp.
func (p *Prefilter) MayMatch(line []byte) bool {
	for _, l := range p.lits {
		if p.fold {
			if containsFoldASCII(line, l) {
				return true
			}
		} else if bytes.Contains(line, l) {
			return true
		}
	}
	return p.unicodeFold && hasNonASCII(line)
}

func hasNonASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// required returns literals of which at least one occurs in every match of re.
// ok is false when no such set is known (e.g. `.*`, `\d+`, `a?`).
func required(re *syntax.Regexp) (lits []string, fold, ok bool) {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{string(re.Rune)}, re.Flags&syntax.FoldCase != 0, true
	case syntax.OpCapture:
		return required(re.Sub[0])
	case syntax.OpPlus:
		return required(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return required(re.Sub[0])
		}
	case syntax.OpConcat:
		// Every part must match, so any single part's requirement holds.
		// Pick the part whose shortest literal is longest (most selective).
		best := -1
		for _, sub := range re.Sub {
			l, f, ok := required(sub)
			if !ok {
				continue
			}
			if m := minLen(l); m > best {
				best, lits, fold = m, l, f
			}
		}
		return lits, fold, best >= 0
	case syntax.OpAlternate:
		// One branch must match: every branch needs a requirement, and the
		// union is required. Mixed case-sensitivity is folded, which only
		// makes the filter more permissive, never wrong.
		for _, sub := range re.Sub {
			l, f, ok := required(sub)
			if !ok {
				return nil, false, false
			}
			lits = append(lits, l...)
			fold = fold || f
		}
		return lits, fold, true
	}
	return nil, false, false
}

func minLen(lits []string) int {
	m := -1
	for _, l := range lits {
		if m < 0 || len(l) < m {
			m = len(l)
		}
	}
	return m
}

// containsFoldASCII reports whether s contains lit (lower-case ASCII) ignoring
// ASCII case. It jumps between candidate first bytes with IndexByte, so it
// stays fast without allocating a lower-cased copy of the line.
func containsFoldASCII(s, lit []byte) bool {
	first := lit[0]
	upper := first
	if 'a' <= first && first <= 'z' {
		upper = first - 'a' + 'A'
	}
	for i := 0; i+len(lit) <= len(s); {
		j := indexEither(s[i:len(s)-len(lit)+1], first, upper)
		if j < 0 {
			return false
		}
		i += j
		if equalFoldASCII(s[i:i+len(lit)], lit) {
			return true
		}
		i++
	}
	return false
}

func indexEither(s []byte, a, b byte) int {
	i := bytes.IndexByte(s, a)
	if a == b {
		return i
	}
	if j := bytes.IndexByte(s, b); j >= 0 && (i < 0 || j < i) {
		return j
	}
	return i
}

func equalFoldASCII(s, lower []byte) bool {
	for i := range lower {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}
