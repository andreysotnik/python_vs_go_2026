"""Форматирование чисел и подписей для графиков и отчёта."""

from decimal import Decimal

THOUSANDS_SEP = " "  # неразрывный пробел — русская типографика
MONTHS_RU = (
    "янв",
    "фев",
    "мар",
    "апр",
    "май",
    "июн",
    "июл",
    "авг",
    "сен",
    "окт",
    "ноя",
    "дек",
)


def format_number(value: float | Decimal, decimals: int = 0) -> str:
    """1234567.891 -> '1 234 567' (или с decimals знаками и запятой)."""
    text = f"{float(value):,.{decimals}f}"
    return text.replace(",", THOUSANDS_SEP).replace(".", ",")


def format_money(value: float | Decimal) -> str:
    return f"{format_number(value, 2)} руб."


def format_millions(value: float | Decimal) -> str:
    """Короткая подпись: 12 345 678 -> '12,3 млн'."""
    return f"{format_number(float(value) / 1_000_000, 1)} млн"


def month_label(month: str) -> str:
    """'2025-03' -> 'мар 2025'."""
    year, month_number = month.split("-")
    return f"{MONTHS_RU[int(month_number) - 1]} {year}"
