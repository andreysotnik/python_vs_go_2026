"""Прогон CLI агента на скрытой выборке. python3 -I eval_profanity.py <hidden.tsv> <project_dir> <cmd...>
cmd должен содержать @INPUT@ — путь к файлу с текстами."""
import json, os, subprocess, sys, tempfile, time
hidden, proj, cmd = sys.argv[1], sys.argv[2], sys.argv[3:]
rows = [l.rstrip("\n").split("\t", 1) for l in open(hidden, encoding="utf-8") if l.strip()]
with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False, encoding="utf-8") as f:
    f.write("\n".join(t for _, t in rows) + "\n"); path = f.name
t0 = time.time()
res = subprocess.run([c.replace("@INPUT@", path) for c in cmd], cwd=proj, capture_output=True, text=True)
dt = time.time() - t0
outs = [json.loads(l) for l in res.stdout.splitlines() if l.strip().startswith("{")]
if len(outs) != len(rows):
    print(json.dumps({"error": f"got {len(outs)} lines for {len(rows)}", "cmd": res.args, "stderr": res.stderr[-800:]}, ensure_ascii=False)); sys.exit(1)
tp = fp = fn = tn = 0; errs = []
for (lab, text), o in zip(rows, outs):
    p = bool(o.get("profane")); l = lab == "1"
    if p and l: tp += 1
    elif p and not l: fp += 1; errs.append(("FP", text, o.get("matches")))
    elif l: fn += 1; errs.append(("FN", text, None))
    else: tn += 1
prec = tp / (tp + fp) if tp + fp else 0; rec = tp / (tp + fn) if tp + fn else 0
f1 = 2 * prec * rec / (prec + rec) if prec + rec else 0
print(json.dumps({"tp": tp, "fp": fp, "fn": fn, "tn": tn, "precision": round(prec, 3), "recall": round(rec, 3),
                  "f1": round(f1, 3), "accuracy": round((tp + tn) / len(rows), 3), "wall_s": round(dt, 2), "errors": errs},
                 ensure_ascii=False, indent=1))
