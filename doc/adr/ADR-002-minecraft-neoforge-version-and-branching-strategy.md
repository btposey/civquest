# ADR-002: Minecraft & NeoForge Versioning and Multi-Branch Release Strategy

## Status
Accepted

## Context
The Minecraft modding ecosystem experiences rapid upstream API churn across major Minecraft releases (e.g., 1.21.1, 1.21.4, and future versions) and corresponding NeoForge builds (21.1.x, 21.4.x, etc.). CivQuest depends critically on third-party mod integrations:
- **Baritone**: For automated voxel navigation, mining, and pathing.
- **Farmer's Delight & Vinery**: For emergent agricultural DAGs, cooking pots, and fermentation recipes.

While newer bleeding-edge versions of NeoForge exist, critical dependencies like Baritone and companion culinary mods have their broadest and most stable ecosystem support on Minecraft 1.21.1 (NeoForge 21.1.x, specifically release 21.1.265+). Choosing bleeding-edge versions without ecosystem stability breaks essential functionality.

Furthermore, maintaining a single `main` trunk without version-specific branches would impede backporting and force breaking changes whenever Mojang updates game protocols.

## Decision
1. **Target Baseline**: Standardize the primary production baseline on **Minecraft 1.21.1** powered by **NeoForge 21.1.265** (Java 21).
2. **Multi-Branch Version Strategy**:
   - `main` serves as the integration trunk tracking the latest stable LTS baseline (1.21.1).
   - Dedicated long-lived branches (e.g., `release/mc-1.21.1`, `version/mc-1.21.4`, `experimental/mc-latest`) will track specific Minecraft/NeoForge version pairs.
   - New features developed on leading/bleeding-edge branches must be cleanly backported to active current-N version branches.
   - Core behavioral logic in `orchestrator-go` remains decoupled from Minecraft version idiosyncrasies through the version-stable gRPC boundary.

## Consequences
### Positive
- Guarantees immediate out-of-the-box compatibility with Baritone and ecosystem mods on 1.21.1.
- Provides a clear lifecycle for forward-porting and backporting mod patches across Minecraft version transitions.
- Isolates Go agent logic from Minecraft protocol breakage.

### Negative / Trade-offs
- Git branch management overhead for cherry-picking and backporting mod fixes across version branches.
- Requires dual or multi-matrix CI pipelines for validating different Minecraft/NeoForge target versions.
