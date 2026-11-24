import json
import subprocess
import sys
from pathlib import Path

import pytest

from profanity_filter.cli import main

DATA = Path(__file__).resolve().parent.parent / "data"


def test_check_file(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    f = tmp_path / "in.txt"
    f.write_text("привет\nну ты и мудак\n\nShit happens\n", encoding="utf-8")
    assert main(["check", "--input", str(f)]) == 0
    lines = [json.loads(x) for x in capsys.readouterr().out.splitlines()]
    assert [r["line"] for r in lines] == [1, 2, 3, 4]
    assert lines[0] == {"line": 1, "text": "привет", "profane": False, "matches": []}
    assert lines[1]["profane"] is True and lines[1]["matches"] == ["мудак"]
    assert lines[2]["profane"] is False
    assert lines[3]["matches"] == ["Shit"]


def _run(args: list[str], stdin: str) -> str:
    proc = subprocess.run(
        [sys.executable, "-m", "profanity_filter", *args],
        input=stdin.encode(),
        capture_output=True,
        check=True,
    )
    return proc.stdout.decode()


def test_check_stdin() -> None:
    out = _run(["check"], "бля\nok\n").splitlines()
    assert json.loads(out[0])["profane"] is True
    assert json.loads(out[1])["profane"] is False


def test_censor_stdin() -> None:
    assert _run(["censor"], "Какой мудак\nхорошо\n") == "Какой м***\nхорошо\n"


def test_eval_sample(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["eval", "--input", str(DATA / "sample.tsv")]) == 0
    out = capsys.readouterr().out
    assert "precision: 1.0000" in out
    assert "recall:    1.0000" in out


def test_eval_reports_errors(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    f = tmp_path / "e.tsv"
    f.write_text("1\tчистый текст\n0\tблядь\n0\tнорм\n", encoding="utf-8")
    main(["eval", "--input", str(f)])
    out = capsys.readouterr().out
    assert "FN line 1" in out
    assert "FP line 2" in out
    assert "precision: 0.0000" in out


def test_missing_file() -> None:
    assert main(["check", "--input", "/nonexistent/file.txt"]) == 2
