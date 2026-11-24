import pytest

from library.domain import isbn


@pytest.mark.parametrize(
    ("body", "check"),
    [("978014044793", "4"), ("978030640615", "7"), ("979100000000", "8")],
)
def test_isbn13_check_digit(body: str, check: str) -> None:
    assert isbn.isbn13_check_digit(body) == check


@pytest.mark.parametrize(("body", "check"), [("014044793", "8"), ("080442957", "X"), ("030640615", "2")])
def test_isbn10_check_digit(body: str, check: str) -> None:
    assert isbn.isbn10_check_digit(body) == check


def test_check_digit_rejects_bad_input() -> None:
    with pytest.raises(ValueError):
        isbn.isbn13_check_digit("12")
    with pytest.raises(ValueError):
        isbn.isbn10_check_digit("abcdefghi")


def test_validation() -> None:
    assert isbn.is_valid_isbn13("9780140447934")
    assert not isbn.is_valid_isbn13("9780140447935")  # wrong check digit
    assert not isbn.is_valid_isbn13("1234567890128")  # wrong prefix
    assert isbn.is_valid_isbn10("080442957X")
    assert not isbn.is_valid_isbn10("0804429571")
    assert not isbn.is_valid_isbn10("08044295")


def test_conversions() -> None:
    assert isbn.isbn10_to_13("0140447938") == "9780140447934"
    assert isbn.isbn13_to_10("9780140447934") == "0140447938"
    assert isbn.isbn13_to_10("9791000000008") is None
    assert isbn.to_isbn13("0-14-044793-8") == "9780140447934"
    assert isbn.to_isbn13("978 0 14 044793 4") == "9780140447934"
    with pytest.raises(ValueError):
        isbn.to_isbn13("123")


def test_make_isbn13_is_valid() -> None:
    for n in (0, 1, 123_456_789, 999_999_999):
        assert isbn.is_valid_isbn13(isbn.make_isbn13("978", n))
        assert isbn.is_valid_isbn13(isbn.make_isbn13("979", n))
