"""Доменные модели."""

from dataclasses import dataclass
from datetime import date
from decimal import Decimal


@dataclass(frozen=True, slots=True)
class Sale:
    """Валидная строка продаж после очистки."""

    row_number: int
    order_id: str
    order_date: date
    region: str
    category: str
    product: str
    manager: str
    quantity: int
    unit_price: Decimal
    discount: Decimal

    @property
    def revenue(self) -> Decimal:
        """Выручка строки без округления: quantity * unit_price * (1 - discount)."""
        return self.quantity * self.unit_price * (1 - self.discount)

    @property
    def month(self) -> str:
        """Месяц заказа в формате YYYY-MM."""
        return f"{self.order_date.year:04d}-{self.order_date.month:02d}"


@dataclass(frozen=True, slots=True)
class SkippedRow:
    """Пропущенная строка: номер строки в Excel и причина."""

    row_number: int
    order_id: str
    reason: str


@dataclass(frozen=True, slots=True)
class Target:
    """План выручки по региону на месяц."""

    region: str
    month: str
    target: Decimal
