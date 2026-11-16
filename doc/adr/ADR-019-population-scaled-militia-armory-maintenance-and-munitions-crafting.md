# ADR-019: Population-Scaled Militia Armory Maintenance and Munitions Crafting

## Status
Accepted

## Context
Settlements in CivQuest must be resilient against sudden, high-threat monster sieges, zombie hordes, and wandering raids. While professional sentinels (3rd shift guards) provide frontline perimeter security, a small standing guard cannot repel catastrophic breaches alone. During serious incursions, the entire civilian populace (farmers, builders, artisans) must be mobilized as an emergency citizen militia (ADR-016).

Previously, agent crafting and inventory checks relied on arbitrary static thresholds (e.g. keeping 1 sword or 4 torches in inventory). This left settlements critically under-equipped for defense. Furthermore, guards lacked structured chore duties during their off-watch waking hours, leading to unproductive idling prior to the tavern social hour.

## Decision

### 1. Population-Scaled Militia Sufficiency Model
We define defensive material sufficiency not by arbitrary static numbers, but by the formula required to fully outfit every individual in the settlement ($N$ citizens) for immediate combat:

For a settlement of $N$ citizens (tracked dynamically via `CivicRegistryDB`):
$$\text{Helmets} \ge N \qquad \text{Chestplates} \ge N \qquad \text{Leggings} \ge N \qquad \text{Boots} \ge N$$
$$\text{Total Complete Armor Sets} \ge N \quad (4N \text{ total armor pieces})$$
$$\text{Swords} \ge N \qquad \text{Bows} \ge N \qquad \text{Arrows} \ge 10 \times N$$
$$\text{Dynamite / TNT Munitions Reserve} \ge \max(4, N)$$

`ScanArmoryStock` pools both the agent's carried inventory and the settlement’s communal granary/armory stockpile (`CivicRegistryDB`).

### 2. Routine Allocation & Off-Watch Chores
We introduce **`RoutineArmoryMaintenance`** (`ARMORY_MAINTENANCE`) in the circadian scheduler for 3rd shift guards:
- **Off-Watch Waking Windows**:
  - **$10000 - 11000$ (Late Afternoon Waking Hour)**: Pre-tavern waking window.
  - **$1000 - 2000$ (Post-Dawn Transition Hour)**: Morning off-watch window following dawn guard handover.
- **Tavern Sacredness**: At tick $11000$, all off-watch chores yield unconditionally to `RoutineTavernSocial` to maintain synchronized communal consciousness (ADR-018).

### 3. Procedural Crafting & Stand-Down Reflexes
During `RoutineArmoryMaintenance`:
1. **Deficit Evaluation**: The guard inspects carried and communal armory stock. If supplies are below the $N$-scaled quota:
   - Evaluates raw materials: Sticks + Feathers + Flint $\to$ `minecraft:arrow`; Gunpowder + Sand $\to$ `minecraft:tnt`; Iron/Cobble + Sticks $\to$ `minecraft:iron_sword`, `minecraft:shield`; Iron Ingots / Leather $\to$ `minecraft:iron_chestplate`, etc.
   - Navigates toward the nearest crafting table workstation ($> 4.5\text{m}$), places a workstation if none exists nearby, or crafts adjacent to the workstation ($\le 4.5\text{m}$) respecting a 20-tick cooldown.
2. **Automatic Stand-Down**: Once all quotas are satisfied (or raw materials are completely exhausted):
   - The guard stands down from chore duty and transitions to **`RoutineSecondaryHobby`** (weapon honing, physical conditioning, personal crafting) until tavern time at tick $11000$.

## Consequences

### Positive
- **Guaranteed Incursion Readiness**: The settlement maintains an armory capable of transforming all civilian settlers into an armed, armored militia instantly upon an alarm bell or breach.
- **Zero Idle Time**: Guard waking hours are purposefully structured with civic defense maintenance chores rather than aimless wander loops.
- **Dynamic Settlement Scaling**: As the settlement grows from 3 pioneers to 20 citizens, munitions crafting targets expand automatically without manual configuration.

### Negative / Trade-offs
- Resource consumption: Intensive arrow and TNT crafting can deplete settlement sulfur, sand, and iron reserves. Mitigated by checking raw material availability before initiating workstation navigation.
