# ADR-027: Clan Settlement Genesis and Pioneer Explorer Nucleation

## Status
Accepted

## Context
In early versions of CivQuest, newly spawned agents could be assigned specialized professions (such as Farmer, Miner, Blacksmith, Crafter, or Builder) at initial process creation or generation. 

This premature specialization created immediate structural failures in uncolonized wilderness:
1. **Utility Starvation**: Specialized agents scored highest utility for specialized tasks (e.g. harvesting wheat, tending livestock, forging tools at an anvil) that had zero available infrastructure or resources in the wilderness.
2. **Scattered Clan Dynamics**: Instead of cooperating to survey terrain, collect basic wood/stone, and establish an initial shelter, agents scattered or became idle attempting to satisfy unattainable professional goals.
3. **Absence of a Cultural & Spatial Anchor**: A clan cannot coordinate communal cartography, desire paths, armories, or shift work without a consecrated communal hub (a settlement POI / hearth).

Human settlements and historical pioneer expeditions do not begin with differentiated urban guilds. In wilderness colonization, all founders are pioneers, foragers, scouts, and bootstrappers until a defensible hearth and communal base of operations are established.

## Decision

### 1. Pioneer Genesis Invariant
Every newly created, spawned, or unattached agent must strictly initialize with:
- `Profession`: `EXPLORER` (or primitive pioneer/scout archetype).
- `Lifecycle Stage`: `BOOTSTRAP`.

No agent may start as a specialized professional (Farmer, Miner, Crafter, Builder, Smith, etc.) in an unestablished wilderness territory.

### 2. Settlement Genesis Milestone
A clan transitions from the "Pioneer Phase" to the "Settled Phase" only when a consecrated **Settlement POI** is established and recorded in the communal spatial ledger (`SpatialIndex` category `POICategorySettlement` or `POICategoryHabitation` hearth).
Criteria for Settlement Genesis:
- Discovery or construction of a recognized communal hearth (e.g., campfire, central dugout/shelter, or town marker).
- Minimal communal sustenance and tool sufficiency achieved by the pioneer party.
- Formal registration of the settlement coordinates $(X, Y, Z)$ in the clan SQLite spatial memory.

### 3. Progressive Nucleation into Specialized Roles
Only after the communal settlement POI is verified and registered by the clan:
- Pioneer agents evaluate clan surplus and environmental assets (nearby arable land, exposed mineral veins, dense timber, water bodies).
- Agents transition dynamically from `EXPLORER` into specialized professions (Farmer, Miner, Forester, Crafter, Builder, Rancher, Guard) according to clan governance, individual affinity traits, and resource availability.
- Any newly arrived settler or offspring born after settlement genesis may apprentice into existing professions if a settlement anchor exists.

## Consequences

### Positive
- Prevents specialized utility starvation in raw wilderness.
- Unifies pioneer party behavior around survival, exploration, and settlement founding.
- Establishes a natural, organic progression from wilderness survival to urban civilization.

### Negative
- Delays specialized production loops until the pioneer exploration and settlement founding phase completes.
- Requires robust settlement POI detection and clan consensus logic.
