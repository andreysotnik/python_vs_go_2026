"""Loading and validation of ``jobs.yaml`` and SMTP settings from the environment."""

from __future__ import annotations

import os
import re
from collections.abc import Mapping
from dataclasses import dataclass, field
from email.utils import parseaddr
from pathlib import Path
from types import MappingProxyType
from typing import Any, Final
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import yaml

from mailer.cron import CronError, CronExpr
from mailer.templates import find_unknown

__all__ = [
    "ConfigError",
    "Job",
    "SmtpSettings",
    "TlsMode",
    "load_config",
    "validate_address",
]

_JOB_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
# Pragmatic address check: local@domain.tld, no whitespace / angle brackets.
_ADDR_RE = re.compile(
    r"^[^@\s<>()\[\],;:\"]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$"
)
_JOB_KEYS = {"name", "schedule", "timezone", "to", "cc", "subject", "body", "html", "attachments"}
_REQUIRED_KEYS = ("name", "schedule", "timezone", "to", "subject", "body")


class ConfigError(Exception):
    """Configuration is invalid; ``errors`` holds every human-readable problem found."""

    def __init__(self, errors: list[str]) -> None:
        super().__init__("; ".join(errors))
        self.errors = errors


@dataclass(frozen=True)
class Job:
    name: str
    schedule: CronExpr
    timezone: ZoneInfo
    to: tuple[str, ...]
    subject: str
    body: str
    cc: tuple[str, ...] = ()
    html: bool = False
    attachments: tuple[Path, ...] = field(default=())

    @property
    def recipients(self) -> list[str]:
        """Envelope recipients (bare addresses) for To and Cc."""
        return [parseaddr(a)[1] for a in (*self.to, *self.cc)]


def validate_address(value: object) -> str | None:
    """Return an error message for an invalid address, or None when it is valid.

    Accepts ``user@example.com`` and ``Display Name <user@example.com>``.
    """
    if not isinstance(value, str) or not value.strip():
        return "must be a non-empty string"
    if "\r" in value or "\n" in value:
        return "must not contain line breaks"
    _, addr = parseaddr(value)
    if not addr or not _ADDR_RE.match(addr):
        return f"invalid email address {value!r}"
    return None


def _check_address_list(
    value: Any, where: str, errors: list[str], *, required: bool
) -> tuple[str, ...]:
    if value is None:
        value = []
    if not isinstance(value, list):
        errors.append(f"{where}: must be a list of email addresses")
        return ()
    if required and not value:
        errors.append(f"{where}: at least one recipient is required")
    result: list[str] = []
    for i, item in enumerate(value):
        problem = validate_address(item)
        if problem:
            errors.append(f"{where}[{i}]: {problem}")
        else:
            result.append(item.strip())
    return tuple(result)


def _check_schedule(raw: Mapping[str, Any], where: str, errors: list[str]) -> CronExpr | None:
    if "schedule" not in raw:
        return None
    try:
        return CronExpr.parse(raw["schedule"])
    except CronError as exc:
        errors.append(f"{where}.schedule: {exc}")
        return None


def _check_timezone(raw: Mapping[str, Any], where: str, errors: list[str]) -> ZoneInfo | None:
    if "timezone" not in raw:
        return None
    tz_name = raw["timezone"]
    if not isinstance(tz_name, str) or not tz_name:
        errors.append(f"{where}.timezone: must be a non-empty string")
        return None
    try:
        return ZoneInfo(tz_name)
    except (ZoneInfoNotFoundError, ValueError):
        errors.append(f"{where}.timezone: unknown time zone {tz_name!r}")
        return None


def _check_texts(raw: Mapping[str, Any], where: str, errors: list[str]) -> dict[str, str]:
    texts: dict[str, str] = {}
    for key in ("subject", "body"):
        if key not in raw:
            continue
        value = raw[key]
        if not isinstance(value, str):
            errors.append(f"{where}.{key}: must be a string")
            continue
        unknown = find_unknown(value)
        if unknown:
            errors.append(
                f"{where}.{key}: unknown placeholder(s) {', '.join(unknown)} "
                "(allowed: date, datetime, job)"
            )
        texts[key] = value
    if "subject" in texts and ("\r" in texts["subject"] or "\n" in texts["subject"]):
        errors.append(f"{where}.subject: must be a single line")
    return texts


def _check_attachments(
    raw: Mapping[str, Any], where: str, base_dir: Path, errors: list[str]
) -> tuple[Path, ...]:
    raw_att = raw.get("attachments") or []
    if not isinstance(raw_att, list):
        errors.append(f"{where}.attachments: must be a list of file paths")
        return ()
    attachments: list[Path] = []
    for i, item in enumerate(raw_att):
        if not isinstance(item, str) or not item:
            errors.append(f"{where}.attachments[{i}]: must be a non-empty string")
            continue
        path = Path(item).expanduser()
        if not path.is_absolute():
            path = base_dir / path
        if not path.is_file():
            errors.append(f"{where}.attachments[{i}]: file not found: {path}")
        elif not os.access(path, os.R_OK):
            errors.append(f"{where}.attachments[{i}]: file is not readable: {path}")
        else:
            attachments.append(path.resolve())
    return tuple(attachments)


def _parse_job(raw: Any, index: int, base_dir: Path, errors: list[str]) -> Job | None:
    where = f"jobs[{index}]"
    if not isinstance(raw, Mapping):
        errors.append(f"{where}: must be a mapping")
        return None
    name = raw.get("name")
    if isinstance(name, str) and name:
        where = f"jobs[{index}] ({name})"
    start = len(errors)

    errors.extend(f"{where}: unknown key {key!r}" for key in sorted(set(raw) - _JOB_KEYS, key=str))
    errors.extend(f"{where}: missing required key {k!r}" for k in _REQUIRED_KEYS if k not in raw)
    if "name" in raw and (not isinstance(name, str) or not _JOB_NAME_RE.match(name)):
        errors.append(
            f"{where}.name: must match {_JOB_NAME_RE.pattern} (used as a state directory name)"
        )
    cron = _check_schedule(raw, where, errors)
    tz = _check_timezone(raw, where, errors)
    to = _check_address_list(raw.get("to"), f"{where}.to", errors, required="to" in raw)
    cc = _check_address_list(raw.get("cc"), f"{where}.cc", errors, required=False)
    texts = _check_texts(raw, where, errors)
    html = raw.get("html", False)
    if not isinstance(html, bool):
        errors.append(f"{where}.html: must be true or false")
    attachments = _check_attachments(raw, where, base_dir, errors)

    if len(errors) != start or cron is None or tz is None:
        return None
    return Job(
        name=name,
        schedule=cron,
        timezone=tz,
        to=to,
        cc=cc,
        subject=texts["subject"],
        body=texts["body"],
        html=html,
        attachments=attachments,
    )


def load_config(path: str | os.PathLike[str]) -> list[Job]:
    """Parse and fully validate a jobs file. Raises ConfigError listing all problems."""
    cfg_path = Path(path)
    try:
        text = cfg_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ConfigError([f"{cfg_path}: cannot read config: {exc.strerror or exc}"]) from exc
    except UnicodeDecodeError as exc:
        raise ConfigError([f"{cfg_path}: config is not valid UTF-8: {exc}"]) from exc
    try:
        data = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        raise ConfigError([f"{cfg_path}: YAML syntax error: {exc}"]) from exc

    if not isinstance(data, Mapping) or "jobs" not in data:
        raise ConfigError([f"{cfg_path}: top-level 'jobs' list is required"])
    raw_jobs = data["jobs"]
    if not isinstance(raw_jobs, list) or not raw_jobs:
        raise ConfigError([f"{cfg_path}: 'jobs' must be a non-empty list"])

    errors: list[str] = []
    for key in sorted(set(data) - {"jobs"}, key=str):
        errors.append(f"{cfg_path}: unknown top-level key {key!r}")
    base_dir = cfg_path.resolve().parent
    jobs: list[Job] = []
    seen: dict[str, int] = {}
    for index, raw in enumerate(raw_jobs):
        job = _parse_job(raw, index, base_dir, errors)
        if isinstance(raw, Mapping) and isinstance(raw.get("name"), str):
            name = raw["name"]
            if name in seen:
                errors.append(
                    f"jobs[{index}].name: duplicate job name {name!r} (see jobs[{seen[name]}])"
                )
            else:
                seen[name] = index
        if job is not None:
            jobs.append(job)
    if errors:
        raise ConfigError(errors)
    return jobs


class TlsMode:
    NONE: Final = "none"
    STARTTLS: Final = "starttls"
    TLS: Final = "tls"
    ALL: Final = (NONE, STARTTLS, TLS)
    DEFAULT_PORTS: Final[Mapping[str, int]] = MappingProxyType({NONE: 25, STARTTLS: 587, TLS: 465})


@dataclass(frozen=True)
class SmtpSettings:
    host: str
    port: int
    sender: str
    tls: str = TlsMode.STARTTLS
    user: str | None = None
    password: str | None = field(default=None, repr=False)
    timeout: float = 30.0

    @classmethod
    def from_env(cls, env: Mapping[str, str] | None = None) -> SmtpSettings:
        env = os.environ if env is None else env
        errors: list[str] = []
        host = env.get("SMTP_HOST", "").strip()
        if not host:
            errors.append("SMTP_HOST: required")
        tls = env.get("SMTP_TLS", TlsMode.STARTTLS).strip().lower() or TlsMode.STARTTLS
        if tls not in TlsMode.ALL:
            errors.append(f"SMTP_TLS: must be one of {', '.join(TlsMode.ALL)}, got {tls!r}")
            tls = TlsMode.STARTTLS
        port_raw = env.get("SMTP_PORT", "").strip()
        port = TlsMode.DEFAULT_PORTS[tls]
        if port_raw:
            if not port_raw.isdigit() or not 0 < int(port_raw) < 65536:
                errors.append(f"SMTP_PORT: must be an integer 1-65535, got {port_raw!r}")
            else:
                port = int(port_raw)
        sender = env.get("SMTP_FROM", "").strip()
        problem = validate_address(sender) if sender else "required"
        if problem:
            errors.append(f"SMTP_FROM: {problem}")
        timeout = 30.0
        timeout_raw = env.get("SMTP_TIMEOUT", "").strip()
        if timeout_raw:
            try:
                timeout = float(timeout_raw)
                if timeout <= 0:
                    raise ValueError
            except ValueError:
                errors.append(f"SMTP_TIMEOUT: must be a positive number, got {timeout_raw!r}")
        if errors:
            raise ConfigError(errors)
        return cls(
            host=host,
            port=port,
            sender=sender,
            tls=tls,
            user=env.get("SMTP_USER") or None,
            password=env.get("SMTP_PASSWORD") or None,
            timeout=timeout,
        )
