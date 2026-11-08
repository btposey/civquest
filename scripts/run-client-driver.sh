#!/usr/bin/env bash
# ==============================================================================
# CivQuest Client Driver Harness Runner
#
# Launches the Minecraft 1.21.1 NeoForge client under the automated driver
# test harness with:
# - User profile: --username <user> (default: pbots)
# - Auto-connect: --quickPlayMultiplayer <host>:<port> (default: 127.0.0.1:25565)
# - Automated scenario: look at settler avatar, whisper chat command, simulate
#   movement/jump/sneak, and capture high-resolution screenshots.
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SERVER_HOST="${SERVER_HOST:-127.0.0.1}"
SERVER_PORT="${SERVER_PORT:-25565}"
USERNAME="${USERNAME:-pbots}"
TARGET_SETTLER="${TARGET_SETTLER:-aria}"
SCREENSHOTS_DIR="${SCREENSHOTS_DIR:-$REPO_ROOT/build/driver-screenshots}"
AUTO_EXIT="${AUTO_EXIT:-true}"
ACTIONS="${ACTIONS:-}"

usage() {
    cat << EOF
Usage: $(basename "$0") [options]

Options:
  --host <host>          Target server hostname (default: 127.0.0.1)
  --port <port>          Target server port (default: 25565)
  --username <user>      Client username / profile (default: pbots)
  --target <name>        Settler avatar to target (default: aria)
  --screenshots-dir <d>  Output directory for captured screenshots (default: build/driver-screenshots)
  --no-exit              Keep client open after driver scenario completes (default: auto-exit)
  --actions <spec>       Custom driver action pipeline (e.g. "wait:40,look,screenshot:view.png,exit")
  -h, --help             Show this help message

Environment variables:
  SERVER_HOST, SERVER_PORT, USERNAME, TARGET_SETTLER, SCREENSHOTS_DIR, AUTO_EXIT, ACTIONS

Examples:
  # Launch automated driver and capture screenshots against local server:
  ./scripts/run-client-driver.sh

  # Connect to custom port with interactive inspection (stay open):
  ./scripts/run-client-driver.sh --port 25565 --no-exit
EOF
    exit 0
}

while [ $# -gt 0 ]; do
    case "$1" in
        --host)
            SERVER_HOST="$2"; shift 2 ;;
        --port)
            SERVER_PORT="$2"; shift 2 ;;
        --username)
            USERNAME="$2"; shift 2 ;;
        --target)
            TARGET_SETTLER="$2"; shift 2 ;;
        --screenshots-dir)
            SCREENSHOTS_DIR="$2"; shift 2 ;;
        --no-exit)
            AUTO_EXIT="false"; shift 1 ;;
        --actions)
            ACTIONS="$2"; shift 2 ;;
        -h|--help)
            usage ;;
        *)
            echo "Unknown option: $1" >&2
            usage ;;
    esac
done

log() {
    echo -e "\033[1;36m[civquest-driver]\033[0m $*"
}

# 1. Prerequisite checks
command -v java >/dev/null 2>&1 || {
    echo "ERROR: Java 21 is required on PATH." >&2
    exit 1
}

mkdir -p "$SCREENSHOTS_DIR"

log "=================================================================="
log "CivQuest Client Driver Harness Launcher"
log "=================================================================="
log "  Target Server  : ${SERVER_HOST}:${SERVER_PORT}"
log "  Username       : ${USERNAME}"
log "  Target Settler : ${TARGET_SETTLER}"
log "  Screenshots Dir: ${SCREENSHOTS_DIR}"
log "  Auto Exit      : ${AUTO_EXIT}"
if [ -n "$ACTIONS" ]; then
    log "  Custom Actions : ${ACTIONS}"
fi
log "=================================================================="

# 2. Check display environment and server availability
if [ -z "${DISPLAY:-}" ] && [ -z "${WAYLAND_DISPLAY:-}" ]; then
    if command -v xvfb-run >/dev/null 2>&1; then
        log "Notice: No display detected. Using xvfb-run for headless virtual framebuffer."
        XVFB_CMD="xvfb-run -a"
    else
        log "⚠️ Warning: No DISPLAY or WAYLAND_DISPLAY detected, and xvfb-run is not installed."
        log "   Minecraft graphical client requires a display server to initialize OpenGL/GLFW."
        log "   Install xvfb (sudo apt install xvfb) or run in an active desktop session."
    fi
fi

if (exec 3<>/dev/tcp/"$SERVER_HOST"/"$SERVER_PORT") 2>/dev/null; then
    exec 3<&-
    exec 3>&-
    log "✓ Dedicated Minecraft server verified online at ${SERVER_HOST}:${SERVER_PORT}"
else
    log "⚠️ Notice: Port ${SERVER_PORT} on ${SERVER_HOST} is not currently listening."
    log "   Make sure a CivQuest server is started before connecting, e.g.:"
    log "   ./scripts/start-playtest-server.sh"
    log "   Proceeding with launch (driver watchdog will timeout after 30s if unjoined)..."
fi

# 3. Construct Gradle run arguments
GRADLE_PROPS=(
    "-Dcivquest.driver.enabled=true"
    "-Dcivquest.driver.target=${TARGET_SETTLER}"
    "-Dcivquest.driver.screenshots.dir=${SCREENSHOTS_DIR}"
    "-Dcivquest.driver.exitOnComplete=${AUTO_EXIT}"
)

if [ -n "$ACTIONS" ]; then
    GRADLE_PROPS+=("-Dcivquest.driver.actions=${ACTIONS}")
fi

CLIENT_ARGS="--username ${USERNAME} --quickPlayMultiplayer ${SERVER_HOST}:${SERVER_PORT}"

log "Starting Minecraft NeoForge client via Gradle (:mod-neoforge:runClient)..."
TIMEOUT_CMD=""
if [ "$AUTO_EXIT" = "true" ] && command -v timeout >/dev/null 2>&1; then
    TIMEOUT_SECS="${CLIENT_TIMEOUT_SECS:-120s}"
    TIMEOUT_CMD="timeout --kill-after=10s ${TIMEOUT_SECS}"
fi

(
    cd "$REPO_ROOT"
    ${XVFB_CMD:-} ${TIMEOUT_CMD} ./gradlew :mod-neoforge:runClient \
        --args="$CLIENT_ARGS" \
        "${GRADLE_PROPS[@]}"
)

# 4. Report captured screenshots
log "Client execution finished."
if [ -d "$SCREENSHOTS_DIR" ]; then
    SCREENSHOT_COUNT="$(find "$SCREENSHOTS_DIR" -type f -name "*.png" | wc -l)"
    if [ "$SCREENSHOT_COUNT" -gt 0 ]; then
        log "Captured $SCREENSHOT_COUNT screenshot(s) in $SCREENSHOTS_DIR:"
        find "$SCREENSHOTS_DIR" -type f -name "*.png" -exec ls -lh {} +
    else
        log "No screenshots found in $SCREENSHOTS_DIR (client may not have joined world or completed scenario)."
    fi
fi
