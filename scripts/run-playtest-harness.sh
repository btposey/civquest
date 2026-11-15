#!/usr/bin/env bash
# ==============================================================================
# CivQuest Automated In-World Playtest Scenario Harness (cq-56x.5)
#
# Runs Tier 3 headless in-world playtest scenarios with multi-agent setup:
# - Able (EXPLORER): Radial frontier scouting >= 48m, sector azimuth lock, zero 137.5° spin
# - boreas (BUILDER): Road paving with campfire 4m exclusion zone, wander threshold 15
# - boreas / Able (BUILDER): Bottom-up cabin assembly, AABB collision safety (zero entombment)
# - aria (CRAFTER): 3x3 crafting reach (<= 4.5m) and 2x2 crafting
# - aria (GATHERER): Item collection with natural drop silence and player gift thank-you
# - Multi-Agent: Day/night cycle progression, nighttime contemplation, vector RAG
#
# Emits deep diagnostic traces:
# - build/playtest/puppeteering.jsonl
# - build/playtest/cognitive_system1.jsonl
# - build/playtest/cognitive_system2.jsonl
# - build/playtest/playtest_trace.jsonl
# - build/playtest/playtest-report.md
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# ------------------------------------------------------------------------------
# Strict XDG Isolation Sandbox: Prevent Automated Tests from Colliding with Player World & Server
# ------------------------------------------------------------------------------
TEST_SANDBOX_DIR="$(mktemp -d "${TMPDIR:-/tmp}/civquest-playtest-xdg-XXXXXX")"
export XDG_CONFIG_HOME="$TEST_SANDBOX_DIR/config"
export XDG_DATA_HOME="$TEST_SANDBOX_DIR/data"
export XDG_STATE_HOME="$TEST_SANDBOX_DIR/state"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME"

MOD_BUILD_DIR="$REPO_ROOT/mod-neoforge/build/integration-server"
META_PATH="$MOD_BUILD_DIR/civquest-server-meta.json"
SERVER_LOG="$MOD_BUILD_DIR/server.log"
FIFO_DIR="${MOD_BUILD_DIR}/stdin_fifo"
SERVER_FIFO="${FIFO_DIR}/console"

OUTPUT_DIR="$REPO_ROOT/build/playtest"
REPORT_FILE="$OUTPUT_DIR/playtest-report.md"

GRADLE_PID=""
SERVER_PID=""
LAUNCHED_SERVER=0
CLEANED_UP=0
FIFO_OPENED=0

is_java_process() {
    local pid="$1"
    if [ -f "/proc/$pid/cmdline" ] && grep -qa "java" "/proc/$pid/cmdline" 2>/dev/null; then
        return 0
    fi
    if [ -f "/proc/$pid/comm" ] && grep -qa "java" "/proc/$pid/comm" 2>/dev/null; then
        return 0
    fi
    if ps -p "$pid" -o comm= 2>/dev/null | grep -q "java"; then
        return 0
    fi
    return 1
}

cleanup() {
    local exit_code=$?
    trap - EXIT INT TERM

    if [ "$CLEANED_UP" -eq 1 ]; then
        exit "$exit_code"
    fi
    CLEANED_UP=1

    if [ "$LAUNCHED_SERVER" -eq 1 ]; then
        echo "==> Shutting down spawned integration server..."
        if [ "$FIFO_OPENED" -eq 1 ]; then
            echo "stop" >&3 2>/dev/null || true
            exec 3>&- 2>/dev/null || true
            FIFO_OPENED=0
        fi

        if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && [ "$GRADLE_PID" -gt 1 ] && kill -0 "$GRADLE_PID" 2>/dev/null; then
            local waited=0
            while [ "$waited" -lt 15 ]; do
                if ! kill -0 "$GRADLE_PID" 2>/dev/null; then
                    break
                fi
                sleep 1
                waited=$((waited + 1))
            done
        fi

        if [[ "${SERVER_PID:-}" =~ ^[0-9]+$ ]] && [ "$SERVER_PID" -gt 1 ] && kill -0 "$SERVER_PID" 2>/dev/null; then
            if is_java_process "$SERVER_PID"; then
                kill -TERM "$SERVER_PID" 2>/dev/null || true
                sleep 2
                if kill -0 "$SERVER_PID" 2>/dev/null; then
                    kill -KILL "$SERVER_PID" 2>/dev/null || true
                fi
            fi
        fi

        if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && [ "$GRADLE_PID" -gt 1 ] && kill -0 "$GRADLE_PID" 2>/dev/null; then
            kill -TERM "$GRADLE_PID" 2>/dev/null || true
            sleep 1
            if kill -0 "$GRADLE_PID" 2>/dev/null; then
                kill -KILL "$GRADLE_PID" 2>/dev/null || true
            fi
        fi
    fi

    if [ -n "$FIFO_DIR" ] && [ -d "$FIFO_DIR" ]; then
        rm -rf "$FIFO_DIR"
    fi

    # Clean up isolated test sandbox
    if [ -n "${TEST_SANDBOX_DIR:-}" ] && [ -d "$TEST_SANDBOX_DIR" ]; then
        rm -rf "$TEST_SANDBOX_DIR"
    fi

    exit "$exit_code"
}

trap cleanup EXIT INT TERM

mkdir -p "$OUTPUT_DIR"

# ------------------------------------------------------------------------------
# Isolated Integration Server Discovery:
# Strictly restricted to the dedicated headless build sandbox ($META_PATH).
# Automated tests NEVER connect to or interfere with the developer's manual playtest server.
# ------------------------------------------------------------------------------
ACTIVE_SERVER=0
ACTIVE_META_PATH=""

if [ -f "$META_PATH" ]; then
    STATUS=$(jq -r '.status // empty' "$META_PATH" 2>/dev/null || true)
    PID=$(jq -r '.server_pid // empty' "$META_PATH" 2>/dev/null || true)
    if [ "$STATUS" = "READY" ] && [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        ACTIVE_SERVER=1
        ACTIVE_META_PATH="$META_PATH"
        echo "==> Reusing active dedicated integration server at $META_PATH (PID: $PID)"
    fi
fi

# If no active integration server found in build sandbox, launch dedicated headless server
if [ "$ACTIVE_SERVER" -eq 0 ]; then
    echo "==> Launching dedicated isolated headless integration server..."
    LAUNCHED_SERVER=1
    mkdir -p "$MOD_BUILD_DIR"
    rm -f "$META_PATH"
    rm -f "$SERVER_LOG"
    touch "$SERVER_LOG"

    mkdir -p "$FIFO_DIR"
    rm -f "$SERVER_FIFO"
    mkfifo "$SERVER_FIFO"
    exec 3<>"$SERVER_FIFO"
    FIFO_OPENED=1

    cd "$REPO_ROOT"
    ./gradlew --no-daemon :mod-neoforge:runIntegrationServer < "$SERVER_FIFO" > "$SERVER_LOG" 2>&1 &
    GRADLE_PID=$!
    echo "==> Server launcher started with PID $GRADLE_PID"

    TIMEOUT_SECS="${INTEGRATION_TIMEOUT_SECS:-180}"
    echo "==> Waiting up to ${TIMEOUT_SECS}s for server discovery metadata..."
    READY=0
    for i in $(seq 1 "$TIMEOUT_SECS"); do
        if [ -f "$META_PATH" ]; then
            STATUS=$(jq -r '.status // empty' "$META_PATH" 2>/dev/null || true)
            if [ "$STATUS" = "READY" ]; then
                READY=1
                SERVER_PID=$(jq -r '.server_pid // empty' "$META_PATH" 2>/dev/null || true)
                echo "==> Server reported READY in ${i}s (Minecraft PID: ${SERVER_PID:-unknown})"
                ACTIVE_META_PATH="$META_PATH"
                break
            fi
        fi

        if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && ! kill -0 "$GRADLE_PID" 2>/dev/null; then
            echo "ERROR: Server runner process terminated prematurely!"
            if [ -f "$SERVER_LOG" ]; then
                tail -n 40 "$SERVER_LOG"
            fi
            exit 1
        fi
        sleep 1
    done

    if [ "$READY" -ne 1 ]; then
        echo "ERROR: Timed out waiting for server discovery metadata."
        if [ -f "$SERVER_LOG" ]; then
            tail -n 40 "$SERVER_LOG"
        fi
        exit 1
    fi
fi

# ------------------------------------------------------------------------------
# Execute Playtest Scenario Test Suite in orchestrator-go
# ------------------------------------------------------------------------------
echo "==> Executing Automated In-World Playtest Scenario Test Suite..."
(
    cd "$REPO_ROOT/orchestrator-go"
    CIVQUEST_DISCOVERY_PATH="$ACTIVE_META_PATH" \
    CIVQUEST_REQUIRE_LIVE_SERVER=true \
    go test -timeout=180s -count=1 -v -tags=integration -run=TestInWorldPlaytestScenarios ./test/integration/...
)

echo "==> Playtest scenario suite executed successfully!"

# ------------------------------------------------------------------------------
# Display Diagnostic Report
# ------------------------------------------------------------------------------
if [ -f "$REPORT_FILE" ]; then
    echo ""
    echo "=============================================================================="
    echo " CIVQUEST IN-WORLD PLAYTEST DIAGNOSTIC REPORT"
    echo "=============================================================================="
    cat "$REPORT_FILE"
    echo "=============================================================================="
    echo "Report file: $REPORT_FILE"
fi
