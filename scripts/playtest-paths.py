#!/usr/bin/env python3
"""Per-agent path-retread report from a playtest-events JSONL log (bead cq-gpa.12).

Usage: playtest-paths.py [LOG] [--cell N] [--loop-min-visits K]
Position samples come from VITALS/MOTION details.position (x,y,z); navigate targets from
INTENT_OUT NavigateIntent details.target/reason. Older logs lack these fields: the report
then says so instead of failing.
"""
import argparse, collections, json, math, os, sys

DEFAULT = os.path.expanduser("~/.local/state/civquest/logs/playtest-events.jsonl")


def tsec(ts):
    # "2026-11-26T02:36:45.485327183Z" -> seconds; trim ns to us.
    from datetime import datetime, timezone
    try:
        base, frac = ts.rstrip("Z").split(".") if "." in ts else (ts.rstrip("Z"), "0")
        dt = datetime.strptime(base, "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc)
        return dt.timestamp() + float("0." + frac)
    except Exception:
        return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("log", nargs="?", default=DEFAULT)
    ap.add_argument("--cell", type=int, default=2, help="cell size in blocks (default 2)")
    ap.add_argument("--loop-min-visits", type=int, default=3,
                    help="flag a cell revisited this many times via re-entry (default 3)")
    a = ap.parse_args()

    agents = collections.defaultdict(lambda: {"pos": [], "nav": [], "harv": 0, "t": []})
    bad = 0
    with open(a.log) as f:
        for line in f:
            try:
                e = json.loads(line)
            except ValueError:
                bad += 1
                continue
            ag = e.get("agent_id")
            if not ag:
                continue
            d = e.get("details") or {}
            cat, t = e.get("category"), tsec(e.get("timestamp", ""))
            A = agents[ag]
            if t is not None:
                A["t"].append(t)
            p = d.get("position")
            if cat in ("VITALS", "MOTION") and isinstance(p, list) and len(p) == 3:
                A["pos"].append((t, p))
            if cat == "INTENT_OUT":
                act = d.get("action", "")
                if act == "NavigateIntent":
                    tg = d.get("target")
                    A["nav"].append((t, tuple(tg) if isinstance(tg, list) and len(tg) == 3 else None,
                                     d.get("reason")))
                elif "HARVEST" in act:
                    A["harv"] += 1

    if bad:
        print(f"(skipped {bad} unparseable lines)")
    for ag in sorted(agents):
        A = agents[ag]
        print(f"== {ag}")
        navs = A["nav"]
        n_harv = A["harv"]
        ratio = f"{len(navs) / n_harv:.2f}" if n_harv else "n/a (no harvests)"
        print(f"  navigate intents: {len(navs)}  harvest intents: {n_harv}  navigate-to-harvest: {ratio}")
        tg = [(t) for _, t, _ in navs if t]
        if not tg:
            print("  nav targets: not recorded (log predates cq-gpa.12)")
        else:
            c = collections.Counter(tuple(math.floor(v / a.cell) for v in t) for t in tg)
            rep = [(k, n) for k, n in c.most_common() if n > 1][:5]
            print(f"  nav targets: {len(tg)} ({len(c)} distinct cells); repeated: "
                  + (", ".join(f"{[k[0]*a.cell,k[1]*a.cell,k[2]*a.cell]}x{n}" for k, n in rep) or "none"))
            kinds = collections.Counter((r or {}).get("activity", "?") for _, t, r in navs if t)
            print("  nav by activity: " + ", ".join(f"{k}={n}" for k, n in kinds.most_common()))
        pos = A["pos"]
        if len(pos) < 2:
            print("  positions: not recorded (log predates cq-gpa.12)" if not pos else "  positions: too few")
            continue
        seen, revisit, loops = set(), 0, collections.Counter()
        prev = None
        for _, p in pos:
            cell = (math.floor(p[0] / a.cell), math.floor(p[2] / a.cell))
            if cell in seen:
                revisit += 1
                if cell != prev:
                    loops[cell] += 1  # re-entered after having left it
            seen.add(cell)
            prev = cell
        ts = [t for t, _ in pos if t is not None]
        mins = (max(ts) - min(ts)) / 60 if len(ts) > 1 else 0
        rr = revisit / len(pos)
        print(f"  position samples: {len(pos)}  unique {a.cell}x{a.cell} cells: {len(seen)}  "
              f"revisit ratio: {rr:.2f}  unique cells/min: "
              + (f"{len(seen)/mins:.1f}" if mins > 0 else "n/a"))
        hot = [(c, n) for c, n in loops.most_common() if n >= a.loop_min_visits][:5]
        if hot:
            print("  FLAG retread loop at cells (x,z): "
                  + ", ".join(f"({c[0]*a.cell},{c[1]*a.cell}) re-entered {n}x" for c, n in hot))
        elif rr > 0.6 and len(pos) >= 10:
            print("  FLAG high revisit ratio (>0.6)")


if __name__ == "__main__":
    main()
