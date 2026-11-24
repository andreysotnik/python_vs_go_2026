"""Command line interface: ``check``, ``censor`` and ``eval`` sub-commands."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Iterator
from contextlib import contextmanager
from typing import TextIO

from profanity_filter.core import censor, find_matches


@contextmanager
def _open_input(path: str | None) -> Iterator[TextIO]:
    if path is None or path == "-":
        sys.stdin.reconfigure(encoding="utf-8", errors="replace")  # type: ignore[union-attr]
        yield sys.stdin
    else:
        with open(path, encoding="utf-8", errors="replace", newline="") as fh:
            yield fh


def _strip_eol(line: str) -> str:
    return line.rstrip("\r\n")


def cmd_check(args: argparse.Namespace) -> int:
    out = sys.stdout
    with _open_input(args.input) as fh:
        for n, raw in enumerate(fh, 1):
            text = _strip_eol(raw)
            found = find_matches(text)
            rec = {
                "line": n,
                "text": text,
                "profane": bool(found),
                "matches": [m.word for m in found],
            }
            out.write(json.dumps(rec, ensure_ascii=False))
            out.write("\n")
    return 0


def cmd_censor(args: argparse.Namespace) -> int:
    with _open_input(args.input) as fh:
        for raw in fh:
            text = _strip_eol(raw)
            sys.stdout.write(censor(text) + raw[len(text) :])
    return 0


def cmd_eval(args: argparse.Namespace) -> int:
    tp = fp = fn = tn = 0
    errors: list[tuple[str, int, str, list[str]]] = []
    with _open_input(args.input) as fh:
        for n, raw in enumerate(fh, 1):
            line = _strip_eol(raw)
            if not line.strip() or line.startswith("#"):
                continue
            label_s, sep, text = line.partition("\t")
            if not sep or label_s.strip() not in ("0", "1"):
                print(f"skip line {n}: bad format", file=sys.stderr)
                continue
            label = label_s.strip() == "1"
            found = find_matches(text)
            pred = bool(found)
            words = [m.word for m in found]
            if pred and label:
                tp += 1
            elif pred:
                fp += 1
                errors.append(("FP", n, text, words))
            elif label:
                fn += 1
                errors.append(("FN", n, text, words))
            else:
                tn += 1
    precision = tp / (tp + fp) if tp + fp else 1.0
    recall = tp / (tp + fn) if tp + fn else 1.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    total = tp + fp + fn + tn
    print(f"total: {total}  TP={tp} FP={fp} FN={fn} TN={tn}")
    print(f"precision: {precision:.4f}")
    print(f"recall:    {recall:.4f}")
    print(f"F1:        {f1:.4f}")
    print(f"errors: {len(errors)}")
    for kind, n, text, words in errors:
        extra = f"  matches={words}" if words else ""
        print(f"  {kind} line {n}: {text}{extra}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="profanity", description="Regex profanity filter")
    sub = p.add_subparsers(dest="command", required=True)
    c = sub.add_parser("check", help="check each input line, output JSONL")
    c.add_argument("--input", "-i", help="input file (default: stdin)")
    c.set_defaults(func=cmd_check)
    z = sub.add_parser("censor", help="censor text from stdin (or --input)")
    z.add_argument("--input", "-i", help="input file (default: stdin)")
    z.set_defaults(func=cmd_censor)
    e = sub.add_parser("eval", help="evaluate on a labelled TSV: label<TAB>text")
    e.add_argument("--input", "-i", required=True, help="TSV file")
    e.set_defaults(func=cmd_eval)
    return p


def main(argv: list[str] | None = None) -> int:
    sys.stdout.reconfigure(encoding="utf-8")  # type: ignore[union-attr]
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except BrokenPipeError:
        return 0
    except FileNotFoundError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
