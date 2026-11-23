#!/usr/bin/env bash
# ==============================================================================
# CivQuest Playtesting Harness (playtest.sh)
#
# Provides rapid start, teardown, and clean reset for manual playtesting
# using the `cq` CLI and XDG directory management.
#
# Usage:
#   ./playtest.sh start    # Configure client, start server & daemon, launch agents
#   ./playtest.sh end      # Stop all agents, supervisor daemon, and NeoForge server
#   ./playtest.sh reset    # Stop all, wipe agent state/configs, recreate fresh roster
# ==============================================================================

set -euo pipefail

# Invariant: Names of playtesting roster
PLAYTEST_AGENTS=("able" "bert" "carl" "dave" "earl" "fred")

# Invariant: Default professions for playtesting roster (Pioneer Genesis per ADR-027)
declare -A AGENT_ROLES=(
  ["able"]="EXPLORER"
  ["bert"]="EXPLORER"
  ["carl"]="EXPLORER"
  ["dave"]="EXPLORER"
  ["earl"]="EXPLORER"
  ["fred"]="EXPLORER"
)

# Invariant: Dynamic XDG Base Directory Resolution
XDG_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/civquest"
XDG_DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/civquest"
XDG_STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/civquest"

log_info() {
  echo -e "\033[1;34m[INFO]\033[0m $*"
}

log_success() {
  echo -e "\033[1;32m[SUCCESS]\033[0m $*"
}

log_warn() {
  echo -e "\033[1;33m[WARN]\033[0m $*"
}

log_error() {
  echo -e "\033[1;31m[ERROR]\033[0m $*"
}

ensure_cq_available() {
  if ! command -v cq &>/dev/null; then
    if [[ -x "$HOME/.local/bin/cq" ]]; then
      export PATH="$HOME/.local/bin:$PATH"
    else
      log_error "'cq' CLI not found in PATH or ~/.local/bin. Run ./deploy.sh first."
      exit 1
    fi
  fi
}

wait_for_server() {
  log_info "Waiting for NeoForge dedicated server to bind gRPC port..."
  local attempts=0
  local max_attempts=30
  while [[ $attempts -lt $max_attempts ]]; do
    if cq server status 2>&1 | grep -q "Running"; then
      log_success "NeoForge dedicated server is active and running."
      return 0
    fi
    sleep 1
    attempts=$((attempts + 1))
  done
  log_warn "Server status check timed out, proceeding anyway..."
}

do_start() {
  ensure_cq_available
  log_info "==> [1/4] Configuring Minecraft launcher client profile..."
  cq client install

  log_info "==> [2/4] Starting NeoForge dedicated server in background..."
  cq server start || true
  wait_for_server

  log_info "==> [3/4] Starting CivQuest supervisor daemon..."
  cq daemon start || true

  log_info "==> [4/4] Starting playtest agent roster (${PLAYTEST_AGENTS[*]}):"
  for agent in "${PLAYTEST_AGENTS[@]}"; do
    # Create profile if not already configured
    if [[ ! -f "$XDG_CONFIG_DIR/agents/${agent}.yaml" ]]; then
      local role="${AGENT_ROLES[$agent]:-EXPLORER}"
      log_info "Creating missing agent profile: $agent (Role: $role)..."
      cq agent create "$agent" --role "$role" --clan "pioneers"
    fi
    log_info "Launching agent avatar: $agent..."
    cq agent start "$agent" || log_warn "Failed to launch $agent (already active?)"
  done

  echo ""
  log_success "Playtest environment active!"
  log_info "Launch your Minecraft client with the 'CivQuest' profile."
  log_info "Inspect status anytime with: cq status | cq daemon status | cq agent list"
}

do_end() {
  ensure_cq_available
  log_info "==> [1/3] Stopping autonomous agents (${PLAYTEST_AGENTS[*]}):"
  for agent in "${PLAYTEST_AGENTS[@]}"; do
    cq agent stop "$agent" 2>/dev/null || true
  done

  log_info "==> [2/3] Stopping supervisor daemon..."
  cq daemon stop 2>/dev/null || true

  log_info "==> [3/3] Stopping NeoForge dedicated server..."
  cq server stop 2>/dev/null || true

  log_success "Playtest environment stopped cleanly."
}

do_reset() {
  ensure_cq_available
  log_info "==> Resetting CivQuest playtest agents and state..."
  
  # 1. Ensure all active processes are shut down first
  do_end

  # 2. Delete existing agent configs, databases, runtime state, logs, and all dead agent / graveyard records
  log_info "Purging agent configs, SQLite persistence, dead agent graveyard archives, and runtime logs..."
  rm -f "$XDG_CONFIG_DIR/agents/"*.yaml 2>/dev/null || true
  rm -rf "$XDG_DATA_DIR/agents/"* 2>/dev/null || true
  rm -rf "$XDG_DATA_DIR"/servers/*/agents/* 2>/dev/null || true
  rm -rf "$XDG_STATE_DIR/agents/"* 2>/dev/null || true
  rm -rf "$XDG_STATE_DIR"/servers/*/agents/* 2>/dev/null || true
  rm -rf "$XDG_STATE_DIR/logs/"* 2>/dev/null || true
  # Invariant: Thoroughly purge dead agent graveyard archives across all server namespaces
  rm -rf "$XDG_DATA_DIR/graveyard/"* 2>/dev/null || true
  rm -rf "$XDG_DATA_DIR"/servers/*/graveyard/* 2>/dev/null || true

  log_info "Recreating fresh agent profiles (${PLAYTEST_AGENTS[*]}):"
  mkdir -p "$XDG_CONFIG_DIR/agents"
  for agent in "${PLAYTEST_AGENTS[@]}"; do
    local role="${AGENT_ROLES[$agent]:-EXPLORER}"
    log_info "Creating agent: $agent (Role: $role, Clan: pioneers)..."
    cq agent create "$agent" --role "$role" --clan "pioneers"
  done

  echo ""
  log_success "Reset complete. All agents restored to pristine initial state."
  log_info "Run './playtest.sh start' when ready to begin playtesting."
}

usage() {
  echo "Usage: $0 [start|end|reset]"
  echo ""
  echo "Commands:"
  echo "  start   Configure client, start server & daemon, launch agents (able, bert, carl, dave, earl, fred)"
  echo "  end     Stop all agent processes, supervisor daemon, and NeoForge server"
  echo "  reset   Stop all running processes, purge agent persistence/configs, and recreate fresh roster"
  exit 1
}

case "${1:-}" in
  start)
    do_start
    ;;
  end)
    do_end
    ;;
  reset)
    do_reset
    ;;
  *)
    usage
    ;;
esac
