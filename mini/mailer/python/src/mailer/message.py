"""Construction of RFC 5322 messages for a job firing."""

from __future__ import annotations

import mimetypes
from datetime import datetime
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import format_datetime, make_msgid, parseaddr

from mailer.config import Job
from mailer.templates import render, template_vars

__all__ = ["build_message"]


def build_message(job: Job, sender: str, slot: datetime, now: datetime) -> EmailMessage:
    """Build the email for ``job`` firing at ``slot`` (aware datetime).

    Subject/body placeholders are rendered in the job's time zone. Non-ASCII headers
    are RFC 2047 encoded by the ``email`` package when serialized with the SMTP policy.
    """
    slot_local = slot.astimezone(job.timezone)
    variables = template_vars(job.name, slot_local)

    msg = EmailMessage(policy=SMTP)
    msg["From"] = sender
    msg["To"] = ", ".join(job.to)
    if job.cc:
        msg["Cc"] = ", ".join(job.cc)
    msg["Subject"] = render(job.subject, variables)
    msg["Date"] = format_datetime(now)
    domain = parseaddr(sender)[1].rpartition("@")[2] or "localhost"
    msg["Message-ID"] = make_msgid(idstring=job.name, domain=domain)
    msg["X-Mailer-Job"] = job.name
    msg["X-Mailer-Slot"] = slot.isoformat(timespec="minutes")

    msg.set_content(
        render(job.body, variables),
        subtype="html" if job.html else "plain",
        charset="utf-8",
        # base64 keeps the message 7-bit clean even for servers without 8BITMIME.
        cte="base64",
    )
    for path in job.attachments:
        ctype, encoding = mimetypes.guess_type(path.name)
        if ctype is None or encoding is not None:
            ctype = "application/octet-stream"
        maintype, subtype = ctype.split("/", 1)
        msg.add_attachment(
            path.read_bytes(), maintype=maintype, subtype=subtype, filename=path.name
        )
    return msg
