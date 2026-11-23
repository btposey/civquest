#!/usr/bin/env bash
set -euo pipefail
umask 0027

# ==============================================================================
# CivQuest Developer Build & Deploy Script
#
# Compiles the NeoForge mod and Go orchestrator binaries from local source,
# verifies Jar-in-Jar packaging, and deploys everything to the standard XDG
# Base Directory installation hierarchy ($XDG_CONFIG_HOME, $XDG_DATA_HOME,
# $XDG_STATE_HOME, and $INSTALL_BIN_DIR / ~/.local/bin).
# ==============================================================================

# Resolve physical script path even if invoked via a symlink
SOURCE="${BASH_SOURCE[0]}"
while [ -h "${SOURCE}" ]; do
    DIR="$(cd -P "$(dirname "${SOURCE}")" && pwd)"
    SOURCE="$(readlink "${SOURCE}")"
    [[ "${SOURCE}" != /* ]] && SOURCE="${DIR}/${SOURCE}"
done
SCRIPT_DIR="$(cd -P "$(dirname "${SOURCE}")" && pwd)"
ROOT_DIR="$(cd -P "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

# ------------------------------------------------------------------------------
# Terminal Formatting
# ------------------------------------------------------------------------------
if [ -t 1 ]; then
    COLOR_RESET="$(printf '\033[0m')"
    COLOR_BOLD="$(printf '\033[1m')"
    COLOR_GREEN="$(printf '\033[32m')"
    COLOR_BLUE="$(printf '\033[34m')"
    COLOR_YELLOW="$(printf '\033[33m')"
    COLOR_RED="$(printf '\033[31m')"
    COLOR_CYAN="$(printf '\033[36m')"
    COLOR_MAGENTA="$(printf '\033[35m')"
else
    COLOR_RESET=""
    COLOR_BOLD=""
    COLOR_GREEN=""
    COLOR_BLUE=""
    COLOR_YELLOW=""
    COLOR_RED=""
    COLOR_CYAN=""
    COLOR_MAGENTA=""
fi

log_info() {
    echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

log_step() {
    echo -e "${COLOR_CYAN}${COLOR_BOLD}==>${COLOR_RESET} ${COLOR_BOLD}$*${COLOR_RESET}"
}

log_success() {
    echo -e "${COLOR_GREEN}${COLOR_BOLD}[SUCCESS]${COLOR_RESET} $*"
}

log_warn() {
    echo -e "${COLOR_YELLOW}[WARN]${COLOR_RESET} $*"
}

log_error() {
    echo -e "${COLOR_RED}${COLOR_BOLD}[ERROR]${COLOR_RESET} $*" >&2
}

# ------------------------------------------------------------------------------
# Usage & Help
# ------------------------------------------------------------------------------
usage() {
    cat <<EOF
${COLOR_BOLD}CivQuest Developer Build & Deploy Script${COLOR_RESET}

${COLOR_BOLD}Usage:${COLOR_RESET}
  $0 [options]

${COLOR_BOLD}Description:${COLOR_RESET}
  Builds the latest NeoForge mod and Go orchestrator binaries from current
  source code and deploys them to the standard XDG directory tree for local
  playtesting.

${COLOR_BOLD}Options:${COLOR_RESET}
  -f, --force                Overwrite existing configuration files (global.yaml, server.properties)
  --skip-build               Skip compilation and deploy existing build artifacts
  --skip-server-runtime      Skip downloading/installing the official NeoForge dedicated server
  --offline                  Offline mode: skip network downloads
  -h, --help                 Show this help message

${COLOR_BOLD}XDG Base Directory Environment Overrides:${COLOR_RESET}
  XDG_CONFIG_HOME  Base configuration path (default: ~/.config)
  XDG_DATA_HOME    Base data path (default: ~/.local/share)
  XDG_STATE_HOME   Base state path (default: ~/.local/state)
  INSTALL_BIN_DIR  Binary installation directory (default: ~/.local/bin)

EOF
}

# ------------------------------------------------------------------------------
# Option Parsing
# ------------------------------------------------------------------------------
FORCE_OVERWRITE=false
SKIP_BUILD=false
SKIP_SERVER_RUNTIME=false
OFFLINE_MODE="${CIVQUEST_OFFLINE:-0}"

while [ $# -gt 0 ]; do
    case "$1" in
        -f|--force)
            FORCE_OVERWRITE=true
            shift
            ;;
        --skip-build)
            SKIP_BUILD=true
            shift
            ;;
        --skip-server-runtime)
            SKIP_SERVER_RUNTIME=true
            shift
            ;;
        --offline)
            OFFLINE_MODE=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
done

# ------------------------------------------------------------------------------
# Environment Validation & XDG Path Resolution
# ------------------------------------------------------------------------------
if [ -z "${HOME:-}" ]; then
    HOME="$(getent passwd "$(id -u)" 2>/dev/null | cut -d: -f6 || true)"
    if [ -z "${HOME}" ] || [ ! -d "${HOME}" ]; then
        HOME="/tmp"
    fi
fi

CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"
STATE_HOME="${XDG_STATE_HOME:-${HOME}/.local/state}"
BIN_DIR="${INSTALL_BIN_DIR:-${HOME}/.local/bin}"

CIVQUEST_CONFIG_DIR="${CONFIG_HOME}/civquest"
CIVQUEST_DATA_DIR="${DATA_HOME}/civquest"
CIVQUEST_STATE_DIR="${STATE_HOME}/civquest"
CIVQUEST_SERVER_DIR="${CIVQUEST_DATA_DIR}/server"
CIVQUEST_CLIENT_DIR="${CIVQUEST_DATA_DIR}/client"

NEOFORGE_VERSION="21.1.265"

echo -e "${COLOR_MAGENTA}${COLOR_BOLD}======================================================================${COLOR_RESET}"
echo -e "${COLOR_MAGENTA}${COLOR_BOLD} CivQuest Developer Build & Deploy${COLOR_RESET}"
echo -e "${COLOR_MAGENTA}${COLOR_BOLD}======================================================================${COLOR_RESET}"
log_info "Source Directory:   ${ROOT_DIR}"
log_info "Config Directory:   ${CIVQUEST_CONFIG_DIR}"
log_info "Data Directory:     ${CIVQUEST_DATA_DIR}"
log_info "State Directory:    ${CIVQUEST_STATE_DIR}"
log_info "Bin Directory:      ${BIN_DIR}"
log_info "Server Directory:   ${CIVQUEST_SERVER_DIR}"
log_info "NeoForge Version:   ${NEOFORGE_VERSION}"
echo ""

# ------------------------------------------------------------------------------
# Step 1: Build NeoForge Java Mod & Go Orchestrator
# ------------------------------------------------------------------------------
if [ "${SKIP_BUILD}" = "true" ]; then
    log_step "[1/4] Skipping build step (--skip-build specified)..."
else
    log_step "[1/4] Building mod and Go binaries from source..."

    # Check required toolchains
    command -v java >/dev/null 2>&1 || { log_error "Java 21+ is required to build the mod."; exit 1; }
    command -v go >/dev/null 2>&1 || { log_error "Go 1.25+ is required to build orchestrator-go."; exit 1; }

    # Build Java Mod
    log_info "Compiling NeoForge mod and packaging Jar-in-Jar..."
    ./gradlew :mod-neoforge:build

    # Build Go Binaries
    log_info "Compiling Go binaries (cq and civquest-agent)..."
    (
        cd "${ROOT_DIR}/orchestrator-go"
        go build -o "${ROOT_DIR}/orchestrator-go/build/cq" ./cmd/cq
        go build -o "${ROOT_DIR}/orchestrator-go/build/civquest-agent" ./cmd/civquest-agent
    )
    log_success "Build completed successfully."
fi

# Locate built artifacts
MOD_JAR_PATH="$(find "${ROOT_DIR}/mod-neoforge/build/libs" -maxdepth 1 -name "*.jar" ! -name "*-sources.jar" | head -n 1 || true)"
if [ -z "${MOD_JAR_PATH}" ] || [ ! -f "${MOD_JAR_PATH}" ]; then
    log_error "Mod jar could not be found in mod-neoforge/build/libs!"
    exit 1
fi
MOD_JAR_NAME="$(basename "${MOD_JAR_PATH}")"

CQ_BIN="${ROOT_DIR}/orchestrator-go/build/cq"
AGENT_BIN="${ROOT_DIR}/orchestrator-go/build/civquest-agent"
if [ ! -f "${CQ_BIN}" ] || [ ! -f "${AGENT_BIN}" ]; then
    log_error "Go binaries not found in orchestrator-go/build!"
    exit 1
fi

echo ""

# ------------------------------------------------------------------------------
# Step 2: Initialize XDG Directory Hierarchy
# ------------------------------------------------------------------------------
log_step "[2/4] Initializing XDG directory skeleton..."

mkdir -p "${BIN_DIR}"
mkdir -p "${CIVQUEST_DATA_DIR}/bin"
mkdir -p "${CIVQUEST_DATA_DIR}/cache"
mkdir -p "${CIVQUEST_DATA_DIR}/agents"
mkdir -p "${CIVQUEST_DATA_DIR}/clans/pioneers"
mkdir -p "${CIVQUEST_DATA_DIR}/graveyard"
mkdir -p "${CIVQUEST_SERVER_DIR}/mods"
mkdir -p "${CIVQUEST_CONFIG_DIR}/agents"
mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/official"
mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/player_imported"
mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/llm_generated"
mkdir -p "${CIVQUEST_STATE_DIR}/agents"
mkdir -p "${CIVQUEST_STATE_DIR}/logs"
mkdir -p "${CIVQUEST_STATE_DIR}/run"
chmod 0700 "${CIVQUEST_STATE_DIR}/run"

log_success "XDG directory skeleton verified."
echo ""

# ------------------------------------------------------------------------------
# Step 3: Deploy Artifacts & Default Configurations
# ------------------------------------------------------------------------------
log_step "[3/4] Deploying binaries, mod JAR, and configurations..."

# 3a. Deploy Go Binaries
log_info "Installing cq and civquest-agent to ${BIN_DIR}..."
cp -f "${CQ_BIN}" "${BIN_DIR}/cq"
cp -f "${AGENT_BIN}" "${BIN_DIR}/civquest-agent"
chmod +x "${BIN_DIR}/cq" "${BIN_DIR}/civquest-agent"

# Symlink civctl -> cq
ln -sf "cq" "${BIN_DIR}/civctl"

log_info "Installing binaries to ${CIVQUEST_DATA_DIR}/bin..."
cp -f "${CQ_BIN}" "${CIVQUEST_DATA_DIR}/bin/cq"
cp -f "${AGENT_BIN}" "${CIVQUEST_DATA_DIR}/bin/civquest-agent"
chmod +x "${CIVQUEST_DATA_DIR}/bin/cq" "${CIVQUEST_DATA_DIR}/bin/civquest-agent"
ln -sf "cq" "${CIVQUEST_DATA_DIR}/bin/civctl"

# 3b. Deploy Mod JAR to Server (and clean up stale versions)
log_info "Deploying mod JAR to ${CIVQUEST_SERVER_DIR}/mods/${MOD_JAR_NAME}..."
# Remove any existing jars to avoid multiple mod versions loading simultaneously
find "${CIVQUEST_SERVER_DIR}/mods" -maxdepth 1 -name "*.jar" ! -name "${MOD_JAR_NAME}" -exec rm -f {} +
cp -f "${MOD_JAR_PATH}" "${CIVQUEST_SERVER_DIR}/mods/${MOD_JAR_NAME}"

# If client directory exists, synchronize the mod JAR there as well
if [ -d "${CIVQUEST_CLIENT_DIR}/mods" ]; then
    log_info "Synchronizing mod JAR to client directory (${CIVQUEST_CLIENT_DIR}/mods)..."
    find "${CIVQUEST_CLIENT_DIR}/mods" -maxdepth 1 -name "*.jar" ! -name "${MOD_JAR_NAME}" -exec rm -f {} +
    cp -f "${MOD_JAR_PATH}" "${CIVQUEST_CLIENT_DIR}/mods/${MOD_JAR_NAME}"
fi

# 3c. Server Runner Configuration
log_info "Ensuring server eula.txt (eula=true)..."
cat > "${CIVQUEST_SERVER_DIR}/eula.txt" << 'EOF'
# By changing the setting below to TRUE you are indicating your agreement to the Mojang EULA (https://aka.ms/MinecraftEULA).
eula=true
EOF

if [ ! -f "${CIVQUEST_SERVER_DIR}/server.properties" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
    log_info "Writing default server.properties..."
    cat > "${CIVQUEST_SERVER_DIR}/server.properties" << 'EOF'
# CivQuest Dedicated Server Properties
level-name=world
pause-when-empty-seconds=-1
online-mode=false
server-port=25565
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
    log_info "Preserving existing server.properties (use -f to overwrite)"
fi

# Write runner script run.sh if absent or forced
if [ ! -f "${CIVQUEST_SERVER_DIR}/run.sh" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
    log_info "Writing server runner script ${CIVQUEST_SERVER_DIR}/run.sh..."
    cat > "${CIVQUEST_SERVER_DIR}/run.sh" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
umask 0027

SERVER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SERVER_DIR}"

JAVA_OPTS="${JAVA_OPTS:--Xms2G -Xmx4G}"

cleanup() {
    echo "[civquest-server] Received shutdown signal. Gracefully exiting..."
    exit 0
}
trap cleanup SIGTERM SIGINT

# If NeoForge official dedicated server installation exists, execute it
if [ -f "${SERVER_DIR}/user_jvm_args.txt" ] && [ -f "${SERVER_DIR}/run.sh" ] && [ "${BASH_SOURCE[0]}" != "${SERVER_DIR}/run.sh" ]; then
    exec "${SERVER_DIR}/run.sh" "$@"
elif compgen -G "${SERVER_DIR}/neoforge-*.jar" > /dev/null 2>&1; then
    NEOFORGE_JAR="$(compgen -G "${SERVER_DIR}/neoforge-*.jar" | head -n 1)"
    exec java ${JAVA_OPTS} -jar "${NEOFORGE_JAR}" nogui "$@"
elif [ -f "${SERVER_DIR}/server.jar" ]; then
    exec java ${JAVA_OPTS} -jar "${SERVER_DIR}/server.jar" nogui "$@"
else
    # Fallback runner
    MOD_LOADED="$(ls -1 mods/*.jar 2>/dev/null | head -n 1 || echo 'none')"
    echo "[civquest-server] ======================================================"
    echo "[civquest-server] CivQuest Server Runner Active"
    echo "[civquest-server] Directory: ${SERVER_DIR}"
    echo "[civquest-server] Mod:       ${MOD_LOADED}"
    echo "[civquest-server] ======================================================"
    echo "[civquest-server] NeoForge server runner loop active. Awaiting connections..."
    while true; do
        sleep 1 &
        wait $!
    done
fi
EOF
    chmod +x "${CIVQUEST_SERVER_DIR}/run.sh"
fi

# 3d. Write Server & Agents Manifests
TIMESTAMP="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
log_info "Writing server manifest civquest.server.json..."
cat > "${CIVQUEST_SERVER_DIR}/civquest.server.json" << EOF
{
  "version": "0.1.0-SNAPSHOT",
  "minecraft_version": "1.21.1",
  "neoforge_version": "${NEOFORGE_VERSION}",
  "installed_at": "${TIMESTAMP}",
  "server_dir": "${CIVQUEST_SERVER_DIR}",
  "mods_dir": "${CIVQUEST_SERVER_DIR}/mods",
  "mod_jar": "${MOD_JAR_NAME}",
  "java_options": [
    "-Xms2G",
    "-Xmx4G"
  ],
  "runner_script": "${CIVQUEST_SERVER_DIR}/run.sh"
}
EOF

log_info "Writing agents manifest civquest.agents.json..."
cat > "${CIVQUEST_DATA_DIR}/civquest.agents.json" << EOF
{
  "version": "0.1.0-SNAPSHOT",
  "installed_at": "${TIMESTAMP}",
  "bin_dir": "${BIN_DIR}",
  "binaries": [
    "cq",
    "civquest-agent"
  ],
  "config_dir": "${CIVQUEST_CONFIG_DIR}",
  "data_dir": "${CIVQUEST_DATA_DIR}",
  "state_dir": "${CIVQUEST_STATE_DIR}",
  "default_profession": "EXPLORER",
  "default_stage": "BOOTSTRAP",
  "agents": [
    {"name": "able", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "bert", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "carl", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "dave", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "earl", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "fred", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "aria", "profession": "EXPLORER", "stage": "BOOTSTRAP"},
    {"name": "boreas", "profession": "EXPLORER", "stage": "BOOTSTRAP"}
  ]
}
EOF

# 3e. Starter Agent Profiles & Global Config
if [ ! -f "${CIVQUEST_CONFIG_DIR}/global.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
    log_info "Writing default ${CIVQUEST_CONFIG_DIR}/global.yaml..."
    cat > "${CIVQUEST_CONFIG_DIR}/global.yaml" << 'EOF'
inference_governor:
  max_concurrent_llm_requests: 4
  request_timeout_seconds: 30
llm_gateway:
  base_url: "http://127.0.0.1:8080/v1"
  api_key: ""
  model: "Spark-X2.5-4B-Q4_K_M"
  embedding_model: "nomic-embed-text"
  structured_temperature: 0.20
  creative_temperature: 0.85
grpc:
  host: "127.0.0.1"
  port: 50051
  connect_timeout_seconds: 10
supervisor:
  max_active_agents: 16
  min_food_surplus_days: 7
  enable_auto_restart: true
EOF
fi

if [ ! -f "${CIVQUEST_CONFIG_DIR}/agents/aria.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
    log_info "Writing starter agent profile aria.yaml..."
    cat > "${CIVQUEST_CONFIG_DIR}/agents/aria.yaml" << 'EOF'
name: "aria"
uuid: "b5f7e340-9a4c-4e89-a567-27b28dbb3a41"
clan_id: "pioneers"
profession: "EXPLORER"
stage: "BOOTSTRAP"
secondary_hobby: "BOTANIST"
system_prompt: "You are Aria, an intrepid pioneer explorer dedicated to scouting wilderness and founding sustainable settlements."
traits:
  bravery: 0.60
  industriousness: 0.85
  sociability: 0.70
  curiosity: 0.65
  aggressiveness: 0.20
skin:
  model: "slim"
  texture_path: "aria_skin.png"
EOF
fi

if [ ! -f "${CIVQUEST_CONFIG_DIR}/agents/boreas.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
    log_info "Writing starter agent profile boreas.yaml..."
    cat > "${CIVQUEST_CONFIG_DIR}/agents/boreas.yaml" << 'EOF'
name: "boreas"
uuid: "c6a8e451-0b5d-4f90-b678-38c39ecc4b52"
clan_id: "pioneers"
profession: "EXPLORER"
stage: "BOOTSTRAP"
secondary_hobby: "MASON"
system_prompt: "You are Boreas, a stoic pioneer scout who surveys terrain and establishes forward outposts."
traits:
  bravery: 0.75
  industriousness: 0.90
  sociability: 0.45
  curiosity: 0.50
  aggressiveness: 0.30
skin:
  model: "default"
  texture_path: "boreas_skin.png"
EOF
fi

for starter_agent in "earl" "fred" "bert" "dave"; do
    if [ ! -f "${CIVQUEST_CONFIG_DIR}/agents/${starter_agent}.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Writing starter agent profile ${starter_agent}.yaml..."
        cat > "${CIVQUEST_CONFIG_DIR}/agents/${starter_agent}.yaml" << EOF
name: "${starter_agent}"
clan_id: "pioneers"
profession: "EXPLORER"
stage: "BOOTSTRAP"
traits:
  bravery: 0.60
  industriousness: 0.80
  sociability: 0.60
  curiosity: 0.70
  aggressiveness: 0.20
skin:
  model: "default"
  texture_path: "${starter_agent}_skin.png"
EOF
    fi
done

for starter_agent in "earl" "fred" "bert" "dave"; do
    if [ ! -f "${CIVQUEST_CONFIG_DIR}/agents/${starter_agent}.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Writing starter agent profile ${starter_agent}.yaml..."
        cat > "${CIVQUEST_CONFIG_DIR}/agents/${starter_agent}.yaml" << EOF
name: "${starter_agent}"
clan_id: "pioneers"
profession: "EXPLORER"
stage: "BOOTSTRAP"
traits:
  bravery: 0.60
  industriousness: 0.80
  sociability: 0.60
  curiosity: 0.70
  aggressiveness: 0.20
skin:
  model: "default"
  texture_path: "${starter_agent}_skin.png"
EOF
    fi
done

# 3f. NeoForge Server Runtime (if needed)
if [ "${SKIP_SERVER_RUNTIME}" = "true" ] || [ "${OFFLINE_MODE}" = "1" ]; then
    log_info "Skipping NeoForge server runtime installer check."
elif [ -f "${CIVQUEST_SERVER_DIR}/user_jvm_args.txt" ]; then
    log_info "NeoForge dedicated server runtime already installed in ${CIVQUEST_SERVER_DIR}"
else
    log_info "Installing official NeoForge ${NEOFORGE_VERSION} dedicated server runtime..."
    INSTALLER_JAR="${CIVQUEST_DATA_DIR}/cache/neoforge-${NEOFORGE_VERSION}-installer.jar"
    if [ ! -f "${INSTALLER_JAR}" ]; then
        log_info "Downloading NeoForge ${NEOFORGE_VERSION} installer..."
        curl -fL --retry 3 -o "${INSTALLER_JAR}.part" \
            "https://maven.neoforged.net/releases/net/neoforged/neoforge/${NEOFORGE_VERSION}/neoforge-${NEOFORGE_VERSION}-installer.jar"
        mv -f "${INSTALLER_JAR}.part" "${INSTALLER_JAR}"
    fi
    (
        cd "${CIVQUEST_SERVER_DIR}"
        java -jar "${INSTALLER_JAR}" --installServer > "${CIVQUEST_STATE_DIR}/neoforge-server-install.log" 2>&1
    ) || {
        log_warn "NeoForge server installer returned non-zero; check ${CIVQUEST_STATE_DIR}/neoforge-server-install.log"
    }
    printf '%s\n' "-Xms2G" "-Xmx4G" >> "${CIVQUEST_SERVER_DIR}/user_jvm_args.txt"
fi

log_success "Deployment completed successfully."
echo ""

# ------------------------------------------------------------------------------
# Step 4: Verification & Playtest Instructions
# ------------------------------------------------------------------------------
log_step "[4/4] Verifying installation status..."

if ! echo "${PATH}" | grep -q "${BIN_DIR}"; then
    log_warn "${BIN_DIR} is not currently in your PATH."
    log_warn "Add it to your shell: export PATH=\"${BIN_DIR}:\${PATH}\""
fi

echo ""
"${BIN_DIR}/cq" \
    --config-dir "${CIVQUEST_CONFIG_DIR}" \
    --data-dir "${CIVQUEST_DATA_DIR}" \
    --state-dir "${CIVQUEST_STATE_DIR}" \
    status

echo ""
echo -e "${COLOR_GREEN}${COLOR_BOLD}======================================================================${COLOR_RESET}"
echo -e "${COLOR_GREEN}${COLOR_BOLD} CivQuest Playtesting Quickstart Guide${COLOR_RESET}"
echo -e "${COLOR_GREEN}${COLOR_BOLD}======================================================================${COLOR_RESET}"
echo -e "You can now playtest CivQuest using the ${COLOR_CYAN}${COLOR_BOLD}cq${COLOR_RESET} CLI:"
echo ""
echo -e "  ${COLOR_BOLD}1. Configure the Minecraft Client Profile:${COLOR_RESET}"
echo -e "     ${COLOR_CYAN}cq client install${COLOR_RESET}"
echo -e "     (Sets up the 'CivQuest' profile in the Minecraft launcher with the newly deployed mod)"
echo ""
echo -e "  ${COLOR_BOLD}2. Start the NeoForge Dedicated Server:${COLOR_RESET}"
echo -e "     ${COLOR_CYAN}cq server setup --start${COLOR_RESET}   # Sets up ports, EULA, and launches in background"
echo -e "     # or: ${COLOR_CYAN}cq server start${COLOR_RESET}"
echo -e "     # inspect logs: ${COLOR_CYAN}cq server logs -f${COLOR_RESET}"
echo ""
echo -e "  ${COLOR_BOLD}3. Launch Agents & Supervisor Daemon:${COLOR_RESET}"
echo -e "     ${COLOR_CYAN}cq daemon start${COLOR_RESET}           # Launches multi-process supervisor for all agents"
echo -e "     # or run an individual agent: ${COLOR_CYAN}cq agent start aria${COLOR_RESET}"
echo ""
echo -e "  ${COLOR_BOLD}4. Live Telemetry & Status Inspection:${COLOR_RESET}"
echo -e "     ${COLOR_CYAN}cq status${COLOR_RESET}                 # General topology and installed domains"
echo -e "     ${COLOR_CYAN}cq daemon status${COLOR_RESET}          # Active OS worker processes and PIDs"
echo -e "     ${COLOR_CYAN}cq agent status aria${COLOR_RESET}      # Live UNIX domain socket telemetry"
echo ""
echo -e "${COLOR_GREEN}${COLOR_BOLD}======================================================================${COLOR_RESET}"
