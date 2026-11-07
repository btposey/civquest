# ADR-001: Polyglot Monorepo Structure

## Status
Accepted

## Context
CivQuest requires a tightly coupled, dual-layer architecture:
1. A server-side mod running inside the Minecraft Java Edition JVM (`mod-neoforge`).
2. An external multi-process autonomous agent orchestrator written in Go (`orchestrator-go`).
3. A shared communications contract defined in Protocol Buffers (`proto/civquest/v1/civquest.proto`).

Maintaining these components across separate repositories would introduce synchronization friction, version drift between protobuf definitions and generated stubs, and complicate end-to-end integration testing and continuous delivery.

## Decision
We adopt a unified polyglot monorepo layout:
- `proto/`: Master `.proto` files defining gRPC services and messages.
- `mod-neoforge/`: Java 21 Gradle project for the NeoForge mod.
- `orchestrator-go/`: Go 1.23+ workspace for the supervisor daemon (`civctl`) and agent worker binary (`civquest-agent`).
- `configs/`: Default configuration templates and blueprint schema definitions.
- `doc/`: Architectural Decision Records (`doc/adr/`) and progressive discovery guides (`doc/ai/`).

## Consequences
### Positive
- Single source of truth for the gRPC/Protobuf contract across both Java and Go codebases.
- Coordinated atomic commits across mod internals, Go behavioral engines, and protocol definitions.
- Simplified continuous integration with unified change detection and linting.

### Negative / Trade-offs
- Build tooling must accommodate both Gradle (Java 21) and Go toolchains.
- Developers working solely on Go or Java must clone the full repository.
