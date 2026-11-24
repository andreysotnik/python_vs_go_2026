"""Scheduling logic: which jobs are due, one-shot slot processing, and the daemon loop."""

from __future__ import annotations

import logging
import threading
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta

from mailer.config import Job
from mailer.message import build_message
from mailer.smtp import Sender, SendError
from mailer.state import StateStore

__all__ = ["Scheduler", "SlotResult", "floor_minute", "is_due", "utc_now"]

log = logging.getLogger("mailer.scheduler")
MINUTE = timedelta(minutes=1)
# Upper bound for enumerating missed slots after downtime (keeps startup cheap).
MAX_MISSED_SCAN = timedelta(days=7)


def utc_now() -> datetime:
    return datetime.now(UTC)


def floor_minute(dt: datetime) -> datetime:
    if dt.tzinfo is None:
        raise ValueError("datetime must be timezone-aware")
    return dt.astimezone(UTC).replace(second=0, microsecond=0)


def is_due(job: Job, slot: datetime) -> bool:
    """True if ``job`` fires in the minute ``slot`` (aware datetime, any zone).

    The cron expression is evaluated against wall-clock time in the job's time zone.
    DST: local times skipped by a spring-forward transition never fire; during a
    fall-back transition the repeated wall-clock minute fires only once (first pass).
    """
    local = floor_minute(slot).astimezone(job.timezone)
    if local.fold == 1 and local.replace(fold=0).utcoffset() != local.utcoffset():
        return False
    return job.schedule.matches(local)


@dataclass
class SlotResult:
    sent: list[str] = field(default_factory=list)
    duplicates: list[str] = field(default_factory=list)
    failed: list[str] = field(default_factory=list)
    aborted: list[str] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.failed and not self.aborted


class Scheduler:
    def __init__(
        self,
        jobs: Sequence[Job],
        sender: Sender,
        store: StateStore,
        from_addr: str,
        *,
        clock: Callable[[], datetime] = utc_now,
        stop_event: threading.Event | None = None,
    ) -> None:
        self.jobs = list(jobs)
        self.sender = sender
        self.store = store
        self.from_addr = from_addr
        self.clock = clock
        self.stop_event = stop_event or threading.Event()

    def due_jobs(self, slot: datetime) -> list[Job]:
        return [job for job in self.jobs if is_due(job, slot)]

    def run_slot(self, slot: datetime) -> SlotResult:
        """Send every job due in ``slot`` that has not been sent for it yet."""
        slot = floor_minute(slot)
        result = SlotResult()
        for job in self.due_jobs(slot):
            ctx = {"job": job.name, "slot": slot.isoformat()}
            if self.stop_event.is_set():
                log.warning("shutdown requested, job not sent", extra=ctx)
                result.aborted.append(job.name)
                continue
            with self.store.lock(job.name):
                if self.store.is_sent(job.name, slot):
                    log.info("already sent for this slot, skipping", extra=ctx)
                    result.duplicates.append(job.name)
                    continue
                try:
                    msg = build_message(job, self.from_addr, slot, self.clock())
                    log.info(
                        "sending",
                        extra={
                            **ctx,
                            "recipients": job.recipients,
                            "message_id": msg["Message-ID"],
                        },
                    )
                    self.sender.send(msg, job.recipients)
                except (SendError, OSError) as exc:
                    log.error("job failed", extra={**ctx, "error": str(exc)})
                    result.failed.append(job.name)
                    continue
                self.store.mark_sent(job.name, slot, msg["Message-ID"], self.clock())
                log.info("sent", extra={**ctx, "message_id": msg["Message-ID"]})
                result.sent.append(job.name)
        return result

    def log_missed(self, start: datetime, end: datetime) -> int:
        """Log (never send) due slots in ``[start, end)`` that were not delivered."""
        start, end = floor_minute(start), floor_minute(end)
        if end <= start:
            return 0
        if end - start > MAX_MISSED_SCAN:
            log.warning(
                "downtime longer than scan window; older missed slots are not enumerated",
                extra={"from": start.isoformat(), "to": end.isoformat()},
            )
            start = end - MAX_MISSED_SCAN
        count = 0
        slot = start
        while slot < end:
            for job in self.due_jobs(slot):
                if not self.store.is_sent(job.name, slot):
                    count += 1
                    log.warning(
                        "missed slot during downtime, not sending",
                        extra={"job": job.name, "slot": slot.isoformat()},
                    )
            slot += MINUTE
        return count

    def _sleep_until(self, target: datetime) -> bool:
        """Sleep until wall clock reaches ``target``. Returns False if stop requested."""
        while True:
            if self.stop_event.is_set():
                return False
            remaining = (target - self.clock()).total_seconds()
            if remaining <= 0:
                return True
            # Wake periodically so wall-clock jumps are noticed.
            self.stop_event.wait(min(remaining, 30.0))

    def run_forever(self) -> None:
        now = self.clock()
        next_slot = floor_minute(now) + MINUTE
        last = self.store.read_last_tick()
        if last is not None:
            self.log_missed(floor_minute(last) + MINUTE, next_slot)
        log.info(
            "daemon started",
            extra={"jobs": [j.name for j in self.jobs], "next_slot": next_slot.isoformat()},
        )
        while self._sleep_until(next_slot):
            current = floor_minute(self.clock())
            if current > next_slot:
                # Overslept (suspend, clock jump, long send): don't catch up, just log.
                self.log_missed(next_slot, current)
                next_slot = current
            self.run_slot(next_slot)
            self.store.write_last_tick(next_slot)
            next_slot += MINUTE
        log.info("daemon stopped")
