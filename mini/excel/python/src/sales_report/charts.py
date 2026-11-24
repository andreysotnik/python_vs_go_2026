"""Построение PNG-графиков.

Используется объектный API matplotlib (``Figure`` без ``pyplot``): не нужен
GUI-бэкенд и глобальное состояние, фигуры не утекают.
"""

from pathlib import Path

from matplotlib.axes import Axes
from matplotlib.figure import Figure
from matplotlib.ticker import FuncFormatter

from sales_report.aggregate import Summary
from sales_report.formatting import (
    format_millions,
    format_number,
    month_label,
)

CHART_FILES: tuple[str, ...] = (
    "revenue_by_month.png",
    "revenue_by_region.png",
    "category_share.png",
    "top_products.png",
    "plan_vs_fact.png",
)

_PALETTE = ("#2E6BC6", "#E8833A", "#3BA272", "#C0504D", "#8064A2", "#4BACC6", "#9C9C9C")
_PLAN_COLOR = "#B8C4D6"
_FACT_COLOR = "#2E6BC6"
_DPI = 120
_MIN_PCT_LABEL = 4.0  # меньшие доли подписываются только в легенде

_thousands = FuncFormatter(lambda x, _pos: format_number(x))


def _style(ax: Axes, title: str, xlabel: str = "", ylabel: str = "") -> None:
    ax.set_title(title, fontsize=14, fontweight="bold", pad=12)
    ax.set_xlabel(xlabel, fontsize=11)
    ax.set_ylabel(ylabel, fontsize=11)
    ax.spines[["top", "right"]].set_visible(False)
    ax.set_axisbelow(True)


def _save(fig: Figure, path: Path) -> None:
    fig.tight_layout()
    fig.savefig(path, dpi=_DPI, bbox_inches="tight", facecolor="white")


def revenue_by_month(summary: Summary, path: Path) -> None:
    months = list(summary.revenue_by_month)
    values = [float(v) for v in summary.revenue_by_month.values()]
    fig = Figure(figsize=(11, 5.5))
    ax = fig.subplots()
    ax.plot(range(len(months)), values, marker="o", linewidth=2.2, color=_PALETTE[0])
    for x, y in enumerate(values):
        ax.annotate(
            format_millions(y),
            (x, y),
            textcoords="offset points",
            xytext=(0, 8),
            ha="center",
            fontsize=8.5,
        )
    ax.set_xticks(range(len(months)), [month_label(m) for m in months], rotation=45, ha="right")
    ax.yaxis.set_major_formatter(_thousands)
    ax.set_ylim(0, max(values, default=1) * 1.15)
    ax.grid(axis="y", alpha=0.3)
    _style(ax, "Выручка по месяцам", "Месяц", "Выручка, руб.")
    _save(fig, path)


def revenue_by_region(summary: Summary, path: Path) -> None:
    items = sorted(summary.revenue_by_region.items(), key=lambda kv: kv[1], reverse=True)
    regions = [k for k, _ in items]
    values = [float(v) for _, v in items]
    fig = Figure(figsize=(10, 5.5))
    ax = fig.subplots()
    bars = ax.bar(regions, values, color=_PALETTE[0], width=0.6)
    ax.bar_label(bars, labels=[format_millions(v) for v in values], padding=3, fontsize=9.5)
    ax.yaxis.set_major_formatter(_thousands)
    ax.set_ylim(0, max(values, default=1) * 1.12)
    ax.grid(axis="y", alpha=0.3)
    _style(ax, "Выручка по регионам", "Регион", "Выручка, руб.")
    _save(fig, path)


def category_share(summary: Summary, path: Path) -> None:
    items = sorted(summary.revenue_by_category.items(), key=lambda kv: kv[1], reverse=True)
    labels = [k for k, _ in items]
    values = [float(v) for _, v in items]
    total = sum(values) or 1.0
    fig = Figure(figsize=(9, 6.5))
    ax = fig.subplots()
    wedges, _texts, autotexts = ax.pie(
        values,
        colors=_PALETTE[: len(values)],
        startangle=90,
        counterclock=False,
        autopct=lambda pct: f"{format_number(pct, 1)}%" if pct >= _MIN_PCT_LABEL else "",
        pctdistance=0.78,
        wedgeprops={"width": 0.42, "edgecolor": "white", "linewidth": 2},
        textprops={"fontsize": 10},
    )
    for text in autotexts:
        text.set_color("white")
        text.set_fontweight("bold")
    ax.text(0, 0, f"Всего\n{format_millions(total)}\nруб.", ha="center", va="center", fontsize=12)
    ax.legend(
        wedges,
        [
            f"{label}: {format_number(v / total * 100, 1)}% ({format_number(v)} руб.)"
            for label, v in zip(labels, values, strict=True)
        ],
        title="Категория",
        loc="center left",
        bbox_to_anchor=(1.0, 0.5),
        frameon=False,
    )
    ax.set_aspect("equal")
    ax.set_title("Доли категорий в выручке", fontsize=14, fontweight="bold", pad=12)
    _save(fig, path)


def top_products(summary: Summary, path: Path) -> None:
    products = list(reversed(summary.top_products))  # крупнейший — сверху
    names = [p.product for p in products]
    values = [float(p.revenue) for p in products]
    fig = Figure(figsize=(11, 6.5))
    ax = fig.subplots()
    bars = ax.barh(names, values, color=_PALETTE[0], height=0.65)
    ax.bar_label(bars, labels=[format_number(v) for v in values], padding=4, fontsize=9)
    ax.xaxis.set_major_formatter(_thousands)
    ax.set_xlim(0, max(values, default=1) * 1.18)
    ax.grid(axis="x", alpha=0.3)
    _style(ax, "Топ-10 товаров по выручке", "Выручка, руб.", "")
    _save(fig, path)


def plan_vs_fact(summary: Summary, path: Path) -> None:
    rows = summary.plan_vs_fact
    regions = [r.region for r in rows]
    targets = [float(r.target) for r in rows]
    facts = [float(r.fact) for r in rows]
    positions = range(len(rows))
    width = 0.38
    fig = Figure(figsize=(11, 6))
    ax = fig.subplots()
    ax.bar([p - width / 2 for p in positions], targets, width, label="План", color=_PLAN_COLOR)
    fact_bars = ax.bar(
        [p + width / 2 for p in positions], facts, width, label="Факт", color=_FACT_COLOR
    )
    labels = [
        "—" if r.completion_pct is None else f"{format_number(r.completion_pct, 1)}%" for r in rows
    ]
    ax.bar_label(fact_bars, labels=labels, padding=3, fontsize=9.5, fontweight="bold")
    ax.set_xticks(list(positions), regions)
    ax.yaxis.set_major_formatter(_thousands)
    ax.set_ylim(0, max([*targets, *facts], default=1) * 1.12)
    ax.grid(axis="y", alpha=0.3)
    ax.legend(frameon=False)
    _style(
        ax,
        "План и факт выручки за год по регионам (подпись — % выполнения)",
        "Регион",
        "Выручка, руб.",
    )
    _save(fig, path)


def render_all(summary: Summary, out_dir: Path) -> list[Path]:
    """Строит все графики; возвращает пути в порядке CHART_FILES."""
    builders = (revenue_by_month, revenue_by_region, category_share, top_products, plan_vs_fact)
    paths: list[Path] = []
    for name, builder in zip(CHART_FILES, builders, strict=True):
        path = out_dir / name
        builder(summary, path)
        paths.append(path)
    return paths
