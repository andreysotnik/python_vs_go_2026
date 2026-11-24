from collections import Counter

from library.domain.ids import id_timestamp, make_id
from library.domain.isbn import is_valid_isbn10, is_valid_isbn13
from library.seed import generator as g


def params() -> g.SeedParams:
    return g.SeedParams(seed=7, n_books=2000, n_authors=300, user_ids=tuple(g.user_id(i) for i in range(5)))


def test_isbns_valid_and_unique() -> None:
    seen = set()
    for i in range(20000):
        i10, i13 = g.isbn_for(i)
        assert is_valid_isbn13(i13)
        assert i10 is None or is_valid_isbn10(i10)
        seen.add(i13)
    assert len(seen) == 20000


def test_ids_monotonic_and_unique() -> None:
    ids = [g.book_id(i, 100_000) for i in range(0, 100_000, 7)]
    assert ids == sorted(ids) and len(set(ids)) == len(ids)
    assert id_timestamp(make_id(1_700_000_000_000, 1, 5)).year == 2023


def test_generation_is_deterministic() -> None:
    p = params()
    a = g.generate_books(p, 0, 0, 500)
    b = g.generate_books(p, 0, 0, 500)
    assert a == b
    c = g.generate_books(g.SeedParams(8, p.n_books, p.n_authors, p.user_ids), 0, 0, 500)
    assert a["books"] != c["books"]


def test_books_shape_and_long_tail() -> None:
    p = params()
    out = g.generate_books(p, 0, 0, 2000)
    assert len(out["books"]) == 2000
    assert all(len(row) == len(g.BOOK_COLUMNS) for row in out["books"])
    assert len(out["book_authors"]) == len(out["author_books"]) >= 2000
    per_author = Counter(row[1] for row in out["book_authors"] if row[2] == "author")
    top = per_author.most_common(3)
    assert top[0][1] > 20  # a few prolific authors
    singles = sum(1 for c in per_author.values() if c == 1)
    assert singles > 10  # ... and a long tail of authors with one book
    scripts = {row[7] for row in out["books"]}
    assert {"en", "ru", "zh", "ar"} <= scripts


def test_authors_multiscript() -> None:
    out = g.generate_authors(params(), 0, 0, 300)
    rows = out["authors"]
    assert len(rows) == 300 and len(out["audit_log"]) == 300
    originals = [r[2] for r in rows if r[2]]
    assert any(any("Ѐ" <= ch <= "ӿ" for ch in o) for o in originals)  # Cyrillic
    assert any(any("一" <= ch <= "鿿" for ch in o) for o in originals)  # CJK
    assert any(any("؀" <= ch <= "ۿ" for ch in o) for o in originals)  # Arabic
    for r in rows:
        if r[3] and r[4]:
            assert r[4] >= r[3]


def test_users() -> None:
    users = g.generate_users(1, 10)
    assert len({u[0] for u in users}) == 10
