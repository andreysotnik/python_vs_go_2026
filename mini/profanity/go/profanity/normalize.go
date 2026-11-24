package profanity

import "unicode"

// homoCyr maps Latin look-alikes, digits and symbols to Cyrillic letters
// (visual similarity: "xуй", "пи3дец", "6лядь", "бл@ть", "пuздец").
var homoCyr = map[rune]rune{
	'a': 'а', 'b': 'б', 'c': 'с', 'd': 'д', 'e': 'е', 'f': 'ф', 'g': 'г',
	'h': 'н', 'i': 'и', 'j': 'й', 'k': 'к', 'l': 'л', 'm': 'м', 'n': 'п',
	'o': 'о', 'p': 'р', 'q': 'к', 'r': 'г', 's': 'с', 't': 'т', 'u': 'и',
	'v': 'в', 'w': 'ш', 'x': 'х', 'y': 'у', 'z': 'з',
	'0': 'о', '1': 'и', '3': 'з', '4': 'ч', '6': 'б', '8': 'в',
	'@': 'а', '$': 'с', '\'': 'ъ', '’': 'ъ',
}

// translitCyr maps Latin letters to Cyrillic by transliteration
// ("pизда", "hуй", "zaеbал"). Digraphs are handled in toCyrTranslit.
var translitCyr = map[rune]rune{
	'a': 'а', 'b': 'б', 'c': 'ц', 'd': 'д', 'e': 'е', 'f': 'ф', 'g': 'г',
	'h': 'х', 'i': 'и', 'j': 'й', 'k': 'к', 'l': 'л', 'm': 'м', 'n': 'н',
	'o': 'о', 'p': 'п', 'q': 'к', 'r': 'р', 's': 'с', 't': 'т', 'u': 'у',
	'v': 'в', 'w': 'в', 'x': 'х', 'z': 'з',
}

var translitDigraphs = map[string]rune{
	"sh": 'ш', "ch": 'ч', "zh": 'ж', "kh": 'х', "ts": 'ц',
	"ya": 'я', "yo": 'е', "yu": 'ю', "ye": 'е',
	"ja": 'я', "jo": 'е', "ju": 'ю',
}

// homoLat maps Cyrillic look-alikes, digits and symbols to Latin letters
// ("fуck", "sh1t", "b1tch", "$hit").
var homoLat = map[rune]rune{
	'а': 'a', 'в': 'b', 'с': 'c', 'е': 'e', 'н': 'h', 'к': 'k', 'м': 'm',
	'о': 'o', 'р': 'p', 'т': 't', 'у': 'u', 'х': 'x', 'и': 'u', 'і': 'i',
	'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b',
	'@': 'a', '$': 's',
}

// Candidate letters for a '*' used as a masked letter ("п*здец", "f*ck").
var (
	starCyr = []rune("уияаоеыбзд")
	starLat = []rune("uioaeck")
)

func isLatin(r rune) bool { return r < 128 && unicode.IsLetter(r) }

func isCyr(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'y', 'а', 'е', 'ё', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// lowerCyr lower-cases a rune and folds ё→е, і/ї→и.
func lowerCyr(r rune) rune {
	r = unicode.ToLower(r)
	switch r {
	case 'ё':
		return 'е'
	case 'і', 'ї':
		return 'и'
	}
	return r
}

// fixBraces replaces the "}{" pseudo-letter with a Latin 'x'.
func fixBraces(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		if rs[i] == '}' && i+1 < len(rs) && rs[i+1] == '{' {
			out = append(out, 'x')
			i++
			continue
		}
		out = append(out, rs[i])
	}
	return out
}

// toCyrHomo maps every non-Cyrillic rune through homoCyr.
// Upper-case 'B' and 'H' look like "В" and "Н", lower-case 'b' looks like "б".
func toCyrHomo(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		switch {
		case r == 'B':
			out = append(out, 'в')
		case isCyr(r):
			out = append(out, lowerCyr(r))
		default:
			l := unicode.ToLower(r)
			if m, ok := homoCyr[l]; ok {
				out = append(out, m)
			} else {
				out = append(out, l)
			}
		}
	}
	return out
}

// toCyrTranslit transliterates Latin letters, other runes go through homoCyr.
func toCyrTranslit(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		l := unicode.ToLower(rs[i])
		if isLatin(l) {
			if i+1 < len(rs) {
				if m, ok := translitDigraphs[string([]rune{l, unicode.ToLower(rs[i+1])})]; ok {
					out = append(out, m)
					i++
					continue
				}
			}
			if l == 'y' {
				if len(out) > 0 && isVowel(out[len(out)-1]) {
					out = append(out, 'й')
				} else {
					out = append(out, 'ы')
				}
				continue
			}
			out = append(out, translitCyr[l])
			continue
		}
		if isCyr(l) {
			out = append(out, lowerCyr(l))
			continue
		}
		if m, ok := homoCyr[l]; ok {
			out = append(out, m)
		} else {
			out = append(out, l)
		}
	}
	return out
}

// toLat maps a token to Latin; three selects the meaning of the digit 3.
func toLat(rs []rune, three rune) []rune {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		l := lowerCyr(r)
		switch {
		case isLatin(l):
			out = append(out, l)
		case l == '3':
			out = append(out, three)
		default:
			if m, ok := homoLat[l]; ok {
				out = append(out, m)
			} else {
				out = append(out, l)
			}
		}
	}
	return out
}

// collapse removes repeated letters ("бляяяять" → "блять", "ббббля" → "бля")
// and drops apostrophes/hard signs inside Latin text.
func collapse(rs []rune) string {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if len(out) > 0 && out[len(out)-1] == r && r != '*' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// expandStars returns the variants of a token with '*' treated either as a
// separator (removed) or as one masked letter from cands.
func expandStars(rs []rune, cands []rune) [][]rune {
	n := 0
	for _, r := range rs {
		if r == '*' {
			n++
		}
	}
	if n == 0 {
		return [][]rune{rs}
	}
	removed := make([]rune, 0, len(rs))
	for _, r := range rs {
		if r != '*' {
			removed = append(removed, r)
		}
	}
	res := [][]rune{removed}
	if n > 2 { // "х***" is already censored text, not a masked word
		return res
	}
	for _, c := range cands {
		v := make([]rune, len(rs))
		for i, r := range rs {
			if r == '*' {
				r = c
			}
			v[i] = r
		}
		res = append(res, v)
	}
	return res
}
