# Acknowledgements & Open Source Credits

The **CivQuest** platform is an independent, community-driven open-source project. We believe deeply in the open-source ethos and acknowledge that this project is made possible by the remarkable work of open-source software creators, maintainers, foundations, and communities worldwide.

---

> [!IMPORTANT]
> **Legal & Trademark Disclaimer**  
> **NOT AN OFFICIAL MINECRAFT PRODUCT. NOT APPROVED BY OR ASSOCIATED WITH MOJANG OR MICROSOFT.**  
> *Minecraft* is a registered trademark of Mojang Synergies AB / Microsoft Corporation.  
> CivQuest is an independent, community-developed software modification and autonomous agent orchestration platform. CivQuest is not affiliated with, endorsed by, sponsored by, or associated with Mojang Synergies AB, Microsoft Corporation, or any of their subsidiaries. All references to Minecraft, related game mechanics, and block identifiers are strictly nominative references for technical compatibility and modding interoperability under applicable fair use doctrines and community guidelines.

---

## Foundational Open Source Projects & Communities

We express our sincere gratitude to the following open-source projects, tools, and platforms that form the pillars of CivQuest:

### 1. Game Modding Engine & Navigation
- **[NeoForged / NeoForge](https://neoforged.net/)** (NeoForged Team) — LGPL-2.1 License  
  *Provides the modern, high-performance modding API, event bus, and game engine patching framework enabling server-side modification on modern Java editions.*
- **[Baritone](https://github.com/cabaletta/baritone)** (cabaletta & the Baritone Contributors) — LGPL-3.0 License  
  *Provides the state-of-the-art voxel pathfinding, dynamic obstacle traversal, parkour navigation, and spatial execution algorithms powering our autonomous physical avatars.*

### 2. High-Performance Language Runtimes & IPC
- **[The Go Programming Language](https://go.dev/)** (The Go Authors / Google LLC) — BSD-3-Clause License  
  *Powers the multi-process supervisor (`civctl`), System 1 reactive utility loops, and System 2 deliberative agent worker binaries (`civquest-agent`).*
- **[Eclipse Temurin OpenJDK](https://adoptium.net/)** (Eclipse Adoptium / Eclipse Foundation) — GPL-2.0-with-classpath-exception License  
  *Provides the robust OpenJDK 21 Java runtime environment powering the server JVM mod.*
- **[gRPC](https://grpc.io/) & [Protocol Buffers](https://protobuf.dev/)** (Google LLC / Cloud Native Computing Foundation) — Apache-2.0 & BSD-3-Clause Licenses  
  *Enables sub-millisecond, strongly typed bidirectional streaming IPC over HTTP/2 between the Java game server and external Go processes.*
- **[Netty](https://netty.io/)** (The Netty Project) — Apache-2.0 License  
  *Powers high-throughput, low-latency asynchronous network socket I/O for the embedded gRPC server.*

### 3. Embedded Persistence & Vector Intelligence
- **[SQLite](https://www.sqlite.org/)** (D. Richard Hipp & SQLite Authors) — Public Domain  
  *Provides rock-solid, zero-configuration relational persistence per agent and clan.*
- **[sqlite-vec](https://github.com/asg017/sqlite-vec)** (Alex Garcia) — Apache-2.0 & MIT Licenses  
  *Provides native 768-dimensional float vector storage and cosine similarity KNN retrieval directly within SQLite for agent episodic memory RAG.*

### 4. Agent Task Tracking & Version-Controlled Sync
- **[Beads (`bd`)](https://github.com/gastownhall/beads)** (Gas Town Hall / Steve Yegge) — MIT & Apache-2.0 Licenses  
  *Powers our durable, graph-based issue tracking, parent-child epic hierarchies, blocker management, and multi-agent coordination.*
- **[Dolt](https://www.dolthub.com/)** (DoltHub, Inc.) — Apache-2.0 License  
  *The world's first version-controlled SQL database engine powering distributed, peer-to-peer Beads issue synchronization over Git remotes.*

### 5. CLI Tooling & Ecosystem Libraries
- **[Cobra](https://github.com/spf13/cobra)** (Steve Francia & Contributors) — Apache-2.0 License  
  *Powers the flexible and intuitive command-line interface for the `civctl` supervisor suite.*
- **[Buf](https://buf.build/)** (Buf Technologies, Inc.) — Apache-2.0 License  
  *Provides modern schema-driven Protocol Buffer linting, formatting, and cross-language stub generation.*

### 6. Culinary & Agrarian Ecosystem Mod Inspiration
- **[Farmer's Delight](https://github.com/vectorwing/FarmersDelight)** (vectorwing) — MIT License  
  *Inspiration and companion DAG integration for culinary crafting, soil cultivation, and nourishment.*
- **[Vinery & Let's Do](https://github.com/satisfy)** (satisfy & Team)  
  *Inspiration for viticulture, grape harvesting, fermentation, and tavern brewing chains.*

---

## Legal Notices & Third-Party Licenses

For formal legal copyright notices, SPDX license identifiers, and complete redistributable third-party license texts, please consult **[NOTICES.md](NOTICES.md)**.
