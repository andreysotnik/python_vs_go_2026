from __future__ import annotations

import textwrap
from pathlib import Path

import pytest

from mailer.config import Job, load_config


@pytest.fixture
def write_config(tmp_path: Path):
    def _write(yaml_text: str, name: str = "jobs.yaml") -> Path:
        path = tmp_path / name
        path.write_text(textwrap.dedent(yaml_text), encoding="utf-8")
        return path

    return _write


@pytest.fixture
def make_job(write_config):
    def _make(
        schedule: str = "0 9 * * 1-5",
        timezone: str = "Europe/Moscow",
        name: str = "daily-report",
        subject: str = "Отчёт за {{date}}",
        body: str = "Тело {{job}} {{datetime}}",
    ) -> Job:
        path = write_config(
            f"""
            jobs:
              - name: {name}
                schedule: "{schedule}"
                timezone: "{timezone}"
                to: ["team@example.com"]
                subject: "{subject}"
                body: "{body}"
            """,
            name=f"{name}.yaml",
        )
        return load_config(path)[0]

    return _make
