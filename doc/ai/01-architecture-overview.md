# Progressive Discovery: Architecture Overview

## 1. System Philosophy & Dual Purpose

CivQuest is engineered around two foundational, equal pillars:

1. **Autonomous Minecraft Settlement Gameplay**:
   Transforms Minecraft into a living, persistent civilization simulation featuring humanoid `FakePlayer` settlers, sharecropping, dynamic barter economies, emergent careers, spatial building, and permanent generational permadeath.
2. **Systems Engineering & Embodied AI Sandbox**:
   Serves as an academic and professional testbed for AI systems architecture. By decomposing agent cognition into biological analogs—Spinal Reflexes, Subcortical Intuition, and Cortical Cognition—CivQuest demonstrates how formal state machines (HFSM) and bounded slot queues eliminate non-deterministic prompt chaining and prevent latency bottlenecks.

> **Purpose-Built for Minecraft**: CivQuest is 100% purpose-built for the Minecraft voxel simulation without premature hardware abstraction layers (HAL). The insights gained provide conceptual and architectural foundations for future physical embodied systems engineered whole-cloth.

### The Tripartite Nervous System:
- **Layer 1A: Reflex (Spinal Cord / In-Engine Mod)**: Native Java execution in `mod-neoforge` at 20 Hz ($<0.1\text{ ms}$, 0 IPC round-trip). Handles sub-tick physical emergencies (shield deflection, item pickup, step climbing) and reports executed reactions upward via `ReflexNotice`.
- **Layer 1B: Intuition (Subcortical / Tactical Harness)**: Deterministic Go utility engine in `civquest-agent` at 5 Hz ($<5\text{ ms}$, 0 LLM tokens). Evaluates threat matrices, runs the HFSM router, and streams kinetic intents.
- **Layer 2: Cognition (Prefrontal Cortex / Strategic Brain)**: Asynchronous LLM reasoning (Ollama/vLLM) and vector RAG (`sqlite-vec`). Compiles high-level goals into parameterized `CognitiveDirective` targets for the Intuition layer.

```mermaid
flowchart TB
    subgraph Minecraft_JVM ["Minecraft Java Edition Server (NeoForge 21.1.265)"]
        World["Minecraft World & Physics Engine"]
        Camp["Pioneer Camp Spawner"]
        Entity["AgentEntity (FakePlayer Avatar)"]
        Reflex["Layer 1A: In-Engine Reflexes (<0.1ms, 20 Hz)"]
        Frustum["Sensory Frustum (5 Hz Telemetry Scanner)"]
        Baritone["Baritone API Navigation Wrapper"]
        Netty["Embedded Netty gRPC Server (:50051)"]

        World --> Camp
        Camp --> Entity
        Entity <--> Reflex
        Entity --> Frustum
        Frustum --> Netty
        Netty --> Baritone
        Baritone --> Entity
    end

    subgraph Go_Orchestrator ["External Go Multi-Process Orchestrator"]
        Supervisor["cq supervisor daemon"]
        Worker1["Agent Process: Kaelen (civquest-agent)"]
        Worker2["Agent Process: Lyra (civquest-agent)"]

        subgraph Worker_Internals ["Per-Agent Process Internals"]
            gRPC_Client["gRPC Bidirectional Stream"]
            S1["Layer 1B: Tactical Intuition & HFSM (<5ms, 0 Tokens)"]
            S2["Layer 2: Deliberative Cognition & LLM (Async)"]
            DB["Local SQLite + sqlite-vec Store"]

            gRPC_Client --> S1
            S1 --> S2
            S1 --> DB
            S2 --> DB
            S1 --> gRPC_Client
        end

        Supervisor -->|Fork / Monitor| Worker1
        Supervisor -->|Fork / Monitor| Worker2
    end

    Netty <===>|HTTP/2 gRPC Stream (TelemetryFrame & AgentIntent)| gRPC_Client
```

## 2. Component Breakdown

### Component 1: `mod-neoforge`
The server-side Minecraft mod acts as the sensory input organ and physical actuator. It does not perform high-level cognitive deliberation. Its responsibilities are:
1. **Starter Camp World Generation**: Generates custom pioneer campfire structures (`civquest:pioneer_campfire`) while preserving vanilla villages and villagers for rich inter-entity co-existence and trade.
2. **`AgentEntity`**: Subclassing `FakePlayer` to provide full player simulation (hunger ticking, item usage, combat knockback, equipment slots) for server-side autonomous agents.
3. **Sensory Frustum**: Gathering visible entities ($R = 24$), interesting blocks ($R = 16$), chat events, and damage frames at 5 Hz.
4. **Physical Actuation**: Executing navigation intents via Baritone, block interactions (harvesting, paving, sleeping), container transfers, signpost placement, and lectern book insertion.

### Component 2: `orchestrator-go`
The external Go orchestrator contains all cognitive, psychological, and civic logic:
1. **`cq` Supervisor**: Spawns and monitors agent worker processes, oversees dynastic reproduction, and exposes administrative CLI commands.
2. **`civquest-agent`**: The single-agent runtime executing:
   - **System 1**: Immediate survival reflexes, fight-or-flight calculations, saturation management, and daily utility scheduling.
   - **System 2**: Asynchronous LLM interactions for dialogue, trade barter, book writing, and blueprint quadrant layout.
   - **Data Management**: Reading and mutating local SQLite databases (`memory.db`, `map_graph.db`) and clan-level databases (`civic_registry.db`, `diplomacy.db`, `reputation.db`).
