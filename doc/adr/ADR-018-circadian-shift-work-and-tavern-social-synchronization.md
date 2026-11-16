# ADR-018: Circadian Shift Work and Tavern Social Synchronization

## Status
Accepted

## Context
In vanilla Minecraft villages, all entities sleep simultaneously during the night and wander during the day. In CivQuest, settlements face dynamic hostile mob spawns, perimeter incursions, and complex vocational divisions of labor. If all settlers sleep at night, settlements are defenseless against monster breaches; conversely, if guards patrol 24/7 without rest, life simulation realism and circadian rhythm are violated.

Furthermore, autonomous agents require a shared spatio-temporal convergence window where all members of the clan congregate to exchange rumors, align political opinions (ADR-016), negotiate trades, and coordinate settlement goals. Without synchronized gathering times, night workers and day workers would live entirely segregated lives with fractured communal consciousness.

## Decision

### 1. Work Shift Architecture
We introduce formal shift work parameterization in `AgentIdentityConfig` (`Shift` field: `"1st"`, `"3rd"`, `"day"`, `"night"`):
- **1st Shift (Day Workers)**: Default for Farmers, Builders, Miners, Artisans, Woodcutters, and Settlers.
- **3rd Shift (Night Watch / Sentinels)**: Default for Guards, Sentinels, Watchmen, Patrols, and Warriors.

### 2. Circadian Schedule Partitioning ($t \pmod{24000}$)

| Day Cycle Window | 1st Shift (Day Workers) | 3rd Shift (Night Watch) | Operational Intent |
|---|---|---|---|
| **$23500 - 1000$ (Dawn Break)** | `RoutineDawnPrep` | `RoutineDawnPrep` | Dawn awakening & morning guard handover |
| **$1000 - 2000$ (Morning Transition)** | `RoutinePrimaryDuty` | `RoutineArmoryMaintenance` / `RoutineDawnPrep` | 1st shift works; 3rd shift performs off-watch chores |
| **$2000 - 8000$ (Midday)** | `RoutinePrimaryDuty` | `RoutineSleep` | 1st shift works; 3rd shift sleeps during safe sunny hours |
| **$8000 - 10000$ (Afternoon)** | `RoutineSecondaryHobby` | `RoutineSleep` | 1st shift personal pursuits; 3rd shift completes sleep |
| **$10000 - 11000$ (Pre-Dusk Hour)** | `RoutineSecondaryHobby` | `RoutineArmoryMaintenance` / `RoutineSecondaryHobby` | 3rd shift wakes and prepares gear for watch |
| **$11000 - 13000$ (Late Day / Dusk)** | **`RoutineTavernSocial`** | **`RoutineTavernSocial`** | **ALL shifts awake; converge at Tavern** |
| **$13000 - 23500$ (Night to Dawn)** | `RoutineSleep` | `RoutineCivicPatrol` | 1st shift sleeps in bed; 3rd shift defends perimeter |

### 3. Communal Social Consciousness Synchronization
The late afternoon/dusk interval ($11000 \le \tau < 13000$) is strictly designated as communal tavern time:
- **Universal Congregration**: Regardless of shift, all non-emergency settlers navigate to the village tavern, communal hall, or central campfire.
- **Shift Briefings & Intelligence Handoff**: Day laborers share resource discoveries from the wild; night watchmen brief the clan on perimeter weaknesses and monster sightings.
- **Democratic Alignment**: Serves as the natural assembly ground for System 2 town hall debates, legislative voting (ADR-016), and rumor propagation.
- **Companion Override**: If accompanied by a human player companion, `RoutineCamp` safely overrides tavern return while honoring the sleep-wake biological cycle.

## Consequences

### Positive
- **Continuous 24-Hour Settlement Defense**: Perimeter walls and watchtowers are actively manned throughout the deep night ($13000 - 23500$) when monster spawns are highest.
- **Unified Social Fabric**: The tavern window ensures that night watchmen are not isolated from settlement governance, marriage/reproduction dynamics, or town debates.
- **Realistic Biological Cycles**: Night guards sleep during the day when zombies and skeletons burn in the sunlight, conserving energy and supplies.

### Negative / Trade-offs
- Bed capacity contention: Settlements with limited beds must avoid bed-sharing collisions between 1st and 3rd shift workers. Addressed by discrete `assigned_bed` tracking in SQLite.
