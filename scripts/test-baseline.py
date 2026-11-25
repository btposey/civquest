#!/usr/bin/env python3
"""Normalize and diff test results (cq-2sr.19 / cq-2sr.20).

Usage:
  scripts/test-baseline.py summarize <results-dir> [-o out.json]
  scripts/test-baseline.py diff <baseline-dir> <after-dir> [-o report.md]

A results dir may contain:
  go-*.json        output of `go test -json` (any file matching go-*.json)
  junit/**/*.xml   Gradle JUnit XML (copied from mod-neoforge/build/test-results/test)
Keys: "<go-file-stem>:<pkg>/<Test>[/<sub>]" and "java:<class>.<method>".
Value: {"status": pass|fail|skip, "dur": seconds}.
"""
import glob, json, os, sys
import xml.etree.ElementTree as ET


def load_go(path, out):
    tag = os.path.basename(path)[:-5]  # e.g. go-hermetic, go-race
    with open(path, errors="replace") as f:
        for line in f:
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                e = json.loads(line)
            except ValueError:
                continue
            a, t, p = e.get("Action"), e.get("Test"), e.get("Package")
            if a in ("pass", "fail", "skip") and p:
                key = "%s:%s/%s" % (tag, p, t) if t else "%s-pkg:%s" % (tag, p)
                out[key] = {"status": a, "dur": e.get("Elapsed", 0.0)}


def load_junit(d, out):
    for x in glob.glob(os.path.join(d, "**", "*.xml"), recursive=True):
        try:
            root = ET.parse(x).getroot()
        except ET.ParseError:
            continue
        for tc in root.iter("testcase"):
            st = "pass"
            if tc.find("failure") is not None or tc.find("error") is not None:
                st = "fail"
            elif tc.find("skipped") is not None:
                st = "skip"
            out["java:%s.%s" % (tc.get("classname"), tc.get("name"))] = {
                "status": st, "dur": float(tc.get("time") or 0)}


def summarize(d):
    out = {}
    for f in sorted(glob.glob(os.path.join(d, "go-*.json"))):
        load_go(f, out)
    load_junit(os.path.join(d, "junit"), out)
    return out


def counts(r):
    c = {"pass": 0, "fail": 0, "skip": 0}
    for v in r.values():
        c[v["status"]] += 1
    return c


def diff(a, b):
    L = ["# Test baseline diff", "",
         "baseline: %s" % counts(a), "after:    %s" % counts(b), ""]
    rows = {"REGRESSION (pass->fail)": [], "FIXED (fail->pass)": [],
            "MISSING after": [], "NEW after": [], "OTHER status change": [],
            "SLOWER (>2x and >0.5s)": []}
    for k in sorted(set(a) | set(b)):
        x, y = a.get(k), b.get(k)
        if x and not y:
            rows["MISSING after"].append(k)
        elif y and not x:
            rows["NEW after"].append("%s [%s]" % (k, y["status"]))
        elif x["status"] != y["status"]:
            if x["status"] == "pass" and y["status"] == "fail":
                rows["REGRESSION (pass->fail)"].append(k)
            elif x["status"] == "fail" and y["status"] == "pass":
                rows["FIXED (fail->pass)"].append(k)
            else:
                rows["OTHER status change"].append("%s %s->%s" % (k, x["status"], y["status"]))
        elif y["dur"] > 0.5 and y["dur"] > 2 * x["dur"]:
            rows["SLOWER (>2x and >0.5s)"].append("%s %.2fs->%.2fs" % (k, x["dur"], y["dur"]))
    for h, items in rows.items():
        L.append("## %s (%d)" % (h, len(items)))
        L += ["- " + i for i in items] or ["- none"]
        L.append("")
    return "\n".join(L)


def main():
    a = sys.argv[1:]
    out = None
    if "-o" in a:
        i = a.index("-o"); out = a[i + 1]; del a[i:i + 2]
    if len(a) >= 2 and a[0] == "summarize":
        r = summarize(a[1])
        text = json.dumps({"counts": counts(r), "tests": r}, indent=1, sort_keys=True)
    elif len(a) >= 3 and a[0] == "diff":
        text = diff(summarize(a[1]), summarize(a[2]))
    else:
        sys.exit(__doc__)
    if out:
        open(out, "w").write(text + "\n")
    else:
        print(text)


main()
