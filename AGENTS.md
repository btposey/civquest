# CivQuest Agent Directives & Architecture Guide

Welcome to **CivQuest** (`https://github.com/btposey/civquest`). This document establishes the foundational principles, invariants, progressive documentation map, and operational workflows for autonomous engineering agents working on this repository.

---

> [!IMPORTANT]
> **Legal & Trademark Disclaimer**  
> **NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT.**  
> *Minecraft* is a registered trademark of Mojang Synergies AB / Microsoft Corporation.  
> CivQuest is an independent, community-developed software modification and autonomous agent orchestration platform. CivQuest is not affiliated with, endorsed by, sponsored by, or associated with Mojang Synergies AB, Microsoft Corporation, or any of their subsidiaries. All references to Minecraft, related game mechanics, and block identifiers are strictly nominative references for technical compatibility and modding interoperability under applicable fair use doctrines and community guidelines.

---

## 1. Project Mission & Core Architecture

CivQuest is an autonomous, dynastic settlement engine introducing intelligent, humanoid, process-isolated `FakePlayer` agents driven by external Go orchestrators alongside vanilla game entities.

### The Dual-Layer Architecture:
1. **Minecraft Java Server Mod (`mod-neoforge`)**:
   - Engine: **Minecraft 1.21.1** powered by **NeoForge 21.1.265** (Java 21).
   - Functions: Spawns headless `AgentEntity` (`FakePlayer`) avatars; delivers dynamic agent skin packets; streams sensory frustum frames (5 Hz) over embedded Netty gRPC; delegates physical voxel pathfinding to an integrated Baritone API wrapper; preserves vanilla villages and villagers for rich inter-entity co-existence.
2. **External Multi-Process Go Orchestrator (`orchestrator-go`)**:
   - Engine: **Go 1.25+**.
   - Functions: Multi-process supervisor (`cq`) spawning dedicated OS worker processes (`civquest-agent`) per agent.
   - **System 1 (Reactive Utility AI Engine)**: Sub-$5\text{ ms}$, zero-token deterministic loop handling combat reflexes, fight-or-flight confidence scoring, hunger, road paving, and daily career state transitions.
   - **System 2 (Deliberative LLM Engine)**: Asynchronous JSON-validated inference pipelines connected to local gateways (Ollama/vLLM) for dialogue, barter trade, Chronicler book writing, landmark toponymy, and Architect quadrant construction.
   - **Persistence Tier**: Embedded SQLite databases per agent and clan with `sqlite-vec` vector RAG.

---

## 2. Invariants & Strict Rules

All agents contributing to CivQuest must uphold these non-negotiable architectural invariants:

1. **Process-Per-Agent Isolation**: Never loop through multiple agents in a single Go process thread. Each active agent is an independent OS process with dedicated state, logs, UNIX domain sockets, and SQLite databases.
2. **XDG Base Directory Compliance**: Hardcoded paths are strictly prohibited. All paths must be dynamically resolved via `$XDG_CONFIG_HOME`, `$XDG_DATA_HOME`, and `$XDG_STATE_HOME`.
3. **Zero Token Survival Reflexes**: System 1 reflexes (shield raising, running from creepers, eating, pathing) must **never** invoke LLM inference. System 1 must execute synchronously in $<5\text{ ms}$.
4. **Strict Permadeath Lifecycle**: When an agent dies in Minecraft (`DeathEvent`), they never respawn. The agent process must record memorial chronicles, move its state directory to `$XDG_DATA_HOME/civquest/graveyard/`, and exit cleanly with code 0.
5. **Server-Side FakePlayer Avatars**: Autonomous agent entities are simulated server-side using standard FakePlayer implementations with deterministic offline UUIDs and custom skin packet distribution.
6. **Version & Branching Discipline**: Feature development targets the stable LTS baseline (**Minecraft 1.21.1 / NeoForge 21.1.265**) on `main`. Upstream updates track dedicated version branches (`version/mc-*`) with backporting to active branches.
7. **Spatial Construction Order**: All collaborative multi-agent building projects must follow Spatial Quadrant Partitioning ($NW, NE, SW, SE$) and build bottom-up ($Y_{\text{min}} \to Y_{\text{max}}$) to prevent collisions and builder entrapment.
8. **Operator Interaction & Subagent Delegation Directive**: The agent holding the conversational thread with the human operator must aggressively make use of right-sized subagents (`flash_lite` for trivial tasks, `flash` for standard tasks, `pro` for high-reasoning decomposition) and keep the chat thread open, responsive, and available to the operator. Never block or monopolize the operator turn with monolithic background execution—delegate heavy tasks to subagents and remain conversational.

---

## 3. Progressive Discovery Documentation (`doc/ai`)

Before beginning work on any subsystem, read the corresponding progressive discovery documentation in `doc/ai/`:

- [00-index.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/00-index.md): Master documentation index and agent onboarding guide.
- [01-architecture-overview.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/01-architecture-overview.md): System topology, data flow diagrams, and component interactions.
- [02-ipc-protobuf-contract.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/02-ipc-protobuf-contract.md): Protobuf specification, gRPC streaming, telemetry frames, and intents.
- [03-neoforge-mod-internals.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/03-neoforge-mod-internals.md): FakePlayer lifecycle, Baritone wrapper, sensory frustum, and skin injection.
- [04-system1-utility-engine.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/04-system1-utility-engine.md): Fight-or-flight mathematics, utility curves, daily schedules, and emergent hobbies.
- [05-system2-llm-pipelines.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/05-system2-llm-pipelines.md): LLM gateway integration, dual-temperature sampling, JSON schemas, Chronicler pagination, and Architect grammar.
- [06-storage-and-xdg.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/06-storage-and-xdg.md): SQLite relational schemas, `sqlite-vec` vector RAG, and filesystem layouts.
- [07-supervisor-and-cli.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/07-supervisor-and-cli.md): `cq` commands, supervisor daemon, UNIX socket telemetry, and process management.
- [08-versioning-and-branching-strategy.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/08-versioning-and-branching-strategy.md): Minecraft/NeoForge version matrix, version branches, and backporting rules.
- [09-adversarial-review-and-pr-lifecycle.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/09-adversarial-review-and-pr-lifecycle.md): Multi-agent adversarial review, hypothesis confirmation, PR comments, and Go/No-Go gates.

---

## 4. Architectural Decision Records (`doc/adr`)

Major technical decisions are formally recorded in `doc/adr/`:
- [ADR-001: Polyglot Monorepo Structure](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-001-polyglot-monorepo-structure.md)
- [ADR-002: Minecraft & NeoForge Versioning and Multi-Branch Release Strategy](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-002-minecraft-neoforge-version-and-branching-strategy.md)
- [ADR-003: Bidirectional Netty gRPC IPC Streaming](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-003-bidirectional-netty-grpc-ipc-streaming.md)
- [ADR-004: Process-Per-Agent Isolation & XDG Base Directory Compliance](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-004-process-per-agent-isolation-and-xdg-compliance.md)
- [ADR-005: Two-Tier Cognitive Architecture (System 1 Utility vs. System 2 LLM)](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-005-two-tier-cognitive-architecture-system1-utility-system2-llm.md)
- [ADR-006: Relational & Vector Persistence (SQLite + sqlite-vec)](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-006-relational-and-vector-persistence-sqlite-and-sqlite-vec.md)
- [ADR-007: Server-Side FakePlayer Architecture & Packet Skin Injection](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-007-server-side-fakeplayer-and-packet-skin-injection.md)
- [ADR-008: Permadeath Lifecycle & Dynastic Reproduction Governor](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-008-permadeath-lifecycle-and-dynastic-reproduction-governor.md)
- [ADR-009: Baritone API Pathfinding Wrapper](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-009-baritone-api-pathfinding-wrapper.md)
- [ADR-010: Spatial Quadrant Partitioning for Multi-Agent Construction](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-010-spatial-quadrant-partitioning-for-multi-agent-construction.md)
- [ADR-011: In-Game Integration Test Harness](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-011-in-game-integration-test-harness.md)
- [ADR-012: Shared-Key Authentication for Cross-Host gRPC IPC](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-012-shared-key-authentication-for-grpc-ipc.md)
- [ADR-013: Host CPU Multimedia SIMD Extensions (AVX2/AVX-512) for System 1 Reflexes and Vector Operations](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-013-host-cpu-multimedia-simd-extensions-for-system1.md)
- [ADR-014: Jar-in-Jar Dependency Packaging & Client-Safe Mod Lifecycle Isolation](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-014-jarjar-dependency-packaging-and-client-safe-lifecycle.md)

---

## 5. Beads Issue Tracker & Workflow (`bd`)

Task and issue tracking in CivQuest uses **Beads (`bd`)**.
**Do NOT use generic TODO lists or informal task files.**

### Essential Commands:
```bash
bd prime                 # Output AI-optimized workflow context and command guide
bd ready                 # Find open, unblocked tasks ready to be worked on
bd show <id>             # Inspect issue details, acceptance criteria, and blockers
bd update <id> --claim   # Claim an issue before starting work
bd close <id>            # Close an issue upon completing verification criteria
bd dep add <id> <blocker># Define dependencies between issues
bd dolt push             # Push beads Dolt state to remote
```

### Issue Prefix:
All issues use the prefix `cq-` (e.g., `cq-1`, `cq-epic-infra`).

---

## 6. Development & Tooling Guidelines

- **Java Development**: Use OpenJDK 21. Build via Gradle (`./gradlew build`). Ensure clean compilation with NeoForge 21.1.265.
- **Go Development**: Use Go 1.25+. Format code with `gofmt` and verify tests with `go test ./...`.
- **Protocol Buffers**: Regenerate stubs when modifying `proto/civquest/v1/civquest.proto` using `buf` or `protoc`.
- **Non-Interactive Commands**: Always use `-f` and `-y` flags for file and shell operations (`rm -rf`, `cp -f`, etc.) to prevent blocking.
