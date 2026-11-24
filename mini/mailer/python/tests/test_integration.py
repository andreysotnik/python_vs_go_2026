"""End-to-end tests: real CLI + real smtplib against an in-process fake SMTP server."""

from __future__ import annotations

import json
import logging
import os
import signal
import socket
import subprocess
import sys
import time
from email import message_from_bytes, policy
from pathlib import Path

import pytest
from aiosmtpd.controller import Controller

from mailer.cli import main

PDF = b"%PDF-1.4\n%\xe2\xe3\xcf\xd3 test attachment\n%%EOF\n"


class RecordingHandler:
    def __init__(self) -> None:
        self.envelopes: list = []
        self.replies: list[str] = []  # scripted failure replies, consumed per DATA

    async def handle_DATA(self, server, session, envelope):  # noqa: N802 - aiosmtpd hook name
        if self.replies:
            return self.replies.pop(0)
        self.envelopes.append(envelope)
        return "250 Message accepted for delivery"


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def smtp_server():
    handler = RecordingHandler()
    controller = Controller(handler, hostname="127.0.0.1", port=_free_port())
    controller.start()
    try:
        yield handler, controller.port
    finally:
        controller.stop()


@pytest.fixture
def project(tmp_path: Path, smtp_server, monkeypatch):
    handler, port = smtp_server
    (tmp_path / "files").mkdir()
    (tmp_path / "files" / "report.pdf").write_bytes(PDF)
    config = tmp_path / "jobs.yaml"
    config.write_text(
        """\
jobs:
  - name: daily-report
    schedule: "0 9 * * 1-5"
    timezone: "Europe/Moscow"
    to: ["team@example.com", "boss@example.com"]
    cc: ["cc@example.com"]
    subject: "Ежедневный отчёт за {{date}}"
    body: |
      Привет! Отчёт за {{date}} во вложении.
    html: false
    attachments: ["./files/report.pdf"]
  - name: hourly-other
    schedule: "15 * * * *"
    timezone: "UTC"
    to: ["other@example.com"]
    subject: "never in this test"
    body: "x"
""",
        encoding="utf-8",
    )
    env = {
        "SMTP_HOST": "127.0.0.1",
        "SMTP_PORT": str(port),
        "SMTP_FROM": "Рассылка <mailer@example.com>",
        "SMTP_TLS": "none",
        "STATE_DIR": str(tmp_path / "state"),
    }
    for key, value in env.items():
        monkeypatch.setenv(key, value)
    for key in ("SMTP_USER", "SMTP_PASSWORD"):
        monkeypatch.delenv(key, raising=False)
    old_handlers = {s: signal.getsignal(s) for s in (signal.SIGTERM, signal.SIGINT)}
    root = logging.getLogger()
    old_log_handlers = root.handlers[:]
    yield {"config": config, "handler": handler, "env": env, "tmp": tmp_path}
    for sig, h in old_handlers.items():
        signal.signal(sig, h)
    root.handlers[:] = old_log_handlers


def _wait_for(predicate, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.02)
    raise AssertionError("condition not met in time")


def test_once_delivers_mail_and_is_idempotent(project, capsys) -> None:
    handler = project["handler"]
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00+03:00"])
    assert rc == 0
    _wait_for(lambda: len(handler.envelopes) == 1)
    [env] = handler.envelopes

    assert env.mail_from == "mailer@example.com"
    assert sorted(env.rcpt_tos) == ["boss@example.com", "cc@example.com", "team@example.com"]

    raw: bytes = env.original_content
    subject_raw = next(line for line in raw.splitlines() if line.startswith(b"Subject:"))
    assert b"=?utf-8?" in subject_raw.lower(), "Cyrillic subject must be RFC 2047 encoded"

    msg = message_from_bytes(raw, policy=policy.default)
    assert msg["Subject"] == "Ежедневный отчёт за 2026-11-19"
    assert msg["To"] == "team@example.com, boss@example.com"
    assert msg["Cc"] == "cc@example.com"
    assert msg["Message-ID"] and msg["Date"]
    body = msg.get_body(("plain",))
    assert body.get_content_charset() == "utf-8"
    # base64 text parts carry canonical CRLF line endings (RFC 2045).
    assert body.get_content().replace("\r\n", "\n") == "Привет! Отчёт за 2026-11-19 во вложении.\n"
    [att] = list(msg.iter_attachments())
    assert att.get_filename() == "report.pdf"
    assert att.get_content_type() == "application/pdf"
    assert att.get_content() == PDF

    # Second run for the same minute (even with seconds / another offset): no duplicate.
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T06:00:30Z"])
    assert rc == 0
    time.sleep(0.2)
    assert len(handler.envelopes) == 1

    out = capsys.readouterr().out
    records = [json.loads(line) for line in out.splitlines() if line.strip()]
    assert any(r["msg"] == "sent" and r["job"] == "daily-report" for r in records)
    assert any(r["msg"] == "already sent for this slot, skipping" for r in records)


def test_once_nothing_due(project) -> None:
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-21T09:00:00+03:00"])
    assert rc == 0
    assert project["handler"].envelopes == []


def test_once_retries_transient_smtp_error(project, monkeypatch) -> None:
    monkeypatch.setattr("time.sleep", lambda _s: None)  # skip real backoff
    handler = project["handler"]
    handler.replies = ["451 4.3.0 Try again later"]
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00+03:00"])
    assert rc == 0
    assert len(handler.envelopes) == 1


def test_once_permanent_error_not_retried_and_not_marked(project, monkeypatch) -> None:
    sleeps: list[float] = []
    monkeypatch.setattr("time.sleep", sleeps.append)
    handler = project["handler"]
    handler.replies = ["554 5.6.0 Message rejected", "554 5.6.0 Message rejected"]
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00+03:00"])
    assert rc == 1
    assert sleeps == []
    assert handler.replies == ["554 5.6.0 Message rejected"]  # exactly one attempt consumed
    # Not marked as sent: a later manual re-run delivers it.
    handler.replies = []
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00+03:00"])
    assert rc == 0 and len(handler.envelopes) == 1


def test_validate_command(project, write_config, capsys) -> None:
    assert main(["validate", "--config", str(project["config"])]) == 0
    bad = write_config(
        """
        jobs:
          - name: x
            schedule: "* * *"
            timezone: "Nowhere/City"
            to: ["bad"]
            subject: "s"
            body: "b"
            attachments: ["nope.pdf"]
        """,
        name="bad.yaml",
    )
    assert main(["validate", "--config", str(bad)]) == 2
    err = capsys.readouterr().err
    assert "expected 5 fields" in err
    assert "unknown time zone 'Nowhere/City'" in err
    assert "invalid email address 'bad'" in err
    assert "file not found" in err


def test_once_rejects_naive_at(project) -> None:
    with pytest.raises(SystemExit) as info:
        main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00"])
    assert info.value.code == 2


def test_missing_smtp_env_is_config_error(project, monkeypatch) -> None:
    monkeypatch.delenv("SMTP_HOST")
    rc = main(["once", "--config", str(project["config"]), "--at", "2026-11-19T09:00:00+03:00"])
    assert rc == 2


def test_daemon_stops_gracefully_on_sigterm(project) -> None:
    env = {**os.environ, **project["env"], "PYTHONUNBUFFERED": "1"}
    proc = subprocess.Popen(  # noqa: S603
        [sys.executable, "-m", "mailer", "run", "--config", str(project["config"])],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        text=True,
    )
    try:
        assert proc.stdout is not None
        lines = []
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            line = proc.stdout.readline()
            if not line:
                break
            lines.append(json.loads(line))
            if lines[-1]["msg"] == "daemon started":
                break
        assert lines and lines[-1]["msg"] == "daemon started"
        proc.send_signal(signal.SIGTERM)
        out, _ = proc.communicate(timeout=10)
    finally:
        if proc.poll() is None:
            proc.kill()
    assert proc.returncode == 0
    msgs = [json.loads(line)["msg"] for line in out.splitlines() if line.strip()]
    assert "signal received, finishing current email" in msgs
    assert msgs[-1] == "daemon stopped"
