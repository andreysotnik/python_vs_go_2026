"""Модульные тесты: по тесту (или группе) на каждое правило очистки."""

from datetime import date, datetime
from decimal import Decimal
from typing import Any

import pytest

from sales_report.cleaning import (
    clean_rows,
    is_empty_row,
    parse_discount,
    parse_order_date,
    parse_quantity,
    parse_unit_price,
)

VALID: tuple[Any, ...] = (
    "ORD-1",
    datetime(2025, 3, 5),
    "Москва",
    "Книги",
    "Книга",
    "Иванов И.",
    2,
    100,
    0.1,
)


def row(**overrides: Any) -> tuple[Any, ...]:
    names = (
        "order_id",
        "order_date",
        "region",
        "category",
        "product",
        "manager",
        "quantity",
        "unit_price",
        "discount",
    )
    values = dict(zip(names, VALID, strict=True))
    values.update(overrides)
    return tuple(values[n] for n in names)


def numbered(*rows: tuple[Any, ...]) -> list[tuple[int, tuple[Any, ...]]]:
    return list(enumerate(rows, start=2))


# --- Правило 1: пустые строки ----------------------------------------------------


class TestEmptyRows:
    def test_all_none_is_empty(self) -> None:
        assert is_empty_row((None,) * 9)

    def test_whitespace_only_is_empty(self) -> None:
        assert is_empty_row(("  ", None, "\t", None, None, None, None, None, None))

    def test_partially_filled_is_not_empty(self) -> None:
        assert not is_empty_row((None, None, "Москва", None, None, None, None, None, None))

    def test_empty_rows_are_neither_valid_nor_skipped(self) -> None:
        result = clean_rows(numbered(row(), (None,) * 9, row(order_id="ORD-2"), ()))
        assert len(result.sales) == 2
        assert result.skipped == []


# --- Правило 2: дубликаты order_id ---------------------------------------------


class TestDuplicates:
    def test_second_occurrence_skipped(self) -> None:
        result = clean_rows(numbered(row(), row(quantity=5)))
        assert [s.quantity for s in result.sales] == [2]
        assert len(result.skipped) == 1
        assert result.skipped[0].row_number == 3
        assert "дубликат" in result.skipped[0].reason

    def test_first_invalid_occurrence_still_wins(self) -> None:
        result = clean_rows(numbered(row(quantity="n/a"), row(quantity=5)))
        assert result.sales == []
        assert [s.row_number for s in result.skipped] == [2, 3]
        assert "дубликат" in result.skipped[1].reason

    def test_order_id_compared_after_strip(self) -> None:
        result = clean_rows(numbered(row(order_id="ORD-1"), row(order_id=" ORD-1 ")))
        assert len(result.sales) == 1
        assert len(result.skipped) == 1

    def test_missing_order_id_skipped(self) -> None:
        result = clean_rows(numbered(row(order_id=None)))
        assert result.sales == []
        assert "order_id" in result.skipped[0].reason


# --- Правило 3: дата ------------------------------------------------------------


class TestOrderDate:
    def test_excel_datetime(self) -> None:
        assert parse_order_date(datetime(2025, 3, 5, 0, 0)) == date(2025, 3, 5)

    def test_excel_serial_number(self) -> None:
        assert parse_order_date(45658) == date(2025, 1, 1)

    @pytest.mark.parametrize(
        ("text", "expected"),
        [("05.03.2025", date(2025, 3, 5)), (" 5.3.2025 ", date(2025, 3, 5))],
    )
    def test_text_dd_mm_yyyy(self, text: str, expected: date) -> None:
        assert parse_order_date(text) == expected

    @pytest.mark.parametrize("bad", ["когда-то", "2025-03-05", "31.02.2025", "", None, True])
    def test_unparseable(self, bad: Any) -> None:
        with pytest.raises(ValueError, match="дата"):
            parse_order_date(bad)

    def test_bad_date_row_skipped(self) -> None:
        result = clean_rows(numbered(row(order_date="когда-то")))
        assert result.sales == []
        assert "дата" in result.skipped[0].reason


# --- Правило 4: количество ------------------------------------------------------


class TestQuantity:
    @pytest.mark.parametrize(("value", "expected"), [(1, 1), (10, 10), (3.0, 3)])
    def test_valid(self, value: Any, expected: int) -> None:
        assert parse_quantity(value) == expected

    @pytest.mark.parametrize("bad", [None, "", "n/a", "5", 0, -1, -10, 2.5, True])
    def test_invalid(self, bad: Any) -> None:
        with pytest.raises(ValueError, match="количество"):
            parse_quantity(bad)

    def test_negative_row_skipped(self) -> None:
        result = clean_rows(numbered(row(quantity=-3)))
        assert result.sales == []
        assert "количество" in result.skipped[0].reason


# --- Правило 5: цена ------------------------------------------------------------


class TestUnitPrice:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (6490, Decimal("6490")),
            (12.5, Decimal("12.5")),
            ("1234,50", Decimal("1234.50")),
            ("1234.5", Decimal("1234.5")),
            (" 990,00 ", Decimal("990.00")),
        ],
    )
    def test_valid(self, value: Any, expected: Decimal) -> None:
        assert parse_unit_price(value) == expected

    @pytest.mark.parametrize("bad", [None, "", "договорная", "1 234,50", "1.234,50", 0, -5, "-1"])
    def test_invalid(self, bad: Any) -> None:
        with pytest.raises(ValueError, match="цена"):
            parse_unit_price(bad)

    def test_text_price_row_kept(self) -> None:
        result = clean_rows(numbered(row(unit_price="19990,00", discount=0)))
        assert result.sales[0].revenue == Decimal("39980.00")


# --- Правило 6: скидка ----------------------------------------------------------


class TestDiscount:
    @pytest.mark.parametrize(
        ("value", "expected"),
        [
            (None, Decimal(0)),
            ("", Decimal(0)),
            (0, Decimal(0)),
            (0.15, Decimal("0.15")),
            (1, Decimal(1)),
            ("15%", Decimal("0.15")),
            ("0%", Decimal(0)),
            (" 12,5 % ", Decimal("0.125")),
            ("0.2", Decimal("0.2")),
        ],
    )
    def test_valid(self, value: Any, expected: Decimal) -> None:
        assert parse_discount(value) == expected

    @pytest.mark.parametrize("bad", ["скидка", "150%", 1.5, -0.1, "%"])
    def test_invalid(self, bad: Any) -> None:
        with pytest.raises(ValueError, match="скидка"):
            parse_discount(bad)

    def test_empty_discount_row_kept(self) -> None:
        result = clean_rows(numbered(row(discount=None)))
        assert result.sales[0].discount == 0


# --- Правило 7: регион ----------------------------------------------------------


class TestRegion:
    def test_region_stripped(self) -> None:
        result = clean_rows(numbered(row(region="  Москва ")))
        assert result.sales[0].region == "Москва"

    def test_blank_region_skipped(self) -> None:
        result = clean_rows(numbered(row(region="   ")))
        assert result.sales == []
        assert "region" in result.skipped[0].reason


# --- Правило 8: выручка ---------------------------------------------------------


class TestRevenue:
    def test_formula(self) -> None:
        result = clean_rows(numbered(row(quantity=3, unit_price="1234,50", discount="15%")))
        assert result.sales[0].revenue == Decimal("3") * Decimal("1234.50") * Decimal("0.85")

    def test_full_precision_no_float_drift(self) -> None:
        result = clean_rows(numbered(row(quantity=1, unit_price=0.1, discount=0)))
        assert result.sales[0].revenue == Decimal("0.1")


def test_multiple_reasons_reported_together() -> None:
    result = clean_rows(numbered(row(order_date="когда-то", unit_price="договорная")))
    reason = result.skipped[0].reason
    assert "дата" in reason
    assert "цена" in reason
