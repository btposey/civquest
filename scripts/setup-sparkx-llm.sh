#!/usr/bin/env bash
# ==============================================================================
# CivQuest - Setup Spark-X2.5 4B LLM Gateway (llama.cpp)
#
# 1. Installs / updates latest llama.cpp backend (~/.local/bin/llama)
# 2. Verifies or downloads Spark-X2.5 4B GGUF into ~/models/
# 3. Configures systemd user service llama-server on http://127.0.0.1:8080
# 4. Configures ~/.config/civquest/global.yaml to target Spark-X
# 5. Tests endpoint health
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

log() {
    echo -e "\033[1;34m[civquest-sparkx]\033[0m $*"
}

# 1. Update / install latest llama.cpp
log "Checking / updating llama.cpp backend..."
curl -LsSf https://llama.app/install.sh | sh

# 2. Ensure model exists
MODEL_DIR="$HOME/models"
MODEL_FILE="$MODEL_DIR/Spark-X2.5-4B-Q4_K_M.gguf"
MODEL_URL="https://huggingface.co/miifanboy/Spark-X2.5-4B-i1-GGUF/resolve/main/Spark-X2.5-4B-Q4_K_M.gguf"

mkdir -p "$MODEL_DIR"
if [ ! -f "$MODEL_FILE" ]; then
    log "Downloading Spark-X2.5 4B (~2.6 GB)..."
    curl -L -C - --progress-bar -o "$MODEL_FILE" "$MODEL_URL"
else
    log "Model verified at: $MODEL_FILE"
fi

# 3. Configure systemd user service
SERVICE_FILE="$HOME/.config/systemd/user/llama-server.service"
mkdir -p "$(dirname "$SERVICE_FILE")"

log "Configuring systemd service ($SERVICE_FILE)..."
cat << EOF > "$SERVICE_FILE"
[Unit]
Description=llama.cpp Local User Daemon (Spark-X2.5 4B - CivQuest System 2)
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/llama serve \\
  -m %h/models/Spark-X2.5-4B-Q4_K_M.gguf \\
  --host 0.0.0.0 \\
  --port 8080 \\
  -c 65536 \\
  -np 2 \\
  -ngl 999 \\
  -fa on \\
  --cache-type-k q8_0 \\
  --cache-type-v q8_0 \\
  --cache-prompt \\
  --context-shift \\
  -t 6 \\
  -tb 6 \\
  --load-mode mmap+mlock \\
  --slots \\
  --metrics
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user restart llama-server
loginctl enable-linger "$USER" 2>/dev/null || true

log "Waiting for Spark-X to start..."
sleep 3

# 4. Configure ~/.config/civquest/global.yaml
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/civquest"
GLOBAL_YAML="$CONFIG_DIR/global.yaml"
mkdir -p "$CONFIG_DIR"

if [ ! -f "$GLOBAL_YAML" ]; then
    log "Generating $GLOBAL_YAML configured for Spark-X at port 8080..."
    cat << EOF > "$GLOBAL_YAML"
inference_governor:
  max_concurrent_llm_requests: 2
  request_timeout_seconds: 30

llm_gateway:
  base_url: "http://127.0.0.1:8080/v1"
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
else
    log "Updating base_url in $GLOBAL_YAML to port 8080..."
    sed -i 's|http://127.0.0.1:11434/v1|http://127.0.0.1:8080/v1|g' "$GLOBAL_YAML" || true
fi

# 5. Health check
log "Testing completion endpoint (http://127.0.0.1:8080/v1/chat/completions)..."
RESPONSE="$(curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [{"role": "user", "content": "Respond with the single word: READY"}],
    "max_tokens": 10,
    "temperature": 0.1
  }')"

if echo "$RESPONSE" | grep -q "choices"; then
    log "SUCCESS: Spark-X2.5 4B is active and generating tokens!"
else
    echo "Warning: Response did not contain choices: $RESPONSE" >&2
fi
