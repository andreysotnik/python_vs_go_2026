import pytest

from profanity_filter import censor, check, classify


def roots(text: str) -> list[str]:
    return [m["root"] for m in check(text)["matches"]]


# --------------------------------------------------------------------------- #
# Roots: positive and negative cases for each one
# --------------------------------------------------------------------------- #

POSITIVE_BY_ROOT = {
    "хуй": [
        "хуй",
        "хуйня",
        "нахуя",
        "похуй",
        "охуеть",
        "нихуя",
        "дохуя",
        "схуяли",
        "хуёвый",
        "хуево",
        "хуило",
        "хую",
        "охуенно",
        "захуярить",
        "нехуй",
    ],
    "пизд": [
        "пизда",
        "пиздец",
        "спиздил",
        "распиздяй",
        "пиздёж",
        "напиздел",
        "пиздюлей",
        "пиздато",
        "пиздуй",
    ],
    "еб": [
        "ебать",
        "заебал",
        "ёбаный",
        "уебок",
        "выебываться",
        "еблан",
        "ебучий",
        "наебнулся",
        "долбоёб",
        "разъебай",
        "съебись",
        "сьебался",
        "въебал",
        "доебался",
        "проебал",
        "уёбище",
        "отъебись",
        "подъебка",
        "неебический",
        "переебал",
        "ебло",
        "заебись",
        "ёпт",
    ],
    "бля": [
        "бля",
        "блядь",
        "блять",
        "блядский",
        "выблядок",
        "поблядушка",
        "бляди",
        "блядина",
        "блят",
    ],
    "мудак": ["мудак", "мудила", "мудило", "мудаки", "мудачьё", "мудозвон"],
    "пидор": ["пидор", "пидар", "пидорас", "пидарас", "пидр", "опидорасили", "пидоры"],
    "залуп": ["залупа", "залупаться", "не залупайся"],
    "fuck": ["fuck", "fucking", "fucked", "FUCK", "fucker", "clusterfuck"],
    "shit": ["shit", "shitty", "shits", "horseshit", "shithead"],
    "bitch": ["bitch", "bitches", "bitchy", "sonofabitch"],
    "cunt": ["cunt", "cunts"],
    "motherfucker": ["motherfucker", "motherfuckers"],
    "bullshit": ["bullshit", "bullshitting"],
}


@pytest.mark.parametrize(
    ("root", "word"),
    [(r, w) for r, ws in POSITIVE_BY_ROOT.items() for w in ws],
)
def test_root_positive(root: str, word: str) -> None:
    result = check(word)
    assert result["profane"], word
    assert root in roots(word)


NEGATIVE_BY_ROOT = {
    "хуй": [
        "застрахуй",
        "страхую",
        "перестрахуйся",
        "Чихуахуа",
        "Хуан",
        "хутор",
        "художник",
        "хулиган",
        "подстрахуй",
        "застрахуйте",
    ],
    "пизд": ["писать", "пицца", "пиджак", "низ"],
    "еб": [
        "учебник",
        "потребитель",
        "употреблять",
        "хлеб",
        "небо",
        "себя",
        "тебе",
        "вебинар",
        "ребенок",
        "требовать",
        "дебил",
        "дебет",
        "колебаться",
        "погребение",
        "жеребец",
        "амеба",
        "зебра",
        "отребье",
        "пребывание",
        "небесно-голубой",
        "двоеборье",
        "Ебург",
        "ебонит",
        "убедить",
        "обед",
    ],
    "бля": [
        "рубля",
        "сабля",
        "оскорблять",
        "употреблять",
        "корабля",
        "бляха",
        "бляшка",
        "блямба",
        "обладатель",
        "оглобля",
        "дирижабля",
    ],
    "мудак": ["мудрый", "мудрость", "премудрость", "мудрец"],
    "пидор": ["скипидар", "скипидарный", "пиджак", "Пидгорный"],
    "залуп": ["залив", "залог", "лупа", "залпом"],
    "манда": ["команда", "мандарин", "мандат", "мандолина", "мандраж", "мандала"],
    "english": [
        "Scunthorpe",
        "shiitake",
        "shitake",
        "assassin",
        "cocktail",
        "Mississippi",
        "Sussex",
        "ebay",
        "ebony",
        "ebola",
        "debate",
        "rebate",
        "Huy",
        "Xue",
        "hue",
        "blade",
        "bleak",
        "Pushkin",
        "peniston",
        "nahuel",
    ],
}


@pytest.mark.parametrize("word", [w for ws in NEGATIVE_BY_ROOT.values() for w in ws])
def test_root_negative(word: str) -> None:
    assert not check(word)["profane"], check(word)


# --------------------------------------------------------------------------- #
# Masking
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    "text",
    [
        # Latin look-alikes
        "xуй",
        "xyй",
        "хуu",
        "пиздeц",
        "eбать",
        "блядb",
        "nизда",
        "мyдак",
        "залyпа",
        "hуй",
        "3ae6aл",
        "XУЙНЯ",
        # digits and symbols
        "пи3да",
        "п1зда",
        "е6ать",
        "уе6ок",
        "3аебал",
        "бл@дь",
        "пизд@",
        "бл9ть",
        "з@луп@",
        "nизд@тый",
        # separators
        "х у й",
        "х.у.й",
        "п-и-з-д-а",
        "б л я д ь",
        "х..у..й",
        "х_у_й",
        "е б а т ь",
        "п и з д е ц",
        "х​у​й",
        "х*у*й",
        # star as a replaced letter
        "п*зда",
        "бл*дь",
        "х*й",
        "f*ck",
        "f**k",
        "sh*t",
        # repeated letters
        "бляяяять",
        "ббббля",
        "хууууй",
        "пиииздец",
        "fuuuuck",
        "shiiiit",
        "ммммудак",
        # case and ё/е
        "ХуЙнЯ",
        "ЗАЕБАЛО",
        "ЁБАНЫЙ",
        "ебаный",
        "Shit",
        "FuCk",
        # leet English and Cyrillic look-alikes in English words
        "b1tch",
        "sh!t",
        "$hit",
        "fuсk",
        "shіt",
        # transliteration
        "pizdec",
        "blyat",
        "nahuy",
        "pohuy",
        "zaebal",
        "pi3da",
        "xyu",
    ],
)
def test_masking_positive(text: str) -> None:
    assert check(text)["profane"], text


@pytest.mark.parametrize(
    "text",
    [
        # look-alike/separator handling must not create false positives
        "eBay-магазин",
        "x y z",
        "a b c",
        "и я у тебя",
        "в и к а",
        "5*3=15",
        "a*b",
        "C-3PO",
        "mp3",
        "Небо-голубое",
        "по-моему",
        "кое-где",
        "x.com",
        "co-op",
        "***",
        "х***",
        "Hello, world!",
        "Ухуру",
    ],
)
def test_masking_negative(text: str) -> None:
    assert not check(text)["profane"], check(text)


# --------------------------------------------------------------------------- #
# False-positive traps from the spec
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    "text",
    [
        "Наша команда лучшая",
        "Мандарины в подарок",
        "Не надо оскорблять людей",
        "употреблять в пищу",
        "Застрахуйте имущество",
        "Скипидарная мазь",
        "Учебник по физике",
        "Потребитель всегда прав",
        "Курс рубля",
        "Острая сабля",
        "Scunthorpe United",
        "shiitake mushrooms",
        "Assassin's Creed",
        "Хлеб всему голова",
        "Художественная литература",
        "Небесно-голубой цвет",
    ],
)
def test_traps(text: str) -> None:
    assert not check(text)["profane"], check(text)


# --------------------------------------------------------------------------- #
# Result structure
# --------------------------------------------------------------------------- #


def test_check_structure_and_offsets() -> None:
    text = "Ну и мудак, бля"
    res = check(text)
    assert res["profane"] is True
    words = [(m["word"], m["root"]) for m in res["matches"]]
    assert words == [("мудак", "мудак"), ("бля", "бля")]
    for m in res["matches"]:
        assert text[m["start"] : m["end"]] == m["word"]


def test_check_clean() -> None:
    assert check("Хороший день") == {"profane": False, "matches": []}
    assert check("") == {"profane": False, "matches": []}


def test_spaced_letters_span() -> None:
    text = "x у й с ним"
    (m,) = check(text)["matches"]
    assert m["root"] == "хуй"
    assert text[m["start"] : m["end"]] == m["word"]
    assert m["word"].startswith("x")


def test_spaced_letters_inside_run() -> None:
    (m,) = check("и я х у й")["matches"]
    assert m["word"] == "х у й"


def test_hyphen_part_reported_precisely() -> None:
    (m,) = check("ёперный-блядь")["matches"]
    assert m["word"] == "блядь"


def test_classify() -> None:
    assert classify("пиздец") == "пизд"
    assert classify("команда") is None
    assert classify("") is None


# --------------------------------------------------------------------------- #
# censor
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    ("text", "expected"),
    [
        ("Ну ты и мудак", "Ну ты и м***"),
        ("Бля, опоздал", "Б***, опоздал"),
        ("иди нахуй!", "иди н***!"),
        ("x у й с ним", "x*** ним"),
        ("п-и-з-д-а рулю", "п*** рулю"),
        ("Shit happens", "S*** happens"),
        ("ЗАЕБАЛО всё", "З*** всё"),
        ("Наша команда лучшая", "Наша команда лучшая"),
        ("", ""),
        ("бля бля бля", "б*** б*** б***"),
    ],
)
def test_censor(text: str, expected: str) -> None:
    assert censor(text) == expected


def test_censor_idempotent_and_clean_after() -> None:
    once = censor("Какой мудак тут припарковался, блядь")
    assert censor(once) == once
    assert not check(once)["profane"]
