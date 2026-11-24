# ADR-029: Data-Driven Runtime Recipe Resolution & Dynamic Orchestrator Crafting Catalog

## Status
Accepted

## Context
In previous iterations of the CivQuest mod (`mod-neoforge`), item crafting in `IntentActuator.actuateCraftItem()` was implemented using hardcoded imperative `switch(recipe)` logic with manual string pattern matching (e.g. `id.endsWith("_log")`). 

This architecture caused critical simulation breakdowns:
1. **Recipe Fidelity & Exploits**: Hand-rolled checks introduced unphysical recipe shortcuts, such as granting a `minecraft:campfire` in exchange for a single log or stick without coal/charcoal, or crafting wooden weapons without sticks or crafting tables.
2. **Modpack & Datapack Incompatibility**: Any custom datapack or NeoForge mod modifying vanilla recipes or introducing new items/materials was ignored, breaking polyglot extensibility.
3. **Loss of Vanilla Crafting Semantics**: Minecraft natively handles remainder items (e.g. buckets returned when crafting cake), output counts, durability, and NBT tags. Imperative code cannot maintain parity with these semantics.
4. **Agent Deliberation Decoupling**: The external Go orchestrator possessed no mechanism to discover which recipes were loaded into the running Minecraft world, forcing Go planners to hardcode assumptions about item requirements.

## Decision

### 1. Authoritative Runtime `RecipeManager` Ground Truth
The Minecraft server's `RecipeManager` (`server.getRecipeManager()`) is the sole authoritative registry for item crafting:
- When handling `CraftItemIntent(recipe_id, workstation_pos, batch_count)`, `IntentActuator` resolves recipes directly via `RecipeManager.byKey(ResourceLocation)`.
- Imperative item recipe switches in `IntentActuator` are eliminated.

### 2. Physical Workstation & Grid Dimension Validation
Crafting recipes are strictly partitioned by dimensional requirements:
- **2x2 Pocket Crafting**: Recipes where `recipe.canCraftInDimensions(2, 2)` evaluates to `true` (e.g. planks, sticks, crafting tables, torches) may be crafted anywhere directly from the agent's inventory.
- **3x3 Table Crafting**: Recipes requiring dimensions $> 2 \times 2$ (e.g. campfires, tools, armor, chests, doors) strictly require an active, physical `minecraft:crafting_table` block within reaching distance ($\le 4.5\text{ m}$) of the agent avatar.

### 3. Native Ingredient Matching (`StackedContents`)
- Agent inventory verification uses Minecraft's native `StackedContents` bitwise matching algorithm or exact ingredient requirement testing against `recipe.getIngredients()`.
- Crafting fails cleanly if the agent lacks the exact quantities and tag-compatible items specified in the recipe.
- Ingredient consumption is atomic: matching items are decremented, output is assembled via `recipe.assemble(...)`, and remainder container items from `recipe.getRemainingItems(...)` are deposited back into the avatar's inventory.

### 4. Dynamic Orchestrator Recipe Catalog
- The server mod extracts loaded crafting recipes and exports them dynamically to the Go orchestrator via gRPC telemetry/metadata or initial agent handshake.
- The Go orchestrator builds an in-memory **Recipe Graph** from this catalog, dynamically resolving prerequisite trees (e.g., Raw Logs $\to$ Planks $\to$ Sticks + Coal $\to$ Campfire) rather than executing hardcoded imperative shortcuts.

## Consequences

### Positive
- Strict, 100% adherence to authentic Minecraft and modpack crafting rules.
- Eliminates recipe shortcuts (e.g. campfires now strictly require 3 logs, 3 sticks, and 1 coal/charcoal at a crafting table).
- Full compatibility with modded items and customized datapack recipes without recompiling the mod.
- Full support for remainder container items (empty buckets, bottles).

### Negative
- Go orchestrator agents cannot craft complex items (like campfires) until prerequisite raw materials (coal, sticks, crafting table) are physically procured or alternative recipes are unlocked.
- Slight CPU overhead during initial recipe registry indexing, mitigated by caching recipe lookups by `ResourceLocation`.
