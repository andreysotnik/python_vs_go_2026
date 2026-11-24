package profanity

import "regexp"

// rule is a single root detector. Patterns are applied to a whole,
// already normalized token (lower case, ё→е, repeated letters collapsed),
// so "^" means "start of the word".
type rule struct {
	root string
	re   *regexp.Regexp
	// excl, if set, vetoes a match (known innocent words containing the root).
	excl *regexp.Regexp
}

// Common Russian verb/noun prefixes that may precede a root.
// "у" is intentionally absent for the хуй-root: "с"+"у"+"хую" would turn the
// adjective "сухую" into a match.
const huPrefix = `(?:на|по|за|от|ото|ни|не|до|вы|при|раз|рас|с|со|о|об|обо|под|подо|пере|недо|про|из|ис|вз|вс|а)`

const ebPrefix = `(?:в[ъь]|вз[ъь]|вы|до|за|из[ъь]|на|недо|не|ни|о|об[ъь]|от[ъь]?|ото|пере|по|под[ъь]?|при|про|раз[ъь]|рас|с[ъь]|у|долбо|мозго|мудо|пиздо|хуе)`

// cyrRules work on the Cyrillic normalization of a token.
var cyrRules = []rule{
	// хуй/хуе/хуё/хуя/хую/хуи: only at the start of the word or after prefixes,
	// otherwise "страхуй", "тихую", "сухую", "плохую" would match.
	{root: "хуй", re: regexp.MustCompile(`^` + huPrefix + `{0,2}ху[йеияю]`)},
	// пизд/пезд: never occurs inside normal words.
	{root: "пизд", re: regexp.MustCompile(`п[ие]зд`)},
	// еб/ёб: only at word start, after a prefix (съ/въ/объ need the hard/soft sign:
	// "себя", "веб", "обед") or in a few fixed compounds ("долбоёб").
	// "учебник", "небо", "хлеб", "потребитель" never start with (prefix)+еб.
	{root: "еб", re: regexp.MustCompile(`^` + ebPrefix + `{0,2}еб`), excl: regexp.MustCompile(`^ебонит`)},
	// бля/блядь: only at word start ("сабля", "рубля", "оскорблять" are safe);
	// "бляха", "бляшка", "блямба" are excluded. "бл@ть"/"бл@дь" → "блать"/"бладь".
	{root: "бля", re: regexp.MustCompile(`^(?:бля(?:$|[^хшм])|(?:вы|про|по|раз|за|недо|у)бляд|бла(?:дь|ть)$|блад(?:ин|ск|ств|ун))`)},
	// мудак/мудила/мудозвон, but not "мудрый", "премудрость".
	{root: "мудак", re: regexp.MustCompile(`муд(?:а[кч]|и[лщ]|о[звхе]|ень|н[яюеиоы]|я[кч])`)},
	// пидор/пидар/пидорас/пидрила, only at word start ("скипидар", "спидометр").
	{root: "пидор", re: regexp.MustCompile(`^(?:за|от|рас|вы|с)?пид(?:[оаеи]р|р[иаеоу])`)},
	{root: "залуп", re: regexp.MustCompile(`залуп`)},
}

// engRules work on the Latin normalization of a token.
var engRules = []rule{
	{root: "fuck", re: regexp.MustCompile(`fuck|^fuk(?:$|in|er|ed|s$)|^fck`)},
	{root: "shit", re: regexp.MustCompile(`shit`), excl: regexp.MustCompile(`shi?take|[a-z]shita`)}, // "Matsushita", "Yamashita"
	{root: "bitch", re: regexp.MustCompile(`bitch`)},
	// Anchored at the word start: "Scunthorpe".
	{root: "cunt", re: regexp.MustCompile(`^cunt`)},
}

// translitRules detect Russian roots written in Latin transliteration
// ("pizdec", "blyat", "nahuy"). They are deliberately stricter than the
// Cyrillic ones because they run on ordinary English words too
// ("hue", "ebay", "ebook", "EBITDA", "spider", "mudra").
var translitRules = []rule{
	{root: "хуй", re: regexp.MustCompile(`^(?:na|po|ni|ne|do|za|ot|o|a|vy|pri|ras|raz)?(?:h|kh|x)(?:u[yj]|ui[lnt]|y[iyj])`)},
	{root: "пизд", re: regexp.MustCompile(`p[ie]zd`)},
	{root: "бля", re: regexp.MustCompile(`^bl(?:y|j|i)a(?:$|d|t)`)},
	{root: "еб", re: regexp.MustCompile(`^(?:za|vy|vi|u|ot|po|na|do|pro|pere|raz|ras|ob|pod|dolbo|nedo)?(?:y?e|yo|jo)b(?:a[lt]|an(?:[aeiouy]|$)|u[tc]|l[ao]|n[uiy]|is(?:$|h|j)|yr)|^(?:u|dolbo|za|vy)(?:y?e|yo)bok`)},
	{root: "мудак", re: regexp.MustCompile(`mudak|mudil|mudach`)},
	{root: "пидор", re: regexp.MustCompile(`^pid(?:[oae]r|r[aio])`)},
	{root: "залуп", re: regexp.MustCompile(`zalup`)},
}

func applyRules(rules []rule, s string) string {
	for i := range rules {
		r := &rules[i]
		if r.re.MatchString(s) && (r.excl == nil || !r.excl.MatchString(s)) {
			return r.root
		}
	}
	return ""
}
