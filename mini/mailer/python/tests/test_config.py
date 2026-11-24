from __future__ import annotations

from pathlib import Path

import pytest

from mailer.config import ConfigError, SmtpSettings, load_config, validate_address


def test_valid_config_resolves_attachments_relative_to_config(write_config, tmp_path: Path) -> None:
    (tmp_path / "files").mkdir()
    (tmp_path / "files" / "report.pdf").write_bytes(b"%PDF")
    path = write_config(
        """
        jobs:
          - name: daily-report
            schedule: "0 9 * * 1-5"
            timezone: "Europe/Moscow"
            to: ["team@example.com", "Boss <boss@example.com>"]
            cc: ["cc@example.com"]
            subject: "Отчёт {{date}}"
            body: "x"
            attachments: ["./files/report.pdf"]
        """
    )
    [job] = load_config(path)
    assert job.attachments == ((tmp_path / "files" / "report.pdf").resolve(),)
    assert job.recipients == ["team@example.com", "boss@example.com", "cc@example.com"]
    assert job.html is False


def test_all_errors_reported(write_config) -> None:
    path = write_config(
        """
        jobs:
          - name: bad
            schedule: "61 * * * *"
            timezone: "Mars/Olympus"
            to: ["not-an-email", "ok@example.com"]
            cc: ["also bad"]
            subject: "Hi {{user}}"
            body: "x"
            html: "yes"
            attachments: ["missing.pdf"]
            extra: 1
          - name: bad
            schedule: "* * * * *"
            timezone: "UTC"
            to: []
            subject: "s"
            body: "b"
          - name: "with/slash"
        """
    )
    with pytest.raises(ConfigError) as info:
        load_config(path)
    text = "\n".join(info.value.errors)
    for needle in [
        "schedule: minute: value 61 out of range",
        "unknown time zone 'Mars/Olympus'",
        "to[0]: invalid email address 'not-an-email'",
        "cc[0]: invalid email address",
        "unknown placeholder(s) user",
        "html: must be true or false",
        "file not found",
        "unknown key 'extra'",
        "duplicate job name 'bad'",
        "at least one recipient is required",
        "missing required key 'schedule'",
        "name: must match",
    ]:
        assert needle in text, needle


@pytest.mark.parametrize(
    "content, needle",
    [
        ("jobs: [\n", "YAML syntax error"),
        ("foo: 1\n", "top-level 'jobs' list is required"),
        ("jobs: []\n", "non-empty list"),
        ("- 1\n", "top-level 'jobs'"),
    ],
)
def test_structural_errors(write_config, content: str, needle: str) -> None:
    with pytest.raises(ConfigError) as info:
        load_config(write_config(content))
    assert needle in info.value.errors[0]


def test_missing_file(tmp_path: Path) -> None:
    with pytest.raises(ConfigError) as info:
        load_config(tmp_path / "nope.yaml")
    assert "cannot read config" in info.value.errors[0]


@pytest.mark.parametrize(
    "addr, ok",
    [
        ("a@example.com", True),
        ("Имя <a.b+c@sub.example.org>", True),
        ("a@localhost", False),
        ("a@@example.com", False),
        ("a b@example.com", False),
        ("a@example.com\nBcc: x@y.z", False),
        ("", False),
        (42, False),
    ],
)
def test_validate_address(addr: object, ok: bool) -> None:
    assert (validate_address(addr) is None) is ok


def test_smtp_settings_defaults_and_errors() -> None:
    s = SmtpSettings.from_env(
        {"SMTP_HOST": "mail", "SMTP_FROM": "bot@example.com", "SMTP_TLS": "none"}
    )
    assert (s.port, s.tls, s.user, s.password) == (25, "none", None, None)
    s = SmtpSettings.from_env({"SMTP_HOST": "m", "SMTP_FROM": "b@example.com", "SMTP_TLS": "tls"})
    assert s.port == 465
    s = SmtpSettings.from_env(
        {"SMTP_HOST": "m", "SMTP_FROM": "b@example.com", "SMTP_PORT": "2525", "SMTP_USER": "u"}
    )
    assert (s.port, s.tls, s.user, s.password) == (2525, "starttls", "u", None)
    assert "secret" not in repr(
        SmtpSettings.from_env(
            {"SMTP_HOST": "m", "SMTP_FROM": "b@example.com", "SMTP_PASSWORD": "secret"}
        )
    )
    with pytest.raises(ConfigError) as info:
        SmtpSettings.from_env({"SMTP_TLS": "ssl", "SMTP_PORT": "x"})
    joined = " ".join(info.value.errors)
    assert "SMTP_HOST" in joined and "SMTP_TLS" in joined
    assert "SMTP_PORT" in joined and "SMTP_FROM" in joined
