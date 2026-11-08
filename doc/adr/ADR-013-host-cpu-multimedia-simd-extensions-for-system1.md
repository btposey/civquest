# ADR-013: Host CPU Multimedia SIMD Extensions (AVX2/AVX-512) for System 1 Reflexes and Vector Operations

## Status
Accepted

## Context
CivQuest utilizes a two-tier cognitive architecture ([ADR-005](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-005-two-tier-cognitive-architecture-system1-utility-system2-llm.md)):
- **System 1**: High-frequency ($5\text{ Hz}$), deterministic reactive utility engine requiring sub-$5\text{ ms}$ latency and consuming zero LLM tokens.
- **System 2**: Deliberative, asynchronous LLM inference (e.g. SparkX 2.5) for dialogue, diplomacy, and chronicling.

Target deployment hardware includes single-GPU workstations or modest local hardware (e.g. NVIDIA GTX 1650 4GB or RTX 3060 12GB). 
If System 1 sensory processing, 3D threat vector calculations, or vector similarity searches offload computations to the GPU, they compete directly for:
1. GPU VRAM (critical for LLM model weights and KV-cache).
2. CUDA / Tensor cores (causing kernel scheduling contention and token generation stalls).
3. Host-to-Device PCIe bandwidth (introducing bus transfer latency on every 200ms tick frame).

## Decision
We mandate that **System 1 reactive logic, 3D spatial geometry, utility scoring, and vector similarity retrieval must execute entirely on host CPU cores utilizing multimedia vector SIMD extensions**:

1. **CPU SIMD Instruction Sets**:
   - System 1 math kernels, threat field calculations, and distance matrices in Go must compile with and leverage CPU vector extensions:
     - x86_64: **AVX2**, **FMA**, and **AVX-512** (where supported).
     - ARM64: **NEON**.
   - These instructions execute parallel vector arithmetic (dot products, Euclidean distance matrices, linear algebra) in microseconds directly on the CPU L1/L2 cache hierarchy.

2. **Persistence Tier Alignment (`sqlite-vec`)**:
   - Agent spatial and episodic memory retrieval uses `sqlite-vec` ([ADR-006](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-006-relational-and-vector-persistence-sqlite-and-sqlite-vec.md)).
   - `sqlite-vec` is natively compiled with AVX2/NEON SIMD acceleration, computing cosine distance and nearest-neighbor vector search directly on host CPU threads without touching the GPU.

3. **100% Dedicated GPU Budget for System 2**:
   - All available GPU VRAM and compute capacity (GTX 1650 4GB / RTX 3060 12GB) are strictly reserved for the System 2 deliberative LLM (e.g. SparkX 2.5 / Ollama / vLLM).
   - The Go agent orchestrator interacts with the GPU exclusively via standard OpenAI-compatible HTTP/JSON API endpoints, with zero direct CUDA dependencies in the Go binary.

## Consequences

### Positive
- **Zero VRAM Contention**: Eliminates the risk of System 1 running the GPU out of memory or thrashing VRAM allocations during intense combat or world exploration.
- **Sub-Millisecond Execution**: Native SIMD vector math executes in $<1\text{ ms}$, effortlessly satisfying the $<5\text{ ms}$ tick budget.
- **Hardware Portability**: Enables autonomous agent fleets to run on headless CPU-only cloud nodes or lightweight edge servers while directing System 2 inference to a centralized remote GPU server.

### Negative / Trade-offs
- CPU utilization increases on machines running dozens of concurrent agent worker processes; requires thread pool governance via `cq` supervisor.
