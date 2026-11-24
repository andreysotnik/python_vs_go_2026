"""SMTP delivery with retry/backoff for transient failures."""

from __future__ import annotations

import logging
import smtplib
import ssl
import time
from collections.abc import Callable
from email.message import EmailMessage

from mailer.config import SmtpSettings, TlsMode

__all__ = [
    "PermanentSendError",
    "SendError",
    "Sender",
    "SmtpSender",
    "TransientSendError",
    "classify",
]

log = logging.getLogger("mailer.smtp")


class SendError(Exception):
    """Delivery failed."""


class TransientSendError(SendError):
    """Delivery failed but may succeed on retry (network problems, 4xx replies)."""


class PermanentSendError(SendError):
    """Delivery failed permanently (5xx replies, TLS/protocol misconfiguration)."""


def classify(exc: BaseException) -> SendError:  # noqa: PLR0911 - flat dispatch table
    """Map an exception raised during an SMTP session to Transient/Permanent."""
    if isinstance(exc, SendError):
        return exc
    if isinstance(exc, smtplib.SMTPRecipientsRefused):
        codes = [code for code, _ in exc.recipients.values()]
        if codes and all(500 <= c < 600 for c in codes):
            return PermanentSendError(f"all recipients refused: {exc.recipients}")
        return TransientSendError(f"recipients refused: {exc.recipients}")
    if isinstance(exc, smtplib.SMTPResponseException):
        text = exc.smtp_error
        if isinstance(text, bytes):
            text = text.decode("utf-8", "replace")
        message = f"SMTP {exc.smtp_code}: {text}"
        if 500 <= exc.smtp_code < 600:
            return PermanentSendError(message)
        return TransientSendError(message)
    if isinstance(exc, smtplib.SMTPNotSupportedError):
        return PermanentSendError(f"server does not support required feature: {exc}")
    if isinstance(exc, ssl.SSLCertVerificationError):
        return PermanentSendError(f"TLS certificate verification failed: {exc}")
    if isinstance(exc, smtplib.SMTPServerDisconnected | OSError | smtplib.SMTPException):
        return TransientSendError(f"{type(exc).__name__}: {exc}")
    return PermanentSendError(f"{type(exc).__name__}: {exc}")


class Sender:
    """Interface: deliver one message or raise SendError."""

    def send(self, msg: EmailMessage, recipients: list[str]) -> None:  # pragma: no cover
        raise NotImplementedError


class SmtpSender(Sender):
    def __init__(
        self,
        settings: SmtpSettings,
        *,
        attempts: int = 3,
        base_delay: float = 1.0,
        sleep: Callable[[float], None] = time.sleep,
        connect: Callable[[SmtpSettings], smtplib.SMTP] | None = None,
    ) -> None:
        if attempts < 1:
            raise ValueError("attempts must be >= 1")
        self.settings = settings
        self.attempts = attempts
        self.base_delay = base_delay
        self._sleep = sleep
        self._connect = connect or _connect

    def send(self, msg: EmailMessage, recipients: list[str]) -> None:
        for attempt in range(1, self.attempts + 1):
            try:
                self._send_once(msg, recipients)
                return
            except Exception as exc:
                err = classify(exc)
                if isinstance(err, PermanentSendError) or attempt == self.attempts:
                    log.error(
                        "smtp send failed",
                        extra={
                            "attempt": attempt,
                            "permanent": isinstance(err, PermanentSendError),
                            "error": str(err),
                        },
                    )
                    raise err from exc
                delay = self.base_delay * (2 ** (attempt - 1))
                log.warning(
                    "smtp transient failure, will retry",
                    extra={"attempt": attempt, "retry_in_s": delay, "error": str(err)},
                )
                self._sleep(delay)

    def _send_once(self, msg: EmailMessage, recipients: list[str]) -> None:
        client = self._connect(self.settings)
        try:
            if self.settings.user:
                client.login(self.settings.user, self.settings.password or "")
            refused = client.send_message(msg, from_addr=None, to_addrs=recipients)
            if refused:
                log.warning("some recipients were refused", extra={"refused": str(refused)})
        finally:
            try:
                client.quit()
            except (smtplib.SMTPException, OSError):
                client.close()


def _connect(settings: SmtpSettings) -> smtplib.SMTP:
    context = ssl.create_default_context()
    if settings.tls == TlsMode.TLS:
        client: smtplib.SMTP = smtplib.SMTP_SSL(
            settings.host, settings.port, timeout=settings.timeout, context=context
        )
    else:
        client = smtplib.SMTP(settings.host, settings.port, timeout=settings.timeout)
    try:
        client.ehlo()
        if settings.tls == TlsMode.STARTTLS:
            client.starttls(context=context)
            client.ehlo()
    except BaseException:
        client.close()
        raise
    return client
