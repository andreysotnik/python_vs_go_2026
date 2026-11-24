from __future__ import annotations

import smtplib
import ssl
from email.message import EmailMessage

import pytest

from mailer.config import SmtpSettings
from mailer.smtp import PermanentSendError, SmtpSender, TransientSendError, classify

SETTINGS = SmtpSettings(host="h", port=25, sender="bot@example.com", tls="none")


class FakeClient:
    def __init__(self, outcome: BaseException | None) -> None:
        self.outcome = outcome
        self.quit_called = False
        self.login_args: tuple[str, str] | None = None

    def login(self, user: str, password: str) -> None:
        self.login_args = (user, password)

    def send_message(self, msg, from_addr=None, to_addrs=None):
        if self.outcome is not None:
            raise self.outcome
        return {}

    def quit(self) -> None:
        self.quit_called = True

    def close(self) -> None:
        pass


def make_sender(outcomes: list[BaseException | None], settings: SmtpSettings = SETTINGS):
    sleeps: list[float] = []
    clients: list[FakeClient] = []

    def connect(_settings):
        outcome = outcomes[len(clients)]
        if isinstance(outcome, OSError) and getattr(outcome, "_on_connect", False):
            clients.append(FakeClient(None))
            raise outcome
        client = FakeClient(outcome)
        clients.append(client)
        return client

    sender = SmtpSender(settings, sleep=sleeps.append, connect=connect)
    return sender, sleeps, clients


def msg() -> EmailMessage:
    m = EmailMessage()
    m["Subject"] = "x"
    m.set_content("x")
    return m


def test_success_first_try() -> None:
    sender, sleeps, clients = make_sender([None])
    sender.send(msg(), ["a@example.com"])
    assert sleeps == [] and len(clients) == 1 and clients[0].quit_called


def test_transient_then_success_with_exponential_backoff() -> None:
    sender, sleeps, clients = make_sender(
        [smtplib.SMTPServerDisconnected("gone"), smtplib.SMTPDataError(451, b"try later"), None]
    )
    sender.send(msg(), ["a@example.com"])
    assert sleeps == [1.0, 2.0]
    assert len(clients) == 3


def test_connection_refused_is_retried() -> None:
    err = ConnectionRefusedError(61, "refused")
    err._on_connect = True  # type: ignore[attr-defined]
    sender, sleeps, _ = make_sender([err, None])
    sender.send(msg(), ["a@example.com"])
    assert sleeps == [1.0]


def test_gives_up_after_three_attempts() -> None:
    sender, sleeps, clients = make_sender([TimeoutError("t")] * 3)
    with pytest.raises(TransientSendError):
        sender.send(msg(), ["a@example.com"])
    assert len(clients) == 3
    assert sleeps == [1.0, 2.0]


def test_permanent_5xx_not_retried() -> None:
    sender, sleeps, clients = make_sender([smtplib.SMTPDataError(554, b"rejected")])
    with pytest.raises(PermanentSendError, match="554"):
        sender.send(msg(), ["a@example.com"])
    assert sleeps == [] and len(clients) == 1


def test_login_used_when_user_configured() -> None:
    settings = SmtpSettings(
        host="h", port=25, sender="b@example.com", tls="none", user="u", password="p"
    )
    sender, _, clients = make_sender([None], settings)
    sender.send(msg(), ["a@example.com"])
    assert clients[0].login_args == ("u", "p")


@pytest.mark.parametrize(
    "exc, permanent",
    [
        (smtplib.SMTPRecipientsRefused({"a@x.y": (550, b"no such user")}), True),
        (smtplib.SMTPRecipientsRefused({"a@x.y": (450, b"busy"), "b@x.y": (550, b"no")}), False),
        (smtplib.SMTPSenderRefused(553, b"bad sender", "s@x.y"), True),
        (smtplib.SMTPAuthenticationError(535, b"auth failed"), True),
        (smtplib.SMTPAuthenticationError(454, b"temp auth failure"), False),
        (smtplib.SMTPConnectError(421, b"too busy"), False),
        (smtplib.SMTPNotSupportedError("STARTTLS"), True),
        (ssl.SSLCertVerificationError("bad cert"), True),
        (smtplib.SMTPServerDisconnected("x"), False),
        (OSError("network down"), False),
        (ValueError("bug"), True),
    ],
)
def test_classify(exc: BaseException, permanent: bool) -> None:
    assert isinstance(classify(exc), PermanentSendError) is permanent
