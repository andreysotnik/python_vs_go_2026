"""Интеграционные тесты: прогон CLI на data/sales.xlsx и на синтетических файлах."""

import csv
import json
from datetime import datetime
from pathlib import Path

import pytest
from openpyxl import Workbook

from sales_report.charts import CHART_FILES
from sales_report.cli import main
from tests.conftest import SAMPLE_XLSX

SUMMARY_KEYS = {
    "valid_rows",
    "skipped_rows",
    "total_revenue",
    "revenue_by_month",
    "revenue_by_region",
    "revenue_by_category",
    "top_products",
    "plan_vs_fact",
}
PNG_MAGIC = b"\x89PNG\r\n\x1a\n"


@pytest.fixture(scope="module")
def sample_run(tmp_path_factory: pytest.TempPathFactory) -> Path:
    if not SAMPLE_XLSX.is_file():
        pytest.skip("data/sales.xlsx отсутствует")
    out = tmp_path_factory.mktemp("out")
    assert main(["--input", str(SAMPLE_XLSX), "--out", str(out), "--quiet"]) == 0
    return out


def test_summary_structure(sample_run: Path) -> None:
    data = json.loads((sample_run / "summary.json").read_text(encoding="utf-8"))
    assert set(data) == SUMMARY_KEYS
    assert isinstance(data["valid_rows"], int)
    assert isinstance(data["skipped_rows"], int)
    assert isinstance(data["total_revenue"], float)
    for key in ("revenue_by_month", "revenue_by_region", "revenue_by_category"):
        assert list(data[key]) == sorted(data[key]), key
        assert all(isinstance(v, float) for v in data[key].values())
    assert len(data["top_products"]) == 10
    for item in data["top_products"]:
        assert set(item) == {"product", "revenue", "quantity"}
    revenues = [(-p["revenue"], p["product"]) for p in data["top_products"]]
    assert revenues == sorted(revenues)
    regions = [r["region"] for r in data["plan_vs_fact"]]
    assert regions == sorted(regions)
    for item in data["plan_vs_fact"]:
        assert set(item) == {"region", "fact", "target", "completion_pct"}


def test_summary_values_on_sample(sample_run: Path) -> None:
    data = json.loads((sample_run / "summary.json").read_text(encoding="utf-8"))
    # 5033 непустые строки: 4833 валидных + 200 пропущенных (из них 33 дубликата).
    assert data["valid_rows"] == 4833
    assert data["skipped_rows"] == 200
    assert data["total_revenue"] == 207149040.5
    assert len(data["revenue_by_month"]) == 12
    assert set(data["revenue_by_region"]) == {
        "Екатеринбург",
        "Казань",
        "Москва",
        "Новосибирск",
        "Санкт-Петербург",
    }
    for key in ("revenue_by_month", "revenue_by_region", "revenue_by_category"):
        assert sum(data[key].values()) == pytest.approx(data["total_revenue"], abs=0.05)
    for item in data["plan_vs_fact"]:
        assert item["completion_pct"] == pytest.approx(
            item["fact"] / item["target"] * 100, abs=0.01
        )


def test_all_pngs_present(sample_run: Path) -> None:
    for name in CHART_FILES:
        path = sample_run / name
        assert path.is_file(), name
        assert path.read_bytes()[:8] == PNG_MAGIC
        assert path.stat().st_size > 10_000


def test_report_html_references_charts(sample_run: Path) -> None:
    html = (sample_run / "report.html").read_text(encoding="utf-8")
    for name in CHART_FILES:
        assert f'src="{name}"' in html
    assert "Отчёт по продажам" in html


def test_skipped_csv_matches_summary(sample_run: Path) -> None:
    data = json.loads((sample_run / "summary.json").read_text(encoding="utf-8"))
    with (sample_run / "skipped.csv").open(encoding="utf-8") as fh:
        rows = list(csv.DictReader(fh))
    assert len(rows) == data["skipped_rows"]
    assert all(r["row_number"].isdigit() and r["reason"] for r in rows)
    assert sum("дубликат" in r["reason"] for r in rows) == 33


def _write_workbook(path: Path) -> None:
    wb = Workbook()
    sales = wb.active
    sales.title = "Sales"
    sales.append(
        [
            "order_id",
            "order_date",
            "region",
            "category",
            "product",
            "manager",
            "quantity",
            "unit_price",
            "discount",
        ]
    )
    sales.append(["A1", datetime(2025, 1, 5), " Москва ", "Книги", "К1", "М", 2, "100,50", "10%"])
    sales.append([None] * 9)
    sales.append(["A1", datetime(2025, 1, 6), "Москва", "Книги", "К1", "М", 1, 100, 0])
    sales.append(["A2", "07.02.2025", "Казань", "Спорт", "С1", "М", 1, 1000, None])
    sales.append(["A3", "когда-то", "Казань", "Спорт", "С1", "М", 1, 1000, None])
    targets = wb.create_sheet("Targets")
    targets.append(["region", "month", "target"])
    targets.append(["Москва", "2025-01", 100])
    targets.append(["Москва", "2025-02", 100])
    targets.append(["Казань", "2025-02", 2000])
    wb.save(path)


def test_synthetic_workbook_end_to_end(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    src = tmp_path / "in.xlsx"
    _write_workbook(src)
    out = tmp_path / "out"
    assert main(["--input", str(src), "--out", str(out)]) == 0
    data = json.loads((out / "summary.json").read_text(encoding="utf-8"))
    assert data["valid_rows"] == 2
    assert data["skipped_rows"] == 2
    assert data["total_revenue"] == 1180.9  # 2*100.5*0.9 + 1000
    assert data["revenue_by_region"] == {"Казань": 1000.0, "Москва": 180.9}
    assert data["plan_vs_fact"][1] == {
        "region": "Москва",
        "fact": 180.9,
        "target": 200.0,
        "completion_pct": 90.45,
    }
    stderr = capsys.readouterr().err
    assert "строка 4 пропущена" in stderr
    assert "строка 6 пропущена" in stderr


def test_missing_input_file(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    code = main(["--input", str(tmp_path / "nope.xlsx"), "--out", str(tmp_path / "o")])
    assert code == 2
    assert "не найден" in capsys.readouterr().err


def test_not_an_excel_file(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    bad = tmp_path / "bad.xlsx"
    bad.write_text("not excel", encoding="utf-8")
    assert main(["--input", str(bad), "--out", str(tmp_path / "o")]) == 2
    assert "Не удалось открыть" in capsys.readouterr().err


def test_missing_sheet(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    src = tmp_path / "in.xlsx"
    wb = Workbook()
    wb.active.title = "Other"
    wb.save(src)
    assert main(["--input", str(src), "--out", str(tmp_path / "o")]) == 2
    assert "нет листа 'Sales'" in capsys.readouterr().err


def test_wrong_header(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    src = tmp_path / "in.xlsx"
    wb = Workbook()
    ws = wb.active
    ws.title = "Sales"
    ws.append(["id", "date"])
    wb.create_sheet("Targets").append(["region", "month", "target"])
    wb.save(src)
    assert main(["--input", str(src), "--out", str(tmp_path / "o")]) == 2
    assert "ожидался заголовок" in capsys.readouterr().err
