#!/usr/bin/env bash
# ==============================================================================
# CivQuest Tier 3 Multi-Tier In-Game Integration Test Runner
#
# Orchestrates:
# 1. Spawning headless Minecraft dedicated server in background (:mod-neoforge:runIntegrationServer)
# 2. Polling discovery metadata (civquest-server-meta.json) for READY state (up to 60s)
# 3. Executing Go synthetic integration test probe (//go:build integration)
# 4. Gracefully stopping server via stdin ("stop\n") and verifying clean exit code
# ==============================================================================

set -euo pipefail

# ------------------------------------------------------------------------------
# Prerequisite checks
# ------------------------------------------------------------------------------
command -v jq >/dev/null 2>&1 || {
    echo "ERROR: 'jq' is required on PATH for discovery JSON parsing." >&2
    exit 1
}

# ------------------------------------------------------------------------------
# Early variable initialization & trap registration
# ------------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MOD_BUILD_DIR="$REPO_ROOT/mod-neoforge/build/integration-server"
META_PATH="$MOD_BUILD_DIR/civquest-server-meta.json"
SERVER_LOG="$MOD_BUILD_DIR/server.log"

FIFO_DIR="${MOD_BUILD_DIR}/stdin_fifo"
SERVER_FIFO="${FIFO_DIR}/console"

GRADLE_PID=""
SERVER_PID=""
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

    echo "==> Tearing down integration server..."

    # 1. Send clean "stop" command to Minecraft dedicated server console
    if [ "$FIFO_OPENED" -eq 1 ]; then
        echo "stop" >&3 2>/dev/null || true
        exec 3>&- 2>/dev/null || true
        FIFO_OPENED=0
    fi

    # 2. Wait up to 15 seconds for Gradle runner to finish gracefully
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

    # 3. Check Minecraft server JVM PID if discovered and validate process
    if [[ "${SERVER_PID:-}" =~ ^[0-9]+$ ]] && [ "$SERVER_PID" -gt 1 ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        if is_java_process "$SERVER_PID"; then
            echo "Minecraft server PID $SERVER_PID still running; sending SIGTERM..."
            kill -TERM "$SERVER_PID" 2>/dev/null || true
            sleep 2
            if kill -0 "$SERVER_PID" 2>/dev/null; then
                echo "Minecraft server PID $SERVER_PID did not terminate; sending SIGKILL..."
                kill -KILL "$SERVER_PID" 2>/dev/null || true
            fi
        else
            echo "PID $SERVER_PID does not match a Java process; skipping signal."
        fi
    fi

    # 4. Process tree reaping: ensure any child processes of Gradle runner are terminated
    if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && [ "$GRADLE_PID" -gt 1 ]; then
        local child_pids
        child_pids=$(pgrep -P "$GRADLE_PID" 2>/dev/null || true)
        for cpid in $child_pids; do
            if [[ "$cpid" =~ ^[0-9]+$ ]] && [ "$cpid" -gt 1 ] && kill -0 "$cpid" 2>/dev/null; then
                if is_java_process "$cpid"; then
                    echo "Reaping child Java process $cpid of Gradle runner..."
                    kill -TERM "$cpid" 2>/dev/null || true
                    sleep 1
                    if kill -0 "$cpid" 2>/dev/null; then
                        kill -KILL "$cpid" 2>/dev/null || true
                    fi
                fi
            fi
        done
    fi

    # Also reap any orphaned integration-server JVM processes matching discovery path
    local lingering_pids
    lingering_pids=$(pgrep -f "civquest.discovery.path" 2>/dev/null || true)
    for lpid in $lingering_pids; do
        if [[ "$lpid" =~ ^[0-9]+$ ]] && [ "$lpid" -gt 1 ] && [ "$lpid" != "$$" ] && kill -0 "$lpid" 2>/dev/null; then
            if is_java_process "$lpid"; then
                echo "Reaping orphaned integration server JVM PID $lpid..."
                kill -TERM "$lpid" 2>/dev/null || true
                sleep 1
                if kill -0 "$lpid" 2>/dev/null; then
                    kill -KILL "$lpid" 2>/dev/null || true
                fi
            fi
        fi
    done

    # 5. Check Gradle runner PID
    if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && [ "$GRADLE_PID" -gt 1 ] && kill -0 "$GRADLE_PID" 2>/dev/null; then
        echo "Gradle runner PID $GRADLE_PID still running; sending SIGTERM..."
        kill -TERM "$GRADLE_PID" 2>/dev/null || true
        sleep 2
        if kill -0 "$GRADLE_PID" 2>/dev/null; then
            echo "Gradle runner PID $GRADLE_PID did not terminate; sending SIGKILL..."
            kill -KILL "$GRADLE_PID" 2>/dev/null || true
        fi
    fi

    # 6. Clean up FIFO directory
    if [ -n "$FIFO_DIR" ] && [ -d "$FIFO_DIR" ]; then
        rm -rf "$FIFO_DIR"
    fi

    if [ "$exit_code" -ne 0 ]; then
        echo "==> Integration tests failed or terminated with code $exit_code."
        if [ -f "$SERVER_LOG" ]; then
            echo "==> Last 50 lines of server log ($SERVER_LOG):"
            tail -n 50 "$SERVER_LOG" || true
        fi
    else
        echo "==> Integration tests completed successfully."
    fi

    exit "$exit_code"
}

trap cleanup EXIT INT TERM

# ------------------------------------------------------------------------------
# Sandbox & FIFO preparation
# ------------------------------------------------------------------------------
cd "$REPO_ROOT"
mkdir -p "$MOD_BUILD_DIR"

# Clean up stale discovery metadata and logs from prior runs
rm -f "$META_PATH"
rm -f "$SERVER_LOG"
touch "$SERVER_LOG"

# Setup FIFO inside build sandbox for non-blocking interactive stdin communication
mkdir -p "$FIFO_DIR"
rm -f "$SERVER_FIFO"
mkfifo "$SERVER_FIFO"
exec 3<>"$SERVER_FIFO"
FIFO_OPENED=1

# ------------------------------------------------------------------------------
# Launch headless Minecraft server with --no-daemon
# ------------------------------------------------------------------------------
echo "==> Launching headless Minecraft server..."
./gradlew --no-daemon :mod-neoforge:runIntegrationServer < "$SERVER_FIFO" > "$SERVER_LOG" 2>&1 &
GRADLE_PID=$!
echo "==> Server runner launched with PID $GRADLE_PID"

# ------------------------------------------------------------------------------
# Wait for discovery metadata
# ------------------------------------------------------------------------------
echo "==> Waiting for discovery metadata at $META_PATH (timeout 60s)..."
READY=0
for i in $(seq 1 60); do
    if [ -f "$META_PATH" ]; then
        STATUS=$(jq -r '.status // empty' "$META_PATH" 2>/dev/null || true)
        if [ "$STATUS" = "READY" ]; then
            READY=1
            SERVER_PID=$(jq -r '.server_pid // empty' "$META_PATH" 2>/dev/null || true)
            echo "==> Server reported READY in ${i}s (Minecraft PID: ${SERVER_PID:-unknown})"
            break
        fi
    fi

    if [[ "${GRADLE_PID:-}" =~ ^[0-9]+$ ]] && ! kill -0 "$GRADLE_PID" 2>/dev/null; then
        echo "ERROR: Gradle server runner process terminated prematurely!"
        exit 1
    fi

    sleep 1
done

if [ "$READY" -ne 1 ]; then
    echo "ERROR: Timed out waiting 60s for server discovery metadata READY status."
    exit 1
fi

# ------------------------------------------------------------------------------
# Execute Go integration probe suite
# ------------------------------------------------------------------------------
echo "==> Executing Go integration probe suite..."
(
    cd "$REPO_ROOT/orchestrator-go"
    CIVQUEST_DISCOVERY_PATH="$META_PATH" \
    CIVQUEST_REQUIRE_LIVE_SERVER=true \
    go test -timeout=60s -count=1 -v -tags=integration ./test/integration/...
)

echo "==> Probe suite passed!"
