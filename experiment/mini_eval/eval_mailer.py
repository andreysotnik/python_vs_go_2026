"""Скрытая проверка рассылки через Mailpit (API на 38025, SMTP на 31025).
python3 -I eval_mailer.py <project_dir> <cmd...>   (cmd — префикс запуска CLI, напр. `go run ./cmd/mailer`)"""
import json, os, shutil, subprocess, sys, tempfile, urllib.request
proj, cmd = sys.argv[1], sys.argv[2:]
here = os.path.dirname(os.path.abspath(__file__))
cfg = os.path.join(here, "mailer_cfg", "jobs.yaml")
API = "http://127.0.0.1:38025/api/v1"
def api(path, method="GET"):
    req = urllib.request.Request(API + path, method=method)
    with urllib.request.urlopen(req, timeout=5) as r:
        body = r.read()
        return json.loads(body) if body[:1] in (b"{", b"[") else None
state = tempfile.mkdtemp(dir=os.path.join(here, "state_tmp") if os.path.isdir(os.path.join(here, "state_tmp")) else None)
env = {**os.environ, "SMTP_HOST": "127.0.0.1", "SMTP_PORT": "31025", "SMTP_TLS": "none",
       "SMTP_FROM": "mailer@example.com", "STATE_DIR": state}
env.pop("HTTPS_PROXY", None); env.pop("HTTP_PROXY", None)
def run(*args):
    r = subprocess.run(cmd + list(args), cwd=proj, env=env, capture_output=True, text=True, timeout=300)
    return r.returncode, r.stderr[-400:]
api("/messages", "DELETE")
steps = [("Чт 09:00 МСК", "2026-11-19T09:00:00+03:00", 2), ("повтор того же слота", "2026-11-19T09:00:00+03:00", 0),
         ("Сб 09:00 МСК", "2026-11-21T09:00:00+03:00", 1), ("Чт 05:00 МСК = 09:00 НСК", "2026-11-19T05:00:00+03:00", 2)]
res, total = [], 0
for name, at, exp in steps:
    rc, err = run("once", "--config", cfg, "--at", at)
    n = api("/messages")["total"]
    res.append({"step": name, "rc": rc, "expected_new": exp, "got_new": n - total, "ok": rc == 0 and n - total == exp})
    total = n
msgs = api("/messages?limit=50")["messages"]
subjects = sorted(m["Subject"] for m in msgs)
daily = [m for m in msgs if m["Subject"].startswith("Ежедневный")]
detail = {}
if daily:
    full = api(f"/message/{daily[0]['ID']}")
    detail = {"to": sorted(a["Address"] for a in full["To"]), "attachments": [a["FileName"] for a in full.get("Attachments", [])],
              "subject": full["Subject"], "body_ok": "2026-11-19" in full.get("Text", "") and "daily-report" in full.get("Text", "")}
pulse = [m for m in msgs if m["Subject"].startswith("Пульс")]
cc_ok = bool(pulse) and all(any(c["Address"] == "audit@example.com" for c in m.get("Cc") or []) for m in pulse)
# validate на битом конфиге
bad = os.path.join(state, "bad.yaml")
open(bad, "w").write('jobs:\n  - name: x\n    schedule: "61 * * * *"\n    timezone: "Mars/Olympus"\n    to: ["not-an-email"]\n    subject: "s"\n    body: "b"\n')
rc_bad, _ = run("validate", "--config", bad)
rc_good, _ = run("validate", "--config", cfg)
checks = [r["ok"] for r in res] + [detail.get("subject") == "Ежедневный отчёт за 2026-11-19",
          detail.get("to") == ["boss@example.com", "team@example.com"], detail.get("attachments") == ["report.pdf"],
          bool(detail.get("body_ok")), cc_ok, rc_bad != 0, rc_good == 0]
print(json.dumps({"steps": res, "subjects": subjects, "daily": detail, "cc_ok": cc_ok, "validate_bad_rc": rc_bad,
                  "validate_good_rc": rc_good, "passed": sum(checks), "total": len(checks)}, ensure_ascii=False, indent=1))
shutil.rmtree(state, ignore_errors=True)
