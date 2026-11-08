# Progressive Discovery: Master Index

Welcome to the CivQuest AI Progressive Discovery documentation suite. These documents are modular guides designed to provide specialized subagents and developers with deep, targeted context on each layer of the CivQuest architecture.

## Document Directory

| Document | Purpose | Primary Audience |
|---|---|---|
| [01-architecture-overview.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/01-architecture-overview.md) | High-level system architecture, dual-layer design, and data flows | All agents & architects |
| [02-ipc-protobuf-contract.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/02-ipc-protobuf-contract.md) | Protobuf contract, gRPC bidirectional streaming, intents & telemetry | Mod and backend engineers |
| [03-neoforge-mod-internals.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/03-neoforge-mod-internals.md) | FakePlayer lifecycle, Baritone API integration, skin packets, and worldgen overrides | NeoForge mod developers |
| [04-system1-utility-engine.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/04-system1-utility-engine.md) | Sub-5ms reactive utility AI, fight-or-flight formula, and daily career FSM | Go behavioral engineers |
| [05-system2-llm-pipelines.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/05-system2-llm-pipelines.md) | Deliberative LLM pipelines, prompt templates, JSON schemas, Chronicler, and Architect | AI & LLM pipeline engineers |
| [06-storage-and-xdg.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/06-storage-and-xdg.md) | XDG filesystem layout, SQLite schemas, and sqlite-vec RAG embeddings | Database & storage engineers |
| [07-supervisor-and-cli.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/07-supervisor-and-cli.md) | `cq` CLI command suite, supervisor daemon, process lifecycle, and UNIX sockets | CLI & DevOps engineers |
| [08-versioning-and-branching-strategy.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/08-versioning-and-branching-strategy.md) | Minecraft & NeoForge version matrix, version branches, and backporting discipline | Release & maintenance engineers |
| [09-adversarial-review-and-pr-lifecycle.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/09-adversarial-review-and-pr-lifecycle.md) | Multi-agent adversarial review, hypothesis criticism, and Go/No-Go PR quality gates | All agents & reviewers |
| [10-integration-testing-harness.md](file:///home/brian/Workspaces/btposey/civquest/doc/ai/10-integration-testing-harness.md) | Multi-tier test architecture, headless server runner, and Go integration probe | Test architects & CI engineers |

## Recommended Reading Path
- If you are implementing **Minecraft Mod features**: Read `01` -> `02` -> `03` -> `08`.
- If you are implementing **Go Agent Behavior (Survival / Movement)**: Read `01` -> `02` -> `04` -> `06`.
- If you are implementing **LLM Pipelines (Dialogue / Chronicler / Architecture)**: Read `01` -> `02` -> `05` -> `06`.
- If you are implementing **CLI Tooling or Supervisor Daemon**: Read `01` -> `06` -> `07`.
