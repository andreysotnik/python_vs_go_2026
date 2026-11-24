"""Чтение Excel-файла с продажами и планами."""

import zipfile
from collections.abc import Iterator, Sequence
from decimal import Decimal
from pathlib import Path
from typing import Any

from openpyxl import load_workbook
from openpyxl.utils.exceptions import InvalidFileException

from sales_report.cleaning import COLUMNS, is_blank, is_empty_row, parse_text_number
from sales_report.errors import InputFileError
from sales_report.models import Target

SALES_SHEET = "Sales"
TARGETS_SHEET = "Targets"
TARGET_COLUMNS: tuple[str, ...] = ("region", "month", "target")

type Row = tuple[int, Sequence[Any]]


class Workbook:
    """Обёртка над openpyxl: проверка структуры и выдача строк листов."""

    def __init__(self, path: Path) -> None:
        if not path.is_file():
            raise InputFileError(f"Входной файл не найден: {path}")
        try:
            self._wb = load_workbook(path, read_only=True, data_only=True)
        except (InvalidFileException, zipfile.BadZipFile, KeyError, OSError) as exc:
            raise InputFileError(f"Не удалось открыть Excel-файл {path}: {exc}") from exc
        self.path = path

    def close(self) -> None:
        self._wb.close()

    def __enter__(self) -> "Workbook":
        return self

    def __exit__(self, *_: object) -> None:
        self.close()

    def _rows(self, sheet: str, expected: tuple[str, ...]) -> Iterator[Row]:
        if sheet not in self._wb.sheetnames:
            raise InputFileError(
                f"В файле {self.path} нет листа {sheet!r} (есть: {', '.join(self._wb.sheetnames)})"
            )
        rows = self._wb[sheet].iter_rows(values_only=True)
        header = next(rows, None)
        actual = tuple(str(v).strip().lower() if v is not None else "" for v in (header or ()))[
            : len(expected)
        ]
        if actual != expected:
            raise InputFileError(
                f"Лист {sheet!r}: ожидался заголовок {', '.join(expected)}, "
                f"получено {', '.join(actual) or '<пусто>'}"
            )
        for index, values in enumerate(rows, start=2):
            yield index, tuple(values[: len(expected)])

    def sales_rows(self) -> Iterator[Row]:
        """Строки листа Sales (без заголовка) с номерами строк Excel."""
        return self._rows(SALES_SHEET, COLUMNS)

    def targets(self) -> list[Target]:
        """Строки листа Targets. Некорректная строка плана — ошибка входных данных."""
        result: list[Target] = []
        for row_number, values in self._rows(TARGETS_SHEET, TARGET_COLUMNS):
            if is_empty_row(values):
                continue
            result.append(_parse_target(row_number, values))
        return result


def _parse_target(row_number: int, values: Sequence[Any]) -> Target:
    region, month, target = (list(values) + [None] * 3)[:3]
    where = f"лист {TARGETS_SHEET!r}, строка {row_number}"
    if is_blank(region):
        raise InputFileError(f"{where}: пустой регион")
    month_text = month.strftime("%Y-%m") if hasattr(month, "strftime") else str(month).strip()
    if len(month_text) != 7 or month_text[4] != "-" or not month_text.replace("-", "").isdigit():
        raise InputFileError(f"{where}: месяц должен быть в формате YYYY-MM, получено {month!r}")
    try:
        if isinstance(target, int | float) and not isinstance(target, bool):
            amount = Decimal(str(target))
        elif isinstance(target, str):
            amount = parse_text_number(target)
        else:
            raise ValueError
    except ValueError as exc:
        raise InputFileError(f"{where}: план не число: {target!r}") from exc
    if amount < 0:
        raise InputFileError(f"{where}: отрицательный план {target!r}")
    return Target(region=str(region).strip(), month=month_text, target=amount)
