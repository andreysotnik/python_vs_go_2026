from __future__ import annotations

from datetime import UTC, datetime

import pytest

from mailer.cron import CronError, CronExpr


def dt(*args: int) -> datetime:
    return datetime(*args)


def test_weekday_morning() -> None:
    expr = CronExpr.parse("0 9 * * 1-5")
    assert expr.matches(dt(2026, 11, 19, 9, 0))  # Thursday
    assert not expr.matches(dt(2026, 11, 19, 9, 1))
    assert not expr.matches(dt(2026, 11, 22, 9, 0))  # Sunday
    assert not expr.matches(dt(2026, 11, 21, 9, 0))  # Saturday


def test_steps_lists_and_ranges() -> None:
    expr = CronExpr.parse("*/15 8-10,22 * * *")
    assert expr.minutes == frozenset({0, 15, 30, 45})
    assert expr.hours == frozenset({8, 9, 10, 22})
    expr = CronExpr.parse("5/20 0 1 * *")
    assert expr.minutes == frozenset({5, 25, 45})
    expr = CronExpr.parse("0-30/10 0 1 * *")
    assert expr.minutes == frozenset({0, 10, 20, 30})


def test_names_and_sunday_alias() -> None:
    expr = CronExpr.parse("0 0 * JAN,dec sun")
    assert expr.months == frozenset({1, 12})
    assert expr.days_of_week == frozenset({0})
    assert CronExpr.parse("0 0 * * 7").days_of_week == frozenset({0})
    assert CronExpr.parse("0 0 * * 7").matches(dt(2026, 11, 22, 0, 0))  # Sunday


def test_dom_dow_or_semantics() -> None:
    expr = CronExpr.parse("0 12 1 * mon")
    assert expr.matches(dt(2026, 12, 1, 12, 0))  # 1st (Tuesday)
    assert expr.matches(dt(2026, 11, 23, 12, 0))  # Monday
    assert not expr.matches(dt(2026, 11, 24, 12, 0))
    # Only DOM restricted -> AND with "*" DOW.
    expr = CronExpr.parse("0 12 1 * *")
    assert not expr.matches(dt(2026, 11, 23, 12, 0))


def test_matches_ignores_tzinfo_and_uses_wall_clock() -> None:
    expr = CronExpr.parse("0 9 * * *")
    assert expr.matches(datetime(2026, 11, 19, 9, 0, tzinfo=UTC))


@pytest.mark.parametrize(
    "bad",
    [
        "",
        "* * * *",
        "* * * * * *",
        "60 * * * *",
        "* 24 * * *",
        "* * 0 * *",
        "* * * 13 *",
        "* * * * 8",
        "*/0 * * * *",
        "5-1 * * * *",
        "a * * * *",
        "1,,2 * * * *",
        "* * * foo *",
        "-1 * * * *",
    ],
)
def test_invalid_expressions(bad: str) -> None:
    with pytest.raises(CronError):
        CronExpr.parse(bad)
