"""Агрегаты по очищенным продажам и формирование summary.json."""

from collections import defaultdict
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from decimal import ROUND_HALF_UP, Decimal
from typing import Any

from sales_report.models import Sale, Target

TOP_PRODUCTS_LIMIT = 10
_CENT = Decimal("0.01")


def money(value: Decimal) -> float:
    """Округление до 2 знаков (half-up) только для вывода."""
    return float(value.quantize(_CENT, rounding=ROUND_HALF_UP))


@dataclass(frozen=True, slots=True)
class ProductStat:
    product: str
    revenue: Decimal
    quantity: int


@dataclass(frozen=True, slots=True)
class PlanFact:
    region: str
    fact: Decimal
    target: Decimal

    @property
    def completion_pct(self) -> Decimal | None:
        """fact / target * 100; None, если план нулевой или отсутствует."""
        if self.target == 0:
            return None
        return self.fact / self.target * 100


@dataclass(frozen=True, slots=True)
class Summary:
    valid_rows: int
    skipped_rows: int
    total_revenue: Decimal
    revenue_by_month: dict[str, Decimal]
    revenue_by_region: dict[str, Decimal]
    revenue_by_category: dict[str, Decimal]
    top_products: list[ProductStat]
    plan_vs_fact: list[PlanFact]

    def to_json_dict(self) -> dict[str, Any]:
        """Структура summary.json; словари отсортированы по ключу, суммы округлены."""

        def rounded(mapping: dict[str, Decimal]) -> dict[str, float]:
            return {key: money(mapping[key]) for key in sorted(mapping)}

        return {
            "valid_rows": self.valid_rows,
            "skipped_rows": self.skipped_rows,
            "total_revenue": money(self.total_revenue),
            "revenue_by_month": rounded(self.revenue_by_month),
            "revenue_by_region": rounded(self.revenue_by_region),
            "revenue_by_category": rounded(self.revenue_by_category),
            "top_products": [
                {"product": p.product, "revenue": money(p.revenue), "quantity": p.quantity}
                for p in self.top_products
            ],
            "plan_vs_fact": [
                {
                    "region": pf.region,
                    "fact": money(pf.fact),
                    "target": money(pf.target),
                    "completion_pct": (
                        None if pf.completion_pct is None else money(pf.completion_pct)
                    ),
                }
                for pf in self.plan_vs_fact
            ],
        }


def sum_by(sales: Iterable[Sale], key: Callable[[Sale], str]) -> dict[str, Decimal]:
    """Сумма выручки по ключу; результат отсортирован по ключу."""
    totals: dict[str, Decimal] = defaultdict(Decimal)
    for sale in sales:
        totals[key(sale)] += sale.revenue
    return dict(sorted(totals.items()))


def top_products(sales: Iterable[Sale], limit: int = TOP_PRODUCTS_LIMIT) -> list[ProductStat]:
    """Топ товаров по убыванию выручки; при равенстве — по названию."""
    revenue: dict[str, Decimal] = defaultdict(Decimal)
    quantity: dict[str, int] = defaultdict(int)
    for sale in sales:
        revenue[sale.product] += sale.revenue
        quantity[sale.product] += sale.quantity
    ordered = sorted(revenue, key=lambda name: (-revenue[name], name))
    return [ProductStat(name, revenue[name], quantity[name]) for name in ordered[:limit]]


def plan_vs_fact(
    revenue_by_region: dict[str, Decimal], targets: Iterable[Target]
) -> list[PlanFact]:
    """План/факт за год по каждому региону (объединение регионов продаж и плана)."""
    target_by_region: dict[str, Decimal] = defaultdict(Decimal)
    for target in targets:
        target_by_region[target.region] += target.target
    regions = sorted(set(revenue_by_region) | set(target_by_region))
    return [
        PlanFact(
            region=region,
            fact=revenue_by_region.get(region, Decimal(0)),
            target=target_by_region.get(region, Decimal(0)),
        )
        for region in regions
    ]


def build_summary(sales: list[Sale], skipped_rows: int, targets: Iterable[Target]) -> Summary:
    by_region = sum_by(sales, lambda s: s.region)
    return Summary(
        valid_rows=len(sales),
        skipped_rows=skipped_rows,
        total_revenue=sum((s.revenue for s in sales), Decimal(0)),
        revenue_by_month=sum_by(sales, lambda s: s.month),
        revenue_by_region=by_region,
        revenue_by_category=sum_by(sales, lambda s: s.category),
        top_products=top_products(sales),
        plan_vs_fact=plan_vs_fact(by_region, targets),
    )
