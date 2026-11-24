// Package profanity detects Russian and basic English obscene language with
// regular expressions (no ML, no external services).
//
// Pipeline: tokenize → build normalized variants of every token (Cyrillic
// homoglyphs, transliteration, Latin, masked '*') → match root rules on whole
// tokens. Letters spaced out with separators ("х у й", "п-и-з-д-а") are
// re-joined into windows of adjacent tokens.
package profanity

import (
	"sort"
	"unicode"
)

// Match is one obscene word found in a text. Start and End are rune
// (Unicode code point) offsets, End is exclusive.
type Match struct {
	Word  string `json:"word"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Root  string `json:"root"`
}

// Result is returned by Check.
type Result struct {
	Profane bool    `json:"profane"`
	Matches []Match `json:"matches"`
}

type token struct {
	start, end int // rune offsets in the text, end exclusive
	runes      []rune
	hasLetter  bool
}

func (t token) single() bool { return len(t.runes) == 1 }

const (
	maxWindow       = 24 // tokens in a separator-joined window
	maxGap          = 3  // separator runes allowed between joined tokens
	maxEndpointLen  = 3  // a multi-letter token may end a window only if this short ("ху й")
	minWindowLetter = 3
)

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '@' || r == '$' || r == '*'
}

// isSep reports whether r may separate spaced-out letters ("х.у.й", "п-и-з-д-а").
func isSep(r rune) bool {
	switch r {
	case ' ', '\t', ' ', '.', '-', '_', '+', '~', '/', '\\', '|', '`', '\'', '’', '"':
		return true
	}
	return false
}

func tokenize(rs []rune) []token {
	var toks []token
	i := 0
	for i < len(rs) {
		if !isWordRune(rs[i]) && (rs[i] != '}' || i+1 >= len(rs) || rs[i+1] != '{') {
			i++
			continue
		}
		start := i
		for i < len(rs) {
			r := rs[i]
			if r == '}' && i+1 < len(rs) && rs[i+1] == '{' {
				i += 2
				continue
			}
			if isWordRune(r) {
				i++
				continue
			}
			// Apostrophe inside a word ("let's", "под'ебка") keeps it whole.
			if (r == '\'' || r == '’') && i > start && i+1 < len(rs) &&
				unicode.IsLetter(rs[i-1]) && unicode.IsLetter(rs[i+1]) {
				i++
				continue
			}
			break
		}
		t := token{start: start, end: i, runes: rs[start:i]}
		for _, r := range t.runes {
			if unicode.IsLetter(r) {
				t.hasLetter = true
				break
			}
		}
		toks = append(toks, t)
	}
	return toks
}

// classify reports which scripts a token uses.
func classify(rs []rune) (hasCyr, hasLat bool) {
	for _, r := range rs {
		if isCyr(r) {
			hasCyr = true
		} else if isLatin(r) {
			hasLat = true
		}
	}
	return
}

// matchWord returns the matched root for a (possibly joined) token, or "".
// allowLatin enables the English/transliteration rules.
func matchWord(raw []rune, allowLatin bool) string {
	rs := fixBraces(raw)
	hasCyr, hasLat := classify(rs)
	if !hasCyr && !hasLat {
		return ""
	}
	if hasCyr {
		variants := [][]rune{toCyrHomo(rs)}
		if hasLat {
			variants = append(variants, toCyrTranslit(rs))
		}
		for _, v := range variants {
			for _, e := range expandStars(v, starCyr) {
				if root := applyRules(cyrRules, collapse(e)); root != "" {
					return root
				}
			}
		}
		if !hasLat || !allowLatin {
			return ""
		}
		// Mixed scripts: "fуck", "shiт".
		for _, e := range expandStars(toLat(rs, 'e'), starLat) {
			if root := applyRules(engRules, collapse(e)); root != "" {
				return root
			}
		}
		return ""
	}
	if !allowLatin {
		return ""
	}
	threes := []rune{'e'}
	for _, r := range rs {
		if r == '3' {
			threes = append(threes, 'z') // "pi3da"
			break
		}
	}
	for _, three := range threes {
		for _, e := range expandStars(toLat(rs, three), starLat) {
			s := collapse(e)
			if root := applyRules(engRules, s); root != "" {
				return root
			}
			if root := applyRules(translitRules, s); root != "" {
				return root
			}
		}
	}
	return ""
}

// joinable reports whether only a short run of separators lies between a and b.
func joinable(rs []rune, a, b token) bool {
	gap := b.start - a.end
	if gap < 1 || gap > maxGap {
		return false
	}
	for _, r := range rs[a.end:b.start] {
		if !isSep(r) {
			return false
		}
	}
	return true
}

// Check finds obscene words in text.
func Check(text string) Result {
	rs := []rune(text)
	toks := tokenize(rs)
	matched := make([]bool, len(toks))
	var matches []Match
	for i, t := range toks {
		if !t.hasLetter {
			continue
		}
		if root := matchWord(t.runes, true); root != "" {
			matched[i] = true
			matches = append(matches, Match{Word: string(t.runes), Start: t.start, End: t.end, Root: root})
		}
	}
	matches = append(matches, findWindows(rs, toks, matched)...)
	sort.Slice(matches, func(a, b int) bool { return matches[a].Start < matches[b].Start })
	if matches == nil {
		matches = []Match{}
	}
	return Result{Profane: len(matches) > 0, Matches: matches}
}

// findWindows re-joins letters spaced out with separators: "х у й",
// "п-и-з-д-а", "ху й". Inside a window every token is a single letter; the
// first and the last token may be short multi-letter fragments. Windows with a
// fragment longer than two letters are checked with Cyrillic rules only, to
// keep "U.S. hit" from becoming "ushit".
func findWindows(rs []rune, toks []token, matched []bool) []Match {
	var out []Match
	for i := 0; i < len(toks); i++ {
		first := toks[i]
		if matched[i] || !first.hasLetter || (!first.single() && len(first.runes) > maxEndpointLen) {
			continue
		}
		joined := append([]rune(nil), first.runes...)
		anySingle := first.single()
		latinOK := len(first.runes) <= 2
		for j := i + 1; j < len(toks) && j-i < maxWindow; j++ {
			t := toks[j]
			if matched[j] || !t.hasLetter || !joinable(rs, toks[j-1], t) {
				break
			}
			if !t.single() && len(t.runes) > maxEndpointLen {
				break
			}
			joined = append(joined, t.runes...)
			anySingle = anySingle || t.single()
			latinOK = latinOK && len(t.runes) <= 2
			if anySingle && len(joined) >= minWindowLetter {
				if root := matchWord(joined, latinOK); root != "" {
					// Shrink from the left: "и п-и-з-д-а" must not swallow "и".
					s := i
					for s < j {
						sub := joinRunes(toks[s+1 : j+1])
						if len(sub) < minWindowLetter || matchWord(sub, latinOK) == "" {
							break
						}
						s++
					}
					// Grow to the right over the rest of a spelled-out word
					// ("п-и-з-д-а"), but not over a following preposition
					// ("x у й с ним").
					for j+1 < len(toks) && !matched[j+1] && toks[j+1].single() && toks[j+1].hasLetter &&
						joinable(rs, toks[j], toks[j+1]) &&
						(!isFunctionWord(toks[j+1].runes[0]) || !onlySpaces(rs[toks[j].end:toks[j+1].start])) {
						j++
					}
					// A short trailing fragment glued by a non-space separator
					// also belongs to the word: "бл.я.дь".
					if j+1 < len(toks) && !matched[j+1] && toks[j+1].hasLetter &&
						len(toks[j+1].runes) <= maxEndpointLen && joinable(rs, toks[j], toks[j+1]) &&
						!onlySpaces(rs[toks[j].end:toks[j+1].start]) {
						j++
					}
					t = toks[j]
					out = append(out, Match{Word: string(rs[toks[s].start:t.end]), Start: toks[s].start, End: t.end, Root: root})
					for k := s; k <= j; k++ {
						matched[k] = true
					}
					i = j
					break
				}
			}
			if !t.single() { // a multi-letter fragment can only end a window
				break
			}
		}
	}
	return out
}

// isFunctionWord reports whether a single letter is a common standalone
// Russian/English word (preposition, conjunction, pronoun).
func isFunctionWord(r rune) bool {
	switch unicode.ToLower(r) {
	case 'а', 'в', 'и', 'к', 'о', 'с', 'у', 'я', 'a', 'i':
		return true
	}
	return false
}

func onlySpaces(rs []rune) bool {
	for _, r := range rs {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func joinRunes(toks []token) []rune {
	var out []rune
	for _, t := range toks {
		out = append(out, t.runes...)
	}
	return out
}

// Censor replaces every obscene word with its first letter followed by "***".
func Censor(text string) string {
	res := Check(text)
	if !res.Profane {
		return text
	}
	rs := []rune(text)
	out := make([]rune, 0, len(rs))
	pos := 0
	for _, m := range res.Matches {
		out = append(out, rs[pos:m.Start]...)
		out = append(out, rs[m.Start])
		out = append(out, '*', '*', '*')
		pos = m.End
	}
	out = append(out, rs[pos:]...)
	return string(out)
}
