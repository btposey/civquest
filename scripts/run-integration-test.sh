#!/usr/bin/env bash
# ==============================================================================
# CivQuest Tier 3 Multi-Tier In-Game Integration Test Runner
#
# Orchestrates:
# 1. Preparing sandbox world configuration (:mod-neoforge:prepareIntegrationServer)
# 2. Spawning headless Minecraft dedicated server in background (:mod-neoforge:runIntegrationServer)
# 3. Polling discovery metadata (civquest-server-meta.json) for READY state (up to 60s)
# 4. Executing Go synthetic integration test probe (//go:build integration)
# 5. Gracefully stopping server via stdin ("stop\n") and verifying clean exit code
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

MOD_BUILD_DIR="$REPO_ROOT/mod-neoforge/build/integration-server"
ROOT_BUILD_DIR="$REPO_ROOT/build/integration-server"
mkdir -p "$MOD_BUILD_DIR"
mkdir -p "$ROOT_BUILD_DIR"

META_PATH="$MOD_BUILD_DIR/civquest-server-meta.json"
SERVER_LOG="$MOD_BUILD_DIR/server.log"
ROOT_LOG="$ROOT_BUILD_DIR/server.log"

# Clean up stale discovery metadata and logs from prior runs
rm -f "$META_PATH"
rm -f "$SERVER_LOG"
rm -f "$ROOT_LOG"
rm -f "$ROOT_BUILD_DIR/civquest-server-meta.json"

touch "$SERVER_LOG"
ln -sf "$SERVER_LOG" "$ROOT_LOG"

echo "==> Preparing integration server sandbox..."
./gradlew :mod-neoforge:prepareIntegrationServer

# Setup FIFO for non-blocking interactive stdin communication
FIFO_DIR=$(mktemp -d /tmp/civquest-fifo.XXXXXX)
SERVER_FIFO="$FIFO_DIR/server_stdin"
mkfifo "$SERVER_FIFO"
exec 3<>"$SERVER_FIFO"

GRADLE_PID=""
SERVER_PID=""
CLEANED_UP=0

cleanup() {
    local exit_code=$?
    trap - EXIT INT TERM

    if [ "$CLEANED_UP" -eq 1 ]; then
        exit "$exit_code"
    fi
    CLEANED_UP=1

    echo "==> Tearing down integration server..."

    # 1. Send clean "stop" command to Minecraft dedicated server console
    if [ -e "$SERVER_FIFO" ]; then
        echo "stop" >&3 2>/dev/null || true
        exec 3>&- 2>/dev/null || true
    fi

    # 2. Wait up to 15 seconds for Gradle runner to finish gracefully
    if [ -n "$GRADLE_PID" ] && kill -0 "$GRADLE_PID" 2>/dev/null; then
        local waited=0
        while [ "$waited" -lt 15 ]; do
            if ! kill -0 "$GRADLE_PID" 2>/dev/null; then
                break
            fi
            sleep 1
            waited=$((waited + 1))
        done
    fi

    # 3. Check Minecraft server JVM PID from discovery metadata
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        echo "Minecraft server PID $SERVER_PID still running; sending SIGTERM..."
        kill -TERM "$SERVER_PID" 2>/dev/null || true
        sleep 2
        if kill -0 "$SERVER_PID" 2>/dev/null; then
            echo "Minecraft server PID $SERVER_PID did not terminate; sending SIGKILL..."
            kill -KILL "$SERVER_PID" 2>/dev/null || true
        fi
    fi

    # 4. Check Gradle runner PID
    if [ -n "$GRADLE_PID" ] && kill -0 "$GRADLE_PID" 2>/dev/null; then
        echo "Gradle runner PID $GRADLE_PID still running; sending SIGTERM..."
        kill -TERM "$GRADLE_PID" 2>/dev/null || true
        sleep 2
        if kill -0 "$GRADLE_PID" 2>/dev/null; then
            echo "Gradle runner PID $GRADLE_PID did not terminate; sending SIGKILL..."
            kill -KILL "$GRADLE_PID" 2>/dev/null || true
        fi
    fi

    # Ensure server.log is copied to root build directory as a physical file
    if [ -f "$SERVER_LOG" ]; then
        cp -f "$SERVER_LOG" "$ROOT_LOG" 2>/dev/null || true
    fi

    rm -rf "$FIFO_DIR"

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

echo "==> Launching headless Minecraft server..."
./gradlew :mod-neoforge:runIntegrationServer < "$SERVER_FIFO" > "$SERVER_LOG" 2>&1 &
GRADLE_PID=$!
echo "==> Server runner launched with PID $GRADLE_PID"

echo "==> Waiting for discovery metadata at $META_PATH (timeout 60s)..."
READY=0
for i in $(seq 1 60); do
    if [ -f "$META_PATH" ]; then
        STATUS=$(jq -r '.status // empty' "$META_PATH" 2>/dev/null || true)
        if [ "$STATUS" = "READY" ]; then
            READY=1
            SERVER_PID=$(jq -r '.server_pid // empty' "$META_PATH" 2>/dev/null || true)
            ln -sf "$META_PATH" "$ROOT_BUILD_DIR/civquest-server-meta.json"
            echo "==> Server reported READY in ${i}s (Minecraft PID: ${SERVER_PID:-unknown})"
            break
        fi
    fi

    if ! kill -0 "$GRADLE_PID" 2>/dev/null; then
        echo "ERROR: Gradle server runner process terminated prematurely!"
        exit 1
    fi

    sleep 1
done

if [ "$READY" -ne 1 ]; then
    echo "ERROR: Timed out waiting 60s for server discovery metadata READY status."
    exit 1
fi

echo "==> Executing Go integration probe suite..."
(
    cd "$REPO_ROOT/orchestrator-go"
    CIVQUEST_DISCOVERY_PATH="$META_PATH" \
    CIVQUEST_REQUIRE_LIVE_SERVER=true \
    go test -count=1 -v -tags=integration ./test/integration/...
)

echo "==> Probe suite passed!"
