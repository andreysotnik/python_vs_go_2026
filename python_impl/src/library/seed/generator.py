"""Deterministic mock-data generation. Pure functions: safe to run in worker processes.

Every chunk is generated from its own Random(seed, chunk) stream, so the output is identical
regardless of the number of workers or the order in which chunks finish.
"""

from __future__ import annotations

import bisect
import json
import random
from dataclasses import dataclass
from datetime import UTC, date, datetime, timedelta
from itertools import accumulate
from typing import Any

from library.domain.ids import make_id
from library.domain.isbn import isbn10_check_digit, make_isbn13
from library.seed import data

NODE_AUTHORS, NODE_BOOKS, NODE_USERS, NODE_AUDIT = 2, 3, 4, 5
SEED_START = datetime(2021, 1, 1, tzinfo=UTC)
SEED_END = datetime(2025, 12, 31, tzinfo=UTC)
_SPAN_MS = int((SEED_END - SEED_START).total_seconds() * 1000)
_START_MS = int(SEED_START.timestamp() * 1000)

_LANG_CODES = list(data.LANGUAGES)
_LANG_CUM = list(accumulate(w for w, _, _ in data.LANGUAGES.values()))

BOOK_COLUMNS = (
    "id",
    "title",
    "original_title",
    "subtitle",
    "description",
    "isbn_10",
    "isbn_13",
    "language_code",
    "publication_year",
    "publisher",
    "page_count",
    "genres",
    "edition",
    "external_ids",
    "created_at",
    "created_by",
    "updated_at",
    "updated_by",
    "version",
)
AUTHOR_COLUMNS = (
    "id",
    "full_name",
    "original_name",
    "birth_date",
    "death_date",
    "country_code",
    "biography",
    "external_ids",
    "created_at",
    "created_by",
    "updated_at",
    "updated_by",
    "version",
)
LINK_COLUMNS = ("book_id", "author_id", "role", "ordinal")
AUTHOR_BOOK_COLUMNS = ("author_id", "book_id", "role", "ordinal")
ISBN_COLUMNS = ("isbn_13", "book_id")
AUDIT_COLUMNS = ("id", "entity_type", "entity_id", "action", "changed_by", "changed_at", "diff")


@dataclass(frozen=True, slots=True)
class SeedParams:
    seed: int
    n_books: int
    n_authors: int
    user_ids: tuple[int, ...]


def rng_for(seed: int, stream: str, chunk: int) -> random.Random:
    return random.Random(f"{seed}:{stream}:{chunk}")


def entity_ts_ms(index: int, total: int) -> int:
    """Creation times are spread over 2021..2025 monotonically with the index."""
    return _START_MS + (index * _SPAN_MS) // max(total, 1)


def author_id(index: int, n_authors: int) -> int:
    return make_id(entity_ts_ms(index, n_authors), NODE_AUTHORS, index)


def book_id(index: int, n_books: int) -> int:
    return make_id(entity_ts_ms(index, n_books), NODE_BOOKS, index)


def user_id(index: int) -> int:
    return make_id(_START_MS - 86_400_000 + index * 1000, NODE_USERS, index)


def isbn_for(index: int) -> tuple[str | None, str]:
    """Unique, valid ISBN per book index: index -> 9-digit body via a bijection mod 1e9."""
    body = (index * 7919 + 12345) % 1_000_000_000
    prefix = "979" if index % 7 == 3 else "978"
    isbn13 = make_isbn13(prefix, body)
    isbn10 = None
    if prefix == "978":
        b9 = f"{body:09d}"
        isbn10 = b9 + isbn10_check_digit(b9)
    return isbn10, isbn13


def long_tail_index(rnd: random.Random, n: int, power: float = 2.0) -> int:
    """Skewed pick: a few low indices get most hits (author popularity, publishers, users)."""
    return min(n - 1, int(n * (rnd.random() ** power)))


def pick_language(rnd: random.Random) -> str:
    return _LANG_CODES[bisect.bisect_right(_LANG_CUM, rnd.random() * _LANG_CUM[-1])]


def _join(words: list[str], lang: str) -> str:
    sep = "" if lang in ("zh", "ja") else " "
    return sep.join(words)


def make_title(rnd: random.Random, lang: str) -> str:
    vocab = data.LANGUAGES[lang][1]
    words = rnd.sample(vocab, rnd.randint(1, 4))
    title = _join(words, lang)
    if lang not in ("zh", "ja", "ar"):
        title = title[0].upper() + title[1:]
    return title


def make_publisher(index: int) -> str:
    r = random.Random(f"publisher:{index}")
    return f"{r.choice(data.PUBLISHER_WORDS)} {r.choice(data.PUBLISHER_WORDS)} {r.choice(data.PUBLISHER_SUFFIX)}"


def make_latin_name(rnd: random.Random) -> str:
    first = "".join(rnd.choice(data.LATIN_SYLLABLES) for _ in range(rnd.randint(2, 3))).capitalize()
    last = "".join(rnd.choice(data.LATIN_SYLLABLES) for _ in range(rnd.randint(1, 2)))
    last = (last + rnd.choice(data.LATIN_SURNAME_SUFFIX)).capitalize()
    return f"{first} {last}"


def make_author_name(rnd: random.Random, lang: str) -> tuple[str, str | None]:
    """Returns (full_name in Latin script, original_name in native script or None)."""
    if lang == "ru":
        first = rnd.choice(data.RU_FIRST)
        sylls = [rnd.choice(data.RU_SYLLABLES) for _ in range(rnd.randint(1, 2))]
        suffix = rnd.choice(data.RU_SUFFIX)
        last_lat = ("".join(s[0] for s in sylls) + suffix[0]).capitalize()
        last_cyr = ("".join(s[1] for s in sylls) + suffix[1]).capitalize()
        return f"{first[0]} {last_lat}", f"{first[1]} {last_cyr}"
    if lang == "zh":
        zh_sur = rnd.choice(data.ZH_SURNAMES)
        zh_given = [rnd.choice(data.ZH_GIVEN) for _ in range(rnd.randint(1, 2))]
        return f"{zh_sur[0]} {''.join(g[0] for g in zh_given)}", zh_sur[1] + "".join(g[1] for g in zh_given)
    if lang == "ja":
        ja_sur, ja_given = rnd.choice(data.JA_SURNAMES), rnd.choice(data.JA_GIVEN)
        return f"{ja_given[0]} {ja_sur[0]}", ja_sur[1] + ja_given[1]
    if lang == "ar":
        first, last = rnd.choice(data.AR_FIRST), rnd.choice(data.AR_LAST)
        return f"{first[0]} {last[0]}", f"{first[1]} {last[1]}"
    return make_latin_name(rnd), None


def generate_users(params_seed: int, count: int) -> list[tuple[Any, ...]]:
    rnd = rng_for(params_seed, "users", 0)
    rows = []
    for i in range(count):
        name = f"librarian_{i:04d}"
        created = datetime.fromtimestamp((user_id(i) >> 22) / 1000 + 1_577_836_800, tz=UTC)
        rows.append((user_id(i), name, f"{name}@library.example", created))
    rnd.shuffle(rows)
    return rows


def generate_authors(p: SeedParams, chunk: int, start: int, end: int) -> dict[str, list[tuple[Any, ...]]]:
    rnd = rng_for(p.seed, "authors", chunk)
    authors, audit = [], []
    for i in range(start, end):
        aid = author_id(i, p.n_authors)
        lang = pick_language(rnd)
        full_name, original = make_author_name(rnd, lang)
        country = rnd.choice(data.LANGUAGES[lang][2])
        birth = death = None
        if rnd.random() < 0.9:
            birth = date(rnd.randint(1780, 2002), rnd.randint(1, 12), rnd.randint(1, 28))
            if birth.year < 1945 and rnd.random() < 0.85:
                death = birth + timedelta(days=rnd.randint(30 * 365, 95 * 365))
                if death.year > 2025:
                    death = None
        bio = rnd.choice(data.BIO_TEMPLATES).format(name=full_name, country=country) if rnd.random() < 0.5 else None
        ext: dict[str, str] = {}
        if rnd.random() < 0.7:
            ext["viaf"] = str(rnd.randint(10_000, 999_999_999))
        if rnd.random() < 0.4:
            ext["isni"] = f"0000000{rnd.randint(100_000_000, 999_999_999)}"
        if rnd.random() < 0.5:
            ext["wikidata"] = f"Q{rnd.randint(1, 120_000_000)}"
        user = p.user_ids[long_tail_index(rnd, len(p.user_ids), 1.5)]
        created = datetime.fromtimestamp(entity_ts_ms(i, p.n_authors) / 1000, tz=UTC)
        authors.append(
            (aid, full_name, original, birth, death, country, bio, json.dumps(ext), created, user, created, user, 1)
        )
        audit.append(_audit_row(i, p.n_authors, "author", aid, user, created, {"full_name": full_name}))
    return {"authors": authors, "audit_log": audit}


def _audit_row(
    index: int, total: int, entity: str, entity_id: int, user: int, at: datetime, diff: dict[str, Any]
) -> tuple[Any, ...]:
    node = NODE_AUDIT if entity == "book" else NODE_AUDIT + 1
    return (make_id(entity_ts_ms(index, total), node, index), entity, entity_id, "create", user, at, json.dumps(diff))


def generate_books(p: SeedParams, chunk: int, start: int, end: int) -> dict[str, list[tuple[Any, ...]]]:
    rnd = rng_for(p.seed, "books", chunk)
    books, links, isbns, audit = [], [], [], []
    for i in range(start, end):
        bid = book_id(i, p.n_books)
        lang: str | None = pick_language(rnd)
        assert lang is not None
        title = make_title(rnd, lang)
        original_title = make_title(rnd, pick_language(rnd)) if rnd.random() < 0.15 else None
        subtitle = make_title(rnd, lang) if rnd.random() < 0.2 else None
        description = None
        if rnd.random() < 0.6:
            description = _join(rnd.choices(data.LANGUAGES[lang][1], k=rnd.randint(6, 16)), lang)
        isbn10: str | None
        isbn13: str | None
        if rnd.random() < 0.95:
            isbn10, isbn13 = isbn_for(i)
            isbns.append((isbn13, bid))
        else:
            isbn10 = isbn13 = None
        r = rnd.random()
        if r < 0.03:
            year = None
        elif r < 0.10:
            year = rnd.randint(1450, 1949)
        else:
            year = int(2025 - (rnd.random() ** 1.8) * 75)
        publisher = make_publisher(long_tail_index(rnd, 5000, 2.0)) if rnd.random() < 0.97 else None
        pages = rnd.randint(40, 1200) if rnd.random() < 0.95 else None
        genres = sorted(set(rnd.sample(data.GENRES, rnd.randint(1, 3))))
        edition = rnd.choice(data.EDITIONS) if rnd.random() < 0.3 else None
        ext: dict[str, str] = {}
        if rnd.random() < 0.4:
            ext["openlibrary"] = f"OL{rnd.randint(1, 40_000_000)}W"
        if rnd.random() < 0.2:
            ext["goodreads"] = str(rnd.randint(1, 90_000_000))
        if rnd.random() < 0.02:
            lang = None
        user = p.user_ids[long_tail_index(rnd, len(p.user_ids), 1.5)]
        created = datetime.fromtimestamp(entity_ts_ms(i, p.n_books) / 1000, tz=UTC)
        books.append(
            (
                bid,
                title,
                original_title,
                subtitle,
                description,
                isbn10,
                isbn13,
                lang,
                year,
                publisher,
                pages,
                genres,
                edition,
                json.dumps(ext),
                created,
                user,
                created,
                user,
                1,
            )
        )
        # Long-tail authorship: a few prolific authors, many with a single book.
        main_author = author_id(long_tail_index(rnd, p.n_authors, 2.0), p.n_authors)
        book_links = [(bid, main_author, "author", 1)]
        r = rnd.random()
        if r < 0.15:
            co = author_id(rnd.randrange(p.n_authors), p.n_authors)
            if co != main_author:
                book_links.append((bid, co, "co-author", 2))
        elif r < 0.20:
            tr = author_id(rnd.randrange(p.n_authors), p.n_authors)
            if tr != main_author:
                book_links.append((bid, tr, rnd.choice(("translator", "editor", "illustrator")), 2))
        links.extend(book_links)
        audit.append(_audit_row(i, p.n_books, "book", bid, user, created, {"title": title}))
    return {
        "books": books,
        "book_isbns": isbns,
        "book_authors": links,
        "author_books": [(a, b, role, o) for (b, a, role, o) in links],
        "audit_log": audit,
    }
