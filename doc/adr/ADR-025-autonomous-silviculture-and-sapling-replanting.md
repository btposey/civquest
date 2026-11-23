# ADR-025: Autonomous Silviculture, Tree Felling, and Sapling Replanting

## Status
Accepted

## Context
In Minecraft survival, partially felled trees ("floating trees") cause visual clutter, impede settlement development, and fail to maximize resource yields. The initial CivQuest tree harvesting routine (`NewHarvestTreeTask`) strictly checked a single vertical coordinate column $(t_x, t_z)$ within $Y \in [Y_{\text{ref}}-1, Y_{\text{ref}}+5]$. This caused several critical issues:
1. **Branch Neglect**: Large oaks and branching trees frequently generate log blocks at $(t_x \pm 1, t_z \pm 1)$ and varying elevations. Because the scanner strictly enforced $(X == t_x \land Z == t_z)$, branch logs were ignored, leaving mutilated tree crowns.
2. **Deforestation & Depletion**: Agents felled timber stands in an expanding radius without replanting, causing progressive deforestation around the settlement and forcing agents into longer, perilous treks to gather basic wood.
3. **Floating Tree Hazards**: Remaining floating tree logs prevent natural leaf decay, preventing saplings, apples, and sticks from dropping to the ground for collection.

Human players systematically clear contiguous trunk and branch logs within reach and immediately plant a sapling in the vacated root space to maintain local renewable timber stands.

## Decision

### 1. Contiguous Cluster Tree Scanning (Trunk & Branches)
We update `scanTreeColumnLogs` and `NewHarvestTreeTask` to detect contiguous log clusters rather than a single 1D vertical column:
- **Spatial Scope**: The scanner evaluates candidate logs within horizontal Manhattan distance $|dx| \le 2, |dz| \le 2$ from the root anchor $(t_x, t_z)$ across vertical range $Y \in [Y_{\text{anchor}}-1, Y_{\text{anchor}}+16]$.
- **Tree Height Classification & Felling Strategy**:
  - **Standard Trees ($H \le 4$ blocks above ground)**: The agent fells logs bottom-up from ground level within normal reach ($\le 4.5\text{ m}$).
  - **Tall Trees ($H \ge 5$ blocks above ground / apex beyond ground reach)**:
    - Cutting bottom-up leaves high logs and branches stranded out of reach ("floating crowns").
    - For tall trees, the agent executes **Top-Down Felling**:
      1. **Ascent Phase**: The agent ascends into the tree crown utilizing natural leaf blocks, trunk notches, or temporary 1-block scaffolding.
      2. **Apex & Canopy Clearance**: Once atop the tree canopy or at maximum accessible elevation, the agent harvests the topmost logs and lateral branches ($Y_{\text{max}} \to Y_{\text{current}}$).
      3. **Descending Trunk Felling**: The agent cuts downward through the central trunk column beneath its position, felling each trunk log layer as it descends safely back to ground level.
- **Harvest Completion**: The tree task only completes when zero reachable logs remain within the canopy cluster.

### 2. Autonomous Sapling Replanting (Silviculture Routine)
Upon successfully clearing all reachable logs from a tree site:
1. **Inventory Inspection**: The agent queries its inventory for any sapling item (`*_sapling` or `mangrove_propagule`).
2. **Root Soil Verification**: The agent identifies the ground block beneath the original trunk base $(t_x, Y_{\text{soil}}, t_z)$ (must be `dirt`, `grass_block`, `coarse_dirt`, `podzol`, or `farmland`) with air immediately above $(t_x, Y_{\text{soil}}+1, t_z)$.
3. **Placement Intent**: If a sapling is present and the soil position is clear, the agent emits an outbound `InteractBlockIntent`:
   - `InteractionType`: `PLACE_BLOCK`
   - `BlockOrItemId`: Identified sapling ID (e.g. `minecraft:oak_sapling`)
   - `Pos`: $(t_x, Y_{\text{soil}}+1, t_z)$
4. **Spatial Memory Retention**: The replanted sapling is registered into `SpatialIndex` with category `CROP` / `POICategoryCrop` so the agent recognizes growing timber stands.

### 3. Leaf Decay & Drop Yields
By completely removing the trunk and branch logs, Minecraft's leaf decay mechanics trigger naturally. The agent's existing dropped item collection routine (`findNearestDroppedWoodItem` / `collect_items`) will opportunistically harvest fallen saplings, sticks, and apples once leaves decay.

## Consequences

### Positive
- Eliminates unsightly and obstructive floating tree canopies.
- Guarantees sustainable, perpetual timber supplies in the immediate perimeter of the settlement without long-range deforestation treks.
- Naturally accelerates leaf decay to yield saplings and food supplies (apples).

### Negative
- Harvesting large branching oaks takes additional ticks and tool durability.
- If trees spawn in dense canopies with overlapping leaves/branches, cluster scanning must remain bounded ($|dx| \le 2, |dz| \le 2$) to prevent runaway tree felling cascades across entire forests.
