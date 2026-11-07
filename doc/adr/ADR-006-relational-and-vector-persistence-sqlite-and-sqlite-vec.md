# ADR-006: Relational & Vector Persistence (SQLite + sqlite-vec)

## Status
Accepted

## Context
Each autonomous agent and clan requires persistent storage for:
- Structured relational data: relationships, disposition scores, skill levels, granary ledgers, contracts, landmarks, and functional zones.
- Vector embeddings: 768-dimensional semantic embeddings of episodic memories (`nomic-embed-text`) for retrieval-augmented generation (RAG) during dialogue and chronicling.

Deploying external vector databases (such as Qdrant, Milvus, or Pinecone) introduces heavy infrastructure dependencies, networking latency, and setup friction for local or private server deployments.

## Decision
We select **embedded SQLite with the `sqlite-vec` extension**:
- **Per-Agent Storage**:
  - `memory.db`: Episodic memory relational tables and `vec0` virtual tables for 768-dim vector embeddings, relationship disposition matrices, and skill progressions.
  - `map_graph.db`: Discovered topological navigation nodes, safety scores, and paved edge graphs.
- **Per-Clan Shared Storage**:
  - `civic_registry.db`: Settlement granary, active construction projects, and functional zones.
  - `reputation.db`: Human hero prestige scores and transaction ledgers.
  - `diplomacy.db`: Inter-clan affinities and trade volumes.
  - `landmarks.db`: Cultural toponyms, coordinates, and lore.
  - `contracts.db`: Sharecropping agreements between players and agents.
- Automatic migrations are executed at startup via embedded migration scripts in Go.

## Consequences
### Positive
- Zero external database daemon dependencies; single-file storage per database.
- High performance, in-process queries with microsecond read latencies.
- Built-in vector search via `sqlite-vec` enables local semantic search directly alongside relational metadata.

### Negative / Trade-offs
- The Go build pipeline requires CGO or dynamic loading for the `sqlite-vec` C shared library.
- Multi-process access to shared clan databases requires SQLite WAL mode and short, retry-safe transactions.
