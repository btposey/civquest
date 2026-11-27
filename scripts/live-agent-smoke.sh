#!/usr/bin/env bash
# Live agent smoke: real agent processes against a throwaway headless server on generated
# terrain, in an isolated XDG sandbox (your installation is not touched). The run has three
# phases: a normal morning, then forced hunger, then night. Fails if any agent brain goes silent
# or stalls, repeats one craft endlessly, repeats one action without moving, stays in an
# emergency, pushes and pops one activity over and over, retries a bed all night, or does not
# answer chat, and if an agent dies to the world (drowning, fire, a fall, hunger). Unit and
# simulation tests missed these (cq-yn1.1, cq-yn1.11, cq-6l3.9).
#
#   scripts/live-agent-smoke.sh                 # 6 agents, 600 seconds
#   AGENTS="able bert" DURATION=240 scripts/live-agent-smoke.sh
#   SEED=12345 ...                              # a different world
#   SEED=2396837692978403373 ...                # playtest 5's icy, spruce and Drowned country
#   KEEP=1 ...                                  # keep the sandbox and its logs
#
# Chat replies are checked only when an OpenAI-compatible endpoint answers at
# CIVQUEST_LLM_BASE_URL (default http://127.0.0.1:8080/v1).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/live-sandbox.sh"
AGENTS="${AGENTS:-able bert carl dave earl fred}"
# Every run generates the same terrain, so runs can be compared; play within it still varies.
# This seed spawns beside woodland, water and broken ground, which has caught real faults.
SEED="${SEED:-6569162923784258077}"
DURATION="${DURATION:-600}"
sandbox_init live

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  sandbox_stop
  if [ "${KEEP:-0}" = 1 ]; then echo "sandbox kept: $SANDBOX"; else rm -rf "$SANDBOX"; fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

sandbox_build

echo "==> starting headless server (generated terrain, seed $SEED)"
sandbox_start_server -Pcivquest.integrationLevelName="smoke-$(date +%s)"

sandbox_create_agents
echo "==> running $(echo "$AGENTS" | wc -w) agents for ${DURATION}s"
sandbox_start_supervisor
started="$(date -u +%Y-%m-%dT%H:%M:%S)"
pause $((DURATION * 35 / 100))
# The Hunger effect is the one drain agents take on top of their metabolism: this empties the
# saturation and most of the food bar in seconds. Agents are not in the player list, so they are
# selected one by one by entity name.
hungry_at="$(date -u +%Y-%m-%dT%H:%M:%S)"
for a in $AGENTS; do
  echo "effect give @e[name=$a] minecraft:hunger 3 255" >&3
  echo "effect give @e[name=${a^}] minecraft:hunger 3 255" >&3
done
pause $((DURATION * 30 / 100))
night_at="$(date -u +%Y-%m-%dT%H:%M:%S)"
echo "time set 13000" >&3
pause $((DURATION - DURATION * 65 / 100 - 45))
first="${AGENTS%% *}"
echo "cq tell $first how are you doing, and what are you working on?" >&3
pause 45
end_iso="$(date -u +%Y-%m-%dT%H:%M:%S)"

python3 -I "$ROOT/scripts/live-report.py" smoke "$XDG_STATE_HOME/civquest/logs/playtest-events.jsonl" "$SERVER_LOG" "$end_iso" "$first" "${MODEL:-}" \
  "$started" "$hungry_at" "$night_at" $AGENTS
