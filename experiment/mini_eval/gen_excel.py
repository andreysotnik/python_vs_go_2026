"""Генерирует входной sales.xlsx (с «грязными» строками) и эталон expected_summary.json.

Запуск: uv run --no-project --with openpyxl python -I gen_excel.py <out_xlsx> <out_expected_json>
"""
import datetime as dt
import json
import random
import sys
from collections import defaultdict

from openpyxl import Workbook

rnd = random.Random(20261119)
REGIONS = ["Москва", "Санкт-Петербург", "Казань", "Новосибирск", "Екатеринбург"]
CATALOG = {
    "Электроника": [("Смартфон X1", 34990), ("Ноутбук Pro 14", 89990), ("Наушники Air", 7990), ("Планшет Tab 10", 24990), ("Умные часы S", 12990)],
    "Бытовая техника": [("Пылесос V8", 19990), ("Кофемашина Barista", 45990), ("Чайник Smart", 3490), ("Микроволновка M20", 8990)],
    "Книги": [("Книга «Go на практике»", 1290), ("Книга «Python для всех»", 1190), ("Комикс «Космос»", 690)],
    "Одежда": [("Куртка зимняя", 8990), ("Кроссовки Run", 6490), ("Футболка базовая", 990), ("Джинсы Slim", 3990)],
    "Спорт": [("Велосипед City", 29990), ("Гантели 10 кг", 2490), ("Коврик для йоги", 1490)],
}
MANAGERS = ["Иванов И.", "Петрова А.", "Сидоров К.", "Кузнецова М.", "Смирнов Д.", "Волкова Е."]
HEADER = ["order_id", "order_date", "region", "category", "product", "manager", "quantity", "unit_price", "discount"]


def main(out_xlsx, out_json):
    wb = Workbook()
    ws = wb.active
    ws.title = "Sales"
    ws.append(HEADER)

    rows = []
    for i in range(1, 5001):
        cat = rnd.choice(list(CATALOG))
        prod, price = rnd.choice(CATALOG[cat])
        date = dt.date(2025, 1, 1) + dt.timedelta(days=rnd.randrange(365))
        qty = rnd.choice([1, 1, 1, 2, 2, 3, 5, 10])
        disc = rnd.choice([0, 0, 0, 0.05, 0.1, 0.15, 0.2])
        rows.append([f"ORD-{i:05d}", date, rnd.choice(REGIONS), cat, prod, rnd.choice(MANAGERS), qty, float(price), disc])

    # «Грязь» по правилам из ТЗ (индексы фиксированы генератором)
    dirty = rnd.sample(range(len(rows)), 400)
    kinds = ["date_text", "region_spaces", "qty_bad", "qty_neg", "qty_empty", "price_comma", "price_text",
             "disc_pct", "disc_empty", "date_bad", "dup", "price_bad"]
    for n, idx in enumerate(dirty):
        r = rows[idx]
        k = kinds[n % len(kinds)]
        if k == "date_text":
            r[1] = r[1].strftime("%d.%m.%Y")
        elif k == "region_spaces":
            r[2] = f"  {r[2]} "
        elif k == "qty_bad":
            r[6] = "n/a"
        elif k == "qty_neg":
            r[6] = -r[6]
        elif k == "qty_empty":
            r[6] = None
        elif k == "price_comma":
            r[7] = f"{r[7]:.2f}".replace(".", ",")
        elif k == "price_text":
            r[7] = f"{r[7]:.1f}"
        elif k == "disc_pct":
            r[8] = f"{int(round(r[8] * 100))}%"
        elif k == "disc_empty":
            r[8] = None
        elif k == "date_bad":
            r[1] = "когда-то"
        elif k == "dup":
            rows.append(list(rows[(idx + 7) % 5000]))  # дубль order_id → должен быть отброшен
        elif k == "price_bad":
            r[7] = "договорная"

    blank_positions = sorted(rnd.sample(range(len(rows)), 15), reverse=True)
    for p in blank_positions:
        rows.insert(p, [None] * len(HEADER))  # полностью пустые строки

    for r in rows:
        ws.append(r)
    for c in ws["B"][1:]:
        if isinstance(c.value, dt.date):
            c.number_format = "DD.MM.YYYY"

    tg = wb.create_sheet("Targets")
    tg.append(["region", "month", "target"])
    targets = {}
    for reg in REGIONS:
        for m in range(1, 13):
            t = float(rnd.randrange(1_500_000, 4_000_000, 10_000))
            targets[(reg, f"2025-{m:02d}")] = t
            tg.append([reg, f"2025-{m:02d}", t])
    wb.save(out_xlsx)

    # ---------- эталон ----------
    seen, by_m, by_r, by_c, by_p = set(), defaultdict(float), defaultdict(float), defaultdict(float), defaultdict(lambda: [0.0, 0])
    by_rm = defaultdict(float)
    valid = skipped = 0
    for r in rows:
        if all(v is None for v in r):
            continue
        oid, date, reg, cat, prod, man, qty, price, disc = r
        if oid in seen:
            skipped += 1
            continue
        seen.add(oid)
        if isinstance(date, str):
            try:
                date = dt.datetime.strptime(date, "%d.%m.%Y").date()
            except ValueError:
                skipped += 1
                continue
        if not isinstance(qty, int) or qty <= 0:
            skipped += 1
            continue
        if isinstance(price, str):
            try:
                price = float(price.replace(",", "."))
            except ValueError:
                skipped += 1
                continue
        if disc is None:
            disc = 0.0
        elif isinstance(disc, str):
            disc = float(disc.rstrip("%")) / 100
        reg = reg.strip()
        rev = qty * price * (1 - disc)
        valid += 1
        month = f"{date.year}-{date.month:02d}"
        by_m[month] += rev
        by_r[reg] += rev
        by_c[cat] += rev
        by_p[prod][0] += rev
        by_p[prod][1] += qty
        by_rm[(reg, month)] += rev

    r2 = lambda x: round(x, 2)
    top = sorted(by_p.items(), key=lambda kv: (-kv[1][0], kv[0]))[:10]
    plan = []
    for reg in sorted(REGIONS):
        fact = sum(v for (rg, _), v in by_rm.items() if rg == reg)
        target = sum(v for (rg, _), v in targets.items() if rg == reg)
        plan.append({"region": reg, "fact": r2(fact), "target": r2(target), "completion_pct": r2(fact / target * 100)})
    expected = {
        "valid_rows": valid,
        "skipped_rows": skipped,
        "total_revenue": r2(sum(by_m.values())),
        "revenue_by_month": {k: r2(v) for k, v in sorted(by_m.items())},
        "revenue_by_region": {k: r2(v) for k, v in sorted(by_r.items())},
        "revenue_by_category": {k: r2(v) for k, v in sorted(by_c.items())},
        "top_products": [{"product": k, "revenue": r2(v[0]), "quantity": v[1]} for k, v in top],
        "plan_vs_fact": plan,
    }
    with open(out_json, "w", encoding="utf-8") as f:
        json.dump(expected, f, ensure_ascii=False, indent=2)
    print("rows written:", len(rows), "valid:", valid, "skipped:", skipped)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
