"""Точка входа командной строки."""

import argparse
import logging
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

from sales_report.aggregate import Summary, build_summary
from sales_report.charts import render_all
from sales_report.cleaning import clean_rows
from sales_report.errors import SalesReportError
from sales_report.reader import Workbook
from sales_report.report import write_html, write_skipped_csv, write_summary_json

log = logging.getLogger("sales_report")


@dataclass(frozen=True, slots=True)
class RunResult:
    summary: Summary
    out_dir: Path


def run(input_path: Path, out_dir: Path) -> RunResult:
    """Полный конвейер: чтение -> очистка -> агрегаты -> файлы в out_dir."""
    with Workbook(input_path) as workbook:
        cleaned = clean_rows(workbook.sales_rows())
        targets = workbook.targets()

    for row in cleaned.skipped:
        log.warning("строка %d пропущена (%s): %s", row.row_number, row.order_id or "-", row.reason)

    summary = build_summary(cleaned.sales, len(cleaned.skipped), targets)
    try:
        out_dir.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        raise SalesReportError(f"Не удалось создать каталог {out_dir}: {exc}") from exc

    write_summary_json(summary, out_dir / "summary.json")
    write_skipped_csv(cleaned.skipped, out_dir / "skipped.csv")
    render_all(summary, out_dir)
    write_html(summary, input_path.name, out_dir / "report.html")
    return RunResult(summary=summary, out_dir=out_dir)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="sales-report",
        description="Очистка выгрузки продаж из Excel, агрегаты, графики и HTML-отчёт.",
    )
    parser.add_argument(
        "--input", required=True, type=Path, help="путь к .xlsx (листы Sales, Targets)"
    )
    parser.add_argument("--out", required=True, type=Path, help="каталог для результатов")
    parser.add_argument(
        "-q",
        "--quiet",
        action="store_true",
        help="не печатать в stderr каждую пропущенную строку (они всё равно в skipped.csv)",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    logging.basicConfig(
        level=logging.ERROR if args.quiet else logging.INFO,
        format="%(levelname)s: %(message)s",
        stream=sys.stderr,
        force=True,
    )
    try:
        result = run(args.input, args.out)
    except SalesReportError as exc:
        log.error("%s", exc)
        return 2
    s = result.summary
    log.info(
        "Готово: валидных строк %d, пропущено %d, выручка %.2f. Результаты: %s",
        s.valid_rows,
        s.skipped_rows,
        s.total_revenue,
        result.out_dir.resolve(),
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
