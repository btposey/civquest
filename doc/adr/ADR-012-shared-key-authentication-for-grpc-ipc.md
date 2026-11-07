# ADR-012: Shared-Key Authentication for Cross-Host gRPC IPC

## Status
Accepted

## Context
CivQuest employs a dual-layer architecture where the physical simulation engine (`mod-neoforge`) and the cognitive orchestration tier (`orchestrator-go`) communicate over Netty gRPC ([ADR-003](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-003-bidirectional-netty-grpc-ipc-streaming.md)).

While initial local development binds gRPC to `127.0.0.1`, real-world deployments frequently distribute workloads across multiple machines:
1. **Dedicated Game Server Host**: Runs the NeoForge Minecraft server (CPU/RAM bound, low GPU requirements).
2. **Agent Fleet & Inference Host(s)**: Runs `civctl`, multiple `civquest-agent` OS worker processes, and dedicated GPU hardware (e.g. RTX 3060 / 12GB) running local LLMs (e.g. SparkX 2.5).

Exposing Netty gRPC across network boundaries (`0.0.0.0:50051`) creates an attack surface. Without authentication, unauthorized clients on the network could inject arbitrary `AgentIntent` commands, execute administrative RPCs (`SpawnAgentEntity`, `DespawnAgentEntity`), or snoop on 5 Hz sensory telemetry frames.

## Decision
We establish **Shared-Key Bearer Token Authentication** across all gRPC communication:

1. **Shared Key Generation & Configuration**:
   - The Minecraft server mod reads `shared_key` from its configuration (`civquest-server.json` or NeoForge server configuration). If unspecified, it generates a cryptographically secure 256-bit hexadecimal token upon server startup and writes it to the local discovery artifact (`civquest-server-meta.json`).
   - The Go orchestrator and agent workers load `shared_key` from `$XDG_CONFIG_HOME/civquest/config.yaml` (`grpc.shared_key`).

2. **gRPC Protocol Metadata Header**:
   - All client requests (both unary RPCs and bidirectional streaming calls) must include an HTTP/2 metadata authorization header:
     ```http
     authorization: Bearer <shared_key>
     ```
   - In Go, a client interceptor (`grpc.WithUnaryInterceptor` and `grpc.WithStreamInterceptor`) transparently attaches this header to all outgoing RPCs.

3. **Server-Side Validation**:
   - On the Minecraft server, a Netty `ServerInterceptor` (`SharedKeyAuthInterceptor`) intercepts every incoming call before method dispatch.
   - The interceptor extracts the `authorization` metadata header, strips the `Bearer ` prefix, and compares it against the configured secret using constant-time string comparison (`MessageDigest.isEqual`) to prevent timing side-channel attacks.
   - If the header is missing, malformed, or invalid, the interceptor immediately closes the call with `Status.UNAUTHENTICATED` ("Invalid or missing shared authentication key").

4. **Network Binding**:
   - The gRPC server supports configurable binding via `bind_address` (default `127.0.0.1` for local, `0.0.0.0` for multi-host fleets) and `port` (default `50051`, or `0` for dynamic ephemeral port discovery in integration testing).

## Consequences

### Positive
- **Multi-Host Scalability**: Enables agent pools and GPU clusters to run on distinct physical hardware from the Minecraft server while preventing unauthorized network access.
- **Minimal Overhead**: Constant-time key comparison in the gRPC interceptor adds $<0.01\text{ ms}$ overhead during connection initialization and stream creation.
- **KISS Compliance**: Avoids the operational complexity of managing full PKI / mutual TLS certificates for local home lab and LAN testing environments, while remaining compatible with TLS proxies if deployed across the public Internet.

### Negative / Trade-offs
- The shared key must be securely distributed between server administrators and agent operators.
- Unencrypted traffic on untrusted networks could expose the token unless wrapped in TLS or WireGuard/VPN tunnels.
