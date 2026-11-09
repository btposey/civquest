# CivQuest

> **Autonomous, Dynastic Civilization Engine & Agent Orchestrator**

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

CivQuest is an autonomous settlement engine and agent orchestration platform for Minecraft (**NeoForge 21.1+ / Java 21** and **Go 1.23+**). It introduces living, intelligent settlers into vanilla worlds—capable of independent farming, barter trading, collaborative spatial construction, and permanent stakes through permadeath.

### Architecture

CivQuest uses a decoupled, dual-layer architecture separating physical voxel simulation on the Minecraft server from autonomous cognitive decision-making in dedicated external Go processes:

```
┌──────────────────────────────────────────────┐
│  Minecraft Java Server (NeoForge 21.1.265)   │
│  - Headless Agent Avatars & Skin Injection   │
│  - Embedded Netty gRPC Server (:50051)       │
│  - 20 Hz Sub-Tick Local Physical Reflexes    │
│  - Sensory Frustum Telemetry Stream (5 Hz)   │
│  - Integrated Baritone Pathfinding Wrapper   │
└──────────────────────┬───────────────────────┘
                       │ Bidirectional gRPC / Protobuf
┌──────────────────────┴───────────────────────┐
│   Go Multi-Process Orchestrator (Go 1.23+)   │
│  - Process Supervisor (`cq`) & Agent Workers │
│  - System 1: Real-time Utility Engine (<5ms) │
│  - System 2: Async LLM Inference (Ollama)    │
│  - Persistence: SQLite + sqlite-vec Vector   │
└──────────────────────────────────────────────┘
```

1. **Server Platform Mod (`mod-neoforge`)**:
   - Spawns server-side agent avatars with full player physics, inventory, and equipment capabilities.
   - Streams 5 Hz sensory frustum telemetry frames over Netty gRPC and executes sub-tick local survival reflexes.
   - Delegates voxel pathfinding and physical navigation to an integrated Baritone API wrapper.

2. **External Go Orchestrator (`orchestrator-go`)**:
   - Manages isolated per-agent OS processes (`civquest-agent`) coordinated by a supervisor daemon (`cq`).
   - Runs a fast, deterministic utility AI loop (System 1, $<5\text{ ms}$, zero LLM tokens) for combat reflexes, threat evaluation, and tactical state routing.
   - Connects to local LLM gateways (Ollama/vLLM) for asynchronous social dialogue, barter negotiation, and historical chronicles (System 2).
   - Persists relational world state and vector memory embeddings in local SQLite databases (`sqlite-vec`).

---

## Documentation & Showcase Pages

- **Project Web Showcase**:
  - [Overview](https://btposey.github.io/civquest/) — Project landing page and architectural summary.
  - [Gameplay Guide](https://btposey.github.io/civquest/gameplay.html) — Living settlement mechanics, sharecropping, barter trade, and permadeath.
  - [Systems & AI Architecture](https://btposey.github.io/civquest/systems-and-ai-testbed.html) — In-depth systems design, state machine complexity control, latency hierarchies, and embodied AI simulation.
- **Developer Documentation (`doc/ai/`)**:
  - [Architecture Overview](doc/ai/01-architecture-overview.md)
  - [IPC & Protobuf Contract](doc/ai/02-ipc-protobuf-contract.md)
  - [Platform Mod Internals](doc/ai/03-neoforge-mod-internals.md)
  - [System 1 Reactive Utility Engine](doc/ai/04-system1-utility-engine.md)
  - [System 2 Deliberative LLM Pipelines](doc/ai/05-system2-llm-pipelines.md)
  - [Storage, Databases & XDG](doc/ai/06-storage-and-xdg.md)
  - [Supervisor & Management CLI (`cq`)](doc/ai/07-supervisor-and-cli.md)
  - [Architectural Decision Records (ADRs)](doc/adr/)

---

## Getting Started

### 1. Toolchain Setup
Run the automated toolchain installer to set up `protoc`, `buf`, Go protobuf plugins, and `sqlite-vec`:

```bash
./setup.sh
```

### 2. Task Backlog & Beads
CivQuest uses **Beads (`bd`)** for issue and dependency tracking:

```bash
bd ready              # View unblocked, actionable tasks
bd show cq-bu1.1      # Inspect root task
bd update <id> --claim # Claim a task before starting work
```

### 3. Progressive Documentation
Explore the progressive discovery documentation suite in `doc/`:
- [Architecture Overview](doc/ai/01-architecture-overview.md)
- [IPC & Protobuf Contract](doc/ai/02-ipc-protobuf-contract.md)
- [Platform Mod Internals](doc/ai/03-neoforge-mod-internals.md)
- [System 1 Reactive Utility Engine](doc/ai/04-system1-utility-engine.md)
- [System 2 Deliberative LLM Pipelines](doc/ai/05-system2-llm-pipelines.md)
- [Storage, Databases & XDG](doc/ai/06-storage-and-xdg.md)
- [Supervisor & Management CLI (`cq`)](doc/ai/07-supervisor-and-cli.md)
- [Branching & Release Strategy](doc/ai/08-versioning-and-branching-strategy.md)
- [Architectural Decision Records (ADRs)](doc/adr/)

---

## Open Source Credits & Legal Notices

CivQuest is built on the shoulders of the open-source community:
- Consult [ACKNOWLEDGEMENTS.md](ACKNOWLEDGEMENTS.md) for community credits and architectural philosophy.
- Consult [NOTICES.md](NOTICES.md) for full third-party software notices and license texts.
