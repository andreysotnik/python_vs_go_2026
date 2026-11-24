"""Сбор метрик эксперимента: токены и вызовы инструментов из транскрипта агента, строки кода из каталога.

Использование: python3 -I metrics.py <transcript.jsonl> <impl_dir>
"""
import json
import os
import sys
from collections import Counter, defaultdict

SKIP_DIRS = {".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache",
             "node_modules", "vendor", ".git", "htmlcov", "bin", "dist", "build", ".idea"}
LANG_BY_EXT = {".go": "Go", ".py": "Python", ".sql": "SQL", ".yaml": "YAML", ".yml": "YAML",
               ".md": "Markdown", ".js": "JavaScript", ".toml": "TOML", ".sh": "Shell",
               ".json": "JSON", ".mod": "go.mod", ".cfg": "Config", ".ini": "Config", ".conf": "Config"}
SPECIAL = {"Dockerfile": "Docker", "Makefile": "Make", "docker-compose.yml": "YAML"}


def tokens(path):
    usage_by_msg = {}
    tools = Counter()
    first_ts = last_ts = None
    with open(path, encoding="utf-8") as f:
        for line in f:
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            ts = rec.get("timestamp")
            if ts:
                first_ts = first_ts or ts
                last_ts = ts
            if rec.get("type") != "assistant":
                continue
            msg = rec.get("message", {})
            if msg.get("usage"):
                usage_by_msg[msg.get("id") or id(rec)] = msg["usage"]
            for block in msg.get("content") or []:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    tools[block.get("name")] += 1
    total = defaultdict(int)
    for u in usage_by_msg.values():
        for k in ("input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"):
            total[k] += u.get(k) or 0
    total["api_calls"] = len(usage_by_msg)
    total["all_tokens"] = sum(total[k] for k in ("input_tokens", "output_tokens",
                                                  "cache_creation_input_tokens", "cache_read_input_tokens"))
    return dict(total), dict(tools), first_ts, last_ts


def is_test(rel):
    name = os.path.basename(rel)
    return name.endswith("_test.go") or name.startswith("test_") or "/tests/" in "/" + rel


def loc(root):
    stats = defaultdict(lambda: {"files": 0, "lines": 0, "code": 0})
    test_code = prod_code = 0
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            lang = SPECIAL.get(fn) or LANG_BY_EXT.get(os.path.splitext(fn)[1])
            if not lang or fn in ("PROMPT.md", "go.sum", "uv.lock", "package-lock.json"):
                continue
            p = os.path.join(dirpath, fn)
            rel = os.path.relpath(p, root)
            try:
                lines = open(p, encoding="utf-8", errors="ignore").read().splitlines()
            except OSError:
                continue
            comment = "#" if lang in ("Python", "YAML", "Shell", "Docker", "Make", "TOML") else "//"
            code = sum(1 for l in lines if l.strip() and not l.strip().startswith((comment, "--")))
            s = stats[lang]
            s["files"] += 1
            s["lines"] += len(lines)
            s["code"] += code
            if lang in ("Go", "Python"):
                if is_test(rel):
                    test_code += code
                else:
                    prod_code += code
    return dict(stats), prod_code, test_code


if __name__ == "__main__":
    tr, impl = sys.argv[1], sys.argv[2]
    t, tools, first_ts, last_ts = tokens(tr)
    s, prod, test = loc(impl)
    print(json.dumps({"tokens": t, "tool_calls": tools, "tool_calls_total": sum(tools.values()),
                      "first_ts": first_ts, "last_ts": last_ts,
                      "loc_by_lang": s, "main_lang_prod_code": prod, "main_lang_test_code": test},
                     ensure_ascii=False, indent=1))
