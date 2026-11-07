# CivQuest Bundled Third-Party Mod Registry

This document maintains the curated registry of approved third-party gameplay and visual mods bundled alongside CivQuest.

Per [ADR-002 (Minecraft & NeoForge Versioning Strategy)](file:///home/brian/Workspaces/btposey/civquest/doc/adr/ADR-002-minecraft-neoforge-version-and-branching-strategy.md), this registry is strictly aligned with repository branches:
- **`main`**: Minecraft **1.21.1** / NeoForge **21.1.265** (LTS Baseline).
- **`version/mc-*`**: Version-specific compatibility branches tracking upstream Minecraft/NeoForge updates.

---

## 1. Mod Registry Matrix

| Mod Name | Mod ID / Namespace | Primary Source / URLs | Target Engine (LTS) | CivQuest Autonomous Role & Integration |
|---|---|---|---|---|
| **Falling Trees** | `fallingtree` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/pandas-falling-trees) / [Modrinth](https://modrinth.com/mod/fallingtree) | MC 1.21.1 / NeoForge 21.1.x | **Lumberjack / Forestry AI**: When an agent breaks the trunk of a tree with an axe, the entire canopy falls realistically. Prevents floating tree artifacts and simplifies settler woodcutting pathing. |
| **Farmer's Delight** | `farmersdelight` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/farmers-delight) / [Modrinth](https://modrinth.com/mod/farmers-delight) | MC 1.21.1 / NeoForge 21.1.x | **Agrarian / Culinary Tier**: Rich soil, organic compost, cutting boards, and cooking pots. Directly leveraged by CivQuest `FARMER` and `CHEF` roles for dynamic crop rotation, food stockpiling, and player sharecropping (`cq-9ff.3`). |
| **[Let's Do] Bakery** | `bakery` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-bakery) / [Modrinth](https://modrinth.com/mod/lets-do-bakery) | MC 1.21.1 / NeoForge 21.1.x | **Baker / Grain Mill AI**: Brick ovens, flour milling, bread loaves, pies, and pastries. Expands settlement grain surplus processing into high-saturation bakery trade goods. |
| **[Let's Do] Brewery** | `brewery` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-brewery) / [Modrinth](https://modrinth.com/mod/lets-do-brewery) | MC 1.21.1 / NeoForge 21.1.x | **Tavernkeeper / Brewer**: Brewing kegs, hops cultivation, beer, ale, and tavern furniture. Integrates with the `TAVERN` zone social hub (`cq-9ff.5`) and `CIDER_BREWER` hobbies. |
| **[Let's Do] Vinery** | `vinery` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-vinery) / [Modrinth](https://modrinth.com/mod/lets-do-vinery) | MC 1.21.1 / NeoForge 21.1.x | **Vintner / Vineyard AI**: Grape trellises, apple wine pressing, fermentation barrels, and cellar aging racks. Emergent luxury barter goods for dynastic caravan trading (`cq-9ff.4`). |
| **[Let's Do] HerbalBrews** | `herbalbrews` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-herbalbrews) / [Modrinth](https://modrinth.com/mod/lets-do-herbalbrews) | MC 1.21.1 / NeoForge 21.1.x | **Herbalist / Healer**: Tea kettles, dried herbs, and soothing teas providing status buffs (regeneration, haste, health boost) consumed by settlers during panic or recovery. |
| **[Let's Do] Meadow** | `meadow` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-meadow) / [Modrinth](https://modrinth.com/mod/lets-do-meadow) | MC 1.21.1 / NeoForge 21.1.x | **Highland Shepherd / Cheesemaker**: Water buffalo, highland cattle, milk curdling, and wheel cheese aging. Expands dairy farming and animal husbandry roles. |
| **[Let's Do] Candlelight** | `candlelight` | [CurseForge](https://www.curseforge.com/minecraft/mc-mods/lets-do-candlelight) / [Modrinth](https://modrinth.com/mod/lets-do-candlelight) | MC 1.21.1 / NeoForge 21.1.x | **Town Architect / Tavern Decor**: Dinner tables, candelabras, hearth cooking, and banquet feasts for tavern gathering hours (Ticks 11000 - 24000). |

---

## 2. Complementary & Compatible Mod Additions

### Farmer's Delight Ecosystem
- **Nether's Delight (`nethersdelight`)**: Adds Strider moss and hoglin culinary options for deep mining or Nether expeditions.
- **Ocean's Delight (`oceansdelight`)**: Coastal fishing settlement integration (kelp rolls, fish stew).
- **Cultural Delights (`culturaldelights`)**: Rice, avocados, cucumbers, and multi-cultural culinary recipes.

### Utility & Compatibility Libraries
- **Architectury API (`architectury`)**: Multi-platform dependency required by several `[Let's Do]` modules.
- **Cloth Config (`cloth_config`)**: Client/server configuration library used by cosmetic and culinary additions.

---

## 3. Recipe DAG Export & Dynamic Compatibility

To prevent brittleness, hardcoded item IDs, or recipe breakage when modpacks change:
1. **Dynamic Recipe Graph Export (`ExportRecipeGraph`)**:
   - The embedded Netty gRPC server in `mod-neoforge` exposes `rpc ExportRecipeGraph(ExportRecipeRequest) returns (ExportRecipeResponse)`.
   - On server startup or agent request, the mod queries Minecraft's `RecipeManager`, discovers all registered modded recipes (including cooking pot recipes, cutting board recipes, brewery keg recipes, and vinery fermentation), and streams the complete Directed Acyclic Graph (DAG) over gRPC.
2. **Go In-Memory Crafting Planner**:
   - The Go orchestrator stores the recipe graph in `orchestrator-go/internal/crafting` and SQLite.
   - When an agent decides to cook `farmersdelight:baked_cod_stew` or brew `brewery:beer`, System 1 evaluates component availability without needing any hardcoded Java changes.

---

## 4. Branching & Maintenance Discipline

1. **Per-Branch Mod Dependency Verification**:
   - When cutting a new version branch (e.g., `version/mc-1.22`), update `doc/third-party-modpacks.md` with the validated file hashes, mod versions, and CurseForge/Modrinth URLs for that target Minecraft and NeoForge release.
2. **Non-Intrusive Integration**:
   - CivQuest treats bundled third-party mods as data-driven dependencies. No hardcoded class references to third-party mods exist in `mod-neoforge/src/main/java`; all cross-mod interaction occurs via vanilla tags (`#c:crops`, `#c:tools/axes`), standard BlockPos/Item APIs, and the dynamic recipe registry.
