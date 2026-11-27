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
#   KEEP=1 ...                                  # keep the sandbox and its logs
#
# Chat replies are checked only when an OpenAI-compatible endpoint answers at
# CIVQUEST_LLM_BASE_URL (default http://127.0.0.1:8080/v1).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
AGENTS="${AGENTS:-able bert carl dave earl fred}"
DURATION="${DURATION:-600}"
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

python3 -I - "$XDG_STATE_HOME/civquest/logs/playtest-events.jsonl" "$SERVER_LOG" "$end_iso" "$first" "${MODEL:-}" \
  "$started" "$hungry_at" "$night_at" $AGENTS <<'PY'
import json, re, sys, collections, datetime as dt
events_path, server_log, end_iso, chat_agent, model, start_iso, hungry_iso, night_iso, *agents = sys.argv[1:]
end = dt.datetime.fromisoformat(end_iso)
duration = (end - dt.datetime.fromisoformat(start_iso)).total_seconds()
when = lambda e: dt.datetime.fromisoformat(e["timestamp"][:19])

def loops(ev):
    """Longest emergency (s), most pushes of one activity, worst motionless repeat, bed tries, lowest food."""
    pushes, repeats = collections.Counter(), collections.Counter()
    emergency = longest = beds = 0.0
    since = here = None
    food, worst = 20, (0, "")
    for e in sorted(ev, key=lambda e: e["timestamp"]):
        d, c = e.get("details") or {}, e["category"]
        if c == "VITALS":
            food = min(food, d.get("food", 20))
            if d.get("stage") == "EMERGENCY":
                since = since or when(e)
                longest = max(longest, (when(e) - since).total_seconds())
            else:
                since = None
            p = d.get("position")
            if p and (here is None or ((p[0]-here[0])**2 + (p[1]-here[1])**2 + (p[2]-here[2])**2) ** .5 > 1.0):
                here, repeats = p, collections.Counter()
        elif c == "FSM_FRAME" and d.get("op") == "push":
            pushes[d.get("kind")] += 1
        elif c == "INTENT_OUT":
            action = d.get("action", "")
            beds += "SLEEP_IN_BED" in action
            # Mining one block takes several swings; chat is not movement or work.
            if "HARVEST_BREAK" in action or "Chat" in action or "Attack" in action:
                continue
            key = f"{action} {d.get('target')}"
            repeats[key] += 1
            if repeats[key] > worst[0]:
                worst = (repeats[key], key)
    kind, n = max(pushes.items(), key=lambda kv: kv[1], default=("", 0))
    return longest, (n, kind), worst, int(beds), food

by = collections.defaultdict(list)
for line in open(events_path):
    try: e = json.loads(line)
    except ValueError: continue
    by[e.get("agent_id")].append(e)
failed = []
print(f"{'agent':8} {'events':>7} {'silent_s':>8} {'stalls':>6} {'stuck':>6} {'moved':>6} {'repeat':>6}  top crafts")
log = open(server_log, errors="replace").read()
deaths = dict(re.findall(r"AgentEntity died: agentId='([^']+)'.*?cause='([^']*)'", log))
# Dying to the world is the agent's own failure; losing a fight is reported but allowed.
SELF_INFLICTED = ("drowned", "flames", "burn", "lava", "fell", "hit the ground", "suffocated", "starved", "cactus", "pricked")
for a in agents:
    ev = by.get(a, [])
    if not ev:
        failed.append(f"{a}: no events"); continue
    silent = (end - when(ev[-1])).total_seconds()
    cat = collections.Counter(e["category"] for e in ev)
    pos = [e["details"]["position"] for e in ev if e["category"] == "VITALS" and e["details"].get("position")]
    moved = sum(((p[0]-q[0])**2 + (p[2]-q[2])**2) ** .5 for p, q in zip(pos, pos[1:]))
    crafts = collections.Counter(e["summary"].split("recipe=")[1].rstrip(")").replace("minecraft:", "")
                                 for e in ev if "recipe=" in e.get("summary", ""))
    targets = collections.Counter(tuple(e["details"]["target"]) for e in ev
                                  if e["category"] == "INTENT_OUT" and e["details"].get("target"))
    repeat = max(targets.values(), default=0)
    dead = a in deaths
    print(f"{a:8} {len(ev):7} {silent:8.0f} {cat['BRAIN_STALL']:6} {cat['MOTOR_STUCK']:6} {moved:6.0f} {repeat:6}  {dict(crafts.most_common(5))}" + (f" DIED: {deaths[a]}" if dead else ""))
    if cat["BRAIN_STALL"]: failed.append(f"{a}: brain stalled")
    if dead and any(w in deaths[a] for w in SELF_INFLICTED): failed.append(f"{a}: died ({deaths[a]})")
    if silent > 15 and not dead: failed.append(f"{a}: silent for the last {silent:.0f}s")
    for recipe, n in crafts.items():
        if n > 40: failed.append(f"{a}: crafted {recipe} {n} times (craft loop)")
    # Walking to one place re-sends it now and then; hundreds of times is a retry loop.
    if repeat > 200: failed.append(f"{a}: sent one movement target {repeat} times (retry loop)")
    longest, (pushed, kind), (still, action), beds, food = loops(ev)
    print(f"{'':8} emergency {longest:.0f}s, {kind} pushed {pushed}x, {still}x unmoved, bed tries {beds}, lowest food {food}")
    if longest > 60: failed.append(f"{a}: in an emergency for {longest:.0f}s")
    # A real preemption now and then; one every few seconds is churn.
    if pushed > max(60, duration / 5): failed.append(f"{a}: pushed {kind} {pushed} times (frame churn)")
    if still > 60: failed.append(f"{a}: sent '{action}' {still} times without moving")
    if beds > 15: failed.append(f"{a}: tried to sleep {beds} times")
    if food > 12 and not dead: failed.append(f"{a}: never got hungry, the hunger phase did not work")
print(f"server: {log.count('Dropping telemetry frames')} backpressure warnings, {log.count(chr(39) + ' spoke in chat')} agent chat lines")
# A run on treeless ground (open ocean) exercises little: say so rather than pass quietly.
crafted = sum(1 for a in agents if any("recipe=" in e.get("summary", "") for e in by.get(a, [])))
if crafted * 2 < len(agents):
    print(f"NOTE: only {crafted} of {len(agents)} agents crafted anything; wood gathering and crafting were barely exercised")
if model:
    replied = f"Agent '{chat_agent}' spoke in chat" in log
    print(f"chat: {chat_agent} {'replied' if replied else 'did NOT reply'}")
    if not replied: failed.append(f"{chat_agent}: no chat reply")
else:
    print("chat: skipped (no model endpoint)")
print(f"phases (UTC): start {start_iso}, hunger {hungry_iso}, night {night_iso}, end {end_iso}")
for f in failed: print("FAIL", f)
print("RESULT:", "FAIL" if failed else "PASS")
sys.exit(1 if failed else 0)
PY
