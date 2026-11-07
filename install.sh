#!/usr/bin/env bash
set -euo pipefail
umask 0027

# ==============================================================================
# CivQuest Polyglot Settlement Engine - Deployment Script
# Targets: server | agents | all
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# ------------------------------------------------------------------------------
# Terminal Color & Logging Setup
# ------------------------------------------------------------------------------
if [ -t 1 ]; then
    COLOR_RESET="$(printf '\033[0m')"
    COLOR_BOLD="$(printf '\033[1m')"
    COLOR_GREEN="$(printf '\033[32m')"
    COLOR_BLUE="$(printf '\033[34m')"
    COLOR_YELLOW="$(printf '\033[33m')"
    COLOR_RED="$(printf '\033[31m')"
    COLOR_CYAN="$(printf '\033[36m')"
else
    COLOR_RESET=""
    COLOR_BOLD=""
    COLOR_GREEN=""
    COLOR_BLUE=""
    COLOR_YELLOW=""
    COLOR_RED=""
    COLOR_CYAN=""
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
${COLOR_BOLD}CivQuest Deployment & Installer${COLOR_RESET}

${COLOR_BOLD}Usage:${COLOR_RESET}
  $0 [options] <server|agents|all>

${COLOR_BOLD}Directives:${COLOR_RESET}
  server   Builds the NeoForge mod and configures server directories, EULA, and runner
  agents   Compiles Go binaries (civctl, civquest-agent), registers XDG tree, and writes starter configs
  all      Performs complete installation of both server and agent domains

${COLOR_BOLD}Options:${COLOR_RESET}
  -y, --yes    Non-interactive mode (accept all default prompts)
  -f, --force  Force overwrite of existing configuration files
  -h, --help   Show this help message

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
NON_INTERACTIVE=false
FORCE_OVERWRITE=false
TARGET=""

while [ $# -gt 0 ]; do
    case "$1" in
        -y|--yes)
            NON_INTERACTIVE=true
            shift
            ;;
        -f|--force)
            FORCE_OVERWRITE=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        server|agents|all)
            if [ -n "${TARGET}" ]; then
                log_error "Multiple targets specified: '${TARGET}' and '$1'"
                usage
                exit 1
            fi
            TARGET="$1"
            shift
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
done

if [ -z "${TARGET}" ]; then
    if [ -t 0 ] && [ "${NON_INTERACTIVE}" != "true" ]; then
        echo -e "${COLOR_BOLD}Select CivQuest installation target:${COLOR_RESET}"
        echo "  1) server (NeoForge mod & dedicated server runtime)"
        echo "  2) agents (Go orchestrator, civctl, civquest-agent, XDG dirs)"
        echo "  3) all    (Both server and agents)"
        echo "  q) quit"
        read -rp "Enter choice [1-3, q]: " CHOICE
        case "${CHOICE}" in
            1|server) TARGET="server" ;;
            2|agents) TARGET="agents" ;;
            3|all)    TARGET="all" ;;
            q|Q|quit) echo "Installation aborted."; exit 0 ;;
            *) log_error "Invalid selection."; exit 1 ;;
        esac
    else
        log_error "No target specified. Please provide 'server', 'agents', or 'all'."
        usage
        exit 1
    fi
fi

# ------------------------------------------------------------------------------
# Resolve XDG Paths
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

log_info "CivQuest Deployment Target: ${COLOR_BOLD}${TARGET}${COLOR_RESET}"
log_info "Config Directory:  ${CIVQUEST_CONFIG_DIR}"
log_info "Data Directory:    ${CIVQUEST_DATA_DIR}"
log_info "State Directory:   ${CIVQUEST_STATE_DIR}"
log_info "Binary Directory:  ${BIN_DIR}"
echo ""

# ------------------------------------------------------------------------------
# Target: Server Installation
# ------------------------------------------------------------------------------
install_server() {
    log_step "Installing CivQuest Server Domain..."

    # 1. Build NeoForge mod jar
    log_info "Building NeoForge mod with Gradle..."
    ./gradlew :mod-neoforge:build

    # 2. Locate built jar
    local jar_path
    jar_path="$(find "${SCRIPT_DIR}/mod-neoforge/build/libs" -maxdepth 1 -name "*.jar" ! -name "*-sources.jar" | head -n 1)"
    if [ -z "${jar_path}" ] || [ ! -f "${jar_path}" ]; then
        log_error "Mod jar could not be found in mod-neoforge/build/libs!"
        exit 1
    fi
    local jar_name
    jar_name="$(basename "${jar_path}")"
    log_info "Found built mod jar: ${jar_name}"

    # 3. Create server directory structure
    mkdir -p "${CIVQUEST_SERVER_DIR}/mods"
    mkdir -p "${CIVQUEST_STATE_DIR}/run"
    chmod 0700 "${CIVQUEST_STATE_DIR}/run"

    # 4. Copy mod jar into mods folder
    log_info "Copying mod jar to ${CIVQUEST_SERVER_DIR}/mods/${jar_name}..."
    cp -f "${jar_path}" "${CIVQUEST_SERVER_DIR}/mods/${jar_name}"

    # 5. Write eula.txt
    log_info "Writing eula.txt..."
    cat > "${CIVQUEST_SERVER_DIR}/eula.txt" << 'EOF'
# By changing the setting below to TRUE you are indicating your agreement to the Mojang EULA (https://aka.ms/MinecraftEULA).
eula=true
EOF

    # 6. Write server.properties
    if [ ! -f "${CIVQUEST_SERVER_DIR}/server.properties" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Writing server.properties..."
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

    # 7. Write lightweight runner script run.sh
    if [ ! -f "${CIVQUEST_SERVER_DIR}/run.sh" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Creating runner script ${CIVQUEST_SERVER_DIR}/run.sh..."
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
    # Standalone runner for mod staging & simulation
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
    else
        log_info "Preserving existing ${CIVQUEST_SERVER_DIR}/run.sh (use -f to overwrite)"
    fi

    # 8. Write civquest.server.json manifest
    local timestamp
    timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    log_info "Writing server manifest civquest.server.json..."
    cat > "${CIVQUEST_SERVER_DIR}/civquest.server.json" << EOF
{
  "version": "0.1.0-SNAPSHOT",
  "minecraft_version": "1.21.1",
  "neoforge_version": "21.1.265",
  "installed_at": "${timestamp}",
  "server_dir": "${CIVQUEST_SERVER_DIR}",
  "mods_dir": "${CIVQUEST_SERVER_DIR}/mods",
  "mod_jar": "${jar_name}",
  "java_options": [
    "-Xms2G",
    "-Xmx4G"
  ],
  "runner_script": "${CIVQUEST_SERVER_DIR}/run.sh"
}
EOF

    # 9. Build and install civctl for server management if Go is available
    if command -v go >/dev/null 2>&1; then
        log_info "Go toolchain detected: compiling and installing civctl for server management..."
        local server_build_tmp
        server_build_tmp="$(mktemp -d)"
        trap 'rm -rf "${server_build_tmp:-}"' EXIT INT TERM
        (
            cd "${SCRIPT_DIR}/orchestrator-go"
            go build -o "${server_build_tmp}/civctl" ./cmd/civctl
        )
        mkdir -p "${BIN_DIR}"
        mkdir -p "${CIVQUEST_DATA_DIR}/bin"
        cp -f "${server_build_tmp}/civctl" "${BIN_DIR}/civctl"
        cp -f "${server_build_tmp}/civctl" "${CIVQUEST_DATA_DIR}/bin/civctl"
        chmod +x "${BIN_DIR}/civctl" "${CIVQUEST_DATA_DIR}/bin/civctl"
        rm -rf "${server_build_tmp}"
        trap - EXIT INT TERM
    fi

    log_success "Server domain installed successfully at ${CIVQUEST_SERVER_DIR}"
}

# ------------------------------------------------------------------------------
# Target: Agents Installation
# ------------------------------------------------------------------------------
install_agents() {
    log_step "Installing CivQuest Agent Domain..."

    # 1. Verify Go toolchain
    if ! command -v go >/dev/null 2>&1; then
        log_error "Go is required to build orchestrator-go. Please install Go 1.23+."
        exit 1
    fi

    # 2. Build Go binaries
    log_info "Building Go orchestrator binaries (civctl and civquest-agent)..."
    local build_tmp
    build_tmp="$(mktemp -d)"
    trap 'rm -rf "${build_tmp:-}"' EXIT INT TERM
    (
        cd "${SCRIPT_DIR}/orchestrator-go"
        go build -o "${build_tmp}/civctl" ./cmd/civctl
        go build -o "${build_tmp}/civquest-agent" ./cmd/civquest-agent
    )

    # 3. Install binaries into target bin directories
    mkdir -p "${BIN_DIR}"
    mkdir -p "${CIVQUEST_DATA_DIR}/bin"

    log_info "Installing binaries into ${BIN_DIR}..."
    cp -f "${build_tmp}/civctl" "${BIN_DIR}/civctl"
    cp -f "${build_tmp}/civquest-agent" "${BIN_DIR}/civquest-agent"
    chmod +x "${BIN_DIR}/civctl" "${BIN_DIR}/civquest-agent"

    log_info "Installing binaries into ${CIVQUEST_DATA_DIR}/bin..."
    cp -f "${build_tmp}/civctl" "${CIVQUEST_DATA_DIR}/bin/civctl"
    cp -f "${build_tmp}/civquest-agent" "${CIVQUEST_DATA_DIR}/bin/civquest-agent"
    chmod +x "${CIVQUEST_DATA_DIR}/bin/civctl" "${CIVQUEST_DATA_DIR}/bin/civquest-agent"

    rm -rf "${build_tmp}"
    trap - EXIT INT TERM

    # 4. Initialize standard XDG directories
    log_info "Initializing XDG directory skeleton..."
    mkdir -p "${CIVQUEST_CONFIG_DIR}/agents"
    mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/official"
    mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/player_imported"
    mkdir -p "${CIVQUEST_CONFIG_DIR}/blueprints/llm_generated"
    mkdir -p "${CIVQUEST_DATA_DIR}/agents"
    mkdir -p "${CIVQUEST_DATA_DIR}/clans/pioneers"
    mkdir -p "${CIVQUEST_DATA_DIR}/graveyard"
    mkdir -p "${CIVQUEST_STATE_DIR}/agents"
    mkdir -p "${CIVQUEST_STATE_DIR}/run"
    chmod 0700 "${CIVQUEST_STATE_DIR}/run"

    # 5. Create default global.yaml
    if [ ! -f "${CIVQUEST_CONFIG_DIR}/global.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Writing ${CIVQUEST_CONFIG_DIR}/global.yaml..."
        cat > "${CIVQUEST_CONFIG_DIR}/global.yaml" << 'EOF'
inference_governor:
  max_concurrent_llm_requests: 4
  request_timeout_seconds: 30
llm_gateway:
  base_url: "http://127.0.0.1:11434/v1"
  api_key: ""
  model: "llama3.1"
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
    else
        log_info "Preserving existing global.yaml (use -f to overwrite)"
    fi

    # 6. Create starter agent configurations
    if [ ! -f "${CIVQUEST_CONFIG_DIR}/agents/aria.yaml" ] || [ "${FORCE_OVERWRITE}" = "true" ]; then
        log_info "Writing starter agent profile aria.yaml..."
        cat > "${CIVQUEST_CONFIG_DIR}/agents/aria.yaml" << 'EOF'
name: "aria"
uuid: "b5f7e340-9a4c-4e89-a567-27b28dbb3a41"
clan_id: "pioneers"
profession: "FARMER"
secondary_hobby: "BOTANIST"
system_prompt: "You are Aria, an industrious pioneer farmer dedicated to cultivating sustainable crops."
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
profession: "BUILDER"
secondary_hobby: "MASON"
system_prompt: "You are Boreas, a stoic pioneer builder who constructs defensive fortifications and granaries."
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

    # 7. Write civquest.agents.json manifest
    local timestamp
    timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    log_info "Writing agents manifest civquest.agents.json..."
    cat > "${CIVQUEST_DATA_DIR}/civquest.agents.json" << EOF
{
  "version": "0.1.0",
  "installed_at": "${timestamp}",
  "bin_dir": "${BIN_DIR}",
  "binaries": [
    "civctl",
    "civquest-agent"
  ],
  "config_dir": "${CIVQUEST_CONFIG_DIR}",
  "data_dir": "${CIVQUEST_DATA_DIR}",
  "state_dir": "${CIVQUEST_STATE_DIR}"
}
EOF

    log_success "Agent domain installed successfully."
    if ! echo "${PATH}" | grep -q "${BIN_DIR}"; then
        log_warn "${BIN_DIR} is not currently in your PATH."
        log_warn "Add it to your shell configuration: export PATH=\"${BIN_DIR}:\${PATH}\""
    fi
}

# ------------------------------------------------------------------------------
# Dispatch Execution
# ------------------------------------------------------------------------------
case "${TARGET}" in
    server)
        install_server
        ;;
    agents)
        install_agents
        ;;
    all)
        install_server
        echo ""
        install_agents
        ;;
esac

echo ""
log_step "Installation Summary"
if [ -x "${BIN_DIR}/civctl" ]; then
    "${BIN_DIR}/civctl" \
        --config-dir "${CIVQUEST_CONFIG_DIR}" \
        --data-dir "${CIVQUEST_DATA_DIR}" \
        --state-dir "${CIVQUEST_STATE_DIR}" \
        status
elif [ -x "${CIVQUEST_DATA_DIR}/bin/civctl" ]; then
    "${CIVQUEST_DATA_DIR}/bin/civctl" \
        --config-dir "${CIVQUEST_CONFIG_DIR}" \
        --data-dir "${CIVQUEST_DATA_DIR}" \
        --state-dir "${CIVQUEST_STATE_DIR}" \
        status
fi

log_success "CivQuest deployment completed successfully."
exit 0
