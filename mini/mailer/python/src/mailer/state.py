"""Durable "job X was sent for slot T" state kept in ``STATE_DIR``.

Layout::

    STATE_DIR/
      sent/<job>/<YYYYMMDDTHHMMZ>.json   one marker per delivered (job, slot)
      locks/<job>.lock                    flock() serializing check→send→mark per job
      last_tick                           last slot processed by the daemon (UTC ISO)

Markers are written atomically (temp file + fsync + rename + directory fsync), so a
crash never leaves a half-written marker. Delivery is at-least-once only in the narrow
window between a successful SMTP DATA and the marker rename.
"""

from __future__ import annotations

import contextlib
import fcntl
import json
import os
import tempfile
from collections.abc import Iterator
from datetime import UTC, datetime
from pathlib import Path

__all__ = ["StateStore", "slot_key"]


def slot_key(slot: datetime) -> str:
    """Canonical, filesystem-safe key for a minute slot (always UTC)."""
    if slot.tzinfo is None:
        raise ValueError("slot must be timezone-aware")
    return slot.astimezone(UTC).strftime("%Y%m%dT%H%MZ")


def _fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(fd, "wb") as fh:
            fh.write(data)
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(tmp)
        raise
    _fsync_dir(path.parent)


class StateStore:
    def __init__(self, root: str | os.PathLike[str]) -> None:
        self.root = Path(root)
        self._sent = self.root / "sent"
        self._locks = self.root / "locks"
        self._sent.mkdir(parents=True, exist_ok=True)
        self._locks.mkdir(parents=True, exist_ok=True)
        probe = self.root / ".write-probe"
        try:
            probe.write_bytes(b"")
            probe.unlink()
        except OSError as exc:
            raise OSError(f"STATE_DIR {self.root} is not writable: {exc}") from exc

    def _marker(self, job: str, slot: datetime) -> Path:
        return self._sent / job / f"{slot_key(slot)}.json"

    def is_sent(self, job: str, slot: datetime) -> bool:
        return self._marker(job, slot).exists()

    def mark_sent(self, job: str, slot: datetime, message_id: str, sent_at: datetime) -> None:
        payload = {
            "job": job,
            "slot": slot.astimezone(UTC).isoformat(),
            "message_id": message_id,
            "sent_at": sent_at.astimezone(UTC).isoformat(),
        }
        _atomic_write(self._marker(job, slot), json.dumps(payload).encode("utf-8"))

    @contextlib.contextmanager
    def lock(self, job: str) -> Iterator[None]:
        """Exclusive cross-process lock for one job (blocks until acquired)."""
        with open(self._locks / f"{job}.lock", "a+b") as fh:
            fcntl.flock(fh.fileno(), fcntl.LOCK_EX)
            try:
                yield
            finally:
                fcntl.flock(fh.fileno(), fcntl.LOCK_UN)

    def read_last_tick(self) -> datetime | None:
        path = self.root / "last_tick"
        try:
            text = path.read_text(encoding="utf-8").strip()
        except FileNotFoundError:
            return None
        try:
            value = datetime.fromisoformat(text)
        except ValueError:
            return None
        return value if value.tzinfo else None

    def write_last_tick(self, slot: datetime) -> None:
        _atomic_write(self.root / "last_tick", slot.astimezone(UTC).isoformat().encode("utf-8"))
