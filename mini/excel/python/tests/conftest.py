from datetime import date
from decimal import Decimal
from pathlib import Path

import pytest

from sales_report.models import Sale

PROJECT_ROOT = Path(__file__).resolve().parent.parent
SAMPLE_XLSX = PROJECT_ROOT / "data" / "sales.xlsx"


def make_sale(
    *,
    product: str = "Товар",
    region: str = "Москва",
    category: str = "Книги",
    order_date: date = date(2025, 1, 15),
    quantity: int = 1,
    unit_price: str = "100",
    discount: str = "0",
    order_id: str = "ORD-1",
) -> Sale:
    return Sale(
        row_number=2,
        order_id=order_id,
        order_date=order_date,
        region=region,
        category=category,
        product=product,
        manager="Иванов И.",
        quantity=quantity,
        unit_price=Decimal(unit_price),
        discount=Decimal(discount),
    )


@pytest.fixture
def sample_xlsx() -> Path:
    if not SAMPLE_XLSX.is_file():
        pytest.skip("data/sales.xlsx отсутствует")
    return SAMPLE_XLSX
