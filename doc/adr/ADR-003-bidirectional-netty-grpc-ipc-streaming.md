# ADR-003: Bidirectional Netty gRPC IPC Streaming

## Status
Accepted

## Context
CivQuest requires low-latency, bidirectional communication between the Minecraft server mod (running in the JVM) and autonomous agent processes (running in Go). Requirements include:
- High-frequency sensory telemetry (~5 Hz) sent from Minecraft to each agent.
- Immediate asynchronous event notifications (damage taken, death, incoming trade/chat).
- Sub-5ms dispatch of behavioral intents (movement, attack, block interaction) from Go back to Minecraft.
- Strong typing, cross-language code generation, and binary wire efficiency.

## Decision
We select **gRPC over HTTP/2 with Protocol Buffers v3**:
- The NeoForge mod hosts an embedded Netty-based gRPC server listening on `127.0.0.1:50051`.
- The primary contract is a bidirectional streaming RPC:
  `rpc ConnectAgent(stream AgentIntent) returns (stream TelemetryFrame);`
- Administrative endpoints (`SpawnAgentEntity`, `DespawnAgentEntity`, `ExportRecipeGraph`) are unary RPCs invoked by the `civctl` supervisor.

## Consequences
### Positive
- Strict compile-time typing and automated client/server stub generation in both Java and Go.
- High throughput, low latency multiplexing over persistent TCP connections.
- Clean separation of concerns: the Minecraft mod acts purely as a physical execution and sensory engine, while Go owns cognitive decision-making.

### Negative / Trade-offs
- Embedded Netty gRPC server inside the Minecraft server process adds JVM memory overhead (~30-50MB).
- Network disconnects or stream drops must be handled with robust auto-reconnection and backoff logic.
