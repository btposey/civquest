#!/usr/bin/env python3
"""Capability coverage checker. Parses doc/capability-coverage.yaml (one JSON mapping per
`  - {...}` line), verifies every referenced test exists, prints coverage %.

  --strict    exit 1 if any capability is gap/partial, or any referenced test is missing
  --deep      additionally confirm Go tests via `go test -list` (tags integration,livellm)
  --emit-md   print the markdown table (used to regenerate doc/capability-coverage.md)
"""
import json, os, re, subprocess, sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
YAML = os.path.join(ROOT, "doc", "capability-coverage.yaml")


def load():
    rows = []
    with open(YAML) as f:
        for n, line in enumerate(f, 1):
            s = line.strip()
            if s.startswith("- {"):
                try:
                    rows.append(json.loads(s[2:]))
                except ValueError as e:
                    sys.exit(f"{YAML}:{n}: bad row: {e}")
    return rows


def read(path):
    try:
        with open(os.path.join(ROOT, path), errors="replace") as f:
            return f.read()
    except OSError:
        return None


def dir_text(path):
    d = os.path.dirname(os.path.join(ROOT, path))
    out = []
    for base, _, files in os.walk(d):
        for fn in files:
            try:
                with open(os.path.join(base, fn), errors="replace") as f:
                    out.append(f.read())
            except OSError:
                pass
    return "\n".join(out)


def check_ref(ref):
    """Return None if ok, else a problem string."""
    m = re.match(r"^(go|java):(.+?)::(.+)$", ref)
    if not m:
        return "malformed ref"
    kind, path, name = m.groups()
    text = read(path)
    if text is None:
        return "file missing"
    if kind == "go":
        fn, _, sub = name.partition("/")
        if fn == "*":
            if not re.search(r"^func Test\w+\(", text, re.M):
                return "no Test funcs in file"
        elif not re.search(r"^func %s\(" % re.escape(fn), text, re.M):
            return f"func {fn} not found"
        if sub and sub not in dir_text(path):
            return f"subtest '{sub}' not found"
    else:
        if name == "*":
            if "@Test" not in text and "@GameTest" not in text:
                return "no @Test in file"
        elif not re.search(r"\bvoid\s+%s\s*\(" % re.escape(name), text):
            return f"method {name} not found"
    return None


def deep_go(rows):
    pkgs = {}
    for r in rows:
        for t in r["tests"]:
            if t.startswith("go:"):
                p, _, n = t[3:].partition("::")
                pkgs.setdefault(os.path.dirname(p), set()).add(n.split("/")[0])
    bad = []
    for d, names in sorted(pkgs.items()):
        mod = os.path.join(ROOT, "orchestrator-go")
        rel = "./" + os.path.relpath(os.path.join(ROOT, d), mod)
        res = subprocess.run(["go", "test", "-tags", "integration,livellm", "-list", ".", rel],
                             cwd=mod, capture_output=True, text=True)
        listed = set(res.stdout.split())
        for n in names:
            if n != "*" and n not in listed:
                bad.append(f"{d}::{n} not listed by go test -list")
    return bad


def md(rows):
    print("| Capability | Layer | Status | Level | Covering test(s) | Group |")
    print("|---|---|---|---|---|---|")
    for r in rows:
        tests = "<br>".join("`%s`" % t.split(":", 1)[1].replace("orchestrator-go/", "").replace("mod-neoforge/src/", "") for t in r["tests"]) or "**GAP**"
        cap = f"`{r['id']}` {r['capability']}"
        if r["note"]:
            cap += f" _({r['note']})_"
        print(f"| {cap} | {r['layer']} | {r['status']} | {r['level']} | {tests} | {r['group']} |")


def main():
    args = sys.argv[1:]
    rows = load()
    if "--emit-md" in args:
        md(rows)
        return 0
    problems, missing = [], []
    for r in rows:
        if r["status"] == "covered" and not r["tests"]:
            problems.append(f"{r['id']}: covered but lists no tests")
        for t in r["tests"]:
            p = check_ref(t)
            if p:
                missing.append(f"{r['id']}: {t} -> {p}")
    if "--deep" in args:
        missing += deep_go(rows)
    broken = {m.split(":")[0] for m in missing}
    scored = [r for r in rows if r["status"] != "wip"]
    covered = [r for r in scored if r["status"] == "covered" and r["id"] not in broken]
    gaps = [r for r in scored if r["status"] == "gap"]
    partial = [r for r in scored if r["status"] == "partial"]
    wip = [r for r in rows if r["status"] == "wip"]
    pct = 100.0 * len(covered) / max(1, len(scored))
    print(f"Capability coverage: {len(covered)}/{len(scored)} = {pct:.1f}%  "
          f"(gap={len(gaps)} partial={len(partial)} wip-excluded={len(wip)} missing-refs={len(missing)})")
    by = {}
    for r in covered:
        by[r["level"]] = by.get(r["level"], 0) + 1
    print("Covered by level: " + ", ".join(f"{k}={v}" for k, v in sorted(by.items())))
    for title, lst in (("GAP", gaps), ("PARTIAL", partial)):
        for r in lst:
            print(f"  {title:7} {r['id']}  [{r['group']}]")
    for m in missing + problems:
        print("  MISSING-TEST " + m)
    if "--strict" in args and (gaps or partial or missing or problems):
        print("FAIL (--strict): every capability needs a covering test that exists.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
