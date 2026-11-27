#!/usr/bin/env bash
# Sandbox play test: real agent processes on a throwaway headless server, outside your
# installation, left to play. The world, the agents and their clan records are new every run.
# Nothing is forced (no hunger effect, time change or chat) and nothing passes or fails; the run
# is recorded for study. The server and XDG directories live in a temporary sandbox; on exit,
# interrupted or not, the data is archived to ~/.local/share/civquest-playtests/<date>-sbN and
# the sandbox is removed. Your server, daemon and civquest directories are not touched.
#
#   scripts/sandbox-playtest.sh                     # 6 agents, 1 in-game day (about 20 minutes)
#   DAYS=3 scripts/sandbox-playtest.sh              # length in in-game days of world ticks
#   AGENTS="able bert" DAYS=0.1 scripts/sandbox-playtest.sh
#   SEED=12345 ...                                  # a different world
#
# difficulty, view-distance and simulation-distance are read from your play test server's
# server.properties when it exists (read only); otherwise normal, 10 and 10.
#
# No player joins this server. Agents count as players, so mobs spawn and crops and saplings
# grow round them as in a play test you are connected to. Phantoms do not (cq-quf).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
DETACH=(setsid)
. "$ROOT/scripts/lib/live-sandbox.sh"
AGENTS="${AGENTS:-able bert carl dave earl fred}"
SEED="${SEED:--7726806586744676324}"
DAYS="${DAYS:-1}"
TICKS="$(awk -v d="$DAYS" 'BEGIN { if (d ~ /^[0-9]*[.]?[0-9]+$/) printf "%d", d * 24000 }')"
[ "${TICKS:-0}" -gt 0 ] || { echo "DAYS must be a positive number of in-game days, not '$DAYS'" >&2; exit 2; }

USER_PROPERTIES="$HOME/.local/share/civquest/server/server.properties"
setting() { # <key> <default>
  local value=""
  if [ -r "$USER_PROPERTIES" ]; then value="$(sed -n "s/^$1=//p" "$USER_PROPERTIES" | tail -1 | tr -d '\r')"; fi
  echo "${value:-$2}"
}
DIFFICULTY="$(setting difficulty normal)"
VIEW_DISTANCE="$(setting view-distance 10)"
SIMULATION_DISTANCE="$(setting simulation-distance 10)"

ARCHIVE_ROOT="$HOME/.local/share/civquest-playtests"
mkdir -p "$ARCHIVE_ROOT"
day="$(date +%Y-%m-%d)"
commit="$(git rev-parse HEAD)"
began="" tick="" start_tick=""
reason="failed before the agents started"

sandbox_init playtest
SERVER_DIR="$SANDBOX/server"
# The commit is recorded now: the tree may move on while the run is going.
echo "$commit" >"$SANDBOX/deployed-commit.txt"
if [ -n "$(git status --porcelain)" ]; then
  { git status --short; git diff HEAD; } >"$SANDBOX/uncommitted.diff"
fi

# Sets tick to the world's age in ticks, as the server console reports it.
read_tick() {
  echo "time query gametime" >&3
  pause 1
  tick="$(grep -a -o ']: The time is [0-9]*' "$SERVER_LOG" | tail -1 | grep -o '[0-9]*$' || true)"
}

# Copies the run's data to a new archive directory. Fails if anything could not be copied.
archive() {
  local n=1 failed=0 f
  while dest="$ARCHIVE_ROOT/$day-sb$n"; ! mkdir "$dest" 2>/dev/null; do
    [ -e "$dest" ] || return 1
    n=$((n + 1))
  done
  mkdir -p "$dest/server" || return 1
  keep() { if [ -e "$1" ]; then cp -a "$1" "$2" || failed=1; fi; }
  # The layout matches the other play test archives: config, share and state are the XDG data.
  keep "$XDG_CONFIG_HOME/civquest" "$dest/config"
  keep "$XDG_DATA_HOME/civquest" "$dest/share"
  keep "$XDG_STATE_HOME/civquest" "$dest/state"
  mkdir -p "$dest/state/logs" || return 1
  keep "$SERVER_LOG" "$dest/state/server.log"
  keep "$SANDBOX/supervisor.out" "$dest/state/logs/supervisor.out"
  for f in world logs config server.properties; do keep "$SERVER_DIR/$f" "$dest/server/$f"; done
  for f in deployed-commit.txt uncommitted.diff run.txt summary.txt; do keep "$SANDBOX/$f" "$dest/$f"; done
  return "$failed"
}

finish() {
  rc=$?
  trap - EXIT
  # A second interrupt, or a closed terminal, must not cut the archiving short.
  trap '' INT TERM HUP PIPE
  set +e
  if [ -z "$GRADLE_PID" ]; then rm -rf "$SANDBOX"; exit "$rc"; fi
  local end_iso ran="none"
  end_iso="$(date -u +%Y-%m-%dT%H:%M:%S)"
  if [ -n "$start_tick" ]; then
    kill -0 "$GRADLE_PID" 2>/dev/null && read_tick
    ran=$((tick - start_tick))
  fi
  cat >"$SANDBOX/run.txt" <<RUN
result: $reason (exit status $rc)
commit: $commit
seed: $SEED
agents: $AGENTS
days requested: $DAYS ($TICKS ticks)
ticks run: $ran (world age ${start_tick:-?} to ${tick:-?})
agents started (UTC): ${began:-never}
ended (UTC): $end_iso
model: ${MODEL:-none}
difficulty: $DIFFICULTY, view-distance: $VIEW_DISTANCE, simulation-distance: $SIMULATION_DISTANCE
RUN
  if [ -n "$began" ] && [ -f "$XDG_STATE_HOME/civquest/logs/playtest-events.jsonl" ]; then
    python3 -I "$ROOT/scripts/live-report.py" playtest "$XDG_STATE_HOME/civquest/logs/playtest-events.jsonl" "$SERVER_LOG" \
      "$end_iso" "" "${MODEL:-}" "$began" "" "" $AGENTS 2>&1 | tee "$SANDBOX/summary.txt"
  fi
  echo "==> stopping agents and server"
  sandbox_stop
  cat "$SANDBOX/run.txt"
  if archive; then
    rm -rf "$SANDBOX"
    echo "archived: $dest"
  else
    echo "archiving failed; sandbox kept: $SANDBOX" >&2
    [ "$rc" != 0 ] || rc=1
  fi
  exit "$rc"
}
trap finish EXIT
trap 'reason="interrupted"; exit 130' INT
trap 'reason="terminated"; exit 143' TERM
trap 'reason="terminal closed"; exit 129' HUP
trap 'reason="output closed"; exit 141' PIPE

sandbox_build
[ -n "$MODEL" ] || echo "WARNING: no model endpoint at $LLM_URL: the agents will play without a language model" >&2
# Keep the agents out of your cache and runtime directories, and away from your server.
export XDG_CACHE_HOME="$SANDBOX/cache" XDG_RUNTIME_DIR="$SANDBOX/run"
mkdir -m 700 "$XDG_CACHE_HOME" "$XDG_RUNTIME_DIR"
unset CIVQUEST_DISCOVERY_PATH CIVQUEST_SERVER_ID CIVQUEST_AGENT_BIN

echo "==> starting headless server (seed $SEED, difficulty $DIFFICULTY, view $VIEW_DISTANCE, simulation $SIMULATION_DISTANCE)"
sandbox_start_server -Pcivquest.integrationRunDir="$SERVER_DIR" -Pcivquest.integrationLevelName=world \
  -Pcivquest.integrationDifficulty="$DIFFICULTY" -Pcivquest.integrationViewDistance="$VIEW_DISTANCE" \
  -Pcivquest.integrationSimulationDistance="$SIMULATION_DISTANCE"

sandbox_create_agents
began="$(date -u +%Y-%m-%dT%H:%M:%S)"
sandbox_start_supervisor
for _ in $(seq 30); do
  read_tick
  [ -z "$tick" ] || break
done
[ -n "$tick" ] || { echo "the server did not report the world time" >&2; exit 1; }
start_tick="$tick"
reason="failed during the run"
echo "==> running $(echo "$AGENTS" | wc -w) agents for $DAYS in-game days ($TICKS ticks); interrupt to stop early"

last_tick="$tick" advanced=$SECONDS shown=$SECONDS
while [ $((tick - start_tick)) -lt "$TICKS" ]; do
  kill -0 "$GRADLE_PID" 2>/dev/null || { reason="server exited"; echo "$reason" >&2; exit 1; }
  kill -0 "$SUP_PID" 2>/dev/null || { reason="supervisor exited"; echo "$reason" >&2; exit 1; }
  pause 4
  read_tick
  if [ "$tick" -gt "$last_tick" ]; then
    last_tick="$tick" advanced=$SECONDS
  elif [ $((SECONDS - advanced)) -gt 180 ]; then
    reason="world ticks stopped"; echo "$reason" >&2; exit 1
  fi
  if [ $((SECONDS - shown)) -ge 60 ]; then
    shown=$SECONDS
    echo "    $((tick - start_tick)) of $TICKS ticks"
  fi
done
reason="complete"
