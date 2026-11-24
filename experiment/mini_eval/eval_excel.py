"""Сверка out/summary.json агента с эталоном. python3 -I eval_excel.py <expected.json> <out_dir>"""
import json, os, sys
exp = json.load(open(sys.argv[1], encoding="utf-8"))
out = sys.argv[2]
got = json.load(open(os.path.join(out, "summary.json"), encoding="utf-8"))
checks, fails = 0, []
def cmp(path, e, g):
    global checks
    checks += 1
    if isinstance(e, dict):
        checks -= 1
        if not isinstance(g, dict) or set(e) != set(g):
            fails.append(f"{path}: keys differ"); return
        for k in e: cmp(f"{path}.{k}", e[k], g[k])
    elif isinstance(e, list):
        checks -= 1
        if not isinstance(g, list) or len(e) != len(g):
            fails.append(f"{path}: list len {len(g) if isinstance(g, list) else g!r}"); return
        for i, (a, b) in enumerate(zip(e, g)): cmp(f"{path}[{i}]", a, b)
    elif isinstance(e, (int, float)) and not isinstance(e, bool):
        if not isinstance(g, (int, float)) or abs(e - g) > 0.011: fails.append(f"{path}: expected {e}, got {g}")
    elif e != g:
        fails.append(f"{path}: expected {e!r}, got {g!r}")
cmp("$", exp, got)
pngs = ["revenue_by_month.png", "revenue_by_region.png", "category_share.png", "top_products.png", "plan_vs_fact.png"]
missing = [p for p in pngs + ["report.html"] if not os.path.isfile(os.path.join(out, p))]
print(json.dumps({"value_checks": checks, "failed": len(fails), "accuracy_pct": round((checks - len(fails)) / checks * 100, 1),
                  "missing_files": missing, "first_failures": fails[:10]}, ensure_ascii=False, indent=1))
