"""Command-line interface: ``validate``, ``once`` and ``run``."""

from __future__ import annotations

import argparse
import logging
import os
import signal
import sys
import threading
from collections.abc import Sequence
from datetime import datetime
from types import FrameType

from mailer import __version__
from mailer.config import ConfigError, Job, SmtpSettings, load_config
from mailer.logs import setup_logging
from mailer.scheduler import Scheduler, floor_minute
from mailer.smtp import SmtpSender
from mailer.state import StateStore

__all__ = ["main"]

EXIT_OK = 0
EXIT_SEND_FAILED = 1
EXIT_CONFIG = 2

log = logging.getLogger("mailer")


def _parse_at(value: str) -> datetime:
    try:
        dt = datetime.fromisoformat(value)
    except ValueError:
        raise argparse.ArgumentTypeError(
            f"invalid ISO 8601 datetime {value!r} (example: 2026-11-19T09:00:00+03:00)"
        ) from None
    if dt.tzinfo is None:
        raise argparse.ArgumentTypeError(
            f"datetime {value!r} must include a UTC offset (example: 2026-11-19T09:00:00+03:00)"
        )
    return dt


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="mailer", description="Scheduled email sender.")
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    sub = parser.add_subparsers(dest="command", required=True)

    p_validate = sub.add_parser("validate", help="validate the jobs config and exit")
    p_validate.add_argument("--config", required=True, help="path to jobs.yaml")

    p_once = sub.add_parser("once", help="send jobs due in the given minute and exit")
    p_once.add_argument("--config", required=True, help="path to jobs.yaml")
    p_once.add_argument(
        "--at",
        required=True,
        type=_parse_at,
        help="ISO 8601 time with offset, e.g. 2026-11-19T09:00:00+03:00",
    )

    p_run = sub.add_parser("run", help="run the scheduler daemon")
    p_run.add_argument("--config", required=True, help="path to jobs.yaml")
    return parser


def _load_jobs(path: str) -> list[Job] | None:
    try:
        return load_config(path)
    except ConfigError as exc:
        print(f"config {path} is invalid:", file=sys.stderr)
        for err in exc.errors:
            print(f"  - {err}", file=sys.stderr)
        log.error("invalid config", extra={"config": path, "errors": exc.errors})
        return None


def _make_scheduler(jobs: list[Job], stop: threading.Event) -> Scheduler | None:
    try:
        smtp = SmtpSettings.from_env()
    except ConfigError as exc:
        print("SMTP settings are invalid:", file=sys.stderr)
        for err in exc.errors:
            print(f"  - {err}", file=sys.stderr)
        log.error("invalid smtp settings", extra={"errors": exc.errors})
        return None
    state_dir = os.environ.get("STATE_DIR") or "./state"
    try:
        store = StateStore(state_dir)
    except OSError as exc:
        print(f"STATE_DIR error: {exc}", file=sys.stderr)
        log.error("state dir unusable", extra={"state_dir": state_dir, "error": str(exc)})
        return None
    log.info(
        "configured",
        extra={
            "smtp_host": smtp.host,
            "smtp_port": smtp.port,
            "smtp_tls": smtp.tls,
            "state_dir": str(store.root),
        },
    )
    return Scheduler(jobs, SmtpSender(smtp), store, smtp.sender, stop_event=stop)


def _install_signal_handlers(stop: threading.Event) -> None:
    def _handler(signum: int, _frame: FrameType | None) -> None:
        log.info(
            "signal received, finishing current email",
            extra={"signal": signal.Signals(signum).name},
        )
        stop.set()

    signal.signal(signal.SIGTERM, _handler)
    signal.signal(signal.SIGINT, _handler)


def main(argv: Sequence[str] | None = None) -> int:
    setup_logging(os.environ.get("LOG_LEVEL", "INFO"))
    args = _build_parser().parse_args(argv)

    jobs = _load_jobs(args.config)
    if jobs is None:
        return EXIT_CONFIG

    if args.command == "validate":
        print(f"config {args.config} is valid: {len(jobs)} job(s)", file=sys.stderr)
        log.info("config valid", extra={"config": args.config, "jobs": [j.name for j in jobs]})
        return EXIT_OK

    stop = threading.Event()
    _install_signal_handlers(stop)
    scheduler = _make_scheduler(jobs, stop)
    if scheduler is None:
        return EXIT_CONFIG

    if args.command == "once":
        slot = floor_minute(args.at)
        result = scheduler.run_slot(slot)
        log.info(
            "once finished",
            extra={
                "slot": slot.isoformat(),
                "sent": result.sent,
                "duplicates": result.duplicates,
                "failed": result.failed,
                "aborted": result.aborted,
            },
        )
        return EXIT_OK if result.ok else EXIT_SEND_FAILED

    scheduler.run_forever()
    return EXIT_OK


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
