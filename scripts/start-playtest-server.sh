#!/usr/bin/env bash
# ==============================================================================
# CivQuest Playtest Dedicated Server Launcher
#
# Prepares the NeoForge server environment for client playtesting:
# - Pre-accepts Mojang EULA (eula=true)
# - Configures server.properties (server-port=25565, online-mode=false)
# - Enforces GameRule mobGriefing false via CivQuest mod
# - Launches the Minecraft NeoForge server for local client connections
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PORT="${PORT:-25565}"
ONLINE_MODE="${ONLINE_MODE:-false}"
MODE="${1:-dev}"

log() {
    echo -e "\033[1;34m[civquest-playtest]\033[0m $*"
}

# 1. Prerequisite checks
command -v java >/dev/null 2>&1 || {
    echo "ERROR: Java 21 is required on PATH." >&2
    exit 1
}

JAVA_VER="$(java -version 2>&1 | head -n 1 | awk -F '"' '{print $2}' | cut -d'.' -f1)"
if [ "$JAVA_VER" -lt 21 ]; then
    echo "ERROR: Java 21 or greater is required (found Java $JAVA_VER)." >&2
    exit 1
fi

log "Preparing playtesting environment on port ${PORT} (online-mode: ${ONLINE_MODE})..."

# 2. Setup Dev Run Directory (mod-neoforge/run)
RUN_DIR="${REPO_ROOT}/mod-neoforge/run"
mkdir -p "${RUN_DIR}/mods"

# Pre-accept EULA
cat > "${RUN_DIR}/eula.txt" << 'EOF'
# By changing the setting below to TRUE you are indicating your agreement to the Mojang EULA (https://aka.ms/MinecraftEULA).
eula=true
EOF

# Write or ensure server.properties has port 25565 and online-mode
if [ ! -f "${RUN_DIR}/server.properties" ]; then
    cat > "${RUN_DIR}/server.properties" << EOF
level-name=world
pause-when-empty-seconds=-1
online-mode=${ONLINE_MODE}
server-port=${PORT}
enable-rcon=false
difficulty=normal
gamemode=survival
max-players=20
sync-chunk-writes=true
view-distance=10
simulation-distance=10
motd=CivQuest Autonomous Settlement Server
EOF
else
    # Update server-port and online-mode if existing
    sed -i "s/^server-port=.*/server-port=${PORT}/" "${RUN_DIR}/server.properties" || true
    sed -i "s/^online-mode=.*/online-mode=${ONLINE_MODE}/" "${RUN_DIR}/server.properties" || true
fi

log "EULA pre-accepted (eula=true) in ${RUN_DIR}/eula.txt"
log "server.properties configured for localhost:${PORT}"

# 3. Launch Server
if [ "${MODE}" = "standalone" ]; then
    log "Starting standalone XDG server via civctl..."
    "${REPO_ROOT}/bin/civctl" server setup --port "${PORT}" --start
else
    log "Launching Minecraft 1.21.1 NeoForge server via Gradle (:mod-neoforge:runServer)..."
    log "Players can connect in Minecraft client: Direct Connection -> localhost:${PORT}"
    cd "${REPO_ROOT}"
    exec ./gradlew :mod-neoforge:runServer --console=plain "$@"
fi
