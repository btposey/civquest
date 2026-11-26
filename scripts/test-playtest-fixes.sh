#!/usr/bin/env bash
# Runs only the playtest-2 fix scenarios (orchestrator-go/test/integration/playtest2_fixes_test.go,
# bead cq-gpa.13) against the real System 1 kernel in the closed-loop synthetic world and prints a
# pass/fail/skip table. Exit status is non-zero if any scenario FAILS (skips are allowed).
#
#   scripts/test-playtest-fixes.sh            # default: scenarios whose fix is not landed are SKIPPED
#   scripts/test-playtest-fixes.sh --pending  # also run the pending ones (PLAYTEST2_RUN_PENDING=1)
#   scripts/test-playtest-fixes.sh --race     # run under the race detector
set -u
cd "$(dirname "$0")/../orchestrator-go" || exit 2

race=""
for a in "$@"; do
  case "$a" in
    --pending) export PLAYTEST2_RUN_PENDING=1 ;;
    --race) race="-race" ;;
    *) echo "unknown flag: $a" >&2; exit 2 ;;
  esac
done

out="$(mktemp)"
trap 'rm -f "$out"' EXIT
# shellcheck disable=SC2086
go test $race -count=1 -json -run 'TestPlaytest2_' ./test/integration >"$out" 2>&1
rc=$?

python3 -I - "$out" <<'PY'
import json, sys, collections
res, reason, fails = collections.OrderedDict(), {}, collections.defaultdict(list)
raw = []
for line in open(sys.argv[1]):
    try:
        e = json.loads(line)
    except ValueError:
        raw.append(line.rstrip()); continue
    t, a = e.get("Test"), e.get("Action")
    if not t:
        if a == "output": raw.append(e["Output"].rstrip())
        continue
    if a in ("pass", "fail", "skip"):
        res[t] = (a.upper(), e.get("Elapsed", 0))
    elif a == "output":
        o = e["Output"].strip()
        if "playtest2_fixes_test.go" in o:
            if "pending cq-" in o: reason[t] = o.split(": ", 1)[-1]
            else: fails[t].append(o)
rows = [(t, v) for t, v in res.items()]
if not rows:
    print("no scenarios ran (build failure?)"); print("\n".join(raw[-30:])); sys.exit(1)
w = max(len(t) for t, _ in rows)
print("%-*s  %-5s  %6s  %s" % (w, "SCENARIO", "RESULT", "SECS", "NOTE"))
cnt = collections.Counter()
for t, (r, el) in rows:
    top = "/" not in t
    if top: cnt[r] += 1
    note = reason.get(t, "")
    if r == "FAIL" and fails.get(t): note = fails[t][0].split(": ", 1)[-1][:110]
    print("%-*s  %-5s  %6.2f  %s" % (w, t, r, el, note))
print("\nTOTAL: %d pass, %d fail, %d skip (top-level scenarios)" % (cnt["PASS"], cnt["FAIL"], cnt["SKIP"]))
PY
exit $rc
