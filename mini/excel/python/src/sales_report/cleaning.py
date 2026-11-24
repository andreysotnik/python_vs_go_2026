"""Правила очистки данных листа Sales.

Каждое правило — отдельная чистая функция. Функции разбора бросают
``ValueError`` с человекочитаемой причиной; ``clean_rows`` собирает причины
и формирует список валидных и пропущенных строк.
"""

import re
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass, field
from datetime import date, datetime
from decimal import Decimal, InvalidOperation
from typing import Any

from openpyxl.utils.datetime import from_excel

from sales_report.models import Sale, SkippedRow

COLUMNS: tuple[str, ...] = (
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

_DATE_TEXT_RE = re.compile(r"^(\d{1,2})\.(\d{1,2})\.(\d{4})$")
_NUMBER_TEXT_RE = re.compile(r"^[+-]?(\d+([.,]\d*)?|[.,]\d+)$")


def is_blank(value: Any) -> bool:
    """Пустая ячейка: None или строка из одних пробельных символов."""
    return value is None or (isinstance(value, str) and not value.strip())


def is_empty_row(values: Sequence[Any]) -> bool:
    """Правило 1: строка полностью пустая."""
    return all(is_blank(v) for v in values)


def _is_number(value: Any) -> bool:
    # bool — подкласс int, но как число в таблице продаж смысла не имеет.
    return isinstance(value, int | float | Decimal) and not isinstance(value, bool)


def _to_decimal(value: int | float | Decimal) -> Decimal:
    # Через str(): 0.15 -> Decimal("0.15"), а не двоичный хвост float.
    result = Decimal(str(value))
    if not result.is_finite():
        raise ValueError(f"нечисловое значение {value!r}")
    return result


def parse_text_number(text: str) -> Decimal:
    """Разбор числа из текста с точкой или запятой как десятичным разделителем."""
    cleaned = text.strip()
    if not _NUMBER_TEXT_RE.match(cleaned):
        raise ValueError(f"не число: {text!r}")
    return Decimal(cleaned.replace(",", "."))


def parse_order_date(value: Any) -> date:
    """Правило 3: дата Excel (datetime или серийный номер) либо текст DD.MM.YYYY."""
    if isinstance(value, datetime):
        return value.date()
    if isinstance(value, date):
        return value
    if _is_number(value):
        try:
            converted = from_excel(value)
        except (ValueError, OverflowError, TypeError) as exc:
            raise ValueError(f"некорректная дата Excel: {value!r}") from exc
        if isinstance(converted, datetime):
            return converted.date()
        raise ValueError(f"некорректная дата Excel: {value!r}")
    if isinstance(value, str):
        match = _DATE_TEXT_RE.match(value.strip())
        if match:
            day, month, year = (int(g) for g in match.groups())
            try:
                return date(year, month, day)
            except ValueError as exc:
                raise ValueError(f"несуществующая дата: {value!r}") from exc
    if is_blank(value):
        raise ValueError("пустая дата")
    raise ValueError(f"дата не разбирается: {value!r}")


def parse_quantity(value: Any) -> int:
    """Правило 4: целое число > 0; пусто, текст, ноль и отрицательное — ошибка."""
    if is_blank(value):
        raise ValueError("пустое количество")
    if not _is_number(value):
        raise ValueError(f"количество не число: {value!r}")
    number = _to_decimal(value)
    if number != number.to_integral_value():
        raise ValueError(f"количество не целое: {value!r}")
    if number <= 0:
        raise ValueError(f"количество не положительное: {value!r}")
    return int(number)


def parse_unit_price(value: Any) -> Decimal:
    """Правило 5: число > 0, допускается текст с точкой или запятой."""
    if is_blank(value):
        raise ValueError("пустая цена")
    if _is_number(value):
        price = _to_decimal(value)
    elif isinstance(value, str):
        try:
            price = parse_text_number(value)
        except ValueError as exc:
            raise ValueError(f"цена не разбирается: {value!r}") from exc
    else:
        raise ValueError(f"цена не разбирается: {value!r}")
    if price <= 0:
        raise ValueError(f"цена не положительная: {value!r}")
    return price


def parse_discount(value: Any) -> Decimal:
    """Правило 6: доля 0..1 или текст с процентом ("15%"); пусто — 0."""
    if is_blank(value):
        return Decimal(0)
    if _is_number(value):
        discount = _to_decimal(value)
    elif isinstance(value, str):
        text = value.strip()
        try:
            if text.endswith("%"):
                discount = parse_text_number(text[:-1]) / 100
            else:
                discount = parse_text_number(text)
        except (ValueError, InvalidOperation) as exc:
            raise ValueError(f"скидка не разбирается: {value!r}") from exc
    else:
        raise ValueError(f"скидка не разбирается: {value!r}")
    if not Decimal(0) <= discount <= Decimal(1):
        raise ValueError(f"скидка вне диапазона 0..1: {value!r}")
    return discount


def parse_required_text(value: Any, field_name: str) -> str:
    """Обязательное текстовое поле; правило 7 — обрезка пробелов по краям."""
    if is_blank(value):
        raise ValueError(f"пустое поле {field_name}")
    return str(value).strip()


def normalize_order_id(value: Any) -> str:
    """Ключ для поиска дубликатов: строковое представление без краевых пробелов."""
    if is_blank(value):
        return ""
    if isinstance(value, float) and value.is_integer():
        return str(int(value))
    return str(value).strip()


@dataclass(slots=True)
class CleaningResult:
    """Итог очистки: валидные продажи и пропущенные строки."""

    sales: list[Sale] = field(default_factory=list)
    skipped: list[SkippedRow] = field(default_factory=list)


_FIELD_CHECKS: tuple[tuple[str, Callable[[Any], object]], ...] = (
    ("order_id", lambda v: parse_required_text(v, "order_id")),
    ("order_date", parse_order_date),
    ("region", lambda v: parse_required_text(v, "region")),
    ("category", lambda v: parse_required_text(v, "category")),
    ("product", lambda v: parse_required_text(v, "product")),
    ("quantity", parse_quantity),
    ("unit_price", parse_unit_price),
    ("discount", parse_discount),
)


def collect_errors(raw: dict[str, Any]) -> list[str]:
    """Прогоняет все правила по строке и возвращает все найденные причины."""
    errors: list[str] = []
    for column, check in _FIELD_CHECKS:
        try:
            check(raw[column])
        except ValueError as exc:
            errors.append(str(exc))
    return errors


def parse_row(row_number: int, values: Sequence[Any]) -> Sale:
    """Разбирает одну непустую строку. Бросает ValueError со всеми причинами сразу."""
    padded = list(values) + [None] * (len(COLUMNS) - len(values))
    raw = dict(zip(COLUMNS, padded, strict=False))
    errors = collect_errors(raw)
    if errors:
        raise ValueError("; ".join(errors))
    # Все проверки пройдены — повторный разбор не бросит исключение (функции чистые).
    return Sale(
        row_number=row_number,
        order_id=parse_required_text(raw["order_id"], "order_id"),
        order_date=parse_order_date(raw["order_date"]),
        region=parse_required_text(raw["region"], "region"),
        category=parse_required_text(raw["category"], "category"),
        product=parse_required_text(raw["product"], "product"),
        manager="" if is_blank(raw["manager"]) else str(raw["manager"]).strip(),
        quantity=parse_quantity(raw["quantity"]),
        unit_price=parse_unit_price(raw["unit_price"]),
        discount=parse_discount(raw["discount"]),
    )


def clean_rows(rows: Iterable[tuple[int, Sequence[Any]]]) -> CleaningResult:
    """Применяет все правила очистки к строкам (номер строки Excel, значения)."""
    result = CleaningResult()
    seen_ids: set[str] = set()
    for row_number, values in rows:
        if is_empty_row(values):
            continue
        order_id = normalize_order_id(values[0] if values else None)
        if order_id:
            if order_id in seen_ids:
                result.skipped.append(
                    SkippedRow(row_number, order_id, f"дубликат order_id {order_id}")
                )
                continue
            seen_ids.add(order_id)
        try:
            result.sales.append(parse_row(row_number, values))
        except ValueError as exc:
            result.skipped.append(SkippedRow(row_number, order_id, str(exc)))
    return result
