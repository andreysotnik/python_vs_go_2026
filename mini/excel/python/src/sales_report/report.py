"""Запись результатов: summary.json, skipped.csv, report.html."""

import csv
import json
from html import escape
from pathlib import Path

from sales_report.aggregate import Summary
from sales_report.charts import CHART_FILES
from sales_report.formatting import format_money, format_number, month_label
from sales_report.models import SkippedRow

_CHART_TITLES = {
    "revenue_by_month.png": "Выручка по месяцам",
    "revenue_by_region.png": "Выручка по регионам",
    "category_share.png": "Доли категорий",
    "top_products.png": "Топ-10 товаров",
    "plan_vs_fact.png": "План / факт по регионам",
}


def write_summary_json(summary: Summary, path: Path) -> None:
    path.write_text(
        json.dumps(summary.to_json_dict(), ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )


def write_skipped_csv(skipped: list[SkippedRow], path: Path) -> None:
    with path.open("w", encoding="utf-8", newline="") as fh:
        writer = csv.writer(fh)
        writer.writerow(["row_number", "order_id", "reason"])
        for row in skipped:
            writer.writerow([row.row_number, row.order_id, row.reason])


def _table(headers: list[str], rows: list[list[str]], numeric_from: int = 1) -> str:
    head = "".join(f"<th>{escape(h)}</th>" for h in headers)
    body = "".join(
        "<tr>"
        + "".join(
            f'<td class="{"num" if i >= numeric_from else ""}">{escape(cell)}</td>'
            for i, cell in enumerate(row)
        )
        + "</tr>"
        for row in rows
    )
    return f"<table><thead><tr>{head}</tr></thead><tbody>{body}</tbody></table>"


def render_html(summary: Summary, source_name: str) -> str:
    kpis = [
        ("Выручка за период", format_money(summary.total_revenue)),
        ("Валидных строк", format_number(summary.valid_rows)),
        ("Пропущено строк", format_number(summary.skipped_rows)),
        ("Регионов", format_number(len(summary.revenue_by_region))),
    ]
    kpi_html = "".join(
        f'<div class="kpi"><div class="label">{escape(k)}</div>'
        f'<div class="value">{escape(v)}</div></div>'
        for k, v in kpis
    )
    plan_rows = [
        [
            pf.region,
            format_money(pf.target),
            format_money(pf.fact),
            "—" if pf.completion_pct is None else f"{format_number(pf.completion_pct, 2)}%",
        ]
        for pf in summary.plan_vs_fact
    ]
    top_rows = [
        [str(i), p.product, format_money(p.revenue), format_number(p.quantity)]
        for i, p in enumerate(summary.top_products, start=1)
    ]
    month_rows = [[month_label(m), format_money(v)] for m, v in summary.revenue_by_month.items()]
    charts = "".join(
        f'<figure><img src="{name}" alt="{escape(_CHART_TITLES[name])}">'
        f"<figcaption>{escape(_CHART_TITLES[name])}</figcaption></figure>"
        for name in CHART_FILES
    )
    return f"""<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Отчёт по продажам</title>
<style>
  body {{ font-family: -apple-system, "Segoe UI", Roboto, Arial, sans-serif; margin: 0;
         background: #f5f7fa; color: #1f2933; }}
  main {{ max-width: 1200px; margin: 0 auto; padding: 24px 16px 48px; }}
  h1 {{ margin: 0 0 4px; }} .muted {{ color: #66788a; margin: 0 0 24px; }}
  .kpis {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
          gap: 16px; margin-bottom: 32px; }}
  .kpi {{ background: #fff; border-radius: 10px; padding: 16px 20px;
         box-shadow: 0 1px 3px rgba(0,0,0,.08); }}
  .kpi .label {{ color: #66788a; font-size: 14px; }}
  .kpi .value {{ font-size: 24px; font-weight: 700; margin-top: 6px; }}
  .charts {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(480px, 1fr));
            gap: 20px; }}
  figure {{ background: #fff; margin: 0; padding: 12px; border-radius: 10px;
           box-shadow: 0 1px 3px rgba(0,0,0,.08); }}
  figure img {{ width: 100%; height: auto; display: block; }}
  figcaption {{ text-align: center; color: #66788a; font-size: 13px; margin-top: 6px; }}
  .tables {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
            gap: 20px; margin-top: 32px; }}
  table {{ width: 100%; border-collapse: collapse; background: #fff; border-radius: 10px;
          overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,.08); font-size: 14px; }}
  th, td {{ padding: 8px 12px; border-bottom: 1px solid #e6eaf0; text-align: left; }}
  th {{ background: #eef2f7; }} td.num {{ text-align: right; white-space: nowrap; }}
  @media (max-width: 560px) {{ .charts, .tables {{ grid-template-columns: 1fr; }} }}
</style>
</head>
<body>
<main>
<h1>Отчёт по продажам</h1>
<p class="muted">Источник: {escape(source_name)}</p>
<section class="kpis">{kpi_html}</section>
<section class="charts">{charts}</section>
<section class="tables">
<div><h2>План / факт за год</h2>
{_table(["Регион", "План", "Факт", "Выполнение"], plan_rows)}</div>
<div><h2>Топ-10 товаров</h2>
{_table(["#", "Товар", "Выручка", "Кол-во"], top_rows, numeric_from=2)}</div>
<div><h2>Выручка по месяцам</h2>
{_table(["Месяц", "Выручка"], month_rows)}</div>
</section>
</main>
</body>
</html>
"""


def write_html(summary: Summary, source_name: str, path: Path) -> None:
    path.write_text(render_html(summary, source_name), encoding="utf-8")
