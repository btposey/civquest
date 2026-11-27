#!/usr/bin/env bash
# Run every Go test and score how the activity stack behaved against the committed baseline
# (ADR-040). Test failures are reported, not fatal.
#
#   scripts/stack-metrics.sh              # compare with doc/stack-baseline.json
#   scripts/stack-metrics.sh out.json     # also write this run's score
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
(cd "$ROOT/orchestrator-go" && CIVQUEST_STACK_METRICS="$WORK/records.jsonl" go test -count=1 -json ./... >"$WORK/tests.json" 2>&1) || true
touch "$WORK/records.jsonl"
args=("$WORK/records.jsonl" --tests "$WORK/tests.json")
[ -f "$ROOT/doc/stack-baseline.json" ] && args+=(--compare "$ROOT/doc/stack-baseline.json")
[ $# -ge 1 ] && args+=(--out "$1")
python3 -I "$ROOT/scripts/stack-metrics.py" "${args[@]}"
