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

verify_sha256() {
    local file="$1"
    local expected="$2"
    local actual
    actual="$(sha256sum "${file}" | awk '{print $1}')"
    if [ "${actual}" != "${expected}" ]; then
        echo "[-] Checksum verification failed for ${file}!" >&2
        echo "    Expected: ${expected}" >&2
        echo "    Actual:   ${actual}" >&2
        return 1
    fi
}

# ------------------------------------------------------------------------------
# 1. Manage Java JDK via SDKMAN (Java 21 Temurin)
# ------------------------------------------------------------------------------
echo "[+] Setting up Java JDK via SDKMAN..."
export SDKMAN_DIR="${HOME}/.sdkman"
if [ ! -s "${SDKMAN_DIR}/bin/sdkman-init.sh" ]; then
    echo "    Installing SDKMAN..."
    curl -sSL -o "/tmp/sdkman-install.sh" "https://get.sdkman.io"
    bash "/tmp/sdkman-install.sh"
    rm -f "/tmp/sdkman-install.sh"
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
echo "    ✓ Gradle wrapper standardized via ./gradlew"

# ------------------------------------------------------------------------------
# 2. Check Go (Go 1.25+ required)
# ------------------------------------------------------------------------------
echo "[+] Checking Go..."
if command -v go >/dev/null 2>&1; then
    GO_VER="$(go version | awk '{print $3}')"
    echo "    ✓ Found Go: ${GO_VER}"
else
    echo "    [-] Go not found! Please install Go 1.25+ from https://go.dev/dl/"
    exit 1
fi

# ------------------------------------------------------------------------------
# 3. Install Protobuf Compiler (protoc) & Buf CLI
# ------------------------------------------------------------------------------
echo "[+] Setting up Protobuf tools..."
PROTOC_VERSION="28.3"
if ! command -v protoc >/dev/null 2>&1; then
    echo "    Downloading protoc v${PROTOC_VERSION}..."
    case "${ARCH_GO}" in
        amd64)
            PROTOC_ZIP="protoc-${PROTOC_VERSION}-linux-x86_64.zip"
            PROTOC_SHA256="0ad949f04a6a174da83cdcbdb36dee0a4925272a5b6d83f79a6bf9852076d53f"
            ;;
        arm64)
            PROTOC_ZIP="protoc-${PROTOC_VERSION}-linux-aarch_64.zip"
            PROTOC_SHA256="1de522032a8b194002fe35cab86d747848238b5e4de4f99648372079f5b46f9a"
            ;;
    esac
    curl -sSL -o "/tmp/${PROTOC_ZIP}" "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"
    verify_sha256 "/tmp/${PROTOC_ZIP}" "${PROTOC_SHA256}"
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
    case "${ARCH_GO}" in
        amd64)
            BUF_BIN="buf-Linux-x86_64"
            BUF_SHA256="3a0c4da8d46eea8136affa63db202c76a44f8112384160b73c3fffb1cf14b5d8"
            ;;
        arm64)
            BUF_BIN="buf-Linux-aarch64"
            BUF_SHA256="47ddd7ac0bb2a29f8c92aa420dd113bed3b6857190976402eec93ab9847270b4"
            ;;
    esac
    curl -sSL -o "/tmp/${BUF_BIN}" "https://github.com/bufbuild/buf/releases/download/v${BUF_VER}/${BUF_BIN}"
    verify_sha256 "/tmp/${BUF_BIN}" "${BUF_SHA256}"
    cp -f "/tmp/${BUF_BIN}" "${LOCAL_BIN}/buf"
    chmod +x "${LOCAL_BIN}/buf"
    rm -f "/tmp/${BUF_BIN}"
    echo "    ✓ Installed buf to ${LOCAL_BIN}/buf"
else
    echo "    ✓ buf already installed ($(buf --version))"
fi

# ------------------------------------------------------------------------------
# 4. Install Protobuf & gRPC Code Generation Plugins
# ------------------------------------------------------------------------------
echo "[+] Installing Protobuf & gRPC plugins..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.1
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
echo "    ✓ Go protobuf plugins ready in $(go env GOPATH)/bin"

GRPC_JAVA_VERSION="1.68.1"
if [ ! -f "${LOCAL_BIN}/protoc-gen-grpc-java" ]; then
    echo "    Downloading protoc-gen-grpc-java v${GRPC_JAVA_VERSION}..."
    case "${ARCH_GO}" in
        amd64)
            GRPC_JAVA_EXE="protoc-gen-grpc-java-${GRPC_JAVA_VERSION}-linux-x86_64.exe"
            GRPC_JAVA_SHA256="6eb5bfb8dde3d4b657920dbb42dd2f8ed2e36a3ffd5f46128a4f7f0e2c181c40"
            ;;
        arm64)
            GRPC_JAVA_EXE="protoc-gen-grpc-java-${GRPC_JAVA_VERSION}-linux-aarch_64.exe"
            GRPC_JAVA_SHA256="9a5ac82afa45aa3a855b1674d661f12accc677b253709c2e6fd126ad587d025b"
            ;;
    esac
    curl -sSL -o "/tmp/${GRPC_JAVA_EXE}" "https://repo1.maven.org/maven2/io/grpc/protoc-gen-grpc-java/${GRPC_JAVA_VERSION}/${GRPC_JAVA_EXE}"
    verify_sha256 "/tmp/${GRPC_JAVA_EXE}" "${GRPC_JAVA_SHA256}"
    cp -f "/tmp/${GRPC_JAVA_EXE}" "${LOCAL_BIN}/protoc-gen-grpc-java"
    chmod +x "${LOCAL_BIN}/protoc-gen-grpc-java"
    rm -f "/tmp/${GRPC_JAVA_EXE}"
    echo "    ✓ Installed protoc-gen-grpc-java to ${LOCAL_BIN}/protoc-gen-grpc-java"
else
    echo "    ✓ protoc-gen-grpc-java already installed"
fi

# ------------------------------------------------------------------------------
# 5. Check Beads (bd)
# ------------------------------------------------------------------------------
echo "[+] Checking Beads (bd)..."
if command -v bd >/dev/null 2>&1; then
    echo "    ✓ bd already installed ($(bd version | head -n 1))"
else
    echo "    Installing bd (beads)..."
    curl -sSL -o "/tmp/beads-install.sh" "https://raw.githubusercontent.com/gastownhall/beads/main/install.sh"
    bash "/tmp/beads-install.sh"
    rm -f "/tmp/beads-install.sh"
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
    case "${ARCH_GO}" in
        amd64)
            VEC_TAR="sqlite-vec-${SQLITE_VEC_VERSION}-loadable-linux-x86_64.tar.gz"
            VEC_SHA256="438e0df29f3f8db3525b3aa0dcc0a199869c0bcec9d7abc5b51850469caf867f"
            ;;
        arm64)
            VEC_TAR="sqlite-vec-${SQLITE_VEC_VERSION}-loadable-linux-aarch64.tar.gz"
            VEC_SHA256="d6e4ba12c5c0186eaab42fb4449b311008d86ffd943e6377d7d88018cffab3aa"
            ;;
    esac
    curl -sSL -o "/tmp/${VEC_TAR}" "https://github.com/asg017/sqlite-vec/releases/download/v${SQLITE_VEC_VERSION}/${VEC_TAR}"
    verify_sha256 "/tmp/${VEC_TAR}" "${VEC_SHA256}"
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
