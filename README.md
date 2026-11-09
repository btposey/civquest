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

CivQuest is an autonomous, dynastic settlement engine introducing intelligent, humanoid, process-isolated `FakePlayer` agents driven by external Go orchestrators alongside the game world.

### Dual Mission & Design Philosophy

CivQuest is founded on two equal, complementary pillars:

1. **An Engaging In-Game Settlement Experience**:
   A deep, living Minecraft modification introducing authentic autonomous settlements, humanoid `FakePlayer` settlers, emergent sharecropping, barter trade economies, generational lineages, and permanent stakes through permadeath.

2. **Systems Engineering & Embodied AI Testbed (Academic & Professional)**:
   An exercise in robust systems design for AI-integrated embodied software. CivQuest tames non-deterministic prompt chaining by combining formal Hierarchical Finite State Machines (HFSM), bounded typed slot queues, and strict OS process isolation. The architecture is inspired by biological nervous systems:
   - **Layer 1A (Spinal / Peripheral Reflexes)**: Native in-engine server tick loops (20 Hz, $<0.1\text{ ms}$, 0 IPC) for instant hazard evasion and physical survival.
   - **Layer 1B (Subcortical / Tactical Intuition)**: External deterministic Go utility AI (5 Hz, $<5\text{ ms}$, 0 LLM tokens) managing fight-or-flight matrices, navigation goals, and daily schedules.
   - **Layer 2 (Cortical / Strategic Cognition)**: Asynchronous LLM inference (Ollama/vLLM) and vector RAG (`sqlite-vec`) for social dialogue, trade bargaining, and architectural blueprints.

> **Purpose-Built for Minecraft**: CivQuest is engineered 100% for the Minecraft server environment. It does not introduce artificial hardware abstractions or leaky robotics drivers. Instead, Minecraft serves as a low-cost, high-fidelity spatial simulation sandbox where systems engineering principles, latency hierarchies, and perception-action feedback loops are proven in practice—yielding transferable architectural insights for future embodied systems.

### The Dual-Layer Architecture:
1. **Server Platform Mod (`mod-neoforge`)**:
   - Engine: Built on **NeoForge 21.1+** (compatible with Java 21 game servers).
   - Functions: Spawns headless `AgentEntity` (`FakePlayer`) avatars; delivers dynamic agent skin packets; streams sensory frustum frames (5 Hz) over embedded Netty gRPC; delegates physical voxel pathfinding to an integrated Baritone API wrapper; preserves vanilla villages and villagers for dynamic co-existence.
2. **External Multi-Process Orchestrator (`orchestrator-go`)**:
   - Engine: **Go 1.23+**.
   - Functions: Multi-process supervisor (`cq`) spawning dedicated OS worker processes (`civquest-agent`) per agent.
   - **System 1 (Reactive Utility AI Engine)**: Sub-$5\text{ ms}$, zero-token deterministic loop handling combat reflexes, fight-or-flight confidence scoring, hunger, road paving, and daily career state transitions.
   - **System 2 (Deliberative LLM Engine)**: Asynchronous JSON-validated inference pipelines connected to local gateways (Ollama/vLLM) for dialogue, barter trade, Chronicler book writing, landmark toponymy, and Architect quadrant construction.
   - **Persistence Tier**: Embedded SQLite databases per agent and clan with `sqlite-vec` vector RAG.

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
