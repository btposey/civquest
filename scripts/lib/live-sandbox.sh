# shellcheck shell=bash
# Shared by scripts/live-agent-smoke.sh and scripts/sandbox-playtest.sh: a throwaway headless
# server on generated terrain with real agent processes, in an isolated XDG sandbox. Source it
# from the repository root, with `set -euo pipefail` in force.
#
# The caller sets AGENTS and SEED, and may set before the calls below:
#   SERVER_DIR  the server's game directory (default: the shared integration-server build dir)
#   DETACH      (setsid) keeps the terminal's signals off the server and supervisor, so that the
#               caller's exit handler is what shuts them down

ROOT="${ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
LLM_URL="${CIVQUEST_LLM_BASE_URL:-http://127.0.0.1:8080/v1}"
SERVER_DIR="${SERVER_DIR:-$ROOT/mod-neoforge/build/integration-server}"
[ "${DETACH+set}" = set ] || DETACH=()
SANDBOX=""
SUP_PID=""
GRADLE_PID=""
# A dev server rewrites this tracked structure (key order only); restore it only if it was clean.
NBT=mod-neoforge/src/main/resources/data/civquest/structure/pioneer_campfire.nbt
nbt_was_clean=0

pause() { read -r -t "$1" <> <(:) || true; }

# sandbox_init <name>: makes the sandbox and points the XDG directories into it.
sandbox_init() {
  command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/civquest-$1-XXXXXX")"
  export XDG_CONFIG_HOME="$SANDBOX/config" XDG_DATA_HOME="$SANDBOX/data" XDG_STATE_HOME="$SANDBOX/state"
  mkdir -p "$XDG_CONFIG_HOME/civquest" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$SANDBOX/bin"
  SERVER_LOG="$SANDBOX/server.log"
  FIFO="$SANDBOX/console"
  git diff --quiet -- "$NBT" && nbt_was_clean=1
  return 0
}

# Builds the agent binaries into the sandbox and writes its global config. Sets MODEL, which is
# empty when no model endpoint answers.
sandbox_build() {
  echo "==> building agent binaries"
  (cd orchestrator-go && go build -o "$SANDBOX/bin/civquest-agent" ./cmd/civquest-agent && go build -o "$SANDBOX/bin/cq" ./cmd/cq)
  export PATH="$SANDBOX/bin:$PATH"
  MODEL="$(curl -s -m 5 "$LLM_URL/models" | jq -r '.data[0].id // empty' 2>/dev/null || true)"
  cat > "$XDG_CONFIG_HOME/civquest/global.yaml" <<YAML
llm_gateway:
  base_url: "$LLM_URL"
  model: "${MODEL:-none}"
YAML
}

# sandbox_start_server <gradle argument>...: starts the server on generated terrain, with its
# console on file descriptor 3, and waits until it is ready.
sandbox_start_server() {
  local meta="$SERVER_DIR/civquest-server-meta.json" start_ms
  mkfifo "$FIFO"
  start_ms=$(($(date +%s) * 1000))
  "${DETACH[@]}" ./gradlew --offline --no-daemon -Pcivquest.integrationLevelType=minecraft:normal \
    -Pcivquest.integrationLevelSeed="$SEED" "$@" :mod-neoforge:runIntegrationServer <"$FIFO" >"$SERVER_LOG" 2>&1 &
  GRADLE_PID=$!
  exec 3>"$FIFO"
  for _ in $(seq 180); do
    if [ -f "$meta" ] && [ "$(jq -r .status "$meta" 2>/dev/null)" = READY ] && [ "$(jq -r .timestamp_ms "$meta")" -ge "$start_ms" ]; then break; fi
    kill -0 "$GRADLE_PID" 2>/dev/null || { echo "server exited early" >&2; tail -30 "$SERVER_LOG" >&2; exit 1; }
    pause 1
  done
  [ "$(jq -r .status "$meta")" = READY ] || { echo "server not ready" >&2; tail -30 "$SERVER_LOG" >&2; exit 1; }
  target="$(jq -r '"\(.grpc_host):\(.grpc_port)"' "$meta")"
  key="$(jq -r .shared_key "$meta")"
  server_id="$(jq -r .server_id "$meta")"
}

sandbox_create_agents() {
  local a
  for a in $AGENTS; do cq agent create --name "$a" --clan pioneers >/dev/null; done
}

sandbox_start_supervisor() {
  "${DETACH[@]}" cq daemon start --foreground --agent-bin "$SANDBOX/bin/civquest-agent" --target "$target" --shared-key "$key" --server-id "$server_id" >"$SANDBOX/supervisor.out" 2>&1 &
  SUP_PID=$!
}

# Stops the agents, then the server (which saves the world), and restores the rewritten
# structure. The sandbox is left for the caller to keep, archive or remove.
sandbox_stop() {
  if [ -n "$SUP_PID" ]; then
    kill -TERM "$SUP_PID" 2>/dev/null && wait "$SUP_PID" 2>/dev/null || true
    SUP_PID=""
  fi
  if [ -n "$GRADLE_PID" ]; then
    echo stop >&3 2>/dev/null || true
    for _ in $(seq 60); do
      kill -0 "$GRADLE_PID" 2>/dev/null || break
      # With its console still open Gradle outlives the finished build: a detached run does not
      # sit out the full wait for it.
      if [ ${#DETACH[@]} -gt 0 ] && grep -aqE '^BUILD (SUCCESSFUL|FAILED)' "$SERVER_LOG"; then break; fi
      pause 1
    done
    kill -TERM "$GRADLE_PID" 2>/dev/null || true
    # A detached server has its own process group: leave nothing of it running.
    if [ ${#DETACH[@]} -gt 0 ]; then kill -TERM -- "-$GRADLE_PID" 2>/dev/null || true; fi
    exec 3>&- || true
    GRADLE_PID=""
  fi
  if [ "$nbt_was_clean" = 1 ]; then git show "HEAD:$NBT" >"$NBT"; fi
}
