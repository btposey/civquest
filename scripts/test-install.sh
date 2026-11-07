#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# CivQuest Installation & CLI Verification Test Suite
# ==============================================================================

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TEST_SANDBOX="$(mktemp -d /tmp/civquest-test-XXXXXX)"
cleanup() {
    rm -rf "${TEST_SANDBOX}"
}
trap cleanup EXIT

export XDG_CONFIG_HOME="${TEST_SANDBOX}/config"
export XDG_DATA_HOME="${TEST_SANDBOX}/data"
export XDG_STATE_HOME="${TEST_SANDBOX}/state"
export INSTALL_BIN_DIR="${TEST_SANDBOX}/bin"
export PATH="${INSTALL_BIN_DIR}:${PATH}"

echo "======================================================================"
echo " CivQuest install.sh & CLI Functional Test Suite"
echo " Sandbox: ${TEST_SANDBOX}"
echo "======================================================================"

# 1. Test invalid arguments
echo "[1/7] Testing invalid arguments rejection..."
set +e
"${ROOT_DIR}/install.sh" invalid_target >/dev/null 2>&1
STATUS=$?
set -e
if [ ${STATUS} -eq 0 ]; then
    echo "[-] FAILED: install.sh should fail on invalid target"
    exit 1
fi
echo "  ✓ Successfully rejected invalid target"

# 2. Test non-interactive missing target rejection
echo "[2/7] Testing non-interactive missing target rejection..."
set +e
"${ROOT_DIR}/install.sh" -y </dev/null >/dev/null 2>&1
STATUS=$?
set -e
if [ ${STATUS} -eq 0 ]; then
    echo "[-] FAILED: install.sh should fail when target missing in non-interactive mode"
    exit 1
fi
echo "  ✓ Successfully rejected missing target"

# 3. Test 'server' target installation
echo "[3/7] Testing './install.sh server'..."
"${ROOT_DIR}/install.sh" -y server

SERVER_DIR="${XDG_DATA_HOME}/civquest/server"
test -d "${SERVER_DIR}/mods" || { echo "[-] mods dir not found"; exit 1; }
test -f "${SERVER_DIR}/eula.txt" || { echo "[-] eula.txt not found"; exit 1; }
grep -q "eula=true" "${SERVER_DIR}/eula.txt" || { echo "[-] eula=true not set"; exit 1; }
test -f "${SERVER_DIR}/server.properties" || { echo "[-] server.properties not found"; exit 1; }
test -f "${SERVER_DIR}/civquest.server.json" || { echo "[-] civquest.server.json not found"; exit 1; }
test -x "${SERVER_DIR}/run.sh" || { echo "[-] run.sh is not executable"; exit 1; }
test -x "${INSTALL_BIN_DIR}/civctl" || { echo "[-] civctl not found in bin after server install"; exit 1; }
test -d "${XDG_STATE_HOME}/civquest/run" || { echo "[-] state/civquest/run not created"; exit 1; }

# Verify run.sh preservation without -f
echo "# Custom run marker" >> "${SERVER_DIR}/run.sh"
"${ROOT_DIR}/install.sh" -y server >/dev/null 2>&1
grep -q "# Custom run marker" "${SERVER_DIR}/run.sh" || { echo "[-] run.sh was unexpectedly overwritten without -f"; exit 1; }

# Verify run.sh overwrite with -f
"${ROOT_DIR}/install.sh" -y -f server >/dev/null 2>&1
if grep -q "# Custom run marker" "${SERVER_DIR}/run.sh"; then
    echo "[-] run.sh was not overwritten with -f"
    exit 1
fi

echo "  ✓ Server domain installation verified"

# 4. Test 'agents' target installation
echo "[4/7] Testing './install.sh agents'..."
"${ROOT_DIR}/install.sh" -y agents

test -x "${INSTALL_BIN_DIR}/civctl" || { echo "[-] civctl not found in INSTALL_BIN_DIR"; exit 1; }
test -x "${INSTALL_BIN_DIR}/civquest-agent" || { echo "[-] civquest-agent not found in INSTALL_BIN_DIR"; exit 1; }
test -x "${XDG_DATA_HOME}/civquest/bin/civctl" || { echo "[-] civctl not found in DATA/bin"; exit 1; }
test -f "${XDG_CONFIG_HOME}/civquest/global.yaml" || { echo "[-] global.yaml not found"; exit 1; }
test -f "${XDG_CONFIG_HOME}/civquest/agents/aria.yaml" || { echo "[-] aria.yaml not found"; exit 1; }
test -f "${XDG_CONFIG_HOME}/civquest/agents/boreas.yaml" || { echo "[-] boreas.yaml not found"; exit 1; }
test -f "${XDG_DATA_HOME}/civquest/civquest.agents.json" || { echo "[-] civquest.agents.json not found"; exit 1; }
echo "  ✓ Agent domain installation verified"

# 5. Test CLI domain autodetection & agent listing
echo "[5/7] Testing CLI domain detection ('civctl status' & 'civctl agent list')..."
STATUS_OUT="$("${INSTALL_BIN_DIR}/civctl" status)"
echo "${STATUS_OUT}" | grep -q "Installed: Yes" || { echo "[-] civctl status did not report Installed: Yes"; exit 1; }
echo "${STATUS_OUT}" | grep -q "aria" || { echo "[-] civctl status did not report aria"; exit 1; }

AGENT_LIST_OUT="$("${INSTALL_BIN_DIR}/civctl" agent list)"
echo "${AGENT_LIST_OUT}" | grep -q "aria" || { echo "[-] civctl agent list did not list aria"; exit 1; }
echo "${AGENT_LIST_OUT}" | grep -q "boreas" || { echo "[-] civctl agent list did not list boreas"; exit 1; }

AGENT_STATUS_OUT="$("${INSTALL_BIN_DIR}/civctl" agent status aria)"
echo "${AGENT_STATUS_OUT}" | grep -q "CivQuest Agent Status: aria" || { echo "[-] agent status aria failed"; exit 1; }
echo "  ✓ CLI detection and agent inspection verified"

# 6. Test server lifecycle: start -> status -> stop
echo "[6/7] Testing server lifecycle ('civctl server start | status | stop')..."
"${INSTALL_BIN_DIR}/civctl" server start
sleep 0.2

SERVER_STATUS_OUT="$("${INSTALL_BIN_DIR}/civctl" server status)"
echo "${SERVER_STATUS_OUT}" | grep -q "RUNNING" || { echo "[-] Server was not reported as RUNNING"; exit 1; }

"${INSTALL_BIN_DIR}/civctl" server stop
sleep 0.2

SERVER_STATUS_STOPPED="$("${INSTALL_BIN_DIR}/civctl" server status)"
echo "${SERVER_STATUS_STOPPED}" | grep -q "STOPPED" || { echo "[-] Server was not reported as STOPPED"; exit 1; }
echo "  ✓ Server lifecycle start/status/stop verified"

# 7. Test 'all' target installation in clean sandbox
echo "[7/7] Testing './install.sh all' in fresh sandbox..."
CLEAN_SANDBOX="$(mktemp -d /tmp/civquest-clean-XXXXXX)"
(
    export XDG_CONFIG_HOME="${CLEAN_SANDBOX}/config"
    export XDG_DATA_HOME="${CLEAN_SANDBOX}/data"
    export XDG_STATE_HOME="${CLEAN_SANDBOX}/state"
    export INSTALL_BIN_DIR="${CLEAN_SANDBOX}/bin"
    export PATH="${INSTALL_BIN_DIR}:${PATH}"

    "${ROOT_DIR}/install.sh" -y all
    test -f "${CLEAN_SANDBOX}/data/civquest/server/civquest.server.json" || exit 1
    test -f "${CLEAN_SANDBOX}/data/civquest/civquest.agents.json" || exit 1
    test -x "${CLEAN_SANDBOX}/bin/civctl" || exit 1
)
rm -rf "${CLEAN_SANDBOX}"
echo "  ✓ Full './install.sh all' installation verified"

echo "======================================================================"
echo " ALL TESTS PASSED SUCCESSFULLY! "
echo "======================================================================"
