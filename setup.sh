#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# CivQuest Local Development Toolchain Setup Script
# ==============================================================================

echo "========================================================"
echo " Starting CivQuest Local Development Toolchain Setup    "
echo "========================================================"

LOCAL_BIN="${HOME}/.local/bin"
mkdir -p "${LOCAL_BIN}"
export PATH="${LOCAL_BIN}:${PATH}"

# Check Architecture and OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64) ARCH_GO="amd64" ;;
    aarch64|arm64) ARCH_GO="arm64" ;;
    *) echo "[-] Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

echo "[+] Detected OS: ${OS}, Architecture: ${ARCH} (${ARCH_GO})"

# ------------------------------------------------------------------------------
# 1. Manage Java JDK via SDKMAN (Java 21 Temurin)
# ------------------------------------------------------------------------------
echo "[+] Setting up Java JDK via SDKMAN..."
export SDKMAN_DIR="${HOME}/.sdkman"
if [ ! -s "${SDKMAN_DIR}/bin/sdkman-init.sh" ]; then
    echo "    Installing SDKMAN..."
    curl -s "https://get.sdkman.io" | bash
fi

# Temporarily disable nounset for SDKMAN functions compatibility
set +u
# shellcheck source=/dev/null
source "${SDKMAN_DIR}/bin/sdkman-init.sh"

TARGET_JDK="21.0.13.0+9-tem"
if [ ! -d "${SDKMAN_DIR}/candidates/java/${TARGET_JDK}" ]; then
    echo "    Installing ${TARGET_JDK} via SDKMAN..."
    sdk install java "${TARGET_JDK}" </dev/null
fi
sdk default java "${TARGET_JDK}" </dev/null || true
set -u

echo "    ✓ Java managed via SDKMAN ($(java -version 2>&1 | head -n 1))"

# ------------------------------------------------------------------------------
# 2. Check Go (Go 1.23+ required)
# ------------------------------------------------------------------------------
echo "[+] Checking Go..."
if command -v go >/dev/null 2>&1; then
    GO_VER="$(go version | awk '{print $3}')"
    echo "    ✓ Found Go: ${GO_VER}"
else
    echo "    [-] Go not found! Please install Go 1.23+ from https://go.dev/dl/"
    exit 1
fi

# ------------------------------------------------------------------------------
# 3. Install Protobuf Compiler (protoc) & Buf CLI
# ------------------------------------------------------------------------------
echo "[+] Setting up Protobuf tools..."
PROTOC_VERSION="28.3"
if ! command -v protoc >/dev/null 2>&1; then
    echo "    Downloading protoc v${PROTOC_VERSION}..."
    PROTOC_ZIP="protoc-${PROTOC_VERSION}-linux-x86_64.zip"
    if [ "${ARCH_GO}" = "arm64" ]; then
        PROTOC_ZIP="protoc-${PROTOC_VERSION}-linux-aarch_64.zip"
    fi
    curl -sSL -o "/tmp/${PROTOC_ZIP}" "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"
    unzip -q -o "/tmp/${PROTOC_ZIP}" -d "/tmp/protoc"
    cp -f "/tmp/protoc/bin/protoc" "${LOCAL_BIN}/protoc"
    chmod +x "${LOCAL_BIN}/protoc"
    rm -rf "/tmp/protoc" "/tmp/${PROTOC_ZIP}"
    echo "    ✓ Installed protoc to ${LOCAL_BIN}/protoc"
else
    echo "    ✓ protoc already installed ($(protoc --version))"
fi

if ! command -v buf >/dev/null 2>&1; then
    echo "    Downloading Buf CLI..."
    BUF_VER="1.47.2"
    curl -sSL -o "${LOCAL_BIN}/buf" "https://github.com/bufbuild/buf/releases/download/v${BUF_VER}/buf-Linux-${ARCH}"
    chmod +x "${LOCAL_BIN}/buf"
    echo "    ✓ Installed buf to ${LOCAL_BIN}/buf"
else
    echo "    ✓ buf already installed ($(buf --version))"
fi

# ------------------------------------------------------------------------------
# 4. Install Go Protobuf & gRPC Code Generation Plugins
# ------------------------------------------------------------------------------
echo "[+] Installing Go Protobuf plugins..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
echo "    ✓ Go protobuf plugins ready in \$(go env GOPATH)/bin"

# ------------------------------------------------------------------------------
# 5. Check Beads (bd)
# ------------------------------------------------------------------------------
echo "[+] Checking Beads (bd)..."
if command -v bd >/dev/null 2>&1; then
    echo "    ✓ bd already installed ($(bd version | head -n 1))"
else
    echo "    Installing bd (beads)..."
    curl -sSL https://raw.githubusercontent.com/gastownhall/beads/main/install.sh | bash
fi

# ------------------------------------------------------------------------------
# 6. Setup sqlite-vec Extension Library
# ------------------------------------------------------------------------------
echo "[+] Setting up sqlite-vec vector extension..."
SQLITE_VEC_VERSION="0.1.6"
SQLITE_VEC_DIR="${HOME}/.local/lib/civquest"
mkdir -p "${SQLITE_VEC_DIR}"

if [ ! -f "${SQLITE_VEC_DIR}/vec0.so" ]; then
    echo "    Downloading sqlite-vec v${SQLITE_VEC_VERSION}..."
    VEC_TAR="sqlite-vec-${SQLITE_VEC_VERSION}-loadable-linux-x86_64.tar.gz"
    if [ "${ARCH_GO}" = "arm64" ]; then
        VEC_TAR="sqlite-vec-${SQLITE_VEC_VERSION}-loadable-linux-aarch64.tar.gz"
    fi
    curl -sSL -o "/tmp/${VEC_TAR}" "https://github.com/asg017/sqlite-vec/releases/download/v${SQLITE_VEC_VERSION}/${VEC_TAR}"
    tar -xzf "/tmp/${VEC_TAR}" -C "${SQLITE_VEC_DIR}"
    rm -f "/tmp/${VEC_TAR}"
    echo "    ✓ Installed vec0.so to ${SQLITE_VEC_DIR}/vec0.so"
else
    echo "    ✓ sqlite-vec extension present at ${SQLITE_VEC_DIR}/vec0.so"
fi

# ------------------------------------------------------------------------------
# 7. Setup Directory Skeleton for XDG dev environment
# ------------------------------------------------------------------------------
echo "[+] Ensuring XDG directory skeleton exists..."
mkdir -p "${HOME}/.config/civquest"
mkdir -p "${HOME}/.local/share/civquest/graveyard"
mkdir -p "${HOME}/.local/state/civquest"

echo ""
echo "========================================================"
echo " ✓ CivQuest Toolchain Setup Complete!                  "
echo " Ensure ${LOCAL_BIN} and $(go env GOPATH)/bin are in PATH "
echo " Run 'bd ready' to see pending development tasks        "
echo "========================================================"
