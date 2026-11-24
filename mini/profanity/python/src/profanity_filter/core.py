"""Regex-based profanity detection for Russian mat and basic English swear words.

Pipeline:
1. Tokenize the line into "compounds": runs of word characters that may be glued
   by intra-word separators (``х.у.й``, ``п-и-з-д-а``, ``х*й`` -> separators are dropped).
2. Runs of >= 3 single-character tokens separated by spaces/separators
   (``х у й``, ``б . л . я``) are merged into one virtual token.
3. Each compact token is lowercased (``ё`` -> ``е``) and fully matched against
   one combined, precompiled regex per alphabet. Every letter of a root is expanded
   into a character class of its look-alikes (``х`` -> ``[хxh]``) with ``+`` so that
   repeated letters (``бляяяять``) are accepted.
4. Roots are anchored: either a root that never occurs inside normal words
   (``пизд``, ``бляд``, ``fuck``) may appear anywhere, or the root must start the
   word / follow a whitelisted prefix (``еб``, ``ху[йеяюи]``, ``пид[оа]р``, ``shit``),
   which rules out ``учебник``, ``застрахуй``, ``скипидар``, ``shiitake``...
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from functools import lru_cache

__all__ = ["Match", "censor", "check", "find_matches"]

# --------------------------------------------------------------------------- #
# Character classes
# --------------------------------------------------------------------------- #

# Cyrillic letter -> characters that may stand for it (homoglyphs, digits, symbols).
CYR_CLASSES: dict[str, str] = {
    "а": "аa@*",
    "б": "б6b",
    "в": "вb8",
    "г": "гr",
    "д": "дdg",
    "е": "еe*",
    "ж": "ж",
    "з": "з3z",
    "и": "иu1iі!|*",
    "й": "йyi",
    "к": "кk",
    "л": "лl",
    "м": "мm",
    "н": "нh",
    "о": "оo0*",
    "п": "пn",
    "р": "рp",
    "с": "сc$",
    "т": "тt",
    "у": "уyu*",
    "ф": "ф",
    "х": "хxh",
    "ц": "ц",
    "ч": "ч4",
    "ш": "шw",
    "щ": "щ",
    "ъ": "ъь'",
    "ы": "ы*",
    "ь": "ьъ'",
    "э": "э",
    "ю": "ю*",
    "я": "я9*",
}

# Latin letter -> leet / look-alike characters.
LAT_CLASSES: dict[str, str] = {
    "a": "a@4*",
    "b": "b8",
    "e": "e3*",
    "i": "i1!|*",
    "l": "l1|",
    "o": "o0*",
    "s": "s$5",
    "t": "t7",
    "u": "uv*",
    "z": "z3",
}

# Cyrillic look-alikes projected to Latin, so "fuсk" with a Cyrillic "с" is caught.
CYR_TO_LAT = str.maketrans(
    {
        "а": "a",
        "в": "b",
        "е": "e",
        "к": "k",
        "м": "m",
        "н": "h",
        "о": "o",
        "р": "p",
        "с": "c",
        "т": "t",
        "у": "y",
        "х": "x",
        "і": "i",
        "ѕ": "s",
        "ј": "j",
    }
)

# Characters that may separate letters of a masked word inside one token.
SEP_CHARS = ".\\-_*'\u2019`~\u00ad\u200b\u200c\u200d\u2060/\\\\"
SEP_RE = f"[{SEP_CHARS}]"
_SEP_SPLIT = re.compile(f"{SEP_RE}+")
_NON_STAR_SEP = re.compile(f"[{SEP_CHARS.replace('*', '')}]+")
# A "word char": letter or digit (not underscore) or symbols used as letters.
_CH = r"(?:[^\W_]|[@$])"
# Leet symbols allowed only in the middle of a word ("sh!t", "п|зда").
_INNER = r"[!|]"
_COMPOUND_RE = re.compile(
    rf"{_CH}+(?:(?:{SEP_RE}{{1,3}}|{_INNER}+){_CH}+)*",
)
_GAP_RE = re.compile(rf"(?:\s|{SEP_RE}){{1,4}}")
_HAS_CYR = re.compile(r"[\u0400-\u04ff]")
_HAS_LEET = re.compile(r"[0-9@$!|*]")


def _expand(src: str, classes: dict[str, str]) -> str:
    """Turn a readable root pattern into a look-alike / repetition tolerant regex.

    Every letter present in ``classes`` becomes ``(?:[class]+)``; letters of the
    same alphabet missing from the table become ``(?:x+)``; everything else
    (regex syntax) is copied verbatim. Sources must not contain ``[...]``.
    """
    out: list[str] = []
    for ch in src:
        if ch in classes:
            cls = "".join(re.escape(c) for c in classes[ch])
            out.append(f"(?:[{cls}]+)")
        elif ch.isalpha():
            out.append(f"(?:{re.escape(ch)}+)")
        else:
            out.append(ch)
    return "".join(out)


# --------------------------------------------------------------------------- #
# Root rules (readable sources, expanded once at import time)
# --------------------------------------------------------------------------- #

_EB_PREFIX = (
    "(?:(?:с|в|под|от|об|раз|рас|из|вз)ъ"
    "|до|за|на|вы|у|по|под|от|об|раз|рас|из|при|про|пере|недо|не|ни|о"
    "|долбо|мудо|дуро|зло)"
)
_XU_PREFIX = "(?:на|по|ни|не|до|за|от|о|об|рас|раз|с|вы|у|под|при|про|пере|недо|а)"
_PID_PREFIX = "(?:о|от|за|рас|вы|у|на)"

# (root name, pattern fully matched against the compact token)
CYR_RULES: list[tuple[str, str]] = [
    ("пизд", ".*п(?:и|е)зд.*"),
    ("бля", ".*бляд.*"),
    ("бля", "блад(?:ь|и|ей|ям|ями|ях|ина.*|ск.*|ств.*)"),
    ("бля", "бля(?:т.*)?"),
    ("хуй", f"(?:{_XU_PREFIX}){{0,3}}ху(?:й|е|я|ю|и).*"),
    ("еб", f"(?:{_EB_PREFIX}){{0,3}}еб(?!ург|онит).*"),
    ("еб", "еп(?:т|та|ть|ти)"),
    ("мудак", ".*муд(?:ак|ач|ац|ил|озвон|оеб|охат).*"),
    ("пидор", f"{_PID_PREFIX}?пид(?:о|а)?р.*"),
    ("залуп", ".*залуп.*"),
    ("манда", "манд(?:а|ы|е|у|ой|ою|ец|еж|ищ.*|ятин.*|авошк.*)"),
]

LAT_RULES: list[tuple[str, str]] = [
    ("motherfucker", ".*motherfuck.*"),
    ("fuck", ".*fuck.*"),
    ("fuck", "fck(?:ing|in|er|ers|ed|s)?"),
    ("fuck", r"f\*\*k(?:ing|in|er|ers|ed|s)?"),
    ("bullshit", ".*bullshit.*"),
    (
        "shit",
        "(?:horse|dog|ape|bat|chicken|dip|jack|holy)?shit"
        "(?:s|ty|tier|tiest|ting|ted|ter|ters|e|es|head|heads|face|faced|hole|holes"
        "|load|loads|storm|show|bag|bags|less|tard|stain|post|talk)?",
    ),
    ("bitch", ".*bitch.*"),
    ("cunt", "cunt(?:s|y|ish|ing|ed|face|faces)?"),
]

# Russian roots written in Latin transliteration (only for tokens without Cyrillic).
TRANSLIT_RULES: list[tuple[str, str]] = [
    ("пизд", ".*p(?:i|e)zd.*"),
    ("хуй", "(?:na|po|ni|ne|do|za|ot|o|vy)?(?:(?:x|kh)u(?:i|y|j)|xy(?:i|y|j|e|a|u|o)).*"),
    ("хуй", "(?:na|po|ni|ne|do|za|ot|o|vy)hu(?:i|y|j|et|ev).*"),
    ("бля", "bl(?:y|j)a(?:t|d)?.*"),
    (
        "еб",
        "(?:za|na|vy|u|po|do|ot|pro|pere|ras|raz|pod|ob)?(?:y|j)?(?:e|yo)b"
        "(?:at|al|an|ash|uch|ut|lo|lan|nu|is|et|ish|ok).*",
    ),
    ("мудак", ".*mud(?:ak|ach|il|ozvon).*"),
    ("пидор", "(?:o|za|ras|vy|u|na)?pid(?:o|a)r.*"),
    ("залуп", ".*zalup.*"),
]


def _combine(
    rules: list[tuple[str, str]], classes: dict[str, str]
) -> tuple[re.Pattern[str], list[str]]:
    parts = [f"(?P<r{i}>{_expand(src, classes)})" for i, (_, src) in enumerate(rules)]
    return re.compile("|".join(parts)), [name for name, _ in rules]


_CYR_RE, _CYR_NAMES = _combine(CYR_RULES, CYR_CLASSES)
_LAT_RE, _LAT_NAMES = _combine(LAT_RULES, LAT_CLASSES)
_TR_RE, _TR_NAMES = _combine(TRANSLIT_RULES, LAT_CLASSES)


def _root_of(m: re.Match[str], names: list[str]) -> str:
    return names[int(m.lastgroup[1:])]  # type: ignore[index]


@lru_cache(maxsize=1 << 16)
def classify(word: str) -> str | None:
    """Return the matched root for a single compact (separator-free) word, else None."""
    w = word.lower().replace("ё", "е")
    if not w:
        return None
    has_cyr = _HAS_CYR.search(w) is not None
    if has_cyr or _HAS_LEET.search(w):
        m = _CYR_RE.fullmatch(w)
        if m:
            return _root_of(m, _CYR_NAMES)
    lat = w.translate(CYR_TO_LAT) if has_cyr else w
    if lat.isascii():
        m = _LAT_RE.fullmatch(lat)
        if m:
            return _root_of(m, _LAT_NAMES)
        if not has_cyr:
            m = _TR_RE.fullmatch(lat)
            if m:
                return _root_of(m, _TR_NAMES)
    return None


# --------------------------------------------------------------------------- #
# Tokenization and public API
# --------------------------------------------------------------------------- #


@dataclass(frozen=True, slots=True)
class Match:
    word: str
    start: int
    end: int
    root: str

    def as_dict(self) -> dict[str, object]:
        return {"word": self.word, "start": self.start, "end": self.end, "root": self.root}


def _check_compound(text: str, start: int, end: int) -> Match | None:
    raw = text[start:end]
    parts = _SEP_SPLIT.split(raw)
    if len(parts) == 1:
        root = classify(raw)
        return Match(raw, start, end, root) if root else None
    # Check each part on its own first (precise span), then the glued word
    # ("п-и-з-д-а") — but only if some part is short, so "eBay-магазин" is not glued.
    pos = start
    for piece in re.finditer(rf"[^{SEP_CHARS}]+", raw):
        root = classify(piece.group())
        if root:
            s = pos + piece.start()
            return Match(piece.group(), s, s + len(piece.group()), root)
    if min(len(p) for p in parts) <= 2:
        root = classify("".join(parts))
        if root:
            return Match(raw, start, end, root)
    # "*" may also replace a single (vowel) letter: "п*зда", "f*ck", "бл*дь".
    if "*" in raw:
        root = classify(_NON_STAR_SEP.sub("", raw))
        if root:
            return Match(raw, start, end, root)
    return None


def find_matches(text: str) -> list[Match]:
    """Find all profane words in ``text``."""
    tokens = [(m.start(), m.end()) for m in _COMPOUND_RE.finditer(text)]
    matches: list[Match] = []
    i, n = 0, len(tokens)
    while i < n:
        # Spaced-out letters: >= 3 single-char tokens separated by short gaps.
        j = i
        while (
            j < n
            and tokens[j][1] - tokens[j][0] == 1
            and (j == i or _GAP_RE.fullmatch(text, tokens[j - 1][1], tokens[j][0]))
        ):
            j += 1
        if j - i >= 3:
            letters = [text[s] for s, _ in tokens[i:j]]
            end = tokens[j - 1][1]
            for k in range(j - i - 2):
                root = classify("".join(letters[k:]))
                if root:
                    s = tokens[i + k][0]
                    matches.append(Match(text[s:end], s, end, root))
                    break
            i = j
            continue
        m = _check_compound(text, *tokens[i])
        if m:
            matches.append(m)
        i += 1
    return matches


def check(text: str) -> dict[str, object]:
    """Check ``text``: ``{"profane": bool, "matches": [{word, start, end, root}]}``."""
    found = find_matches(text)
    return {"profane": bool(found), "matches": [m.as_dict() for m in found]}


def censor(text: str) -> str:
    """Replace every profane word with its first letter followed by ``***``."""
    found = find_matches(text)
    if not found:
        return text
    out: list[str] = []
    pos = 0
    for m in found:
        out.append(text[pos : m.start])
        out.append(m.word[0] + "***")
        pos = m.end
    out.append(text[pos:])
    return "".join(out)
