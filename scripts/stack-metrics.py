#!/usr/bin/env python3
"""Score activity-stack behaviour (ADR-040) from CIVQUEST_STACK_METRICS records.

  stack-metrics.py records.jsonl [--tests go-test.json] [--out score.json] [--compare base.json]
"""
import argparse, collections, json, sys


def score(records, tests):
    ticks, depth, given_up = 0, 0, 0
    push, idle, viol = collections.Counter(), collections.Counter(), collections.Counter()
    stuck = collections.Counter()
    for line in open(records, errors="replace"):
        try:
            r = json.loads(line)
        except ValueError:
            continue
        ticks += r.get("ticks", 0)
        depth = max(depth, r.get("depth", 0))
        given_up += r.get("given_up", 0)
        push.update(r.get("push", []))
        idle.update(r.get("idle", []))
        viol.update(r.get("violations", []))
        stuck.update(r.get("watchdog", []))
    by_rule = collections.Counter()
    for key, n in viol.items():
        by_rule[key.split("|")[0]] += n
    per_k = lambda n: round(1000 * n / ticks, 2) if ticks else 0.0
    out = {
        "ticks": ticks,
        "max_depth": depth,
        "walks_given_up": given_up,
        "watchdog_firings": sum(stuck.values()),
        "watchdog_by_condition_activity": dict(stuck.most_common()),
        "pushes_per_1000_ticks": per_k(sum(push.values())),
        "idle_pops_per_1000_ticks": per_k(sum(idle.values())),
        "violations_per_1000_ticks": per_k(sum(viol.values())),
        "violations_by_rule": dict(by_rule.most_common()),
        "top_violations": dict(viol.most_common(15)),
        "idle_pops_by_kind": dict(idle.most_common()),
        "pushes_by_kind": dict(push.most_common()),
    }
    if tests:
        passed, failed = set(), set()
        for line in open(tests, errors="replace"):
            try:
                e = json.loads(line)
            except ValueError:
                continue
            if e.get("Test") and e.get("Action") in ("pass", "fail"):
                (passed if e["Action"] == "pass" else failed).add(e["Package"].split("orchestrator-go/")[-1] + " " + e["Test"])
        out["tests_passed"] = len(passed)
        out["tests_failed"] = sorted(failed)
    return out


def main():
    p = argparse.ArgumentParser()
    p.add_argument("records")
    p.add_argument("--tests")
    p.add_argument("--out")
    p.add_argument("--compare")
    a = p.parse_args()
    s = score(a.records, a.tests)
    if a.out:
        with open(a.out, "w") as f:
            json.dump(s, f, indent=2)
            f.write("\n")
    base = json.load(open(a.compare)) if a.compare else {}
    rows = ["ticks", "max_depth", "pushes_per_1000_ticks", "idle_pops_per_1000_ticks", "violations_per_1000_ticks", "walks_given_up", "watchdog_firings", "tests_passed"]
    print(f"{'measure':34}{'baseline':>12}{'now':>12}")
    for k in rows:
        if k in s:
            print(f"{k:34}{str(base.get(k, '-')):>12}{str(s[k]):>12}")
    if "tests_failed" in s:
        print(f"{'tests_failed':34}{str(len(base.get('tests_failed', [])) if base else '-'):>12}{len(s['tests_failed']):>12}")
        for t in s["tests_failed"][:25]:
            print("  FAIL", t)
    for k in sorted(set(s["violations_by_rule"]) | set(base.get("violations_by_rule", {}))):
        print(f"{k[:34]:34}{str(base.get('violations_by_rule', {}).get(k, 0 if base else '-')):>12}{s['violations_by_rule'].get(k, 0):>12}")
    print("watchdog reports (condition | activity), including those tests declare:")
    for k, n in s["watchdog_by_condition_activity"].items():
        print(f"  {n:8}  {k}")
    print("most frequent violations (rule | under>pushed):")
    for k, n in list(s["top_violations"].items())[:10]:
        print(f"  {n:8}  {k}")


if __name__ == "__main__":
    sys.exit(main())
