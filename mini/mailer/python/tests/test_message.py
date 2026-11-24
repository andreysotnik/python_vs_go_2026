from __future__ import annotations

from datetime import UTC, datetime
from email import message_from_bytes, policy
from email.header import decode_header, make_header

from mailer.config import load_config
from mailer.message import build_message


def test_message_headers_and_encoding(write_config, tmp_path) -> None:
    (tmp_path / "отчёт.pdf").write_bytes(b"%PDF-1.4 data")
    path = write_config(
        """
        jobs:
          - name: daily-report
            schedule: "0 9 * * *"
            timezone: "Europe/Moscow"
            to: ["team@example.com", "Начальник <boss@example.com>"]
            cc: ["cc@example.com"]
            subject: "Ежедневный отчёт за {{date}}"
            body: "<p>Привет, {{job}}!</p>"
            html: true
            attachments: ["отчёт.pdf"]
        """
    )
    [job] = load_config(path)
    slot = datetime.fromisoformat("2026-11-19T06:00:00+00:00")
    msg = build_message(
        job, "Bot <bot@example.com>", slot, datetime(2026, 11, 19, 6, 0, 1, tzinfo=UTC)
    )
    raw = msg.as_bytes()
    raw.decode("ascii")  # everything on the wire is 7-bit safe

    subject_line = next(line for line in raw.splitlines() if line.startswith(b"Subject:"))
    assert b"=?utf-8?" in subject_line.lower()
    parsed = message_from_bytes(raw, policy=policy.default)
    assert str(make_header(decode_header(parsed["Subject"]))) == "Ежедневный отчёт за 2026-11-19"
    assert parsed["Message-ID"].startswith("<") and parsed["Message-ID"].endswith("@example.com>")
    assert parsed["Date"] == "Thu, 19 Nov 2026 06:00:01 +0000"
    assert parsed["Cc"] == "cc@example.com"
    body = parsed.get_body(("html",))
    assert body.get_content_charset() == "utf-8"
    assert body.get_content().strip() == "<p>Привет, daily-report!</p>"
    [att] = list(parsed.iter_attachments())
    assert att.get_filename() == "отчёт.pdf"
    assert att.get_content_type() == "application/pdf"
    assert att.get_content() == b"%PDF-1.4 data"
