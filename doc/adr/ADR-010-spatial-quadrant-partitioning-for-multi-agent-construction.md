# ADR-010: Spatial Quadrant Partitioning for Multi-Agent Construction

## Status
Accepted

## Context
When multiple autonomous agents collaboratively construct complex Tier 2 architectural blueprints (such as Taverns, Granaries, or Markets), spatial conflicts arise:
- Agents can navigate to the same voxel simultaneously, blocking each other's path.
- Builders placing scaffolding or walls out of order can inadvertently entomb or trap co-workers inside hollow structures.
- Uncoordinated block placement leads to race conditions, invalid block state attachments (e.g. stairs facing incorrect orientations), and fallen falling blocks (sand/gravel).

## Decision
We enforce a **Spatial Quadrant Partitioning and Strict Bottom-Up Order**:
1. **Quadrant Subdivision**:
   - The Architect divides the building's bounding box into four non-overlapping quadrants: North-West ($NW$), North-East ($NE$), South-West ($SW$), and South-East ($SE$).
   - Individual worker agents are exclusively assigned to a single quadrant at any given time.
2. **Bottom-Up ($Y_{\text{min}} \to Y_{\text{max}}$) Execution**:
   - Blocks within a quadrant must be placed layer by layer from the lowest elevation upward.
   - Temporary scaffolds must be designated in metadata and systematically disassembled from top to bottom upon completion.
3. **Bill of Materials (BOM) Gate**:
   - Construction cannot transition from `SURVEYING` to `BUILDING` until required resources in the `settlement_granary` ledger satisfy the foundation phase requirements.

## Consequences
### Positive
- Completely avoids physical agent collisions, entombment, and voxel race conditions.
- Enables linear scaling of construction speed as additional builder agents are allocated.

### Negative / Trade-offs
- Quadrant boundaries slightly restrict continuous architectural styling and require additional coordination overhead in Go.
