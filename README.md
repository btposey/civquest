# CivQuest

> **Autonomous Civilization Engine & Agent Orchestrator for Minecraft**

[![CI](https://github.com/btposey/civquest/actions/workflows/ci.yml/badge.svg)](https://github.com/btposey/civquest/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Platform: NeoForge](https://img.shields.io/badge/Loader-NeoForge%2021.1+-green.svg)](https://neoforged.net/)
[![Go: 1.23+](https://img.shields.io/badge/Go-1.23+-00ADD8.svg)](https://go.dev/)

---

> [!IMPORTANT]
> **Legal & Trademark Disclaimer**  
> **NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT.**  
> *Minecraft* is a registered trademark of Mojang Synergies AB / Microsoft Corporation.  
> CivQuest is an independent, community-developed software modification and autonomous agent orchestration platform. CivQuest is not affiliated with, endorsed by, sponsored by, or associated with Mojang Synergies AB, Microsoft Corporation, or any of their subsidiaries. All references to Minecraft, related game mechanics, and block identifiers are strictly nominative references for technical compatibility and modding interoperability under applicable fair use doctrines and community guidelines.

---

## Overview

CivQuest is an open-source Minecraft modification (**NeoForge 21.1+ / Java 21**) coupled with an external multi-process orchestrator (**Go 1.23+**). The project enables autonomous, process-isolated agent entities within Minecraft worlds, communicating across an embedded gRPC telemetry and control stream.

This document serves as the developer guide for configuring a local workspace, building subsystems, executing tests, and contributing code. End-user documentation, gameplay guides, and system showcases are hosted on the [project showcase pages](https://btposey.github.io/civquest/).

---

## Prerequisites

Ensure the following dependencies are installed or managed via the automated setup script:

- **Java JDK 21** (e.g., Eclipse Temurin 21)
- **Go 1.23+**
- **Protocol Buffers Compiler (`protoc` 28+)** & **Buf CLI**
- **SQLite 3** with `sqlite-vec` extension library
- **Beads (`bd`)** for issue and backlog management

---

## Development Environment Setup

### 1. Automated Toolchain Installation

Run the project setup script to install `protoc`, `buf`, required Go gRPC plugins, the `protoc-gen-grpc-java` plugin, `sqlite-vec` binaries, and `bd`:

```bash
./setup.sh
```

Ensure local binary directories are included in your shell `PATH`:

```bash
export PATH="${HOME}/.local/bin:$(go env GOPATH)/bin:${PATH}"
```

### 2. Manual Dependency Verification

Verify tool availability across your environment:

```bash
java -version    # Must report Java 21
go version       # Must report Go 1.23+
protoc --version # Protobuf compiler
buf --version    # Buf CLI
bd version       # Beads issue tracker
```

---

## Building & Testing

### Protocol Buffer Compilation

When modifying definitions in `proto/civquest/v1/civquest.proto`, regenerate the Go and Java gRPC stubs:

```bash
./scripts/gen-proto.sh
```

### Go Orchestrator (`orchestrator-go`)

Build all binaries (`cq` supervisor and `civquest-agent` worker) and execute tests:

```bash
cd orchestrator-go

# Run unit and integration tests
go test ./...

# Build CLI and worker binaries
go build ./cmd/cq
go build ./cmd/civquest-agent
```

### NeoForge Server Mod (`mod-neoforge`)

Compile the server mod and run test suites using the Gradle wrapper:

```bash
# Build the mod JAR
./gradlew build

# Run unit tests
./gradlew test
```

### Integration Test Harness

Launch the automated playtest server and headless agent driver:

```bash
# Start local playtest dedicated server
./scripts/start-playtest-server.sh

# Run end-to-end integration test harness
./scripts/run-integration-test.sh
```

---

## Contributing Workflow

### Task Management with Beads (`bd`)

CivQuest uses **Beads (`bd`)** for distributed issue tracking. All ongoing engineering tasks and dependencies are tracked through `bd`:

```bash
bd prime                 # Output AI and contributor workflow context
bd ready                 # List open, unblocked tasks ready for work
bd show <task-id>        # View task requirements and acceptance criteria
bd update <task-id> --claim # Claim a task before beginning implementation
bd close <task-id>       # Mark task complete after passing validation
```

### Code Style & Quality Standards

- **Go**: Ensure all Go code is formatted using `gofmt` and passes `go vet ./...` and `golangci-lint` without warnings.
- **Java**: Follow standard NeoForge conventions and ensure code compiles with zero compiler warnings under Java 21.
- **Protobuf**: Keep Protobuf contracts backward-compatible. Validate definitions using `buf lint`.
- **Commits**: Write clear, imperative commit messages describing the functional impact of changes.

### Technical Reference Documentation

For deeper subsystem details and architectural specifications, consult the reference documents in `doc/`:

- [Progressive Discovery Documentation](doc/ai/00-index.md) — In-depth guides for IPC protocols, mod internals, AI loops, storage schemas, and release management.
- [Architectural Decision Records (ADRs)](doc/adr/) — Formal records of technical choices, invariants, and design patterns.

---

## Open Source & Legal Notices

- See [ACKNOWLEDGEMENTS.md](ACKNOWLEDGEMENTS.md) for open-source credits and third-party software details.
- See [NOTICES.md](NOTICES.md) for full license texts and distribution disclosures.
- Licensed under the [MIT License](LICENSE).
