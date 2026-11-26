#!/usr/bin/env bash
# Live agent smoke: real agent processes against a throwaway headless server on generated
# terrain, in an isolated XDG sandbox (your installation is not touched). Fails if any agent
# brain goes silent or stalls, repeats one craft endlessly, or does not answer chat. Unit and
# simulation tests missed these (cq-yn1.1, cq-yn1.11).
#
#   scripts/live-agent-smoke.sh                 # 6 agents, 300 seconds
#   AGENTS="able bert" DURATION=120 scripts/live-agent-smoke.sh
#   KEEP=1 ...                                  # keep the sandbox and its logs
#
# Chat replies are checked only when an OpenAI-compatible endpoint answers at
# CIVQUEST_LLM_BASE_URL (default http://127.0.0.1:8080/v1).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
AGENTS="${AGENTS:-able bert carl dave earl fred}"
DURATION="${DURATION:-300}"
LLM_URL="${CIVQUEST_LLM_BASE_URL:-http://127.0.0.1:8080/v1}"
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/civquest-live-XXXXXX")"
export XDG_CONFIG_HOME="$SANDBOX/config" XDG_DATA_HOME="$SANDBOX/data" XDG_STATE_HOME="$SANDBOX/state"
mkdir -p "$XDG_CONFIG_HOME/civquest" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$SANDBOX/bin"
SERVER_DIR="$ROOT/mod-neoforge/build/integration-server"
META="$SERVER_DIR/civquest-server-meta.json"
SERVER_LOG="$SANDBOX/server.log"
FIFO="$SANDBOX/console"
SUP_PID=""
GRADLE_PID=""
# A dev server rewrites this tracked structure (key order only); restore it only if it was clean.
NBT=mod-neoforge/src/main/resources/data/civquest/structure/pioneer_campfire.nbt
nbt_was_clean=0
git diff --quiet -- "$NBT" && nbt_was_clean=1

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  [ -n "$SUP_PID" ] && kill -TERM "$SUP_PID" 2>/dev/null && wait "$SUP_PID" 2>/dev/null
  if [ -n "$GRADLE_PID" ]; then
    echo stop >&3 2>/dev/null || true
    for _ in $(seq 60); do kill -0 "$GRADLE_PID" 2>/dev/null || break; read -r -t 1 <> <(:) || true; done
    kill -TERM "$GRADLE_PID" 2>/dev/null || true
    exec 3>&- || true
  fi
  [ "$nbt_was_clean" = 1 ] && git show "HEAD:$NBT" >"$NBT"
  if [ "${KEEP:-0}" = 1 ]; then echo "sandbox kept: $SANDBOX"; else rm -rf "$SANDBOX"; fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

pause() { read -r -t "$1" <> <(:) || true; }

echo "==> building agent binaries"
(cd orchestrator-go && go build -o "$SANDBOX/bin/civquest-agent" ./cmd/civquest-agent && go build -o "$SANDBOX/bin/cq" ./cmd/cq)
export PATH="$SANDBOX/bin:$PATH"
MODEL="$(curl -s -m 5 "$LLM_URL/models" | jq -r '.data[0].id // empty' 2>/dev/null || true)"
cat > "$XDG_CONFIG_HOME/civquest/global.yaml" <<YAML
llm_gateway:
  base_url: "$LLM_URL"
  model: "${MODEL:-none}"
YAML

echo "==> starting headless server (generated terrain)"
mkfifo "$FIFO"
start_ms=$(($(date +%s) * 1000))
./gradlew --offline --no-daemon -Pcivquest.integrationLevelType=minecraft:normal \
  -Pcivquest.integrationLevelName="smoke-$(date +%s)" :mod-neoforge:runIntegrationServer <"$FIFO" >"$SERVER_LOG" 2>&1 &
GRADLE_PID=$!
exec 3>"$FIFO"
for _ in $(seq 180); do
  if [ -f "$META" ] && [ "$(jq -r .status "$META" 2>/dev/null)" = READY ] && [ "$(jq -r .timestamp_ms "$META")" -ge "$start_ms" ]; then break; fi
  kill -0 "$GRADLE_PID" 2>/dev/null || { echo "server exited early" >&2; tail -30 "$SERVER_LOG" >&2; exit 1; }
  pause 1
done
[ "$(jq -r .status "$META")" = READY ] || { echo "server not ready" >&2; tail -30 "$SERVER_LOG" >&2; exit 1; }
target="$(jq -r '"\(.grpc_host):\(.grpc_port)"' "$META")"
key="$(jq -r .shared_key "$META")"
server_id="$(jq -r .server_id "$META")"

for a in $AGENTS; do cq agent create --name "$a" --clan pioneers >/dev/null; done
echo "==> running $(echo "$AGENTS" | wc -w) agents for ${DURATION}s"
cq daemon start --foreground --agent-bin "$SANDBOX/bin/civquest-agent" --target "$target" --shared-key "$key" --server-id "$server_id" >"$SANDBOX/supervisor.out" 2>&1 &
SUP_PID=$!
pause $((DURATION - 45))
first="${AGENTS%% *}"
echo "cq tell $first how are you doing, and what are you working on?" >&3
pause 45
end_iso="$(date -u +%Y-%m-%dT%H:%M:%S)"

python3 -I - "$XDG_STATE_HOME/civquest/logs/playtest-events.jsonl" "$SERVER_LOG" "$end_iso" "$first" "${MODEL:-}" $AGENTS <<'PY'
import json, sys, collections, datetime as dt
events_path, server_log, end_iso, chat_agent, model, *agents = sys.argv[1:]
end = dt.datetime.fromisoformat(end_iso)
by = collections.defaultdict(list)
for line in open(events_path):
    try: e = json.loads(line)
    except ValueError: continue
    by[e.get("agent_id")].append(e)
failed = []
print(f"{'agent':8} {'events':>7} {'silent_s':>8} {'stalls':>6} {'stuck':>6} {'moved':>6}  top crafts")
for a in agents:
    ev = by.get(a, [])
    if not ev:
        failed.append(f"{a}: no events"); continue
    when = lambda e: dt.datetime.fromisoformat(e["timestamp"][:19])
    silent = (end - when(ev[-1])).total_seconds()
    cat = collections.Counter(e["category"] for e in ev)
    pos = [e["details"]["position"] for e in ev if e["category"] == "VITALS" and e["details"].get("position")]
    moved = sum(((p[0]-q[0])**2 + (p[2]-q[2])**2) ** .5 for p, q in zip(pos, pos[1:]))
    crafts = collections.Counter(e["summary"].split("recipe=")[1].rstrip(")").replace("minecraft:", "")
                                 for e in ev if "recipe=" in e.get("summary", ""))
    dead = any(e["category"] in ("DEATH", "PERMADEATH") for e in ev)
    print(f"{a:8} {len(ev):7} {silent:8.0f} {cat['BRAIN_STALL']:6} {cat['MOTOR_STUCK']:6} {moved:6.0f}  {dict(crafts.most_common(5))}" + (" DIED" if dead else ""))
    if cat["BRAIN_STALL"]: failed.append(f"{a}: brain stalled")
    if silent > 15 and not dead: failed.append(f"{a}: silent for the last {silent:.0f}s")
    for recipe, n in crafts.items():
        if n > 40: failed.append(f"{a}: crafted {recipe} {n} times (craft loop)")
log = open(server_log, errors="replace").read()
print(f"server: {log.count('Dropping telemetry frames')} backpressure warnings, {log.count(chr(39) + ' spoke in chat')} agent chat lines")
if model:
    replied = f"Agent '{chat_agent}' spoke in chat" in log
    print(f"chat: {chat_agent} {'replied' if replied else 'did NOT reply'}")
    if not replied: failed.append(f"{chat_agent}: no chat reply")
else:
    print("chat: skipped (no model endpoint)")
for f in failed: print("FAIL", f)
print("RESULT:", "FAIL" if failed else "PASS")
sys.exit(1 if failed else 0)
PY
