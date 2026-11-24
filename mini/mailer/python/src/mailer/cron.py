"""Minimal, strict 5-field cron expression parser and matcher.

Supported syntax per field: ``*``, ``N``, ``A-B``, ``*/S``, ``A-B/S``, ``A/S`` and
comma-separated lists of those. Month (``jan``..``dec``) and day-of-week
(``sun``..``sat``) names are accepted case-insensitively. Day-of-week ``7`` is an
alias for Sunday (``0``).

Semantics follow Vixie cron: if both day-of-month and day-of-week are restricted
(i.e. do not start with ``*``), a date matches when *either* of them matches.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

__all__ = ["CronError", "CronExpr"]

_MONTH_NAMES = {
    name: i
    for i, name in enumerate(
        ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"],
        start=1,
    )
}
_DOW_NAMES = {name: i for i, name in enumerate(["sun", "mon", "tue", "wed", "thu", "fri", "sat"])}


class CronError(ValueError):
    """Raised when a cron expression cannot be parsed."""


@dataclass(frozen=True)
class _FieldSpec:
    name: str
    low: int
    high: int
    names: dict[str, int] | None = None


_FIELDS = (
    _FieldSpec("minute", 0, 59),
    _FieldSpec("hour", 0, 23),
    _FieldSpec("day-of-month", 1, 31),
    _FieldSpec("month", 1, 12, _MONTH_NAMES),
    _FieldSpec("day-of-week", 0, 7, _DOW_NAMES),
)


def _parse_value(token: str, spec: _FieldSpec) -> int:
    lowered = token.lower()
    if spec.names and lowered in spec.names:
        return spec.names[lowered]
    if not token.isdigit():
        raise CronError(f"{spec.name}: invalid value {token!r}")
    value = int(token)
    if not spec.low <= value <= spec.high:
        raise CronError(f"{spec.name}: value {value} out of range {spec.low}-{spec.high}")
    return value


def _parse_field(text: str, spec: _FieldSpec) -> frozenset[int]:
    if not text:
        raise CronError(f"{spec.name}: empty field")
    values: set[int] = set()
    for part in text.split(","):
        if not part:
            raise CronError(f"{spec.name}: empty list element in {text!r}")
        range_part, sep, step_part = part.partition("/")
        step = 1
        if sep:
            if not step_part.isdigit() or int(step_part) == 0:
                raise CronError(f"{spec.name}: invalid step {step_part!r}")
            step = int(step_part)
        if range_part == "*":
            low, high = spec.low, spec.high
        elif "-" in range_part:
            a, _, b = range_part.partition("-")
            low, high = _parse_value(a, spec), _parse_value(b, spec)
            if low > high:
                raise CronError(f"{spec.name}: range {range_part!r} is reversed")
        else:
            low = _parse_value(range_part, spec)
            # "A/S" means "from A to the field maximum with step S".
            high = spec.high if sep else low
        values.update(range(low, high + 1, step))
    if spec.name == "day-of-week" and 7 in values:
        values.discard(7)
        values.add(0)
    return frozenset(values)


@dataclass(frozen=True)
class CronExpr:
    """A parsed cron expression."""

    source: str
    minutes: frozenset[int]
    hours: frozenset[int]
    days_of_month: frozenset[int]
    months: frozenset[int]
    days_of_week: frozenset[int]
    dom_restricted: bool
    dow_restricted: bool

    @classmethod
    def parse(cls, expr: str) -> CronExpr:
        if not isinstance(expr, str):
            raise CronError("cron expression must be a string")
        parts = expr.split()
        if len(parts) != 5:
            raise CronError(f"expected 5 fields, got {len(parts)} in {expr!r}")
        parsed = [_parse_field(p, s) for p, s in zip(parts, _FIELDS, strict=True)]
        return cls(
            source=expr,
            minutes=parsed[0],
            hours=parsed[1],
            days_of_month=parsed[2],
            months=parsed[3],
            days_of_week=parsed[4],
            dom_restricted=not parts[2].startswith("*"),
            dow_restricted=not parts[4].startswith("*"),
        )

    def matches(self, dt: datetime) -> bool:
        """Return True if the (local, wall-clock) datetime matches this expression."""
        if dt.minute not in self.minutes or dt.hour not in self.hours:
            return False
        if dt.month not in self.months:
            return False
        dom_ok = dt.day in self.days_of_month
        dow_ok = (dt.isoweekday() % 7) in self.days_of_week  # Sunday -> 0
        if self.dom_restricted and self.dow_restricted:
            return dom_ok or dow_ok
        return dom_ok and dow_ok
