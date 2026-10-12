package rules

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

func TestPrefilterLiterals(t *testing.T) {
	cases := []struct {
		pattern string
		want    string // lits joined with "|", "" = no prefilter
		fold    bool
	}{
		{`ERROR code=(?P<code>\w+)`, "ERROR code=", false},
		{`(?i)panic|fatal|segfault`, "panic|fatal|segfault", true},
		{`took=(?P<ms>[5-9]\d{3,})ms`, "took=", false},
		{`status=5\d\d`, "status=5", false},
		{`(?i)out of memory`, "out of memory", true},
		{`(ERROR|WARN) disk`, " disk", false},
		{`timeout (after|exceeded)`, "timeout ", false},
		{`(?:connection )+refused`, "connection ", false}, // longest-min part
		{`\d+ms`, "", false},
		{`.*`, "", false},
		{`a|bcd`, "", false},     // "a" too short
		{`(foo)?bar`, "", false}, // "bar" ok but... see below
		{`x*abc`, "abc", false},  // star part skipped, literal kept
		{`(?i)ÉRROR`, "", false}, // non-ASCII fold: no prefilter
		{"err�or", "", false},    // U+FFFD guard
		{`(?i)disk|(?-i)PANIC`, "disk|panic", true},
	}
	for _, c := range cases {
		p := NewPrefilter(c.pattern)
		got, fold := "", false
		if p != nil {
			var ls []string
			for _, l := range p.lits {
				ls = append(ls, string(l))
			}
			got, fold = strings.Join(ls, "|"), p.fold
		}
		if c.pattern == `(foo)?bar` {
			// Either no prefilter or "bar" is acceptable; both are correct.
			if got != "" && got != "bar" {
				t.Errorf("%s: lits %q", c.pattern, got)
			}
			continue
		}
		if got != c.want || (got != "" && fold != c.fold) {
			t.Errorf("%s: lits %q fold %v, want %q fold %v", c.pattern, got, fold, c.want, c.fold)
		}
	}
}

// The prefilter's ASCII folding is only sound if 'k' and 's' are the only ASCII
// letters with non-ASCII case-fold equivalents (Kelvin sign and long s).
func TestOnlyKAndSHaveNonASCIIFolds(t *testing.T) {
	var got []string
	for c := 'a'; c <= 'z'; c++ {
		for f := unicode.SimpleFold(c); f != c; f = unicode.SimpleFold(f) {
			if f > unicode.MaxASCII {
				got = append(got, fmt.Sprintf("%c→%U", c, f))
			}
		}
	}
	if strings.Join(got, ",") != "k→U+212A,s→U+017F" {
		t.Fatalf("non-ASCII folds: %v; update Prefilter.unicodeFold", got)
	}
}

var propPatterns = []string{
	`(?i)panic|fatal|segfault`, `ERROR code=(?P<code>\w+)`, `(?i)out of memory`, `(ERROR|WARN) disk`,
	`timeout (after|exceeded)`, `(?:connection )+refused`, `x*abc`, `(?i)disk|(?-i)PANIC`,
	`(?i)KILLED|sigsegv`, `(?i)ask`, `status=5\d\d`, `took=(?P<ms>[5-9]\d{3,})ms`, `(?i)(abc|xyz)+`,
	`(?i)stack( trace)?:`, `^\[(?i:crit)\]`, `(?s)begin.*end`, `abc{2,}`, `(?i)a(bc|bd)e`,
}

var propAlphabet = []string{"a", "b", "c", "d", "e", "s", "k", "K", "S", "ſ", "K", "x", "y", "z", " ", ":", "[", "]",
	"5", "0", "ms", "=", "\xff", "\xc3", "É", "é", "PANIC", "panic", "Fatal", "SegFault", "ſegfault", "DISK",
	"disk", "ERROR", "WARN", "code=", "timeout ", "after", "connection ", "refused", "OUT OF MEMORY",
	"out of memory", "ASK", "aſk", "KILLED", "killed", "sigsegv", "SIGSEGV", "abc", "xyz", "stack trace:",
	"[crit]", "[CRIT]", "begin", "end", "status=500", "took=6000ms", "\n"}

func randomLine(r *rand.Rand) string {
	var b strings.Builder
	for n := r.IntN(12); n >= 0; n-- {
		b.WriteString(propAlphabet[r.IntN(len(propAlphabet))])
	}
	return b.String()
}

// TestPrefilterNeverRejectsAMatch is the safety property: whenever the regexp
// matches a line, the prefilter must let it through.
func TestPrefilterNeverRejectsAMatch(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, pat := range propPatterns {
		re := regexp.MustCompile(pat)
		p := NewPrefilter(pat)
		if p == nil {
			continue
		}
		matched := 0
		for i := 0; i < 200000; i++ {
			line := []byte(randomLine(r))
			if re.Match(line) {
				matched++
				if !p.MayMatch(line) {
					t.Fatalf("pattern %q: prefilter rejected matching line %q", pat, line)
				}
			}
		}
		if matched == 0 {
			t.Errorf("pattern %q: no random line matched; property not exercised", pat)
		}
	}
}

func FuzzPrefilter(f *testing.F) {
	for i, p := range propPatterns {
		f.Add(p, propAlphabet[i%len(propAlphabet)]+" "+propAlphabet[(i*7)%len(propAlphabet)])
	}
	f.Fuzz(func(t *testing.T, pattern, line string) {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return
		}
		p := NewPrefilter(pattern)
		if p != nil && re.MatchString(line) && !p.MayMatch([]byte(line)) {
			t.Fatalf("pattern %q: prefilter rejected matching line %q", pattern, line)
		}
	})
}

func BenchmarkPrefilterCaseInsensitive(b *testing.B) {
	line := []byte(`2026-10-12T15:46:30.341Z INFO service=search req=35b1d5e7bea0f9ba method=GET path=/api/v1/search status=200 took=258ms bytes=37188 ua="Mozilla/5.0 (X11; Linux x86_64)"`)
	re := regexp.MustCompile(`(?i)panic|fatal|segfault`)
	p := NewPrefilter(`(?i)panic|fatal|segfault`)
	b.Run("regexp-only", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			re.Match(line)
		}
	})
	b.Run("with-prefilter", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if p.MayMatch(line) {
				re.Match(line)
			}
		}
	})
}
