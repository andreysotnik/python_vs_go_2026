from __future__ import annotations

from datetime import datetime
from zoneinfo import ZoneInfo

import pytest

from mailer.templates import TemplateError, find_unknown, render, template_vars


def test_render_all_placeholders() -> None:
    slot = datetime(2026, 11, 19, 9, 0, tzinfo=ZoneInfo("Europe/Moscow"))
    variables = template_vars("daily-report", slot)
    out = render("{{date}} | {{ datetime }} | {{job}}", variables)
    assert out == "2026-11-19 | 2026-11-19T09:00:00+03:00 | daily-report"


def test_date_uses_job_timezone() -> None:
    utc_slot = datetime.fromisoformat("2026-11-19T22:30:00+00:00")
    local = utc_slot.astimezone(ZoneInfo("Asia/Tokyo"))
    assert template_vars("j", local)["date"] == "2026-11-20"


def test_unknown_placeholder() -> None:
    assert find_unknown("Hi {{name}} {{date}} {{name}} {{ foo }}") == ["name", "foo"]
    with pytest.raises(TemplateError):
        render("{{nope}}", {"date": "x"})


def test_text_without_placeholders_and_braces_untouched() -> None:
    assert render("a {b} {{ }} c", {}) == "a {b} {{ }} c"
