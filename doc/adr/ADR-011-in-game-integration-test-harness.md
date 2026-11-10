# ADR-011: Multi-Tier In-Game Integration Test Harness

## Status
Accepted

## Context
CivQuest operates as a distributed polyglot system: a Minecraft 1.21.1 / NeoForge 21.1.265 Java mod (hosting headless `AgentEntity` FakePlayers, Netty gRPC, and Baritone navigation) connected via IPC to external Go orchestrator processes.

Relying solely on unit tests (JUnit 5 / Go mock tests) fails to catch runtime issues such as:
1. Broken serialization or frame dropping across live Netty gRPC streams.
2. NeoForge event bus regressions, thread deadlocks, or world tick desynchronization.
3. Failures in FakePlayer entity spawning, physics ticking, or Baritone path execution in an active `ServerLevel`.
4. Process lifecycle, zombie processes, or file descriptor leaks during shutdown.

Conversely, relying on heavy virtualization (such as Docker / Testcontainers) introduces severe drawbacks: 3–5 minute startup latencies, high CI memory consumption, Docker-in-Docker friction in GitHub Actions, and difficulty sharing Unix Domain Sockets between containers and host processes.

## Decision
We adopt a **Three-Tier Testing Pyramid** grounded in native NeoForge tooling and host-native headless server automation:

1. **Tier 1: Code-Level Unit Tests**:
   - Java: JUnit 5 for domain math, protocol serializers, and mock services.
   - Go: `go test` with mock gRPC clients for System 1 reflexes, System 2 schemas, and SQLite migrations.
2. **Tier 2: In-Engine GameTests (`runGameTestServer`)**:
   - Utilizes NeoForge ModDevGradle (`net.neoforged.moddev`) built-in `gameTestServer` run configuration.
   - Executes `@GameTest` methods directly inside a lightweight headless server world to verify Java mod internals (FakePlayer ticks, inventory state, starter camp worldgen, Baritone API wrappers) with deterministic tick accuracy.
3. **Tier 3: In-Game End-to-End Integration Harness (`runIntegrationTest`)**:
   - Launches a headless dedicated Minecraft server with optimized settings (`level-type=minecraft:flat`, `online-mode=false`, `sync-chunk-writes=false`, `max-tick-time=-1`, `pause-when-empty-seconds=-1`).
   - Mod binds Netty gRPC to an ephemeral port (`127.0.0.1:0`) or temporary Unix Domain Socket and exports discovery metadata (`civquest-server-meta.json`).
   - A dual-stage readiness probe waits for stdout `Done (X.XXs)!` and verifies `grpc.health.v1.Health/Check` returning `SERVING`.
   - A synthetic Go test probe (`//go:build integration`) connects, spawns a agent entity, verifies 5 Hz `TelemetryFrame` streaming, issues a `NavigateIntent`, asserts movement state transitions, and despawns the agent.
   - Deterministic teardown sends `/stop` to stdin, ensuring clean chunk flush, `session.lock` release, and 0 exit code.

## Consequences
### Positive
- **Full Architectural Verification**: Exercises the true end-to-end pipeline: NeoForge server -> embedded Netty -> gRPC IPC -> Go orchestrator probe -> FakePlayer avatar movement in an actual voxel level.
- **Zero Port Collisions**: Ephemeral port negotiation (`port=0`) and metadata discovery eliminate flakiness in CI and parallel local runs.
- **Fast CI Execution**: A flat plains world generates in <2 seconds, bringing full dedicated server boot time down to ~20–30s and total test execution to <45s.
- **KISS & Minimal Dependencies**: Uses standard JDK 21 and Go 1.25 toolchains directly without Docker, Testcontainers, or external virtualization.

### Negative / Trade-offs
- Dedicated server startup requires ~1.5–2GB of JVM memory during tests, requiring careful heap sizing (`-Xms1024m -Xmx2048m`) on 7GB GitHub Actions runners.
- Requires maintaining both Java GameTest definitions and Go integration probe suites.
