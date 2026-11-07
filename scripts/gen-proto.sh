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
    curl -sSL -o "${GRPC_JAVA_PLUGIN}" "https://repo1.maven.org/maven2/io/grpc/protoc-gen-grpc-java/1.68.1/protoc-gen-grpc-java-1.68.1-linux-x86_64.exe"
    chmod +x "${GRPC_JAVA_PLUGIN}"
fi

protoc --proto_path=proto \
    --java_out="${JAVA_OUT_DIR}" \
    --plugin=protoc-gen-grpc-java="${GRPC_JAVA_PLUGIN}" \
    --grpc-java_out="${JAVA_OUT_DIR}" \
    proto/civquest/v1/civquest.proto

echo "[+] Protobuf generation complete!"
echo "    Go stubs:   ${GO_OUT_DIR}/internal/proto/v1"
echo "    Java stubs: ${JAVA_OUT_DIR}/com/civquest/mod/grpc/v1"
