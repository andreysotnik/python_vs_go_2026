"""Throughput benchmark: lines/second of ``find_matches`` on 100k generated lines.

Usage: uv run python scripts/bench.py [N_LINES]
Two corpora are measured:
  * "realistic": sentences built from a fixed vocabulary (the word cache helps);
  * "unique":    every word gets a random suffix, so the per-word cache is useless
                 (worst case for the regex engine).
Also writes the realistic corpus to ``bench_100k.txt`` (git-ignored) for CLI timing.
"""

from __future__ import annotations

import random
import sys
import time
from pathlib import Path

from profanity_filter.core import classify, find_matches

WORDS = [
    "привет",
    "как",
    "дела",
    "сегодня",
    "хорошая",
    "погода",
    "команда",
    "мандарин",
    "учебник",
    "потребитель",
    "рубля",
    "сабля",
    "застрахуйте",
    "скипидар",
    "хлеб",
    "небо",
    "себя",
    "тебе",
    "вебинар",
    "ребенок",
    "мудрый",
    "бляха",
    "работа",
    "проект",
    "встреча",
    "отчёт",
    "договор",
    "клиент",
    "сервер",
    "база",
    "данных",
    "ошибка",
    "релиз",
    "hello",
    "world",
    "shiitake",
    "Scunthorpe",
    "assassin",
    "meeting",
    "report",
    "deploy",
    "server",
    "блядь",
    "пиздец",
    "нахуй",
    "заебал",
    "мудак",
    "пидор",
    "залупа",
    "ебать",
    "хуйня",
    "fuck",
    "shit",
    "bitch",
    "x.у.й",
    "п-и-з-д-а",
    "бляяять",
    "пи3дец",
    "3аеб@л",
    "fuсk",
]


def make_lines(n: int, unique: bool, seed: int = 42) -> list[str]:
    rnd = random.Random(seed)
    lines = []
    for _ in range(n):
        k = rnd.randint(5, 15)
        ws = rnd.choices(WORDS, k=k)
        if unique:
            ws = [w + "".join(rnd.choices("абвгдежзик", k=3)) for w in ws]
        lines.append(" ".join(ws) + rnd.choice([".", "!", "?", ""]))
    return lines


def bench(lines: list[str]) -> float:
    classify.cache_clear()
    t0 = time.perf_counter()
    for line in lines:
        find_matches(line)
    return len(lines) / (time.perf_counter() - t0)


def main() -> None:
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 100_000
    real = make_lines(n, unique=False)
    Path("bench_100k.txt").write_text("\n".join(real) + "\n", encoding="utf-8")
    print(f"realistic: {bench(real):,.0f} lines/s ({n} lines)")
    print(f"unique:    {bench(make_lines(n, unique=True)):,.0f} lines/s ({n} lines)")


if __name__ == "__main__":
    main()
