from sales_report.formatting import format_millions, format_money, format_number, month_label

NBSP = " "


def test_format_number_thousands() -> None:
    assert format_number(1234567.4) == f"1{NBSP}234{NBSP}567"
    assert format_number(1234.5, 1) == f"1{NBSP}234,5"


def test_format_money_and_millions() -> None:
    assert format_money(1000) == f"1{NBSP}000,00 руб."
    assert format_millions(12_345_678) == "12,3 млн"


def test_month_label() -> None:
    assert month_label("2025-03") == "мар 2025"
