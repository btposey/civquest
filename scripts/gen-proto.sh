#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# CivQuest Protobuf & gRPC Code Generation Script
# ==============================================================================

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

LOCAL_BIN="${HOME}/.local/bin"
export PATH="${LOCAL_BIN}:$(go env GOPATH 2>/dev/null || true)/bin:${PATH}"

echo "========================================================"
echo " Generating CivQuest Protobuf & gRPC Code              "
echo "========================================================"

# Check toolchain prerequisites
if ! command -v protoc >/dev/null 2>&1; then
    echo "[-] Error: protoc not found in PATH" >&2
    exit 1
fi

if ! command -v protoc-gen-go >/dev/null 2>&1; then
    echo "[-] Error: protoc-gen-go not found in PATH" >&2
    exit 1
fi

if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
    echo "[-] Error: protoc-gen-go-grpc not found in PATH" >&2
    exit 1
fi

GO_OUT_DIR="${ROOT_DIR}/orchestrator-go"
JAVA_OUT_DIR="${ROOT_DIR}/mod-neoforge/src/generated/main/java"

mkdir -p "${GO_OUT_DIR}/internal/proto/v1"
mkdir -p "${JAVA_OUT_DIR}"

# Multi-language code generation strategy:
# - Go stubs are generated via Buf CLI (`buf generate`) per buf.gen.yaml, or direct protoc fallback.
# - Java gRPC stubs require the official protoc-gen-grpc-java C++ plugin, which is invoked
#   directly via protoc to produce source files in mod-neoforge/src/generated/main/java.

echo "[+] Generating Go stubs..."
if command -v buf >/dev/null 2>&1; then
    echo "    Using buf generate..."
    buf generate
else
    echo "    Using protoc directly..."
    protoc --proto_path=proto \
        --go_out="${GO_OUT_DIR}" --go_opt=module=github.com/btposey/civquest/orchestrator-go \
        --go-grpc_out="${GO_OUT_DIR}" --go-grpc_opt=module=github.com/btposey/civquest/orchestrator-go \
        proto/civquest/v1/civquest.proto
fi

echo "[+] Generating Java stubs..."
GRPC_JAVA_PLUGIN="${LOCAL_BIN}/protoc-gen-grpc-java"
if [ ! -f "${GRPC_JAVA_PLUGIN}" ]; then
    echo "    Downloading protoc-gen-grpc-java..."
    ARCH="$(uname -m)"
    case "${ARCH}" in
        x86_64)
            GRPC_JAVA_EXE="protoc-gen-grpc-java-1.68.1-linux-x86_64.exe"
            GRPC_JAVA_SHA256="6eb5bfb8dde3d4b657920dbb42dd2f8ed2e36a3ffd5f46128a4f7f0e2c181c40"
            ;;
        aarch64|arm64)
            GRPC_JAVA_EXE="protoc-gen-grpc-java-1.68.1-linux-aarch_64.exe"
            GRPC_JAVA_SHA256="9a5ac82afa45aa3a855b1674d661f12accc677b253709c2e6fd126ad587d025b"
            ;;
        *)
            echo "[-] Error: Unsupported architecture for gRPC Java plugin: ${ARCH}" >&2
            exit 1
            ;;
    esac

    curl -sSL -o "/tmp/${GRPC_JAVA_EXE}" "https://repo1.maven.org/maven2/io/grpc/protoc-gen-grpc-java/1.68.1/${GRPC_JAVA_EXE}"
    ACTUAL_SHA256="$(sha256sum "/tmp/${GRPC_JAVA_EXE}" | awk '{print $1}')"
    if [ "${ACTUAL_SHA256}" != "${GRPC_JAVA_SHA256}" ]; then
        echo "[-] Checksum verification failed for ${GRPC_JAVA_EXE}!" >&2
        echo "    Expected: ${GRPC_JAVA_SHA256}" >&2
        echo "    Actual:   ${ACTUAL_SHA256}" >&2
        rm -f "/tmp/${GRPC_JAVA_EXE}"
        exit 1
    fi
    mkdir -p "${LOCAL_BIN}"
    cp -f "/tmp/${GRPC_JAVA_EXE}" "${GRPC_JAVA_PLUGIN}"
    chmod +x "${GRPC_JAVA_PLUGIN}"
    rm -f "/tmp/${GRPC_JAVA_EXE}"
fi

protoc --proto_path=proto \
    --java_out="${JAVA_OUT_DIR}" \
    --plugin=protoc-gen-grpc-java="${GRPC_JAVA_PLUGIN}" \
    --grpc-java_out="${JAVA_OUT_DIR}" \
    proto/civquest/v1/civquest.proto

echo "[+] Protobuf generation complete!"
echo "    Go stubs:   ${GO_OUT_DIR}/internal/proto/v1"
echo "    Java stubs: ${JAVA_OUT_DIR}/com/civquest/mod/grpc/v1"
