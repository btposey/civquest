# ADR-021: Blueprint Studio SPA, Token-Efficient Voxel Schematics, and Autonomous Construction Pipeline

## Status
Accepted

## Context
In CivQuest, settlements evolve through autonomous construction of civic and domestic structures (ADR-010). However, early implementations relied on rigid, hand-coded Go voxel arrays (such as `cabin.go`), preventing human operators from designing custom buildings, adding new architectural styles, or adapting structures to diverse terrain.

Furthermore, players need an intuitive, tactile authoring tool to design multi-story structures without wrestling with external modding utilities or heavy web frameworks. When blueprints are authored, autonomous agents must be able to:
1. Discover and categorize designs by civic purpose (`SHELTER`, `HOME`, `LIBRARY`, `TOWNHALL`, `TAVERN`, `ARMORY`, `FARM`).
2. Reason about material feasibility by calculating a Bill of Materials (BOM) against communal granary reserves.
3. Derive architectural variations using System 2 LLMs (e.g., adding an armory annex to a tavern).
4. Autonomously construct the physical structure bottom-up using ADR-010 Spatial Quadrant Partitioning.

## Decision

### 1. Zero-Framework Embedded SPA Architecture
The Blueprint Studio is delivered as a zero-dependency Single-Page Application (SPA) built purely with **vanilla HTML, CSS, and JavaScript** (no React, Vue, NPM, or Node.js runtime).
- **Embedded Distribution**: Static assets are bundled directly into the `cq` binary using Go 1.16+ `embed.FS`.
- **Command & Auto-Launch**: Executed via `cq blueprint studio [--port=8765]`. The CLI spins up a local loopback HTTP server and automatically opens the host default browser via `xdg-open`.
- **REST API**: Serves `/api/blueprints`, `/api/palette` (standard Minecraft blocks, textures, and hex colors), and `/api/blueprints/:id/validate`.

### 2. Dual-Pane Coordinate Model: 2D Layer Grid & Isometric Canvas
Minecraft defines **$Y$ as vertical elevation** and **$(X, Z)$ as the horizontal ground plane**:
- **2D Layer Grid Editor**:
  - Displays the $(X, Z)$ horizontal plane while looking down from above.
  - An elevation slider steps through vertical layers $Y \in [0, H-1]$.
  - **Onion-Skinning**: Layer $Y-1$ (underlying foundation/walls) is rendered as a semi-transparent, cyan/grey blueprint outline beneath the active layer, providing visual alignment for load-bearing walls, stairwells, and doorways.
- **HTML5 Canvas 2.5D Isometric Renderer**:
  - Real-time dimetric projection ($30^\circ$ isometric grid, 2:1 aspect ratio) updating instantaneously as blocks are placed or erased.
  - **Interactive X-Ray Wall Ghosting**: When the user moves their mouse cursor across the face of the isometric view, front-facing exterior walls (camera-adjacent South and East facades) drop to $15\%$ opacity with wireframe contours, revealing interior rooms, staircases, chests, and beds. When the mouse leaves the canvas, full solid exterior occlusion is restored.

### 3. Token-Efficient ASCII-Slice Blueprint Schema
To enable System 2 LLMs to inspect, reason about, and mutate blueprints without context exhaustion, blueprints are persisted in `$XDG_DATA_HOME/civquest/blueprints/` using a **Semantic Layer-Slice Format** ($<400$ tokens per building):

```json
{
  "id": "pioneer_tavern_v1",
  "name": "Timber Hearth Tavern",
  "purpose": "TAVERN",
  "tags": ["social", "nightlife", "communal"],
  "dimensions": {"width": 9, "depth": 9, "height": 5},
  "author": "operator",
  "bom": {
    "minecraft:oak_planks": 86,
    "minecraft:oak_log": 24,
    "minecraft:glass_pane": 12,
    "minecraft:barrel": 4,
    "minecraft:crafting_table": 1
  },
  "layers": [
    {
      "y": 0,
      "name": "foundation",
      "grid": [
        "LLLLLLLLL",
        "LPPPPPPPL",
        "LPPPPPPPL",
        "LLLLDLLLL"
      ]
    }
  ]
}
```

### 4. System 2 Deliberative Reasoning & Derivative Generation
- **`BlueprintEvaluator` (ADR-020)**: Compares the structure's `bom` against the settlement's `civic_registry.db` granary reserves, emitting facts: `blueprint.affordable` and `blueprint.missing_materials`.
- **Purpose Selection**: Agents evaluate immediate civic needs (e.g., lack of communal space $\to$ query library for `purpose == "TAVERN"`).
- **LLM Mutation & Derivatives**: System 2 prompts receive the compact ASCII layer representation, allowing the LLM to generate architectural adaptations (e.g., adding an armory wing or expanding living quarters) while guaranteeing geometric validity.

### 5. ADR-010 Autonomous Construction Pipeline
Blueprints compile directly into the **Spatial Quadrant Partitioning** pipeline:
- Partitioned into $NW, NE, SW, SE$ quadrant work queues.
- Scheduled bottom-up ($Y_{\text{min}} \to Y_{\text{max}}$) via `ConstructionSkill` emitting `AgentIntent_PlaceBlock` so builders construct foundations before upper floors and never entrap themselves.

## Consequences

### Positive
- **Human-in-the-Loop Creative Empowerment**: Players design custom architectural styles in a fluid, tactile web interface that immediately populates the settlement's construction library.
- **Zero Runtime Dependencies**: The entire studio runs out-of-the-box from a single statically linked Go binary.
- **Seamless Dual-Tier Synergy**: Provides a clean bridge between human creativity, System 2 LLM strategic selection/derivation, and System 1 zero-token quadrant physical construction.

### Negative / Trade-offs
- Browser canvas performance: Very large structures ($>64 \times 64 \times 32$) can experience rendering latency on software-rasterized canvas. Mitigated by chunked dirty-bounding-box redraws and layer slicing.
