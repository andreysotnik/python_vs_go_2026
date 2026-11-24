from __future__ import annotations

import json
import logging
import threading
from datetime import UTC, datetime, timedelta
from email.message import EmailMessage

import pytest

from mailer.scheduler import Scheduler, floor_minute, is_due
from mailer.smtp import PermanentSendError, Sender
from mailer.state import StateStore, slot_key


class RecordingSender(Sender):
    def __init__(self, fail: bool = False) -> None:
        self.sent: list[tuple[EmailMessage, list[str]]] = []
        self.fail = fail
        self.on_send = None

    def send(self, msg: EmailMessage, recipients: list[str]) -> None:
        if self.on_send:
            self.on_send()
        if self.fail:
            raise PermanentSendError("SMTP 554: rejected")
        self.sent.append((msg, recipients))


def at(text: str) -> datetime:
    return datetime.fromisoformat(text)


# --- cron + time zones -------------------------------------------------------


def test_is_due_in_job_timezone(make_job) -> None:
    job = make_job("0 9 * * 1-5", "Europe/Moscow")
    assert is_due(job, at("2026-11-19T09:00:00+03:00"))
    assert is_due(job, at("2026-11-19T06:00:00+00:00"))  # same instant in UTC
    assert is_due(job, at("2026-11-19T09:00:59+03:00"))  # any second within the minute
    assert not is_due(job, at("2026-11-19T09:00:00+00:00"))
    assert not is_due(job, at("2026-11-21T09:00:00+03:00"))  # Saturday


def test_weekday_evaluated_in_job_timezone(make_job) -> None:
    # Friday 23:30 UTC is already Saturday 08:30 in Tokyo.
    job = make_job("30 8 * * sat", "Asia/Tokyo")
    assert is_due(job, at("2026-11-20T23:30:00+00:00"))


def test_dst_spring_forward_skipped_time_never_fires(make_job) -> None:
    job = make_job("30 2 * * *", "Europe/Berlin")
    day = datetime(2026, 3, 29, 0, 0, tzinfo=UTC)  # 02:00-03:00 local does not exist
    hits = [m for m in range(24 * 60) if is_due(job, day + timedelta(minutes=m))]
    assert hits == []


def test_dst_fall_back_repeated_time_fires_once(make_job) -> None:
    job = make_job("30 2 * * *", "Europe/Berlin")
    day = datetime(2026, 10, 25, 0, 0, tzinfo=UTC)  # 02:00-03:00 local happens twice
    hits = [
        day + timedelta(minutes=m)
        for m in range(24 * 60)
        if is_due(job, day + timedelta(minutes=m))
    ]
    assert hits == [datetime(2026, 10, 25, 0, 30, tzinfo=UTC)]  # 02:30 CEST


def test_floor_minute_requires_aware() -> None:
    with pytest.raises(ValueError, match="aware"):
        floor_minute(datetime(2026, 1, 1))


# --- dedup / state -----------------------------------------------------------


def test_run_slot_sends_once_and_deduplicates(make_job, tmp_path) -> None:
    job = make_job()
    sender = RecordingSender()
    store = StateStore(tmp_path / "state")
    sched = Scheduler([job], sender, store, "bot@example.com")
    slot = at("2026-11-19T09:00:00+03:00")

    first = sched.run_slot(slot)
    assert first.sent == ["daily-report"] and first.ok
    msg, rcpts = sender.sent[0]
    assert rcpts == ["team@example.com"]
    assert msg["Subject"] == "Отчёт за 2026-11-19"

    # Same slot again, even expressed in another zone / with seconds, from a new process.
    sched2 = Scheduler([job], sender, StateStore(tmp_path / "state"), "bot@example.com")
    second = sched2.run_slot(at("2026-11-19T06:00:42+00:00"))
    assert second.sent == [] and second.duplicates == ["daily-report"]
    assert len(sender.sent) == 1

    marker = tmp_path / "state" / "sent" / "daily-report" / f"{slot_key(slot)}.json"
    data = json.loads(marker.read_text())
    assert data["slot"] == "2026-11-19T06:00:00+00:00"
    assert data["message_id"] == msg["Message-ID"]


def test_not_due_sends_nothing(make_job, tmp_path) -> None:
    sender = RecordingSender()
    sched = Scheduler([make_job()], sender, StateStore(tmp_path), "bot@example.com")
    result = sched.run_slot(at("2026-11-19T09:01:00+03:00"))
    assert result.sent == [] and result.ok and sender.sent == []


def test_failed_send_is_not_marked_and_can_be_retried(make_job, tmp_path) -> None:
    job = make_job()
    store = StateStore(tmp_path)
    slot = at("2026-11-19T09:00:00+03:00")
    failing = Scheduler([job], RecordingSender(fail=True), store, "bot@example.com")
    result = failing.run_slot(slot)
    assert result.failed == ["daily-report"] and not result.ok
    assert not store.is_sent("daily-report", slot)

    ok_sender = RecordingSender()
    assert Scheduler([job], ok_sender, store, "bot@example.com").run_slot(slot).sent == [
        "daily-report"
    ]


def test_state_key_is_utc_minute() -> None:
    assert slot_key(at("2026-11-19T09:00:00+03:00")) == "20261119T0600Z"
    with pytest.raises(ValueError):
        slot_key(datetime(2026, 1, 1))


def test_last_tick_roundtrip(tmp_path) -> None:
    store = StateStore(tmp_path)
    assert store.read_last_tick() is None
    store.write_last_tick(at("2026-11-19T09:00:00+03:00"))
    assert store.read_last_tick() == at("2026-11-19T06:00:00+00:00")
    (tmp_path / "last_tick").write_text("garbage")
    assert store.read_last_tick() is None


# --- graceful shutdown -------------------------------------------------------


def test_stop_during_send_finishes_current_and_skips_rest(make_job, tmp_path) -> None:
    job_a = make_job(name="a", schedule="* * * * *")
    job_b = make_job(name="b", schedule="* * * * *")
    stop = threading.Event()
    sender = RecordingSender()
    sender.on_send = stop.set  # SIGTERM arrives while the first email is in flight
    store = StateStore(tmp_path)
    sched = Scheduler([job_a, job_b], sender, store, "bot@example.com", stop_event=stop)
    slot = at("2026-11-19T09:00:00+03:00")
    result = sched.run_slot(slot)
    assert result.sent == ["a"]
    assert result.aborted == ["b"]
    assert store.is_sent("a", slot) and not store.is_sent("b", slot)


# --- daemon loop / missed slots ----------------------------------------------


class FakeClock:
    def __init__(self, start: datetime) -> None:
        self.now = start

    def __call__(self) -> datetime:
        return self.now


class ClockAdvancingEvent(threading.Event):
    """Event whose wait() advances a fake clock instead of sleeping."""

    def __init__(self, clock: FakeClock, stop_after: datetime, jump: dict | None = None) -> None:
        super().__init__()
        self.clock = clock
        self.stop_after = stop_after
        self.jump = jump or {}

    def wait(self, timeout: float | None = None) -> bool:
        assert timeout is not None and timeout > 0
        self.clock.now += timedelta(seconds=timeout)
        for when, to in list(self.jump.items()):
            if self.clock.now >= when:
                self.clock.now = to
                del self.jump[when]
        if self.clock.now >= self.stop_after:
            self.set()
        return self.is_set()


def test_run_forever_fires_each_minute(make_job, tmp_path) -> None:
    job = make_job(name="every-minute", schedule="* * * * *", timezone="UTC")
    clock = FakeClock(at("2026-11-19T09:00:30+00:00"))
    stop = ClockAdvancingEvent(clock, stop_after=at("2026-11-19T09:03:30+00:00"))
    sender = RecordingSender()
    store = StateStore(tmp_path)
    Scheduler([job], sender, store, "bot@example.com", clock=clock, stop_event=stop).run_forever()
    slots = [m["X-Mailer-Slot"] for m, _ in sender.sent]
    assert slots == ["2026-11-19T09:01+00:00", "2026-11-19T09:02+00:00", "2026-11-19T09:03+00:00"]
    assert store.read_last_tick() == at("2026-11-19T09:03:00+00:00")


def test_missed_slots_after_restart_are_logged_not_sent(make_job, tmp_path, caplog) -> None:
    job = make_job(name="every-5", schedule="*/5 * * * *", timezone="UTC")
    store = StateStore(tmp_path)
    store.write_last_tick(at("2026-11-19T08:00:00+00:00"))
    clock = FakeClock(at("2026-11-19T08:20:10+00:00"))
    stop = ClockAdvancingEvent(clock, stop_after=at("2026-11-19T08:21:30+00:00"))
    sender = RecordingSender()
    caplog.set_level(logging.WARNING, logger="mailer.scheduler")
    Scheduler([job], sender, store, "bot@example.com", clock=clock, stop_event=stop).run_forever()
    missed = [r.slot for r in caplog.records if r.getMessage().startswith("missed slot")]
    assert missed == [
        "2026-11-19T08:05:00+00:00",
        "2026-11-19T08:10:00+00:00",
        "2026-11-19T08:15:00+00:00",
        "2026-11-19T08:20:00+00:00",
    ]
    assert sender.sent == []  # 08:21 is not due for */5


def test_clock_jump_during_run_is_not_caught_up(make_job, tmp_path, caplog) -> None:
    job = make_job(name="every-minute", schedule="* * * * *", timezone="UTC")
    clock = FakeClock(at("2026-11-19T09:00:30+00:00"))
    # Host suspended: clock jumps from 09:01:xx to 09:05:10.
    stop = ClockAdvancingEvent(
        clock,
        stop_after=at("2026-11-19T09:05:30+00:00"),
        jump={at("2026-11-19T09:01:00+00:00"): at("2026-11-19T09:05:10+00:00")},
    )
    sender = RecordingSender()
    caplog.set_level(logging.WARNING, logger="mailer.scheduler")
    Scheduler(
        [job], sender, StateStore(tmp_path), "bot@example.com", clock=clock, stop_event=stop
    ).run_forever()
    assert [m["X-Mailer-Slot"] for m, _ in sender.sent] == ["2026-11-19T09:05+00:00"]
    missed = [r.slot for r in caplog.records if r.getMessage().startswith("missed slot")]
    assert missed == [f"2026-11-19T09:0{i}:00+00:00" for i in range(1, 5)]
