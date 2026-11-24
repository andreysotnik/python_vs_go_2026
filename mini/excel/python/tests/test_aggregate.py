"""Модульные тесты агрегатов и формата summary.json."""

from datetime import date
from decimal import Decimal

from sales_report.aggregate import (
    build_summary,
    money,
    plan_vs_fact,
    sum_by,
    top_products,
)
from sales_report.models import Target
from tests.conftest import make_sale


def test_money_rounds_half_up_only_on_output() -> None:
    assert money(Decimal("0.125")) == 0.13
    assert money(Decimal("2.675")) == 2.68  # float(2.675) округлился бы до 2.67


def test_sum_keeps_full_precision() -> None:
    # 3 строки по 0.005: при округлении каждой было бы 0.03, точная сумма — 0.015.
    sales = [make_sale(unit_price="0.005", order_id=str(i)) for i in range(3)]
    summary = build_summary(sales, 0, [])
    assert summary.total_revenue == Decimal("0.015")
    assert summary.to_json_dict()["total_revenue"] == 0.02


def test_sum_by_month_sorted() -> None:
    sales = [
        make_sale(order_date=date(2025, 3, 1), unit_price="10"),
        make_sale(order_date=date(2025, 1, 31), unit_price="5"),
        make_sale(order_date=date(2025, 3, 31), unit_price="1"),
    ]
    assert sum_by(sales, lambda s: s.month) == {"2025-01": Decimal(5), "2025-03": Decimal(11)}


def test_top_products_order_and_tie_break() -> None:
    sales = [
        make_sale(product="Б", unit_price="100"),
        make_sale(product="А", unit_price="100"),
        make_sale(product="В", unit_price="50", quantity=3),
        make_sale(product="Г", unit_price="10"),
    ]
    result = top_products(sales)
    assert [p.product for p in result] == ["В", "А", "Б", "Г"]
    assert result[0].quantity == 3
    assert result[0].revenue == Decimal(150)


def test_top_products_limited_to_ten() -> None:
    sales = [make_sale(product=f"P{i:02d}", unit_price=str(i + 1)) for i in range(15)]
    result = top_products(sales)
    assert len(result) == 10
    assert result[0].product == "P14"


def test_plan_vs_fact_year_sum_and_alphabetical() -> None:
    targets = [
        Target("Москва", "2025-01", Decimal(100)),
        Target("Москва", "2025-02", Decimal(100)),
        Target("Казань", "2025-01", Decimal(50)),
    ]
    rows = plan_vs_fact({"Москва": Decimal(150), "Казань": Decimal(50)}, targets)
    assert [r.region for r in rows] == ["Казань", "Москва"]
    assert rows[1].target == Decimal(200)
    assert rows[1].completion_pct == Decimal(75)
    assert rows[0].completion_pct == Decimal(100)


def test_plan_vs_fact_missing_target_or_fact() -> None:
    rows = plan_vs_fact({"Сочи": Decimal(10)}, [Target("Тула", "2025-01", Decimal(5))])
    by_region = {r.region: r for r in rows}
    assert by_region["Сочи"].completion_pct is None
    assert by_region["Тула"].fact == 0
    assert by_region["Тула"].completion_pct == 0


def test_summary_json_structure_and_values() -> None:
    sales = [
        make_sale(region="Москва", category="Книги", unit_price="1000", discount="0.15"),
        make_sale(
            region="Казань",
            category="Спорт",
            unit_price="2000",
            quantity=2,
            order_date=date(2025, 2, 1),
        ),
    ]
    targets = [
        Target("Москва", "2025-01", Decimal(1000)),
        Target("Казань", "2025-01", Decimal(8000)),
    ]
    data = build_summary(sales, 3, targets).to_json_dict()
    assert list(data) == [
        "valid_rows",
        "skipped_rows",
        "total_revenue",
        "revenue_by_month",
        "revenue_by_region",
        "revenue_by_category",
        "top_products",
        "plan_vs_fact",
    ]
    assert data["valid_rows"] == 2
    assert data["skipped_rows"] == 3
    assert data["total_revenue"] == 4850.0
    assert data["revenue_by_month"] == {"2025-01": 850.0, "2025-02": 4000.0}
    assert list(data["revenue_by_region"]) == ["Казань", "Москва"]
    assert data["revenue_by_category"] == {"Книги": 850.0, "Спорт": 4000.0}
    assert data["top_products"][0] == {"product": "Товар", "revenue": 4850.0, "quantity": 3}
    assert data["plan_vs_fact"] == [
        {"region": "Казань", "fact": 4000.0, "target": 8000.0, "completion_pct": 50.0},
        {"region": "Москва", "fact": 850.0, "target": 1000.0, "completion_pct": 85.0},
    ]
